package weibo

import (
	"net/http"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

func init() {
	concern.RegisterConcern(NewConcern(concern.GetNotifyChan()))
}

func freshCookieOpt(sub string) {
	var cookies []*http.Cookie
	var err error

	// 如果启用了 Cookie 刷新 API，优先从 API 获取
	if cfg.GetWeiboCookieRefreshEnable() {
		cookies, err = FreshCookieFromAPI()
		if err != nil {
			logger.Errorf("FreshCookieFromAPI error %v, fallback to normal method", err)
		}
	}

	// 如果从 API 获取失败或未启用 API，使用原有方法
	if len(cookies) == 0 {
		localutils.Retry(3, time.Second, func() bool {
			if isGuestMode() {
				cookies, err = FreshCookieGuest()
			} else {
				cookies, err = FreshCookieLogin()
			}
			return err == nil
		})
	}

	if err != nil {
		logger.Errorf("FreshCookie error %v", err)
	} else {
		var opt []requests.Option
		for _, cookie := range cookies {
			// 如果配置了 SUB，使用配置的 SUB 值
			if cookie.Name == "SUB" && sub != "" {
				cookie.Value = sub
			}
			opt = append(opt, requests.HttpCookieOption(cookie))
		}
		visitorCookiesOpt.Store(opt)
	}
}

func GetSettingCookie() string {
	return config.GlobalConfig.GetString("weibo.sub")
}

func GetQRLoginEnable() bool {
	return config.GlobalConfig.GetBool("weibo.qrlogin")
}
