package qqofficial

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TokenManager 管 access_token 的获取与缓存刷新。
//
// 凭据规则（官方口径 + 实测）：
//   - POST /app/getAppAccessToken，body {appId, clientSecret} -> {access_token, expires_in}；
//   - 有效期内重复获取返回同一个值，过期前 60 秒内获取会拿到新值，旧值还有 60 秒可用
//     -> 所以在到期前 60 秒刷新（实测 expires_in 见过 7200、4442、184，且**类型在数字与
//     字符串之间摇摆**，见 tokenTTL）；
//   - expires_in 比刷新提前量还短时，提前量退成有效期的一半，免得每次调用都去刷。
//
// 并发：刷新的 HTTP 调用在锁内完成，多个调用方只会有一个真的去取，其余等它取完
// 直接用缓存（避免打 token 接口的惊群）。
type TokenManager struct {
	appID     string
	appSecret string

	httpClient *http.Client
	baseURL    string

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	ahead     time.Duration // 当前这枚 token 实际使用的刷新提前量
}

// NewTokenManager 建一个凭据管理器。appID / appSecret 为空时 Token 会直接报错。
func NewTokenManager(appID, appSecret string) *TokenManager {
	cfg := GetConfig()
	cfg.AppID, cfg.AppSecret = appID, appSecret
	return newTokenManagerFromConfig(cfg)
}

func newTokenManagerFromConfig(cfg *Config) *TokenManager {
	return &TokenManager{
		appID:      cfg.AppID,
		appSecret:  cfg.AppSecret,
		httpClient: &http.Client{Timeout: cfg.HTTPTimeout},
		baseURL:    cfg.APIBaseURL,
		ahead:      cfg.TokenRefreshAhead,
	}
}

// accessTokenResp 是 token 接口的响应。
type accessTokenResp struct {
	AccessToken string   `json:"access_token"`
	ExpiresIn   tokenTTL `json:"expires_in"`
}

// tokenTTL 是 expires_in 的兼容解析类型：**平台返回的类型会在数字和字符串之间摇摆**。
//
// 实测（2026-11-11）：
//   - 数字形态 7200：实机连接的日志算出「有效期 2h0m0s」，只有拿到数字才成立；
//   - 字符串形态 "4442" / "4779"：当天 17:10 三次复测都是带引号的字符串。
//
// 7200 是一枚全新 token 的标准有效期；4442 / 4779 这类偏小的值是「token 还在有效期内
// 被重复获取」时返回的剩余时间。摇摆的触发条件没证实（推测与新建 / 续期有关），
// 所以**两种形态都必须吃下去**。
//
// 用 int 声明时，遇到字符串形态 json.Unmarshal 会直接报类型错 -> token 取不到 ->
// 整条通道不可用，这就是要修的缺陷。
//
// 这里是**一次**反序列化内按形态分支，不是「先按 int 解一遍、失败再按 string 解一遍」
// 那种双次解析。
type tokenTTL struct {
	Seconds int  // 解析出的秒数
	Quoted  bool // 这次平台是用字符串给的（留着观察类型摇摆）
}

// UnmarshalJSON 同时接受 JSON 数字与数字字符串；空值 / null 按 0 处理（外层会退到默认有效期）。
func (t *tokenTTL) UnmarshalJSON(raw []byte) error {
	*t = tokenTTL{}

	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil
	}

	if strings.HasPrefix(s, `"`) {
		var quoted string
		if err := json.Unmarshal(raw, &quoted); err != nil {
			return err
		}
		quoted = strings.TrimSpace(quoted)
		if quoted == "" {
			return nil
		}
		n, err := parseSeconds(quoted)
		if err != nil {
			return fmt.Errorf("expires_in 不是整数：%q", quoted)
		}
		t.Seconds, t.Quoted = n, true
		return nil
	}

	n, err := parseSeconds(s)
	if err != nil {
		return fmt.Errorf("expires_in 不是数字：%q", s)
	}
	t.Seconds = n
	return nil
}

// parseSeconds 把秒数文本转成整数：整数形态优先，顺带容忍 "7200.0" 这种浮点写法。
func parseSeconds(s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int(f), nil
}

// Token 返回可用的 access_token，必要时先刷新。
func (t *TokenManager) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.validLocked(time.Now()) {
		return t.token, nil
	}
	return t.refreshLocked(ctx)
}

func (t *TokenManager) validLocked(now time.Time) bool {
	if t.token == "" {
		return false
	}
	ahead := t.ahead
	if ahead < 0 {
		ahead = 0
	}
	return now.Before(t.expiresAt.Add(-ahead))
}

// refreshLocked 取一枚新 token。调用方必须持有 t.mu。
func (t *TokenManager) refreshLocked(ctx context.Context) (string, error) {
	if t.appID == "" || t.appSecret == "" {
		return "", fmt.Errorf("%s: appId/appSecret 未配置，取不到 access_token", ConfigKeyPrefix)
	}

	payload, err := json.Marshal(map[string]string{
		"appId":        t.appID,
		"clientSecret": t.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("%s: 序列化 token 请求失败：%w", ConfigKeyPrefix, err)
	}

	base := t.baseURL
	if base == "" {
		base = APIBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PathGetAppAccessToken, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("%s: 构造 token 请求失败：%w", ConfigKeyPrefix, err)
	}
	req.Header.Set("Content-Type", ContentTypeJSON)
	req.Header.Set("User-Agent", UserAgent)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: 请求 access_token 失败：%w", ConfigKeyPrefix, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
	if err != nil {
		return "", fmt.Errorf("%s: 读 access_token 响应失败：%w", ConfigKeyPrefix, err)
	}

	var out accessTokenResp
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}

	if out.AccessToken == "" {
		// 失败时 HTTP 也可能是 200，要靠 body 里的 code/err_code 判
		if apiErr := ParseAPIError(resp.StatusCode, body); apiErr != nil {
			return "", fmt.Errorf("%s: 获取 access_token 失败：%w", ConfigKeyPrefix, apiErr)
		}
		return "", fmt.Errorf("%s: 获取 access_token 失败：HTTP %d 的响应里没有 access_token", ConfigKeyPrefix, resp.StatusCode)
	}

	ttl := time.Duration(out.ExpiresIn.Seconds) * time.Second
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	ahead := t.ahead
	if ahead <= 0 {
		ahead = TokenRefreshAhead
	}
	if ttl <= ahead*2 {
		// 有效期还没提前量长（实测见过 184 秒），退成一半，避免每次调用都刷新
		ahead = ttl / 2
	}

	form := "数字"
	if out.ExpiresIn.Quoted {
		form = "字符串"
	}

	t.token = out.AccessToken
	t.expiresAt = time.Now().Add(ttl)
	t.ahead = ahead
	logger.Infof("%s: access_token 已刷新，有效期 %s（expires_in=%d，%s形态，提前 %s 刷新）",
		ConfigKeyPrefix, ttl, out.ExpiresIn.Seconds, form, ahead)
	return t.token, nil
}

// AppID 返回本 manager 绑定的 appId。
// ID 映射必须按 appId 隔离（同一个群在不同机器人下 openid 不同），上层会用到。
func (t *TokenManager) AppID() string { return t.appID }

// ExpiresAt 返回当前缓存 token 的到期时间，零值表示还没有 token。
func (t *TokenManager) ExpiresAt() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expiresAt
}

// Invalidate 丢弃缓存 token，下一次 Token 强制重新获取。
// 撞到 4004（token 无效）或 401 之类的路径上应该调它。
func (t *TokenManager) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.token = ""
	t.expiresAt = time.Time{}
}

// AuthHeader 拼鉴权头的值：Authorization: QQBot <token>
// 传进来的如果已经带了 QQBot 前缀，就原样返回。
func AuthHeader(token string) string {
	if token == "" {
		return ""
	}
	if strings.HasPrefix(token, AuthScheme+" ") {
		return token
	}
	return AuthScheme + " " + token
}
