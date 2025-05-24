package bilibili

import (
	"errors"
	"fmt"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/skip2/go-qrcode"
	"net/url"
	"time"
)

const (
	PathQRLoginOAuth2Login = "/x/passport-login/web/qrcode/poll"
	PathQRLoginGenerateQR  = "/x/passport-login/web/qrcode/generate"
)

func GetQRCode() (*GetQRCodeResponse, error) {
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		AddUAOption(),
		AddReferOption(),
		requests.TimeoutOption(time.Second*10),
		requests.RetryOption(3),
	)
	var GetQRCodeResp = new(GetQRCodeResponse)
	err := requests.Get(BPath(PathQRLoginGenerateQR), nil, GetQRCodeResp, opts...)
	if err != nil {
		return nil, err
	}
	if GetQRCodeResp.Code != 0 {
		return nil, errors.New(GetQRCodeResp.Message)
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

func QRLoginCheck(token string) (*QRLoginResponse, error) {
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
	var QRLoginResp = new(QRLoginResponse)
	err := requests.Get(BPath(PathQRLoginOAuth2Login), map[string]string{
		"qrcode_key": token,
	}, QRLoginResp, opts...)
	if err != nil {
		return nil, err
	}
	if QRLoginResp.Data.Code != 0 {
		return nil, errors.New(QRLoginResp.Data.Message)
	}
	return QRLoginResp, nil
}

type BiliCookies struct {
	SESSDATA string
	BILI_JCT string
}

func GetCookies(Url string) (*BiliCookies, error) {
	parsedURL, err := url.Parse(Url)
	if err != nil {
		fmt.Println("解析 URL 失败:", err)
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
	return &cookies, nil
}
