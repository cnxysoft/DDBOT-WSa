package weibo

import (
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
)

const (
	PathConcainerGetIndex_Profile_Login = "https://weibo.com/ajax/profile/info"
	PathContainerGetIndex_Cards_Login   = "https://weibo.com/ajax/statuses/mymblog"
	PathContainerGetIndex_Guest         = "https://m.weibo.cn/api/container/getIndex"
)

func ApiContainerGetIndexProfile(uid int64) (*ApiContainerGetIndexProfileResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	if isGuestMode() {
		return apiContainerGetIndexProfileGuest(uid)
	}
	return apiContainerGetIndexProfileLogin(uid)
}

func apiContainerGetIndexProfileLogin(uid int64) (*ApiContainerGetIndexProfileResponse, error) {
	opts := buildRequestOptions(CreateReferer(uid))
	opts = append(opts, requests.CookieOption("SUB", GetSettingCookie()))
	opts = append(opts, CookieOption()...)
	opts = append(opts, SetXsrfToken(opts))

	profileResp := new(ApiContainerGetIndexProfileResponse)
	err := requests.Get(PathConcainerGetIndex_Profile_Login, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		return nil, err
	}
	return profileResp, nil
}

func apiContainerGetIndexProfileGuest(uid int64) (*ApiContainerGetIndexProfileResponse, error) {
	opts := buildRequestOptions(CreateGuestReferer(uid))
	opts = append(opts, CookieOption()...)

	guestResp := new(apiContainerGetIndexGuestProfileResponse)
	err := requests.Get(PathContainerGetIndex_Guest, CreateGuestProfileParam(uid), &guestResp, opts...)
	if err != nil {
		return nil, err
	}
	return guestResp.ToProfileResponse(), nil
}

func ApiContainerGetIndexCards(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	if isGuestMode() {
		return apiContainerGetIndexCardsGuest(uid)
	}
	return apiContainerGetIndexCardsLogin(uid)
}

func apiContainerGetIndexCardsLogin(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	opts := buildRequestOptions(CreateReferer(uid))
	opts = append(opts, requests.CookieOption("SUB", GetSettingCookie()))
	opts = append(opts, CookieOption()...)
	opts = append(opts, SetXsrfToken(opts))

	profileResp := new(ApiContainerGetIndexCardsResponse)
	err := requests.Get(PathContainerGetIndex_Cards_Login, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		return nil, err
	}
	return profileResp, nil
}

func apiContainerGetIndexCardsGuest(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	opts := buildRequestOptions(CreateGuestReferer(uid))
	opts = append(opts, CookieOption()...)

	guestResp := new(apiContainerGetIndexGuestCardsResponse)
	err := requests.Get(PathContainerGetIndex_Guest, CreateGuestCardsParam(uid), &guestResp, opts...)
	if err != nil {
		return nil, err
	}
	return guestResp.ToCardsResponse(), nil
}

func buildRequestOptions(referer string) []requests.Option {
	return []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.TimeoutOption(time.Second * 10),
		requests.HeaderOption("referer", referer),
	}
}

type apiContainerGetIndexGuestProfileResponse struct {
	Ok   int32                                         `json:"ok"`
	Data *apiContainerGetIndexGuestProfileResponseData `json:"data"`
}

type apiContainerGetIndexGuestProfileResponseData struct {
	UserInfo *ApiContainerGetIndexProfileResponse_Data_UserInfo `json:"userInfo"`
	User     *ApiContainerGetIndexProfileResponse_Data_UserInfo `json:"user"`
}

func (r *apiContainerGetIndexGuestProfileResponse) ToProfileResponse() *ApiContainerGetIndexProfileResponse {
	resp := &ApiContainerGetIndexProfileResponse{Ok: r.Ok}
	if r.Data == nil {
		return resp
	}
	data := &ApiContainerGetIndexProfileResponse_Data{}
	if r.Data.UserInfo != nil {
		data.User = r.Data.UserInfo
	} else {
		data.User = r.Data.User
	}
	resp.Data = data
	return resp
}

type apiContainerGetIndexGuestCardsResponse struct {
	Ok   int32                                       `json:"ok"`
	Data *apiContainerGetIndexGuestCardsResponseData `json:"data"`
}

type apiContainerGetIndexGuestCardsResponseData struct {
	Cards []apiContainerGetIndexGuestCard `json:"cards"`
}

type apiContainerGetIndexGuestCard struct {
	Mblog     *Card                           `json:"mblog"`
	CardGroup []apiContainerGetIndexGuestCard `json:"card_group"`
}

func (r *apiContainerGetIndexGuestCardsResponse) ToCardsResponse() *ApiContainerGetIndexCardsResponse {
	resp := &ApiContainerGetIndexCardsResponse{Ok: r.Ok}
	if r.Data == nil {
		return resp
	}
	var list []*Card
	for _, card := range r.Data.Cards {
		appendGuestCards(&list, card)
	}
	resp.Data = &ApiContainerGetIndexCardsResponse_Data{List: list}
	return resp
}

func appendGuestCards(target *[]*Card, card apiContainerGetIndexGuestCard) {
	if card.Mblog != nil {
		*target = append(*target, card.Mblog)
	}
	for _, group := range card.CardGroup {
		if group.Mblog != nil {
			*target = append(*target, group.Mblog)
		}
	}
}

func CreateParam(uid int64) gout.H {
	return gout.H{
		"uid":  strconv.FormatInt(uid, 10),
		"page": "1",
	}
}

func CreateGuestProfileParam(uid int64) gout.H {
	return gout.H{
		"containerid": "100505" + strconv.FormatInt(uid, 10),
	}
}

func CreateGuestCardsParam(uid int64) gout.H {
	return gout.H{
		"containerid": "107603" + strconv.FormatInt(uid, 10),
	}
}

func SetXsrfToken(opts []requests.Option) requests.Option {
	xsrf := requests.ExtractCookieOption(opts, "XSRF-TOKEN")
	return requests.HeaderOption("x-xsrf-token", xsrf)
}

func CreateReferer(uid int64) string {
	return "https://weibo.com/u/" + strconv.FormatInt(uid, 10)
}

func CreateGuestReferer(uid int64) string {
	return "https://m.weibo.cn/u/" + strconv.FormatInt(uid, 10)
}

func isGuestMode() bool {
	return strings.EqualFold(cfg.GetWeiboMode(), "guest")
}
