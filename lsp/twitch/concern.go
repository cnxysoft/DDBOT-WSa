package twitch

import (
	"context"
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
	freshCountLiveThreshold    = int32(1)
	freshCountOfflineThreshold = int32(3)
)

var online bool

// userCache 缓存用户名映射 login -> displayName
var userCache = sync.Map{}

// UserInfoKey 返回用户信息的存储 key
func UserInfoKey(login string) string {
	return fmt.Sprintf("twitch:userinfo:%s", login)
}

// twitchStateManager 包装 concern.StateManager，覆盖 GetGroupConcernConfig
type twitchStateManager struct {
	*concern.StateManager
}

func (sm *twitchStateManager) GetGroupConcernConfig(groupCode int64, id interface{}) concern.IConfig {
	return NewGroupConcernConfig(sm.StateManager.GetGroupConcernConfig(groupCode, id))
}

// AddUserInfo 存储用户信息到 buntdb
func (sm *twitchStateManager) AddUserInfo(user *UserData) error {
	if user == nil {
		return fmt.Errorf("nil UserInfo")
	}
	return sm.SetJson(UserInfoKey(user.Login), user)
}

// GetUserInfo 从 buntdb 获取用户信息
func (sm *twitchStateManager) GetUserInfo(login string) (*UserData, error) {
	var user UserData
	err := sm.GetJson(UserInfoKey(login), &user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// TwitchConcern 是 Twitch 直播监控的 Concern 实现
type TwitchConcern struct {
	*twitchStateManager
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

	c.UseFreshFunc(c.fresh())
	c.UseNotifyGeneratorFunc(c.twitchNotifyGenerator())

	logger.Info("Twitch 订阅模块启动成功")
	return c.StateManager.Start()
}

func (c *TwitchConcern) Stop() {
	logger.Trace("正在停止 twitch concern")
	c.StateManager.Stop()
	logger.Trace("twitch concern 已停止")
}

// fresh 是自定义刷新函数，实现 Bilibili 风格的过滤机制
func (c *TwitchConcern) fresh() concern.FreshFunc {
	return func(ctx context.Context, eventChan chan<- concern.Event) {
		t := time.NewTimer(time.Second * 3)
		// 直接读取 twitch.interval，不使用 GetEmitIntervalForSite（它会 fallback 到 concern.emitInterval）
		interval := config.GlobalConfig.GetDuration("twitch.interval")
		if interval <= 0 {
			interval = time.Second * 30
		}
		var freshCount atomic.Int32
		if !cfg.GetTwitchOnlyOnlineNotify() {
			freshCount.Store(1000)
		}
		for {
			select {
			case <-t.C:
			case <-ctx.Done():
				return
			}
			start := time.Now()

			// 获取所有 Twitch 订阅
			_, ids, types, err := c.StateManager.ListConcernState(
				func(groupCode int64, id interface{}, p concern_type.Type) bool {
					return p.ContainAny(Live)
				})
			if err != nil {
				logger.Errorf("ListConcernState error %v", err)
			}

			// 提取所有 login
			var logins []string
			for _, id := range ids {
				if login, ok := id.(string); ok {
					logins = append(logins, login)
				}
			}

			// 批量查询所有用户的直播状态
			liveMap := make(map[string]*StreamData)
			if len(logins) > 0 {
				streams, err := GetStreamsByLogins(logins)
				if err != nil {
					logger.Errorf("GetStreamsByLogins error %v", err)
				} else {
					for i := range streams {
						liveMap[streams[i].UserLogin] = streams[i]
					}
				}
			}

			// 过滤出有效的订阅类型
			ids, _, err = c.GroupTypeById(ids, types)
			if err != nil {
				logger.Errorf("GroupTypeById error %v", err)
			}

			// 逐个检查状态变化
			for _, id := range ids {
				login := id.(string)
				stream, isLive := liveMap[login]
				last, hasLast := c.getLastStatus(login)

				if !hasLast {
					// 首次状态
					title := ""
					if isLive {
						event := c.buildLiveEvent(login, stream, isLive, false, nil)
						title = event.Title
						if freshCount.Load() < freshCountLiveThreshold {
							event.liveStatusChanged = true
							eventChan <- event
						}
					}
					c.updateLastStatus(login, &LastStatus{Live: isLive, Title: title})
					continue
				}

				if last.Live == isLive {
					// 状态无变化
					if isLive && stream != nil {
						// 检查标题变化
						if last.Title != "" && last.Title != stream.Title {
							event := c.buildLiveEvent(login, stream, isLive, hasLast, last)
							event.titleChanged = true
							event.liveStatusChanged = false
							if freshCount.Load() < freshCountLiveThreshold {
								eventChan <- event
							}
							c.updateLastStatus(login, &LastStatus{Live: isLive, Title: stream.Title})
						}
					}
					continue
				}

				// 状态变化
				event := c.buildLiveEvent(login, stream, isLive, hasLast, last)

				if isLive {
					// 开播
					if freshCount.Load() < freshCountLiveThreshold {
						event.liveStatusChanged = true
						eventChan <- event
					}
				} else {
					// 下播
					if freshCount.Load() < freshCountOfflineThreshold {
						event.liveStatusChanged = true
						eventChan <- event
					}
				}
				c.updateLastStatus(login, &LastStatus{Live: isLive, Title: event.Title})
			}

			freshCount.Add(1)
			end := time.Now()
			logger.WithField("cost", end.Sub(start)).Tracef("twitch fresh loop done")
			t.Reset(interval)
		}
	}
}

// buildLiveEvent 构建直播事件
func (c *TwitchConcern) buildLiveEvent(login string, stream *StreamData, isLive, hasLast bool, last *LastStatus) *LiveEvent {
	event := &LiveEvent{
		Id:    login,
		Login: login,
		Name:  c.getDisplayName(login),
		Live:  isLive,
	}

	// 获取用户信息（包含头像）
	if user, err := c.GetUserInfo(login); err == nil {
		event.ProfileImageURL = user.ProfileImageURL
		event.OfflineImageURL = user.OfflineImageURL
	}

	if isLive && stream != nil {
		event.Title = stream.Title
		event.GameName = stream.GameName
		event.ViewerCount = stream.ViewerCount
		event.ThumbnailURL = stream.ThumbnailURL
		event.Name = stream.UserName
		// 更新缓存
		userCache.Store(login, stream.UserName)
	}

	return event
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

	// 存储用户信息到 buntdb
	if err := c.AddUserInfo(user); err != nil {
		log.Warnf("存储用户信息失败: %v", err)
	}

	_, err = c.GetStateManager().AddGroupConcern(groupCode, login, ctype)
	if err != nil {
		log.Errorf("AddGroupConcern error %v", err)
		return nil, err
	}

	// 如果用户正在直播，发送通知
	if ctype.ContainAny(Live) {
		stream, err := GetStreamByLogin(login)
		if err == nil && stream != nil {
			event := c.buildLiveEvent(login, stream, true, false, nil)
			event.liveStatusChanged = true
			if ctx.GetTarget().TargetType().IsGroup() {
				defer c.GroupWatchNotify(groupCode, login)
			}
			if ctx.GetTarget().TargetType().IsPrivate() {
				defer func() {
					ctx.Send(mmsg.NewText("检测到该用户正在直播，但由于您目前处于私聊模式，" +
						"因此不会在群内推送本次直播，将在该用户下次直播时推送"))
				}()
			}
			_ = event // event 会在 GroupWatchNotify 中使用
		}
	}

	log.WithField("displayName", user.DisplayName).Info("Twitch 订阅添加成功")
	return c.Get(id)
}

// GroupWatchNotify 向指定群发送直播通知
func (c *TwitchConcern) GroupWatchNotify(groupCode int64, login string) {
	stream, err := GetStreamByLogin(login)
	if err != nil || stream == nil {
		return
	}
	event := c.buildLiveEvent(login, stream, true, false, nil)
	event.liveStatusChanged = true
	notify := &LiveNotify{
		groupCode: groupCode,
		LiveEvent: *event,
	}
	concern.GetNotifyChan() <- notify
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

// getDisplayName 从缓存或 API 获取用户显示名，同时刷新用户信息
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

	// 刷新 buntdb 中的用户信息
	if err := c.AddUserInfo(user); err != nil {
		logger.WithField("login", login).Warnf("刷新用户信息失败: %v", err)
	}

	return user.DisplayName
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
