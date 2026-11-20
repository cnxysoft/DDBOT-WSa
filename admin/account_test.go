package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
)

// 这里只测 qqofficial 这条：B站/微博 的 fetch 会打真实远端接口，不适合放进单测。
const testQQAppID = "1900000000"
const testQQAppSecret = "TEST_APP_SECRET_MUST_NOT_LEAK"

// setQQOfficialConfig 写 qqofficial 配置并清掉账号缓存，测试结束还原。
func setQQOfficialConfig(t *testing.T, appID, secret string) {
	t.Helper()
	config.GlobalConfig.Set("qqofficial.app-id", appID)
	config.GlobalConfig.Set("qqofficial.app-secret", secret)
	invalidateAccountCache(loginPlatformQQOfficial)
	t.Cleanup(func() {
		config.GlobalConfig.Set("qqofficial.app-id", "")
		config.GlobalConfig.Set("qqofficial.app-secret", "")
		invalidateAccountCache(loginPlatformQQOfficial)
	})
}

func getPlatformAccount(t *testing.T, s *Server, site string) (*httptest.ResponseRecorder, platformAccount) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handlePlatformAccount(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/account?site="+site, nil))
	var acc platformAccount
	_ = json.Unmarshal(rec.Body.Bytes(), &acc)
	return rec, acc
}

func TestPlatformAccountQQOfficialBound(t *testing.T) {
	setQQOfficialConfig(t, testQQAppID, testQQAppSecret)
	s := &Server{}

	rec, acc := getPlatformAccount(t, s, loginPlatformQQOfficial)
	if rec.Code != http.StatusOK {
		t.Fatalf("已绑定应返回 200，got %d: %s", rec.Code, rec.Body.String())
	}
	if !acc.LoggedIn || acc.Site != loginPlatformQQOfficial {
		t.Fatalf("应报已绑定：%+v", acc)
	}
	if acc.Name != testQQAppID {
		t.Fatalf("AppID 应作为账号标识返回，got %q", acc.Name)
	}
	if acc.Message == "" {
		t.Fatal("应给出状态文案")
	}
	// 凭据绝不回显
	if strings.Contains(rec.Body.String(), testQQAppSecret) {
		t.Fatalf("响应里出现了 app-secret：%s", rec.Body.String())
	}
}

func TestPlatformAccountQQOfficialUnbound(t *testing.T) {
	setQQOfficialConfig(t, "", "")
	s := &Server{}

	rec, acc := getPlatformAccount(t, s, loginPlatformQQOfficial)
	if rec.Code != http.StatusOK {
		t.Fatalf("未绑定不该报错，got %d: %s", rec.Code, rec.Body.String())
	}
	if acc.LoggedIn {
		t.Fatal("没配置不该报已绑定")
	}
	if !strings.Contains(acc.Message, "尚未绑定") {
		t.Fatalf("文案应说明未绑定，got %q", acc.Message)
	}
}

func TestPlatformAccountQQOfficialMissingSecret(t *testing.T) {
	setQQOfficialConfig(t, testQQAppID, "")
	s := &Server{}

	rec, acc := getPlatformAccount(t, s, loginPlatformQQOfficial)
	if rec.Code != http.StatusOK {
		t.Fatalf("半配置不该报错，got %d", rec.Code)
	}
	if acc.LoggedIn {
		t.Fatal("缺 AppSecret 不该报已绑定")
	}
	if acc.Name != testQQAppID {
		t.Fatalf("仍应报出 AppID，got %q", acc.Name)
	}
	if !strings.Contains(acc.Message, "AppSecret") {
		t.Fatalf("文案应点明缺 AppSecret，got %q", acc.Message)
	}
}

func TestPlatformAccountRejectsUnsupportedSite(t *testing.T) {
	s := &Server{}
	rec, _ := getPlatformAccount(t, s, "telegram")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知 site 应返回 400，got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unsupported site") {
		t.Fatalf("错误信息应点明 site，got %s", rec.Body.String())
	}
}

// TestPlatformAccountQQOfficialCacheInvalidation 固化「绑定成功要清缓存」这条约定：
// finishQQOfficialLogin 写完配置必须调 invalidateAccountCache，否则面板最长 60 秒
// 还显示旧状态。
func TestPlatformAccountQQOfficialCacheInvalidation(t *testing.T) {
	setQQOfficialConfig(t, testQQAppID, testQQAppSecret)
	s := &Server{}

	if _, acc := getPlatformAccount(t, s, loginPlatformQQOfficial); !acc.LoggedIn {
		t.Fatalf("首次应报已绑定：%+v", acc)
	}

	// 改配置但不失效缓存：应仍然命中缓存里的旧值
	config.GlobalConfig.Set("qqofficial.app-id", "1999999999")
	if _, acc := getPlatformAccount(t, s, loginPlatformQQOfficial); acc.Name != testQQAppID {
		t.Fatalf("应命中缓存，got %q", acc.Name)
	}

	// 失效后拿到新值
	invalidateAccountCache(loginPlatformQQOfficial)
	if _, acc := getPlatformAccount(t, s, loginPlatformQQOfficial); acc.Name != "1999999999" {
		t.Fatalf("失效缓存后应读到新值，got %q", acc.Name)
	}
}
