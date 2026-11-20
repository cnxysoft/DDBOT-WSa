package bilibili

import (
	"strconv"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

const (
	// PathWebDynamicFeedAll 网页新版动态完整列表，取代已下线的 dynamic_new / dynamic_history
	PathWebDynamicFeedAll = "/x/polymer/web-dynamic/v1/feed/all"
	// PathWebDynamicFeedSpace 指定用户的空间动态，取代已下线的 space_history
	PathWebDynamicFeedSpace = "/x/polymer/web-dynamic/v1/feed/space"

	// polymerFeedFeatures 与网页端保持一致的特性开关，缺少时部分字段不会下发
	polymerFeedFeatures = "itemOpusStyle,opusBigCover,onlyfansVote,endFooterHidden," +
		"decorationCard,onlyfansAssetsV2,ugcDelete,onlyfansQaCard,editable," +
		"opusPrivateVisible,avatarAutoTheme"
)

// polymerFeedCommonOpts 三个接口共用的请求选项。
// 旧接口用的 t.bilibili.com origin/referer 对新接口不再合适，这里对齐网页端。
func polymerFeedCommonOpts() []requests.Option {
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.HeaderOption("origin", "https://www.bilibili.com"),
		requests.HeaderOption("referer", "https://www.bilibili.com/"),
		AddUAOption(),
		requests.TimeoutOption(time.Second*15),
		delete412ProxyOption,
	)
	// 只带 SESSDATA/bili_jct 时 feed/space 会被风控返回 412 + HTML（实测），
	// 必须补上 buvid3 等设备 Cookie。这里直接复用 refreshCookieJar 预访问 www 拿到的 jar。
	if jar := cj.Load(); jar != nil {
		opts = append(opts, requests.WithCookieJar(jar))
	}
	return opts
}

// WebDynamicFeedAll 拉取登录账号的完整动态列表。
// updateBaseline 传空表示首次建立基线；传上一次返回的基线则只取该水位之后的内容。
func WebDynamicFeedAll(updateBaseline string) (*polymerFeedResponse, error) {
	if !IsVerifyGiven() {
		return nil, ErrVerifyRequired
	}
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", time.Since(st))
	}()

	params := map[string]string{
		"type":            "all",
		"page":            "1",
		"offset":          "",
		"update_baseline": updateBaseline,
		"features":        polymerFeedFeatures,
	}
	signWbi(params)

	opts := polymerFeedCommonOpts()
	opts = append(opts, GetVerifyOption()...)

	resp := new(polymerFeedResponse)
	if err := requests.Get(BPath(PathWebDynamicFeedAll), params, resp, opts...); err != nil {
		return nil, err
	}
	return resp, nil
}

// WebDynamicFeedSpace 拉取指定用户的空间动态，offset 为空表示第一页。
func WebDynamicFeedSpace(hostMid int64, offset string) (*polymerFeedResponse, error) {
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", time.Since(st))
	}()

	params := map[string]string{
		"host_mid": strconv.FormatInt(hostMid, 10),
		"offset":   offset,
		"need_top": "0",
		"features": polymerFeedFeatures,
	}
	signWbi(params)

	opts := polymerFeedCommonOpts()
	opts = append(opts,
		requests.HeaderOption("referer", "https://space.bilibili.com/"+strconv.FormatInt(hostMid, 10)+"/dynamic"),
	)
	opts = append(opts, GetVerifyOption()...)

	resp := new(polymerFeedResponse)
	if err := requests.Get(BPath(PathWebDynamicFeedSpace), params, resp, opts...); err != nil {
		return nil, err
	}
	return resp, nil
}
