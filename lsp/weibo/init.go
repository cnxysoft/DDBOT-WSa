package weibo

import (
	"net/http"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
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
	localutils.Retry(3, time.Second, func() bool {
		cookies, err = FreshCookie()
		return err == nil
	})
	if err != nil {
		logger.Errorf("FreshCookie error %v", err)
	} else {
		var opt []requests.Option
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
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
