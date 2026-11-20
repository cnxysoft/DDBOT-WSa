package requests

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/guonaihong/gout"
	"github.com/guonaihong/gout/dataflow"
	"github.com/guonaihong/gout/middler"
)

type RespHeader struct {
	Server             string `header:"Server" json:"server"`
	Date               string `header:"Date" json:"date"`
	ContentDisposition string `header:"Content-Disposition" json:"content_disposition"`
	ContentLength      string `header:"Content-Length" json:"content_length"`
	ContentEncoding    string `header:"Content-Encoding" json:"content_encoding"`
	ContentType        string `header:"Content-Type" json:"content_type"`
	Connection         string `header:"Connection" json:"connection"`
}

var logger = utils.GetModuleLogger("request")

var defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36 Edg/142.0.0.0"

type option struct {
	Timeout            time.Duration
	InsecureSkipVerify bool

	Debug               bool
	Cookies             []*http.Cookie
	Header              gout.H
	Proxy               string
	HttpCode            *int
	Retry               int
	ProxyCallbackOption func(out interface{}, proxy string)
	CookieJar           http.CookieJar
	ResponseMiddleware  []middler.ResponseMiddler
	AutoHeaderHost      bool
	AutoHeaderReferer   bool
	NotIgnoreEmpty      bool
	Transport           *http.Transport
	HTTP2               bool
	proxyConfigured     bool
	proxyError          error
	requestTransport    *http.Transport
	selectedProxy       *url.URL
}

func (o *option) getGout() *gout.Client {
	var goutOpts []gout.Option
	if o.Timeout != 0 {
		goutOpts = append(goutOpts, gout.WithTimeout(o.Timeout))
	} else {
		goutOpts = append(goutOpts, gout.WithTimeout(time.Second*15))
	}
	if o.HTTP2 || o.CookieJar != nil || o.Transport != nil || o.proxyConfigured || o.InsecureSkipVerify {
		tr := o.Transport
		if o.proxyConfigured || o.HTTP2 || o.InsecureSkipVerify {
			if tr != nil {
				tr = tr.Clone()
			} else if base, ok := http.DefaultTransport.(*http.Transport); ok {
				tr = base.Clone()
			} else {
				tr = &http.Transport{}
			}
			o.requestTransport = tr
		}
		if o.proxyConfigured {
			// Candidate off means direct, even if the environment or a supplied
			// transport has a proxy. Native ProxyURL also preserves SOCKS routing
			// with DialContext, which gout's legacy SetSOCKS5 does not override.
			tr.Proxy = nil
			if o.selectedProxy != nil {
				tr.Proxy = http.ProxyURL(o.selectedProxy)
			}
		}
		if o.HTTP2 {
			tr.ForceAttemptHTTP2 = true
		}
		if o.InsecureSkipVerify {
			if tr.TLSClientConfig == nil {
				tr.TLSClientConfig = &tls.Config{}
			} else {
				tr.TLSClientConfig = tr.TLSClientConfig.Clone()
			}
			tr.TLSClientConfig.InsecureSkipVerify = true
		}
		client := &http.Client{Jar: o.CookieJar}
		if tr != nil {
			client.Transport = tr
		}
		goutOpts = append(goutOpts, gout.WithClient(client))
	}
	df := gout.NewWithOpt(goutOpts...)
	if o.NotIgnoreEmpty {
		df.NotIgnoreEmpty = true
	}
	return df
}

type Option func(o *option)

func empty(*option) {}

func HttpCookieOption(cookie *http.Cookie) Option {
	return func(o *option) {
		o.Cookies = append(o.Cookies, cookie)
	}
}

func CookieOption(name, value string) Option {
	return HttpCookieOption(&http.Cookie{Name: name, Value: value})
}

func TimeoutOption(d time.Duration) Option {
	return func(o *option) {
		o.Timeout = d
	}
}

func HttpCodeOption(code *int) Option {
	return func(o *option) {
		o.HttpCode = code
	}
}

func HeaderOption(key, value string) Option {
	return func(o *option) {
		if o.Header == nil {
			o.Header = make(gout.H)
		}
		o.Header[key] = value
	}
}

func AddUAOption(ua ...string) Option {
	if len(ua) > 0 && len(ua[0]) > 0 {
		return HeaderOption("user-agent", ua[0])
	}
	return HeaderOption("user-agent", defaultUA)
}

func AddRandomUAOption(entry FakeUAEntry) Option {
	return HeaderOption("user-agent", RandomUA(entry))
}

func ProxyOption(prefer proxy_pool.Prefer) Option {
	if prefer == proxy_pool.PreferNone {
		return empty
	}
	proxy, err := proxy_pool.Get(prefer)
	if err != nil {
		if err != proxy_pool.ErrNil {
			logger.Errorf("get proxy failed: %v", err)
		}
		return empty
	} else {
		return func(o *option) {
			o.Proxy = proxy.ProxyString()
		}
	}
}

// ProxyOptionWithGetter resolves the selection when a request starts, so an
// option retained by a client follows the getter's commit and abort lifecycle.
// A nil getter uses the running pool with the legacy fallback behavior.
// An injected getter fails closed on errors other than explicit direct ErrNil.
func ProxyOptionWithGetter(prefer proxy_pool.Prefer, getter proxy_pool.Getter) Option {
	if getter == nil {
		return func(o *option) { ProxyOption(prefer)(o) }
	}
	return func(o *option) {
		o.Proxy, o.proxyError, o.proxyConfigured = "", nil, true
		proxy, err := getter(prefer)
		if err != nil {
			if errors.Is(err, proxy_pool.ErrNil) {
				return
			}
			o.proxyError = fmt.Errorf("proxy selection failed: %w", err)
			return
		}
		if proxy == nil {
			o.proxyError = errors.New("proxy selector returned no proxy")
			return
		}
		if prefer == proxy_pool.PreferNone {
			return
		}
		o.Proxy = proxy.ProxyString()
		if o.Proxy == "" {
			o.proxyError = errors.New("proxy selector returned an empty address")
		}
	}
}

func RawProxyOption(proxy string) Option {
	return func(o *option) {
		o.Proxy = proxy
	}
}

func RetryOption(retry int) Option {
	return func(o *option) {
		o.Retry = retry
	}
}

func ProxyCallbackOption(f func(out interface{}, proxy string)) Option {
	return func(o *option) {
		o.ProxyCallbackOption = f
	}
}

func DisableTlsOption() Option {
	return func(o *option) {
		o.InsecureSkipVerify = true
	}
}

func DebugOption() Option {
	return func(o *option) {
		o.Debug = true
	}
}

func HTTP2Option() Option {
	return func(o *option) {
		o.HTTP2 = true
	}
}

func RequestAutoHostOption() Option {
	return func(o *option) {
		o.AutoHeaderHost = true
	}
}

func RequestAutoRefererOption() Option {
	return func(o *option) {
		o.AutoHeaderReferer = true
	}
}

func NotIgnoreEmptyOption() Option {
	return func(o *option) {
		o.NotIgnoreEmpty = true
	}
}

// WithCookieJar CookieJar可能导致Cookie泄漏，谨慎使用
func WithCookieJar(jar http.CookieJar) Option {
	return func(o *option) {
		o.CookieJar = jar
	}
}

func WithTransport(tran *http.Transport) Option {
	return func(o *option) {
		o.Transport = tran
	}
}

func WithResponseMiddleware(middler middler.ResponseMiddler) Option {
	return func(o *option) {
		o.ResponseMiddleware = append(o.ResponseMiddleware, middler)
	}
}

func GetResponseCookieOption(cookies *[]*http.Cookie) Option {
	if cookies == nil {
		return empty
	}
	return WithResponseMiddleware(middler.WithResponseMiddlerFunc(
		func(response *http.Response) error {
			*cookies = response.Cookies()
			return nil
		},
	))
}

// ExtractCookieOption 提供一种从Option中提取Cookie的方法
func ExtractCookieOption(opts []Option, cookieName string) string {
	// 创建一个临时的option实例
	tempOpt := &option{}

	// 应用所有选项到临时实例
	for _, opt := range opts {
		opt(tempOpt)
	}

	// 查找指定名称的cookie
	for _, cookie := range tempOpt.Cookies {
		if cookie.Name == cookieName {
			return cookie.Value
		}
	}

	return ""
}

func Do(f func(*gout.Client) *dataflow.DataFlow, out interface{}, options ...Option) error {
	var (
		opt  = new(option)
		code int
		err  error
	)
	for _, o := range options {
		o(opt)
	}
	if opt.proxyError != nil {
		return opt.proxyError
	}
	if opt.proxyConfigured && opt.Proxy != "" {
		parsed, err := url.Parse(opt.Proxy)
		if err != nil || parsed.Hostname() == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
			return errors.New("proxy selector returned an invalid address")
		}
		opt.selectedProxy = parsed
	}
	if opt.ProxyCallbackOption != nil && len(opt.Proxy) > 0 {
		defer func() {
			opt.ProxyCallbackOption(out, opt.Proxy)
		}()
	}
	var df = f(opt.getGout())
	if opt.requestTransport != nil {
		defer opt.requestTransport.CloseIdleConnections()
	}
	if opt.Debug {
		df.Debug(true)
	}
	if opt.AutoHeaderHost || opt.AutoHeaderReferer {
		if h, err := df.GetHost(); err == nil {
			if opt.Header == nil {
				opt.Header = make(gout.H)
			}
			if opt.AutoHeaderHost {
				opt.Header["host"] = h
			} else if opt.AutoHeaderReferer {
				opt.Header["referer"] = "https://" + h
			}
		}
	}
	if len(opt.Cookies) > 0 {
		df.SetCookies(opt.Cookies...)
	}
	if len(opt.Header) > 0 {
		df.SetHeader(opt.Header)
	}
	if len(opt.Proxy) > 0 && !opt.proxyConfigured {
		if strings.HasPrefix(opt.Proxy, "socks5://") {
			df.SetSOCKS5(strings.TrimPrefix(opt.Proxy, "socks5://"))
		} else {
			df.SetProxy(opt.Proxy)
		}
	}
	df.Code(&code)
	if opt.ResponseMiddleware != nil {
		df.ResponseUse(opt.ResponseMiddleware...)
	}
	switch out.(type) {
	case io.Writer, []byte, *[]byte, *string:
		df.BindBody(out)
	default:
		df.BindJSON(out)
	}
	if opt.Retry > 0 {
		err = df.F().Retry().Attempt(opt.Retry).Do()
	} else {
		err = df.Do()
	}
	if opt.HttpCode != nil {
		*opt.HttpCode = code
	}
	if err != nil {
		return err
	}
	if code >= http.StatusBadRequest {
		return fmt.Errorf("http code error %v", code)
	}
	return nil
}

func Get(url string, params interface{}, out interface{}, options ...Option) error {
	return Do(func(gcli *gout.Client) *dataflow.DataFlow {
		return gcli.GET(url).SetQuery(params)
	}, out, options...)
}

func GetWithHeader(url string, params interface{}, out interface{}, respHeader interface{}, options ...Option) error {
	return Do(func(gcli *gout.Client) *dataflow.DataFlow {
		return gcli.GET(url).BindHeader(respHeader).SetQuery(params)
	}, out, options...)
}

func Head(url string, params interface{}, rspHeaders interface{}, options ...Option) error {
	return Do(func(gcli *gout.Client) *dataflow.DataFlow {
		return gcli.HEAD(url).BindHeader(rspHeaders).SetQuery(params)
	}, rspHeaders, options...)
}

func PostForm(url string, params gout.H, out interface{}, options ...Option) error {
	return Do(func(gcli *gout.Client) *dataflow.DataFlow {
		return gcli.POST(url).SetForm(params)
	}, out, options...)
}

func PostJson(url string, params gout.H, out interface{}, options ...Option) error {
	return Do(func(gcli *gout.Client) *dataflow.DataFlow {
		return gcli.POST(url).SetJSON(params)
	}, out, options...)
}

func PostWWWForm(url string, params gout.H, out interface{}, options ...Option) error {
	return Do(func(gcli *gout.Client) *dataflow.DataFlow {
		return gcli.POST(url).SetWWWForm(params)
	}, out, options...)
}

func PostBody(url string, body []byte, out interface{}, options ...Option) error {
	return Do(func(gcli *gout.Client) *dataflow.DataFlow {
		return gcli.POST(url).SetBody(body)
	}, out, options...)
}
