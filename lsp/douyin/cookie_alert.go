package douyin

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/authprompt"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/sirupsen/logrus"
)

// 失败计数按功能分别计数：live、news、link_parse、general 互不清零。
// 成品调用点：FreshLiveSnapshot=live、GetPosts=news、fetchDouyinWorkDetail=link_parse、
// (*Concern).Start=general（字符串常量见表内 callers 证据）。
const (
	douyinFailureScopeLive      = "live"
	douyinFailureScopeNews      = "news"
	douyinFailureScopeLinkParse = "link_parse"
	douyinFailureScopeGeneral   = "general"
)

// douyinCookieRequiredFailures 连续多少次认证失败才判定 Cookie 失效。
// 证据：成品 recordDouyinCookieFailureFor @0xd77b60 中 `cmp $0x3`。
const douyinCookieRequiredFailures = 3

// douyinSiteDisplayName 站点展示名（告警标题使用）。
const douyinSiteDisplayName = "抖音"

// douyinCookieFailureState 单一功能的连续失败计数状态。
type douyinCookieFailureState struct {
	consecutiveFailures     int
	consecutiveRiskFailures int
	unhealthy               bool
	alerted                 bool
}

var (
	douyinCookieFailureMutex sync.Mutex
	douyinCookieFailures     = map[string]*douyinCookieFailureState{}
	// douyinCookieFailureFingerprint 是当前计数器所对应的 Cookie Jar 指纹。
	// 证据：成品 recordDouyinCookieFailureFor @0xd77b60 与 resetDouyinCookieFailureScope
	// @0xd78140 都会先比较指纹字符串（全局 @0x1bcfe40/@0x1bcfe48），不同则重建
	// 两个计数/告警字典（@0x1bcfe50 map[string]int、@0x1bcfe58 map[string]bool）。
	douyinCookieFailureFingerprint string
)

// douyinCookieAlertSender 由 lsp 层接线注入的告警发送回调；
// 未注入时仅记录日志（避免平台包反向依赖 lsp）。
var douyinCookieAlertSender func(m *mmsg.MSG)

// isDouyinAuthFailure 判断 err 是否为明确的 Cookie 认证失败（HTTP 401）。
//
// 成品 @0xd77540（672B，cookie_alert.go:28）签名 func(err error) bool：
//   - err==nil 返回 false（入口 `test %rax,%rax; je ret0`），否则调用 err.Error()
//     并经 strings.ToLower（call 0x53b860）得到小写文本；
//   - 对 5 种 401 文本形态逐一 strings.Contains 匹配（callers 证据：FreshLiveSnapshot/
//     GetPosts/fetchDouyinWorkDetail 均传 error）；
//   - 字符串/字节：arr[0] 长度 3 = "401"（0x1098869），prefix 依次
//     "http "（0x109a8e3,len5）、"http code error "（0x10af595,len16）、
//     "status "（0x10a31c9,len7）、"status_code="（0x10aa128,len12）、
//     "response status: "（0x10b0b56,len17）；每轮 runtime.concatstring2(prefix,code)
//     后 internal/stringslite.Index(msg, needle)（callees 已坐实）；
//   - 循环上界 `test %rdx,%rdx; jg ret0` 表示 codes 数组只有 1 个元素（对照
//     configuredDouyinCookieHeader 的 3 元素循环为 `cmp $0x3`），即仅 "401"。
//
// 403/429 属可重试风控，由 isDouyinRetryableRiskFailure（literal "http 403"/"http 429"/
// "http code error 403|429"/"status 403|429"）单独处理，不在本函数判定范围内。
func isDouyinAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, code := range []string{"401"} {
		if strings.Contains(msg, "http "+code) ||
			strings.Contains(msg, "http code error "+code) ||
			strings.Contains(msg, "status "+code) ||
			strings.Contains(msg, "status_code="+code) ||
			strings.Contains(msg, "response status: "+code) {
			return true
		}
	}
	return false
}

// douyinCookieFailureStateFor 取出（或初始化）指定 scope 的计数状态。
func douyinCookieFailureStateFor(scope string) *douyinCookieFailureState {
	state, ok := douyinCookieFailures[scope]
	if !ok {
		state = &douyinCookieFailureState{}
		douyinCookieFailures[scope] = state
	}
	return state
}

// markDouyinCookieUnhealthyFor 记录指定功能的一次 Cookie 失效。
//
// 成品签名：func markDouyinCookieUnhealthyFor(scope string, reason string)（@0xd777e0，896B）。
// 证据：
//   - 入口保存 rax/rbx（scope string）、rcx/rdi（第二参 string），共 4 word；第二参并非 int。
//   - +0x40 currentDouyinCookieJar、+0x4f douyinCookieJarFingerprint 取得指纹；
//   - +0x6a recordDouyinCookieFailureFor(scope, fingerprint)，返回值 (count, reached, justReached)
//     分别存入 0x1a8/0x1b0/0x1c0 后的 0x30(`%rax`)、0x2f(`%bl`)、0x2e(`%cl`)；
//   - `!reached` → logrus Warn「抖音 Cookie 认证请求失败，等待连续确认」（level 3）；
//   - `reached && !justReached` → logrus Debug「抖音 Cookie 已连续失败达到阈值，本次不重复告警」（level 5）；
//   - `reached && justReached` → +0x294 reportDouyinCookieFailure(reason)（用入口保存的 rcx/rdi）。
//
// 日志字段（map[string]interface{}）：reason/consecutive_failures/required_failures/failure_scope。
func markDouyinCookieUnhealthyFor(scope string, reason string) {
	fingerprint := douyinCookieJarFingerprint(currentDouyinCookieJar())
	count, reached, justReached := recordDouyinCookieFailureFor(scope, fingerprint)
	fields := logrus.Fields{
		"reason":               reason,
		"consecutive_failures": count,
		"required_failures":    douyinCookieRequiredFailures,
		"failure_scope":        scope,
	}
	if !reached {
		logger.WithFields(fields).Warn("抖音 Cookie 认证请求失败，等待连续确认")
		return
	}
	if !justReached {
		logger.WithFields(fields).Debug("抖音 Cookie 已连续失败达到阈值，本次不重复告警")
		return
	}
	reportDouyinCookieFailure(reason)
}

// recordDouyinCookieFailureFor 记录指定功能的一次认证失败。
//
// 成品签名：func recordDouyinCookieFailureFor(scope string, fingerprint string) (int, bool, bool)
// （@0xd77b60，768B；唯一 callers 是 markDouyinCookieUnhealthyFor @+0x6a）。
// 证据：
//   - 参数 (rax,rbx)=scope string、(rcx,rdi)=fingerprint string，均被 map key/指纹比较使用；
//   - 指纹与全局保存值不同时重建计数/告警字典（+0x0 起 memequal 比较后 makemap）；
//   - 计数在 @0x1bcfe50 的 map[string]int：旧值 < 3 时写入 旧值+1；
//   - 返回 rax=新计数、bl=新计数>=3(reached)、cl=(reached && 旧值<3)(justReached)；
//   - reached 时向 @0x1bcfe58 的 map[string]bool 写入 true（告警去重标记）。
//
// 不同 scope 之间互不清零；Cookie 指纹变化时整体清零。
func recordDouyinCookieFailureFor(scope string, fingerprint string) (int, bool, bool) {
	douyinCookieFailureMutex.Lock()
	defer douyinCookieFailureMutex.Unlock()
	if douyinCookieFailureFingerprint != fingerprint {
		douyinCookieFailureFingerprint = fingerprint
		douyinCookieFailures = map[string]*douyinCookieFailureState{}
	}
	state := douyinCookieFailureStateFor(scope)
	previous := state.consecutiveFailures
	state.consecutiveFailures++
	state.consecutiveRiskFailures++
	count := state.consecutiveFailures
	reached := count >= douyinCookieRequiredFailures
	justReached := reached && previous < douyinCookieRequiredFailures
	if reached {
		state.unhealthy = true
	}
	return count, reached, justReached
}

// resetDouyinCookieFailures 清零全部功能的失败计数（请求恢复成功时调用）。
func resetDouyinCookieFailures() {
	douyinCookieFailureMutex.Lock()
	defer douyinCookieFailureMutex.Unlock()
	for scope, state := range douyinCookieFailures {
		if state.consecutiveFailures == 0 && !state.unhealthy {
			continue
		}
		state.consecutiveFailures = 0
		state.consecutiveRiskFailures = 0
		state.unhealthy = false
		logger.WithField("failure_scope", scope).
			Infof("抖音 Cookie 请求恢复成功，连续失败计数已清零")
	}
}

// resetDouyinCookieFailureScope 清零单个功能的失败计数。
func resetDouyinCookieFailureScope(scope string) {
	douyinCookieFailureMutex.Lock()
	defer douyinCookieFailureMutex.Unlock()
	state, ok := douyinCookieFailures[scope]
	if !ok {
		return
	}
	if state.consecutiveFailures == 0 && !state.unhealthy {
		return
	}
	state.consecutiveFailures = 0
	state.consecutiveRiskFailures = 0
	state.unhealthy = false
	logger.WithField("failure_scope", scope).
		Infof("抖音 Cookie 对应请求恢复成功，连续失败计数已清零")
}

// reportDouyinCookieFailure 输出（并发送）Cookie 告警。
// douyin.disableCookieAlert 为 true 时关闭告警。
//
// 成品 @0xd78400 关键行为：从 currentDouyinCookieJar + douyinCookieJarFingerprint
// 取指纹（@+0x62/+0x71），填入 authprompt.Failure.Fingerprint 后调用
// authprompt.ReportFailure（@+0x1aa）——指纹即 authprompt 的失败去重键
// （见 lsp/authprompt failureKey = "failure:"+Site+":"+shortFingerprint(Fingerprint)）。
func reportDouyinCookieFailure(reason string) {
	if config.GlobalConfig != nil && !cfg.GetDouyinCookieAlertEnabled() {
		return
	}
	message := fmt.Sprintf("[%sCookie告警] %s", douyinSiteDisplayName, "抖音 Cookie 状态异常")
	if reason != "" {
		message += "：" + reason
	}
	logger.Errorf("%s", message)
	if config.GlobalConfig != nil {
		authprompt.ReportFailure(authprompt.Failure{
			Site:        Site,
			DisplayName: douyinSiteDisplayName,
			Mode:        cfg.GetDouyinCookieMode(),
			Fingerprint: douyinCookieJarFingerprint(currentDouyinCookieJar()),
			Reason:      message,
			AllowQR:     cfg.GetDouyinQRLoginEnabled(),
			GenerateQR:  RunDouyinQRLogin,
		})
	}
	if douyinCookieAlertSender == nil {
		return
	}
	go func() {
		douyinCookieAlertSender(mmsg.NewText(message))
	}()
}

// markDouyinCookieHealthy 清零全部功能并把 Cookie 标记为恢复健康。
func markDouyinCookieHealthy() {
	douyinCookieFailureMutex.Lock()
	for scope := range douyinCookieFailures {
		state := douyinCookieFailures[scope]
		state.alerted = false
	}
	douyinCookieFailureMutex.Unlock()
	// 注意：不能持锁调用 resetDouyinCookieFailures，后者会再次 Lock 同一把
	// 非可重入互斥锁（原实现即因此死锁）。
	resetDouyinCookieFailures()
	// 凭据已恢复：清理 authprompt 的待确认状态，使下一份失效 Cookie 能重新提醒。
	authprompt.ObserveCredential(Site, douyinCookieJarFingerprint(currentDouyinCookieJar()))
}

// markDouyinCookieHealthyFor 清零单个功能并解除其告警状态。
func markDouyinCookieHealthyFor(scope string) {
	douyinCookieFailureMutex.Lock()
	if state, ok := douyinCookieFailures[scope]; ok {
		state.alerted = false
	}
	douyinCookieFailureMutex.Unlock()
	// 注意：不能持锁调用 resetDouyinCookieFailureScope，后者会再次 Lock 同一把
	// 非可重入互斥锁（自死锁）。与 markDouyinCookieHealthy 的写法保持一致。
	resetDouyinCookieFailureScope(scope)
	// 凭据已恢复：清理 authprompt 的待确认状态，使下一份失效 Cookie 能重新提醒。
	authprompt.ObserveCredential(Site, douyinCookieJarFingerprint(currentDouyinCookieJar()))
}
