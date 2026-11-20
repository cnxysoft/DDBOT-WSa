package admin

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
)

func TestSetupRejectedResourceRetainsUninitializedState(t *testing.T) {
	const initial = "admin:\n  token: ''\nwebsocket:\n  mode: ws-server\n  ws-server: 127.0.0.1:15630\n"
	path := withConfigFile(t, initial)
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		if next.GetString("admin.token") != "new-secret" || next.GetString("websocket.ws-server") != "127.0.0.1:16630" {
			t.Fatal("setup did not prepare the whole candidate")
		}
		return nil, nil, errors.New("Bot连接更新失败，配置未应用")
	})
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(`{"token":"new-secret","obMode":"ws-server","obAddr":"127.0.0.1:16630"}`))
	s.handleSetup(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "配置未应用") {
		t.Fatal("resource rejection was presented as a disk error", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte(initial)) || s.getToken() != "" || config.GlobalConfig.GetString("admin.token") != "" || config.GlobalConfig.Status().Revision != 1 {
		t.Fatal("failed setup changed credentials or source")
	}
}
