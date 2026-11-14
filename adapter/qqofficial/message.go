package qqofficial

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// 本文件是消息发送层：把「发一条消息」拆成
// 【选目标】→【选 msg_type】→【被动还是主动】→【占 msg_seq】→【发】→【失败按降级链重来】。
//
// 实测踩出来的硬规矩：
//  1. **msg_seq 必须严格递增**：同一个 msg_id 的第 N 次回复用 msg_seq=N。
//     不许用随机数——官方 JS SDK 的 getNextMsgSeq 就是「时间戳异或随机数取模」，
//     openclaw 的 CHANGELOG 里记着由此引发的 seq 撞车 → 消息被判重丢失的事故。
//  2. **引用索引只认 REFIDX_**：接口对无效引用索引照样返回 200，
//     拿 msg_id 去引用只会让客户端显示「原消息已过期」，靠响应判不出来。
//  3. **被动优先、主动兜底**：窗口（群 300s / 单聊 3600s）或次数
//     （群 5 次 / 单聊 4 次）用尽，就摘掉 msg_id 转主动发送。
//  4. **降级链分层**：markdown 被拒 → 降级纯文本重发一次；
//     富媒体被拒**不降级**（降级成纯文本会让用户以为图已经发出去了），直接报错。
//  5. **@全体每天只有 1 次**：超限（40034142）时平台判**整条消息**失败，不是掉了
//     标记照发正文。所以剥掉 <qqbot-at-everyone /> 重发一次，并把这个目标记进
//     进程内的冷却账本，当天余下时间不再带 @全体。

// 消息类型（msg_type）。群聊没有 6「输入中状态」，那是单聊独有。
const (
	MsgTypeText     = 0 // content
	MsgTypeMarkdown = 2 // markdown.content
	MsgTypeMedia    = 7 // media.file_info，值由 media.go 的上传流程给
)

// RefIdxPrefix 是引用索引（message_reference.message_id）的前缀。
//
// 来源：机器人自己发的消息取发消息响应里的 ext_info.ref_idx；别人发的消息取事件
// message_scene.ext 里的 msg_idx=<REFIDX_…>。
const RefIdxPrefix = "REFIDX_"

// Markdown 被拒的错误码组。
//
// 这批码按「markdown 被拒 → 降级纯文本」的契约就近声明在 message.go：
// errors.go 只允许改一处（40034005 改名），不往里加新码。
// 依据是官方 groups/users messages 接口页的错误码表（文案一并抄在注释里，
// 但**判定只按码**——官方明说 message 文案会变）：
//
//	40034008 markdown 参数有空值          40034009 markdown 参数有换行符
//	40034010 模版参数中不能含有 markdown 语法
//	40034011 无效的 markdown 内容         40034124 markdown 消息参数错误
//
// 无模板权限那两枚（40034127 / 304036）errors.go 里已有，降级判定一并认。
const (
	ErrCodeMarkdownEmptyParam   = 40034008
	ErrCodeMarkdownNewline      = 40034009
	ErrCodeMarkdownSyntaxInTmpl = 40034010
	ErrCodeMarkdownInvalid      = 40034011
	ErrCodeMarkdownParamErr     = 40034124
)

// ErrCodeAtEveryoneLimit 是「@全体成员次数超限」。
//
// 实测码（2026-11-11，同一个机器人同一个群每天只能成功 @全体 1 次），
// **不在官方文档的常见码表里**，是本项目实测出来的：
//
//	{"message":"消息发送失败，@全体成员次数超限","code":40034142,"err_code":40034142}
//
// 与 markdown 那批码一样就近声明在本文件：errors.go 只允许改 40034005 一处。
const ErrCodeAtEveryoneLimit = 40034142

// 被动回复失效相关的码。
const (
	// ErrCodeMsgIDInvalidOrForbidden：请求参数 msg_id 无效或越权。
	ErrCodeMsgIDInvalidOrForbidden = 40034024
	// ErrCodeReplyNotSupported：该事件不支持回复消息。
	ErrCodeReplyNotSupported = 40034027
	// ErrCodeMsgIDExpiredOld：旧码段的「消息 ID 已过期，不能回复」。
	ErrCodeMsgIDExpiredOld = 304103
)

// Target 是一次发送的落点。
//
// Kind 必须显式给 TargetGroup / TargetUser：零值 TargetUnknown 会直接报错，
// 不猜也不默认落到某个目标——群的 group_openid 与用户的 user_openid 填错了
// 就是发错地方。TargetKind 的定义在 media.go（包内唯一一份）。
type Target struct {
	Kind   TargetKind
	OpenID string
}

func (t Target) String() string {
	switch t.Kind {
	case TargetGroup:
		return "group(" + sendClip(t.OpenID) + ")"
	case TargetUser:
		return "user(" + sendClip(t.OpenID) + ")"
	default:
		return fmt.Sprintf("%v(%s)", t.Kind, sendClip(t.OpenID))
	}
}

// path 返回发消息的接口路径（不含 base URL）。
func (t Target) path() (string, error) {
	if strings.TrimSpace(t.OpenID) == "" {
		return "", fmt.Errorf("%s: 发送目标 %v 的 openid 为空", ConfigKeyPrefix, t.Kind)
	}
	switch t.Kind {
	case TargetGroup:
		return fmt.Sprintf(PathGroupMessages, t.OpenID), nil
	case TargetUser:
		return fmt.Sprintf(PathUserMessages, t.OpenID), nil
	default:
		return "", fmt.Errorf("%s: 发送目标的 Kind 没给（拿到 %d），必须显式写 TargetGroup 或 TargetUser",
			ConfigKeyPrefix, uint8(t.Kind))
	}
}

// messagePathByID 返回撤回消息的接口路径。
func (t Target) messagePathByID(messageID string) (string, error) {
	if strings.TrimSpace(messageID) == "" {
		return "", fmt.Errorf("%s: 撤回用的消息 id 为空", ConfigKeyPrefix)
	}
	switch t.Kind {
	case TargetGroup:
		return fmt.Sprintf(PathGroupMessageByID, t.OpenID, messageID), nil
	case TargetUser:
		return fmt.Sprintf(PathUserMessageByID, t.OpenID, messageID), nil
	default:
		return "", fmt.Errorf("%s: 撤回目标的 Kind 没给（拿到 %d），必须显式写 TargetGroup 或 TargetUser",
			ConfigKeyPrefix, uint8(t.Kind))
	}
}

// passiveTTL 是这类目标的被动回复有效期（官方：群 5 分钟、单聊 60 分钟）。
func (t Target) passiveTTL() time.Duration {
	switch t.Kind {
	case TargetGroup:
		return PassiveTTLGroup
	case TargetUser:
		return PassiveTTLUser
	default:
		return 0
	}
}

// maxPassiveReply 是这类目标每条被动消息最多能回几次（官方：群 5 次、单聊 4 次）。
func (t Target) maxPassiveReply() int {
	switch t.Kind {
	case TargetGroup:
		return MaxPassiveReplyGroup
	case TargetUser:
		return MaxPassiveReplyUser
	default:
		return 0
	}
}

// SendOptions 是一次发送的全部输入。msg_type 由内容字段推导：
// file_info 有值 → 7（富媒体）；否则 markdown 有值 → 2；否则 0（纯文本）。
type SendOptions struct {
	// MsgID 是被动回复的事件消息 id（事件体里的 d.id）。为空即主动发送。
	//
	// 给了它也不一定走被动：本地账本判定窗口/次数已用尽时会自动摘掉转主动。
	MsgID string
	// EventID 是**事件帧顶层**的 id（不是 d 里的那个），当被动回复的锚点用。
	//
	// 实测（2026-11-12，一手证据）：GROUP_ADD_ROBOT 的帧顶层带
	//
	//	"id":"GROUP_ADD_ROBOT:1fbfcd00-71bc-4971-b47f-66a2b7bc7794"
	//
	// （事件体 d 里**没有** id）。把它当 event_id 发消息，在 allow_proactive_msg=false
	// 的群里也返回 http=200；同一个群同一天用 msg_id 主动发是 40034105
	// 「主动消息失败, 无权限」，用 event_id 却能过 —— 它是一条被动通道。
	//
	// 与 MsgID 的**互斥语义**：两个都给时以 EventID 为准，并且这时**不发 msg_seq**
	// （实测不带 msg_seq 也能成；硬发还会和 msg_id 那套序号账本打架）。
	//
	// 它**不套用** passive 那套窗口/次数账本：event_id 的有效期与可用次数**未实测**，
	// 没证据说它和 msg_id 同规则，所以这里走单独一条路径，不查也不写 msg_seq 账本。
	EventID string
	// RefIdx 是引用索引，**只接受 REFIDX_ 形态**，给别的形态直接报错。
	//
	// 注意：markdown 消息不挂引用（部分客户端会把正文重复渲染），会被丢弃并打日志。
	RefIdx string
	// Content 是纯文本（msg_type=0）。富媒体消息里它可以当附带文本。
	// markdown 被拒需要降级时它就是兜底文案；为空则自动把 markdown 剥成纯文本。
	Content string
	// Markdown 是 Markdown 内容（msg_type=2）。
	Markdown string
	// FileInfo 是富媒体消息的 media.file_info，来自 media.go 的上传结果。
	FileInfo string
}

// SendResult 是一次发送的结果。
type SendResult struct {
	Target    Target
	MsgID     string // 平台返回的消息 id（可用于撤回）
	RefIdx    string // ext_info.ref_idx：本机器人这条消息自己的引用索引
	Timestamp string // 平台给的发送时间（RFC3339 东八区）
	MsgType   int    // 实际生效的 msg_type（降级后可能与请求时不同）
	MsgSeq    int    // 被动回复序号；主动发送为 0（不发这个字段）
	Passive   bool   // 是否真的走了 **msg_id** 被动回复（不含 event_id 那条路）
	// EventPassive 表示这次是靠 event_id 走的被动通道（SendOptions.EventID 生效了）。
	//
	// 与 Passive 分开记的原因：Passive 专指「msg_id + msg_seq」那条实测有窗口
	// （群 5 分钟 / 单聊 60 分钟）与次数（群 5 次 / 单聊 4 次）限制的路，而 event_id
	// 是另一条通道 —— 不带 msg_seq、不吃 seq 账本、上限**未实测**。两者互斥，
	// 不会同时为真。
	EventPassive bool
	Degraded     bool // 是否发生了 markdown → 纯文本降级
	AtAllDropped bool // 是否为了绕开 @全体每天 1 次的限额，把 <qqbot-at-everyone /> 剥掉了
	Attempts     int  // 实际尝试了几次（含降级与退避重试）
}

// Send 发一条消息，包了「被动优先、主动兜底 + 降级链 + 配额退避」的完整逻辑。
func (c *Client) Send(ctx context.Context, target Target, opts SendOptions) (*SendResult, error) {
	path, err := target.path()
	if err != nil {
		return nil, err
	}

	ref := strings.TrimSpace(opts.RefIdx)
	if ref != "" && !strings.HasPrefix(ref, RefIdxPrefix) {
		return nil, fmt.Errorf("%s: 引用索引必须是 %s 形态（拿到的是 %q）；msg_id（ROBOT1.0_…）不是引用索引，"+
			"实测拿它去引用接口照样返回 200，但客户端只会显示「原消息已过期」",
			ConfigKeyPrefix, RefIdxPrefix, sendClip(ref))
	}

	plan := sendPlan{
		content:  opts.Content,
		markdown: opts.Markdown,
		fileInfo: strings.TrimSpace(opts.FileInfo),
		refIdx:   ref,
	}
	plan.normalizeMsgType()

	// @全体冷却：这个目标今天已经撞过一次「每天 1 次」的限额了，就别再带着标记去撞。
	// 平台的额度超限会让**整条消息**失败（40034142），所以先剥掉再走正常流程，
	// 不去浪费一次注定失败的请求。
	atAllKey := c.scopedKey(target)
	atAllDropped := false
	if atAllCooldowns.cooling(atAllKey, time.Now()) {
		if plan.stripAtEveryone() {
			plan.normalizeMsgType()
			atAllDropped = true
			logger.Warnf("%s: %s 今天已经撞过 @全体每天 1 次的限额，本次先剥掉 %s 再发（正文照发）",
				ConfigKeyPrefix, target, atAllCooldownTag)
		}
	}
	if plan.msgType == MsgTypeText && strings.TrimSpace(plan.content) == "" {
		return nil, fmt.Errorf("%s: 发送内容为空：content / markdown / file_info 至少要给一个", ConfigKeyPrefix)
	}

	// markdown 不挂引用：markdown + message_reference 在 Mac QQ 等客户端会把正文
	// 重复渲染（卡片 + 纯文本），这是既定的行为。
	if plan.msgType == MsgTypeMarkdown && plan.refIdx != "" {
		logger.Warnf("%s: markdown 消息不挂引用（部分客户端会重复渲染正文），已丢弃 ref_idx=%s",
			ConfigKeyPrefix, sendClip(plan.refIdx))
		plan.refIdx = ""
	}

	// 被动优先，而且**两条被动路互斥**：
	//   - event_id（进群事件帧顶层的 id）：实测不带 msg_seq 就能发出去。它的有效期与
	//     可用次数**未实测**，没有证据说它和 msg_id 同规则，所以单独一条路径 ——
	//     不查也不写 msg_seq 账本，别凭空造出「窗口用尽」的假判。
	//   - msg_id + msg_seq：老路，窗口（群 5 分钟 / 单聊 60 分钟）与次数
	//     （群 5 次 / 单聊 4 次）那套账本照旧。
	// 两个都给时 event_id 优先，msg_id 与 msg_seq 一律不发。
	eventID := strings.TrimSpace(opts.EventID)
	plan.eventID = eventID
	tracker := c.seqTrackerFor()
	key := msgSeqKey{kind: target.Kind, openID: target.OpenID, msgID: strings.TrimSpace(opts.MsgID)}
	if plan.eventID != "" {
		if key.msgID != "" {
			logger.Infof("%s: %s 同时给了 event_id 与 msg_id，按 event_id 发（两者互斥，event_id 优先）："+
				"event_id=%s msg_id=%s",
				ConfigKeyPrefix, target, sendClip(plan.eventID), sendClip(key.msgID))
		}
	} else if key.msgID != "" {
		now := time.Now()
		if tracker.usable(key, target.passiveTTL(), target.maxPassiveReply(), now) {
			plan.msgID = key.msgID
			plan.seq = tracker.allocate(key, now)
		} else {
			logger.Warnf("%s: 被动回复窗口或次数已用尽（%s，窗口 %s、上限 %d 次），摘掉 msg_id 改走主动：msg_id=%s",
				ConfigKeyPrefix, target, target.passiveTTL(), target.maxPassiveReply(), sendClip(key.msgID))
		}
	}

	result := &SendResult{
		Target:       target,
		MsgType:      plan.msgType,
		MsgSeq:       plan.seq,
		Passive:      plan.msgID != "",
		EventPassive: plan.eventID != "",
	}

	policy := c.sendPolicyFor()
	var (
		degraded       bool
		passiveDropped bool
		eventDropped   bool
		seqBumped      bool
		quotaRetries   int
		lastErr        error
	)

	// 一次 Send 最多尝试 policy.MaxAttempts 次：降级、摘 msg_id、换 seq、退避
	// 都只是把 plan 改一改再走一圈；每种降级各由旗标控制只做一次，不会乒乓。
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		result.Attempts = attempt

		var resp sendMessageResponse
		err := c.PostJSON(ctx, path, plan.body(), &resp)
		if err == nil {
			if plan.msgID != "" {
				tracker.commit(key)
			}
			result.MsgID = resp.ID
			result.RefIdx = resp.ExtInfo.RefIdx
			result.Timestamp = resp.Timestamp
			result.MsgType = plan.msgType
			result.MsgSeq = plan.seq
			result.Passive = plan.msgID != ""
			result.EventPassive = plan.eventID != ""
			result.Degraded = degraded
			result.AtAllDropped = atAllDropped
			return result, nil
		}
		lastErr = err

		apiErr, ok := AsAPIError(err)
		if !ok {
			// 传输层错误：不知道平台收没收到，所以原样重发（沿用同一个 msg_seq——
			// 万一真发出去了，换序号会把「回复同一条消息」的语义搞乱）。
			if attempt >= policy.TransportAttempts || !IsRetryable(err) {
				return nil, err
			}
			logger.Warnf("%s: %s 发送撞传输层错误（第 %d 次尝试），%s 后原样重发：%v",
				ConfigKeyPrefix, target, attempt, policy.TransportBackoff, err)
			if werr := sendSleepCtx(ctx, policy.TransportBackoff); werr != nil {
				return nil, err
			}
			continue
		}

		code, errCode := apiErr.Code, apiErr.ErrCode
		switch {
		case plan.msgType == MsgTypeMarkdown && !degraded && isMarkdownRejectedCode(code, errCode):
			fallback := strings.TrimSpace(plan.content)
			if fallback == "" {
				fallback = plainTextFromMarkdown(plan.markdown)
			}
			if fallback == "" {
				logger.Warnf("%s: markdown 被拒（code=%d err_code=%d, message=%q），但剥不出非空纯文本，不再降级",
					ConfigKeyPrefix, code, errCode, apiErr.Message)
				return nil, apiErr
			}
			logger.Warnf("%s: markdown 被拒（code=%d err_code=%d, message=%q），按降级链改纯文本重发：%s",
				ConfigKeyPrefix, code, errCode, apiErr.Message, sendClip(fallback))
			degraded = true
			plan.msgType = MsgTypeText
			plan.markdown = ""
			plan.content = fallback

		case plan.eventID != "" && !eventDropped && isPassiveInvalidCode(code, errCode):
			// event_id 被平台拒：摘掉它、按普通发送重来一次。
			//
			// 语义只有一句话，别绕：**这次重发是主动消息，不是被动回复**。所以
			//   - 群里有主动消息权限 → 消息发得出去，但平台记的是主动量；
			//   - 群里没有（allow_proactive_msg=false，实测很常见）→ **白打一次请求**，
			//     立刻拿到 40034105。这不是 bug 而是代价：那条错误比「event_id 无效」
			//     更可操作，而且硬带着失效的 event_id 重发是 100% 失败。
			//
			// 与 adapter.go 旧注释（「绝不回退到主动发送，回退只会换来 40034105」）的关系：
			// **以本处为准**。已发生的事实压过文档 —— 回退在有权限的群确实能把消息发出去。
			// 两层语义统一到「允许回退一次，但日志必须明说它变成了主动消息」，
			// adapter.go 的注释已按此订正，两处现在是同一套说法。
			//
			// 依据强度说清楚：**event_id 的失效码没有一手实测**（实测只到「能发出去」
			// 这一步），这里复用 msg_id 那批码纯属保守兜底 —— 万一真失效了，硬带着它
			// 重发只会把这条消息钉死。只做一次（eventDropped 旗标）。
			eventDropped = true
			logger.Warnf("%s: event 被动被平台拒（code=%d err_code=%d, message=%q）；"+
				"摘掉 event_id 重发一次 —— 注意这次重发**将成为主动消息**（不再是被动回复）："+
				"群里有主动消息权限才发得出去，没有的群会白打一次请求并拿到 40034105。"+
				"event_id 的失效码未实测，这里是保守兜底，只做一次：event_id=%s",
				ConfigKeyPrefix, code, errCode, apiErr.Message, sendClip(plan.eventID))
			plan.eventID = ""

		case plan.msgID != "" && !passiveDropped && isPassiveInvalidCode(code, errCode):
			logger.Warnf("%s: 被动回复被平台判失效（code=%d err_code=%d, message=%q），摘掉 msg_id 改走主动兜底",
				ConfigKeyPrefix, code, errCode, apiErr.Message)
			passiveDropped = true
			tracker.exhaust(key)
			plan.msgID = ""
			plan.seq = 0

		case plan.msgID != "" && !seqBumped && isDuplicateSeqCode(code, errCode):
			seqBumped = true
			plan.seq = tracker.allocate(key, time.Now())
			logger.Warnf("%s: 消息被判重（code=%d err_code=%d），本地计数器与平台对不上，"+
				"换 msg_seq=%d 重发一次：msg_id=%s",
				ConfigKeyPrefix, code, errCode, plan.seq, sendClip(key.msgID))

		case isProactiveNoPermissionCode(code, errCode):
			// 40034105 是**权限**问题，不是配额：群主没开「允许机器人主动发送消息」，
			// 平台对主动纯文本 / 主动 markdown 都回同一枚码（实测 2026-11-12）。
			// 重试一万次也不会成功，只会把发送队列拖慢、日志刷屏 —— 所以一次就交回，
			// 并把「去群里开开关」写进错误文本：上层每条推送都会把这句 Error 记下来，
			// 那句日志本身就够用了。告警另按群限流（同群 1 次/小时）。
			// 注意 adapter.go 启动期还有一条每群**永久一次**的同类告警（warnProactiveDeniedOnce）：
			// 那条讲前提、这条讲这一次失败，分工与「不刷屏」的边界见 proactiveDenyWarns 的注释。
			c.warnProactiveNoPermission(target, apiErr, time.Now())
			return nil, proactiveNoPermissionError(target, apiErr)

		case isQuotaGateCode(code, errCode) && quotaRetries < policy.QuotaAttempts:
			quotaRetries++
			wait := policy.backoff(quotaRetries)
			logger.Warnf("%s: 撞配额闸门（code=%d err_code=%d, message=%q），第 %d 次退避 %s 后重试（不立刻重发）",
				ConfigKeyPrefix, code, errCode, apiErr.Message, quotaRetries, wait)
			if werr := sendSleepCtx(ctx, wait); werr != nil {
				return nil, apiErr
			}

		case isAtEveryoneLimitCode(code, errCode) && !atAllDropped:
			// @全体成员每天只有 1 次，撞了之后平台是把**整条消息**判失败
			// （不是掉了这一枚标记、正文照发）。所以只剩一条路：把标记从 markdown
			// 与 content 里都剥掉，正文原样重发一次。
			//
			// 只做一次（atAllDropped 旗标），并且顺手把目标记进冷却账本——
			// 今天余下的时间不再带 @全体，省掉后面每一条推送的这一次白撞。
			atAllDropped = true
			atAllCooldowns.block(atAllKey, time.Now())
			if !plan.stripAtEveryone() {
				logger.Warnf("%s: 撞 @全体限额（code=%d err_code=%d, message=%q），但请求里找不到 %s，无从降级",
					ConfigKeyPrefix, code, errCode, apiErr.Message, atAllCooldownTag)
				return nil, apiErr
			}
			plan.normalizeMsgType()
			if plan.msgType == MsgTypeText && strings.TrimSpace(plan.content) == "" {
				// 理论上不该出现（消息只有一枚 @全体 标签），但宁可报错也不发空串请求。
				logger.Warnf("%s: 撞 @全体限额（code=%d err_code=%d），剥掉 %s 之后什么都不剩，不发空消息",
					ConfigKeyPrefix, code, errCode, atAllCooldownTag)
				return nil, apiErr
			}
			logger.Warnf("%s: 撞 @全体每天 1 次的限额（code=%d err_code=%d, message=%q），剥掉 %s 后重发（正文照发），"+
				"今天余下时间对 %s 不再带 @全体",
				ConfigKeyPrefix, code, errCode, apiErr.Message, atAllCooldownTag, target)

		default:
			// 网关层失败（HTTP 5xx / 408、或不带业务码的裸 429）：errors.go 的
			// ClassifyHTTP 早就把它们标成了 KindRetryable，但响应体被判成 *APIError，
			// 走不进上面那条「非 *APIError」的传输层重发分支——APIError.Retryable()
			// 于是成了发送路径上的死代码，网关抖一下这条消息就没了。
			//
			// 放在前面几条降级 / 权限 case **之后**：只有它们全不匹配时才考虑原样重发，
			// 所以 markdown 降级 / 被动失效转主动 / 重号换 seq / 配额退避的优先级不变。
			// 重发沿用同一个 msg_seq（万一平台真收到了，换序号会把「回复同一条消息」的语义搞乱），
			// 预算与传输层分支共用 policy.TransportAttempts（总尝试次数上限，默认 2），
			// 外层还有 policy.MaxAttempts 封顶 —— 不会无限重发。
			if attempt >= policy.TransportAttempts || !shouldRetrySend(apiErr) {
				return nil, apiErr
			}
			logger.Warnf("%s: %s 发送撞网关层失败（第 %d 次尝试，HTTP %d, code=%d, err_code=%d），%s 后原样重发：%v",
				ConfigKeyPrefix, target, attempt, apiErr.HTTPStatus, code, errCode, policy.TransportBackoff, apiErr)
			if werr := sendSleepCtx(ctx, policy.TransportBackoff); werr != nil {
				return nil, apiErr
			}
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("%s: %s 发送尝试次数用尽（上限 %d 次）", ConfigKeyPrefix, target, policy.MaxAttempts)
	}
	return nil, lastErr
}

// SendText 发纯文本（msg_type=0）。extra 里可以带 MsgID / RefIdx。
func (c *Client) SendText(ctx context.Context, target Target, content string, extra SendOptions) (*SendResult, error) {
	extra.Content = content
	extra.Markdown = ""
	extra.FileInfo = ""
	return c.Send(ctx, target, extra)
}

// SendMarkdown 发 Markdown（msg_type=2）。
//
// 被拒时按降级链改纯文本重发一次，兜底文案取 extra.Content；extra.Content 为空
// 则自动把 markdown 语法剥成纯文本。
func (c *Client) SendMarkdown(ctx context.Context, target Target, markdown string, extra SendOptions) (*SendResult, error) {
	extra.Markdown = markdown
	extra.FileInfo = ""
	return c.Send(ctx, target, extra)
}

// SendMedia 发富媒体（msg_type=7），fileInfo 来自 media.go 的上传结果。
//
// 富媒体失败**不降级**：直接返回错误——降级成纯文本会让用户以为图已经发出去了。
func (c *Client) SendMedia(ctx context.Context, target Target, fileInfo string, extra SendOptions) (*SendResult, error) {
	extra.FileInfo = fileInfo
	return c.Send(ctx, target, extra)
}

// DeleteMessage 撤回一条本机器人发的消息（实测超过 2 分钟不可撤回，10 QPS）。
func (c *Client) DeleteMessage(ctx context.Context, target Target, messageID string) error {
	path, err := target.messagePathByID(messageID)
	if err != nil {
		return err
	}
	return c.DeleteJSON(ctx, path, nil)
}

// sendPlan 是一次发送的当前形态：降级链每走一步就改这个结构，再重发一次。
type sendPlan struct {
	msgType  int
	content  string
	markdown string
	fileInfo string
	refIdx   string
	msgID    string
	seq      int
	// eventID 非空表示这次是「回复某个事件帧」的被动发送（见 SendOptions.EventID）。
	// 它有值时请求体不带 msg_id / msg_seq，也不参与 seq 账本。
	eventID string
}

// normalizeMsgType 按当前内容重新推导 msg_type：file_info 优先，其次 markdown，
// 最后纯文本。降级链每改一次内容都要重算一遍——@全体 剥完 markdown 空了、content
// 还有字时，必须从 2 切回 0，否则会发一条 markdown.content="" 的请求。
func (p *sendPlan) normalizeMsgType() {
	switch {
	case p.fileInfo != "":
		p.msgType = MsgTypeMedia
	case strings.TrimSpace(p.markdown) != "":
		p.msgType = MsgTypeMarkdown
	default:
		p.msgType = MsgTypeText
	}
}

// stripAtEveryone 把 @全体标记从 markdown 与 content 里都剥掉，返回是否真的剥到了东西。
//
// 只剥 <qqbot-at-everyone />：<qqbot-at-user id="…"/> 是 @某个人，与每天 1 次的
// @全体 限额无关，剥掉它等于把消息语义改坏。
func (p *sendPlan) stripAtEveryone() bool {
	md, mdHit := removeAtEveryoneTag(p.markdown)
	content, contentHit := removeAtEveryoneTag(p.content)
	if !mdHit && !contentHit {
		return false
	}
	p.markdown = md
	p.content = content
	return true
}

// body 把当前形态拼成请求体。
//
// 注意：msg_seq 只在 **msg_id** 被动发送时才发。没有 msg_id 时平台按默认值 1 处理，
// 主动消息本来也不参与 msg_id + msg_seq 去重，显式发等于多给一个可能撞车的字段。
// event_id 同理：实测不带 msg_seq 也能发出去（带上也不报错，但没有意义）。
//
// event_id 与 msg_id **互斥，event_id 优先**：两个都有时只发 event_id。
func (p sendPlan) body() *sendMessageRequest {
	req := &sendMessageRequest{MsgType: p.msgType}
	switch {
	case p.eventID != "":
		req.EventID = p.eventID
	case p.msgID != "":
		req.MsgID = p.msgID
		req.MsgSeq = p.seq
	}
	if p.refIdx != "" {
		req.MessageReference = &messageReference{MessageID: p.refIdx}
	}
	switch p.msgType {
	case MsgTypeMedia:
		req.Media = &mediaPayload{FileInfo: p.fileInfo}
		req.Content = p.content
	case MsgTypeMarkdown:
		// 传了 markdown，content 必须为空（官方接口页原话），否则参数错。
		req.Markdown = &markdownPayload{Content: p.markdown}
	default:
		req.Content = p.content
	}
	return req
}

// sendMessageRequest 是发消息的请求体。
type sendMessageRequest struct {
	MsgType  int              `json:"msg_type"`
	Content  string           `json:"content,omitempty"`
	Markdown *markdownPayload `json:"markdown,omitempty"`
	Media    *mediaPayload    `json:"media,omitempty"`
	MsgID    string           `json:"msg_id,omitempty"`
	MsgSeq   int              `json:"msg_seq,omitempty"`
	// EventID 是事件帧顶层的 id（实测只有 GROUP_ADD_ROBOT 有样本），当被动锚点用。
	// 与 msg_id 互斥：给了它就不发 msg_id / msg_seq（见 sendPlan.body）。
	EventID          string            `json:"event_id,omitempty"`
	MessageReference *messageReference `json:"message_reference,omitempty"`
}

type markdownPayload struct {
	Content string `json:"content"`
}

// mediaPayload 是发消息时的 media 字段。media.go 的 MediaInfo 是上传结果的形态，
// 两者别混：这里只要 file_info 一个字段。
type mediaPayload struct {
	FileInfo string `json:"file_info"`
}

type messageReference struct {
	MessageID string `json:"message_id"`
}

// sendMessageResponse 是发消息的响应（响应解析按宽松口径：未知字段忽略）。
type sendMessageResponse struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	ExtInfo   struct {
		RefIdx string `json:"ref_idx"`
	} `json:"ext_info"`
}

// ---------------------------------------------------------------------------
// @全体冷却账本
// ---------------------------------------------------------------------------

// atAllCooldownTag 是 @全体成员的标记。实测纯文本 msg_type=0 也认这一枚标签。
const atAllCooldownTag = "<qqbot-at-everyone />"

// atAllCooldown 记「这个发送目标今天已经把 @全体用掉了」。
//
// 为什么需要它：平台的 @全体 是**每天 1 次**，第 2 次不只拒绝这一枚标记，
// 而是整条消息失败（40034142）。只做单条降级的话，撞过之后当天每一条带 @全体
// 的推送都要先废一条请求再重发；不如直接记住「今天别带了」。
//
// 账本是进程级的：Client 的字段在 api.go，改动范围不含它，而且这个状态本来就
// 只该活在本次进程里——重启后不再带 @全体反而更保险（平台侧额度可能已经恢复）。
type atAllCooldown struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func newAtAllCooldown() *atAllCooldown {
	return &atAllCooldown{until: make(map[string]time.Time)}
}

// warnThrottle 是进程内的日志限流表：同一个 key 在 interval 内只放行一次。
//
// 用它挡住的是「配置问题刷屏」——比如某个群没开主动发送权限，每次订阅推送都会失败，
// 而无权限这条信息本身一小时说一次就够了。进程级：重启后重新喊一遍，反而更保险
// （重启往往正是人来看日志的时候）。
type warnThrottle struct {
	interval time.Duration
	mu       sync.Mutex
	at       map[string]time.Time
}

func newWarnThrottle(interval time.Duration) *warnThrottle {
	return &warnThrottle{interval: interval, at: make(map[string]time.Time)}
}

// allow 判断这个 key 现在能不能打日志：能就顺手记下这次的时间。
//
// key 为空一律不放行：调用方没拿到目标（TargetUnknown）时不该凭空产生一条无主的日志。
func (t *warnThrottle) allow(key string, now time.Time) bool {
	if t == nil || key == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.at[key]; ok && now.Sub(last) < t.interval {
		return false
	}
	t.sweepLocked(now)
	t.at[key] = now
	return true
}

// sweepLocked 清掉过期条目，别让表无上限长（每次放行顺手扫一遍就够）。
func (t *warnThrottle) sweepLocked(now time.Time) {
	for key, last := range t.at {
		if now.Sub(last) >= t.interval {
			delete(t.at, key)
		}
	}
}

// size 返回表里的条目数（排查与测试用）。
func (t *warnThrottle) size() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.at)
}

// reset 清空限流表（单测用：用例之间别互相压掉告警）。
func (t *warnThrottle) reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.at = make(map[string]time.Time)
}

// scopedKey 返回「机器人（appid）+ 目标 openid」作用域的键。
//
// 「每天 1 次」的 @全体额度、以及「无主动消息权限」的告警限流都按这个作用域算：
// 同一进程里跑多个机器人时，A 撞上了不该让 B 也闭嘴。目标类型没给（TargetUnknown）
// 时返回空串，那时既不查也不写——不猜，也就不会误伤。
func (c *Client) scopedKey(target Target) string {
	if c == nil {
		return ""
	}
	switch target.Kind {
	case TargetGroup, TargetUser:
	default:
		return ""
	}
	appID := ""
	if c.tm != nil {
		appID = c.tm.AppID()
	}
	return appID + "\x00" + target.OpenID
}

// resetAtAllCooldowns 清空进程级 @全体 冷却账本。**只给测试用**。
//
// 账本是进程级的，go test -count=N 同进程连跑多轮时，上一轮留下的冷却会让下一轮的
// 用例误以为「今天已经撞过限额」，一批 @全体 用例会连带变红。测试建自己的假平台时
// 顺手清一次，保证每轮从干净状态开始（生产路径绝不调用）。
func resetAtAllCooldowns() { atAllCooldowns = newAtAllCooldown() }

// atAllCooldowns 是进程级账本（key = appid + openid，见 scopedKey）。
var atAllCooldowns = newAtAllCooldown()

// cooling 判断这个目标是不是还在当天的 @全体 冷却里；过期的条目顺手删掉。
func (a *atAllCooldown) cooling(key string, now time.Time) bool {
	if a == nil || key == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.until[key]
	if !ok {
		return false
	}
	if !now.Before(until) {
		delete(a.until, key)
		return false
	}
	return true
}

// block 记下「这个目标今天不能再带 @全体」，过期时间取次日零点（进程本地时区）。
func (a *atAllCooldown) block(key string, now time.Time) {
	if a == nil || key == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(now)
	a.until[key] = nextDayStart(now)
}

// sweepLocked 清掉已经过期的条目，别让账本无上限长（每次写入顺手扫一遍就够）。
func (a *atAllCooldown) sweepLocked(now time.Time) {
	for key, until := range a.until {
		if !now.Before(until) {
			delete(a.until, key)
		}
	}
}

// size 返回账本里的条目数（排查与测试用）。
func (a *atAllCooldown) size() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.until)
}

// nextDayStart 返回 now 之后最近的本地零点。
//
// 刻意只用 time.Now() 的本地时区，不引时区库、不查配置：平台的「每天」按
// 东八区算，而部署机就在东八区；真跨时区部署的话这里只会早一点或晚一点
// 解除冷却，最坏结果是少带一次 @全体，不会多发。
func nextDayStart(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
}

// ---------------------------------------------------------------------------
// msg_seq 计数器
// ---------------------------------------------------------------------------

// msgSeqKey 是 msg_seq 账本的键：目标（类型 + openid）+ 被动的 msg_id。
//
// 为什么按 msg_id 而不是一个全局计数器：官方语义是「同一个 msg_id 的第 N 次回复用
// msg_seq=N」。按 msg_id 计数既满足递增，也不会因为两个群的回复互相推高序号。
type msgSeqKey struct {
	kind   TargetKind
	openID string
	msgID  string
}

type msgSeqState struct {
	// first 是**本地第一次分配被动序号**的时刻，窗口从这里起算。
	//
	// 已知偏差（故意保留，不是没想到）：平台的被动回复窗口按**事件发生时间**算
	// （群 5 分钟 / 单聊 60 分钟），不是按「我们第一次回复」算。所以回复得越晚，
	// 这里的窗口就比平台宽一点——判「还能被动回」的那一次可能多撞一次
	// 40034005 / 40034128，然后由 Send 的 passiveDropped 分支摘掉 msg_id 降级成
	// 主动兜底（见下面 Send 里的分支）。代价是慢回复场景多发一次注定失败的请求。
	//
	// 为什么没改成事件时间：事件时间只在 adapter 侧手里（passiveAnchor.At 与事件体的
	// timestamp），而 SendOptions 目前没有承载字段。凭空加一个没人填的字段等于没改，
	// 所以这一步留作后续：由 adapter 侧把事件时间传进来（例如 SendOptions.EventAt
	// time.Time），usable/allocate 再以它为准。现在的偏差与代价上面已写明。
	first     time.Time
	allocated int  // 已分配出去的最大 msg_seq
	used      int  // 真正发成功的次数（决定次数上限）
	dead      bool // 平台说这个 msg_id 不能用了（过期/越权），别再走被动
}

// msg_seq 账本的清理节奏。保留时间必须大于单聊被动窗口（60 分钟），
// 否则把还在窗口内的状态清掉，序号会从 1 重来 → 撞 40054005。
const (
	msgSeqSweepEvery  = 256
	msgSeqSweepRetain = 2 * time.Hour
	msgSeqSweepSize   = 1024
)

type seqTracker struct {
	mu     sync.Mutex
	states map[msgSeqKey]*msgSeqState
	calls  int
}

func newSeqTracker() *seqTracker {
	return &seqTracker{states: make(map[msgSeqKey]*msgSeqState)}
}

// usable 判断这条被动消息还能不能回：窗口没过、次数没用完、没被平台判死。
func (t *seqTracker) usable(key msgSeqKey, ttl time.Duration, limit int, now time.Time) bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.calls++; t.calls%msgSeqSweepEvery == 0 || len(t.states) > msgSeqSweepSize {
		t.sweepLocked(now)
	}
	st := t.states[key]
	if st == nil {
		return true
	}
	if st.dead {
		return false
	}
	if limit > 0 && st.used >= limit {
		return false
	}
	if ttl > 0 && !st.first.IsZero() && now.Sub(st.first) >= ttl {
		return false
	}
	return true
}

// allocate 占一个序号：同一个 msg_id 的第 N 次回复返回 N（从 1 开始）。
func (t *seqTracker) allocate(key msgSeqKey, now time.Time) int {
	if t == nil {
		return DefaultMsgSeq
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.states[key]
	if st == nil {
		// 窗口起点 = 本地第一次分配的时刻（不是事件时间，偏差与代价见 msgSeqState.first）
		st = &msgSeqState{first: now}
		t.states[key] = st
	}
	if st.first.IsZero() {
		st.first = now
	}
	st.allocated++
	if st.allocated < DefaultMsgSeq {
		st.allocated = DefaultMsgSeq
	}
	return st.allocated
}

// commit 记一次成功。次数上限只按成功计——失败的尝试不该吃掉被动配额。
func (t *seqTracker) commit(key msgSeqKey) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if st := t.states[key]; st != nil {
		st.used++
	}
}

// exhaust 把这个 msg_id 标死：不再走被动，转主动兜底。
func (t *seqTracker) exhaust(key msgSeqKey) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.states[key]
	if st == nil {
		st = &msgSeqState{first: time.Now()}
		t.states[key] = st
	}
	st.dead = true
}

// sweepLocked 清掉早就用不上的条目，别让账本无上限长。
func (t *seqTracker) sweepLocked(now time.Time) {
	for key, st := range t.states {
		if st.first.IsZero() || now.Sub(st.first) >= msgSeqSweepRetain {
			delete(t.states, key)
		}
	}
}

// size 返回账本里的条目数（排查与测试用）。
func (t *seqTracker) size() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.states)
}

// ---------------------------------------------------------------------------
// 错误码分类（发送层专用的小判断；通用业务分类仍以 errors.go 的 Classify 为准）
// ---------------------------------------------------------------------------

// isMarkdownRejectedCode 是不是「markdown 这条内容不行」——可以降级成纯文本。
func isMarkdownRejectedCode(code, errCode int) bool {
	for _, c := range []int{code, errCode} {
		switch c {
		case ErrCodeMarkdownEmptyParam, ErrCodeMarkdownNewline, ErrCodeMarkdownSyntaxInTmpl,
			ErrCodeMarkdownInvalid, ErrCodeMarkdownParamErr,
			ErrCodeNoMarkdownTemplate, ErrCodeMarkdownNoPermission:
			return true
		}
	}
	return false
}

// isPassiveInvalidCode 是不是「这个 msg_id 不能用来回复了」——摘掉转主动。
func isPassiveInvalidCode(code, errCode int) bool {
	for _, c := range []int{code, errCode} {
		switch c {
		case ErrCodeReplyMsgIDExpired, // 40034005：回复消息 msg_id 已过期（实测与接口页一致）
			ErrCodeRFCallbackLimit,         // 40034128：被动回复时间或次数超限
			ErrCodeMsgIDInvalidOrForbidden, // 40034024：msg_id 无效或越权
			ErrCodeMsgIDExpiredOld:         // 304103：旧码段的「消息 ID 已过期」
			return true
		}
	}
	return false
}

// isDuplicateSeqCode 是不是「这个 msg_id + msg_seq 已经发过了」——换个序号重发。
func isDuplicateSeqCode(code, errCode int) bool {
	for _, c := range []int{code, errCode} {
		if c == ErrCodeMessageDuplicated { // 40054005
			return true
		}
	}
	return false
}

// isAtEveryoneLimitCode 是不是「@全体成员每天次数超限」——剥掉标记重发。
func isAtEveryoneLimitCode(code, errCode int) bool {
	for _, c := range []int{code, errCode} {
		if c == ErrCodeAtEveryoneLimit {
			return true
		}
	}
	return false
}

// shouldRetrySend 判断这条发送失败该不该原样重发（退避后再来一次）。
//
// 判定完全复用 errors.go 的分类：5xx、408、以及标了 KindRetryable 的业务码。
// **故意排除配额闸门那两枚**（isQuotaGateCode 认的那两个）：它们有自己的退避预算，
// 预算用尽后就该失败收场，不能被这里再偷偷放大一轮。
// 40034105 不在这里也没关系：它已经不再是 KindQuota/KindRetryable（纯权限），
// 而且主循环里有一条显式 case 先行拦截。
func shouldRetrySend(apiErr *APIError) bool {
	if apiErr == nil || !apiErr.Retryable() {
		return false
	}
	return !isQuotaGateCode(apiErr.Code, apiErr.ErrCode)
}

// isQuotaGateCode 是不是配额/频控闸门——指数退避后再试，不立刻重发。
//
// 只收「等一等真的会好」的两枚。**40034105（主动消息无权限）故意不在里面**：
// 实测它是群主没开「允许机器人主动发送消息」，退避再久也不会成功，
// 见 isProactiveNoPermissionCode。
func isQuotaGateCode(code, errCode int) bool {
	for _, c := range []int{code, errCode} {
		switch c {
		case ErrCodeProactiveOverQuota, // 40034100：主动消息超过频控
			ErrCodeRateLimited: // 100001：频率过高
			return true
		}
	}
	return false
}

// isProactiveNoPermissionCode 是不是「这个群没开主动发送权限」——不可重试的权限失败。
func isProactiveNoPermissionCode(code, errCode int) bool {
	for _, c := range []int{code, errCode} {
		if c == ErrCodeProactiveNoPermission { // 40034105
			return true
		}
	}
	return false
}

// proactiveNoPermissionHint 是给操作者的那句话：说清原因、说清去哪儿开、说清后果。
//
// 实测（2026-11-12）：没有权限的群 bot_state 是 allow_proactive_msg=false，
// 主动消息一律回 code=40034105；有权限的群是 true + recv_msg_setting="all"。
func proactiveNoPermissionHint(target Target) string {
	if target.Kind == TargetGroup {
		return "这个群没有开启「允许机器人主动发送消息」，订阅推送发不出去；" +
			"请群主在群设置的「机器人权限设置」里打开它（打开后无需重启，后续推送会继续尝试）。" +
			"这是权限问题，自动重试无解，所以本条不再退避重发"
	}
	return "该单聊会话没有主动消息权限（平台 code=40034105），主动推送发不出去；" +
		"这是权限问题，自动重试无解，所以本条不再退避重发"
}

// proactiveNoPermissionError 把 40034105 包成一条可操作的错误。
//
// 用 %w 保住 *APIError：上层仍能按业务码判定，AsAPIError 也照旧可用。
func proactiveNoPermissionError(target Target, apiErr *APIError) error {
	if apiErr == nil {
		return fmt.Errorf("%s: %s 发不出主动消息（没有权限）：%s",
			ConfigKeyPrefix, target, proactiveNoPermissionHint(target))
	}
	return fmt.Errorf("%w —— %s", apiErr, proactiveNoPermissionHint(target))
}

// proactiveDenyWarnInterval 是同一目标「没有主动消息权限」告警的限流间隔。
//
// 一小时足够：这是个配置问题，群主看到就会去开，重复喊只会淹掉别的告警。
const proactiveDenyWarnInterval = time.Hour

// proactiveDenyWarns 是进程级告警限流表（key 见 scopedKey）。
//
// 关于「没开主动消息权限」这件事，全仓有**两条**告警，key 与策略都不同，别当成重复实现删掉一头：
//
//   - adapter.go 的 warnProactiveDeniedOnce（**启动期**，读 bot_state 的 allow_proactive_msg）：
//     每个群**永久一次**，讲的是「前提」——这个群没开权限、订阅推送会全部发不出去、去哪儿开；
//   - 本文件这一条 warnProactiveNoPermission（**发送期**，真撞上 40034105）：按 appid+openid
//     **每小时最多一条**，讲的是「这一次」——哪次请求、平台原始 code/err_code/message、
//     以及同样的操作指引。
//
// 为什么两个都得留：bot_state 是启动后按群懒加载的（新加的群尤其只有发消息时才暴露），
// 启动期那条覆盖不到全部群；而发送期这条是**真的失败过的现场**，比「可能发不出去」有用。
// 叠加会不会刷屏：首条失败最多 2 条/群（启动期 1 + 发送期 1），之后每条失败消息最多再贡献
// 1 条/小时。所以这一条保持 Warn，不降 Debug（降级等于把发送失败的现场埋掉，只剩启动期那句
// 预告）；TestProactiveDenyWarnLayersDoNotFlood 守着这个上限。
var proactiveDenyWarns = newWarnThrottle(proactiveDenyWarnInterval)

// warnProactiveNoPermission 打一条能直接指导操作的 Warn，同一目标每小时最多一条。
func (c *Client) warnProactiveNoPermission(target Target, apiErr *APIError, now time.Time) {
	if c == nil || apiErr == nil {
		return
	}
	if !proactiveDenyWarns.allow(c.scopedKey(target), now) {
		return
	}
	logger.Warnf("%s: %s 发不出主动消息（code=%d err_code=%d, message=%q）：%s",
		ConfigKeyPrefix, target, apiErr.Code, apiErr.ErrCode, apiErr.Message, proactiveNoPermissionHint(target))
}

// ---------------------------------------------------------------------------
// markdown → 纯文本（降级兜底文案）
// ---------------------------------------------------------------------------

// plainTextFromMarkdown 把 markdown 剥成还能看的纯文本。
//
// 只做保守处理：去掉标题/引用/列表的行首标记、成对的强调标记，链接保留文字，
// 去掉 <qqbot-… /> 这类在纯文本里只会显示成源码的标签。不认识的语法原样留着——
// 兜底文案丑一点没关系，丢内容才是事故。
func plainTextFromMarkdown(markdown string) string {
	text := strings.ReplaceAll(markdown, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		cleaned := strings.TrimRight(stripInlineMarkdown(stripLineMarkdown(line)), " 	")
		if cleaned == "" {
			if blank || len(out) == 0 {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, cleaned)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// stripLineMarkdown 去掉行首的 markdown 标记（标题、引用、列表）。
func stripLineMarkdown(line string) string {
	s := strings.TrimLeft(line, " 	")
	for strings.HasPrefix(s, "#") {
		s = s[1:]
	}
	for strings.HasPrefix(s, ">") {
		s = s[1:]
	}
	s = strings.TrimLeft(s, " 	")
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(s, marker) {
			s = s[len(marker):]
			break
		}
	}
	return stripOrderedListMarker(s)
}

// stripOrderedListMarker 去掉 "1. " / "1) " 这种有序列表标记。
func stripOrderedListMarker(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
		return s[i+2:]
	}
	return s
}

// stripInlineMarkdown 去掉行内的 markdown 标记。
func stripInlineMarkdown(line string) string {
	s := replaceMarkdownLinks(line)
	s = stripQQBotTags(s)
	for _, token := range []string{"**", "__", "~~", "`"} {
		s = strings.ReplaceAll(s, token, "")
	}
	return strings.TrimSpace(collapseSpaces(s))
}

// replaceMarkdownLinks 把 [文字](链接) 拆成 "文字 (链接)"；图片则丢掉 "!" 前缀。
func replaceMarkdownLinks(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		open := strings.IndexByte(s[i:], '[')
		if open < 0 {
			b.WriteString(s[i:])
			break
		}
		open += i
		mid := strings.Index(s[open:], "](")
		if mid < 0 {
			b.WriteString(s[i:])
			break
		}
		mid += open
		end := strings.IndexByte(s[mid+2:], ')')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		end += mid + 2

		head := s[i:open]
		if open > i && s[open-1] == '!' {
			head = s[i : open-1]
		}
		label := s[open+1 : mid]
		b.WriteString(head)
		b.WriteString(label)
		link := strings.TrimSpace(s[mid+2 : end])
		if link != "" && link != strings.TrimSpace(label) {
			b.WriteString(" (" + link + ")")
		}
		i = end + 1
	}
	return b.String()
}

// removeAtEveryoneTag 去掉文本里所有 @全体 标记（<qqbot-at-everyone />）。
//
// 匹配刻意放宽：大小写和标签里的空格都不计较，只要求以 <qqbot-at-everyone 开头、
// 到最近的 > 结束。多剥一个同族标签最多是少一次 @，漏剥则是整条消息白丢。
func removeAtEveryoneTag(s string) (string, bool) {
	const prefix = "<qqbot-at-everyone"
	var b strings.Builder
	removed := false
	for {
		i := strings.Index(strings.ToLower(s), prefix)
		if i < 0 {
			b.WriteString(s)
			break
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		s = s[i+end+1:]
		removed = true
	}
	return b.String(), removed
}

// stripQQBotTags 去掉 <qqbot-… /> 这类标签（纯文本里它们只会显示成源码）。
func stripQQBotTags(s string) string {
	for {
		start := strings.Index(s, "<qqbot-")
		if start < 0 {
			break
		}
		rest := s[start:]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			break
		}
		s = s[:start] + rest[end+1:]
	}
	return s
}

// collapseSpaces 把连续空格压成一个。
func collapseSpaces(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}
