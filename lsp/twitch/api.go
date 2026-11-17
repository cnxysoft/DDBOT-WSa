package twitch

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/guonaihong/gout"
)

const (
	authURL = "https://id.twitch.tv/oauth2/token"
	apiBase = "https://api.twitch.tv/helix"
)

// validLoginRegex Twitch用户名格式验证：4-25字符，字母数字下划线
var validLoginRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{4,25}$`)

// appToken 缓存的 App Access Token
type appToken struct {
	mu           sync.Mutex
	refreshMu    sync.Mutex
	generation   uint64
	accessToken  string
	expiresIn    int
	fetchedAt    time.Time
	clientId     string
	clientSecret string
}

var tokenStore = &appToken{}

// consecutiveFailures 记录连续失败次数，用于判断是否需要重置连接池
var consecutiveFailures atomic.Int32

// resetTransportIfNeeded 在连续多次请求失败后关闭 DefaultTransport 的空闲连接，强制建立新连接
func resetTransportIfNeeded() {
	if consecutiveFailures.Load() >= 3 {
		logger.Warn("Twitch API 连续多次请求超时，重置空闲连接")
		if closer, ok := http.DefaultTransport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
		consecutiveFailures.Store(0)
	}
}

// invalidateToken 强制清除已缓存的 Token，下次请求时重新获取
func invalidateToken(auth apiAuth) {
	tokenStore.mu.Lock()
	if tokenStore.generation == auth.generation {
		tokenStore.accessToken = ""
	}
	tokenStore.mu.Unlock()
	logger.Warn("Twitch Token 已失效，下次请求时将重新获取")
}

// tokenResponse 是 Twitch OAuth2 token 响应
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// StreamData 表示 Twitch Helix streams 响应中的单个流
type StreamData struct {
	ID           string   `json:"id"`
	UserID       string   `json:"user_id"`
	UserLogin    string   `json:"user_login"`
	UserName     string   `json:"user_name"`
	GameID       string   `json:"game_id"`
	GameName     string   `json:"game_name"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	ViewerCount  int      `json:"viewer_count"`
	StartedAt    string   `json:"started_at"`
	Language     string   `json:"language"`
	ThumbnailURL string   `json:"thumbnail_url"`
	Tags         []string `json:"tags"`
	IsMature     bool     `json:"is_mature"`
}

// StreamsResponse 是 GET /helix/streams 响应
type StreamsResponse struct {
	Data []StreamData `json:"data"`
}

// UserData 表示 Twitch Helix users 响应中的单个用户
type UserData struct {
	ID              string `json:"id"`
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	BroadcasterType string `json:"broadcaster_type"`
	Description     string `json:"description"`
	ProfileImageURL string `json:"profile_image_url"`
	OfflineImageURL string `json:"offline_image_url"`
}

// UsersResponse 是 GET /helix/users 响应
type UsersResponse struct {
	Data []UserData `json:"data"`
}

// ErrUserNotFound Twitch用户不存在
var ErrUserNotFound = errors.New("twitch 用户不存在")

// InitToken installs a credential generation. Requests already in flight cannot
// publish their old token into a newer generation.
func InitToken(clientId, clientSecret string) {
	tokenStore.install(&appToken{clientId: clientId, clientSecret: clientSecret})
}

type apiAuth struct {
	token, clientID string
	generation      uint64
}

func (store *appToken) install(candidate *appToken) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.generation++
	store.clientId, store.clientSecret = candidate.clientId, candidate.clientSecret
	store.accessToken, store.expiresIn, store.fetchedAt = candidate.accessToken, candidate.expiresIn, candidate.fetchedAt
}

func (store *appToken) configured() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.clientId != "" && store.clientSecret != ""
}

func requestAppToken(clientID, secret string) (tokenResponse, error) {
	var resp tokenResponse
	err := requests.PostWWWForm(authURL, gout.H{"client_id": clientID, "client_secret": secret, "grant_type": "client_credentials"}, &resp,
		requests.TimeoutOption(10*time.Second), requests.RetryOption(3), requests.ProxyOption(proxy_pool.PreferOversea))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("Twitch API 认证请求失败")
	}
	if resp.AccessToken == "" || resp.ExpiresIn <= 0 {
		return tokenResponse{}, fmt.Errorf("Twitch API 未返回有效令牌")
	}
	return resp, nil
}

func (store *appToken) auth(fetch func(string, string) (tokenResponse, error)) (apiAuth, error) {
	store.refreshMu.Lock()
	defer store.refreshMu.Unlock()
	for {
		store.mu.Lock()
		generation, clientID, secret := store.generation, store.clientId, store.clientSecret
		oldToken := store.accessToken
		if oldToken != "" && time.Since(store.fetchedAt) < time.Duration(store.expiresIn-60)*time.Second {
			store.mu.Unlock()
			return apiAuth{oldToken, clientID, generation}, nil
		}
		store.mu.Unlock()
		if clientID == "" || secret == "" {
			return apiAuth{}, fmt.Errorf("twitch.clientId 和 twitch.clientSecret 未配置")
		}
		resp, err := fetch(clientID, secret)
		store.mu.Lock()
		if generation != store.generation {
			store.mu.Unlock()
			continue
		}
		if err != nil {
			store.mu.Unlock()
			if oldToken != "" {
				return apiAuth{oldToken, clientID, generation}, nil
			}
			return apiAuth{}, err
		}
		store.accessToken, store.expiresIn, store.fetchedAt = resp.AccessToken, resp.ExpiresIn, time.Now()
		store.mu.Unlock()
		return apiAuth{resp.AccessToken, clientID, generation}, nil
	}
}

func getAPIAuth() (apiAuth, error)    { return tokenStore.auth(requestAppToken) }
func getAccessToken() (string, error) { auth, err := getAPIAuth(); return auth.token, err }

func apiOptions(auth apiAuth) []requests.Option {
	return []requests.Option{
		requests.HeaderOption("Client-Id", auth.clientID), requests.HeaderOption("Authorization", "Bearer "+auth.token),
		requests.TimeoutOption(10 * time.Second), requests.RetryOption(3), requests.ProxyOption(proxy_pool.PreferOversea),
	}
}

// buildUserLoginQuery 构建 user_login 查询参数（用于 streams API）
func buildUserLoginQuery(logins []string) string {
	var sb strings.Builder
	for i, login := range logins {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString("user_login=")
		sb.WriteString(url.QueryEscape(login))
	}
	return sb.String()
}

// buildLoginQuery 构建 login 查询参数（用于 users API）
func buildLoginQuery(logins []string) string {
	var sb strings.Builder
	for i, login := range logins {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString("login=")
		sb.WriteString(url.QueryEscape(login))
	}
	return sb.String()
}

// GetStreamsByLogins 批量获取多个用户的直播状态
// Twitch API 最多 100 个用户一次请求
func GetStreamsByLogins(logins []string) ([]*StreamData, error) {
	if len(logins) == 0 {
		return nil, nil
	}

	// 如果之前连续失败多次，先重置连接池再发请求
	resetTransportIfNeeded()

	auth, err := getAPIAuth()
	if err != nil {
		return nil, err
	}

	var allStreams []*StreamData

	// 分批处理，每批最多 100 个
	for i := 0; i < len(logins); i += 100 {
		end := i + 100
		if end > len(logins) {
			end = len(logins)
		}
		batch := logins[i:end]

		streams, err := getStreamsBatch(auth, batch)
		if err != nil {
			consecutiveFailures.Add(1)
			logger.Errorf("批量查询 Twitch 直播状态失败: %v", err)
			return nil, err
		}
		// 转换为指针切片
		for i := range streams {
			allStreams = append(allStreams, &streams[i])
		}
	}

	consecutiveFailures.Store(0)
	return allStreams, nil
}

// getStreamsBatch 批量查询一批用户的直播状态
func getStreamsBatch(auth apiAuth, logins []string) ([]StreamData, error) {
	query := buildUserLoginQuery(logins)
	urlStr := fmt.Sprintf("%s/streams?%s", apiBase, query)

	var resp StreamsResponse
	var httpCode int
	opts := append(apiOptions(auth), requests.HttpCodeOption(&httpCode))
	err := requests.Get(urlStr, nil, &resp, opts...)
	if err != nil {
		if httpCode == http.StatusUnauthorized {
			invalidateToken(auth)
		}
		logger.WithField("logins", logins).Errorf("查询 Twitch 直播状态失败: %v", err)
		return nil, fmt.Errorf("查询 Twitch 直播状态失败: %w", err)
	}

	logger.WithField("count", len(resp.Data)).Trace("批量查询直播状态成功")
	return resp.Data, nil
}

// GetStreamByLogin 根据用户登录名查询直播状态
// 返回 StreamData 如果正在直播，nil 如果离线
func GetStreamByLogin(login string) (*StreamData, error) {
	// 验证login格式
	if !validLoginRegex.MatchString(login) {
		return nil, fmt.Errorf("无效的 Twitch 用户名: %s", login)
	}

	auth, err := getAPIAuth()
	if err != nil {
		return nil, err
	}

	query := buildUserLoginQuery([]string{login})
	urlStr := fmt.Sprintf("%s/streams?%s", apiBase, query)

	var resp StreamsResponse
	err = requests.Get(urlStr, nil, &resp, apiOptions(auth)...)
	if err != nil {
		logger.WithField("login", login).Errorf("查询 Twitch 直播状态失败: %v", err)
		return nil, fmt.Errorf("查询 Twitch 直播状态失败 [%s]: %w", login, err)
	}

	if len(resp.Data) == 0 {
		logger.WithField("login", login).Trace("当前未直播")
		return nil, nil // 离线
	}
	logger.WithField("login", login).WithField("title", resp.Data[0].Title).Trace("当前正在直播")
	return &resp.Data[0], nil
}

// GetUserByLogin 根据用户登录名查询用户信息
func GetUserByLogin(login string) (*UserData, error) {
	// 验证login格式
	if !validLoginRegex.MatchString(login) {
		return nil, fmt.Errorf("无效的 Twitch 用户名: %s", login)
	}

	auth, err := getAPIAuth()
	if err != nil {
		return nil, err
	}

	query := buildLoginQuery([]string{login})
	urlStr := fmt.Sprintf("%s/users?%s", apiBase, query)

	var resp UsersResponse
	err = requests.Get(urlStr, nil, &resp, apiOptions(auth)...)
	if err != nil {
		logger.WithField("login", login).Errorf("查询 Twitch 用户信息失败: %v", err)
		return nil, fmt.Errorf("查询 Twitch 用户信息失败 [%s]: %w", login, err)
	}

	if len(resp.Data) == 0 {
		logger.WithField("login", login).Warn("Twitch 用户不存在")
		return nil, ErrUserNotFound
	}
	logger.WithField("login", login).WithField("displayName", resp.Data[0].DisplayName).Trace("查询 Twitch 用户信息成功")
	return &resp.Data[0], nil
}

// FormatThumbnailURL 将 Twitch 缩略图 URL 中的 {width}x{height} 替换为实际尺寸
func FormatThumbnailURL(urlStr string, width, height int) string {
	result := strings.ReplaceAll(urlStr, "{width}", fmt.Sprintf("%d", width))
	result = strings.ReplaceAll(result, "{height}", fmt.Sprintf("%d", height))
	return result
}
