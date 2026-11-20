package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestRealConfigSaveNavigationDescribesOnlyCommittedListenerChange(t *testing.T) {
	previous := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = previous })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	oldAddr := ln.Addr().String()
	path := filepath.Join(t.TempDir(), "application.yaml")
	initial := "admin:\n  enable: true\n  addr: " + oldAddr + "\n  token: navigation-secret\nbot:\n  commandPrefix: /\n"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig = c
	s := &Server{token: "navigation-secret"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/config", s.withAuth(s.handleConfig))
	s.handler = mux
	s.addr = oldAddr
	s.serve(ln)
	t.Cleanup(func() {
		s.runtimeMu.Lock()
		h := s.httpServer
		s.runtimeMu.Unlock()
		if h != nil {
			_ = h.Close()
		}
		s.retired.Wait()
	})
	c.OnPrepareReload(s.prepareRuntimeReload)
	client := &http.Client{Timeout: 3 * time.Second}
	post := func(addr, patch string) (int, map[string]interface{}, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/config", strings.NewReader(`{"config":`+patch+`}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer navigation-secret")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal("save response lost during listener transition", err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err, string(data))
		}
		if strings.Contains(string(data), "navigation-secret") {
			t.Fatal("navigation metadata included the authorization token")
		}
		return resp.StatusCode, body, string(data)
	}
	status, body, raw := post(oldAddr, `{"admin":{"addr":"127.0.0.1:0"}}`)
	navigation, ok := body["navigation"].(map[string]interface{})
	s.runtimeMu.Lock()
	newAddr := s.listener.Addr().String()
	s.runtimeMu.Unlock()
	if status != http.StatusOK || !ok || navigation["enabled"] != true || navigation["addr"] != newAddr || newAddr == oldAddr || strings.HasSuffix(newAddr, ":0") {
		t.Fatalf("listener migration was not advertised accurately: status=%d actual=%s body=%s", status, newAddr, raw)
	}
	status, body, raw = post(newAddr, `{"bot":{"commandPrefix":"!"}}`)
	if status != http.StatusOK || body["navigation"] != nil {
		t.Fatalf("an unrelated save advertised a listener migration: status=%d body=%s", status, raw)
	}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	status, body, raw = post(newAddr, fmt.Sprintf(`{"admin":{"addr":%q}}`, occupied.Addr().String()))
	if status != http.StatusBadRequest || body["navigation"] != nil {
		t.Fatalf("a rejected listener change advertised success: status=%d body=%s", status, raw)
	}
	status, body, raw = post(newAddr, `{"admin":{"enable":false}}`)
	navigation, ok = body["navigation"].(map[string]interface{})
	if status != http.StatusOK || !ok || navigation["enabled"] != false || navigation["addr"] != nil {
		t.Fatalf("listener disable was not advertised accurately: status=%d body=%s", status, raw)
	}
}
