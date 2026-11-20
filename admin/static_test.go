package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestServeWebFile_Index 验证根路径返回内嵌的 index.html。
func TestServeWebFile_Index(t *testing.T) {
	w := httptest.NewRecorder()
	serveWebFile(w, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Contains(t, w.Body.String(), "DDBOT")
}

// TestServeWebFile_SpaFallback 验证未命中静态文件的路径回退到 index.html（SPA 兜底）。
func TestServeWebFile_SpaFallback(t *testing.T) {
	for _, p := range []string{"/dashboard", "/subscriptions/live/123", "/deep/nested/path"} {
		w := httptest.NewRecorder()
		serveWebFile(w, httptest.NewRequest(http.MethodGet, p, nil))

		assert.Equal(t, http.StatusOK, w.Code, "path: %s", p)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html", "path: %s", p)
		assert.Contains(t, w.Body.String(), "DDBOT", "path: %s", p)
	}
}

// TestServeWebFile_PathTraversal 验证路径穿越请求被拒绝，不会逃出内嵌目录。
// http.ServeFileFS 对含 ".." 的原始 URL.Path 直接返回 400（invalid URL path），
// 即使 handler 已将目标回退为 index.html，标准库这层防护仍然生效。
func TestServeWebFile_PathTraversal(t *testing.T) {
	w := httptest.NewRecorder()
	serveWebFile(w, httptest.NewRequest(http.MethodGet, "/../server.go", nil))

	// 被标准库拒绝，而不是暴露 admin 包源码
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotContains(t, w.Body.String(), "package admin")
}

// TestServeMux_StaticDoesNotShadowAPI 验证 catch-all 静态路由不会吞掉精确注册的 API 路径。
func TestServeMux_StaticDoesNotShadowAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"ok\":true}"))
	})
	mux.HandleFunc("/api/debug", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("debugger"))
	})
	mux.Handle("/", http.HandlerFunc(serveWebFile))

	// API 路径命中各自的 handler
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "{\"ok\":true}", w.Body.String())

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/debug", nil))
	assert.Equal(t, "debugger", w.Body.String())

	// 根路径命中静态资源
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.True(t, strings.Contains(w.Body.String(), "DDBOT"))
}
