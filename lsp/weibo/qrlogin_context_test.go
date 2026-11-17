package weibo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQRLoginContextCancelsImageRequest(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	previous := qrImageURL
	qrImageURL = server.URL
	t.Cleanup(func() { qrImageURL = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := RunQRLoginForQQContext(ctx, time.Minute); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("image request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled image request returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("QR creation ignored cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("image HTTP request was not canceled")
	}
}

func TestQRLoginContextCancelsInFlightPollAndClosesResults(t *testing.T) {
	polled, stopped := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `callback({"retcode":20000000,"data":{"qrid":"test-qr","image":%q}})`, server.URL+"/png")
	})
	mux.HandleFunc("/png", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("png")) })
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		close(polled)
		<-r.Context().Done()
		close(stopped)
	})
	previousImage, previousCheck := qrImageURL, qrCheckURL
	qrImageURL, qrCheckURL = server.URL+"/image", server.URL+"/check"
	t.Cleanup(func() { qrImageURL, qrCheckURL = previousImage, previousCheck })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results, err := RunQRLoginForQQContext(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first := <-results; string(first.QRCodeImage) != "png" {
		t.Fatalf("missing initial QR: %+v", first)
	}
	select {
	case <-polled:
	case <-time.After(3 * time.Second):
		t.Fatal("poll request did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("in-flight HTTP poll did not observe cancellation")
	}
	limit := time.After(time.Second)
	for {
		select {
		case result, ok := <-results:
			if !ok {
				return
			}
			if result.PersistSuccess || result.Sub != "" {
				t.Fatalf("canceled QR reported committed credentials: %+v", result)
			}
		case <-limit:
			t.Fatal("canceled QR result producer did not exit")
		}
	}
}
