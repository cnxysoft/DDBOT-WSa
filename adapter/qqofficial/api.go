package qqofficial

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 本文件是 qqofficial 的 HTTP 客户端：拼 base URL 与鉴权头、处理 token 失效重试、
// 把响应体判成 *APIError。
//
// 三条约定：
//  1. 业务码一律按 code / err_code 判定，不看 message 文案（官方明说文案会变）；
//  2. **失败时 HTTP 也可能是 200**，业务码在 body 里 —— 不许只按状态码判成败；
//  3. Client 挂着发送层的本地状态（msg_seq 计数器、被动窗口账本），要长期复用，
//     别每发一条消息就 NewClient 一个（那样计数器会从 1 重新开始 → 撞 40054005）。

// token 路径上客户端自己需要认出来的几枚码。
//
// errors.go 那份是业务码表，这里补的是 HTTP 客户端在「换 token 重发」
// 这条路上要用的判断依据——客户端不做业务分类，只认「token 是不是不认了」。
const (
	// ErrCodeTokenMissing：参数里缺少 token。这是本地拼装的锅，重试没意义。
	ErrCodeTokenMissing = 11241
	// ErrCodeTokenCheckFailed：校验 token 失败。官方口径是系统错误、重试一次会好。
	ErrCodeTokenCheckFailed = 11242
	// ErrCodeTokenNotPass：校验 token 未通过（token 本身不对）→ 作废缓存重取。
	ErrCodeTokenNotPass = 11243
	// ErrCodeAccessTokenInvalid：access_token 失效。
	// [推断] 文档没列这一枚（同族只到 11243），留着兜底；判断不依赖它单独成立——
	// HTTP 401 一样会触发「作废缓存 → 重取 → 重发一次」。
	ErrCodeAccessTokenInvalid = 11244
)

// Client 是 QQ 开放平台 OpenAPI v2 的 HTTP 客户端。
type Client struct {
	tm         *TokenManager
	httpClient *http.Client
	baseURL    string

	// 以下三项在 message.go 里用：被动序号计数器、发送重试预算。
	seqOnce sync.Once
	seqs    *seqTracker
	policy  sendPolicy
}

// NewClient 建一个客户端。token 由 tm 提供，tm 为空时所有请求都取不到凭据。
//
// base URL 与超时取 qqofficial.* 配置（见 config.go），缺省是 https://api.bot.qq.com。
func NewClient(tm *TokenManager) *Client {
	return newClientFromConfig(tm, GetConfig())
}

func newClientFromConfig(tm *TokenManager, cfg *Config) *Client {
	c := &Client{tm: tm}
	if tm != nil {
		c.baseURL = tm.baseURL
	}
	if c.baseURL == "" {
		c.baseURL = cfg.APIBaseURL
	}
	if c.baseURL == "" {
		c.baseURL = APIBase
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = HTTPTimeout
	}
	c.httpClient = &http.Client{Timeout: timeout}
	return c
}

// TokenManager 返回本客户端用的凭据管理器（可能为 nil）。
func (c *Client) TokenManager() *TokenManager {
	if c == nil {
		return nil
	}
	return c.tm
}

// BaseURL 返回拼请求用的 base URL。
func (c *Client) BaseURL() string {
	if c == nil || c.baseURL == "" {
		return APIBase
	}
	return c.baseURL
}

// PostJSON 发一个 JSON POST。body 为 nil 时不带请求体；out 为 nil 时不解析响应体。
//
// path 形如 "/v2/groups/{group_openid}/messages"：拼 base URL 与 Authorization 头
// 由本方法负责，调用方只管路径与 body。
func (c *Client) PostJSON(ctx context.Context, path string, body any, out any) error {
	payload, err := marshalJSONBody(body)
	if err != nil {
		return fmt.Errorf("%s: POST %s 的请求体序列化失败：%w", ConfigKeyPrefix, path, err)
	}
	contentType := ""
	if payload != nil {
		contentType = ContentTypeJSON
	}
	return c.DoRequest(ctx, http.MethodPost, path, contentType, payload, out)
}

// GetJSON 发一个 GET。out 非空时把响应体解进去。
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	return c.DoRequest(ctx, http.MethodGet, path, "", nil, out)
}

// FetchBotUsername 取机器人自己的名字：GET /users/@me 的 username。
//
// 一手实测（2026-11-12）：
//
//	GET /users/@me
//	-> {"id":"14103455547383994665","username":"DDBOT","avatar":"…","share_url":"…","welcome_msg":""}
//
// username 就是机器人名字。目前只有 adapter 启动时拉一次、填进模板 bot_name 这一处用。
func (c *Client) FetchBotUsername(ctx context.Context) (string, error) {
	var resp struct {
		Username string `json:"username"`
	}
	if err := c.GetJSON(ctx, PathUsersMe, &resp); err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Username), nil
}

// DeleteJSON 发一个 DELETE。
//
// 实测撤回接口成功时返回空对象 {}（与文档「无响应体」一致），所以 out 可以给 nil。
func (c *Client) DeleteJSON(ctx context.Context, path string, out any) error {
	return c.DoRequest(ctx, http.MethodDelete, path, "", nil, out)
}

// DoRequest 是三个快捷方法下面的通用实现，给「body 已经是字节」的调用方用
// （比如 media.go 的 multipart 上传表单）。
//
// payload 为 nil 表示不带请求体；contentType 为空时按 JSON 处理（payload 非空时）。
// 它会取 token、拼 Authorization 头；撞到 token 失效（HTTP 401 或 11242/11243/11244）
// 就作废缓存 token 重取后**重发一次**（只一次，避免死循环）。
func (c *Client) DoRequest(ctx context.Context, method, path, contentType string, payload []byte, out any) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s: %s 请求的 path 为空", ConfigKeyPrefix, method)
	}

	// 最多两次：第一次用缓存 token，被拒就换一枚再试一次。
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}
		req, err := c.newRequest(ctx, method, path, contentType, payload, token)
		if err != nil {
			return err
		}

		resp, err := c.httpDo(req)
		if err != nil {
			return fmt.Errorf("%s: %s %s 请求失败：%w", ConfigKeyPrefix, method, path, err)
		}

		// 有上限地读，免得异常响应吃内存；官方响应都很小。
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("%s: %s %s 读取响应失败：%w", ConfigKeyPrefix, method, path, readErr)
		}
		if closeErr != nil {
			logger.Debugf("%s: %s %s 关闭响应体失败：%v", ConfigKeyPrefix, method, path, closeErr)
		}

		apiErr := ParseAPIError(resp.StatusCode, raw)
		if apiErr == nil {
			return decodeJSONInto(out, raw, method, path)
		}
		if attempt == 0 && isTokenInvalid(resp.StatusCode, apiErr) {
			logger.Warnf("%s: %s %s 被拒（HTTP %d, code=%d, err_code=%d），作废缓存 token 换一枚重发一次",
				ConfigKeyPrefix, method, path, resp.StatusCode, apiErr.Code, apiErr.ErrCode)
			if c.tm != nil {
				c.tm.Invalidate()
			}
			continue
		}
		return apiErr
	}
	// 循环里要么 return 要么 continue，走到这里说明两次都被 token 判定拒了。
	return fmt.Errorf("%s: %s %s 连续两次被判定为 token 失效", ConfigKeyPrefix, method, path)
}

// token 取一枚可用的 access_token。
func (c *Client) token(ctx context.Context) (string, error) {
	if c == nil || c.tm == nil {
		return "", fmt.Errorf("%s: 没有 TokenManager，取不到 access_token", ConfigKeyPrefix)
	}
	return c.tm.Token(ctx)
}

func (c *Client) httpDo(req *http.Request) (*http.Response, error) {
	if c == nil || c.httpClient == nil {
		// 刻意不使用 http.DefaultClient 兜底：它是进程级单例，任何包替换它的
		// Transport / Timeout 都会静默作用到官方通道上，而且超时不可控。
		// 正常路径不可达（NewClient 一定会设置 httpClient），这里显式报错，
		// 免得将来有人手搓 &Client{} 时又悄悄走回全局 client。
		return nil, fmt.Errorf("%s: HTTP client 未初始化（请用 NewClient 构造 Client）", ConfigKeyPrefix)
	}
	return c.httpClient.Do(req)
}

// newRequest 拼一个请求：URL、鉴权头、内容类型、UA。
func (c *Client) newRequest(ctx context.Context, method, path, contentType string, payload []byte, token string) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), body)
	if err != nil {
		return nil, fmt.Errorf("%s: 构造 %s %s 请求失败：%w", ConfigKeyPrefix, method, path, err)
	}
	switch {
	case contentType != "":
		req.Header.Set("Content-Type", contentType)
	case payload != nil:
		req.Header.Set("Content-Type", ContentTypeJSON)
	}
	req.Header.Set("Accept", ContentTypeJSON)
	req.Header.Set("User-Agent", UserAgent)
	if token != "" {
		req.Header.Set(AuthHeaderName, AuthHeader(token))
	}
	return req, nil
}

// url 把 path 拼到 base URL 后面。path 带不带前导斜杠都认。
func (c *Client) url(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimSuffix(c.BaseURL(), "/") + path
}

// marshalJSONBody 序列化请求体；body 为 nil 时返回 nil（表示不带请求体）。
func marshalJSONBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	return json.Marshal(body)
}

// decodeJSONInto 把响应体解进 out。out 为 nil 或响应体为空都算成功。
//
// 解析按宽松口径：未声明的字段忽略（官方近半年一直在加字段）。
func decodeJSONInto(out any, raw []byte, method, path string) error {
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %s %s 的响应解析失败（%d 字节）：%w",
			ConfigKeyPrefix, method, path, len(raw), err)
	}
	return nil
}

// isTokenInvalid 判断这次失败是不是「token 不认了」。
func isTokenInvalid(status int, apiErr *APIError) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if apiErr == nil {
		return false
	}
	for _, code := range []int{apiErr.Code, apiErr.ErrCode} {
		switch code {
		case ErrCodeTokenCheckFailed, ErrCodeTokenNotPass, ErrCodeAccessTokenInvalid:
			return true
		}
	}
	return false
}

// sendPolicy 是发送层的重试预算。零值走默认值（见 withDefaults），测试可以调小。
type sendPolicy struct {
	// MaxAttempts 是一次 Send 的总尝试上限（含降级重发与退避重试）。
	MaxAttempts int
	// TransportAttempts 是传输层错误（网络抖动、5xx）的原样重发次数上限。
	TransportAttempts int
	// TransportBackoff 是传输层错误重发前的固定等待。
	TransportBackoff time.Duration
	// QuotaAttempts 是撞配额闸门时的退避重试次数上限。
	QuotaAttempts int
	// QuotaBackoffBase / QuotaBackoffMax 是配额退避的基数与单次等待上限。
	QuotaBackoffBase time.Duration
	QuotaBackoffMax  time.Duration
}

const (
	defaultSendMaxAttempts       = 6
	defaultTransportAttempts     = 2
	defaultTransportBackoff      = 300 * time.Millisecond
	defaultQuotaAttempts         = 3
	defaultQuotaBackoffBase      = 500 * time.Millisecond
	defaultQuotaBackoffMaxWindow = 8 * time.Second
)

// withDefaults 把零值补成默认值。
func (p sendPolicy) withDefaults() sendPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaultSendMaxAttempts
	}
	if p.TransportAttempts <= 0 {
		p.TransportAttempts = defaultTransportAttempts
	}
	if p.TransportBackoff <= 0 {
		p.TransportBackoff = defaultTransportBackoff
	}
	if p.QuotaAttempts <= 0 {
		p.QuotaAttempts = defaultQuotaAttempts
	}
	if p.QuotaBackoffBase <= 0 {
		p.QuotaBackoffBase = defaultQuotaBackoffBase
	}
	if p.QuotaBackoffMax <= 0 {
		p.QuotaBackoffMax = defaultQuotaBackoffMaxWindow
	}
	return p
}

// backoff 返回第 n 次退避要等多久（n 从 1 开始，指数增长、封顶）。
func (p sendPolicy) backoff(n int) time.Duration {
	if n <= 1 {
		return p.QuotaBackoffBase
	}
	wait := p.QuotaBackoffBase
	for i := 1; i < n; i++ {
		wait *= 2
		if wait >= p.QuotaBackoffMax {
			return p.QuotaBackoffMax
		}
	}
	if wait > p.QuotaBackoffMax {
		wait = p.QuotaBackoffMax
	}
	return wait
}

// sendPolicyFor 取本客户端生效的发送预算。
func (c *Client) sendPolicyFor() sendPolicy {
	if c == nil {
		return sendPolicy{}.withDefaults()
	}
	return c.policy.withDefaults()
}

// seqTrackerFor 取本客户端的 msg_seq 计数器（懒建，零值 Client 也能用）。
func (c *Client) seqTrackerFor() *seqTracker {
	if c == nil {
		return newSeqTracker()
	}
	c.seqOnce.Do(func() {
		if c.seqs == nil {
			c.seqs = newSeqTracker()
		}
	})
	return c.seqs
}

// sendClip 把长字符串截短，只用在日志里（msg_id / ref_idx 很长）。
func sendClip(s string) string {
	const limit = 48
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// sendSleepCtx 是可被 ctx 打断的等待（退避用）。
func sendSleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
