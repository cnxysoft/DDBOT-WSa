package qqofficial

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// 配置段：application.yaml 里的 qqofficial.*
//
// 读法沿用仓库既有风格（跟 adapter/config.go 读 websocket.* 一样，直接打 viper 的
// GlobalConfig），每次调用重新读，不在这里缓存全局单例。

const (
	// ConfigKeyPrefix 是配置段前缀。
	ConfigKeyPrefix = "qqofficial"

	// ModeWebSocket 是长连接模式（默认，也是骨架阶段唯一实现的）。
	ModeWebSocket = "websocket"
	// ModeWebhook 是 HTTP 回调模式，接口留了名但尚未实现。
	ModeWebhook = "webhook"
)

// 配置键名（拼在 ConfigKeyPrefix 后面）。
const (
	ConfigKeyAppID             = "app-id"
	ConfigKeyAppSecret         = "app-secret"
	ConfigKeyMode              = "mode"
	ConfigKeyIntents           = "intents"
	ConfigKeyShardID           = "shard-id"
	ConfigKeyShardCount        = "shard-count"
	ConfigKeyAPIBaseURL        = "api-base-url"
	ConfigKeyHTTPTimeout       = "http-timeout"
	ConfigKeyTokenRefreshAhead = "token-refresh-ahead"
	ConfigKeyStartTimeout      = "start-timeout"

	// ConfigKeyJoinIntroTip 是机器人被拉进群后那条**短提示**（自我介绍的第一条，例如
	// 「@我发送 菜单」）。它和长文案都在 SendJoinIntro 里发，共用进群帧那个 event_id。
	//
	// 长文案**不是配置项**：走全局模板 trigger.group.bot_joined.tmpl，由 lsp 渲染后交给
	// SendJoinIntro。tip 留空 = 只发模板；
	// 全都没有 = 一条都不发，这是默认行为。配置保存并完成适配器更新后生效。
	ConfigKeyJoinIntroTip = "join-intro-tip"
)

// Config 是 qqofficial adapter 的运行期配置。
type Config struct {
	// AppID / AppSecret：QQ 开放平台管理端的机器人凭据。
	AppID     string
	AppSecret string

	// Mode：websocket（默认）或 webhook。骨架阶段只实现了 websocket。
	Mode string

	// Intents：事件订阅位掩码，默认 IntentGroupAndC2C。
	Intents int

	// ShardID / ShardCount：分片参数，未分片用 0 / 1。
	ShardID    int
	ShardCount int

	// APIBaseURL：业务接口域名，默认 https://api.bot.qq.com。
	// 网关（wss）地址**不在这里**——它只能运行时从 /gateway 取。
	APIBaseURL string

	// HTTPTimeout：单次 HTTP 请求超时。
	HTTPTimeout time.Duration

	// TokenRefreshAhead：access_token 到期前多久开始刷新（官方口径 60 秒）。
	TokenRefreshAhead time.Duration

	// StartTimeout：Start 等待首个 READY 的上限。
	StartTimeout time.Duration
}

// GetConfig 读 qqofficial.* 配置，缺项补默认值。
func GetConfig() *Config {
	return GetConfigFrom(config.GlobalConfig.Snapshot())
}

// GetConfigFrom decodes one candidate without changing the running configuration.
func GetConfigFrom(reader adapter.ConfigReader) *Config {
	str := func(key string) string { return strings.TrimSpace(reader.GetString(ConfigKeyPrefix + "." + key)) }
	integer := func(key string) int { return reader.GetInt(ConfigKeyPrefix + "." + key) }
	duration := func(key string) time.Duration { return reader.GetDuration(ConfigKeyPrefix + "." + key) }
	cfg := &Config{
		AppID:             firstNonEmpty(str(ConfigKeyAppID), str("appId")),
		AppSecret:         firstNonEmpty(str(ConfigKeyAppSecret), str("appSecret"), str("secret")),
		Mode:              strings.ToLower(str(ConfigKeyMode)),
		Intents:           integer(ConfigKeyIntents),
		ShardID:           integer(ConfigKeyShardID),
		ShardCount:        integer(ConfigKeyShardCount),
		APIBaseURL:        str(ConfigKeyAPIBaseURL),
		HTTPTimeout:       duration(ConfigKeyHTTPTimeout),
		TokenRefreshAhead: duration(ConfigKeyTokenRefreshAhead),
		StartTimeout:      duration(ConfigKeyStartTimeout),
	}
	cfg.ApplyDefaults()
	return cfg
}

// ApplyDefaults 把零值补成可用默认值。导出是为了让上层/测试能自己拼一份配置。
func (c *Config) ApplyDefaults() {
	if c.Mode == "" {
		c.Mode = ModeWebSocket
	}
	if c.Intents <= 0 {
		c.Intents = DefaultIntents
	}
	if c.ShardID < 0 {
		c.ShardID = DefaultShardID
	}
	if c.ShardCount <= 0 {
		c.ShardCount = DefaultShardCount
	}
	if c.ShardID >= c.ShardCount {
		// 分片号越界会直接被网关拒（4010），退回不分片
		c.ShardID = DefaultShardID
		c.ShardCount = DefaultShardCount
	}
	if c.APIBaseURL == "" {
		c.APIBaseURL = APIBase
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = HTTPTimeout
	}
	if c.TokenRefreshAhead <= 0 {
		c.TokenRefreshAhead = TokenRefreshAhead
	}
	if c.StartTimeout <= 0 {
		c.StartTimeout = DefaultStartTimeout
	}
}

// Validate 报告配置能不能连。
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("%s: 配置为空", ConfigKeyPrefix)
	}
	if c.AppID == "" {
		return fmt.Errorf("%s: 缺少 %s.%s", ConfigKeyPrefix, ConfigKeyPrefix, ConfigKeyAppID)
	}
	if c.AppSecret == "" {
		return fmt.Errorf("%s: 缺少 %s.%s", ConfigKeyPrefix, ConfigKeyPrefix, ConfigKeyAppSecret)
	}
	if c.Mode != ModeWebSocket {
		return fmt.Errorf("%s: mode %q 尚未实现（当前只支持 %s）", ConfigKeyPrefix, c.Mode, ModeWebSocket)
	}
	return nil
}

func getString(key string) string {
	return strings.TrimSpace(config.GlobalConfig.GetString(ConfigKeyPrefix + "." + key))
}

func getInt(key string) int {
	return config.GlobalConfig.GetInt(ConfigKeyPrefix + "." + key)
}

func getDuration(key string) time.Duration {
	return config.GlobalConfig.GetDuration(ConfigKeyPrefix + "." + key)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
