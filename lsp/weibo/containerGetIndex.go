package weibo

import (
	stdjson "encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	structpb "google.golang.org/protobuf/types/known/structpb"

	"github.com/Sora233/MiraiGo-Template/config"
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
	cookieOpts := CookieOption()
	opts := buildRequestOptions(CreateReferer(uid))
	opts = append(opts, cookieOpts...)
	opts = append(opts, SetXsrfToken(opts))

	profileResp := new(ApiContainerGetIndexProfileResponse)
	err := requests.Get(PathConcainerGetIndex_Profile_Login, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		markCookieUnhealthy(err)
		return nil, err
	}
	return profileResp, nil
}

func apiContainerGetIndexProfileGuest(uid int64) (*ApiContainerGetIndexProfileResponse, error) {
	opts := buildRequestOptions(CreateGuestReferer(uid))
	opts = append(opts, CookieOption()...)
	GetUserPage(uid, opts...)

	guestResp := new(apiContainerGetIndexGuestProfileResponse)
	err := requests.Get(PathContainerGetIndex_Guest, CreateGuestProfileParam(uid), &guestResp, opts...)
	if err != nil {
		markCookieUnhealthy(err)
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

	mobileResp := new(WeiboMobileProfileResponse)
	err := requests.Get(apiURL, nil, &mobileResp)
	if err != nil {
		return nil, err
	}
	return mobileResp.ToProfileResponse(), nil
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
	cookieOpts := CookieOption()
	opts := buildRequestOptions(CreateReferer(uid))
	opts = append(opts, cookieOpts...)
	opts = append(opts, SetXsrfToken(opts))

	profileResp := new(ApiContainerGetIndexCardsResponse)
	err := requests.Get(PathContainerGetIndex_Cards_Login, CreateParam(uid), &profileResp, opts...)
	if err != nil {
		// 检测是否是 Cookie 失效（返回 HTML）
		if isCookieFailure(err) {
			logger.Warnf("uid=%d: 检测到 Cookie 失效，尝试刷新会话 cookie", uid)
			if RefreshSessionCookie() {
				// 刷新成功，重新构建请求并重试
				cookieOpts = CookieOption()
				opts = buildRequestOptions(CreateReferer(uid))
				opts = append(opts, cookieOpts...)
				opts = append(opts, SetXsrfToken(opts))
				profileResp = new(ApiContainerGetIndexCardsResponse)
				err = requests.Get(PathContainerGetIndex_Cards_Login, CreateParam(uid), &profileResp, opts...)
				if err != nil {
					// 刷新后仍失败，可能是 SUB 过期
					if isCookieFailure(err) {
						logger.Errorf("uid=%d: 会话 cookie 刷新后仍返回 HTML，SUB 可能已过期", uid)
						NotifySUBExpired()
					} else {
						logger.Errorf("uid=%d: 刷新后重试仍失败: %v", uid, err)
					}
					markCookieUnhealthy(err)
					return nil, err
				}
				logger.Infof("uid=%d: Cookie 刷新后重试成功", uid)
			} else {
				// 刷新失败，可能是 SUB 过期
				logger.Errorf("uid=%d: 会话 cookie 刷新失败，SUB 可能已过期", uid)
				NotifySUBExpired()
				markCookieUnhealthy(err)
				return nil, err
			}
		} else {
			markCookieUnhealthy(err)
			logger.Errorf("uid=%d: 请求失败 - %v", uid, err)
			return nil, err
		}
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

	mobileResp := new(WeiboMobileCardsResponse)
	err := requests.Get(apiURL, nil, &mobileResp)
	if err != nil {
		return nil, err
	}
	return mobileResp.ToCardsResponse(), nil
}

func apiContainerGetIndexCardsGuest(uid int64) (*ApiContainerGetIndexCardsResponse, error) {
	// Guest 模式：使用自动生成的访客 Cookie
	cookieOpts := CookieOption()
	if len(cookieOpts) == 0 {
		logger.Warnf("uid=%d: 移动端 CookieOption 为空", uid)
	}

	opts := buildRequestOptions(CreateGuestReferer(uid))
	opts = append(opts, cookieOpts...)
	GetUserPage(uid, opts...)

	guestResp := new(apiContainerGetIndexGuestCardsResponse)
	err := requests.Get(PathContainerGetIndex_Guest, CreateGuestCardsParam(uid), &guestResp, opts...)
	if err != nil {
		markCookieUnhealthy(err)
		return nil, err
	}

	resp := guestResp.ToCardsResponse()

	// Guest 模式：处理错误码
	if !cfg.IsWeiboAPIMode() {
		if resp.GetOk() == -100 {
			// -100: 频率限制触发，暂停刷新 10 分钟
			logger.Warnf("uid=%d: 检测到 -100 错误（频率限制），暂停刷新 Cookie 10 分钟", uid)
			PauseRefreshOnRateLimit(time.Minute * 10)
			return resp, nil
		}
		if resp.GetOk() == 432 {
			// 432: 需要人机验证，正常刷新 Cookie
			logger.Warnf("uid=%d: 检测到 432 错误（人机验证），尝试刷新", uid)
			if !TryRefreshGuestCookie() {
				cookieHealthy.Store(false)
				consecutiveCookieFails.Inc()
				return resp, nil
			}
			// 刷新成功后重试一次
			// 随机延迟 1-3s，避免固定间隔被检测
			time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)
			cookieOpts = CookieOption()
			opts = buildRequestOptions(CreateGuestReferer(uid))
			opts = append(opts, cookieOpts...)
			GetUserPage(uid, opts...)
			guestResp := new(apiContainerGetIndexGuestCardsResponse)
			err = requests.Get(PathContainerGetIndex_Guest, CreateGuestCardsParam(uid), &guestResp, opts...)
			if err != nil {
				markCookieUnhealthy(err)
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
		requests.AddUAOption(GetVisitorUA()),
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

// ToProfileResponse 将移动端用户资料转换为通用 Profile 响应格式
func (r *WeiboMobileProfileResponse) ToProfileResponse() *ApiContainerGetIndexProfileResponse {
	resp := &ApiContainerGetIndexProfileResponse{Ok: r.Ok}
	if r.Data == nil {
		return resp
	}
	data := &ApiContainerGetIndexProfileResponse_Data{}
	if r.Data.UserInfo != nil {
		data.User = &ApiContainerGetIndexProfileResponse_Data_UserInfo{
			Id:              r.Data.UserInfo.Id,
			ScreenName:      r.Data.UserInfo.ScreenName,
			ProfileImageUrl: r.Data.UserInfo.ProfileImageUrl,
			ProfileUrl:      r.Data.UserInfo.ProfileUrl,
		}
	}
	resp.Data = data
	return resp
}

// ToCardsResponse 将移动端卡片响应转换为通用卡片响应格式
func (r *WeiboMobileCardsResponse) ToCardsResponse() *ApiContainerGetIndexCardsResponse {
	resp := &ApiContainerGetIndexCardsResponse{Ok: r.Ok}
	if r.Data == nil {
		return resp
	}
	resp.Data = &ApiContainerGetIndexCardsResponse_Data{
		SinceId: strconv.FormatInt(r.Data.CardlistInfo.SinceId, 10),
		List:    r.Data.CardsToCardList(),
	}
	return resp
}

// CardsToCardList 将 MobileCard 列表转换为 Card 列表
func (r *WeiboMobileCardsResponse_Data) CardsToCardList() []*Card {
	var list []*Card
	for _, mc := range r.Cards {
		if mc.Mblog != nil {
			card := mc.ToCard()
			list = append(list, card)
		}
	}
	return list
}

// ToCard 将 MobileCard 转换为 Card
func (r *MobileCard) ToCard() *Card {
	if r.Mblog == nil {
		return nil
	}
	return r.Mblog.ToCard()
}

// ToCard 将 MobileMblog 转换为 Card
func (r *MobileMblog) ToCard() *Card {
	if r == nil {
		return nil
	}
	card := &Card{
		Id:         int64StrToInt64(r.Id),
		Mid:        r.Mid,
		Text:       r.Text,
		TextLength: r.TextLength,
		RawText:    r.RawText,
		CreatedAt:  r.CreatedAt,
		User:       r.User.ToUserInfo(),
		Mblogid:    r.Mblogid,
		Mblogtype:  CardType(r.Mblogtype),
		PicIds:     r.PicIds,
		PageInfo:   toCardPageInfo(r.PageInfo),
	}
	if r.Visible != nil {
		card.Visible = &Card_Visible{
			Type:   r.Visible.Type,
			ListId: r.Visible.ListId,
		}
	}
	if r.RetweetedStatus != nil {
		card.RetweetedStatus = r.RetweetedStatus.ToCard()
	}
	// 处理图片：优先用 pic_infos，否则从 pics 构造
	if len(r.PicInfos) > 0 {
		card.PicInfos = r.PicInfos
	} else if r.Pics != nil {
		card.PicInfos = extractPicsToPicInfos(r.Pics)
	}
	return card
}

// extractPicsToPicInfos 将移动端 pics 数组转换为 Card.PicInfo map
func extractPicsToPicInfos(v *structpb.Value) map[string]*Card_PicInfo {
	if v == nil {
		return nil
	}
	data, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	var pics []struct {
		Pid   string `json:"pid"`
		Url   string `json:"url"`
		Size  string `json:"size"`
		Large *struct {
			Url string `json:"url"`
		} `json:"large"`
	}
	if err := stdjson.Unmarshal(data, &pics); err != nil {
		return nil
	}
	result := make(map[string]*Card_PicInfo)
	for _, pic := range pics {
		info := &Card_PicInfo{
			Original: &Card_PicVariant{Url: pic.Url},
		}
		if pic.Large != nil {
			info.Large = &Card_PicVariant{Url: pic.Large.Url}
		}
		result[pic.Pid] = info
	}
	return result
}

// toCardPageInfo 将 google.protobuf.Value 转换为 *Card_PageInfo
func toCardPageInfo(v *structpb.Value) *Card_PageInfo {
	if v == nil {
		return nil
	}
	data, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	var pi struct {
		Type       interface{} `json:"type"`
		PageId     string      `json:"page_id"`
		ObjectId   string      `json:"object_id"`
		ObjectType interface{} `json:"object_type"`
		Content1   string      `json:"content1"`
		Content2   string      `json:"content2"`
		PagePic    *struct {
			Url string `json:"url"`
		} `json:"page_pic"`
		PageTitle string `json:"page_title"`
		ShortUrl  string `json:"short_url"`
		PageUrl   string `json:"page_url"`
		ActStatus int32  `json:"act_status"`
	}
	if err := stdjson.Unmarshal(data, &pi); err != nil {
		return nil
	}
	pageInfo := &Card_PageInfo{}
	if pi.PageId != "" {
		pageInfo.PageId = pi.PageId
	}
	if pi.ObjectId != "" {
		pageInfo.ObjectId = pi.ObjectId
	}
	if pi.PagePic != nil && pi.PagePic.Url != "" {
		pageInfo.PagePic = pi.PagePic.Url
	}
	if pi.PageTitle != "" {
		pageInfo.PageTitle = pi.PageTitle
	}
	if pi.Content1 != "" {
		pageInfo.Content1 = pi.Content1
	}
	if pi.Content2 != "" {
		pageInfo.Content2 = pi.Content2
	}
	if pi.ShortUrl != "" {
		pageInfo.ShortUrl = pi.ShortUrl
	}
	if pi.PageUrl != "" {
		pageInfo.ShortUrl = pi.PageUrl
	}
	if pi.ActStatus != 0 {
		pageInfo.ActStatus = pi.ActStatus
	}
	// type 字段用 Value 包装
	if pi.Type != nil {
		pageInfo.Type, _ = structpb.NewValue(pi.Type)
	}
	// object_type 可能是 string 或 int，转换为 string
	if pi.ObjectType != nil {
		switch v := pi.ObjectType.(type) {
		case string:
			pageInfo.ObjectType = v
		case float64:
			pageInfo.ObjectType = strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return pageInfo
}

// ToUserInfo 将 WeiboMobileUserInfo 转换为 ApiContainerGetIndexProfileResponse_Data_UserInfo
func (r *WeiboMobileUserInfo) ToUserInfo() *ApiContainerGetIndexProfileResponse_Data_UserInfo {
	if r == nil {
		return nil
	}
	return &ApiContainerGetIndexProfileResponse_Data_UserInfo{
		Id:              r.Id,
		ScreenName:      r.ScreenName,
		ProfileImageUrl: r.ProfileImageUrl,
		ProfileUrl:      r.ProfileUrl,
	}
}

// int64StrToInt64 转换字符串 ID 到 int64
func int64StrToInt64(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
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
		"type":        "uid",
		"value":       strconv.FormatInt(uid, 10),
	}
}

func CreateGuestCardsParam(uid int64) gout.H {
	return gout.H{
		"containerid": "107603" + strconv.FormatInt(uid, 10),
		"type":        "uid",
		"value":       strconv.FormatInt(uid, 10),
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

// isGuestMode 判断是否走访客模式。
//
// 与 cfg.GetWeiboMode 的差别：没显式配 weibo.mode 时，**运行时的 SUB 也算数**。
// 扫码登录的流程是 setRuntimeSUB → 刷新 Cookie → 最后才落盘，只认配置的话，
// 刷新 Cookie 那一刻会被判成 guest，白跑一次访客流程（那条路还要打外部服务，
// 实测能拖出 30 多秒）。
func isGuestMode() bool {
	if mode := strings.TrimSpace(config.GlobalConfig.GetString("weibo.mode")); mode != "" {
		return strings.EqualFold(mode, "guest")
	}
	if getRuntimeSUB() != "" {
		return false
	}
	return strings.EqualFold(cfg.GetWeiboMode(), "guest")
}

func markCookieUnhealthy(err error) {
	if err != nil && isCookieFailure(err) {
		cookieHealthy.Store(false)
		consecutiveCookieFails.Inc()
	}
}

// isCookieFailure 判断是否是 Cookie 失效导致的错误（API 返回了 HTML）
func isCookieFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid character '<'") ||
		strings.Contains(msg, "looking for beginning of value")
}

// isAuthFailure 判断错误是否属于明确的凭据失效（非网络层错误）
// 只把以下情况归类为凭据失效：
// - Cookie/SUB 失效返回登录页 HTML 导致的 JSON 解析失败（isCookieFailure）
// - HTTP 401/403 明确鉴权拒绝
// 其余情况（429 限流、414 请求错误、空响应/截断 JSON、5xx 服务端错误等）按临时错误
// 或状态未知处理，避免 429/414 等非鉴权错误错误触发凭据失效告警。
func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if isCookieFailure(err) {
		return true
	}
	// 微博明确拒绝凭据的 HTTP 状态码：401 未授权、403 禁止
	if strings.Contains(msg, "http code error 401") ||
		strings.Contains(msg, "http code error 403") {
		return true
	}
	return false
}

func GetUserPage(id int64, Opts ...requests.Option) error {
	return requests.Get(CreateGuestReferer(id), nil, nil, Opts...)
}
