package authprompt

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Sora233/MiraiGo-Template/bot"
	mtutils "github.com/Sora233/MiraiGo-Template/utils"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/buntdb"
)

// ModeQrcode / ModeCookieCloud 与站点侧 cookieMode 配置值一致。
const (
	ModeQrcode      = "qrcode"
	ModeCookieCloud = "cookiecloud"
)

// Failure 描述一次站点登录凭据失效事件。
// Site/DisplayName/Mode/Fingerprint/Reason/AllowQR 由站点侧填充，
// GenerateQR 是站点侧的二维码生成动作，由管理员私聊回复“生成 站点名”后触发。
type Failure struct {
	Site        string
	DisplayName string
	Mode        string
	Fingerprint string
	Reason      string
	AllowQR     bool
	GenerateQR  func() error
}

// failureState 是一个失败事件的提醒状态。
// sentTo 记录已经私聊提醒过的管理员；pending 记录等待管理员确认的管理员；
// running 防止同一份凭据的二维码被并发重复生成。
type failureState struct {
	failure Failure
	sentTo  map[int64]bool
	pending map[int64]bool
	running bool
}

var (
	mu     sync.Mutex
	states = make(map[string]*failureState)
)

var logger = mtutils.GetModuleLogger("authprompt")

// normalizeFailure 归一化站点侧提交的失败信息，补齐默认值。
func normalizeFailure(f Failure) Failure {
	f.Site = strings.TrimSpace(f.Site)
	f.DisplayName = strings.TrimSpace(f.DisplayName)
	if f.DisplayName == "" {
		f.DisplayName = f.Site
	}
	f.Mode = strings.ToLower(strings.TrimSpace(f.Mode))
	if f.Mode == "" {
		f.Mode = ModeQrcode
	}
	f.Fingerprint = strings.TrimSpace(f.Fingerprint)
	return f
}

// shortFingerprint 返回指纹的缩写形式，用于日志展示。
// 成品 ReportFailure 的重复提醒日志取 min(len, 12) 作为指纹前缀
// （0xc27930 cmp $0xc）。
func shortFingerprint(fingerprint string) string {
	fingerprint = strings.TrimSpace(fingerprint)
	if len(fingerprint) > 12 {
		return fingerprint[:12]
	}
	return fingerprint
}

// ObserveCredential 由站点侧在凭据验证通过时调用：把当前可用凭据的完整指纹写入
// CookieCredentialFingerprint 键。ReportFailure 与 Flush 以同一键回读比对，
// 凭据更新后即使该指纹曾经失败过也会重新提醒。
// 成品 ObserveCredential @0xc27160 只读写 buntdb，不操作内存 states。
func ObserveCredential(site string, fingerprint string) {
	site = strings.TrimSpace(site)
	fingerprint = strings.TrimSpace(fingerprint)
	if site == "" || fingerprint == "" {
		return
	}
	key := localdb.Key("CookieCredentialFingerprint:" + strings.ToLower(site))
	persisted, _ := localdb.Get(key)
	if persisted == fingerprint {
		return
	}
	_ = localdb.Set(key, fingerprint)
}

// ReportFailure 由站点侧在凭据失效时调用：记录失败事件并提醒管理员确认是否生成二维码。
// states 的 key 是 f.Site（成品 0xc277a0 mapassign_faststr，key=f.Site 0x288/0x290）；
// 是否重复提醒由 buntdb 中 CookieCredentialFingerprint/CookieFailureFingerprint
// 两个完整指纹键共同决定，而不是由内存去重键决定。
func ReportFailure(f Failure) {
	f = normalizeFailure(f)
	if f.Site == "" {
		return
	}
	credentialFP, _ := localdb.Get(localdb.Key("CookieCredentialFingerprint:" + strings.ToLower(f.Site)))
	failureFP, _ := localdb.Get(localdb.Key("CookieFailureFingerprint:" + strings.ToLower(f.Site)))
	alreadyNotified := credentialFP == f.Fingerprint && failureFP == f.Fingerprint

	mu.Lock()
	state, exist := states[f.Site]
	if exist && state.failure.Fingerprint == f.Fingerprint {
		state.failure = f
	} else {
		state = &failureState{
			failure: f,
			sentTo:  make(map[int64]bool),
			pending: make(map[int64]bool),
		}
		states[f.Site] = state
	}
	admins := botAdmins()
	if f.AllowQR {
		for _, admin := range admins {
			state.pending[admin] = true
		}
	}
	mu.Unlock()

	if alreadyNotified {
		logger.WithFields(logrus.Fields{
			"site":               f.Site,
			"cookie_fingerprint": shortFingerprint(f.Fingerprint),
		}).Debug("同一份Cookie失效已提醒过，本次不重复私聊管理员")
		return
	}
	Flush()
}

// Flush 把尚未提醒的失败事件私聊给全部 BOT 管理员。
// 成品 Flush @0xc27a80：入口先要求 bot.Instance（0x1bcc770）非 nil 且
// Online（go.uber.org/atomic.Bool@+0x18）为 true，否则直接返回；
// 每个待处理 state 逐管理员 sendPrivate，成功后写两个完整指纹键
// "CookieCredentialFingerprint:"/"CookieFailureFingerprint:"+ToLower(map key)。
func Flush() {
	if bot.Instance == nil || !bot.Instance.Online.Load() {
		return
	}
	admins := botAdmins()

	mu.Lock()
	defer mu.Unlock()

	for key, state := range states {
		text := failureMessage(state.failure)
		sent := false
		for _, admin := range admins {
			if state.sentTo[admin] {
				continue
			}
			if sendPrivate(admin, text) {
				state.sentTo[admin] = true
				if state.failure.AllowQR {
					state.pending[admin] = true
				}
				sent = true
			}
		}
		if sent {
			_ = localdb.Set(localdb.Key("CookieCredentialFingerprint:"+strings.ToLower(key)), state.failure.Fingerprint)
			_ = localdb.Set(localdb.Key("CookieFailureFingerprint:"+strings.ToLower(key)), state.failure.Fingerprint)
		}
	}
}

// failureMessage 组装单个失败事件的私聊提醒文案。
// 成品结构（failureMessage @0xc27f00）：
//   - 前缀为 110 字节格式串 0x10f0bb0，两个 %s 都取 f.DisplayName；
//   - f.Mode=="cookiecloud"（11 字节字面量比较）追加 155 字节后缀 0x10f2bb9；
//   - 否则 f.AllowQR 为真追加 125 字节 0x10f1a06（内含一个 %s 取 DisplayName）；
//   - 否则追加 119 字节 0x10f172d。
//
// 成品不使用 Failure.Reason。
func failureMessage(f Failure) string {
	prefix := fmt.Sprintf("[%sCookie告警] %sCookie状态异常\n检测到当前登录凭据失效，相关订阅可能无法正常推送", f.DisplayName, f.DisplayName)
	if f.Mode == ModeCookieCloud {
		return prefix + "\n当前为 CookieCloud 模式，Bot不会生成二维码；请在浏览器重新登录并等待CookieCloud同步。\n同一份Cookie只提醒本次一次。"
	}
	if f.AllowQR {
		return prefix + fmt.Sprintf("\n如需生成登录二维码，请私聊回复：生成 %s\n不回复则不会生成；同一份Cookie只提醒本次一次。", f.DisplayName)
	}
	return prefix + "\n当前配置不允许生成二维码，请更新Cookie或调整登录模式。\n同一份Cookie只提醒本次一次。"
}

// HandlePrivateReply 处理管理员对登录二维码提醒的私聊回复。
// 回复“生成”或“生成 站点名”时触发对应站点的二维码生成。
// 第一个返回值表示该消息是否已被消费；第二个返回值是需要私聊回复给管理员的
// 文案，为空表示无需回复，由调用方负责发送。成品证据：HandlePrivateReply
// @0xc28080 的返回寄存器为 rax=bool、rbx:rcx=string，函数内部不再直接 sendPrivate
// 提示文案（只有 func1 完成后回发结果）。
func HandlePrivateReply(sender int64, text string) (bool, string) {
	site, ok := parseGenerateCommand(text)
	if !ok {
		return false, ""
	}
	if sender <= 0 {
		return true, ""
	}
	log := logger.WithFields(logrus.Fields{
		"user_id": sender,
		"site":    site,
	})

	mu.Lock()
	defer mu.Unlock()

	var matched []*failureState
	for _, state := range states {
		if !state.pending[sender] {
			continue
		}
		if site != "" && state.failure.DisplayName != site && state.failure.Site != site {
			continue
		}
		matched = append(matched, state)
	}

	if len(matched) == 0 {
		if site == "" {
			log.Debug("管理员回复生成，但没有等待确认的站点")
			return true, ""
		}
		log.Info("管理员要求生成二维码，但该站点没有待处理的凭据失效")
		return true, ""
	}

	if len(matched) > 1 && site == "" {
		var sites []string
		for _, state := range matched {
			sites = append(sites, state.failure.DisplayName)
		}
		sort.Strings(sites)
		log.Info("管理员回复生成，但目前有多个站点等待处理，需要指定站点")
		// 成品 HandlePrivateReply（prompt.go:160）该分支：前缀 55 字节 rodata 0x10e1c95
		// "目前有多个站点等待处理，请回复：生成 "，分隔符 10 字节 rodata 0x10a7d1d " / 生成 "。
		return true, "目前有多个站点等待处理，请回复：生成 " + strings.Join(sites, " / 生成 ")
	}

	state := matched[0]
	if state.running {
		log.WithField("site", state.failure.Site).Info("该站点的登录二维码已在生成中，忽略重复确认")
		// 成品证据：HandlePrivateReply @0xc28080 +0x46e「登录二维码正在生成或等待扫码，
		// 请查看程序目录和日志。」作为返回文案交给调用方回复管理员。
		return true, "登录二维码正在生成或等待扫码，请查看程序目录和日志。"
	}
	if state.failure.Mode == ModeCookieCloud {
		log.WithField("site", state.failure.Site).Info("管理员确认生成二维码，但当前为 CookieCloud 模式")
		return true, "当前为 CookieCloud 模式，Bot不会生成二维码；请在浏览器重新登录并等待CookieCloud同步。"
	}
	if !state.failure.AllowQR || state.failure.GenerateQR == nil {
		log.WithField("site", state.failure.Site).Info("管理员确认生成二维码，但当前配置不允许")
		return true, "当前配置不允许生成二维码，请更新Cookie或调整登录模式。"
	}

	state.running = true
	generate := state.failure.GenerateQR
	generateSite := state.failure.Site
	generateName := state.failure.DisplayName
	go func() {
		defer func() {
			mu.Lock()
			state.running = false
			mu.Unlock()
		}()
		log.WithField("site", generateSite).Info("管理员已确认，开始生成登录二维码")
		if err := generate(); err != nil {
			log.WithFields(logrus.Fields{
				"site":          generateSite,
				"generate_fail": true,
			}).Errorf("登录二维码流程失败：%v", err)
			// 成品证据：HandlePrivateReply.func1 @0xc288a0 失败回复用 77 字节
			// 0x10ec0ff：站点名 + 失败原因 + 重试命令均带站点名。
			sendPrivate(sender, fmt.Sprintf("%s登录二维码流程失败：%v\n如需重试，请再次回复：生成 %s", generateName, err, generateName))
			return
		}
		// 成品证据：func1 成功回复用 45 字节 0x10d91a7，前缀拼接站点名。
		sendPrivate(sender, generateName+"扫码登录成功，新的Cookie已生效。")
	}()
	// 成品证据：HandlePrivateReply @0xc2863a 以 89 字节 0x10eea2d 作为返回文案。
	return true, "登录二维码开始生成，请查看程序目录和日志并使用官方 App 扫码。"
}

// parseGenerateCommand 解析管理员的“生成 [站点名]”回复。
// 站点名可省略，省略时由调用方在多站点待处理时要求指定。
func parseGenerateCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !(text == "生成" || strings.HasPrefix(text, "生成 ")) {
		return "", false
	}
	site := strings.TrimSpace(strings.TrimPrefix(text, "生成"))
	return site, true
}

// botAdmins 返回全部 BOT 管理员 QQ。
// 管理员保存在本地库 Permission:<uin>:Admin 键下，与 permission 模块的 ListAdmin 一致。
func botAdmins() []int64 {
	var result []int64
	err := localdb.RCoverTx(func(tx *buntdb.Tx) error {
		return tx.Ascend(localdb.PermissionKey(), func(key, value string) bool {
			splits := strings.Split(key, ":")
			if len(splits) != 3 {
				return true
			}
			if splits[2] != "Admin" {
				return true
			}
			uin, err := strconv.ParseInt(splits[1], 10, 64)
			if err != nil {
				logger.WithField("Key", key).Errorf("Parse PermissionKey error %v", err)
				return true
			}
			result = append(result, uin)
			return true
		})
	})
	if err != nil {
		result = nil
		logger.Errorf("botAdmins 查询失败: %v", err)
	}
	return result
}

// sendPrivate 给指定 QQ 发送一条私聊文本。
// 成品 sendPrivate @0xc29280：bot.Instance（0x1bcc770）为 nil 或
// Online（go.uber.org/atomic.Bool@+0x18）为 false 时返回 false；
// uin<=0 或文本去空白后为空同样返回 false。
func sendPrivate(uin int64, text string) bool {
	if bot.Instance == nil || !bot.Instance.Online.Load() {
		return false
	}
	if uin <= 0 || strings.TrimSpace(text) == "" {
		return false
	}
	_, err := localutils.GetBot().SendApi("send_private_msg", map[string]interface{}{
		"user_id":     uin,
		"message":     text,
		"auto_escape": false,
	})
	if err != nil {
		logger.WithField("user_id", uin).Errorf("发送私聊提醒失败: %v", err)
		return false
	}
	return true
}
