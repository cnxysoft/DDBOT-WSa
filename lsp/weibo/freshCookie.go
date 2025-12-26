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
	pathWeibo              = "https://weibo.com"
	pathPassportGenvisitor = "https://passport.weibo.com/visitor/genvisitor2"
)

var (
	genvisitorRegex = regexp.MustCompile(`\((.*)\)`)
)

func genvisitor(externalOpts ...requests.Option) (*GenVisitorResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	params := gout.H{
		"cb":   "visitor_gray_callback",
		"tid":  "",
		"from": `weibo`,
	}
	var opts = []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.TimeoutOption(time.Second * 10),
	}
	opts = append(opts, externalOpts...)
	var result string
	err := requests.Get(pathPassportGenvisitor, params, &result, opts...)
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

func refreshXsrfToken(jar *cookiejar.Jar) error {
	return requests.Get(pathWeibo, nil, nil,
		requests.WithCookieJar(jar),
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second*10),
	)
}

func FreshCookie() ([]*http.Cookie, error) {
	jar, _ := cookiejar.New(nil)
	genVisitorResp, err := genvisitor(requests.WithCookieJar(jar))
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

	err = refreshXsrfToken(jar)
	if err != nil {
		logger.Errorf("refreshXsrfToken error %v", err)
		return nil, err
	}

	baseUrl, err := url.Parse(pathWeibo)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathWeibo))
	}
	cookieUrl, err := url.Parse(pathPassportGenvisitor)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathPassportGenvisitor))
	}
	cookies := jar.Cookies(cookieUrl)
	for _, cookie := range jar.Cookies(baseUrl) {
		if cookie.Name == "XSRF-TOKEN" || cookie.Name == "WBPSESS" {
			cookies = append(cookies, cookie)
		}
	}
	return cookies, nil
}
