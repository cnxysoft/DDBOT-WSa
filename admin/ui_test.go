package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postUI(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleUISave(rec, req)
	return rec
}

func getUI(s *Server) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handleUIGet(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ui", nil))
	return rec
}

func decodeUI(t *testing.T, rec *httptest.ResponseRecorder) UISettings {
	t.Helper()
	var got UISettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

func TestUIGetDefaultsWhenMissing(t *testing.T) {
	withConfigFile(t, "bot:\n  commandPrefix: \"/\"\n")
	s := &Server{}
	rec := getUI(s)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := decodeUI(t, rec)
	assert.Empty(t, got.BackgroundURL)
	assert.Equal(t, 0, got.BackgroundBlur)
	assert.Equal(t, 85, got.BackgroundOpacity)
	assert.Equal(t, 35, got.BackgroundMask)
	assert.Equal(t, "center", got.BackgroundPosition)
	assert.False(t, got.FontCustom)
	assert.Empty(t, got.FontColor)
}

func TestHandleUISaveAndGet(t *testing.T) {
	withConfigFile(t, "bot:\n  commandPrefix: \"/\"\n")
	s := &Server{}

	rec := postUI(s, `{
		"backgroundUrl":"https://example.com/bg.png",
		"backgroundBlur":6,
		"backgroundOpacity":70,
		"backgroundMask":50,
		"backgroundPosition":"top",
		"fontCustom":true,
		"fontColor":"#ff9dc7"
	}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 文件必须真的落在与 application.yaml 同目录的 ui.json
	path := uiSettingsPath()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"backgroundUrl": "https://example.com/bg.png"`)
	assert.Contains(t, string(data), `"fontColor": "#ff9dc7"`)

	got := decodeUI(t, getUI(s))
	assert.Equal(t, "https://example.com/bg.png", got.BackgroundURL)
	assert.Equal(t, 6, got.BackgroundBlur)
	assert.Equal(t, 70, got.BackgroundOpacity)
	assert.Equal(t, 50, got.BackgroundMask)
	assert.Equal(t, "top", got.BackgroundPosition)
	assert.True(t, got.FontCustom)
	assert.Equal(t, "#ff9dc7", got.FontColor)
}

func TestHandleUISaveAcceptsDataURL(t *testing.T) {
	withConfigFile(t, "")
	s := &Server{}

	rec := postUI(s, `{"backgroundUrl":"data:image/png;base64,AAAA","backgroundOpacity":90,"backgroundPosition":"bottom"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeUI(t, getUI(s))
	assert.True(t, strings.HasPrefix(got.BackgroundURL, "data:image/png;base64,"))
	assert.Equal(t, "bottom", got.BackgroundPosition)
}

func TestHandleUISaveRejectsInvalid(t *testing.T) {
	withConfigFile(t, "")
	s := &Server{}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"bad url", `{"backgroundUrl":"ftp://example.com/bg.png"}`, "http(s)"},
		{"blur", `{"backgroundBlur":25}`, "模糊"},
		{"opacity", `{"backgroundOpacity":10}`, "不透明度"},
		{"mask", `{"backgroundMask":81}`, "遮罩"},
		{"position", `{"backgroundPosition":"diagonal"}`, "位置"},
		{"font custom empty", `{"fontCustom":true}`, "字体颜色"},
		{"font color", `{"fontColor":"red"}`, "字体颜色"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postUI(s, c.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), c.want)
		})
	}
	if _, err := os.Stat(uiSettingsPath()); !os.IsNotExist(err) {
		t.Fatalf("校验失败时不应写出 ui.json: %v", err)
	}
}

func TestHandleUIRejectsWrongMethod(t *testing.T) {
	withConfigFile(t, "")
	s := &Server{}

	rec := httptest.NewRecorder()
	s.handleUIGet(rec, httptest.NewRequest(http.MethodPost, "/api/v1/ui", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = httptest.NewRecorder()
	s.handleUISave(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ui/save", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// 本机匿名（登录页也能改外观）应放行；远端必须带 admin.token。
func TestGuardLocalOrAuthUI(t *testing.T) {
	withConfigFile(t, "")
	s := &Server{token: "pw"}
	h := s.guardLocalOrAuth(s.handleUISave)

	// 本机 + 同源：放行
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ui/save", strings.NewReader(`{"backgroundOpacity":90}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:15631"
	req.Header.Set("Origin", "http://127.0.0.1:15631")
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 远端无 token：401
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ui/save", strings.NewReader(`{"backgroundOpacity":90}`))
	req.RemoteAddr = "192.168.1.20:12345"
	rec = httptest.NewRecorder()
	h(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// 远端带 token：放行
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ui/save", strings.NewReader(`{"backgroundOpacity":90}`))
	req.RemoteAddr = "192.168.1.20:12345"
	req.Header.Set("Authorization", "Bearer pw")
	rec = httptest.NewRecorder()
	h(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
