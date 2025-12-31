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
// It also saves the QR image to outputDir/weibo_debug.png and SUB to outputDir/weibo_sub.txt.
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
	_ = os.WriteFile(subPath, []byte(sub), 0o644)
	qrLogger.Infof("SUB 已保存到: %s", subPath)
	if err := writeBackConfig(sub); err != nil {
		qrLogger.Warnf("SUB 已获取，但写回配置失败: %v (请手动写入 application.yaml weibo.sub)", err)
	} else {
		qrLogger.Infof("已写入配置 weibo.sub 并保存到 application.yaml")
	}
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

// writeBackConfig writes SUB into application.yaml, touching only weibo.sub.
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

	// If sub exists, replace the line only.
	subLineRe := regexp.MustCompile(`(?m)^(\s*sub:\s*).*$`)
	if subLineRe.MatchString(content) {
		content = subLineRe.ReplaceAllString(content, fmt.Sprintf("${1}\"%s\"", sub))
		return os.WriteFile(cfgFile, []byte(content), 0o644)
	}

	// If weibo block exists, append sub under it.
	weiboRe := regexp.MustCompile(`(?m)^(?P<indent>\s*)weibo:\s*$`)
	if loc := weiboRe.FindStringSubmatchIndex(content); loc != nil {
		indent := weiboRe.ReplaceAllString(content[loc[0]:loc[1]], "${indent}")
		insert := fmt.Sprintf("\n%s  sub: \"%s\"", indent, sub)
		// insert right after the weibo: line
		insertPos := loc[1]
		content = content[:insertPos] + insert + content[insertPos:]
		return os.WriteFile(cfgFile, []byte(content), 0o644)
	}

	// Otherwise append a new weibo block.
	appendBlock := fmt.Sprintf("\nweibo:\n  sub: \"%s\"\n", sub)
	content += appendBlock
	return os.WriteFile(cfgFile, []byte(content), 0o644)
}
