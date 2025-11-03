package twitter

import (
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"net/http/cookiejar"
)

var (
	BaseURL   = []string{"https://lightbrd.com/", "https://nitter.net/"}
	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36 Edg/135.0.0.0"
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
}
