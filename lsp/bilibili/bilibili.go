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
	"go.uber.org/atomic"
)

const (
	Site         = "bilibili"
	BaseHost     = "https://api.bilibili.com"
	BaseLiveHost = "https://api.live.bilibili.com"
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
	PathRelationModify:                  BaseHost,
	PathRelationFeedList:                BaseLiveHost,
	PathGetAttentionList:                BaseHost,
	PathPassportLoginWebKey:             PassportHost,
	PathPassportLoginOAuth2Login:        PassportHost,
	PathQRLoginGenerateQR:               PassportHost,
	PathQRLoginOAuth2Login:              PassportHost,
	PathXRelationStat:                   BaseHost,
	PathXWebInterfaceNav:                BaseHost,
	PathGetPlayTogetherUserAnchorInfoV2: BaseLiveHost,
	PathRoomInfo:                        BaseLiveHost,
	PathWebAreaList:                     BaseLiveHost,
	PathWebDynamicDetail:                BaseHost,
	PathWebDynamicDetailPic:             BaseHost,
	PathWebDynamicFeedAll:               BaseHost,
	PathWebDynamicFeedSpace:             BaseHost,
}

type VerifyInfo struct {
	SESSDATA   string
	BiliJct    string
	VerifyOpts []requests.Option
}

type ICode interface {
	GetCode() int32
}

var (
	ErrVerifyRequired = errors.New("账号信息缺失")
	atomicVerifyInfo  atomic.Pointer[VerifyInfo]

	mux                  = new(sync.Mutex)
	credentialPublishMu  sync.Mutex
	atomicAccount        atomic.Pointer[accountCredentials]
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
		useQRLogin = config.GlobalConfig.GetBool("bilibili.QRLogin")
	)
	if len(SESSDATA) != 0 && len(biliJct) != 0 {
		SetVerify(SESSDATA, biliJct)
		FreshSelfInfo()
	} else if useQRLogin {
		logger.Info("B站扫码登陆已开启")
		go func() {
			for {
				if IsCookieGiven() {
					return
				}
				// 创建扫码会话，预访问 bilibili.com 获取 buvid3/buvid4
				session := NewQRLoginSession()
				GetQRResp, err := GetQRCode(session)
				if err != nil {
					logger.Errorf("生成登陆二维码失败 - %v", err)
					time.Sleep(time.Second * 5)
					continue
				}
				logger.Info("已生成二维码，请扫码登陆~")
				for {
					time.Sleep(time.Second * 5)
					if IsCookieGiven() {
						return
					}
					CheckResp, err := QRLoginCheck(session, GetQRResp.Data.QrcodeKey)
					if err != nil {
						logger.Errorf("轮询扫码状态失败 - %v", err)
						continue
					}
					switch CheckResp.Data.Code {
					case QRPollSuccess:
						// 登录成功，优先从 cookie jar 提取，fallback 从 data.url 提取
						cookies, err := GetCookiesFromJar(session)
						if err != nil {
							logger.Warnf("从 cookie jar 提取失败，尝试从 data.url 提取: %v", err)
							cookies, err = GetCookies(CheckResp.Data.Url)
							if err != nil {
								logger.Errorf("解析 Cookies 失败 - %v", err)
								continue
							}
						}
						if cookies.SESSDATA == "" || cookies.BILI_JCT == "" {
							continue
						}
						prepared, err := PrepareCredentials(cookies.SESSDATA, cookies.BILI_JCT)
						if err != nil {
							logger.Errorf("扫码登录验证失败: %v", err)
							continue
						}
						if err := cfg.WriteConfigKeyValues(map[string]string{
							"bilibili.SESSDATA": cookies.SESSDATA, "bilibili.bili_jct": cookies.BILI_JCT,
						}); err != nil {
							logger.Errorf("保存 B站登录凭据失败: %v", err)
							continue
						}
						// During startup the transactional reload participant may not yet
						// be registered. Applying the already verified candidate is safe.
						if !transactionalReloadEnabled.Load() {
							prepared.Apply()
							config.GlobalConfig.MarkApplied("bilibili.SESSDATA", "bilibili.bili_jct")
						}
						logger.Info("B站扫码登录成功，已在当前进程生效")
						return
					case QRPollExpired:
						logger.Warn("二维码已失效，重新生成二维码")
						goto regenerate
					case QRPollWaiting:
						// 等待扫码，继续轮询
					case QRPollScanned:
						// 已扫码，等待确认，继续轮询
					default:
						logger.Errorf("扫码异常状态: code=%d message=%s", CheckResp.Data.Code, CheckResp.Data.Message)
						goto regenerate
					}
				}
			regenerate:
			}
		}()
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
	credentialPublishMu.Lock()
	defer credentialPublishMu.Unlock()
	atomicVerifyInfo.Store(&VerifyInfo{
		SESSDATA:   _SESSDATA,
		BiliJct:    _biliJct,
		VerifyOpts: []requests.Option{requests.CookieOption("SESSDATA", _SESSDATA), requests.CookieOption("bili_jct", _biliJct)},
	})
}

func getVerify() *VerifyInfo {
	return atomicVerifyInfo.Load()
}

type accountCredentials struct{ username, password string }

func getAccount() *accountCredentials {
	if value := atomicAccount.Load(); value != nil {
		return value
	}
	return new(accountCredentials)
}

func SetAccount(username, password string) {
	atomicAccount.Store(&accountCredentials{username: username, password: password})
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
					expiringGeneration := getVerify()
					t := time.Until(time.Unix(expire, 0))
					if t <= 0 {
						logger.Info("当前Cookie已过期，即将自动刷新")
						atomicVerifyInfo.CompareAndSwap(expiringGeneration, new(VerifyInfo))
					} else {
						logger.Infof("当前Cookie还有 %v 时间过期，过期后会自动刷新", t)
						time.AfterFunc(t, func() {
							atomicVerifyInfo.CompareAndSwap(expiringGeneration, new(VerifyInfo))
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
	generation := getVerify()
	navResp, err := XWebInterfaceNav(true)
	credentialPublishMu.Lock()
	defer credentialPublishMu.Unlock()
	if getVerify() != generation {
		return
	}
	if err != nil {
		logger.Errorf("获取个人信息失败 - %v，b站功能可能无法使用", err)
	} else {
		if navResp.GetCode() != 0 {
			if isBilibiliLoginInvalidResponse(navResp.GetCode(), navResp.GetMessage()) {
				notifyBilibiliLoginExpired(bilibiliLoginSourceSelf)
			}
			logger.Errorf("获取个人信息失败 - %v %v", navResp.GetCode(), navResp.GetMessage())
		} else {
			if navResp.GetData().GetIsLogin() {
				markBilibiliLoginRecovered(bilibiliLoginSourceSelf)
				logger.Infof("B站启动成功，当前使用账号：UID:%v %v LV%v %v",
					navResp.GetData().GetMid(),
					navResp.GetData().GetVipLabel().GetText(),
					navResp.GetData().GetLevelInfo().GetCurrentLevel(),
					navResp.GetData().GetUname())
				if navResp.GetData().GetLevelInfo().GetCurrentLevel() >= 5 {
					logger.Warnf("注意：当前使用的B站账号为5级或以上，请注意使用b站订阅时，该账号会自动关注订阅的目标用户！" +
						"如果不想新增关注，请使用小号。")
				}
				accountUid.Store(navResp.GetData().GetMid())
				return
			} else {
				notifyBilibiliLoginExpired(bilibiliLoginSourceSelf)
				logger.Errorf("账号未登陆")
			}
		}
	}
	accountUid.Store(0)
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
	if getAccount().username == "" {
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
	account := getAccount()
	logger.Debug("freshAccountCookieInfo")
	if !IsAccountGiven() {
		return nil, errors.New("未设置帐号")
	}
	if ci, err := GetCookieInfo(account.username); err == nil {
		logger.Debug("GetCookieInfo from db ok")
		return ci, nil
	}
	logger.Debug("login to fresh cookie")
	resp, err := Login(account.username, account.password)
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
	if err = SetCookieInfo(account.username, resp.GetData().GetCookieInfo()); err != nil {
		logger.Errorf("SetCookieInfo error %v", err)
	} else {
		logger.Debug("SetCookieInfo ok")
	}
	return resp.GetData().GetCookieInfo(), nil
}
