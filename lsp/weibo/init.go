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
	// API 模式不需要刷新 Cookie，直接从外部 API 获取数据
	if cfg.IsWeiboAPIMode() {
		return
	}

	var cookies []*http.Cookie
	var err error

	// 如果传入了有效的 sub，直接使用（autorefresh 会传入）
	if sub != "" {
		// 只需要获取 XSRF-TOKEN，SUB 已经由调用方提供
		localutils.Retry(3, time.Second, func() bool {
			if isGuestMode() {
				cookies, err = FreshCookieGuest()
			} else {
				cookies, err = FreshCookieLogin()
			}
			return err == nil
		})
		if err != nil {
			logger.Errorf("FreshCookie error %v", err)
			return
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

	// 保留所有 Cookie
	opt := []requests.Option{}

	// 确定要使用的 SUB 值
	var useSub string
	if sub != "" {
		useSub = sub
		logger.Infof("使用传入的 SUB 参数：%s...", sub[:min(20, len(sub))])
	} else if configuredSub := GetSettingCookie(); configuredSub != "" {
		useSub = configuredSub
		logger.Infof("使用配置中的 SUB")
	}

	// 构建 Cookie 选项
	for _, cookie := range cookies {
		if cookie.Name == "SUB" && useSub != "" {
			// 使用指定的 SUB 替代
			opt = append(opt, requests.CookieOption("SUB", useSub))
		} else {
			opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
		}
	}

	// 如果没有找到 SUB 但需要使用指定的值，手动添加
	if useSub != "" {
		hasSub := false
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				hasSub = true
				break
			}
		}
		if !hasSub {
			opt = append(opt, requests.CookieOption("SUB", useSub))
		}
	} else {
		// 记录获取到的 SUB
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				logger.Infof("使用获取到的 SUB：%s...", cookie.Value[:min(20, len(cookie.Value))])
				break
			}
		}
	}

	visitorCookiesOpt.Store(opt)
	if isGuestMode() {
		logger.Infof("微博 Guest Cookie 已加载，共 %d 个", len(opt))
	} else {
		logger.Infof("微博 Login Cookie 已加载，共 %d 个", len(opt))
	}
}

func GetSettingCookie() string {
	return config.GlobalConfig.GetString("weibo.sub")
}

func GetQRLoginEnable() bool {
	return config.GlobalConfig.GetBool("weibo.qrlogin")
}
