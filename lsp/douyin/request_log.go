package douyin

import (
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// douyinAPIRequestTrace 记录一次抖音 API 请求的生命周期，
// 用于在日志中输出请求类型、排队等待与错误分类等结构化字段。
type douyinAPIRequestTrace struct {
	id           uint64
	requestType  string
	method       string
	host         string
	path         string
	target       string
	queueWait    time.Duration
	throughGate  bool
	requestStart time.Time
}

var douyinRequestTraceID uint64

// 请求类型标记（request_type 字段取值），集中定义避免散落字符串。
const (
	douyinRequestTypeQRCreate          = "qr_create"
	douyinRequestTypeQRPoll            = "qr_poll"
	douyinRequestTypeQRHome            = "qr_home"
	douyinRequestTypeQRRedirect        = "qr_redirect"
	douyinRequestTypeQRChallenge       = "qr_challenge"
	douyinRequestTypeQRTicketGuard     = "qr_ticket_guard"
	douyinRequestTypeMsToken           = "ms_token"
	douyinRequestTypeShortLink         = "short_link"
	douyinRequestTypeRoomPage          = "room_page"
	douyinRequestTypeWebPage           = "web_page"
	douyinRequestTypeSignedUserProfile = "signed_user_profile"
	douyinRequestTypeSignedUserPosts   = "signed_user_posts"
	douyinRequestTypeSignedRoomAPI     = "signed_room_api"
	douyinRequestTypeMinimalRoomAPI    = "minimal_room_api"
	douyinRequestTypeLiveRoomSigned    = "live_room_signed"
	douyinRequestTypeLiveRoomMinimal   = "live_room_minimal"
	douyinRequestTypeLivePageSSR       = "live_page_ssr"
	douyinRequestTypeDetailSource      = "detail_source"
	douyinRequestTypeWebSignRuntime    = "web_sign_runtime"
	douyinRequestTypeNewsSignature     = "news_signature"
	douyinRequestTypeAllDouyinAPI      = "all_douyin_api"
)

// 错误分类（error_kind 字段取值）。
const (
	douyinErrorKindHTTPError      = "http_error"
	douyinErrorKindTransportError = "transport_error"
	douyinErrorKindTLSError       = "tls_error"
	douyinErrorKindDNSError       = "dns_error"
	douyinErrorKindQueueWait      = "queue_wait"
)

// beginDouyinAPIRequest 在发起请求前登记一条请求追踪记录。
func beginDouyinAPIRequest(requestType string, method string, rawURL string) *douyinAPIRequestTrace {
	trace := &douyinAPIRequestTrace{
		id:           atomic.AddUint64(&douyinRequestTraceID, 1),
		requestType:  requestType,
		method:       strings.ToUpper(method),
		target:       rawURL,
		requestStart: time.Now(),
	}
	if u, err := url.Parse(rawURL); err == nil {
		trace.host = u.Host
		trace.path = u.Path
	}
	return trace
}

// fields 输出结构化日志字段；endpoint 经过脱敏，不包含查询参数。
func (t *douyinAPIRequestTrace) fields() logrus.Fields {
	if t == nil {
		return logrus.Fields{}
	}
	fields := logrus.Fields{
		"request_type": t.requestType,
		"method":       t.method,
		"host":         t.host,
		"endpoint":     safeDouyinAPIEndpoint(t.target),
	}
	if t.path != "" {
		fields["path"] = t.path
	}
	if t.throughGate {
		fields["through_gate"] = true
		fields["queue_wait"] = t.queueWait.String()
	}
	return fields
}

// finish 在请求结束后输出结果日志，err 为 nil 时仅输出耗时。
func (t *douyinAPIRequestTrace) finish(err error) {
	if t == nil {
		return
	}
	log := logger.WithFields(t.fields()).
		WithField("cost", time.Since(t.requestStart).String())
	if err == nil {
		log.Tracef("抖音API请求完成")
		return
	}
	log.WithField("error_kind", safeDouyinRequestErrorKind(err)).
		Errorf("抖音API请求失败")
}

// safeDouyinRequestErrorKind 将请求错误归类为有限的 error_kind 取值。
func safeDouyinRequestErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return douyinErrorKindDNSError
	}
	var tlsRecordErr tls.RecordHeaderError
	if errors.As(err, &tlsRecordErr) {
		return douyinErrorKindTLSError
	}
	if isDouyinTLSFailure(err) {
		return douyinErrorKindTLSError
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if _, ok := urlErr.Err.(*tls.CertificateVerificationError); ok {
			return douyinErrorKindTLSError
		}
		return douyinErrorKindTransportError
	}
	if strings.Contains(err.Error(), "HTTP ") {
		return douyinErrorKindHTTPError
	}
	return douyinErrorKindTransportError
}

// isDouyinTLSFailure 识别握手中断等 TLS 相关错误（分类辅助）。
func isDouyinTLSFailure(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "tls:") ||
		strings.Contains(msg, "x509:") ||
		(strings.Contains(msg, "EOF") && strings.Contains(msg, "handshake"))
}

// safeDouyinAPIEndpoint 返回脱敏后的端点（scheme://host/path），
// 查询串中可能含有签名与票据，绝不写入日志。
func safeDouyinAPIEndpoint(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if u.Path == "" {
		return u.Scheme + "://" + u.Host
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// douyinLoginRequestType 根据请求 URL 判定登录链路的 request_type 标记。
func douyinLoginRequestType(rawURL string) string {
	if strings.Contains(rawURL, "/passport/web/get_qrcode/") {
		return douyinRequestTypeQRCreate
	}
	if strings.Contains(rawURL, "/passport/web/check_qrconnect/") {
		return douyinRequestTypeQRPoll
	}
	if strings.Contains(rawURL, "/passport/web/challenge") {
		return douyinRequestTypeQRChallenge
	}
	if strings.Contains(rawURL, "/passport/ticket_guard/get_client_cert/") {
		return douyinRequestTypeQRTicketGuard
	}
	if strings.Contains(rawURL, "mssdk.bytedance.com") {
		return douyinRequestTypeMsToken
	}
	if strings.Contains(rawURL, "login.douyin.com") {
		return douyinRequestTypeQRRedirect
	}
	if isDouyinHostRoot(rawURL) {
		return douyinRequestTypeQRHome
	}
	return ""
}

// isDouyinHostRoot 判断 URL 是否为抖音站内首页地址。
func isDouyinHostRoot(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return isDouyinDomain(u.Host) && (u.Path == "" || u.Path == "/")
}
