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
	)
	opts = append(opts, CookieOption()...)
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
	)
	opts = append(opts, CookieOption()...)
	profileResp := new(ApiContainerGetIndexCardsResponse)
	err := requests.Get(path, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		return nil, err
	}
	return profileResp, nil
}

func CreateParam(uid int64) gout.H {
	return gout.H{
		"uid":          strconv.FormatInt(uid, 10),
		"page":         "1",
		"x-xsrf-token": getXsrfToken(CookieOption()),
	}
}

// getXsrfToken 从options中提取XSRF-TOKEN cookie值
func getXsrfToken(opts []requests.Option) string {
	// 使用我们新添加的ExtractCookieOption函数来提取XSRF-TOKEN
	return requests.ExtractCookieOption(opts, "XSRF-TOKEN")
}

func CreateReferer(uid int64) string {
	return "https://weibo.com/u/" + strconv.FormatInt(uid, 10)
}
