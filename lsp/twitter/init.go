package twitter

import (
	"net/http/cookiejar"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

const (
	ModeAPI    = "api"
	ModeMirror = "mirror"
)

var (
	BaseURL     = []string{"https://nitter.tiekoetter.com/", "https://nitter.catsarch.com/"}
	UserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36 Edg/135.0.0.0"
	twitterAPI  *TwitterAPI
	TwitterMode = ModeMirror
)

func init() {
	concern.RegisterConcern(newConcern(concern.GetNotifyChan()))
}

func setCookies() {
	ua := config.GlobalConfig.GetString("twitter.userAgent")
	url := config.GlobalConfig.GetStringSlice("twitter.BaseUrl")
	Cookie, _ = cookiejar.New(nil)
	if ua != "" {
		UserAgent = ua
	}
	if len(url) > 0 {
		BaseURL = url
	}

	mode := config.GlobalConfig.GetString("twitter.mode")
	if mode == ModeAPI {
		TwitterMode = ModeAPI
	} else {
		TwitterMode = ModeMirror
	}

	if TwitterMode == ModeAPI {
		ct0 := config.GlobalConfig.GetString("twitter.ct0")
		authToken := config.GlobalConfig.GetString("twitter.auth_token")
		bearerToken := config.GlobalConfig.GetString("twitter.bearerToken")
		queryId := config.GlobalConfig.GetString("twitter.queryId")
		screenName := config.GlobalConfig.GetString("twitter.screenName")

		twitterAPI = NewTwitterAPI(ct0, authToken, bearerToken, queryId, screenName)

		// 自动获取 screenName 和 main.js URL - 一次请求获取所有数据
		if twitterAPI != nil && twitterAPI.IsEnabled() {
			if screenName == "" {
				logger.Info("Cookie验证：正在获取账号信息...")
				maxRetries := 10
				retryInterval := time.Second * 3
				var mainJsUrl string
				for i := 0; i < maxRetries; i++ {
					sn, mjUrl, err := twitterAPI.FetchInitialState()
					if err == nil && sn != "" {
						twitterAPI.screenName = sn
						mainJsUrl = mjUrl
						logger.Infof("Cookie验证成功！账号: %s", sn)
						break
					} else if err != nil {
						logger.Warnf("Cookie验证第%d/%d次失败: %v", i+1, maxRetries, err)
					} else {
						logger.Warnf("Cookie验证第%d/%d次失败: screenName为空", i+1, maxRetries)
					}
					if i < maxRetries-1 {
						logger.Infof("%v后重试...", retryInterval)
						time.Sleep(retryInterval)
					} else {
						logger.Error("Cookie验证超时，Twitter功能已禁用")
						twitterAPI = nil
						TwitterMode = ModeMirror
					}
				}

				// 获取 queryId 和 Bearer token (使用已获取的 mainJsUrl)
				if twitterAPI != nil && twitterAPI.IsEnabled() && mainJsUrl != "" {
					logger.Info("正在从 main.js 获取最新的 queryId 和 Bearer token...")
					if err := RefreshAPIFromMainJSWithUrl(mainJsUrl); err != nil {
						logger.Warnf("获取 queryId/Bearer 失败，使用默认配置: %v", err)
					} else {
						logger.Infof("成功获取 queryId: %s", twitterAPI.queryId)
					}
				}
			} else {
				logger.Infof("使用配置的screenName: %s", screenName)
				twitterAPI.screenName = screenName

				// 也获取最新的 queryId 和 Bearer token
				logger.Info("正在从 main.js 获取最新的 queryId 和 Bearer token...")
				if err := RefreshAPIFromMainJS(); err != nil {
					logger.Warnf("获取 queryId/Bearer 失败，使用默认配置: %v", err)
				} else {
					logger.Infof("成功获取 queryId: %s", twitterAPI.queryId)
				}
			}
		}
	}
}

func IsTwitterEnabled() bool {
	return TwitterMode == ModeAPI && twitterAPI != nil && twitterAPI.IsEnabled()
}

func IsMirrorMode() bool {
	return TwitterMode == ModeMirror
}
