package bilibili

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

const (
	Site         = "bilibili"
	BaseHost     = "https://api.bilibili.com"
	BaseLiveHost = "https://api.live.bilibili.com"
	BaseVCHost   = "https://api.vc.bilibili.com"
	VideoView    = "https://www.bilibili.com/video"
	DynamicView  = "https://www.bilibili.com/opus"
	PassportHost = "https://passport.bilibili.com"

	CompactExpireTime = time.Minute * 60
	// followerNotifyCap 提示粉丝数过少的阈值
	followerNotifyCap = 50
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

var BasePath = map[string]string{
	PathXSpaceAccInfo:                   BaseHost,
	PathDynamicSrvSpaceHistory:          BaseVCHost,
	PathDynamicSrvDynamicNew:            BaseVCHost,
	PathRelationModify:                  BaseHost,
	PathRelationFeedList:                BaseLiveHost,
	PathGetAttentionList:                BaseVCHost,
	PathPassportLoginWebKey:             PassportHost,
	PathPassportLoginOAuth2Login:        PassportHost,
	PathQRLoginGenerateQR:               PassportHost,
	PathQRLoginOAuth2Login:              PassportHost,
	PathXRelationStat:                   BaseHost,
	PathXWebInterfaceNav:                BaseHost,
	PathDynamicSrvDynamicHistory:        BaseVCHost,
	PathGetPlayTogetherUserAnchorInfoV2: BaseLiveHost,
	PathRoomInfo:                        BaseLiveHost,
	PathWebAreaList:                     BaseLiveHost,
	PathWebDynamicDetail:                BaseHost,
	PathWebDynamicDetailPic:             BaseHost,
	PathWebDynamicFeedAll:               BaseHost,
	PathWebDynamicFeedAllUpdate:         BaseHost,
}

// VerifyInfo 是登录凭据快照。
// 成品类型元数据（recovered/types/types-closure-x64.tsv）：
// struct 88B = SESSDATA/BiliJct/DedeUserID/Buvid3 4 个 string + VerifyOpts []requests.Option，
// 没有 ac_time_value 字段；ac_time_value 只作为配置键 bilibili.ac_time_value 存在。
type VerifyInfo struct {
	SESSDATA   string
	BiliJct    string
	DedeUserID string
	Buvid3     string
	VerifyOpts []requests.Option
}

type ICode interface {
	GetCode() int32
}

var (
	ErrVerifyRequired = errors.New("账号信息缺失")
	atomicVerifyInfo  atomic.Pointer[VerifyInfo]

	mux                  = new(sync.Mutex)
	username             string
	password             string
	accountUid           atomic.Int64
	wbi                  atomic.Pointer[WebInterfaceNavResponse_Data_WbiImg]
	delete412ProxyOption = func() requests.Option {
		return requests.ProxyCallbackOption(func(out interface{}, proxy string) {
			if out == nil {
				return
			}
			if c, ok := out.(ICode); ok && (c.GetCode() == -412 || c.GetCode() == 412) {
				proxy_pool.Delete(proxy)
			}
		})
	}()
	mixinKeyEncTab = []int{
		46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49,
		33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13, 37, 48, 7, 16, 24, 55, 40,
		61, 26, 17, 0, 1, 60, 51, 30, 4, 22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11,
		36, 20, 34, 44, 52,
	}
)

func Init() {
	var (
		SESSDATA   = config.GlobalConfig.GetString("bilibili.SESSDATA")
		biliJct    = config.GlobalConfig.GetString("bilibili.bili_jct")
		dedeUserID = config.GlobalConfig.GetString("bilibili.DedeUserID")
		buvid3     = config.GlobalConfig.GetString("bilibili.buvid3")
		// 成品 Init @0xcee485 调 cfg.GetBilibiliQRLoginEnabled（键 bilibili.qrlogin，
		// 未配置时默认 true），不是裸 GetBool（未配置会得到 false）。
		useQRLogin = cfg.GetBilibiliQRLoginEnabled()
	)
	// 成品 Init(bilibili.go:94) 的三条模式文案（funcstr @0xcee3c0，level 由
	// logrus.(*Entry).Log 的 ebx 实证）：客户端启动 Info、"扫码（默认）" Info、
	// 未知模式 Warn 并回退扫码。
	if isBilibiliCookieCloudMode() {
		// 成品 Init(bilibili.go:94, @0xcee3c0) 该分支 @0xcee557 先做 SESSDATA/verify 段：
		// @0xcee50b 判 SESSDATA/bili_jct 两个 len 非空 → newVerifyInfoWithCookies +
		// atomic.StorePointer，@0xcee582 打 "CookieCloud 首次获取成功前，将临时使用配置文件
		// 中的B站Cookie"(len 79)，@0xcee5ab FreshSelfInfo，之后才 load。loader 失败会直接
		// return（@0xcee7b4），故 SESSDATA 段必须先于 load，否则会被 return 吞掉。
		if len(SESSDATA) != 0 && len(biliJct) != 0 {
			atomicVerifyInfo.Store(newVerifyInfoWithCookies(SESSDATA, biliJct, dedeUserID, buvid3))
			logger.Info("CookieCloud 首次获取成功前，将临时使用配置文件中的B站Cookie")
			FreshSelfInfo()
		}
		cloudConfig, err := loadCookieCloudConfig()
		if err != nil {
			// 成品 Init @0xcee651..@0xcee7b4：loader 出错时
			// logrus.(*Entry).WithFields(error).Log(level=2=Error,
			//   "B站 Cookie来源已设为 CookieCloud，但配置不完整" @0x1125210,len 59)
			// 随后直接 return（@0xcee7b4）。SESSDATA 段已在上方完成，此 return 不吞扫码段。
			logger.WithError(err).Error("B站 Cookie来源已设为 CookieCloud，但配置不完整")
			return
		}
		// 成品 Init @0xceea07 WithFields({cookie_source,server_host,refresh_interval,crypto_type})
		// .Log(level=4=Info, "B站 CookieCloud 客户端已启动，将立即获取并定时刷新Cookie" @0x1125230)；
		// refresh_interval=config.Interval，crypto_type=config.CryptoType。
		logger.WithFields(logrus.Fields{
			"cookie_source":    cookieModeCookieCloud,
			"server_host":      cookieCloudServerHost(cloudConfig.Endpoint),
			"refresh_interval": cloudConfig.Interval,
			"crypto_type":      cloudConfig.CryptoType,
		}).Info("B站 CookieCloud 客户端已启动，将立即获取并定时刷新Cookie")
		// 成品 Init @0xceea80 call refreshCookieFromCookieCloud(config)（config 只 load 一次，
		// 同一份 config 在 @0xceeda0 再传 startCookieCloudRefresh）；失败按 level=2=Error 记
		// "CookieCloud 首次获取B站Cookie失败，将按配置间隔继续重试"（@0x1125240,len 73）。
		if err := refreshCookieFromCookieCloud(cloudConfig); err != nil {
			// 成品 Init @0xcee847 WithFields({cookie_source,server_host})，
			// @0xcee968 再并入 error（内联 WithError），
			// Log(level=2=Error, "CookieCloud 首次获取B站Cookie失败，将按配置间隔继续重试" @0x1125240)。
			logger.WithFields(logrus.Fields{
				"cookie_source": cookieModeCookieCloud,
				"server_host":   cookieCloudServerHost(cloudConfig.Endpoint),
			}).WithError(err).Error("CookieCloud 首次获取B站Cookie失败，将按配置间隔继续重试")
		}
		startCookieCloudRefresh(cloudConfig)
		return
	}
	// 成品 Init @0xceedae：按 cookieMode 分派（getCookieMode 已把空值规整为 "qrcode"）。
	// 未知模式 @0xceeec1 WithFields({cookie_mode:<mode>}).Log(level=3=Warn
	// "未知的B站 Cookie来源模式，已回退到默认扫码模式" @0x1125250)，
	// 随后与 qrcode 分支共同落到 @0xceefd9 WithFields({cookie_source:"qrcode"})
	// .Log(level=4=Info "B站 Cookie来源模式：扫码（默认）" @0x1125270)——未知模式也会补打 Info。
	switch mode := strings.ToLower(strings.TrimSpace(cfg.GetBilibiliCookieMode())); mode {
	case "", cookieModeQrcode:
	default:
		logger.WithFields(logrus.Fields{"cookie_mode": mode}).
			Warn("未知的B站 Cookie来源模式，已回退到默认扫码模式")
	}
	logger.WithFields(logrus.Fields{"cookie_source": cookieModeQrcode}).
		Info("B站 Cookie来源模式：扫码（默认）")
	if len(SESSDATA) != 0 && len(biliJct) != 0 {
		// 成品 Init(bilibili.go:94, @0xcee3c0) 的 GetString 序列（@0xcee3e7/@0xcee412/
		// @0xcee43f/@0xcee467，长度 0x11/0x11/0x13/0xf）依次读 bilibili.SESSDATA /
		// bilibili.bili_jct / bilibili.DedeUserID / bilibili.buvid3；@0xcef016 处只以
		// 两个 len 判断就绪；@0xcef060 直接把 4 个 cookie 传给 newVerifyInfoWithCookies
		// 并 atomic.StorePointer（未经过 SetVerify——SetVerify @0xcef100 第 3、4 参为空串，
		// 仅用于其它登录路径）。
		atomicVerifyInfo.Store(newVerifyInfoWithCookies(SESSDATA, biliJct, dedeUserID, buvid3))
		FreshSelfInfo()
	} else if !isBilibiliCookieCloudMode() && useQRLogin {
		// Lite：扫码登录由管理员私聊确认后触发（authprompt），不再自动轮询
		logger.Info("B站扫码登陆已开启")
		// 成品 Init @0xcef07c：SESSDATA/bili_jct 任一为空且 QRLogin 开启时，
		// @0xcef085 取常量 0x10ac8ff(len 0xe="missing_cookie") 调
		// markBilibiliCookieUnhealthy(@0xd10460)，由 authprompt 私聊提醒管理员。
		markBilibiliCookieUnhealthy("missing_cookie")
	}
	SetAccount(config.GlobalConfig.GetString("bilibili.account"), config.GlobalConfig.GetString("bilibili.password"))
}

func BPath(path string) string {
	if strings.HasPrefix(path, "/") {
		return BasePath[path] + path
	} else {
		return BasePath[path] + "/" + path
	}
}

func BVIDUrl(bvid string) string {
	return fmt.Sprintf("%v/%v", VideoView, bvid)
}

func DynamicUrl(dynamicIdStr string) string {
	return fmt.Sprintf("%v/%v", DynamicView, dynamicIdStr)
}

func SetVerify(_SESSDATA string, _biliJct string) {
	// 成品 SetVerify(bilibili.go:164, 128B, @0xcef100) 反汇编：
	//   xor esi,esi / xor r8d,r8d / mov rsi,r9 / mov r8,r10 → 第 3、4 个 cookie 参数为空串
	//   call newVerifyInfoWithCookies(SESSDATA, biliJct, "", "")
	//   mov rax,rbx; lea 0x1bcc298(%rip),rax; call sync/atomic.StorePointer
	// 全程无 config 读取（无 cfg.GetString("bilibili.buvid3") 调用点）。
	// Buvid3 字段保留，值由 live_websocket 侧 fetchBilibiliBuvid3 补取后写回。
	info := newVerifyInfoWithCookies(_SESSDATA, _biliJct, "", "")
	atomicVerifyInfo.Store(info)
}

// bilibiliLoginReady 判断登录凭据是否就绪（纯内存检查，无网络访问）。
// 成品 bilibili.go:201 实现：verify != nil && TrimSpace(SESSDATA) != "" &&
// TrimSpace(BiliJct) != "" && accountUid > 0，共两次 strings.TrimSpace 调用。
func bilibiliLoginReady() bool {
	verify := getVerify()
	if verify == nil {
		return false
	}
	if strings.TrimSpace(verify.SESSDATA) == "" {
		return false
	}
	if strings.TrimSpace(verify.BiliJct) == "" {
		return false
	}
	return accountUid.Load() > 0
}

func getVerify() *VerifyInfo {
	return atomicVerifyInfo.Load()
}

func SetAccount(_username string, _password string) {
	username = _username
	password = _password
}

func GetVerifyOption() []requests.Option {
	info := GetVerifyInfo()
	if info == nil {
		return nil
	}
	return info.VerifyOpts
}

func GetVerifyBiliJct() string {
	info := GetVerifyInfo()
	if info == nil {
		return ""
	}
	return info.BiliJct
}

func GetVerifyInfo() *VerifyInfo {
	if IsCookieGiven() {
		return getVerify()
	}

	mux.Lock()
	defer mux.Unlock()

	if IsCookieGiven() {
		return getVerify()
	}

	if !IsAccountGiven() {
		logger.Trace("GetVerifyInfo error - 未设置cookie和帐号")
		return nil
	} else {
		var (
			SESSDATA string
			biliJct  string
			expire   int64 = -1
			ok       bool
		)
		logger.Debug("GetVerifyInfo 使用帐号刷新cookie")
		cookieInfo, err := freshAccountCookieInfo()
		if err != nil {
			logger.Errorf("b站登陆失败，请手动指定cookie配置 - freshAccountCookieInfo error %v", err)
		} else {
			logger.Debug("b站登陆成功 - freshAccountCookieInfo ok")
			for _, cookie := range cookieInfo.GetCookies() {
				if expire == -1 || expire > cookie.GetExpires() {
					expire = cookie.GetExpires()
				}
				if cookie.GetName() == "SESSDATA" {
					SESSDATA = cookie.GetValue()
					logger.Debug("使用cookieInfo设置 SESSDATA")
				}
				if cookie.GetName() == "bili_jct" {
					biliJct = cookie.GetValue()
					logger.Debug("使用cookieInfo设置 bili_jct")
				}
			}
			if len(SESSDATA) == 0 || len(biliJct) == 0 {
				logger.Errorf("b站登陆成功，但是设置cookie失败，如果发现这个问题，请反馈给开发者。")
			} else {
				ok = true
				SetVerify(SESSDATA, biliJct)
				FreshSelfInfo()
				if expire > 0 {
					// 尝试cookie过期之后自动刷新
					// 但是登陆方法有效期应该比cookie有效期短
					// 这样做真的值得吗
					t := time.Until(time.Unix(expire, 0))
					if t <= 0 {
						logger.Info("当前Cookie已过期，即将自动刷新")
						atomicVerifyInfo.Store(new(VerifyInfo))
					} else {
						logger.Infof("当前Cookie还有 %v 时间过期，过期后会自动刷新", t)
						time.AfterFunc(t, func() {
							atomicVerifyInfo.Store(new(VerifyInfo))
						})
					}
				}
			}
		}
		if !ok {
			SetVerify("wrong", "wrong")
			FreshSelfInfo()
		}
	}
	return getVerify()
}

func FreshSelfInfo() {
	navResp, err := XWebInterfaceNav(true)
	if err != nil {
		logger.Errorf("获取个人信息失败 - %v，b站功能可能无法使用", err)
		// 成品 bilibili.go:302 在 nav 失败时调用 markBilibiliCookieUnhealthy("nav_api_not_logged_in")
		markBilibiliCookieUnhealthy("nav_api_not_logged_in")
	} else if navResp.GetCode() != 0 {
		logger.Errorf("获取个人信息失败 - %v %v", navResp.GetCode(), navResp.GetMessage())
		markBilibiliCookieUnhealthy("nav_api_not_logged_in")
	} else if navResp.GetData().GetIsLogin() {
		acceptBilibiliSelfInfo(navResp)
		return
	} else {
		logger.Errorf("账号未登陆")
		markBilibiliCookieUnhealthy("nav_login_false")
	}
	accountUid.Store(0)
}

// acceptBilibiliSelfInfo 接受已登录的 nav 响应：打印账号信息、更新 UID、标记 Cookie 健康。
// 成品 bilibili.go:329 证据：两次 logrus Logf（成功信息 + LV>=5 提醒）、
// convT64/convTstring/convT32/convTstring 四个日志参数、并调用 markBilibiliCookieHealthy。
func acceptBilibiliSelfInfo(navResp *WebInterfaceNavResponse) {
	if navResp == nil {
		return
	}
	data := navResp.GetData()
	logger.Infof("B站启动成功，当前使用账号：UID:%v %v LV%v %v",
		data.GetMid(),
		data.GetVipLabel().GetText(),
		data.GetLevelInfo().GetCurrentLevel(),
		data.GetUname())
	if data.GetLevelInfo().GetCurrentLevel() >= 5 {
		logger.Warnf("注意：当前使用的B站账号为5级或以上，请注意使用b站订阅时，该账号会自动关注订阅的目标用户！如果不想新增关注，请使用小号。")
	}
	accountUid.Store(data.GetMid())
	markBilibiliCookieHealthy()
}

func AddUAOption() requests.Option {
	return requests.AddRandomUAOption(requests.Computer)
}

func AddReferOption(refer ...string) requests.Option {
	if len(refer) == 0 {
		return requests.HeaderOption("Referer", "https://www.bilibili.com/")
	}
	return requests.HeaderOption("Referer", refer[0])
}

func IsVerifyGiven() bool {
	if IsCookieGiven() || IsAccountGiven() {
		return true
	}
	return false
}

func IsCookieGiven() bool {
	v := atomicVerifyInfo.Load()
	if v == nil {
		return false
	}
	return len(v.VerifyOpts) > 0
}

func IsAccountGiven() bool {
	if username == "" {
		return false
	}
	return true
}

func ParseUid(s string) (int64, error) {
	// 手机端复制的时候会带上UID:前缀，所以支持这个格式
	s = strings.TrimPrefix(strings.ToUpper(s), "UID:")
	return strconv.ParseInt(s, 10, 64)
}

func freshAccountCookieInfo() (*LoginResponse_Data_CookieInfo, error) {
	logger.Debug("freshAccountCookieInfo")
	if !IsAccountGiven() {
		return nil, errors.New("未设置帐号")
	}
	if ci, err := GetCookieInfo(username); err == nil {
		logger.Debug("GetCookieInfo from db ok")
		return ci, nil
	}
	logger.Debug("login to fresh cookie")
	resp, err := Login(username, password)
	if err != nil {
		logger.Errorf("Login error %v", err)
		return nil, err
	}
	if resp.GetCode() != 0 {
		logger.Errorf("Login error %v - %v", resp.GetCode(), resp.GetMessage())
		return nil, fmt.Errorf("login error %v - %v", resp.GetCode(), resp.GetMessage())
	}
	if resp.GetData().GetStatus() != 0 {
		logger.Errorf("Login status error %v - %v", resp.GetData().GetStatus(), resp.GetData().GetMessage())
		return nil, fmt.Errorf("login status error %v - %v", resp.GetData().GetStatus(), resp.GetData().GetMessage())
	}
	logger.Debug("login success")
	if err = SetCookieInfo(username, resp.GetData().GetCookieInfo()); err != nil {
		logger.Errorf("SetCookieInfo error %v", err)
	} else {
		logger.Debug("SetCookieInfo ok")
	}
	return resp.GetData().GetCookieInfo(), nil
}
