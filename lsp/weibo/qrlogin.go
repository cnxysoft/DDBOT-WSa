package weibo

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/MiraiGo-Template/utils"
)

type QRLoginOption struct {
	OutputDir string
}

type qrImageResp struct {
	Retcode int64 `json:"retcode"`
	Data    struct {
		Qrid  string `json:"qrid"`
		Image string `json:"image"`
	} `json:"data"`
	Msg string `json:"msg"`
}

type qrCheckResp struct {
	Retcode int64                  `json:"retcode"`
	Data    map[string]interface{} `json:"data"`
	Msg     string                 `json:"msg"`
}

type profileInfoResp struct {
	Ok   int `json:"ok"`
	Data struct {
		User interface{} `json:"user"`
	} `json:"data"`
}

var (
	qrLogger      = utils.GetModuleLogger("weibo-qr")
	jsonpRegex    = regexp.MustCompile(`\((.*)\)`)
	altRegex      = regexp.MustCompile(`(ALT-[\w-]+)`)
	defaultUA     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	defaultRefer  = "https://weibo.com/newlogin?tabtype=weibo&gid=102803&url=https%3A%2F%2Fweibo.com%2F"
	qrImageURL    = "https://login.sina.com.cn/sso/qrcode/image"
	qrCheckURL    = "https://passport.weibo.com/sso/v2/qrcode/check"
	qrLoginURL    = "https://passport.weibo.com/sso/v2/login"
	qrLoginTarget = "https://weibo.com/newlogin?tabtype=weibo&gid=102803&openLoginLayer=0&url=https%3A%2F%2Fweibo.com%2F"

	currentSUB             string
	subLastChecked         time.Time
	lastSuccessfulValidate time.Time
	subMutex               sync.Mutex
	lastRefreshErr         time.Time
	retryCount             int

	keepAliveInterval = 3 * time.Hour // 保活间隔：每3小时尝试一次轻量访问
	checkInterval     = 8 * time.Hour // 严格失效检查间隔
	maxRetryDelay     = 4 * time.Hour // 指数退避最大等待
)

const profileCheckURL = "https://weibo.com/ajax/profile/info"

// RunQRLogin 下载二维码、等待扫码、兑换 ALT 并返回 SUB
func RunQRLogin(opt QRLoginOption) (string, error) {
	if opt.OutputDir == "" {
		opt.OutputDir = "."
	}
	if err := os.MkdirAll(opt.OutputDir, 0755); err != nil {
		return "", err
	}
	if !filepath.IsAbs(opt.OutputDir) {
		if abs, err := filepath.Abs(opt.OutputDir); err == nil {
			opt.OutputDir = abs
		}
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
	}

	qrid, err := fetchQRCode(client, opt)
	if err != nil {
		return "", err
	}
	qrLogger.Infof("QRCode ready, qrid=%s", qrid)

	rawCheck, err := pollQRCode(client, qrid)
	if err != nil {
		return "", err
	}

	sub, err := finalizeLogin(client, rawCheck, opt.OutputDir)
	if err != nil {
		return "", err
	}

	subMutex.Lock()
	currentSUB = sub
	lastSuccessfulValidate = time.Now()
	subMutex.Unlock()

	// 删除二维码图片文件
	imgPath := filepath.Join(opt.OutputDir, "weibo_debug.png")
	_ = os.Remove(imgPath)

	qrLogger.Infof("SUB 登录/刷新成功 | 上次验证: %s | 下次检查 ≈ %s (间隔 %v)",
		lastSuccessfulValidate.Format("2006-01-02 15:04:05"),
		time.Now().Add(checkInterval).Format("2006-01-02 15:04:05"),
		checkInterval)

	return sub, nil
}

func fetchQRCode(client *http.Client, opt QRLoginOption) (string, error) {
	cb := fmt.Sprintf("STK_%d", time.Now().UnixMilli())
	params := url.Values{}
	params.Set("entry", "miniblog")
	params.Set("size", "180")
	params.Set("callback", cb)

	reqURL := qrImageURL + "?" + params.Encode()
	req, _ := http.NewRequest(http.MethodGet, reqURL, nil)
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", defaultRefer)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	m := jsonpRegex.FindStringSubmatch(string(body))
	if len(m) < 2 {
		return "", fmt.Errorf("unexpected response: %s", string(body))
	}

	var qrResp qrImageResp
	if err := json.Unmarshal([]byte(m[1]), &qrResp); err != nil {
		return "", err
	}
	if qrResp.Retcode != 20000000 {
		return "", fmt.Errorf("qr image retcode=%d msg=%s", qrResp.Retcode, qrResp.Msg)
	}

	imageURL := qrResp.Data.Image
	if !strings.HasPrefix(imageURL, "http") {
		imageURL = "https:" + imageURL
	}

	imgReq, _ := http.NewRequest(http.MethodGet, imageURL, nil)
	imgReq.Header.Set("User-Agent", defaultUA)
	imgReq.Header.Set("Referer", defaultRefer)
	imgResp, err := client.Do(imgReq)
	if err != nil {
		return "", err
	}
	defer imgResp.Body.Close()
	imgData, _ := io.ReadAll(imgResp.Body)

	imgPath := filepath.Join(opt.OutputDir, "weibo_debug.png")
	if err := os.WriteFile(imgPath, imgData, 0644); err != nil {
		return "", err
	}

	qrLogger.Infof("二维码已保存至 %s，请扫码登录", imgPath)
	return qrResp.Data.Qrid, nil
}

func pollQRCode(client *http.Client, qrid string) (*qrCheckResp, error) {
	params := url.Values{}
	params.Set("entry", "miniblog")
	params.Set("source", "miniblog")
	params.Set("url", qrLoginTarget)
	params.Set("qrid", qrid)
	params.Set("disp", "popup")
	params.Set("rid", "")
	params.Set("ver", "20250520")

	for {
		time.Sleep(2 * time.Second)
		reqURL := qrCheckURL + "?" + params.Encode()
		req, _ := http.NewRequest(http.MethodGet, reqURL, nil)
		req.Header.Set("User-Agent", defaultUA)
		req.Header.Set("Referer", defaultRefer)

		resp, err := client.Do(req)
		if err != nil {
			qrLogger.Debugf("轮询异常: %v", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var c qrCheckResp
		if err := json.Unmarshal(body, &c); err != nil {
			qrLogger.Debugf("轮询解析失败: %v - %s", err, string(body))
			continue
		}

		if rid, ok := c.Data["rid"].(string); ok && rid != "" {
			params.Set("rid", rid)
		}

		switch c.Retcode {
		case 20000000:
			qrLogger.Info("扫码成功，等待登录完成")
			return &c, nil
		case 50114002, 50114001:
			// 静音
		case 50114004:
			return nil, fmt.Errorf("二维码已过期，请重试")
		default:
			qrLogger.Debugf("轮询状态: ret=%d msg=%s", c.Retcode, c.Msg)
		}
	}
}

func finalizeLogin(client *http.Client, raw *qrCheckResp, outputDir string) (string, error) {
	alt := extractALT(raw)
	if alt == "" {
		return "", fmt.Errorf("未能从响应中提取ALT/ticket")
	}
	qrLogger.Infof("提取到票据 ALT: %s", alt)

	params := url.Values{}
	params.Set("entry", "miniblog")
	params.Set("source", "miniblog")
	params.Set("type", "3")
	params.Set("alt", alt)
	params.Set("url", qrLoginTarget)
	params.Set("disp", "popup")
	params.Set("ver", "20250520")

	loginURL := qrLoginURL + "?" + params.Encode()
	req, _ := http.NewRequest(http.MethodGet, loginURL, nil)
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", defaultRefer)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	qrLogger.Infof("登录完成，跳转: %s", resp.Request.URL.String())

	sub := pickSUB(client, resp.Request.URL)
	if sub == "" {
		return "", fmt.Errorf("未找到SUB cookie")
	}

	subPath := filepath.Join(outputDir, "weibo_sub.txt")
	_ = os.WriteFile(subPath, []byte(sub), 0644)
	qrLogger.Infof("SUB 已保存到: %s", subPath)

	if err := writeBackConfig(sub); err != nil {
		qrLogger.Warnf("写回配置失败: %v (请手动写入 application.yaml weibo.sub)", err)
	} else {
		qrLogger.Infof("已写入配置 weibo.sub")
	}

	return sub, nil
}

func extractALT(raw *qrCheckResp) string {
	if raw == nil || raw.Data == nil {
		return ""
	}
	if v, ok := raw.Data["alt"].(string); ok && v != "" {
		return v
	}
	if v, ok := raw.Data["ticket"].(string); ok && v != "" {
		return v
	}
	str := fmt.Sprintf("%v", raw.Data)
	if m := altRegex.FindStringSubmatch(str); len(m) > 1 {
		return m[1]
	}
	return ""
}

func pickSUB(client *http.Client, u *url.URL) string {
	if client == nil || client.Jar == nil {
		return ""
	}
	targets := []*url.URL{u}
	if base, err := url.Parse("https://weibo.com"); err == nil {
		targets = append(targets, base)
	}
	for _, t := range targets {
		for _, c := range client.Jar.Cookies(t) {
			if c.Name == "SUB" && c.Value != "" {
				return c.Value
			}
		}
	}
	return ""
}

func isValidSUB(sub string) bool {
	if sub == "" {
		return false
	}

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://weibo.com")
	jar.SetCookies(u, []*http.Cookie{{
		Name:   "SUB",
		Value:  sub,
		Domain: ".weibo.com",
		Path:   "/",
	}})

	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	req, _ := http.NewRequest(http.MethodGet, profileCheckURL, nil)
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", "https://weibo.com/")

	resp, err := client.Do(req)
	if err != nil {
		qrLogger.Debugf("检查 SUB 网络异常: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, _ := io.ReadAll(resp.Body)
	var info profileInfoResp
	if err := json.Unmarshal(body, &info); err != nil {
		return false
	}

	if info.Ok == 1 && info.Data.User != nil {
		subMutex.Lock()
		lastSuccessfulValidate = time.Now()
		subMutex.Unlock()
		return true
	}
	return false
}

func writeBackConfig(sub string) error {
	cfgFile := config.GlobalConfig.ConfigFileUsed()
	if cfgFile == "" {
		cfgFile = "application.yaml"
	}
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return err
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	var out []string
	inWeibo := false
	indentWeibo := ""
	inserted := false

	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "weibo:") && !inWeibo {
			inWeibo = true
			indentWeibo = line[:strings.Index(line, "weibo:")]
			out = append(out, line)
			continue
		}

		if inWeibo {
			if len(trim) > 0 && !strings.HasPrefix(line, indentWeibo+" ") && !strings.HasPrefix(line, indentWeibo+"\t") {
				if !inserted {
					out = append(out, fmt.Sprintf("%s  sub: \"%s\"", indentWeibo, sub))
					inserted = true
				}
				inWeibo = false
			} else {
				subLineRe := regexp.MustCompile(`^\s*sub:\s*`)
				if subLineRe.MatchString(line) {
					out = append(out, fmt.Sprintf("%s  sub: \"%s\"", indentWeibo, sub))
					inserted = true
					continue
				}
			}
		}
		out = append(out, line)

		if i == len(lines)-1 && inWeibo && !inserted {
			out = append(out, fmt.Sprintf("%s  sub: \"%s\"", indentWeibo, sub))
			inserted = true
		}
	}

	if !inserted {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "weibo:")
		out = append(out, fmt.Sprintf("  sub: \"%s\"", sub))
	}

	return os.WriteFile(cfgFile, []byte(strings.Join(out, "\n")), 0644)
}

// keepAliveTryRenew 尝试通过访问轻量接口续期 session
func keepAliveTryRenew(sub string) (renewed bool, invalid bool, err error) {
	if sub == "" {
		return false, true, fmt.Errorf("sub 为空")
	}

	jar, _ := cookiejar.New(nil)
	baseURL, _ := url.Parse("https://weibo.com")
	jar.SetCookies(baseURL, []*http.Cookie{{
		Name:   "SUB",
		Value:  sub,
		Domain: ".weibo.com",
		Path:   "/",
	}})

	client := &http.Client{
		Jar:     jar,
		Timeout: 12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if strings.Contains(req.URL.String(), "passport.weibo.com") ||
				strings.Contains(req.URL.String(), "login.sina.com.cn") {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequest("GET", profileCheckURL, nil)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Referer", "https://weibo.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	resp, err := client.Do(req)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	if resp.StatusCode >= 300 && resp.StatusCode < 400 &&
		(strings.Contains(location, "passport.weibo.com") || strings.Contains(location, "login")) {
		return false, true, fmt.Errorf("重定向到登录页，cookie 很可能已失效")
	}

	if len(resp.Header.Values("Set-Cookie")) > 0 {
		renewed = true
		qrLogger.Infof("保活获得 %d 条 Set-Cookie → 可能续期成功", len(resp.Header.Values("Set-Cookie")))
	}

	body, _ := io.ReadAll(resp.Body)
	var info profileInfoResp
	if json.Unmarshal(body, &info) == nil && info.Ok == 1 && info.Data.User != nil {
		renewed = true
	} else {
		invalid = true
	}

	return renewed, invalid, nil
}

func AutoRefreshSUB() (string, error) {
	opt := QRLoginOption{
		OutputDir: ".",
	}
	newSub, err := RunQRLogin(opt)
	return newSub, err
}

func GetCurrentSUB() string {
	subMutex.Lock()
	defer subMutex.Unlock()
	return currentSUB
}

func InitSUB() {
	subMutex.Lock()
	currentSUB = config.GlobalConfig.GetString("weibo.sub")
	subMutex.Unlock()

	if currentSUB == "" || !isValidSUB(currentSUB) {
		qrLogger.Warn("SUB 不存在或已失效，启动自动登录...")
		_, _ = AutoRefreshSUB()
	} else {
		subMutex.Lock()
		lastSuccessfulValidate = time.Now()
		subMutex.Unlock()
		qrLogger.Infof("启动检测：SUB 有效 | 上次验证: %s | 下次检查 ≈ %s",
			lastSuccessfulValidate.Format("2006-01-02 15:04"),
			time.Now().Add(checkInterval).Format("2006-01-02 15:04"))
	}
	subLastChecked = time.Now()
	retryCount = 0
}

func StartSUBMonitor() {
	go func() {
		checkTicker := time.NewTicker(checkInterval)
		keepAliveTicker := time.NewTicker(keepAliveInterval)

		for {
			select {
			case <-checkTicker.C:
				subMutex.Lock()
				sub := currentSUB
				subMutex.Unlock()

				if isValidSUB(sub) {
					subMutex.Lock()
					lastSuccessfulValidate = time.Now()
					subMutex.Unlock()
					qrLogger.Infof("周期检查：SUB 仍有效 | 上次验证: %s | 下次检查 ≈ %s",
						lastSuccessfulValidate.Format("2006-01-02 15:04"),
						time.Now().Add(checkInterval).Format("2006-01-02 15:04"))
				} else {
					qrLogger.Warn("周期检查：SUB 已失效，尝试自动刷新...")
					now := time.Now()
					delaySec := math.Min(float64(retryCount*retryCount*30), float64(maxRetryDelay/time.Second))
					delay := time.Duration(delaySec) * time.Second
					if now.Sub(lastRefreshErr) < delay {
						qrLogger.Infof("退避等待 %.0f 秒...", delay.Seconds())
						time.Sleep(delay)
					}

					_, err := AutoRefreshSUB()
					if err == nil {
						retryCount = 0
						lastRefreshErr = time.Time{}
					} else {
						retryCount++
						lastRefreshErr = now
					}
				}
				subLastChecked = time.Now()

			case <-keepAliveTicker.C:
				subMutex.Lock()
				sub := currentSUB
				subMutex.Unlock()

				if sub == "" {
					continue
				}

				renewed, invalid, err := keepAliveTryRenew(sub)
				if err != nil {
					qrLogger.Debugf("保活异常: %v", err)
				}
				if invalid {
					qrLogger.Warn("保活检测到登录跳转，提前判定失效 → 触发刷新")
					_, _ = AutoRefreshSUB()
				} else if renewed {
					qrLogger.Infof("保活成功，可能已续期 session")
				} else {
					qrLogger.Debug("保活完成，无明显续期迹象")
				}
			}
		}
	}()
}
