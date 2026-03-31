package twitch

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

var logger = utils.GetModuleLogger("twitch-concern")

const (
	Site                   = "twitch"
	Live concern_type.Type = "live"

	// freshCount阈值：开播通知在count<1时发送，下播通知在count<3时发送
	freshCountLiveThreshold   = int32(1)
	freshCountOfflineThreshold = int32(3)
)

var online bool

// userCache 缓存用户名映射 login -> displayName
var userCache = sync.Map{}

// twitchStateManager 包装 concern.StateManager，覆盖 GetGroupConcernConfig
type twitchStateManager struct {
	*concern.StateManager
}

func (sm *twitchStateManager) GetGroupConcernConfig(groupCode int64, id interface{}) concern.IConfig {
	return NewGroupConcernConfig(sm.StateManager.GetGroupConcernConfig(groupCode, id))
}

// TwitchConcern 是 Twitch 直播监控的 Concern 实现
type TwitchConcern struct {
	*twitchStateManager
	freshCount    atomic.Int32
	freshStopChan chan struct{}
}

func (c *TwitchConcern) Site() string {
	return Site
}

func (c *TwitchConcern) Types() []concern_type.Type {
	return []concern_type.Type{Live}
}

func (c *TwitchConcern) ParseId(s string) (interface{}, error) {
	if s == "" {
		return nil, fmt.Errorf("twitch 用户名不能为空")
	}
	return s, nil
}

func (c *TwitchConcern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

// Start 初始化 Twitch API 凭据并启动轮询
func (c *TwitchConcern) Start() error {
	c.freshStopChan = make(chan struct{})

	var tickerStarted bool
	defer func() {
		if !tickerStarted {
			close(c.freshStopChan)
		}
	}()

	c.UseEmitQueue()

	if config.GlobalConfig.Get("twitch") == nil {
		logger.Errorf("找不到 Twitch 配置，Twitch 订阅将不会启动")
		return fmt.Errorf("找不到 Twitch 配置，Twitch 订阅将不会启动。")
	}

	clientId := config.GlobalConfig.GetString("twitch.clientId")
	clientSecret := config.GlobalConfig.GetString("twitch.clientSecret")

	if clientId == "" || clientSecret == "" {
		logger.Errorf("twitch.clientId 和 twitch.clientSecret 不能为空，请在 application.yaml 中配置")
		return fmt.Errorf("twitch.clientId 和 twitch.clientSecret 不能为空，请在 application.yaml 中配置")
	}

	logger.Debug("正在初始化 Twitch API 凭据")
	InitToken(clientId, clientSecret)

	// 验证凭据有效性
	_, err := getAccessToken()
	if err != nil {
		logger.Errorf("Twitch API 认证失败: %v", err)
		return fmt.Errorf("Twitch API 认证失败: %w", err)
	}

	// 初始化 freshCount
	if !cfg.GetTwitchOnlyOnlineNotify() {
		c.freshCount.Store(1000) // 非 onlyOnlineNotify 模式，设置极大值
	}

	// 启动 freshCount 递增 goroutine
	go c.freshCountTicker()
	tickerStarted = true

	c.UseFreshFunc(c.twitchFresh())
	c.UseNotifyGeneratorFunc(c.twitchNotifyGenerator())

	logger.Info("Twitch 订阅模块启动成功")
	return c.StateManager.Start()
}

// freshCountTicker 每隔 concern.emitInterval 递增 freshCount
func (c *TwitchConcern) freshCountTicker() {
	interval := cfg.GetEmitInterval()
	if interval <= 0 {
		interval = time.Second * 5
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 每隔一个 emitInterval 递增 freshCount
			c.freshCount.Add(1)
			logger.Tracef("freshCount incremented to %d", c.freshCount.Load())
		case <-c.freshStopChan:
			return
		}
	}
}

func (c *TwitchConcern) Stop() {
	logger.Trace("正在停止 twitch concern")
	if c.freshStopChan != nil {
		close(c.freshStopChan)
	}
	c.StateManager.Stop()
	logger.Trace("twitch concern 已停止")
}

// Add 添加一个 Twitch 订阅
func (c *TwitchConcern) Add(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	login := id.(string)
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("login", login)

	log.Debug("正在添加 Twitch 订阅")

	// 验证用户存在
	user, err := GetUserByLogin(login)
	if err != nil {
		log.Errorf("查询 Twitch 用户失败: %v", err)
		return nil, fmt.Errorf("查询 Twitch 用户失败 [%s]: %v", login, err)
	}

	// 缓存用户名
	userCache.Store(login, user.DisplayName)

	_, err = c.GetStateManager().AddGroupConcern(groupCode, login, ctype)
	if err != nil {
		log.Errorf("AddGroupConcern error %v", err)
		return nil, err
	}

	log.WithField("displayName", user.DisplayName).Info("Twitch 订阅添加成功")
	return c.Get(id)
}

// Remove 删除一个 Twitch 订阅
func (c *TwitchConcern) Remove(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	login := id.(string)
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("login", login)

	log.Debug("正在移除 Twitch 订阅")
	identity, _ := c.Get(id)

	_, err := c.GetStateManager().RemoveGroupConcern(groupCode, login, ctype)
	if err != nil {
		log.Errorf("RemoveGroupConcern error %v", err)
		return nil, err
	}

	log.Info("Twitch 订阅移除成功")
	return identity, nil
}

// Get 获取订阅信息
func (c *TwitchConcern) Get(id interface{}) (concern.IdentityInfo, error) {
	login := id.(string)
	displayName := c.getDisplayName(login)
	name := fmt.Sprintf("%v(%v)", displayName, login)
	return concern.NewIdentity(id, name), nil
}

// getDisplayName 从缓存或 API 获取用户显示名
func (c *TwitchConcern) getDisplayName(login string) string {
	if data, ok := userCache.Load(login); ok {
		if name, ok := data.(string); ok {
			logger.WithField("login", login).Trace("displayName 缓存命中")
			return name
		}
	}

	user, err := GetUserByLogin(login)
	if err != nil {
		logger.WithField("login", login).Warnf("获取用户名失败: %v", err)
		return login
	}

	logger.WithField("login", login).WithField("displayName", user.DisplayName).Trace("displayName 已从 API 获取并缓存")
	userCache.Store(login, user.DisplayName)
	return user.DisplayName
}

// twitchFresh 创建轮询刷新函数
func (c *TwitchConcern) twitchFresh() concern.FreshFunc {
	return c.EmitQueueFresher(func(p concern_type.Type, id interface{}) ([]concern.Event, error) {
		login := id.(string)

		logger.Tracef("正在检测 Twitch 用户 (%v) 的直播状态..", login)

		stream, err := GetStreamByLogin(login)
		if err != nil {
			logger.WithField("login", login).Errorf("GetStreamByLogin error %v", err)
			return nil, err
		}

		isLive := stream != nil

		// 获取上次状态
		last, hasLast := c.getLastStatus(login)

		// 构造事件
		event := c.buildLiveEvent(login, stream, isLive, hasLast, last)

		// 判断状态变化
		if hasLast && last.Live == isLive {
			// 状态无变化，不推送
			logger.Tracef("%v 的直播状态与上次相同，已略过", login)
			return nil, nil
		}

		if !hasLast && !isLive {
			// 初始状态为离线，不推送
			logger.Tracef("%v 的初始状态为下播，已略过。", login)
			// 保存状态
			c.updateLastStatus(login, &LastStatus{Live: false, Title: ""})
			return nil, nil
		}

		// 更新状态
		c.updateLastStatus(login, &LastStatus{Live: isLive, Title: event.Title})

		currentCount := c.freshCount.Load()

		// 根据 freshCount 决定是否发送
		if !c.shouldNotify(event, currentCount) {
			logger.Tracef("%v 的通知因 freshCount(%d) 被过滤", login, currentCount)
			return nil, nil
		}

		if isLive {
			logger.WithField("login", login).WithField("title", event.Title).Debug("检测到 Twitch 开播")
		} else {
			logger.WithField("login", login).Debug("检测到 Twitch 下播")
		}

		return []concern.Event{event}, nil
	})
}

// shouldNotify 根据 freshCount 决定是否发送通知
// 开播通知在 count < 1 时发送，下播通知在 count < 3 时发送
func (c *TwitchConcern) shouldNotify(event *LiveEvent, count int32) bool {
	if event.Live {
		// 开播通知
		return count < freshCountLiveThreshold
	}
	// 下播通知
	return count < freshCountOfflineThreshold
}

// buildLiveEvent 构建直播事件
func (c *TwitchConcern) buildLiveEvent(login string, stream *StreamData, isLive, hasLast bool, last *LastStatus) *LiveEvent {
	event := &LiveEvent{
		Id:    login,
		Login: login,
		Name:  c.getDisplayName(login),
		Live:  isLive,
	}

	if isLive {
		event.Title = stream.Title
		event.GameName = stream.GameName
		event.ViewerCount = stream.ViewerCount
		event.ThumbnailURL = stream.ThumbnailURL
		event.Name = stream.UserName
		// 更新缓存
		userCache.Store(login, stream.UserName)

		// 检测标题变化
		if hasLast && last.Title != "" && last.Title != event.Title {
			event.titleChanged = true
		}
	}

	// 判断状态变化
	if !hasLast {
		event.liveStatusChanged = true // 首次发现
	} else if last.Live != isLive {
		event.liveStatusChanged = true // 状态变化
	}

	return event
}

// twitchNotifyGenerator 创建通知生成函数
func (c *TwitchConcern) twitchNotifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, event concern.Event) []concern.Notify {
		if liveEvent, ok := event.(*LiveEvent); ok {
			liveEvent.Logger().WithFields(localutils.GroupLogFields(groupCode)).Trace("生成 Twitch 推送通知")
			return []concern.Notify{
				&LiveNotify{
					groupCode: groupCode,
					LiveEvent: *liveEvent,
				},
			}
		}
		logger.WithFields(localutils.GroupLogFields(groupCode)).Errorf("未知的 Twitch 事件类型")
		return nil
	}
}

// LastStatus 记录上次的直播状态
type LastStatus struct {
	Live  bool   `json:"live"`
	Title string `json:"title"`
}

func (c *TwitchConcern) getLastStatus(login string) (*LastStatus, bool) {
	key := fmt.Sprintf("twitch_lastStatus_%s", login)
	var status LastStatus
	err := c.StateManager.GetJson(key, &status)
	if err != nil {
		return nil, false
	}
	return &status, true
}

func (c *TwitchConcern) updateLastStatus(login string, status *LastStatus) {
	key := fmt.Sprintf("twitch_lastStatus_%s", login)
	err := c.StateManager.SetJson(key, status)
	if err != nil {
		logger.Errorf("更新 Twitch 用户 %v 状态失败: %v", login, err)
	}
}

// NewConcern 创建新的 TwitchConcern 实例
func NewConcern(notify chan<- concern.Notify) *TwitchConcern {
	sm := &twitchStateManager{concern.NewStateManagerWithStringID(Site, notify)}
	return &TwitchConcern{twitchStateManager: sm}
}

// init 向框架注册 Twitch 插件
func init() {
	concern.RegisterConcern(NewConcern(concern.GetNotifyChan()))
}
