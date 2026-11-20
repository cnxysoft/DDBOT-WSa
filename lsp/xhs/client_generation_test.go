package xhs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestSignedRequestsUseOneCookieGeneration(t *testing.T) {
	client := NewClient(map[string]string{"a1": "A", "web_session": "A"})
	errors := make(chan error, 200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a1, e1 := r.Cookie("a1")
		session, e2 := r.Cookie("web_session")
		if e1 != nil || e2 != nil {
			errors <- fmt.Errorf("missing cookies")
		} else {
			raw, err := client.crypto.B64Encoder().Decode(r.Header.Get("x-s-common"))
			var common struct {
				X5 string `json:"x5"`
			}
			if err != nil {
				errors <- err
			} else if err = json.Unmarshal([]byte(raw), &common); err != nil {
				errors <- err
			} else if common.X5 != a1.Value || a1.Value != session.Value {
				errors <- fmt.Errorf("mixed generation: signed=%s cookies=%s/%s", common.X5, a1.Value, session.Value)
			}
		}
		fmt.Fprint(w, `{"code":0,"success":true,"data":{"onebox_list":[]}}`)
	}))
	defer server.Close()
	client.baseURL = server.URL
	client.SetTransport(server.Client().Transport.(*http.Transport))
	stop := make(chan struct{})
	toggled := make(chan struct{})
	go func() {
		defer close(toggled)
		for {
			select {
			case <-stop:
				return
			default:
				client.SetCookies(map[string]string{"a1": "A", "web_session": "A"})
				client.SetCookies(map[string]string{"a1": "B", "web_session": "B"})
			}
		}
	}()
	var wg sync.WaitGroup
	for j := 0; j < 3; j++ {
		wg.Go(func() {
			for i := 0; i < 12; i++ {
				if _, err := client.SearchUserOnebox("audit"); err != nil {
					errors <- err
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	<-toggled
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
