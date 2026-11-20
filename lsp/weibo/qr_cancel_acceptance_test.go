package weibo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

// Use real HTTP QR image, polling and ticket exchange rather than substituting
// the public login helper. Cancellation must close both HTTP and result channel.
func TestPublicWeiboQRCancellationDrainsNetworkWithoutPublishing(t *testing.T) {
	for _, stage := range []string{"initial_image", "ticket_exchange"} {
		t.Run(stage, func(t *testing.T) {
			restoreWeiboRuntimeForHTTPTest(t)
			oldImageURL, oldCheckURL, oldLoginURL := qrImageURL, qrCheckURL, qrLoginURL
			t.Cleanup(func() { qrImageURL, qrCheckURL, qrLoginURL = oldImageURL, oldCheckURL, oldLoginURL })
			const initial = "weibo:\n  mode: login\n  sub: old-sub\n"
			path := filepath.Join(t.TempDir(), "application.yaml")
			if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			c := &config.Config{Viper: viper.New()}
			c.SetConfigFile(path)
			if err := c.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			config.GlobalConfig = c
			setRuntimeSUB("old-sub")
			var prepared atomic.Int32
			c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
				prepared.Add(1)
				return nil, nil, nil
			})
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (stage == "initial_image" && r.URL.Path == "/image") || r.URL.Path == "/login" {
					close(entered)
					select {
					case <-r.Context().Done():
						close(canceled)
					case <-release:
					}
					return
				}
				switch r.URL.Path {
				case "/image":
					fmt.Fprintf(w, `callback({"retcode":20000000,"data":{"qrid":"local-id","image":"http://%s/png"}})`, r.Host)
				case "/png":
					_, _ = w.Write([]byte{1, 2, 3})
				case "/check":
					fmt.Fprint(w, `{"retcode":20000000,"data":{"alt":"ALT-local-ticket"}}`)
				default:
					t.Errorf("unexpected QR request path: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			defer unblock()
			qrImageURL, qrCheckURL, qrLoginURL = server.URL+"/image", server.URL+"/check", server.URL+"/login"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				results, err := RunQRLoginForQQContext(ctx, 10*time.Second)
				if err != nil {
					finished <- err
					return
				}
				for result := range results {
					if result.PersistSuccess || result.RuntimeLoaded || result.Sub != "" {
						finished <- fmt.Errorf("canceled QR emitted successful credentials")
						return
					}
				}
				finished <- nil
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("QR did not reach the HTTP stage under test")
			}
			cancel()
			select {
			case <-canceled:
			case <-time.After(2 * time.Second):
				t.Fatal("canceling public QR did not cancel its HTTP request")
			}
			select {
			case err := <-finished:
				if stage == "ticket_exchange" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("canceled QR result channel or initial helper remained blocked")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, []byte(initial)) || c.Status().Revision != 1 || c.GetString("weibo.sub") != "old-sub" || getRuntimeSUB() != "old-sub" || prepared.Load() != 0 {
				t.Fatal("canceled QR mutated disk, accepted config or runtime session")
			}
		})
	}
}

func TestWeiboCanceledLoginCannotPersistAfterCredentialPrepare(t *testing.T) {
	restoreWeiboRuntimeForHTTPTest(t)
	const initial = "weibo:\n  mode: guest\n  sub: old-sub\n"
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig = c
	setRuntimeSUB("old-sub")
	prepared, resume := make(chan struct{}), make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	defer release()
	committed, aborted := 0, 0
	c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		if next.GetString("weibo.mode") != "login" || next.GetString("weibo.sub") != "new-sub" {
			t.Error("Weibo prepare observed a partial mode/cookie pair")
		}
		close(prepared)
		<-resume
		return func() { committed++ }, func() { aborted++ }, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- writeLoginConfigContext(ctx, "new-sub") }()
	select {
	case <-prepared:
	case <-time.After(3 * time.Second):
		t.Fatal("Weibo credentials did not enter transaction prepare")
	}
	cancel()
	release()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Weibo credentials were accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, []byte(initial)) || c.Status().Revision != 1 || c.GetString("weibo.mode") != "guest" || c.GetString("weibo.sub") != "old-sub" || getRuntimeSUB() != "old-sub" || committed != 0 || aborted != 1 {
		t.Fatal("canceled Weibo login persisted or published late credentials")
	}
}
