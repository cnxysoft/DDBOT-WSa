package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
)

// TestValidateSetupOb 覆盖 M5：向导必须与 adapter/wsclient.go 的启动约束一致——
// ws-server 监听非回环地址且 Access Token 为空时必须拒绝，
// 否则会写出「向导说成功、重启后 ws-server 拒绝启动」的配置。
func TestValidateSetupOb(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		addr    string
		token   string
		wantErr bool
	}{
		{"回环地址可留空 token", "ws-server", "127.0.0.1:15630", "", false},
		{"IPv6 回环可留空 token", "ws-server", "[::1]:15630", "", false},
		{"回环地址带 token 仍通过", "ws-server", "127.0.0.1:15630", "secret", false},
		{"全网卡监听且无 token 被拒绝", "ws-server", "0.0.0.0:15630", "", true},
		{"空 host 绑定全部网卡被拒绝", "ws-server", ":15630", "", true},
		{"具体网卡 IP 无 token 被拒绝", "ws-server", "192.168.1.10:15630", "", true},
		{"localhost 被拒绝（与 adapter 判定一致）", "ws-server", "localhost:15630", "", true},
		{"缺少端口被拒绝（与 adapter 判定一致）", "ws-server", "0.0.0.0", "", true},
		{"非回环有 token 通过", "ws-server", "0.0.0.0:15630", "secret", false},
		{"ws-reverse 不受该限制", "ws-reverse", "ws://example.com:3001", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSetupOb(tt.mode, tt.addr, tt.token)
			if tt.wantErr && err == nil {
				t.Fatalf("validateSetupOb(%q, %q, %q) 应返回错误", tt.mode, tt.addr, tt.token)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateSetupOb(%q, %q, %q) 不应返回错误: %v", tt.mode, tt.addr, tt.token, err)
			}
			// 拒绝时的提示必须包含用户填的地址，方便定位
			if err != nil && !strings.Contains(err.Error(), tt.addr) {
				t.Fatalf("错误信息应包含监听地址 %q，got %q", tt.addr, err.Error())
			}
		})
	}
}

// TestHandleSetupRejectsExposedWSServerWithoutToken 验证 M5 的接口接线：
// 向导在写配置前返回 400 与明确提示，而不是"status: ok"后重启起不来。
func TestHandleSetupRejectsExposedWSServerWithoutToken(t *testing.T) {
	// 配置路径指向临时目录：即便校验回归而继续写出，也不会污染仓库
	prev := config.GlobalConfig.ConfigFileUsed()
	config.GlobalConfig.SetConfigFile(filepath.Join(t.TempDir(), "application.yaml"))
	defer config.GlobalConfig.SetConfigFile(prev)

	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup",
		strings.NewReader(`{"token":"secret123","obMode":"ws-server","obAddr":"0.0.0.0:15630","obToken":""}`))
	rec := httptest.NewRecorder()
	s.handleSetup(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("应返回 400，got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Access Token") {
		t.Fatalf("错误信息应提示 Access Token，got %s", rec.Body.String())
	}
}
