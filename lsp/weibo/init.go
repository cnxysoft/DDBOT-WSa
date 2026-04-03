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

	// 优先使用配置中的 SUB（如果有）
	if configuredSub := GetSettingCookie(); configuredSub != "" {
		logger.Infof("使用配置中的 SUB")
		subValue = configuredSub
	} else if cfg.IsWeiboAPIMode() {
		// API 模式：从 API 返回中提取 SUB
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				subValue = cookie.Value
				break
			}
		}
		if subValue == "" {
			logger.Warnf("API 未返回 SUB Cookie")
			return
		}
		logger.Infof("使用 API 返回的 SUB：%s...", subValue[:min(20, len(subValue))])

		// 检查是否有 XSRF-TOKEN
		var hasXsrf bool
		for _, cookie := range cookies {
			if cookie.Name == "XSRF-TOKEN" {
				hasXsrf = true
				break
			}
		}
		if !hasXsrf {
			logger.Warnf("API 未返回 XSRF-TOKEN Cookie，可能导致请求失败")
		}
	} else {
		// 非 API 模式且无配置：使用原有逻辑生成的 Cookie
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				subValue = cookie.Value
				break
			}
		}
	}

	if subValue == "" {
		logger.Warnf("未找到有效的 SUB Cookie")
		return
	}

	// 只设置 SUB Cookie
	opt := []requests.Option{
		requests.CookieOption("SUB", subValue),
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
