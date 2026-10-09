package adapter

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/sirupsen/logrus"
)

// imageSendMode 是 OneBot 图片发送方式（字符串枚举）。
// auto 保持原样；url 优先把远程直链交给 OneBot；
// base64 由 DDBOT 取到图片后以 base64:// 交付；file 下载到临时文件后以 file:// 交付。
type imageSendMode string

const (
	imageSendModeAuto   imageSendMode = "auto"
	imageSendModeURL    imageSendMode = "url"
	imageSendModeBase64 imageSendMode = "base64"
	imageSendModeFile   imageSendMode = "file"
)

const (
	// imageSendTempDirDefault 是 bot.imageSend.tempDir 未配置时的默认目录。
	imageSendTempDirDefault = ".image-send"
	// imageSendPrepareTimeoutDefault 是 bot.imageSend.timeout 未配置或非法时的默认准备超时。
	imageSendPrepareTimeoutDefault = 30 * time.Second
	// ddbotImageTempPattern 是 file 模式临时文件名模式（os.CreateTemp pattern）。
	ddbotImageTempPattern = "ddbot-image-*"
	// imageSendMaxDownloadBytes 是单张网络图片允许的最大字节数。
	// 成品证据：downloadImage @0x8919c0 在 io.ReadAll 之后用
	// "cmp \$0x4000000,%rbx"（64 MiB）比较长度，超过时以 24 字节
	// "图片超过%d MiB限制" 报错，参数为常量 64（0x11240c0 处数据字 0x40）。
	imageSendMaxDownloadBytes = 64 << 20
	// imageSendMaxDownloadMiB 与 imageSendMaxDownloadBytes 对应，用于报错文案。
	imageSendMaxDownloadMiB = 64
	// ddbotImageUserAgent / ddbotImageAccept 是网络图片请求头。
	// 成品证据：downloadImage @0x8919c0 +0x171 起 mapassign "User-Agent"
	// （91 字节 UA）、+0x228 起 mapassign "Accept"（64 字节）。
	ddbotImageUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/143.0.0.0 Safari/537.36"
	ddbotImageAccept    = "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"
)

// configuredImageSendMode 读取 bot.imageSend.mode 配置；未配置视为 auto，非法值告警后回退 auto。
func configuredImageSendMode() imageSendMode {
	mode := imageSendMode(strings.ToLower(strings.TrimSpace(config.GlobalConfig.GetString("bot.imageSend.mode"))))
	switch mode {
	case "":
		return imageSendModeAuto
	case imageSendModeAuto, imageSendModeURL, imageSendModeBase64, imageSendModeFile:
		return mode
	}
	// 成品证据：Warn 消息 0x1128120（level=3）。在 logConfiguredImageSendMode
	// @0x890358 由 WithFields 构造，key：configured_mode（0x10ae7e3,15，
	// convTstring）、fallback_mode（0x10abccc,13，静态 "auto" 头 0x1128110）。
	// 本文件把该 Warn 放在 configuredImageSendMode，字段值同源。
	messengerLogger.WithFields(logrus.Fields{
		"configured_mode": string(mode),
		"fallback_mode":   string(imageSendModeAuto),
	}).Warn("未知的OneBot图片发送模式，已回退为auto")
	return imageSendModeAuto
}

// configuredImageTempDir 读取 file 模式的临时目录；相对路径以 DDBOT 工作目录为准，未配置默认 .image-send。
func configuredImageTempDir() string {
	dir := config.GlobalConfig.GetString("bot.imageSend.tempDir")
	if dir == "" {
		return imageSendTempDirDefault
	}
	return dir
}

// configuredImagePrepareTimeout 读取 base64/file 模式准备网络图片的超时；未填或小于等于0时默认30s。
func configuredImagePrepareTimeout() time.Duration {
	timeStr := config.GlobalConfig.GetString("bot.imageSend.timeout")
	if timeStr == "" {
		return imageSendPrepareTimeoutDefault
	}
	d, err := time.ParseDuration(timeStr)
	if err != nil || d <= 0 {
		return imageSendPrepareTimeoutDefault
	}
	return d
}

// logConfiguredImageSendMode 在启动时输出图片发送相关配置，便于排查 OneBot 端兼容性问题。
func logConfiguredImageSendMode() {
	mode := configuredImageSendMode()
	fields := logrus.Fields{
		// 成品证据：0x890421 mapassign "mode"（0x109953b,4）取 configuredImageSendMode
		// 第一返回值（0x40/0x30），convTstring。
		"mode": string(mode),
	}
	if mode == imageSendModeBase64 || mode == imageSendModeFile {
		// 成品证据：0x890470 判 "base64"、0x890496 判 "file"，命中才在
		// 0x8904a6 取 bot.imageSend.timeout（viper.GetDuration，<=0 用
		// 0x6fc23ac00=30s），0x8904ef mapassign "prepare_timeout"（0x10ae7f2,15）。
		fields["prepare_timeout"] = configuredImagePrepareTimeout()
	}
	if mode == imageSendModeFile {
		// 成品证据：0x890542 判 "file" 后才调用 configuredImageTempDir
		// @0x890180，0x89056e mapassign "temp_dir"（0x10a5016,8）。
		fields["temp_dir"] = configuredImageTempDir()
	}
	// 成品证据：0x8905c4 WithFields、0x8905f3 Entry.Log（level=4 @0x8905e1），
	// 消息 0x1128130「OneBot图片发送模式已加载」。
	messengerLogger.WithFields(fields).Info("OneBot图片发送模式已加载")
}

// prepareImageElements 按配置的图片发送模式准备消息中的媒体元素。
// 成品三值返回 ([]IMessageElement, func(), error)：第 4 寄存器 rdi 是 cleanup func。
// 证据：adapter.(*Messenger).prepareImageElements @0x890620（224B）把
// prepareImageElementsWithMode @0x890700 的 rax/rbx/rcx（elements）与
// rsi:r8（error）原样返回，rdi 未被覆盖即 WithMode 的 cleanup；调用方按
// "prepare -> 发送 -> cleanup" 顺序使用（SendPrivateMessage @0x8961c6、
// sendGroupChunkWithImageFallback.func1 @0x8a5980）。
func (m *Messenger) prepareImageElements(elements []IMessageElement) ([]IMessageElement, func(), error) {
	return prepareImageElementsWithMode(elements, configuredImageSendMode(), configuredImagePrepareTimeout(), configuredImageTempDir())
}

// prepareImageElementsWithMode 按指定模式转换消息元素中的媒体。
// 成品证据：prepareImageElementsWithMode @0x890700（1888B）的循环只对
// *ImageSegment 做类型判定（itab 0x1131030，比较点 0x890830），不存在语音/
// 视频/文件的 base64 转换分支；其闭包 func1 @0x8aaf60 与 func3 @0x8aaf80
// 都是仅一条 ret 的空闭包，func2 @0x890e60 是 cleanup。
// timeout/tempDir 由 (*Messenger).prepareImageElements 提前解析后逐层传递，
// 成品下载图片与写临时文件都不在内部重新读配置。
func prepareImageElementsWithMode(elements []IMessageElement, mode imageSendMode, timeout time.Duration, tempDir string) ([]IMessageElement, func(), error) {
	if mode == imageSendModeAuto || mode == "" {
		// 成品证据：auto 早退把 rdi 设为静态 funcval 0x111eaa0（代码指针
		// 0x8aaf60，仅一条 ret），即空 cleanup。
		return elements, func() {}, nil
	}

	// tempFiles 记录 file 模式写出的临时图片路径，供 cleanup 在消息发送完成后删除。
	// 成品证据：0x89079a newobject 建立被闭包按引用捕获的 []string（初值 runtime
	// 空切片 0x1bf43a0），0x8907b8 生成闭包 0x890e60。
	var tempFiles []string
	// cleanup 删除本次准备产生的全部临时图片文件。
	// 成品证据：闭包 0x890e60 逐个 os.Remove（0x514220），错误非 nil 且
	// !os.IsNotExist（0x50fae0）时以字段 "path"（0x10994fb，4 字节）Debug 打印
	// 36 字节 "清理OneBot图片临时文件失败"（0x1128140 -> 0x10cddc3）。
	cleanup := func() {
		for _, path := range tempFiles {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				messengerLogger.WithError(err).WithField("path", path).Debug("清理OneBot图片临时文件失败")
			}
		}
	}

	// 成品证据：0x890786 call runtime.makeslicecopy 先克隆整个 elements，
	// 循环命中 *ImageSegment 时原位替换对应下标，其余元素保持克隆后的原样。
	result := make([]IMessageElement, len(elements))
	copy(result, elements)
	for index, el := range elements {
		// 成品证据：0x890830 "cmp %r10,(%r9)" 只比较 itab 0x1131030
		// （*ImageSegment）；不匹配（0x890833 jne）或指针为 nil（0x890846 je）
		// 时直接跳到循环自增 0x890812。
		e, ok := el.(*ImageSegment)
		if !ok || e == nil {
			continue
		}
		// prepareImageSegment 转换单个图片元素；第二个返回值是 file 模式
		// 写出的临时文件路径（其余模式为空串）。
		// 成品证据：0x890880 call prepareImageSegment @0x891160 后
		// 0x890885 "test %rdi,%rdi" 判错、0x8908d4 "test %rcx,%rcx"
		// 判临时路径长度决定是否追加进 tempFiles。
		converted, tempPath, err := prepareImageSegment(e, mode, timeout, tempDir)
		if err != nil {
			// 成品证据：0x890888 jne 0x8909bf → 循环 B（0x890a20 os.Remove
			// 逐个删除已写出的临时文件）→ 0x890cfa fmt.Errorf
			// "图片准备失败 (mode=%s, index=%d): %w"（42 字节 0x10d5cfc）
			// → 0x890dd7 返回 (nil, 空闭包 0x8aaf80, err)：单张图片准备
			// 失败即中止整条消息，不保留部分结果。
			cleanup()
			return nil, func() {}, fmt.Errorf("图片准备失败 (mode=%s, index=%d): %w", mode, index, err)
		}
		result[index] = converted
		if tempPath != "" {
			tempFiles = append(tempFiles, tempPath)
		}
	}
	return result, cleanup, nil
}

// prepareImageSegment 按模式转换单个图片元素。
// url 模式优先远程直链；base64 模式以 base64:// 交付；file 模式写入临时目录后以 file:// 交付。
// 第二返回值是 file 模式写出的临时文件路径（其余模式为空串），供调用方登记清理。
// 成品证据：prepareImageSegment @0x891160（1024B）返回 rax=*ImageSegment、rbx:rcx=临时路径
// （file 分支 0x891377 "mov 0x58(%rsp),%rbx; mov 0x38(%rsp),%rcx"，其余分支显式清零）、
// rdi:rsi=error。
func prepareImageSegment(img *ImageSegment, mode imageSendMode, timeout time.Duration, tempDir string) (*ImageSegment, string, error) {
	switch mode {
	case imageSendModeURL:
		// 尽量把网络图片地址直接交给 OneBot 下载
		if url := imageHTTPURL(img); url != "" {
			return &ImageSegment{File: url, Url: img.Url}, "", nil
		}
		return img, "", nil
	case imageSendModeBase64:
		if strings.HasPrefix(img.File, "base64://") {
			// 已经是 base64 形式，无需重复下载
			return img, "", nil
		}
		data, err := readImageBytes(img, timeout)
		if err != nil {
			return nil, "", err
		}
		messengerLogger.Debug("转换base64图片")
		return &ImageSegment{File: "base64://" + base64.StdEncoding.EncodeToString(data), Url: img.Url}, "", nil
	case imageSendModeFile:
		if strings.HasPrefix(img.File, "file://") {
			return img, "", nil
		}
		data, err := readImageBytes(img, timeout)
		if err != nil {
			return nil, "", err
		}
		path, err := writeImageTempFile(data, tempDir)
		if err != nil {
			return nil, "", err
		}
		return &ImageSegment{File: "file://" + path, Url: img.Url}, path, nil
	}
	return img, "", nil
}

// imageHTTPURL 返回图片元素的远程直链（http/https），没有则返回空串。
func imageHTTPURL(img *ImageSegment) string {
	if img == nil {
		return ""
	}
	for _, candidate := range []string{img.Url, img.File} {
		if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
			return candidate
		}
	}
	return ""
}

// readImageBytes 读取图片原始字节：本地路径直接读取，base64:// 解码，远程地址下载。
// timeout 透传给 downloadImage，成品不在本函数内读配置。
func readImageBytes(img *ImageSegment, timeout time.Duration) ([]byte, error) {
	if img == nil || (img.File == "" && img.Url == "") {
		return nil, fmt.Errorf("图片来源为空")
	}
	if path := imageLocalPath(img); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取本地图片 %q 失败: %w", path, err)
		}
		return nonEmptyImage(data)
	}
	if strings.HasPrefix(img.File, "base64://") {
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(img.File, "base64://"))
		if err != nil {
			return nil, fmt.Errorf("Base64解码失败: %w", err)
		}
		return nonEmptyImage(data)
	}
	if url := imageHTTPURL(img); url != "" {
		return downloadImage(url, timeout)
	}
	return nil, fmt.Errorf("图片来源为空")
}

// downloadImage 下载网络图片。
// 成品结构：http.NewRequestWithContext(context.Background(),"GET",url,nil) 失败报
// "创建图片请求失败: %w"；设置 User-Agent/Accept 后用 http.Client{Timeout: timeout}.Do 发送，
// status 不在 [200,300) 报 "下载图片返回HTTP %d"，io.ReadAll 失败报 "读取图片响应失败: %w"，
// 超过 64 MiB 报 "图片超过%d MiB限制"。
func downloadImage(url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建图片请求失败: %w", err)
	}
	if timeout <= 0 {
		timeout = imageSendPrepareTimeoutDefault
	}
	req.Header.Set("User-Agent", ddbotImageUserAgent)
	req.Header.Set("Accept", ddbotImageAccept)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载图片失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("下载图片返回HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取图片响应失败: %w", err)
	}
	if len(data) > imageSendMaxDownloadBytes {
		return nil, fmt.Errorf("图片超过%d MiB限制", imageSendMaxDownloadMiB)
	}
	return nonEmptyImage(data)
}

// nonEmptyImage 校验图片字节非空；为空时返回 22 字节 "图片内容大小为0"。
// 成品证据：nonEmptyImage @0x8921c0 入参为 []byte（rax/rbx/rcx），
// 返回同一切片或 error，调用点为 readImageBytes(+0x177/+0x329) 与 downloadImage(+0x6ce)。
func nonEmptyImage(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("图片内容大小为0")
	}
	return data, nil
}

// imageLocalPath 提取图片元素的本地路径（file:// 前缀或普通路径），其他形式返回空串。
func imageLocalPath(img *ImageSegment) string {
	if img == nil {
		return ""
	}
	if strings.HasPrefix(img.File, "file://") {
		return strings.TrimPrefix(img.File, "file://")
	}
	if img.File != "" && !strings.Contains(img.File, "://") {
		return img.File
	}
	return ""
}

// writeImageTempFile 把图片字节写入临时目录并返回绝对路径。
// dir 由调用方传入；成品先 filepath.Abs 解析目录（失败报 34 字节
// "解析图片临时目录失败: %w"），再以 0o700 MkdirAll。file 模式下 DDBOT 与
// OneBot 必须共享该目录；OneBot 返回后由发送方删除临时文件。
func writeImageTempFile(data []byte, dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("解析图片临时目录失败: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建图片临时目录失败: %w", err)
	}
	f, err := os.CreateTemp(dir, ddbotImageTempPattern+detectedImageExtension(data))
	if err != nil {
		return "", fmt.Errorf("创建图片临时文件失败: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("写入图片临时文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("写入图片临时文件失败: %w", err)
	}
	path, err := filepath.Abs(f.Name())
	if err != nil {
		return f.Name(), nil
	}
	return path, nil
}

// detectedImageExtension 按图片字节魔数推断扩展名，识别不出时返回空串。
func detectedImageExtension(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return ".jpg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return ".gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	}
	return ""
}
