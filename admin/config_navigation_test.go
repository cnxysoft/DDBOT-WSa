package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
)

func startNavigationTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	withConfigFile(t, "admin:\n  enable: true\n  token: navigation-test-token\n  addr: "+addr+"\nbot:\n  commandPrefix: /\n")
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		ln.Close()
		t.Fatal(err)
	}
	s := &Server{addr: addr, token: "navigation-test-token"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/config", s.withAuth(s.handleConfig))
	s.handler = mux
	s.serve(ln)
	config.GlobalConfig.OnPrepareReload(s.prepareRuntimeReload)
	t.Cleanup(func() {
		s.runtimeMu.Lock()
		h := s.httpServer
		s.runtimeMu.Unlock()
		if h != nil {
			_ = h.Close()
		}
		s.retired.Wait()
	})
	return s, addr
}

func saveNavigationTestConfig(t *testing.T, addr, patch string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/config", bytes.NewBufferString(`{"config":`+patch+`}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer navigation-test-token")
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestConfigNavigationReportsOnlyCommittedListenerChanges(t *testing.T) {
	s, addr := startNavigationTestServer(t)
	for _, patch := range []string{`{"bot":{"commandPrefix":"!"}}`, `{"admin":{"addr":"` + addr + `"}}`} {
		status, body := saveNavigationTestConfig(t, addr, patch)
		if status != http.StatusOK || bytes.Contains(body, []byte(`"navigation"`)) {
			t.Fatalf("unchanged listener produced navigation: %d %s", status, body)
		}
	}
	status, body := saveNavigationTestConfig(t, addr, `{"admin":{"addr":"127.0.0.1:0"}}`)
	var result struct{ Navigation *configNavigation }
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || result.Navigation == nil || !result.Navigation.Enabled {
		t.Fatalf("missing successful listener navigation: %d %s", status, body)
	}
	s.runtimeMu.Lock()
	actual := s.listener.Addr().String()
	s.runtimeMu.Unlock()
	_, port, err := net.SplitHostPort(result.Navigation.Addr)
	if err != nil || port == "0" || result.Navigation.Addr != actual || actual == addr {
		t.Fatalf("navigation did not report actual assigned listener: %s vs %s", body, actual)
	}
	status, body = saveNavigationTestConfig(t, actual, `{"admin":{"enable":false}}`)
	result.Navigation = nil
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || result.Navigation == nil || result.Navigation.Enabled || result.Navigation.Addr != "" {
		t.Fatalf("disable did not return navigation before draining: %d %s", status, body)
	}
	if bytes.Contains(body, []byte("navigation-test-token")) {
		t.Fatal("navigation exposed management credentials")
	}
}

func TestConfigNavigationIsAbsentWhenListenerPreparationFails(t *testing.T) {
	_, addr := startNavigationTestServer(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	status, body := saveNavigationTestConfig(t, addr, `{"admin":{"addr":"`+occupied.Addr().String()+`"}}`)
	if status != http.StatusBadRequest || bytes.Contains(body, []byte(`"navigation"`)) {
		t.Fatalf("failed listener advertised migration: %d %s", status, body)
	}
}
