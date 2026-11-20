package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- H2：setup 匿名接口的来源限制 ----

func TestIsLoopbackAddr(t *testing.T) {
	assert.True(t, isLoopbackAddr("127.0.0.1:15631"))
	assert.True(t, isLoopbackAddr("127.0.0.5:80"))
	assert.True(t, isLoopbackAddr("[::1]:15631"))
	assert.False(t, isLoopbackAddr("192.168.1.10:15631"))
	assert.False(t, isLoopbackAddr("evil.com:80"))
	assert.False(t, isLoopbackAddr(""))
}

func TestIsLoopbackHost(t *testing.T) {
	assert.True(t, isLoopbackHost("127.0.0.1:15631"))
	assert.True(t, isLoopbackHost("localhost:15631"))
	assert.True(t, isLoopbackHost("[::1]:15631"))
	assert.False(t, isLoopbackHost("evil.com"))
	assert.False(t, isLoopbackHost("evil.com:15631"))
	assert.False(t, isLoopbackHost(""))
}

func TestSameOriginHost(t *testing.T) {
	assert.True(t, sameOriginHost("http://127.0.0.1:15631", "127.0.0.1:15631"))
	assert.True(t, sameOriginHost("https://localhost:15631", "localhost:15631"))
	assert.False(t, sameOriginHost("http://evil.com", "127.0.0.1:15631"))
	// 同主机不同端口不是同源
	assert.False(t, sameOriginHost("http://127.0.0.1:15630", "127.0.0.1:15631"))
	assert.False(t, sameOriginHost("not-a-url", "127.0.0.1:15631"))
}

// TestGuardSetup 覆盖审查报告 H2：跨站页面（Origin 不同源）与 DNS rebinding（Host 非回环）
// 都必须被拒绝，正常的本机同源向导请求放行。
func TestGuardSetup(t *testing.T) {
	s := &Server{}
	reached := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/setup", s.guardSetup(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 本机 + 无 Origin：放行（httptest 客户端来源是 127.0.0.1）
	resp, err := http.Post(ts.URL+"/api/v1/setup", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, reached)

	// 跨站 Origin：拒绝
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Origin", "http://evil.com")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// 只有 Referer（无 Origin）且跨站：拒绝
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Referer", "http://evil.com/attack.html")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Host 为非回环域名（DNS rebinding 形态）：拒绝
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Host = "evil.com"
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// ---- M2：POST /api/admin/sub/config 省略 config 字段不再 panic ----

func TestHandleSubConfigMissingConfig(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/sub/config",
		strings.NewReader(`{"site":"bilibili","id":"1","groupCode":1}`))
	rec := httptest.NewRecorder()

	s.handleSubConfig(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "config is required")
}

// ---- M6：不能移除最后一个管理员 ----

func TestGuardAdminUngrantLastAdmin(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	sm := lsp.Instance.PermissionStateManager
	sm.FreshIndex() // ListAdmin 依赖 buntdb 索引

	// 清掉其它用例可能残留的管理员，保证从干净状态开始
	for _, qq := range sm.ListAdmin() {
		_ = sm.UngrantRole(qq, permission.Admin)
	}
	require.NoError(t, sm.GrantRole(10001, permission.Admin))

	s := &Server{token: "t"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/permissions/admin", s.guardAdminUngrant(s.handleAdminAction))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 移除唯一管理员：400，且管理员保留
	resp, err := http.Post(ts.URL+"/api/v1/permissions/admin", "application/json",
		strings.NewReader(`{"qq":10001,"action":"ungrant"}`))
	require.NoError(t, err)
	var body map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.True(t, sm.CheckAdmin(10001), "最后一个管理员不应被移除")

	// 存在第二个管理员后可以移除
	require.NoError(t, sm.GrantRole(10002, permission.Admin))
	resp, err = http.Post(ts.URL+"/api/v1/permissions/admin", "application/json",
		strings.NewReader(`{"qq":10001,"action":"ungrant"}`))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.False(t, sm.CheckAdmin(10001))
	assert.True(t, sm.CheckAdmin(10002))

	// 清理
	_ = sm.UngrantRole(10002, permission.Admin)
}

// TestWithAuthNoWildcardCORS 覆盖 H2：数据接口不再返回 Access-Control-Allow-Origin: *，
// 避免任意网页在受害者浏览器里带 token 读取面板数据。
func TestWithAuthNoWildcardCORS(t *testing.T) {
	s := &Server{token: "t"}

	// 已鉴权请求：正常放行，但不带 CORS 通配符
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer t")
	s.withAuth(s.handleHealth)(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	// 未鉴权请求：拒绝，同样不返回 CORS 头
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	s.withAuth(s.handleHealth)(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}
