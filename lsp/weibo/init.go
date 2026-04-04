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

	// API 模式：强制从 API 获取
	if cfg.IsWeiboAPIMode() {
		cookies, err = FreshCookieFromAPI()
		if err != nil {
			logger.Errorf("FreshCookieFromAPI error %v", err)
			logger.Warn("API 模式获取 Cookie 失败，微博功能可能无法正常使用")
			return // 直接返回，不执行后续逻辑
		}
	} else {
		// 非 API 模式：使用原有逻辑
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
		return
	}

	var subValue string
	var xsrfToken string

	// 优先使用传入的 sub 参数（如果有效）
	if sub != "" {
		logger.Infof("使用传入的 SUB 参数：%s...", sub[:min(20, len(sub))])
		subValue = sub
		// 从 cookies 中提取 XSRF-TOKEN（login 模式下 FreshCookieLogin 会返回）
		for _, cookie := range cookies {
			if cookie.Name == "XSRF-TOKEN" {
				xsrfToken = cookie.Value
				break
			}
		}
	} else if configuredSub := GetSettingCookie(); configuredSub != "" {
		// 其次使用配置中的 SUB
		logger.Infof("使用配置中的 SUB")
		subValue = configuredSub
		// 从 cookies 中提取 XSRF-TOKEN
		for _, cookie := range cookies {
			if cookie.Name == "XSRF-TOKEN" {
				xsrfToken = cookie.Value
				break
			}
		}
	} else if cfg.IsWeiboAPIMode() {
		// API 模式：从 API 返回中提取 SUB 和 XSRF-TOKEN
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				subValue = cookie.Value
			}
			if cookie.Name == "XSRF-TOKEN" {
				xsrfToken = cookie.Value
			}
		}
		if subValue == "" {
			logger.Warnf("API 未返回 SUB Cookie")
			return
		}
		logger.Infof("使用 API 返回的 SUB：%s...", subValue[:min(20, len(subValue))])

		if xsrfToken == "" {
			logger.Warnf("API 未返回 XSRF-TOKEN Cookie，可能导致请求失败")
		}
	} else {
		// 非 API 模式且无配置：使用原有逻辑生成的 Cookie
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				subValue = cookie.Value
			}
			if cookie.Name == "XSRF-TOKEN" {
				xsrfToken = cookie.Value
			}
		}
	}

	if subValue == "" {
		logger.Warnf("未找到有效的 SUB Cookie")
		return
	}

	// 设置 SUB 和 XSRF-TOKEN Cookie
	opt := []requests.Option{
		requests.CookieOption("SUB", subValue),
	}
	if xsrfToken != "" {
		opt = append(opt, requests.CookieOption("XSRF-TOKEN", xsrfToken))
		logger.Debugf("已加载 XSRF-TOKEN: %s...", xsrfToken[:min(10, len(xsrfToken))])
	} else {
		logger.Warnf("未找到 XSRF-TOKEN，部分 API 请求可能失败")
	}
	visitorCookiesOpt.Store(opt)
	logger.Infof("微博 SUB Cookie 已加载：%s...", subValue[:min(20, len(subValue))])
}

func GetSettingCookie() string {
	return config.GlobalConfig.GetString("weibo.sub")
}

func GetQRLoginEnable() bool {
	return config.GlobalConfig.GetBool("weibo.qrlogin")
}
