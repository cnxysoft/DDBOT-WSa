package twitch

import (
	"fmt"
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

// InitToken 初始化 token 存储的凭据信息
func InitToken(clientId, clientSecret string) {
	tokenStore.mu.Lock()
	defer tokenStore.mu.Unlock()
	tokenStore.clientId = clientId
	tokenStore.clientSecret = clientSecret
	tokenStore.accessToken = ""
	logger.Debug("Twitch Token 凭据已初始化")
}

// getAccessToken 获取有效的 access token，过期时自动刷新
func getAccessToken() (string, error) {
	tokenStore.mu.Lock()
	defer tokenStore.mu.Unlock()

	// 检查 token 是否有效（留 60 秒缓冲）
	if tokenStore.accessToken != "" {
		elapsed := time.Since(tokenStore.fetchedAt)
		if elapsed < time.Duration(tokenStore.expiresIn-60)*time.Second {
			logger.Trace("Twitch Token 缓存命中，使用已有 Token")
			return tokenStore.accessToken, nil
		}
	}

	if tokenStore.clientId == "" || tokenStore.clientSecret == "" {
		logger.Errorf("twitch clientId 或 clientSecret 未配置")
		return "", fmt.Errorf("twitch clientId 或 clientSecret 未配置")
	}

	logger.Debug("Twitch Token 已过期或不存在，正在刷新")

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
		return "", fmt.Errorf("获取 Twitch App Token 失败: %w", err)
	}
	if resp.AccessToken == "" {
		logger.Errorf("获取 Twitch App Token 失败: 响应为空")
		return "", fmt.Errorf("获取 Twitch App Token 失败: 响应为空")
	}

	tokenStore.accessToken = resp.AccessToken
	tokenStore.expiresIn = resp.ExpiresIn
	tokenStore.fetchedAt = time.Now()

	logger.WithField("expiresIn", resp.ExpiresIn).Debug("Twitch Token 刷新成功")

	return tokenStore.accessToken, nil
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

// GetStreamByLogin 根据用户登录名查询直播状态
// 返回 StreamData 如果正在直播，nil 如果离线
func GetStreamByLogin(login string) (*StreamData, error) {
	token, err := getAccessToken()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/streams?user_login=%s", apiBase, login)
	var resp StreamsResponse
	err = requests.Get(url, nil, &resp, apiOptions(token)...)
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
	token, err := getAccessToken()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/users?login=%s", apiBase, login)
	var resp UsersResponse
	err = requests.Get(url, nil, &resp, apiOptions(token)...)
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
func FormatThumbnailURL(url string, width, height int) string {
	result := strings.ReplaceAll(url, "{width}", fmt.Sprintf("%d", width))
	result = strings.ReplaceAll(result, "{height}", fmt.Sprintf("%d", height))
	return result
}
