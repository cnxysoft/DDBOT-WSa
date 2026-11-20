package bilibili

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/skip2/go-qrcode"
)

const (
	PathQRLoginOAuth2Login = "/x/passport-login/web/qrcode/poll"
	PathQRLoginGenerateQR  = "/x/passport-login/web/qrcode/generate"
	BilibiliHomePage       = "https://www.bilibili.com/"
)

// QR poll 状态码
const (
	QRPollSuccess = 0     // 登录成功
	QRPollWaiting = 86101 // 等待扫码
	QRPollScanned = 86090 // 已扫码，等待确认
	QRPollExpired = 86038 // 二维码已失效
)

// QRLoginSession 维护扫码登录过程中的 cookie jar
type QRLoginSession struct {
	jar http.CookieJar
}

// NewQRLoginSession 预访问 bilibili.com 获取 buvid3/buvid4 等设备级 Cookie
func NewQRLoginSession() *QRLoginSession {
	jar, _ := cookiejar.New(nil)
	var dummy struct{}
	err := requests.Get(BilibiliHomePage, nil, &dummy,
		requests.ProxyOption(proxy_pool.PreferNone),
		AddUAOption(),
		requests.TimeoutOption(time.Second*10),
		requests.WithCookieJar(jar),
	)
	if err != nil {
		logger.Debugf("预访问 bilibili.com 失败（可继续）: %v", err)
	}
	return &QRLoginSession{jar: jar}
}

// GetQRCodeQuiet 取二维码，但不写 qrcode.png、也不往控制台打字符画。
// Web 面板会把二维码直接画给用户看，控制台再打一份只是噪音（还会写进日志页）。
func GetQRCodeQuiet(session *QRLoginSession) (*GetQRCodeResponse, error) {
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		AddUAOption(),
		AddReferOption(),
		requests.TimeoutOption(time.Second*10),
		requests.RetryOption(3),
	)
	if session != nil {
		opts = append(opts, requests.WithCookieJar(session.jar))
	}
	var GetQRCodeResp = new(GetQRCodeResponse)
	err := requests.Get(BPath(PathQRLoginGenerateQR), nil, GetQRCodeResp, opts...)
	if err != nil {
		return nil, err
	}
	if GetQRCodeResp.Code != 0 {
		return nil, errors.New(GetQRCodeResp.Message)
	}
	return GetQRCodeResp, nil
}

// GetQRCode 取二维码并输出到控制台（终端场景：写 qrcode.png + 打字符画）。
func GetQRCode(session *QRLoginSession) (*GetQRCodeResponse, error) {
	GetQRCodeResp, err := GetQRCodeQuiet(session)
	if err != nil {
		return nil, err
	}
	err = qrcode.WriteFile(GetQRCodeResp.Data.Url, qrcode.Low, 256, "qrcode.png")
	if err != nil {
		return nil, err
	}
	logger.Info("若无法识别下方二维码，请打开qrcode.png扫描~")
	qrCode, err := qrcode.New(GetQRCodeResp.Data.Url, 0)
	if err != nil {
		return nil, err
	}
	qrCodeString := qrCode.ToSmallString(true)
	fmt.Println(qrCodeString)
	return GetQRCodeResp, nil
}

// QRLoginCheck 轮询扫码状态，返回完整响应（包括非零状态码如 86101/86090）
// 调用方应根据 QRLoginResp.Data.Code 判断状态
func QRLoginCheck(session *QRLoginSession, token string) (*QRLoginResponse, error) {
	if token == "" {
		return nil, errors.New("查询的Token为空")
	}
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		AddUAOption(),
		AddReferOption(),
		requests.TimeoutOption(time.Second*10),
		requests.RetryOption(3),
	)
	if session != nil {
		opts = append(opts, requests.WithCookieJar(session.jar))
	}
	var QRLoginResp = new(QRLoginResponse)
	err := requests.Get(BPath(PathQRLoginOAuth2Login), map[string]string{
		"qrcode_key": token,
	}, QRLoginResp, opts...)
	if err != nil {
		return nil, err
	}
	return QRLoginResp, nil
}

type BiliCookies struct {
	SESSDATA string
	BILI_JCT string
}

// GetCookiesFromJar 从 session 的 cookie jar 中提取 SESSDATA 和 bili_jct
func GetCookiesFromJar(session *QRLoginSession) (*BiliCookies, error) {
	if session == nil {
		return nil, errors.New("session 为空")
	}
	var bc BiliCookies
	// bilibili 的 cookie 通常设置在 .bilibili.com 域下
	for _, domain := range []string{"https://www.bilibili.com/", "https://passport.bilibili.com/"} {
		u, _ := url.Parse(domain)
		for _, c := range session.jar.Cookies(u) {
			switch c.Name {
			case "SESSDATA":
				if bc.SESSDATA == "" {
					bc.SESSDATA = c.Value
				}
			case "bili_jct":
				if bc.BILI_JCT == "" {
					bc.BILI_JCT = c.Value
				}
			}
		}
	}
	if bc.SESSDATA == "" || bc.BILI_JCT == "" {
		return nil, errors.New("cookie jar 中未找到 SESSDATA 或 bili_jct")
	}
	return &bc, nil
}

// GetCookies 从 URL 中解析 SESSDATA 和 bili_jct（兼容旧 API 返回 data.url 的场景）
func GetCookies(Url string) (*BiliCookies, error) {
	if Url == "" {
		return nil, errors.New("url 为空")
	}
	parsedURL, err := url.Parse(Url)
	if err != nil {
		return nil, err
	}
	queryParams := parsedURL.Query()
	var cookies BiliCookies
	for key, values := range queryParams {
		if key == "SESSDATA" {
			cookies.SESSDATA = values[0]
		} else if key == "bili_jct" {
			cookies.BILI_JCT = values[0]
		}
	}
	if cookies.SESSDATA == "" || cookies.BILI_JCT == "" {
		return nil, errors.New("URL 参数中未找到完整的 SESSDATA 或 bili_jct")
	}
	return &cookies, nil
}
