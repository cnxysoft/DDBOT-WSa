package qqofficial

import "time"

// 本文件收拢 QQ 官方机器人 OpenAPI v2 里固定不变的端点、配额与超时。
//
// 一条硬规矩：**网关（WebSocket）地址不在这里**。GET /gateway 每次返回的域名都可能
// 不一样——本机实测拿到的是 wss://api.sgroup.qq.com/websocket，和官方文档示例里的
// api.bot.qq.com 就不是一回事。所以网关地址只能运行时获取，任何地方都不许硬编。

const (
	// APIHost 是 OpenAPI v2 主域名：取 token、发现网关、业务接口都走它。
	APIHost = "api.bot.qq.com"
	// APIBase 是 APIHost 的 https 形式，拼请求 URL 用。
	APIBase = "https://" + APIHost
)

// 接口路径。带 %s 的是 openid 占位（用户 user_openid / 群 group_openid）。
const (
	PathGetAppAccessToken = "/app/getAppAccessToken" // POST {appId, clientSecret} -> {access_token, expires_in}
	PathGateway           = "/gateway"               // GET -> {url}，wss 地址，2 QPM
	PathUsersMe           = "/users/@me"             // GET 机器人自身信息

	PathUserMessages      = "/v2/users/%s/messages"
	PathUserStreamMessage = "/v2/users/%s/stream_messages" // 流式只有单聊有
	PathGroupMessages     = "/v2/groups/%s/messages"
	PathUserMessageByID   = "/v2/users/%s/messages/%s"
	PathGroupMessageByID  = "/v2/groups/%s/messages/%s"

	PathUserFiles             = "/v2/users/%s/files"
	PathGroupFiles            = "/v2/groups/%s/files"
	PathUserUploadPrepare     = "/v2/users/%s/upload_prepare"
	PathGroupUploadPrepare    = "/v2/groups/%s/upload_prepare"
	PathUserUploadPartFinish  = "/v2/users/%s/upload_part_finish"
	PathGroupUploadPartFinish = "/v2/groups/%s/upload_part_finish"

	PathGroupInfo                = "/v2/groups/%s/info"
	PathGroupBotState            = "/v2/groups/%s/bot_state"
	PathGroupMembers             = "/v2/groups/%s/members"
	PathGroupMember              = "/v2/groups/%s/members/%s"
	PathGroupMemberBlacklist     = "/v2/groups/%s/member_blacklist"
	PathGroupBatchRemoveMembers  = "/v2/groups/%s/batch_remove_members"
	PathGroupRestrictChatSetting = "/v2/groups/%s/restrict_chat_setting"
	PathGroupJoinRequestList     = "/v2/groups/%s/join_request_list"

	PathMenu            = "/v2/menu"
	PathPanels          = "/v2/panels"
	PathGenerateURLLink = "/v2/generate_url_link"
)

const (
	// AuthHeaderName 是鉴权头名。
	AuthHeaderName = "Authorization"
	// AuthScheme 是鉴权头的方案名：Authorization: QQBot <access_token>
	AuthScheme = "QQBot"
	// ContentTypeJSON 是发 JSON 用的内容类型。
	ContentTypeJSON = "application/json"
	// UserAgent 是 HTTP 请求与 WS 握手的标识。
	UserAgent = "DDBOT-WSa/qqofficial"
)

// Intents 是事件订阅位掩码，位号来自官方文档的事件订阅页。
const (
	IntentGuilds                = 1 << 0  // GUILDS（默认有权限）
	IntentGuildMembers          = 1 << 1  // GUILD_MEMBERS（默认有权限）
	IntentGuildMessages         = 1 << 9  // GUILD_MESSAGES
	IntentGuildMessageReactions = 1 << 10 // GUILD_MESSAGE_REACTIONS
	IntentDirectMessage         = 1 << 12 // DIRECT_MESSAGE
	IntentGroupAndC2C           = 1 << 25 // GROUP_AND_C2C_EVENT：群与单聊事件组（本 adapter 的主场）
	IntentInteraction           = 1 << 26 // INTERACTION_CREATE
	IntentMessageAudit          = 1 << 27 // MESSAGE_AUDIT
	IntentForum                 = 1 << 28 // 论坛事件
	IntentAudioAction           = 1 << 29 // 音频
	IntentPublicGuildMessages   = 1 << 30 // PUBLIC_GUILD_MESSAGES（默认有权限）

	// DefaultIntents 只订群与单聊事件组。
	//
	// 文档写「1<<25 属特殊事件需申请，传了无权限的 intents 会直接断开连接」，
	// 但实测（2026-11-11，扫码绑定的个人机器人）带 1<<25 直接 READY，既没有 4013
	// 也没有 4014——开箱可订，按实测来。
	DefaultIntents = IntentGroupAndC2C
)

// WebSocket 协议 op 码。
const (
	OpDispatch               = 0  // 事件分发
	OpHeartbeat              = 1  // 心跳（d 为最新 s）
	OpIdentify               = 2  // 鉴权
	OpPresenceUpdate         = 3  // 心跳状态更新
	OpResume                 = 6  // 断线恢复
	OpReconnect              = 7  // 服务端要求重连
	OpInvalidSession         = 9  // 会话无效（d=true 还能 resume）
	OpHello                  = 10 // 建连后的欢迎包，带 heartbeat_interval
	OpHeartbeatACK           = 11 // 心跳回执
	OpHTTPCallbackACK        = 12 // webhook 回执
	OpHTTPCallbackValidation = 13 // webhook 回调地址校验
)

// WebSocket 事件类型（信封的 t 字段）。
const (
	EventReady   = "READY"
	EventResumed = "RESUMED"

	EventC2CMessageCreate     = "C2C_MESSAGE_CREATE"
	EventGroupAtMessageCreate = "GROUP_AT_MESSAGE_CREATE"
	EventGroupMessageCreate   = "GROUP_MESSAGE_CREATE" // 需群主开「接收全量消息」

	EventGroupAddRobot     = "GROUP_ADD_ROBOT"
	EventGroupDelRobot     = "GROUP_DEL_ROBOT"
	EventFriendAdd         = "FRIEND_ADD"
	EventFriendDel         = "FRIEND_DEL"
	EventC2CMsgReject      = "C2C_MSG_REJECT"
	EventC2CMsgReceive     = "C2C_MSG_RECEIVE"
	EventGroupMsgReject    = "GROUP_MSG_REJECT"
	EventGroupMsgReceive   = "GROUP_MSG_RECEIVE"
	EventGroupJoinRequest  = "GROUP_JOIN_REQUEST"
	EventGroupMemberAdd    = "GROUP_MEMBER_ADD"
	EventGroupMemberRemove = "GROUP_MEMBER_REMOVE"

	EventInteractionCreate  = "INTERACTION_CREATE"
	EventMessageAuditPass   = "MESSAGE_AUDIT_PASS"
	EventMessageAuditReject = "MESSAGE_AUDIT_REJECT"
)

// WebSocket 关闭码。4006/4007 要重新 Identify，4009 还能 Resume，4914/4915 直接放弃。
const (
	WSCloseInvalidOpcode      = 4001
	WSCloseInvalidPayload     = 4002
	WSCloseNotIdentified      = 4003
	WSCloseAuthFailed         = 4004 // token 无效 -> 作废缓存 token 再 Identify
	WSCloseAlreadyIdentified  = 4005 // 重复 Identify
	WSCloseSessionInvalid     = 4006 // 无效 session -> 重新 Identify
	WSCloseIdentifySeqInvalid = 4007 // Identify 时 seq 非法 -> 重新 Identify
	WSCloseRateLimited        = 4008 // 发得太快
	WSCloseSessionTimeout     = 4009 // 会话超时，文档口径仍可 Resume
	WSCloseInvalidShard       = 4010
	WSCloseShardRequired      = 4011
	WSCloseInvalidVersion     = 4012
	WSCloseInvalidIntents     = 4013
	WSCloseInternalErrorFrom  = 4900
	WSCloseInternalErrorTo    = 4913
	WSCloseBotOffline         = 4914 // 机器人已下架（只允许沙箱）-> 不该重连
	WSCloseBotBanned          = 4915 // 机器人已封禁 -> 不该重连
)

// 配额、时效与体积上限。
const (
	// TokenRefreshAhead 是 access_token 的刷新提前量：官方口径「过期前 60 秒内获取会拿到
	// 新值，旧值还有 60 秒可用」，所以到期前 60 秒就换。
	TokenRefreshAhead = 60 * time.Second
	// DefaultTokenTTL 是文档给的 access_token 默认有效期。
	// 实测 expires_in 见过 7200，也见过 184（说明 token 被别处滚动使用）——一律以响应为准。
	DefaultTokenTTL = 7200 * time.Second

	// 被动消息窗口：过了就只能走主动消息。
	PassiveTTLGroup = 5 * time.Minute
	PassiveTTLUser  = 60 * time.Minute
	// 每条被动消息最多能回几次。
	MaxPassiveReplyGroup = 5
	MaxPassiveReplyUser  = 4

	// DefaultMsgSeq 是被动回复的起始序号。相同 msg_id + msg_seq 重复发送会被判重
	// （40054005），想对同一条消息多次回复必须递增 msg_seq。
	DefaultMsgSeq = 1

	// MediaTTLFallback 是 file_info 的兜底有效期。
	// 实测 ttl = 86400（24 小时）且同一 file_info 可跨消息复用；文档示例写 300 秒——
	// 一律以响应里的 ttl 为准，这个只兜底。
	MediaTTLFallback = 86400 * time.Second

	// DedupWindow 是事件去重窗口下限：官方明说相同 msg_id 可能重复推送，
	// 且重连退避最长 30 秒，5 分钟足够盖住。
	DedupWindow = 5 * time.Minute

	// MaxResponseBodyBytes 是读响应体的上限，防止异常响应吃内存。
	MaxResponseBodyBytes = 1 << 20

	// GatewayQPM 是 /gateway 的频控（2 QPM，burst 10）。启动取一次就够，别轮询。
	GatewayQPM = 2
)

// 超时与重连参数。
const (
	// HTTPTimeout 是单次业务 HTTP 请求的默认超时。
	HTTPTimeout = 10 * time.Second
	// GatewayFetchTimeout 是 GET /gateway 的超时。
	GatewayFetchTimeout = 10 * time.Second
	// HandshakeTimeout 是 WebSocket 握手超时。
	HandshakeTimeout = 10 * time.Second
	// HelloTimeout 是建连后等待 op=10 Hello 的超时。
	HelloTimeout = 10 * time.Second
	// WriteTimeout 是单次 WS 写超时。
	WriteTimeout = 10 * time.Second
	// DefaultHeartbeatInterval 只作兜底：真实间隔由 Hello 的 heartbeat_interval 决定。
	// 实测服务端下发 41250ms，文档示例 45000ms。
	DefaultHeartbeatInterval = 45 * time.Second
	// HeartbeatAckSlack 是判定「心跳没被 ACK」时额外容忍的余量。
	HeartbeatAckSlack = 5 * time.Second
	// DefaultStartTimeout 是 Start 等待首个 READY 的上限。
	DefaultStartTimeout = 20 * time.Second
	// StopTimeout 是 Stop 等待读循环退出的上限。
	StopTimeout = 5 * time.Second
	// MinReconnectBackoff / MaxReconnectBackoff 是断线重连退避区间。
	MinReconnectBackoff = 1 * time.Second
	MaxReconnectBackoff = 30 * time.Second

	// DefaultShardID / DefaultShardCount：未分片时的 shard 参数。
	DefaultShardID    = 0
	DefaultShardCount = 1
)
