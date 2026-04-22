package weibo

import (
	"fmt"
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
	if cfg.IsWeiboAPIMode() {
		return apiContainerGetIndexProfileAPI(uid)
	}
	if isGuestMode() {
		return apiContainerGetIndexProfileGuest(uid)
	}
	return apiContainerGetIndexProfileLogin(uid)
}

func apiContainerGetIndexProfileLogin(uid int64) (*ApiContainerGetIndexProfileResponse, error) {
	opts := buildRequestOptions(CreateReferer(uid))
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

// apiContainerGetIndexProfileAPI 通过外部 API 获取用户资料
func apiContainerGetIndexProfileAPI(uid int64) (*ApiContainerGetIndexProfileResponse, error) {
	baseURL := cfg.GetWeiboAPIModeBaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("未配置微博 API 模式基础地址")
	}
	apiURL := fmt.Sprintf("%s/api/Weibo/GetMobileProfile?uid=%d", baseURL, uid)

	profileResp := new(ApiContainerGetIndexProfileResponse)
	err := requests.Get(apiURL, nil, &profileResp)
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
	if cfg.IsWeiboAPIMode() {
		return apiContainerGetIndexCardsAPI(uid)
	}
	if isGuestMode() {
		return apiContainerGetIndexCardsGuest(uid)
	}
	// Login 模式：使用桌面端 API
	return apiContainerGetIndexCardsLogin(uid)
}

func apiContainerGetIndexCardsLogin(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	// 获取 CookieOption
	cookieOpts := CookieOption()
	if len(cookieOpts) == 0 {
		logger.Warnf("uid=%d: CookieOption 为空，未加载任何 Cookie", uid)
	} else {
		subValue := requests.ExtractCookieOption(cookieOpts, "SUB")
		if subValue != "" {
			logger.Debugf("uid=%d: 使用 SUB=%s...", uid, subValue[:min(20, len(subValue))])
		} else {
			logger.Warnf("uid=%d: CookieOption 中未找到 SUB", uid)
		}
	}

	// 构建请求选项：先添加基础选项
	opts := buildRequestOptions(CreateReferer(uid))

	// 然后添加 Cookie
	opts = append(opts, cookieOpts...)

	// 最后从完整的 opts 中提取 XSRF-TOKEN（这样就能从 Cookie 中提取了）
	opts = append(opts, SetXsrfToken(opts))

	// 调试：打印使用的 XSRF-TOKEN
	xsrfToken := requests.ExtractCookieOption(cookieOpts, "XSRF-TOKEN")
	if xsrfToken != "" {
		logger.Debugf("uid=%d: 使用 XSRF-TOKEN=%s", uid, xsrfToken)
	} else {
		logger.Warnf("uid=%d: 未找到 XSRF-TOKEN", uid)
	}

	profileResp := new(ApiContainerGetIndexCardsResponse)
	err := requests.Get(PathContainerGetIndex_Cards_Login, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		// 调试：打印错误详情
		logger.Errorf("uid=%d: 请求失败 - %v", uid, err)

		// 尝试获取原始响应内容，看看返回了什么
		var rawResp map[string]interface{}
		rawErr := requests.Get(PathContainerGetIndex_Cards_Login, CreateParam(uid), &rawResp, opts...)
		if rawErr != nil {
			logger.Warnf("uid=%d: 无法解析为 JSON，可能返回了 HTML", uid)
		}

		// 如果是 API 模式且请求失败，提示用户检查 baseURL 配置
		if cfg.IsWeiboAPIMode() {
			logger.Warnf("uid=%d: API 模式请求失败，请检查 apiModeBaseURL 配置是否正确", uid)
		}
		return nil, err
	}

	// 调试：检查返回的 OK 状态
	if profileResp.GetOk() != 1 {
		logger.Warnf("uid=%d: API 返回非成功状态 ok=%d", uid, profileResp.GetOk())
	}

	return profileResp, nil
}

// apiContainerGetIndexCardsAPI 通过外部 API 获取用户微博卡片列表
func apiContainerGetIndexCardsAPI(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	baseURL := cfg.GetWeiboAPIModeBaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("未配置微博 API 模式基础地址")
	}
	apiURL := fmt.Sprintf("%s/api/Weibo/GetMobileCards?uid=%d", baseURL, uid)

	cardsResp := new(ApiContainerGetIndexCardsResponse)
	err := requests.Get(apiURL, nil, &cardsResp)
	if err != nil {
		return nil, err
	}
	return cardsResp, nil
}

func apiContainerGetIndexCardsGuest(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	// Guest 模式：使用自动生成的访客 Cookie
	cookieOpts := CookieOption()

	opts := buildRequestOptions(CreateGuestReferer(uid))
	opts = append(opts, cookieOpts...)

	guestResp := new(apiContainerGetIndexGuestCardsResponse)
	err := requests.Get(PathContainerGetIndex_Guest, CreateGuestCardsParam(uid), &guestResp, opts...)
	if err != nil {
		return nil, err
	}

	resp := guestResp.ToCardsResponse()

	// 如果是 Guest 模式且返回 -100 错误，尝试刷新 Cookie 并重试一次
	if !cfg.IsWeiboAPIMode() && resp.GetOk() == -100 {
		logger.Warnf("uid=%d: 检测到 -100 错误（Cookie 失效），尝试刷新", uid)
		if TryRefreshGuestCookie() {
			// 刷新成功后重试一次
			cookieOpts = CookieOption()
			opts = buildRequestOptions(CreateGuestReferer(uid))
			opts = append(opts, cookieOpts...)

			guestResp = new(apiContainerGetIndexGuestCardsResponse)
			err = requests.Get(PathContainerGetIndex_Guest, CreateGuestCardsParam(uid), &guestResp, opts...)
			if err != nil {
				return nil, err
			}
			resp = guestResp.ToCardsResponse()
		}
	}

	return resp, nil
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
