package weibo

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/mattn/go-colorable"
)

// QRLoginOption controls the QR login helper behavior.
type QRLoginOption struct {
	OutputDir string
	AutoOpen  bool
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

var (
	qrLogger      = utils.GetModuleLogger("weibo-qr")
	jsonpRegex    = regexp.MustCompile(`\((.*)\)`)
	altRegex      = regexp.MustCompile(`(ALT-[\w-]+)`)
	defaultUA     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	defaultRefer  = "https://weibo.com/newlogin?tabtype=weibo&gid=102803&url=https%3A%2F%2Fweibo.com%2F"
	qrImageURL    = "https://login.sina.com.cn/sso/qrcode/image"
	qrCheckURL    = "https://passport.weibo.com/sso/v2/qrcode/check"
	qrLoginURL    = "https://passport.weibo.com/sso/v2/login"
	qrLoginTarget = "https://weibo.com/newlogin?tabtype=weibo&gid=102803&openLoginLayer=0&url=https%3A%2F%2Fweibo.com%2F"
)

// RunQRLogin downloads a QR code, waits for scan, exchanges ALT for cookies, and returns SUB.
// It also saves the QR image to outputDir/weibo_debug.png.
func RunQRLogin(opt QRLoginOption) (string, error) {
	if opt.OutputDir == "" {
		opt.OutputDir = "."
	}
	if err := os.MkdirAll(opt.OutputDir, 0o755); err != nil {
		return "", err
	}
	if !filepath.IsAbs(opt.OutputDir) {
		abs, err := filepath.Abs(opt.OutputDir)
		if err == nil {
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
	// download image
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
	if err := os.WriteFile(imgPath, imgData, 0o644); err != nil {
		return "", err
	}
	printQRCode(imgData)
	qrLogger.Infof("二维码已保存: %s (如控制台无法扫，请用此文件)", imgPath)
	if opt.AutoOpen {
		_ = exec.Command("cmd", "/C", "start", imgPath).Start()
	}
	return qrResp.Data.Qrid, nil
}

// QRLoginResult 扫码登录结果
type QRLoginResult struct {
	QRCodeImage    []byte // 二维码图片数据
	Sub            string // 登录成功后的 SUB
	Error          error  // 错误信息
	RuntimeLoaded  bool   // 运行时 Cookie 是否已加载到内存
	PersistSuccess bool   // 配置文件是否持久化成功
	PersistError   error  // 持久化错误信息（若 PersistSuccess 为 false）
}

// RunQRLoginForQQ 为 QQ 扫码登录优化的版本
// 返回一个 channel，用户可以通过它获取二维码图片和登录结果
// timeout 为扫码超时时间，默认 5 分钟
func RunQRLoginForQQ(timeout time.Duration) (<-chan QRLoginResult, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
	}

	// 获取二维码
	qrid, qrImage, err := fetchQRCodeWithImage(client)
	if err != nil {
		return nil, fmt.Errorf("获取二维码失败: %v", err)
	}

	resultChan := make(chan QRLoginResult, 1)

	// 先发送二维码图片
	resultChan <- QRLoginResult{QRCodeImage: qrImage}

	// 启动异步扫码等待
	go func() {
		defer close(resultChan)

		// 等待扫码
		rawCheck, err := pollQRCodeWithTimeout(client, qrid, timeout)
		if err != nil {
			resultChan <- QRLoginResult{Error: err}
			return
		}

		// 完成登录
		sub, err := finalizeLogin(client, rawCheck, ".")
		if err != nil {
			resultChan <- QRLoginResult{Error: err}
			return
		}

		// 分别处理运行时加载和配置持久化
		result := QRLoginResult{Sub: sub}

		// 1. 运行时加载：更新运行时 SUB 并加载 Cookie 到内存
		setRuntimeSUB(sub)
		if err := freshCookieOpt(sub); err != nil {
			qrLogger.Warnf("SUB 已获取，但加载 Cookie 到内存失败: %v", err)
			result.RuntimeLoaded = false
		} else {
			result.RuntimeLoaded = true
		}

		// 2. 配置持久化：写回 application.yaml
		if err := WriteBackConfig(sub); err != nil {
			qrLogger.Warnf("SUB 已获取，但写入配置失败: %v", err)
			result.PersistError = err
			result.PersistSuccess = false
		} else {
			qrLogger.Infof("已写入配置 weibo.sub 并保存到 application.yaml")
			result.PersistSuccess = true
		}

		resultChan <- result
	}()

	return resultChan, nil
}

// fetchQRCodeWithImage 获取二维码图片并返回图片数据
func fetchQRCodeWithImage(client *http.Client) (string, []byte, error) {
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
		return "", nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	m := jsonpRegex.FindStringSubmatch(string(body))
	if len(m) < 2 {
		return "", nil, fmt.Errorf("unexpected response: %s", string(body))
	}
	var qrResp qrImageResp
	if err := json.Unmarshal([]byte(m[1]), &qrResp); err != nil {
		return "", nil, err
	}
	if qrResp.Retcode != 20000000 {
		return "", nil, fmt.Errorf("qr image retcode=%d msg=%s", qrResp.Retcode, qrResp.Msg)
	}
	imageURL := qrResp.Data.Image
	if !strings.HasPrefix(imageURL, "http") {
		imageURL = "https:" + imageURL
	}

	// 下载图片
	imgReq, _ := http.NewRequest(http.MethodGet, imageURL, nil)
	imgReq.Header.Set("User-Agent", defaultUA)
	imgReq.Header.Set("Referer", defaultRefer)
	imgResp, err := client.Do(imgReq)
	if err != nil {
		return "", nil, err
	}
	defer imgResp.Body.Close()
	imgData, _ := io.ReadAll(imgResp.Body)

	return qrResp.Data.Qrid, imgData, nil
}

// pollQRCodeWithTimeout 带超时的扫码轮询
func pollQRCodeWithTimeout(client *http.Client, qrid string, timeout time.Duration) (*qrCheckResp, error) {
	params := url.Values{}
	params.Set("entry", "miniblog")
	params.Set("source", "miniblog")
	params.Set("url", qrLoginTarget)
	params.Set("qrid", qrid)
	params.Set("disp", "popup")
	params.Set("rid", "")
	params.Set("ver", "20250520")

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
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
		case 50114002:
			// 静音轮询，避免刷屏
		case 50114001:
			// 静音轮询，避免刷屏
		case 50114004:
			return nil, fmt.Errorf("二维码已过期，请重试")
		default:
			qrLogger.Debugf("轮询状态: ret=%d msg=%s", c.Retcode, c.Msg)
		}
	}

	return nil, fmt.Errorf("扫码超时（%v），请重试", timeout)
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
		case 50114002:
			// 静音轮询，避免刷屏
		case 50114001:
			// 静音轮询，避免刷屏
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
	// 脱敏记录，避免完整 ALT/ticket 泄露登录凭据
	qrLogger.Infof("提取到票据 ALT: %s", maskSub(alt))

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
	// 跳转 URL 可能携带 alt/ticket 参数，脱敏后记录
	qrLogger.Infof("登录完成，跳转: %s", sanitizeLoginURL(resp.Request.URL))

	sub := pickSUB(client, resp.Request.URL)
	if sub == "" {
		return "", fmt.Errorf("未找到SUB cookie")
	}
	qrLogger.Infof("SUB 已获取")
	return sub, nil
}

func extractALT(raw *qrCheckResp) string {
	if raw == nil || raw.Data == nil {
		return ""
	}
	if v, ok := raw.Data["alt"].(string); ok {
		return v
	}
	if v, ok := raw.Data["ticket"].(string); ok {
		return v
	}
	str := fmt.Sprintf("%v", raw.Data)
	if m := altRegex.FindStringSubmatch(str); len(m) > 1 {
		return m[1]
	}
	return ""
}

// sanitizeLoginURL 脱敏登录跳转 URL，隐藏 alt/ticket 等敏感参数，避免日志泄露登录凭据
func sanitizeLoginURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	q := clone.Query()
	for _, key := range []string{"alt", "ticket"} {
		if q.Get(key) != "" {
			q.Set(key, "***")
		}
	}
	clone.RawQuery = q.Encode()
	return clone.String()
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

// printQRCode renders PNG QR to console; falls back silently on error.
func printQRCode(imgData []byte) {
	const (
		black = "\033[48;5;0m  \033[0m"
		white = "\033[48;5;7m  \033[0m"
	)
	img, err := png.Decode(bytes.NewReader(imgData))
	if err != nil {
		qrLogger.Debugf("二维码控制台打印失败: %v", err)
		return
	}
	gray, ok := img.(*image.Gray)
	if !ok {
		qrLogger.Debug("二维码控制台打印失败: 非灰度图")
		return
	}
	data := gray.Pix
	bound := img.Bounds().Max.X
	buf := make([]byte, 0, (bound*4+1)*bound)
	i := 0
	for y := 0; y < bound; y++ {
		i = y * bound
		for x := 0; x < bound; x++ {
			if data[i] != 255 {
				buf = append(buf, white...)
			} else {
				buf = append(buf, black...)
			}
			i++
		}
		buf = append(buf, '\n')
	}
	_, _ = colorable.NewColorableStdout().Write(buf)
}

// WriteBackConfig writes SUB into application.yaml, touching only weibo.sub.
// Uses global config write lock and atomic rename to prevent corruption.
func WriteBackConfig(sub string) error {
	cfgFile := config.GlobalConfig.ConfigFileUsed()
	if cfgFile == "" {
		cfgFile = "application.yaml"
	}
	// 使用全局配置写锁，防止与其他写操作并发
	cfg.GetConfigWriteMutex().Lock()
	defer cfg.GetConfigWriteMutex().Unlock()

	// 标记正在写入，暂停热重载
	cfg.MarkConfigWriteStart()
	defer cfg.MarkConfigWriteEnd()

	return writeBackConfigToPath(sub, cfgFile)
}

// writeBackConfigToPath writes SUB into the config file at cfgPath using atomic rename.
// It touches only the weibo.sub field and preserves all other content and file permissions.
func writeBackConfigToPath(sub string, cfgPath string) error {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}

	// 保留原文件权限
	var fileMode os.FileMode = 0o644
	if info, err := os.Stat(cfgPath); err == nil {
		fileMode = info.Mode()
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	// Line-by-line rewrite to avoid touching other blocks
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
			// 退出 weibo 块：遇到非缩进行或空缩进变化
			if len(trim) > 0 && !strings.HasPrefix(line, indentWeibo+" ") {
				// 退出前若未插入sub则补一行
				if !inserted {
					out = append(out, fmt.Sprintf("%s  sub: \"%s\"", indentWeibo, sub))
					inserted = true
				}
				inWeibo = false
			} else {
				// 在 weibo 块内
				subLineRe := regexp.MustCompile(`^(\s*)sub:\s*`)
				if matches := subLineRe.FindStringSubmatch(line); len(matches) > 1 {
					// 保留原有缩进格式
					out = append(out, fmt.Sprintf("%ssub: \"%s\"", matches[1], sub))
					inserted = true
					continue
				}
			}
		}
		out = append(out, line)

		// 最后一行且在weibo块且未插入
		if i == len(lines)-1 && inWeibo && !inserted {
			out = append(out, fmt.Sprintf("%s  sub: \"%s\"", indentWeibo, sub))
			inserted = true
		}
	}

	if !inserted {
		// 未找到 weibo 块，追加新块
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "weibo:")
		out = append(out, fmt.Sprintf("  sub: \"%s\"", sub))
	}

	newData := []byte(strings.Join(out, "\n"))

	// 使用临时文件 + 原子替换，避免写入中途崩溃导致配置损坏
	tmpFile := cfgPath + ".tmp"
	if err := os.WriteFile(tmpFile, newData, fileMode); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := os.Rename(tmpFile, cfgPath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("原子替换配置文件失败: %w", err)
	}

	return nil
}
