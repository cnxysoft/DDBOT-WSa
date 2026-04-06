package weibo

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	"github.com/sirupsen/logrus"
)

const (
	pathWeiboCN                = "https://weibo.cn/pub/"
	pathWeiboDesktop           = "https://weibo.com"
	pathPassportGenvisitorTest = "https://visitor.passport.weibo.cn/visitor/genvisitor2"
	pathPassportGenvisitorProd = "https://passport.weibo.com/visitor/genvisitor2"
)

var (
	genvisitorRegex = regexp.MustCompile(`\((.*)\)`)
)

func genvisitor(path string, params gout.H, externalOpts ...requests.Option) (*GenVisitorResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	var opts = []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.TimeoutOption(time.Second * 10),
	}
	opts = append(opts, externalOpts...)
	var result string
	err := requests.Get(path, params, &result, opts...)
	if err != nil {
		return nil, err
	}
	submatch := genvisitorRegex.FindStringSubmatch(result)
	if len(submatch) < 2 {
		logger.Errorf("genvisitorRegex submatch not found")
		return nil, fmt.Errorf("genvisitor response regex extract failed")
	}
	var resp = new(GenVisitorResponse)
	err = json.Unmarshal([]byte(submatch[1]), resp)
	if err != nil {
		logger.WithField("Content", submatch[1]).Errorf("genvisitor data unmarshal error %v", err)
		resp = nil
	}
	return resp, err
}

func genvisitorGuest(externalOpts ...requests.Option) (*GenVisitorResponse, error) {
	params := gout.H{
		"cb": "gen_callback",
	}
	return genvisitor(pathPassportGenvisitorTest, params, externalOpts...)
}

func genvisitorLogin(externalOpts ...requests.Option) (*GenVisitorResponse, error) {
	params := gout.H{
		"cb":   "visitor_gray_callback",
		"tid":  "",
		"from": "weibo",
	}
	return genvisitor(pathPassportGenvisitorProd, params, externalOpts...)
}

func refreshGuestCN(jar *cookiejar.Jar) error {
	return requests.Get(pathWeiboCN, nil, nil,
		requests.WithCookieJar(jar),
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second*10),
	)
}

func refreshLoginXsrfToken(jar *cookiejar.Jar) error {
	return requests.Get(pathWeiboDesktop, nil, nil,
		requests.WithCookieJar(jar),
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second*10),
	)
}

func FreshCookieGuest() ([]*http.Cookie, error) {
	jar, _ := cookiejar.New(nil)
	genVisitorResp, err := genvisitorGuest(requests.WithCookieJar(jar))
	if err != nil {
		logger.Errorf("genvisitor error %v", err)
		return nil, err
	}
	if genVisitorResp.GetRetcode() != 20000000 || !strings.Contains(genVisitorResp.GetMsg(), "succ") {
		logger.WithFields(logrus.Fields{
			"Msg":     genVisitorResp.GetMsg(),
			"Retcode": genVisitorResp.GetRetcode(),
		}).Errorf("incarnateResp error")
		return nil, fmt.Errorf("genvisitor response error %v - %v",
			genVisitorResp.GetRetcode(), genVisitorResp.GetMsg())
	}

	err = refreshGuestCN(jar)
	if err != nil {
		logger.Errorf("refreshGuestMobile error %v", err)
		return nil, err
	}

	cookieUrl, err := url.Parse(pathWeiboCN)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathWeiboCN))
	}
	return jar.Cookies(cookieUrl), nil
}

func FreshCookieLogin() ([]*http.Cookie, error) {
	jar, _ := cookiejar.New(nil)
	genVisitorResp, err := genvisitorLogin(requests.WithCookieJar(jar))
	if err != nil {
		logger.Errorf("genvisitor error %v", err)
		return nil, err
	}
	if genVisitorResp.GetRetcode() != 20000000 || !strings.Contains(genVisitorResp.GetMsg(), "succ") {
		logger.WithFields(logrus.Fields{
			"Msg":     genVisitorResp.GetMsg(),
			"Retcode": genVisitorResp.GetRetcode(),
		}).Errorf("incarnateResp error")
		return nil, fmt.Errorf("genvisitor response error %v - %v",
			genVisitorResp.GetRetcode(), genVisitorResp.GetMsg())
	}

	err = refreshLoginXsrfToken(jar)
	if err != nil {
		logger.Errorf("refreshLoginXsrfToken error %v", err)
		return nil, err
	}

	baseUrl, err := url.Parse(pathWeiboDesktop)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathWeiboDesktop))
	}
	cookieUrl, err := url.Parse(pathPassportGenvisitorProd)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathPassportGenvisitorProd))
	}
	cookies := jar.Cookies(cookieUrl)
	for _, cookie := range jar.Cookies(baseUrl) {
		if cookie.Name == "XSRF-TOKEN" || cookie.Name == "WBPSESS" {
			cookies = append(cookies, cookie)
		}
	}
	return cookies, nil
}

func FreshCookie() ([]*http.Cookie, error) {
	if isGuestMode() {
		return FreshCookieGuest()
	}
	return FreshCookieLogin()
}
