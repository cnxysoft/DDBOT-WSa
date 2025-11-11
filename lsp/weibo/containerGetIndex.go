package weibo

import (
	"strconv"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
)

const (
	PathConcainerGetIndex_Profile = "https://weibo.com/ajax/profile/info"
	PathContainerGetIndex_Cards   = "https://weibo.com/ajax/statuses/mymblog"
)

func ApiContainerGetIndexProfile(uid int64) (*ApiContainerGetIndexProfileResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	path := PathConcainerGetIndex_Profile

	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.TimeoutOption(time.Second*10),
		requests.HeaderOption("referer", CreateReferer(uid)),
		requests.CookieOption("SUB", GetSettingCookie()),
	)
	opts = append(opts, CookieOption()...)
	opts = append(opts, SetXsrfToken(opts))
	profileResp := new(ApiContainerGetIndexProfileResponse)
	err := requests.Get(path, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		return nil, err
	}
	return profileResp, nil
}

func ApiContainerGetIndexCards(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	path := PathContainerGetIndex_Cards
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.TimeoutOption(time.Second*10),
		requests.HeaderOption("referer", CreateReferer(uid)),
		requests.CookieOption("SUB", GetSettingCookie()),
	)
	opts = append(opts, CookieOption()...)
	opts = append(opts, SetXsrfToken(opts))
	profileResp := new(ApiContainerGetIndexCardsResponse)
	err := requests.Get(path, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		return nil, err
	}
	return profileResp, nil
}

func CreateParam(uid int64) gout.H {
	return gout.H{
		"uid":  strconv.FormatInt(uid, 10),
		"page": "1",
	}
}

func SetXsrfToken(opts []requests.Option) requests.Option {
	xsrf := requests.ExtractCookieOption(opts, "XSRF-TOKEN")
	return requests.HeaderOption("x-xsrf-token", xsrf)
}

func CreateReferer(uid int64) string {
	return "https://weibo.com/u/" + strconv.FormatInt(uid, 10)
}
