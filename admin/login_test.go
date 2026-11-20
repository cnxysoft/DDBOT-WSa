package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postJSON(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return rec
}

// 同一平台只允许一个会话在跑：用户再点一次「扫码登录」是要换一张码，
// 两个会话同时扫会抢着往同一份配置里写凭据。
func TestLoginManagerKeepsOneSessionPerPlatform(t *testing.T) {
	m := newLoginManager()

	first := m.start(loginPlatformBilibili)
	select {
	case <-first.cancel:
		t.Fatal("新会话不该一开始就是取消状态")
	default:
	}

	second := m.start(loginPlatformBilibili)
	select {
	case <-first.cancel:
		// 期望：旧会话被停掉
	case <-time.After(time.Second):
		t.Fatal("开新会话时应停掉同平台的旧会话")
	}
	if m.get(first.id) != nil {
		t.Fatal("旧会话应从表里移除")
	}
	if m.get(second.id) != second {
		t.Fatal("新会话应在表里")
	}

	// 不同平台互不影响
	weibo := m.start(loginPlatformWeibo)
	if m.get(weibo.id) != weibo || m.get(second.id) != second {
		t.Fatal("不同平台的会话应共存")
	}
}

func TestLoginManagerReleaseOnlyClearsOwnSlot(t *testing.T) {
	m := newLoginManager()
	first := m.start(loginPlatformBilibili)
	second := m.start(loginPlatformBilibili)

	// first 已经被 second 顶掉，release(first) 不该把 second 的槽位也删了
	m.release(first)
	if m.get(second.id) != second {
		t.Fatal("release 旧会话不应影响当前会话")
	}
	m.release(second)
	if m.get(second.id) != nil {
		t.Fatal("release 后会话应移除")
	}
}

func TestHandleLoginQRCodeRejectsUnknownPlatform(t *testing.T) {
	s := &Server{}
	rec := postJSON(t, s.handleLoginQRCode, "{\"platform\":\"telegram\"}")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知平台应返回 400，got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported platform") {
		t.Fatalf("错误信息应点明平台，got %s", rec.Body.String())
	}
}

func TestHandleLoginQRCodeRejectsBadBody(t *testing.T) {
	s := &Server{}
	rec := postJSON(t, s.handleLoginQRCode, "not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏请求体应返回 400，got %d", rec.Code)
	}
}

func TestHandleLoginQRCodeRejectsGet(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleLoginQRCode(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应返回 405，got %d", rec.Code)
	}
}

func TestHandleLoginQRCodeStatusUnknownSession(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleLoginQRCodeStatus(rec, httptest.NewRequest(http.MethodGet, "/?session=nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知会话应返回 404，got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleLoginQRCodeStatusReportsState(t *testing.T) {
	s := &Server{}
	sess := s.loginManagerOf().start(loginPlatformWeibo)
	sess.setState(loginStateScanned, "已扫码，请在手机上确认")

	rec := httptest.NewRecorder()
	s.handleLoginQRCodeStatus(rec, httptest.NewRequest(http.MethodGet, "/?session="+sess.id, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, loginStateScanned) || !strings.Contains(body, "已扫码") {
		t.Fatalf("应回传状态与提示，got %s", body)
	}
}

// 取消后会话应当消失：前端关掉弹层就会走到这里，后台不该继续轮询。
func TestHandleLoginQRCodeCancel(t *testing.T) {
	s := &Server{}
	sess := s.loginManagerOf().start(loginPlatformBilibili)

	rec := postJSON(t, s.handleLoginQRCodeCancel, "{\"session\":\""+sess.id+"\"}")
	if rec.Code != http.StatusOK {
		t.Fatalf("取消应返回 200，got %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case <-sess.cancel:
	default:
		t.Fatal("取消后会话应被停止")
	}
	if s.loginManagerOf().get(sess.id) != nil {
		t.Fatal("取消后会话应移除")
	}

	// 再取消一次（会话已不在）也不该报错
	rec = postJSON(t, s.handleLoginQRCodeCancel, "{\"session\":\""+sess.id+"\"}")
	if rec.Code != http.StatusOK {
		t.Fatalf("重复取消应幂等，got %d", rec.Code)
	}
}

// loginManagerOf 必须能用在测试里直接构造的 &Server{} 上。
func TestLoginManagerLazyInit(t *testing.T) {
	s := &Server{}
	if s.loginManagerOf() == nil {
		t.Fatal("懒初始化应返回管理器")
	}
	if s.loginManagerOf() != s.loginManagerOf() {
		t.Fatal("多次调用应返回同一个管理器")
	}
}

// 会话跑完（成功/失败）后必须还能读到最终状态。
//
// 回归背景：原先 run 函数用 defer mgr.release(sess) 立刻把会话从表里删掉，
// 前端每 2 秒轮询一次，等它下一次来问时已经是 404 —— 面板就永远停在倒数第二个状态。
// 实测症状：手机上已经确认登录、Cookie 也写进配置了，面板还显示「已扫码，请在手机上确认」。
func TestLoginSessionKeepsTerminalStateReadable(t *testing.T) {
	s := &Server{}
	mgr := s.loginManagerOf()
	sess := mgr.start(loginPlatformBilibili)

	sess.setState(loginStateConfirmed, "登录成功，Cookie 已写入配置")
	mgr.finish(sess) // 相当于 runBilibiliLogin 的 defer

	if mgr.get(sess.id) != sess {
		t.Fatal("会话跑完后仍应能被读到最终状态")
	}
	rec := httptest.NewRecorder()
	s.handleLoginQRCodeStatus(rec, httptest.NewRequest(http.MethodGet, "/?session="+sess.id, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), loginStateConfirmed) {
		t.Fatalf("终态应可读，got %d: %s", rec.Code, rec.Body.String())
	}

	// 但「同平台只留一个」的占位要腾出来：用户应能马上再发一次登录
	next := mgr.start(loginPlatformBilibili)
	if mgr.get(next.id) != next {
		t.Fatal("应能开新会话")
	}
	select {
	case <-sess.cancel:
		t.Fatal("finish 只是收尾，不该把上一个会话标记成取消")
	default:
	}
}

func TestHandlePlatformAccountRejectsUnknownSite(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handlePlatformAccount(rec, httptest.NewRequest(http.MethodGet, "/?site=telegram", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知站点应返回 400，got %d: %s", rec.Code, rec.Body.String())
	}
}

// 没配凭据时不该报「已登录」——登录态卡片就是靠这个字段决定显示什么。
func TestHandlePlatformAccountWeiboNotConfigured(t *testing.T) {
	invalidateAccountCache(loginPlatformWeibo)

	s := &Server{}
	rec := httptest.NewRecorder()
	s.handlePlatformAccount(rec, httptest.NewRequest(http.MethodGet, "/?site=weibo", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，got %d: %s", rec.Code, rec.Body.String())
	}

	var acc platformAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil {
		t.Fatal(err)
	}
	if acc.Site != loginPlatformWeibo {
		t.Fatalf("site 应为 weibo，got %q", acc.Site)
	}
	if acc.LoggedIn {
		t.Fatalf("没配 SUB 不该报已登录：%+v", acc)
	}
	if acc.Message == "" {
		t.Fatal("应给出未登录的原因")
	}
}

// 账号信息有缓存，登录成功后必须能立刻失效，否则面板还显示上一个账号。
func TestAccountCacheInvalidation(t *testing.T) {
	invalidateAccountCache(loginPlatformBilibili)
	storeAccount(loginPlatformBilibili, platformAccount{Site: loginPlatformBilibili, Name: "旧账号"})
	if _, ok := cachedAccount(loginPlatformBilibili); !ok {
		t.Fatal("写入后应命中缓存")
	}
	invalidateAccountCache(loginPlatformBilibili)
	if _, ok := cachedAccount(loginPlatformBilibili); ok {
		t.Fatal("失效后不该再命中缓存")
	}
}
