package admin

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func adminSnapshot(enabled bool, addr, token string) *config.Snapshot {
	v := viper.New()
	v.Set("admin.enable", enabled)
	v.Set("admin.addr", addr)
	v.Set("admin.token", token)
	return (&config.Config{Viper: v}).Snapshot()
}

func TestAdminRuntimeReloadRetainsListenerOnFailureAndAbort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{addr: ln.Addr().String(), token: "old", handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	s.serve(ln)
	t.Cleanup(func() {
		if s.httpServer != nil {
			_ = s.httpServer.Close()
		}
	})
	previous := adminSnapshot(true, s.addr, "old")
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, _, err := s.prepareRuntimeReload(previous, adminSnapshot(true, occupied.Addr().String(), "new")); err == nil {
		t.Fatal("occupied listener accepted")
	}
	if s.getToken() != "old" || s.listener != ln {
		t.Fatal("failed candidate changed server")
	}
	_, abort, err := s.prepareRuntimeReload(previous, adminSnapshot(true, "127.0.0.1:0", "new"))
	if err != nil {
		t.Fatal(err)
	}
	abort()
	if s.getToken() != "old" || s.listener != ln {
		t.Fatal("aborted candidate changed server")
	}
	commit, _, err := s.prepareRuntimeReload(previous, adminSnapshot(true, s.addr, "new"))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if s.getToken() != "new" || s.listener != ln {
		t.Fatal("token reload rebound listener")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer old")
	rec := httptest.NewRecorder()
	s.withAuth(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })(rec, request)
	if rec.Code != http.StatusForbidden {
		t.Fatal("old token still authorized")
	}
	request.Header.Set("Authorization", "Bearer new")
	rec = httptest.NewRecorder()
	s.withAuth(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })(rec, request)
	if rec.Code != http.StatusNoContent {
		t.Fatal("new token not authorized")
	}
	commit, _, err = s.prepareRuntimeReload(adminSnapshot(true, s.addr, "new"), adminSnapshot(false, s.addr, "new"))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if s.listener != nil || s.httpServer != nil {
		t.Fatal("disable did not detach listener")
	}
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.DialTimeout("tcp", previous.GetString("admin.addr"), 50*time.Millisecond)
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("old listener remained active")
		}
	}
	commit, _, err = s.prepareRuntimeReload(adminSnapshot(false, s.addr, "new"), adminSnapshot(true, "127.0.0.1:0", "new"))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if s.listener == nil {
		t.Fatal("off to on failed")
	}
}
