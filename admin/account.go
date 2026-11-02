package admin

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/bilibili"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
)

// 平台登录态：Web 面板在「扫码登录」开着的时候用它替换掉凭据输入框。
//
// 凭据字段是给「手动填 Cookie」的人看的；开了扫码登录之后，用户关心的是
// 「现在登的是谁」，而不是那两串又长又没意义的 base64。
// 关掉扫码登录，凭据字段就回来（见 app.js 的 qrLoginOf）。

type platformAccount struct {
	Site     string `json:"site"`
	QRLogin  bool   `json:"qrLogin"`  // 该平台是否开着扫码登录
	LoggedIn bool   `json:"loggedIn"` // 是否已拿到可用凭据
	UID      string `json:"uid,omitempty"`
	Name     string `json:"name,omitempty"`
	Avatar   string `json:"avatar,omitempty"` // data:image/...;base64,... 见 avatarDataURL
	Level    int32  `json:"level,omitempty"`
	Vip      string `json:"vip,omitempty"`
	Message  string `json:"message,omitempty"`
	Mode     string `json:"mode,omitempty"` // 微博：guest / login / api
}

// accountCacheTTL 账号信息缓存时长：面板一进来就会问，B站那边要打两个接口，
// 不缓存的话来回切页面会把接口刷爆。
const accountCacheTTL = 60 * time.Second

var (
	accountMu     sync.Mutex
	accountCached = make(map[string]platformAccount)
	accountAt     = make(map[string]time.Time)
)

func cachedAccount(site string) (platformAccount, bool) {
	accountMu.Lock()
	defer accountMu.Unlock()
	acc, ok := accountCached[site]
	if !ok || time.Since(accountAt[site]) > accountCacheTTL {
		return platformAccount{}, false
	}
	return acc, true
}

func storeAccount(site string, acc platformAccount) {
	accountMu.Lock()
	defer accountMu.Unlock()
	accountCached[site] = acc
	accountAt[site] = time.Now()
}

// invalidateAccountCache 扫码登录成功后调用，让面板立刻看到新账号。
func invalidateAccountCache(site string) {
	accountMu.Lock()
	defer accountMu.Unlock()
	delete(accountCached, site)
	delete(accountAt, site)
}

// handlePlatformAccount GET /api/v1/platform/account?site=bilibili|weibo
func (s *Server) handlePlatformAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	site := r.URL.Query().Get("site")
	switch site {
	case loginPlatformBilibili, loginPlatformWeibo:
	default:
		writeJSONError(w, http.StatusBadRequest, "unsupported site: "+site)
		return
	}

	acc, ok := cachedAccount(site)
	if !ok {
		if site == loginPlatformBilibili {
			acc = fetchBilibiliAccount()
		} else {
			acc = fetchWeiboAccount()
		}
		storeAccount(site, acc)
	}
	_ = json.NewEncoder(w).Encode(acc)
}

func fetchBilibiliAccount() platformAccount {
	acc := platformAccount{
		Site:    loginPlatformBilibili,
		QRLogin: config.GlobalConfig.GetBool("bilibili.qrlogin"),
	}

	sessdata := strings.TrimSpace(config.GlobalConfig.GetString("bilibili.SESSDATA"))
	jct := strings.TrimSpace(config.GlobalConfig.GetString("bilibili.bili_jct"))
	if sessdata == "" || jct == "" {
		acc.Message = "尚未配置 Cookie"
		return acc
	}

	// 面板可能在 bot 上线之前就被打开：bilibili.Init()（它负责 SetVerify）要等
	// OneBot 连上才跑，那之前 verify 信息是空的，nav 接口会直接报「账号信息缺失」。
	// 这里按配置补一次；已经有运行态凭据就不动它，免得覆盖更新的值。
	if !bilibili.IsVerifyGiven() {
		bilibili.SetVerify(sessdata, jct)
	}

	nav, err := bilibili.XWebInterfaceNav(true)
	if err != nil {
		acc.Message = "查询账号信息失败：" + err.Error()
		return acc
	}
	if nav.GetCode() != 0 || !nav.GetData().GetIsLogin() {
		acc.Message = "Cookie 已失效，请重新扫码登录"
		return acc
	}

	acc.LoggedIn = true
	acc.UID = strconv.FormatInt(nav.GetData().GetMid(), 10)
	acc.Name = nav.GetData().GetUname()
	acc.Level = nav.GetData().GetLevelInfo().GetCurrentLevel()
	acc.Vip = nav.GetData().GetVipLabel().GetText()

	// nav 接口不带头像，头像在空间信息接口里；拿不到就算了，不影响卡片其余部分
	if info, err := bilibili.XSpaceAccInfo(nav.GetData().GetMid()); err == nil && info.GetData() != nil {
		face := httpsURL(info.GetData().GetFace())
		// 要小图：原图 80KB，面板上只画 44px。hdslb 支持 @<w>w_<h>h_1c.webp 缩放后缀。
		acc.Avatar = avatarDataURL(face+"@96w_96h_1c.webp", face)
		if acc.Name == "" {
			acc.Name = info.GetData().GetName()
		}
	}
	return acc
}

func fetchWeiboAccount() platformAccount {
	acc := platformAccount{
		Site:    loginPlatformWeibo,
		QRLogin: weibo.GetQRLoginEnable(),
		// 微博有三种模式（guest / login / api），默认游客。配上 SUB 之后
		// GetWeiboMode() 会自动切到 login —— 把当前模式报给面板，省得靠猜。
		Mode: cfg.GetWeiboMode(),
	}

	sub := strings.TrimSpace(weibo.GetSettingCookie())
	if sub == "" {
		acc.Message = "尚未配置 SUB"
		return acc
	}
	acc.LoggedIn = true
	acc.Message = "SUB 已配置"

	// 昵称 / 头像：先拿 uid（登录态响应头 x-log-uid），再复用项目里已有的资料接口
	// ApiContainerGetIndexProfile（它本来就返回 screen_name / profile_image_url）。
	// 任一步失败都保留上面的「SUB 已配置」，不让卡片空掉。
	uid, err := weibo.GetSelfUid()
	if err != nil {
		acc.Message = "SUB 已配置（取账号信息失败：" + err.Error() + "）"
		return acc
	}
	acc.UID = strconv.FormatInt(uid, 10)

	profile, err := weibo.GetSelfProfile(uid)
	if err != nil {
		acc.Message = "SUB 已配置（取昵称头像失败：" + err.Error() + "）"
		return acc
	}
	acc.Name = profile.ScreenName
	acc.Avatar = avatarDataURL(httpsURL(profile.ProfileImageUrl), "")
	if acc.Name != "" {
		acc.Message = ""
	}
	return acc
}

// avatarDataURL 把头像抓回来内嵌成 data URL。
//
// 为什么不让浏览器直接加载头像地址：
//   - 微博的 sinaimg 对**任何浏览器 UA** 都返回 403（实测 Chrome / Safari / Firefox /
//     iPhone / Android 全部 403，而空 UA、curl、Go-http-client 都是 200）。
//     浏览器必然带 UA，所以面板里那个头像永远打不开。
//   - 它的 ssig 签名链接还有时效，过期即失效。
//
// 服务端取回来内嵌，前端 img src 用 data: 一律能显示。
//
// preferred 抓不到时退回 fallback（比如缩放后缀不被支持）。
func avatarDataURL(preferred, fallback string) string {
	if d := fetchAsDataURL(preferred); d != "" {
		return d
	}
	if fallback != "" && fallback != preferred {
		return fetchAsDataURL(fallback)
	}
	return ""
}

// fetchAsDataURL 用非浏览器 UA 取图并转 data URL。
// 故意不设 User-Agent：Go 默认的 Go-http-client 正好是 sinaimg 放行的那一类。
func fetchAsDataURL(u string) string {
	if u == "" {
		return ""
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	resp, err := avatarHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || len(body) == 0 {
		return ""
	}
	mime := resp.Header.Get("Content-Type")
	if i := strings.Index(mime, ";"); i > 0 {
		mime = mime[:i]
	}
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body)
}

var avatarHTTPClient = &http.Client{Timeout: 10 * time.Second}

// httpsURL 把 B站 返回的 http 头像地址换成 https：
// 面板可能跑在 https 下，混合内容会被浏览器拦掉。
func httpsURL(u string) string {
	if strings.HasPrefix(u, "http://") {
		return "https://" + strings.TrimPrefix(u, "http://")
	}
	return u
}
