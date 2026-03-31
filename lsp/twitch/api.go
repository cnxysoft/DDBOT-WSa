package twitch

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
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
	accessToken  string
	expiresIn    int
	fetchedAt    time.Time
	clientId     string
	clientSecret string
}

var tokenStore = &appToken{}

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

// InitToken 初始化 token 存储的凭据信息
func InitToken(clientId, clientSecret string) {
	tokenStore.mu.Lock()
	defer tokenStore.mu.Unlock()
	tokenStore.clientId = clientId
	tokenStore.clientSecret = clientSecret
	tokenStore.accessToken = ""
	logger.Debug("Twitch Token 凭据已初始化")
}

// isTokenExpired 检查token是否即将过期（60秒缓冲）
func isTokenExpired() bool {
	if tokenStore.accessToken == "" {
		return true
	}
	elapsed := time.Since(tokenStore.fetchedAt)
	return elapsed >= time.Duration(tokenStore.expiresIn-60)*time.Second
}

// getAccessToken 获取有效的 access token，过期时自动刷新
// 使用 double-checked locking 避免在HTTP请求期间持有锁
func getAccessToken() (string, error) {
	// 第一次检查：快速路径，缓存命中时无需加锁
	tokenStore.mu.Lock()
	if tokenStore.accessToken != "" && !isTokenExpired() {
		token := tokenStore.accessToken
		tokenStore.mu.Unlock()
		return token, nil
	}
	// 记录旧token以便失败时回退
	oldToken := tokenStore.accessToken
	tokenStore.mu.Unlock()

	// 检查凭据
	tokenStore.mu.Lock()
	if tokenStore.clientId == "" || tokenStore.clientSecret == "" {
		tokenStore.mu.Unlock()
		logger.Errorf("twitch clientId 或 clientSecret 未配置")
		return "", fmt.Errorf("twitch clientId 或 clientSecret 未配置")
	}
	tokenStore.mu.Unlock()

	logger.Debug("Twitch Token 已过期或不存在，正在刷新")

	// 在锁外执行HTTP请求
	var resp tokenResponse
	err := requests.PostWWWForm(authURL, gout.H{
		"client_id":     tokenStore.clientId,
		"client_secret": tokenStore.clientSecret,
		"grant_type":    "client_credentials",
	}, &resp,
		requests.TimeoutOption(time.Second*10),
		requests.RetryOption(3),
		requests.ProxyOption(proxy_pool.PreferOversea),
	)
	if err != nil {
		logger.Errorf("获取 Twitch App Token 失败: %v", err)
		// 失败时返回旧token（如果存在）
		if oldToken != "" {
			logger.Warn("Token刷新失败，使用旧Token")
			return oldToken, nil
		}
		return "", fmt.Errorf("获取 Twitch App Token 失败: %w", err)
	}
	if resp.AccessToken == "" {
		logger.Errorf("获取 Twitch App Token 失败: 响应为空")
		if oldToken != "" {
			return oldToken, nil
		}
		return "", fmt.Errorf("获取 Twitch App Token 失败: 响应为空")
	}

	// 更新token（持有锁）
	tokenStore.mu.Lock()
	tokenStore.accessToken = resp.AccessToken
	tokenStore.expiresIn = resp.ExpiresIn
	tokenStore.fetchedAt = time.Now()
	tokenStore.mu.Unlock()

	logger.WithField("expiresIn", resp.ExpiresIn).Debug("Twitch Token 刷新成功")

	return resp.AccessToken, nil
}

// apiOptions 返回 Twitch API 请求的通用选项
func apiOptions(token string) []requests.Option {
	return []requests.Option{
		requests.HeaderOption("Client-Id", tokenStore.clientId),
		requests.HeaderOption("Authorization", "Bearer "+token),
		requests.TimeoutOption(time.Second * 10),
		requests.RetryOption(3),
		requests.ProxyOption(proxy_pool.PreferOversea),
	}
}

// buildLoginQuery 构建 user_login 查询参数
func buildLoginQuery(logins []string) string {
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

// GetStreamsByLogins 批量获取多个用户的直播状态
// Twitch API 最多 100 个用户一次请求
func GetStreamsByLogins(logins []string) ([]*StreamData, error) {
	if len(logins) == 0 {
		return nil, nil
	}

	token, err := getAccessToken()
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

		streams, err := getStreamsBatch(token, batch)
		if err != nil {
			logger.Errorf("批量查询 Twitch 直播状态失败: %v", err)
			return nil, err
		}
		// 转换为指针切片
		for i := range streams {
			allStreams = append(allStreams, &streams[i])
		}
	}

	return allStreams, nil
}

// getStreamsBatch 批量查询一批用户的直播状态
func getStreamsBatch(token string, logins []string) ([]StreamData, error) {
	query := buildLoginQuery(logins)
	urlStr := fmt.Sprintf("%s/streams?%s", apiBase, query)

	var resp StreamsResponse
	err := requests.Get(urlStr, nil, &resp, apiOptions(token)...)
	if err != nil {
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

	token, err := getAccessToken()
	if err != nil {
		return nil, err
	}

	query := buildLoginQuery([]string{login})
	urlStr := fmt.Sprintf("%s/streams?%s", apiBase, query)

	var resp StreamsResponse
	err = requests.Get(urlStr, nil, &resp, apiOptions(token)...)
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

	token, err := getAccessToken()
	if err != nil {
		return nil, err
	}

	query := buildLoginQuery([]string{login})
	urlStr := fmt.Sprintf("%s/users?%s", apiBase, query)

	var resp UsersResponse
	err = requests.Get(urlStr, nil, &resp, apiOptions(token)...)
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
