package acfun

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	jsoniter "github.com/json-iterator/go"
)

var (
	ErrVerifyRequired = errors.New("账号信息缺失")
	json              = jsoniter.ConfigCompatibleWithStandardLibrary
	mux               = new(sync.Mutex)
	username          string
	password          string
	accountUid        atomic.Int64
	atomicVerifyInfo  atomic.Pointer[VerifyInfo]
)

const (
	Site              = "acfun"
	BaseHost          = "https://live.acfun.cn"
	IdHost            = "https://id.app.acfun.cn"
	ApiHost           = "https://api.acfunchina.com"
	CompactExpireTime = time.Minute * 60
	UserAgent         = "acvideo core/6.77.0.1306(Xiaomi;24129PN74C;15) aegon/1.39.2-1-g3de73b77-curl"
)

var BasePath = map[string]string{
	PathApiChannelList:  BaseHost,
	PathApiLogin:        IdHost,
	PathApiUserInfo:     ApiHost,
	PathApiPersonalInfo: ApiHost,
	PathApiFollow:       ApiHost,
	PathApiGetFollows:   ApiHost,
	PathApiIsFollowing:  ApiHost,
	PathApiFollowFeedV2: ApiHost,
}

type VerifyInfo struct {
	AuthKey     int64
	AcPassToken string
	VerifyOpts  []requests.Option
}

func APath(path string) string {
	if strings.HasPrefix(path, "/") {
		return BasePath[path] + path
	} else {
		return BasePath[path] + "/" + path
	}
}

func LiveUrl(uid int64) string {
	return "https://live.acfun.cn/live/" + strconv.FormatInt(uid, 10)
}

func DynamicUrl(dynamic string) string {
	return "https://www.acfun.cn/moment/am" + dynamic
}

func VideoUrl(vid string) string {
	return "https://www.acfun.cn/v/ac" + vid
}

func Init() {
	var (
		AuthKey     = config.GlobalConfig.GetInt64("acfun.authKey")
		AcPassToken = config.GlobalConfig.GetString("acfun.acPassToken")
	)
	if AuthKey != 0 && len(AcPassToken) != 0 {
		SetVerify(AuthKey, AcPassToken)
		FreshSelfInfo()
	}
	SetAccount(config.GlobalConfig.GetString("acfun.account"), config.GlobalConfig.GetString("acfun.password"))
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

func SetVerify(_AuthKey int64, _AcPassToken string) {
	atomicVerifyInfo.Store(&VerifyInfo{
		AuthKey:     _AuthKey,
		AcPassToken: _AcPassToken,
		VerifyOpts:  []requests.Option{requests.CookieOption("auth_key", strconv.FormatInt(_AuthKey, 10)), requests.CookieOption("acPasstoken", _AcPassToken)},
	})
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

func IsAccountGiven() bool {
	if username == "" {
		return false
	}
	return true
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
			AuthKey     int64
			AcPassToken string
			ok          bool
		)
		logger.Debug("GetVerifyInfo 使用帐号刷新cookie")
		cookie, err := freshAccountCookieInfo()
		if err != nil {
			logger.Errorf("A站登陆失败，请手动指定cookie配置 - freshAccountCookieInfo error %v", err)
		} else {
			logger.Debug("A站登陆成功 - freshAccountCookieInfo ok")
			AuthKey = cookie.GetAuthKey()
			logger.Debug("使用cookieInfo设置 AuthKey")
			AcPassToken = cookie.GetAcPassToken()
			logger.Debug("使用cookieInfo设置 AcPassToken")
			if AuthKey == 0 || len(AcPassToken) == 0 {
				logger.Errorf("A站登陆成功，但是设置cookie失败，如果发现这个问题，请反馈给开发者。")
			} else {
				ok = true
				SetVerify(AuthKey, AcPassToken)
				FreshSelfInfo()
			}
		}
		if !ok {
			SetVerify(0, "wrong")
			FreshSelfInfo()
		}
	}
	return getVerify()
}

func FreshSelfInfo() {
	Resp, err := GetUserPersonalInfo()
	if err != nil {
		logger.Errorf("获取个人信息失败 - %v，A站功能可能无法使用", err)
	} else {
		if Resp.GetResult() != 0 {
			logger.Errorf("获取个人信息失败 - %v", Resp.GetResult())
		} else {
			if Resp.GetInfo() != nil {
				info := Resp.GetInfo()
				logger.Infof("A站启动成功，当前使用账号：UID:%v LV%v %v",
					info.GetUserId(),
					info.GetLevel(),
					info.GetUserName())
				if info.Level >= 5 {
					logger.Warnf("注意：当前使用的A站账号为5级或以上，请注意使用A站订阅时，该账号会自动关注订阅的目标用户！" +
						"如果不想新增关注，请使用小号。")
				}
				accountUid.Store(info.GetUserId())
				return
			} else {
				logger.Errorf("账号未登陆")
			}
		}
	}
	accountUid.Store(0)
}

func freshAccountCookieInfo() (*LoginResponse, error) {
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
	if resp.GetResult() != 0 {
		logger.Errorf("Login error %v - %v", resp.GetResult(), resp.GetErrorMsg())
		return nil, fmt.Errorf("login error %v - %v", resp.GetResult(), resp.GetErrorMsg())
	}
	logger.Debug("login success")
	if err = SetCookieInfo(username, resp); err != nil {
		logger.Errorf("SetCookieInfo error %v", err)
	} else {
		logger.Debug("SetCookieInfo ok")
	}
	return resp, nil
}

func GetGeneralOptions(withCookie bool) []requests.Option {
	opts := []requests.Option{
		requests.TimeoutOption(10 * time.Second),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(UserAgent),
		requests.RetryOption(3),
	}
	if withCookie {
		opts = append(opts, GetVerifyOption()...)
	}
	return opts
}

func GetEncodeOption() requests.Option {
	return requests.HeaderOption("Accept-Encoding", "gzip, deflate, br")
}
