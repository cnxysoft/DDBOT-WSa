package qqofficial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// 业务错误码常量。
//
// 一条实测结论：**失败响应里 code 与 err_code 的关系不固定**。
//   - 白名单接口：     {"message":"应用无接口访问权限","code":11253,"err_code":40012010}
//   - 主动消息无权限： {"message":"主动消息失败, 无权限","code":40034105,"err_code":40034105}
//   - 图片过小被拒：   {"message":"富媒体文件格式不支持","code":850019,"err_code":40034002}
//
// 官方文档只强调按 err_code 判定，但实测业务码实际落在 code 里；所以两个字段都得取，
// 不能假定二者相同、也不能只认其中一个。
const (
	ErrCodeOK = 0

	// 鉴权 / 应用级
	ErrCodeAppIDInvalid     = 100007 // appid invalid
	ErrCodeAppSecretInvalid = 100016 // invalid appid or secret
	ErrCodeBotNotExist      = 10004  // 机器人不存在
	ErrCodeRateLimited      = 100001 // 频率过高

	// 权限 / 资源
	ErrCodeAppPrivilegeNotPass  = 11253    // 应用无接口访问权限（能力未开放 / 未申请）
	ErrCodeResourceNotExist     = 11255    // 请求的资源不存在（用户/群已注销）
	ErrCodeNotGroupAdmin        = 11703    // 机器人不是群管理员（禁言、入群审批类）
	ErrCodePrivilegePairErrCode = 40012010 // 实测与 11253 成对出现的 err_code，同样按权限处理

	// 消息
	ErrCodeProactiveOverQuota     = 40034100 // 主动消息发送超过频控限制
	ErrCodeProactiveNoPermission  = 40034105 // 主动消息发送失败，无权限
	ErrCodeBotNotGroupMember      = 40034101 // 机器人非群成员
	ErrCodeBotNotGroupMemberAlt   = 40054003 // 机器人不是群成员
	ErrCodeGroupMuted             = 40054002 // 机器人被禁言
	ErrCodeMessageDuplicated      = 40054005 // 消息被去重（相同 msg_id + msg_seq）
	ErrCodeURLNotAllowed          = 40054010 // 不允许发送 URL
	ErrCodeRFCallbackLimit        = 40034128 // 被动回复时间或次数超限
	ErrCodeReplyMsgIDExpired      = 40034005 // 回复消息 msg_id 已过期：被动回复失效，要改走主动兜底
	ErrCodeBotOffline             = 40054016 // 机器人已下线
	ErrCodeNoFriendRelation       = 40054004 // 无好友关系
	ErrCodeUserRejectMsg          = 40054013 // 用户拒收消息
	ErrCodeNoMarkdownTemplate     = 40034127 // 无 Markdown 模板权限
	ErrCodeMarkdownNoPermission   = 304036   // 同上，另一处文档口径
	ErrCodeContentIllegal         = 40034006 // 消息内容违规
	ErrCodeRecallPeriodLimit      = 40034122 // 召回消息已达区间上限
	ErrCodeARKTemplateNoPrivilege = 304004   // 无权限使用该 ARK 模板
	ErrCodeSubscribeUnauthorized  = 304064   // 订阅消息未授权
	ErrCodeKeyboardOverLimit      = 40034029 // 内联键盘行/列超限

	// 素材 / 上传
	ErrCodeMediaDailyQuota   = 40093002 // 超过今天发送文件容量上限
	ErrCodeFileTooLarge      = 850031   // 上传文件超过大小限制
	ErrCodeImageFormat       = 40035001 // 图片格式不支持
	ErrCodeImageTooLarge     = 40035002 // 超过大小上限
	ErrCodeMediaTaskNotFound = 40035003 // 上传任务不存在 / 已过期 / 不属于当前机器人
	ErrCodeMediaServerError  = 40035006 // 服务端内部错误
	ErrCodeImageCountLimit   = 40035007 // 图片数量超上限
	ErrCodeImageNotFound     = 40035008 // 图片不存在或已删除
	ErrCodeImageExpired      = 40035009 // 图片已过期或已删除
	ErrCodeImageRefInvalid   = 40035010 // 引用链接无效 / 不属于当前机器人

	// 实测（2026-11-11，personal 机器人）：上传 1x1、70 字节的 PNG 被拒
	//   {"message":"富媒体文件格式不支持","code":850019,"err_code":40034002}
	// 而同格式的 64x64 PNG 返回 200 —— **平台会挑图**：尺寸 / 体积过小也会被报成
	// 「格式不支持」，不全是真格式问题。上层别把它当纯格式错误处理，也别因此以为
	// png/jpg 不可用。
	ErrCodeMediaFormatUnsupported    = 850019   // 富媒体文件格式不支持（实测：过小的图也落到这里）
	ErrCodeMediaFormatUnsupportedAlt = 40034002 // 同一错误的 err_code 侧
)

// ErrorKind 是错误分类**位掩码**，不是单值枚举。
//
// 用位掩码是因为实测里同一枚码能同时属于两类：40034100「主动消息超过频控」既是
// 配额又是可重试（等一等真的会好）；40034105 曾经也被一起标成配额，但实测
// （2026-11-12）它是群主没开「允许机器人主动发送消息」——纯权限、重试无解，
// 标成配额只会让它被无谓退避重发，所以现在只保留 KindPermission。
// 单值枚举只能二选一，那种真正「两类都占」的码才会丢信息。
type ErrorKind uint8

const (
	KindQuota          ErrorKind = 1 << iota // 配额/频控：退避后可能成功，立刻重试没意义
	KindPermission                           // 权限：能力未开放或没授权，原样重试必然失败
	KindPassiveExpired                       // 被动回复过期/次数用尽：要改走主动消息兜底
	KindInvalidParam                         // 参数或内容不合法：原样重试必然失败
	KindRetryable                            // 可重试：5xx、网络抖动、限流退避后重试有意义
	KindFatal                                // 不可重试：要人工处理（凭据错、机器人下架/封禁）
)

// KindUnknown 是零值：没识别出来的码。
const KindUnknown ErrorKind = 0

// Has 判断是否含某一类。
func (k ErrorKind) Has(target ErrorKind) bool { return k&target != 0 }

// IsQuota 是否配额类。
func (k ErrorKind) IsQuota() bool { return k.Has(KindQuota) }

// IsPermission 是否权限类。
func (k ErrorKind) IsPermission() bool { return k.Has(KindPermission) }

// IsPassiveExpired 是否被动回复过期类。
func (k ErrorKind) IsPassiveExpired() bool { return k.Has(KindPassiveExpired) }

// IsInvalidParam 是否参数类。
func (k ErrorKind) IsInvalidParam() bool { return k.Has(KindInvalidParam) }

// IsRetryable 是否可重试。
func (k ErrorKind) IsRetryable() bool { return k.Has(KindRetryable) }

// IsFatal 是否需要人工处理。
func (k ErrorKind) IsFatal() bool { return k.Has(KindFatal) }

func (k ErrorKind) String() string {
	if k == KindUnknown {
		return "unknown"
	}
	var parts []string
	for _, item := range []struct {
		kind ErrorKind
		name string
	}{
		{KindQuota, "quota"},
		{KindPermission, "permission"},
		{KindPassiveExpired, "passive-expired"},
		{KindInvalidParam, "invalid-param"},
		{KindRetryable, "retryable"},
		{KindFatal, "fatal"},
	} {
		if k.Has(item.kind) {
			parts = append(parts, item.name)
		}
	}
	return strings.Join(parts, "|")
}

// codeKinds 是显式码表，优先于按码段粗分。
var codeKinds = map[int]ErrorKind{
	// 配额 / 频控
	ErrCodeProactiveOverQuota: KindQuota | KindRetryable,
	ErrCodeRateLimited:        KindQuota | KindRetryable,
	ErrCodeMediaDailyQuota:    KindQuota,
	ErrCodeRecallPeriodLimit:  KindQuota,

	// 权限
	ErrCodeAppPrivilegeNotPass:    KindPermission,
	ErrCodePrivilegePairErrCode:   KindPermission,
	ErrCodeNotGroupAdmin:          KindPermission,
	ErrCodeProactiveNoPermission:  KindPermission, // 纯权限：群主没开主动发言，重试无解（见 message.go 的 isProactiveNoPermissionCode）
	ErrCodeNoMarkdownTemplate:     KindPermission,
	ErrCodeMarkdownNoPermission:   KindPermission,
	ErrCodeARKTemplateNoPrivilege: KindPermission,
	ErrCodeSubscribeUnauthorized:  KindPermission,
	ErrCodeBotNotGroupMember:      KindPermission,
	ErrCodeBotNotGroupMemberAlt:   KindPermission,
	ErrCodeGroupMuted:             KindPermission,
	ErrCodeBotOffline:             KindPermission,
	ErrCodeNoFriendRelation:       KindPermission,
	ErrCodeUserRejectMsg:          KindPermission,

	// 被动回复过期
	ErrCodeRFCallbackLimit:   KindPassiveExpired,
	ErrCodeReplyMsgIDExpired: KindPassiveExpired,

	// 参数 / 内容
	ErrCodeResourceNotExist:  KindInvalidParam,
	ErrCodeURLNotAllowed:     KindInvalidParam,
	ErrCodeContentIllegal:    KindInvalidParam,
	ErrCodeMessageDuplicated: KindInvalidParam, // 原样重试必失败：要递增 msg_seq
	ErrCodeKeyboardOverLimit: KindInvalidParam,
	ErrCodeImageFormat:       KindInvalidParam,
	ErrCodeImageTooLarge:     KindInvalidParam,
	ErrCodeFileTooLarge:      KindInvalidParam,
	ErrCodeImageCountLimit:   KindInvalidParam,
	ErrCodeImageRefInvalid:   KindInvalidParam,
	// 实测的过小图片拒收（850019 / 40034002）：内容不被接受，原样重试必失败
	ErrCodeMediaFormatUnsupported:    KindInvalidParam,
	ErrCodeMediaFormatUnsupportedAlt: KindInvalidParam,
	// file_info / upload_id 已失效：要重新上传，原样重试必失败
	ErrCodeMediaTaskNotFound: KindInvalidParam,
	ErrCodeImageNotFound:     KindInvalidParam,
	ErrCodeImageExpired:      KindInvalidParam,

	// 服务端自己出错：值得重试
	ErrCodeMediaServerError: KindRetryable,

	// 凭据级：人工处理
	ErrCodeAppIDInvalid:     KindFatal | KindPermission,
	ErrCodeAppSecretInvalid: KindFatal | KindPermission,
	ErrCodeBotNotExist:      KindFatal,
}

// Classify 按业务码分类。code 与 err_code 两个字段都查，结果取并集（实测二者关系不固定）。
func Classify(code, errCode int) ErrorKind {
	var k ErrorKind
	for _, c := range []int{code, errCode} {
		if c == 0 {
			continue
		}
		if kind, ok := codeKinds[c]; ok {
			k |= kind
			continue
		}
		k |= classifyByRange(c)
	}
	return k
}

// classifyByRange 兜住码表里没有的码，只按有把握的码段粗分。
func classifyByRange(code int) ErrorKind {
	// 400110xx/400119xx 是「请求参数错误」族（实测 40011028 就落在这段）。
	if code >= 40011000 && code <= 40011999 {
		return KindInvalidParam
	}
	return KindUnknown
}

// ClassifyHTTP 先按业务码分类，再用 HTTP 状态兜底。
//
// 注意：失败时 HTTP 也可能是 200（业务码在 body 里），所以**不能只按状态码判**，
// 这里的状态码只用来补信息。
func ClassifyHTTP(status, code, errCode int) ErrorKind {
	k := Classify(code, errCode)
	switch {
	case status >= 500 || status == http.StatusRequestTimeout:
		k |= KindRetryable
	case status == http.StatusTooManyRequests:
		k |= KindQuota | KindRetryable
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		k |= KindPermission
	}
	return k
}

// APIError 是 QQ 开放平台返回的业务错误。
type APIError struct {
	HTTPStatus int
	Message    string
	Code       int
	ErrCode    int
	TraceID    string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("qq 开放平台错误：HTTP %d, code=%d, err_code=%d, message=%q",
		e.HTTPStatus, e.Code, e.ErrCode, e.Message)
	if e.TraceID != "" {
		s += ", trace_id=" + e.TraceID
	}
	return s
}

// Kind 返回这次错误的分类。
func (e *APIError) Kind() ErrorKind {
	if e == nil {
		return KindUnknown
	}
	return ClassifyHTTP(e.HTTPStatus, e.Code, e.ErrCode)
}

// Retryable 判断原样重试是否有意义。
func (e *APIError) Retryable() bool { return e.Kind().IsRetryable() }

// IsQuota 是否撞了配额/频控。
func (e *APIError) IsQuota() bool { return e.Kind().IsQuota() }

// IsPermission 是否权限不足（含能力未开放）。
func (e *APIError) IsPermission() bool { return e.Kind().IsPermission() }

// IsPassiveExpired 是否被动回复窗口过期/次数用尽。
func (e *APIError) IsPassiveExpired() bool { return e.Kind().IsPassiveExpired() }

// IsInvalidParam 是否参数/内容问题。
func (e *APIError) IsInvalidParam() bool { return e.Kind().IsInvalidParam() }

// ParseAPIError 从响应体解析业务错误，返回 nil 表示这次调用是成功的。
//
// 判定规则（实测）：
//   - code 或 err_code 任一非 0 -> 失败，哪怕 HTTP 是 200；
//   - 两者都为 0 但 HTTP >= 400 -> 也算失败（比如 4xx 返回空对象或非 JSON 体）；
//   - 其余 -> 成功。
func ParseAPIError(status int, body []byte) *APIError {
	var raw struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
		ErrCode int    `json:"err_code"`
		TraceID string `json:"trace_id"`
	}
	// 非 JSON 或空体不是错误线索，留着让状态码兜底
	_ = json.Unmarshal(body, &raw)

	if raw.Code == 0 && raw.ErrCode == 0 && status < 400 {
		return nil
	}
	return &APIError{
		HTTPStatus: status,
		Message:    raw.Message,
		Code:       raw.Code,
		ErrCode:    raw.ErrCode,
		TraceID:    raw.TraceID,
	}
}

// AsAPIError 从错误链上取 *APIError。
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// IsRetryable 判断任意错误是否值得原样重试。
// 网络层错误算可重试；context 被取消/超时不算（那是调用方主动结束的）。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := AsAPIError(err); ok {
		return apiErr.Retryable()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}
