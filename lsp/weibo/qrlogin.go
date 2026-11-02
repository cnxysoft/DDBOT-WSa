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
	Scanned        bool   // 已扫码，正在完成登录（中间态；此条 Sub 为空）
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

		// 先回一条中间态：扫码成功之后的「完成登录」要打外部接口换 Cookie，
		// 网络不好时会卡几十秒，中间态能让调用方（Web 面板 / 私聊命令）知道进度，
		// 而不是一直停在「请扫码」上。
		resultChan <- QRLoginResult{Scanned: true}

		// 完成登录
		sub, err := finalizeLogin(client, rawCheck, ".")
		if err != nil {
			resultChan <- QRLoginResult{Error: err}
			return
		}

		// 分别处理运行时加载和配置持久化
		result := QRLoginResult{Sub: sub}

		// 先更新运行时 SUB：后面刷新 Cookie 时模式判断要用到它
		setRuntimeSUB(sub)

		// 1. 配置持久化：写回 application.yaml。
		//    放在加载 Cookie 之前——SUB 先落盘，后面无论成败，下次启动/刷新都能
		//    按 login 模式走，不会退化成访客流程。
		if err := WriteBackConfig(sub); err != nil {
			qrLogger.Warnf("SUB 已获取，但写入配置失败: %v", err)
			result.PersistError = err
			result.PersistSuccess = false
		} else {
			qrLogger.Infof("已写入配置 weibo.sub 并保存到 application.yaml")
			result.PersistSuccess = true
		}

		// 2. 运行时加载：加载 Cookie 到内存
		if err := freshCookieOpt(sub); err != nil {
			qrLogger.Warnf("SUB 已获取，但加载 Cookie 到内存失败: %v", err)
			result.RuntimeLoaded = false
		} else {
			result.RuntimeLoaded = true
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
	cfgFile := cfg.ConfigFilePath()
	// 使用全局配置写锁，防止与其他写操作并发
	cfg.GetConfigWriteMutex().Lock()
	defer cfg.GetConfigWriteMutex().Unlock()

	// 标记正在写入，暂停热重载
	cfg.MarkConfigWriteStart()
	defer cfg.MarkConfigWriteEnd()

	if err := writeBackConfigToPath(sub, cfgFile); err != nil {
		return err
	}

	// 同步到内存里的 viper。GetWeiboMode() 是看配置里有没有 weibo.sub 来判断
	// guest/login 的，只写文件的话（热重载这会儿正被 MarkConfigWrite 挡着）
	// 它会一直认为 SUB 是空的 —— 表现就是「扫码登录成功了，但还在跑访客 Cookie」。
	config.GlobalConfig.Set("weibo.sub", sub)
	return nil
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
	childIndent := "" // weibo 块内已有子键的缩进，插入 sub 时对齐它
	inserted := false

	// 大小写不敏感（viper 就是大小写不敏感），命中时保留原缩进与原键名写法，只换值
	subLineRe := regexp.MustCompile("(?i)^(\\s*)(sub)(\\s*):(\\s*)(.*)$")

	// 和 lsp/cfg 的 yamlutil 同一个坑：上面那个正则把行尾的 \r 吃进了第 5 组，
	// 拼回时不补上就把 CRLF 改成了 LF，文件变成混合换行。
	eol := ""
	if strings.Contains(content, "\r\n") {
		eol = "\r"
	}
	// 块里已有子键就照它的缩进插，没有才退回块头缩进 + 两空格。
	// 写死两空格会把 4 空格缩进的块改成混合缩进、YAML 直接不合法 ——
	// 和 lsp/cfg 的 yamlutil 是同一个坑。
	insertKey := func() string {
		indent := childIndent
		if indent == "" {
			indent = indentWeibo + "  "
		}
		return indent + "sub: \"" + sub + "\"" + eol
	}

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "weibo:") && !inWeibo {
			inWeibo = true
			indentWeibo = line[:strings.Index(line, "weibo:")]
			out = append(out, line)
			continue
		}

		if inWeibo {
			// 退出 weibo 块：遇到非缩进行
			if len(trim) > 0 && !strings.HasPrefix(line, indentWeibo+" ") {
				if !inserted {
					out = append(out, insertKey())
					inserted = true
				}
				inWeibo = false
			} else if m := subLineRe.FindStringSubmatch(line); m != nil && strings.EqualFold(m[2], "sub") {
				out = append(out, m[1]+m[2]+m[3]+":"+m[4]+"\""+sub+"\""+eol)
				inserted = true
				continue
			} else if childIndent == "" && len(trim) > 0 && !strings.HasPrefix(trim, "#") {
				if idx := strings.Index(line, trim); idx > len(indentWeibo) {
					childIndent = line[:idx]
				}
			}
		}
		out = append(out, line)
	}

	// 块一直延伸到文件末尾且没找到 sub：插到块内最后一个非空行之后
	if inWeibo && !inserted {
		pos := len(out)
		for pos > 0 && strings.TrimSpace(out[pos-1]) == "" {
			pos--
		}
		out = append(out, "")
		copy(out[pos+1:], out[pos:])
		out[pos] = insertKey()
		inserted = true
	}

	if !inserted {
		// 未找到 weibo 块，追加新块
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "weibo:"+eol)
		out = append(out, fmt.Sprintf("  sub: \"%s\"", sub)+eol)
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
