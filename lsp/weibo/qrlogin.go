package weibo

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/mattn/go-colorable"
	"github.com/sirupsen/logrus"
	"github.com/skip2/go-qrcode"
)

// QRLoginOption controls the QR login helper behavior.
type QRLoginOption struct {
	OutputDir string
	AutoOpen  bool
}

var qrLogger = utils.GetModuleLogger("weibo-qr")

// RunQRLogin 扫码登录入口：访客态初始化 → 生成二维码 → 终端展示 → 轮询扫码
// → 完成 SSO 登录并激活 Cookie，返回 SUB。
//
// 成品调用图证据（lsp/weibo/qrlogin.go:30）：
//
//	RunQRLogin -> currentWeiboStatePaths/loadCookieState/snapshot/newWeiboProtocol/loadCookieMeta
//	           -> createVisitor -> createQRCode -> printQRToConsole -> openQRImage
//	           -> waitForQRCode -> completeQRLogin -> refreshLoginCookies
//	           -> saveCookieState/writePrivateJSON -> activateCookieJar
//	           -> cookieValueForURL/cookieNamesForURL
//
// 调用方：concern.go 启动时 SUB 为空，以及 reportWeiboCookieFailure 的 GenerateQR 闭包
// （管理员私聊回复“生成 微博”后触发）。
func RunQRLogin(opt QRLoginOption) (string, error) {
	if opt.OutputDir == "" {
		opt.OutputDir = "."
	}
	if err := os.MkdirAll(opt.OutputDir, 0o755); err != nil {
		return "", err
	}
	if !filepath.IsAbs(opt.OutputDir) {
		if abs, err := filepath.Abs(opt.OutputDir); err == nil {
			opt.OutputDir = abs
		}
	}

	_, loginPath := currentWeiboStatePaths()
	stateDir := filepath.Dir(loginPath)
	jar := newPersistentCookieJar()
	if state, err := loadCookieState(loginPath); err == nil && state != nil && len(state.Cookies) > 0 {
		jar.importRecords(state.Cookies)
	}
	jar.setConfiguredSUB()
	p := newWeiboProtocol(jar)
	meta, metaErr := loadCookieMeta(stateDir)
	if metaErr != nil {
		return "", metaErr
	}

	// 成品证据：RunQRLogin(qrlogin.go:30) 两次调用 cfg.GetWeiboProtocolTimeout()
	// 为协议步骤构造独立 context；扫码等待本身由 weibo.qrloginTimeout 控制。
	visitCtx, visitCancel := weiboProtocolContext()
	defer visitCancel()

	// 访客态初始化：失败不致命，仅记录后继续生成登录二维码。
	// 成品证据：RunQRLogin +0x34b 调 createVisitor(ctx, meta.VisitorTID)，
	// 返回值 7 词聚合 + error，调用点分别消费 error(r10/r11) 与 bool(0xa58)。
	spaOnly, visitorTID, visitorNewTID, _, err := p.createVisitor(visitCtx, meta.VisitorTID)
	if err != nil {
		qrLogger.Warnf("微博扫码前访客态初始化失败，将继续生成登录二维码: %v", err)
	} else if !spaOnly {
		// 成品 disasm 证据（RunQRLogin @0xc60d00，非 spaOnly 成功路径 +0x3e5 分支）：
		//   +0x1673 / +0x16f3 两次内联 firstNonEmpty：
		//       meta.VisitorTID    = firstNonEmpty(s1, meta.VisitorTID)
		//       meta.VisitorNewTID = firstNonEmpty(s2, meta.VisitorNewTID)
		//   +0x172b time.Now -> meta.VisitorUpdatedAt；
		//   +0x17ce writePrivateJSON（内联 saveCookieMeta(stateDir, meta)）。
		// 这里**不** saveCookieState：createVisitor 写入的访客 Cookie 仍在 p.jar 中，
		// 随后的登录 saveCookieState(+0x106f) 会 snapshot 整个 jar 一并落盘。
		meta.VisitorTID = firstNonEmpty(visitorTID, meta.VisitorTID)
		meta.VisitorNewTID = firstNonEmpty(visitorNewTID, meta.VisitorNewTID)
		meta.VisitorUpdatedAt = time.Now()
		if err := saveCookieMeta(stateDir, meta); err != nil {
			qrLogger.Warnf("写入微博访客 Cookie 元信息失败: %v", err)
		}
	}
	qrLogger.Debugf("微博访客态初始化完成: spaOnly=%v visitorTID=%s visitorNewTID=%s", spaOnly, maskSensitive(visitorTID), maskSensitive(visitorNewTID))

	// 生成二维码：createQRCode 返回 weiboQRCode{Qrid, ImageURL, Data, Interval} + error。
	// 成品 RunQRLogin(@qrlogin.go:30) 用 go-qrcode.WriteFile 把 data 渲染成 PNG：
	// +0x6a0 createQRCode、+0x7b3 WriteFile(data, level=1(Medium), size=0x168=360, qrPath)、
	// 失败报 "保存微博二维码失败: %w"(@0x10c56f9 len31)、成功 +0x88a os.Chmod(qrPath,0o600)、
	// +0x920 printQRToConsole(data)（传入 data，不是 PNG 字节）。
	qrCtx, qrCancel := weiboRequestContext()
	defer qrCancel()
	qr, err := p.createQRCode(qrCtx)
	if err != nil {
		return "", err
	}
	qrPath := filepath.Join(stateDir, "weibo-qr.png")
	if err := qrcode.WriteFile(qr.Data, qrcode.Medium, 360, qrPath); err != nil {
		return "", fmt.Errorf("保存微博二维码失败: %w", err)
	}
	_ = os.Chmod(qrPath, 0o600)
	defer func() {
		if err := os.Remove(qrPath); err != nil && !os.IsNotExist(err) {
			qrLogger.Debug("清理微博登录二维码失败")
		}
	}()
	printQRToConsole(qr.Data)
	if opt.AutoOpen {
		if openErr := openQRImage(qrPath); openErr != nil {
			qrLogger.Warnf("自动打开微博二维码失败，请手动打开二维码文件: %v", openErr)
		}
	}

	// 轮询等待扫码确认：成品在 +0xcc5 用独立的 context.WithTimeout(cfg.GetWeiboQRLoginTimeout())
	// 计时（不能用 protocolTimeout，否则扫码较慢时会被提前取消）；waitForQRCode 本身不读配置。
	waitCtx, waitCancel := weiboQRLoginContext()
	defer waitCancel()
	// 成品 RunQRLogin.func2 @0xc69cc0 的回调只在内部由 retcode 算文本：
	//   对 20000000 / 50114001 / 50114002 / 50114003 / 50114004 / 50114013-15 / 50114018
	//   做与 qrStatusMessage 完全相同的 switch（c69dc8 起），convTstring 结果写入 "status" 字段；
	//   回调第二参（waitForQRCode c5f102 传入的 rbx/rcx，即 checkQRCode 的 msg）从未被读取
	//   （func2 只把 retcode 存到 0x28(%rsp)，rbx/rdi 存下后再无引用），
	//   文本不来自服务端 msg。字段为 "retcode"(int64) + "status"(string)，
	//   经 WithFields 后按 InfoLevel(0x4) 输出 "微博二维码轮询状态变化"(@0x10c8be7 len 33)。
	onStatus := func(retcode int64, msg string) {
		qrLogger.WithFields(logrus.Fields{
			"retcode": retcode,
			"status":  qrStatusMessage(retcode),
		}).Info("微博二维码轮询状态变化")
	}
	checkResp, err := p.waitForQRCode(waitCtx, onStatus, qr)
	if err != nil {
		return "", err
	}
	// 成品 RunQRLogin +0xcb2：mapaccess1_faststr(@0x416400, Data @0x308(%rsp), "alt" @0x1098896 len3)
	// 后做 string 类型断言（cmp @0xee7100）；只用 "alt"、无 ticket 回退。取不到即空串，
	// 由 completeQRLogin 首行报 "微博扫码成功响应未包含 alt"。
	alt, _ := checkResp.Data["alt"].(string)

	// 完成 SSO 登录并落盘状态（重新起算 protocolTimeout，避免扫码耗时计入）。
	loginCtx, loginCancel := weiboProtocolContext()
	defer loginCancel()
	if _, err := p.completeQRLogin(loginCtx, alt); err != nil {
		return "", err
	}
	refreshCtx, refreshCancel := weiboProtocolContext()
	defer refreshCancel()
	if err := p.refreshLoginCookies(refreshCtx); err != nil {
		qrLogger.Error("微博 SSO 已执行但登录状态验证失败")
	}
	if err := saveCookieState(loginPath, p.userAgent, p.jar.snapshot()); err != nil {
		qrLogger.Warnf("写入微博登录 Cookie 状态失败: %v", err)
	}
	meta, metaErr = loadCookieMeta(stateDir)
	if metaErr != nil {
		return "", metaErr
	}
	meta.LoginUpdatedAt = time.Now()
	if err := saveCookieMeta(stateDir, meta); err != nil {
		qrLogger.Warnf("写入微博登录 Cookie 元信息失败: %v", err)
	}

	weiboURL, _ := url.Parse("https://weibo.com/")
	sub := cookieValueForURL(p.jar, weiboURL.String(), "SUB")
	if sub == "" {
		return "", errors.New("未找到SUB cookie")
	}
	if !activateCookieJar(cookieModeLogin, p.jar, p.userAgent) {
		return "", fmt.Errorf("微博 %s Cookie Jar 中没有可用 Cookie", cookieModeLogin)
	}
	qrLogger.Info("微博扫码登录成功，完整 Cookie Jar 已安全保存")
	qrLogger.Infof("微博登录二维码已生成，请使用微博官方 App 扫码并确认")
	qrLogger.Debugf("微博登录 Cookie 名称: %v", cookieNamesForURL(p.jar, weiboURL.String()))
	return sub, nil
}

// printQRToConsole 把二维码内容渲染到终端；失败时静默降级。
// 成品符号证据：printQRToConsole(qrlogin.go:155, 1056 字节)，唯一调用方为 RunQRLogin。
// 成品 callees：go-qrcode.New(content, level=1(Medium))、(*QRCode).Bitmap；
// 每个 cell：true(黑块) 输出白底转义 "\x1b[48;5;7m  \x1b[0m"，
// false(白块) 输出黑底转义 "\x1b[48;5;0m  \x1b[0m"，每行追加 '\n'（+0x29cc/+0x2a3d）。
func printQRToConsole(content string) {
	const (
		black = "\033[48;5;0m  \033[0m"
		white = "\033[48;5;7m  \033[0m"
	)
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		qrLogger.Debug("生成微博终端二维码失败")
		return
	}
	bitmap := q.Bitmap()
	var buf bytes.Buffer
	for _, row := range bitmap {
		for _, cell := range row {
			if cell {
				buf.WriteString(white)
			} else {
				buf.WriteString(black)
			}
		}
		buf.WriteByte('\n')
	}
	_, _ = colorable.NewColorableStdout().Write(buf.Bytes())
}

// openQRImage 用系统默认程序打开二维码图片。
// 成品符号证据：openQRImage(qrlogin.go:180, 128 字节)，唯一调用方为 RunQRLogin。
// [推断] 128 字节只够一次 exec.Command(...).Start()，这里按平台选择打开命令。
func openQRImage(imgPath string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", "", imgPath)
	case "darwin":
		cmd = exec.Command("open", imgPath)
	default:
		cmd = exec.Command("xdg-open", imgPath)
	}
	return cmd.Start()
}
