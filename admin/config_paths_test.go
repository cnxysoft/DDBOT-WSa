package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
)

func TestConfigDottedCredentialMergeRetainsOriginalPeers(t *testing.T) {
	path := withConfigFile(t, "BILIBILI.SESSDATA: old-session\nBILIBILI.bili_jct: old-csrf\nBILIBILI.interval: 30s\n")
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	rec := postConfig(t, `{"config":{"bilibili":{"sessdata":"new-session","interval":"2m"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if config.GlobalConfig.GetString("bilibili.sessdata") != "new-session" || config.GlobalConfig.GetString("bilibili.bili_jct") != "old-csrf" || config.GlobalConfig.GetString("bilibili.interval") != "2m" {
		t.Fatal("dotted source was not merged into one unambiguous runtime path")
	}
	for key := range readConfigMap(t, path) {
		if strings.Contains(key, ".") {
			t.Fatal("saved source retained a shadowing dotted alias", key)
		}
	}
}

func TestSetupMergesRuntimeEquivalentExistingPaths(t *testing.T) {
	withConfigFile(t, "ADMIN:\n  TOKEN: ''\n  enable: true\nWEBSOCKET:\n  mode: ws-server\n  token: keep-connection-token\n  ws-server: '127.0.0.1:15630'\n")
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(`{"token":"new-management-token","obMode":"ws-server","obAddr":"127.0.0.1:16630"}`))
	s.handleSetup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if config.GlobalConfig.GetString("admin.token") != "new-management-token" || s.getToken() != "new-management-token" || config.GlobalConfig.GetString("websocket.token") != "keep-connection-token" || config.GlobalConfig.GetString("websocket.ws-server") != "127.0.0.1:16630" {
		t.Fatal("setup did not preserve and update the actual runtime paths")
	}
}
