package missevan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	templUtils "github.com/Sora233/MiraiGo-Template/utils"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

const (
	// 这个名字是日志中的名字，如果不知道取什么名字，可以和Site一样
	ConcernName = "missevan-concern"

	// 插件支持的网站名
	Site = "missevan"

	// 订阅类型：直播开播/下播/标题变化，以及主页新声音
	Live concern_type.Type = "live"
	News concern_type.Type = "news"

	// 用户状态的持久化键前缀
	userStateKey = "MissevanUserState"
)

var logger = templUtils.GetModuleLogger(ConcernName)

var errUserStateEmpty = errors.New("猫耳FM用户状态为空")

// Concern 实现猫耳FM的订阅模块，订阅目标为用户ID（int64）。
type Concern struct {
	*concern.StateManager
	client *Client
}

func NewConcern() *Concern {
	c := &Concern{
		client: &Client{},
	}
	c.StateManager = concern.NewStateManagerWithInt64ID(Site, concern.GetNotifyChan())
	return c
}

func (c *Concern) Site() string {
	return Site
}

func (c *Concern) Types() []concern_type.Type {
	return []concern_type.Type{Live, News}
}

func (c *Concern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

// ParseId 支持三种写法：用户ID、个人主页链接、直播间链接。
// 返回的int64是订阅目标的主播用户ID，直播间链接会先回查主播ID。
func (c *Concern) ParseId(s string) (interface{}, error) {
	id, err := parseUserID(s)
	if err == nil {
		return id, nil
	}
	roomID, roomErr := parseLiveRoomID(s)
	if roomErr != nil {
		return nil, err
	}
	snapshot, err := c.client.FetchLive(roomID)
	if err != nil {
		return nil, err
	}
	if snapshot.CreatorID <= 0 {
		return nil, errors.New("猫耳FM直播间未返回主播用户ID")
	}
	return snapshot.CreatorID, nil
}

// parseUserID 解析用户ID或个人主页链接，返回主播用户ID。
func parseUserID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("请输入猫耳FM用户ID或个人主页链接")
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		if v <= 0 {
			return 0, errors.New("请输入猫耳FM用户ID或个人主页链接")
		}
		return v, nil
	}
	if strings.Contains(s, "missevan.com") && !strings.Contains(s, "fm.missevan.com") {
		u, err := url.Parse(s)
		if err != nil {
			return 0, errors.New("不是猫耳FM链接")
		}
		seg := strings.TrimSpace(strings.Trim(u.Path, "/"))
		if idx := strings.Index(seg, "/"); idx >= 0 {
			seg = seg[:idx]
		}
		if v, err := strconv.ParseInt(seg, 10, 64); err == nil && v > 0 {
			return v, nil
		}
		// 成品 parseUserID（concern.go:51）此分支用 43 字节 rodata 0x10d688b：
		// "猫耳FM个人主页中没有有效用户ID"。
		return 0, errors.New("猫耳FM个人主页中没有有效用户ID")
	}
	return 0, errors.New("不是猫耳FM链接")
}

// parseLiveRoomID 解析直播间链接，返回直播间号。
func parseLiveRoomID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || !strings.Contains(s, "fm.missevan.com") {
		return 0, errors.New("不是猫耳FM链接")
	}
	u, err := url.Parse(s)
	if err != nil {
		return 0, errors.New("不是猫耳FM链接")
	}
	seg := strings.TrimSpace(strings.Trim(u.Path, "/"))
	if idx := strings.Index(seg, "/"); idx >= 0 {
		seg = seg[:idx]
	}
	v, err := strconv.ParseInt(seg, 10, 64)
	if err != nil || v <= 0 {
		return 0, errors.New("不是猫耳FM链接")
	}
	return v, nil
}

// ResolveSubscribedID 把命令里输入的原始id解析成已订阅的主播用户ID，
// 直播间链接会通过直播接口回查主播ID。
func (c *Concern) ResolveSubscribedID(groupCode int64, rawID string, ctype concern_type.Type) (interface{}, error) {
	id, err := parseUserID(rawID)
	if err == nil {
		if _, err := c.GetConcern(id); err == nil {
			return id, nil
		}
	}
	roomID, roomErr := parseLiveRoomID(rawID)
	if roomErr != nil {
		return nil, err
	}
	snapshot, err := c.client.FetchLive(roomID)
	if err != nil {
		return nil, err
	}
	if snapshot.CreatorID <= 0 {
		return nil, errors.New("猫耳FM直播间未返回主播用户ID")
	}
	return snapshot.CreatorID, nil
}

// stateKey 返回用户状态在 buntdb 中的键。
//
// 证据（DDBOT-Lite x64 成品，missevan.stateKey @ concern.go:105，416 字节，包级函数）：
// 反汇编为 convT64(uid) 后构造 []interface{}{userStateKey, uid} 并调用 buntdb.Key，
// 即 localdb.Key(userStateKey, uid)。成品中该函数只有 getState/setState/Remove 三个调用点，
// 且没有任何地方取它的地址（无 lea），因此不是 KeyPatternFunc。
func stateKey(uid int64) string {
	return localdb.Key(userStateKey, uid)
}

func (c *Concern) getState(uid int64) (*UserState, error) {
	var state *UserState
	err := c.GetJson(stateKey(uid), &state, localdb.IgnoreNotFoundOpt())
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (c *Concern) setState(state *UserState) error {
	if state == nil || state.UserID <= 0 {
		return errUserStateEmpty
	}
	return c.SetJson(stateKey(state.UserID), state)
}

// loadProfile 拉取个人主页并把用户名/头像/直播间号更新到用户状态，返回更新后的状态。
func (c *Concern) loadProfile(uid int64) (*UserState, error) {
	profile, err := c.client.FetchProfile(uid)
	if err != nil {
		return nil, err
	}
	// 读取失败/不存在都不阻塞资料刷新，缺失时新建状态
	state, _ := c.getState(uid)
	if state == nil {
		state = &UserState{UserID: uid}
	}
	state.Name = profile.Name
	state.Avatar = profile.Avatar
	state.RoomID = profile.RoomID
	state.ProfileCheckedAt = time.Now().Unix()
	if err := c.setState(state); err != nil {
		return nil, err
	}
	return state, nil
}

// initializeTypes 在首次订阅时建立各类型的基线，只记基线不补发历史内容。
// state 是当前用户状态，profile 是本次可能已抓取的个人主页（可为nil）。
func (c *Concern) initializeTypes(state *UserState, ctype concern_type.Type, profile *Profile) error {
	if state == nil {
		return errUserStateEmpty
	}

	if ctype.ContainAny(News) && !state.NewsInitialized {
		list, err := c.client.FetchSounds(state.UserID, defaultSoundPageSize)
		if err != nil {
			return err
		}
		var maxID int64
		for _, sound := range list.Info.Data {
			if sound != nil && sound.ID > maxID {
				maxID = sound.ID
			}
		}
		state.LatestSoundID = maxID
		state.NewsInitialized = true
	}

	if ctype.ContainAny(Live) && !state.LiveInitialized {
		state.LiveInitialized = true
		if profile != nil {
			state.Living = profile.PageLive
		} else {
			state.Living = false
		}

		if state.RoomID > 0 {
			snapshot, err := c.client.FetchLive(state.RoomID)
			if err != nil {
				// 拉不到直播接口时先用个人页的直播状态做临时基线，下一轮刷新会重试
				logger.WithFields(logrus.Fields{
					"uid":     state.UserID,
					"room_id": state.RoomID,
				}).Warnf("初始化猫耳FM直播基线失败，临时使用个人页直播状态")
			} else {
				state.Living = snapshot.Living
				if snapshot.RoomID > 0 {
					state.RoomID = snapshot.RoomID
				}
				if snapshot.Name != "" {
					state.Name = snapshot.Name
				}
				if snapshot.Avatar != "" {
					state.Avatar = snapshot.Avatar
				}
				if snapshot.Living {
					state.LastLiveTitle = snapshot.Title
					state.LastLiveCover = snapshot.Cover
				}
			}
		}

		// 直播接口不可用时用个人页横幅补齐直播标题/封面
		if profile != nil {
			if state.Living && state.LastLiveTitle == "" {
				state.LastLiveTitle = profile.PageLiveTitle
			}
			if state.LastLiveCover == "" {
				state.LastLiveCover = profile.PageLiveCover
			}
		}
	}

	return c.setState(state)
}

func (c *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	var uid = id.(int64)

	if err := c.StateManager.CheckGroupConcern(groupCode, id, ctype); err != nil {
		return nil, err
	}

	state, err := c.getState(uid)
	if err != nil {
		return nil, err
	}

	// 用户状态缺失或还没拿到用户名时，先回源个人主页补一份
	var profile *Profile
	if state == nil || state.Name == "" {
		profile, err = c.client.FetchProfile(uid)
		if err != nil {
			return nil, fmt.Errorf("查询猫耳FM用户失败 %d - %w", uid, err)
		}
		if state == nil {
			state = &UserState{UserID: uid}
		}
		state.Name = profile.Name
		state.Avatar = profile.Avatar
		state.RoomID = profile.RoomID
		state.ProfileCheckedAt = time.Now().Unix()
	}

	// 首次订阅先建立基线，避免把历史声音和当前直播状态当成更新推送
	if err = c.initializeTypes(state, ctype, profile); err != nil {
		return nil, fmt.Errorf("初始化猫耳FM订阅基线失败: %w", err)
	}

	if _, err = c.StateManager.AddGroupConcern(groupCode, id, ctype); err != nil {
		return nil, err
	}

	logger.WithFields(localutils.GroupLogFields(groupCode)).WithFields(logrus.Fields{
		"uid":     uid,
		"name":    state.Name,
		"type":    ctype,
		"room_id": state.RoomID,
	}).Infof("猫耳FM订阅添加成功")

	return state, nil
}

func (c *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	var uid = id.(int64)
	identity, _ := c.Get(id)

	_, err := c.StateManager.RemoveGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}

	// 其他群可能还在订阅同一用户，只有最后一个订阅被移除后才清理用户状态
	if _, err := c.GetConcern(id); err != nil {
		if _, err := c.Delete(stateKey(uid), localdb.IgnoreNotFoundOpt()); err != nil {
			logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("uid", uid).
				Errorf("remove UserState error %v", err)
		}
	}
	return identity, nil
}

func (c *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	uid := id.(int64)
	state, err := c.getState(uid)
	if err != nil {
		return nil, err
	}
	if state == nil {
		// 本地没有状态时回源个人主页补一份
		state, err = c.loadProfile(uid)
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}

// getInterval 读取duration类型的配置项，配置低于min时回退默认值。
func getInterval(key string, def time.Duration, min time.Duration) time.Duration {
	interval := def
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration(key); v > 0 {
			interval = v
		}
	}
	if interval < min {
		interval = def
	}
	return interval
}

func liveInterval() time.Duration {
	return getInterval("missevan.liveInterval", defaultLiveInterval, minLiveInterval)
}

func newsInterval() time.Duration {
	return getInterval("missevan.newsInterval", defaultNewsInterval, minNewsInterval)
}

// subscribedIDs 返回订阅了指定类型的去重用户ID列表，同一用户多群订阅只刷新一次。
func (c *Concern) subscribedIDs(ctype concern_type.Type) ([]int64, error) {
	_, ids, _, err := c.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool {
		return p.ContainAll(ctype)
	})
	if err != nil {
		logger.WithError(err).Errorf("读取猫耳FM订阅列表失败")
		return nil, err
	}
	idSet := make(map[int64]struct{})
	var result []int64
	for _, id := range ids {
		uid, ok := id.(int64)
		if !ok {
			continue
		}
		if _, exist := idSet[uid]; exist {
			continue
		}
		idSet[uid] = struct{}{}
		result = append(result, uid)
	}
	return result, nil
}

// refreshProfileIfNeeded 按profileInterval复查用户名/头像/直播间号，
// 返回（可能是重新拉取后的）用户状态。
func (c *Concern) refreshProfileIfNeeded(state *UserState) (*UserState, error) {
	if state == nil {
		return nil, errUserStateEmpty
	}
	// 用户名还没有、从没复查过、或已超过复查间隔时才回源个人主页
	if state.Name != "" && state.ProfileCheckedAt > 0 &&
		time.Since(time.Unix(state.ProfileCheckedAt, 0)) < getInterval("missevan.profileInterval", defaultProfileInterval, minProfileInterval) {
		return state, nil
	}
	return c.loadProfile(state.UserID)
}

// refreshLive 检查单个用户的直播状态变化，返回产生的事件。
// 直播接口失败时保留已有基线不误报；首次建立基线不推送。
func (c *Concern) refreshLive(uid int64) ([]concern.Event, error) {
	state, err := c.getState(uid)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, errUserStateEmpty
	}
	log := logger.WithField("uid", uid)

	// 直播间号为空时先复查个人页；仍为空则只跟踪个人页直播状态，不产生事件
	if state.RoomID <= 0 {
		state, err = c.refreshProfileIfNeeded(state)
		if err != nil {
			return nil, err
		}
	}

	if state.RoomID <= 0 {
		// 没有直播间号：只记基线不产生事件，等个人页复查发现直播间号后再走直播接口
		if !state.LiveInitialized {
			state.LiveInitialized = true
			state.Living = false
			if err := c.setState(state); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}

	snapshot, err := c.client.FetchLive(state.RoomID)
	if err != nil {
		if !state.LiveInitialized {
			// 初始化直播基线失败，临时使用个人页直播状态
			log.WithError(err).Warnf("初始化猫耳FM直播基线失败，临时使用个人页直播状态")
			profile, perr := c.client.FetchProfile(uid)
			if perr != nil {
				return nil, err
			}
			state.LiveInitialized = true
			state.Living = profile.PageLive
			state.LastLiveTitle = profile.PageLiveTitle
			state.LastLiveCover = profile.PageLiveCover
			if err := c.setState(state); err != nil {
				return nil, err
			}
			return nil, nil
		}
		// 接口失败保留基线不误报
		return nil, err
	}

	if !state.LiveInitialized {
		// 首轮只记基线不推送
		state.LiveInitialized = true
		state.Living = snapshot.Living
		state.LastLiveTitle = snapshot.Title
		state.LastLiveCover = snapshot.Cover
		if err := c.setState(state); err != nil {
			return nil, err
		}
		return nil, nil
	}

	statusChanged := state.Living != snapshot.Living
	titleChanged := snapshot.Living && snapshot.Title != state.LastLiveTitle
	if !statusChanged && !titleChanged {
		return nil, nil
	}

	oldLiving := state.Living
	info := &LiveInfo{
		UserID:           uid,
		Name:             snapshot.Name,
		Avatar:           snapshot.Avatar,
		RoomID:           snapshot.RoomID,
		Title:            snapshot.Title,
		Cover:            snapshot.Cover,
		IsLiving:         snapshot.Living,
		StatusChanged:    statusChanged,
		LiveTitleChanged: titleChanged,
	}
	state.Living = snapshot.Living
	state.LastLiveTitle = snapshot.Title
	state.LastLiveCover = snapshot.Cover
	if err := c.setState(state); err != nil {
		return nil, err
	}
	log.WithFields(logrus.Fields{
		"old_living": oldLiving,
		"living":     snapshot.Living,
	}).Infof("猫耳FM直播状态发生变化")
	return []concern.Event{info}, nil
}

// refreshNews 检查单个用户的新声音，返回产生的事件。
// 首次检查只记录最新声音ID不补发。
func (c *Concern) refreshNews(uid int64) ([]concern.Event, error) {
	state, err := c.getState(uid)
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = &UserState{UserID: uid}
	}

	// 复查用户名/头像/直播间号，必要时重新抓个人主页
	state, err = c.refreshProfileIfNeeded(state)
	if err != nil {
		return nil, err
	}

	list, err := c.client.FetchSounds(uid, defaultSoundPageSize)
	if err != nil {
		return nil, err
	}

	var maxID int64
	for _, sound := range list.Info.Data {
		if sound != nil && sound.ID > maxID {
			maxID = sound.ID
		}
	}

	if !state.NewsInitialized {
		// 首次检查只记基线不补发
		state.NewsInitialized = true
		state.LatestSoundID = maxID
		return nil, c.setState(state)
	}

	var newSounds []*Sound
	for _, sound := range list.Info.Data {
		if sound != nil && sound.ID > state.LatestSoundID {
			newSounds = append(newSounds, sound)
		}
	}
	if maxID > state.LatestSoundID {
		state.LatestSoundID = maxID
	}
	if err := c.setState(state); err != nil {
		return nil, err
	}
	if len(newSounds) == 0 {
		return nil, nil
	}

	sort.Slice(newSounds, func(i, j int) bool {
		return newSounds[i].ID < newSounds[j].ID
	})

	logger.WithFields(logrus.Fields{
		"uid":        uid,
		"new_sounds": len(newSounds),
	}).Infof("检测到猫耳FM新声音")
	return []concern.Event{
		&NewsInfo{
			UserID: uid,
			Name:   state.Name,
			Avatar: state.Avatar,
			Sounds: newSounds,
		},
	}, nil
}

// refreshType 刷新一类订阅的所有去重用户，产生的事件发送到eventChan。
func (c *Concern) refreshType(ctype concern_type.Type, eventChan chan<- concern.Event) {
	ids, err := c.subscribedIDs(ctype)
	if err != nil {
		return
	}
	for _, uid := range ids {
		var (
			events []concern.Event
			err    error
		)
		switch ctype {
		case Live:
			events, err = c.refreshLive(uid)
		case News:
			events, err = c.refreshNews(uid)
		default:
			continue
		}
		if err != nil {
			logger.WithError(err).WithField("uid", uid).Errorf("刷新猫耳FM订阅失败")
			continue
		}
		for _, event := range events {
			eventChan <- event
		}
	}
}

// fresh 按liveInterval/newsInterval两个独立周期轮询订阅。
func (c *Concern) fresh() concern.FreshFunc {
	return func(ctx context.Context, eventChan chan<- concern.Event) {
		liveTimer := time.NewTimer(liveInterval())
		defer liveTimer.Stop()
		newsTimer := time.NewTimer(newsInterval())
		defer newsTimer.Stop()
		for {
			select {
			case <-liveTimer.C:
				c.refreshType(Live, eventChan)
				liveTimer.Reset(liveInterval())
			case <-newsTimer.C:
				c.refreshType(News, eventChan)
				newsTimer.Reset(newsInterval())
			case <-ctx.Done():
				return
			}
		}
	}
}

// notifyGenerator 把事件转换成每个群的推送。
func (c *Concern) notifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, event concern.Event) (result []concern.Notify) {
		switch e := event.(type) {
		case *LiveInfo:
			result = append(result, &LiveNotify{
				GroupCode: groupCode,
				LiveInfo:  e,
			})
		case *NewsInfo:
			for _, sound := range e.Sounds {
				result = append(result, &NewsNotify{
					GroupCode: groupCode,
					UserID:    e.UserID,
					Name:      e.Name,
					Avatar:    e.Avatar,
					Sound:     sound,
				})
			}
		default:
			logger.Errorf("unknown EventType %+v", event.Type().String())
		}
		return
	}
}

func (c *Concern) Start() error {
	logger.WithFields(logrus.Fields{
		"live_interval":    liveInterval(),
		"news_interval":    newsInterval(),
		"profile_interval": getInterval("missevan.profileInterval", defaultProfileInterval, minProfileInterval),
		"request_spacing":  getRequestSpacing(),
		"authentication":   "not_required",
		"live_supported":   true,
		"news_supported":   true,
	}).Info("猫耳FM监听模块已启用")
	c.StateManager.UseFreshFunc(c.fresh())
	c.StateManager.UseNotifyGeneratorFunc(c.notifyGenerator())
	return c.StateManager.Start()
}

func (c *Concern) Stop() {
	logger.Trace("正在停止猫耳FM监听模块")
	c.StateManager.Stop()
}
