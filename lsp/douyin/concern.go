package douyin

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http/cookiejar"
	"time"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/Sora233/MiraiGo-Template/config"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"golang.org/x/sync/errgroup"

	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

const (
	// 这个名字是日志中的名字，如果不知道取什么名字，可以和Site一样
	ConcernName = "douyin-concern"

	// 插件支持的网站名
	Site = "douyin"
	// 这个插件支持的订阅类型可以像这样自定义，然后在 Types 中返回
	Live concern_type.Type = "live"
	News concern_type.Type = "news"
	// 当像这样定义的时候，支持 /watch -s mysite -t type1 id
	// 当实现的时候，请修改上面的定义
	// API Base URL
	BaseHost          = "https://www.douyin.com"
	BaseLiveHost      = "https://live.douyin.com"
	ErrNotFound       = "not found"
	PathGetUserInfo   = "/user/"
	CompactExpireTime = time.Minute * 60
)

var (
	logger   = utils.GetModuleLogger(ConcernName)
	Cookie   *cookiejar.Jar
	online   bool
	BasePath = map[string]string{
		PathGetUserInfo:         BaseHost,
		PathCheckUserLiveStatus: BaseLiveHost,
		PathGetUserPosts:        BaseHost,
		PathWebcastRoomWebEnter: BaseLiveHost,
		PathWebcastUserProfile:  BaseLiveHost,
	}
)

type StateManager struct {
	*concern.StateManager
	*ExtraKey
	concern *Concern
}

// GetGroupConcernConfig 重写 concern.StateManager 的GetGroupConcernConfig方法，让我们自己定义的 GroupConcernConfig 生效
func (d *StateManager) GetGroupConcernConfig(groupCode int64, id interface{}) concern.IConfig {
	return NewGroupConcernConfig(d.StateManager.GetGroupConcernConfig(groupCode, id), d.concern)
}

func (c *StateManager) SetUidFirstTimestampIfNotExist(uid string, timestamp int64) error {
	return c.SetInt64(c.UidFirstTimestamp(uid), timestamp, localdb.SetNoOverWriteOpt())
}

func (c *StateManager) UnsetUidFirstTimestamp(uid string) error {
	_, err := c.Delete(c.UidFirstTimestamp(uid))
	return err
}

func (c *StateManager) GetUidFirstTimestamp(uid string) (timestamp int64, err error) {
	return c.GetInt64(c.UidFirstTimestamp(uid))
}

func (c *StateManager) SetGroupCompactMarkIfNotExist(groupCode int64, compactKey string) error {
	return c.Set(c.CompactMarkKey(groupCode, compactKey), "",
		localdb.SetExpireOpt(CompactExpireTime), localdb.SetNoOverWriteOpt())
}

func (c *StateManager) SetNotifyMsg(notifyKey string, msg *message.GroupMessage) error {
	tmp := &message.GroupMessage{
		Id:        msg.Id,
		GroupCode: msg.GroupCode,
		Sender:    msg.Sender,
		Time:      msg.Time,
		Elements: localutils.MessageFilter(msg.Elements, func(e message.IMessageElement) bool {
			return e.Type() == message.Text || e.Type() == message.Image
		}),
	}
	value, err := localutils.SerializationGroupMsg(tmp)
	if err != nil {
		return err
	}
	return c.Set(c.NotifyMsgKey(tmp.GroupCode, notifyKey), value,
		localdb.SetExpireOpt(CompactExpireTime), localdb.SetNoOverWriteOpt())
}

func (c *StateManager) GetNotifyMsg(groupCode int64, notifyKey string) (*message.GroupMessage, error) {
	value, err := c.Get(c.NotifyMsgKey(groupCode, notifyKey))
	if err != nil {
		return nil, err
	}
	return localutils.DeserializationGroupMsg(value)
}

func (c *StateManager) MarkDynamicId(dynamic string) (bool, error) {
	var isOverwrite bool
	err := c.Set(c.DynamicIdKey(dynamic), "",
		localdb.SetExpireOpt(time.Hour*120), localdb.SetGetIsOverwriteOpt(&isOverwrite))
	return isOverwrite, err
}

type Concern struct {
	*StateManager
	notify       chan<- concern.Notify
	cacheStartTs int64
}

func (d *Concern) Site() string {
	return Site
}

func (d *Concern) Types() []concern_type.Type {
	return []concern_type.Type{Live, News}
}

func (d *Concern) ParseId(s string) (interface{}, error) {
	// 在这里解析id
	// 此处返回的id类型，即是其他地方id interface{}的类型
	// 其他所有地方的id都由此函数生成
	// 推荐在string 或者 int64类型中选择其一
	// 如果订阅源有uid等数字唯一标识，请选择int64，如 bilibili
	// 如果订阅源有数字并且有字符，请选择string， 如 douyu
	return s, nil
}

func (d *Concern) FindUserInfo(id string, refresh bool) (*UserInfo, error) {
	if refresh {
		info, err := GetUserInfo(id)
		if err != nil {
			return nil, err
		}
		err = d.AddUserInfo(info)
		if err != nil {
			return nil, err
		}
	}
	return d.GetUserInfo(id)
}

func (d *Concern) FindOrLoadUserInfo(uid string) (*UserInfo, error) {
	info, _ := d.FindUserInfo(uid, false)
	if info == nil {
		return d.FindUserInfo(uid, true)
	}
	return info, nil
}

func (d *Concern) GetUserInfo(uid string) (*UserInfo, error) {
	var userInfo *UserInfo
	err := d.GetJson(d.UserInfoKey(uid), &userInfo)
	if err != nil {
		return nil, err
	}
	return userInfo, nil
}

func (d *Concern) AddUserInfo(info *UserInfo) error {
	if info == nil {
		return errors.New("<nil userInfo>")
	}
	return d.SetJson(d.UserInfoKey(info.SecUid), info)
}

func (d *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	var err error
	var uid = id.(string)
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("id", id)
	// 这里是添加订阅的函数
	// 可以使 c.StateManager.AddGroupConcern(groupCode, id, ctype) 来添加这个订阅
	// 通常在添加订阅前还需要通过id访问网站上的个人信息页面，来确定id是否存在，是否可以正常订阅
	err = d.StateManager.CheckGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	liveInfo, _ := d.GetUserInfo(uid)

	info, err := d.FindOrLoadUserInfo(uid)
	if err != nil {
		log.Errorf("FindOrLoadUserInfo error %v", err)
		return nil, fmt.Errorf("查询用户信息失败 %v - %v", id, err)
	}
	if ctype.ContainAny(Live) {
		if info.WebRoomId == "" {
			return nil, fmt.Errorf("用户 %v 未绑定直播房间", id)
		}
	}
	_, err = d.GetStateManager().AddGroupConcern(groupCode, id, ctype)
	if err != nil {
		log.Errorf("AddGroupConcern error %v", err)
		return nil, fmt.Errorf("关注用户失败 - 内部错误")
	}
	err = d.StateManager.SetUidFirstTimestampIfNotExist(uid, time.Now().Add(-time.Second*30).Unix())
	if err != nil && !localdb.IsRollback(err) {
		log.Errorf("SetUidFirstTimestampIfNotExist failed %v", err)
	}
	if ctype.ContainAny(Live) {
		// 其他群关注了同一uid，并且推送过Living，那么给新watch的群也推一份
		if liveInfo != nil {
			if ctx.GetTarget().TargetType().IsGroup() {
				defer d.GroupWatchNotify(groupCode, uid)
			}
			if ctx.GetTarget().TargetType().IsPrivate() {
				defer ctx.Send(mmsg.NewText("检测到该用户正在直播，但由于您目前处于私聊模式，" +
					"因此不会在群内推送本次直播，将在该用户下次直播时推送"))
			}
		}
	}
	return info, nil
}

func (d *Concern) GroupWatchNotify(groupCode int64, mid string) {
	userInfo, _ := d.GetUserInfo(mid)
	if userInfo.WebRoomId != "" {
		var liveInfo *LiveInfo
		liveInfo.IsLiving = true
		liveInfo.UserInfo = *userInfo
		d.notify <- NewConcernLiveNotify(groupCode, liveInfo)
	}
}

func (d *Concern) removeUserInfo(id string) error {
	_, err := d.Delete(d.UserInfoKey(id), buntdb.IgnoreNotFoundOpt())
	return err
}

func (d *Concern) removeCurrentLive(id string) error {
	_, err := d.Delete(d.CurrentLiveKey(id), buntdb.IgnoreNotFoundOpt())
	return err
}

func (d *Concern) removeFresh(id string) error {
	_, err := d.Delete(d.FreshKey(id), buntdb.IgnoreNotFoundOpt())
	return err
}

func (d *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	// 大部分时候简单的删除即可
	// 如果还有更复杂的逻辑可以自由实现
	identity, _ := d.Get(id)
	_, err := d.GetStateManager().RemoveGroupConcern(groupCode, id.(string), ctype)
	if err != nil {
		return nil, err
	}

	if err = d.removeCurrentLive(id.(string)); err != nil {
		if err != errors.New("not found") {
			logger.WithError(err).Errorf("remove CurrentLive error")
		} else {
			err = nil
		}
	}

	if err = d.removeUserInfo(id.(string)); err != nil {
		if err != errors.New("not found") {
			logger.WithError(err).Errorf("remove UserInfo error")
		} else {
			err = nil
		}
	}

	if err = d.removeFresh(id.(string)); err != nil {
		if err != errors.New("not found") {
			logger.WithError(err).Errorf("remove Fresh error")
		} else {
			err = nil
		}
	}

	if identity == nil {
		identity = concern.NewIdentity(id, "unknown")
	}
	return identity, err
}

func (d *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	// 查看一个订阅的信息
	// 通常是查看数据库中是否有id的信息，如果没有可以去网页上获取
	usrInfo, err := d.GetUserInfo(id.(string))
	if err != nil {
		return nil, errors.New("GetUserInfo error")
	}
	return concern.NewIdentity(usrInfo.SecUid, usrInfo.NikeName), nil
}

func (d *Concern) notifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, ievent concern.Event) (result []concern.Notify) {
		log := ievent.Logger()
		switch event := ievent.(type) {
		case *LiveInfo:
			notify := NewConcernLiveNotify(groupCode, event)
			result = append(result, notify)
			if event.Living() {
				log.WithFields(localutils.GroupLogFields(groupCode)).Trace("living notify")
			} else {
				log.WithFields(localutils.GroupLogFields(groupCode)).Trace("noliving notify")
			}
		case *NewsInfo:
			notifies := NewConcernNewsNotify(groupCode, event, d)
			log.WithFields(localutils.GroupLogFields(groupCode)).
				WithField("Size", len(notifies)).Trace("news notify")
			for _, notify := range notifies {
				result = append(result, notify)
			}
		default:
			logger.Errorf("unknown EventType %+v", event.Type().String())
		}
		return
	}
}

// 新增辅助函数获取刷新间隔
func getRefreshInterval() time.Duration {
	if config.GlobalConfig != nil {
		interval := config.GlobalConfig.GetDuration("douyin.interval")
		if interval > 0 {
			return interval
		}
	}
	return time.Second * 30
}

func (d *Concern) fresh() concern.FreshFunc {
	return func(ctx context.Context, eventChan chan<- concern.Event) {
		interval := getRefreshInterval()
		ti := time.NewTimer(time.Second * 3)
		defer ti.Stop() // 确保定时器资源释放

		for {
			select {
			case <-ti.C:
			case <-ctx.Done():
				return
			}
			var start = time.Now()
			var errGroup errgroup.Group

			errGroup.Go(func() error {
				defer func() {
					logger.WithField("cost", time.Since(start)).
						Tracef("watchCore live fresh done")
				}()
				_, ids, _, _ := d.StateManager.ListConcernState(func(g int64, id interface{}, p concern_type.Type) bool { return p.ContainAll(Live) })
				for _, userId := range ids {
					events, err := d.freshInfo(Live, userId)
					if err != nil {
						continue
					}
					for _, e := range events {
						eventChan <- e
					}
					time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
				}
				return nil
			})

			errGroup.Go(func() error {
				defer func() {
					logger.WithField("cost", time.Since(start)).
						Tracef("watchCore dynamic fresh done")
				}()
				_, ids, _, _ := d.StateManager.ListConcernState(func(g int64, id interface{}, p concern_type.Type) bool { return p.ContainAll(News) })
				for _, userId := range ids {
					events, err := d.freshInfo(News, userId)
					if err != nil {
						continue
					}
					for _, e := range events {
						eventChan <- e
					}
					time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
				}
				return nil
			})

			err := errGroup.Wait()
			end := time.Now()
			if err == nil {
				logger.WithField("cost", end.Sub(start)).Tracef("watchCore loop done")
			} else {
				logger.WithField("cost", end.Sub(start)).Errorf("watchCore error %v", err)
			}
			ti.Reset(interval)
		}
	}
}

func (d *Concern) freshInfo(ctype concern_type.Type, id interface{}) ([]concern.Event, error) {
	var start = time.Now()
	var result []concern.Event
	userId := id.(string)
	usrInfo, err := d.FindOrLoadUserInfo(userId)
	if err != nil {
		logger.Errorf("查找用户信息失败：%v", err)
	}
	if ctype.ContainAll(Live) {
		isLive, err := FreshLiveStatus(usrInfo.Uid)
		if err != nil {
			return nil, err
		}
		oldIsLive, err := d.GetCurrentLive(userId)
		if err != nil && err.Error() != ErrNotFound {
			return nil, err
		}
		oldFreshTime, err := d.GetFreshTime(userId)
		if err != nil && err.Error() != ErrNotFound {
			return nil, err
		}
		if oldIsLive != isLive {
			err = d.SetCurrentLive(userId, isLive)
			if err != nil {
				logger.Errorf("内部错误 - 推送状态更新失败：%v", err)
				return nil, err
			}
			if time.Since(time.Unix(oldFreshTime, 0)) < 30*time.Minute || oldFreshTime == 0 {
				live := &LiveInfo{
					UserInfo:          *usrInfo,
					IsLiving:          isLive,
					liveStatusChanged: true,
				}
				roomData, err := GetRoomData(usrInfo.GetRoomId())
				if err != nil {
					logger.Warnf("内部错误 - 获取直播间数据失败：%v", err)
				}
				if roomData != nil {
					live.LiveTitle = roomData.GetData().GetData()[0].GetTitle()
					live.Cover = roomData.GetData().GetData()[0].GetCover().GetUrlList()[0]
				}
				result = append(result, live)
			}
		}
		err = d.SetFreshTime(userId, time.Now())
		if err != nil {
			logger.Errorf("内部错误 - 刷新时间更新失败：%v", err)
			return nil, err
		}
		err = d.AddUserInfo(usrInfo)
		if err != nil {
			logger.Errorf("内部错误 - 用户信息更新失败：%v", err)
			return nil, err
		}
	}
	if ctype.ContainAll(News) {
		resp, err := GetPosts(usrInfo.SecUid)
		if err != nil {
			return nil, err
		}
		if resp.GetStatusCode() != 0 {
			logger.WithField("RespCode", resp.GetStatusCode()).
				Errorf("DynamicSvrDynamicNew failed")
			return nil, fmt.Errorf("DynamicSvrDynamicNew failed %v", resp.GetStatusCode())
		}
		var resCards []*UserPostsResponse_AwemeList
		logger.WithField("cost", time.Since(start)).Trace("freshDynamicNew cost 1")
		for i, card := range resp.GetAwemeList() {
			if i == 0 {
				usrInfo.NikeName = card.GetAuthor().GetNickname()
			}
			// 遇到审核中的视频就跳过
			if isUnderReview(card.GetStatus()) {
				continue
			}
			if d.filterCard(card) {
				resCards = append(resCards, card)
			}
		}
		result = append(result, NewNewsInfoWithDetail(usrInfo, resCards))
		logger.WithField("cost", time.Since(start)).
			WithField("NewsInfo Size", len(result)).
			Trace("freshDynamicNew done")
	}
	err = d.AddUserInfo(usrInfo)
	if err != nil {
		logger.Errorf("内部错误 - 用户信息更新失败：%v", err)
		return nil, err
	}
	return result, nil
}

func (d *Concern) SetFreshTime(id string, ts time.Time) error {
	return d.SetInt64(d.FreshKey(id), ts.Unix())
}
func (d *Concern) GetFreshTime(id string) (int64, error) {
	return d.GetInt64(d.FreshKey(id))
}
func (d *Concern) SetCurrentLive(id string, j interface{}) error {
	err := d.SetJson(d.CurrentLiveKey(id), j)
	if err != nil {
		return err
	}
	return nil
}
func (d *Concern) GetCurrentLive(id string) (bool, error) {
	var status bool
	err := d.GetJson(d.CurrentLiveKey(id), &status)
	if err != nil {
		return false, err
	}
	return status, nil
}

func SetRequestOptions() []requests.Option {
	return []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second * 10),
		requests.AddUAOption(UserAgent),
		requests.RequestAutoHostOption(),
		requests.CookieOption("__ac_signature", AcSignature),
		requests.CookieOption("__ac_nonce", AcNonce),
		requests.CookieOption("sessionid", SessionId),
		requests.HeaderOption("Connection", "keep-alive"),
		requests.HeaderOption("Accept", "*/*"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br, zstd"),
		requests.HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"),
		requests.RetryOption(3),
		requests.WithCookieJar(Cookie),
	}
}

func (d *Concern) Start() error {
	// 以用户设置覆盖默认设置
	setCookies()
	if Stop {
		logger.Warn("抖音Cookie未设置，将关闭抖音推送功能。")
		return nil
	}
	// 如果需要启用轮询器，可以使用下面的方法
	//d.UseEmitQueue()
	// 下面两个函数是订阅的关键，需要实现，请阅读文档
	d.StateManager.UseFreshFunc(d.fresh())
	d.StateManager.UseNotifyGeneratorFunc(d.notifyGenerator())
	go func() {
		for msg := range eventbus.BusObj.Subscribe("bot_online") {
			if m, ok := msg.(bool); ok {
				if !online && m {
					d.cacheStartTs = time.Now().Unix()
					logger.Info("BOT已上线，刷新抖音订阅模块启动时间")
				}
				online = m
			}
			logger.Debugf("模块 DOUYIN 收到：bot_online: %v", msg)
		}
	}()
	return d.StateManager.Start()
}

func (d *Concern) Stop() {
	logger.Tracef("正在停止%v concern", Site)
	logger.Tracef("正在停止%v StateManager", Site)
	d.StateManager.Stop()
	logger.Tracef("%v StateManager已停止", Site)
	logger.Tracef("%v concern已停止", Site)
}

func (d *Concern) GetStateManager() concern.IStateManager {
	return d.StateManager
}

func (c *Concern) filterCard(card *UserPostsResponse_AwemeList) bool {
	uid := card.GetAuthor().GetSecUid()
	// 应该用dynamic_id_str
	// 但好像已经没法保持向后兼容同时改动了
	// 只能相信概率论了，出问题的概率应该比较小，出问题会导致推送丢失
	replaced, err := c.MarkDynamicId(card.GetAwemeId())
	if err != nil {
		logger.WithField("uid", uid).
			WithField("dynamicId", card.GetAwemeId()).
			Errorf("MarkDynamicId error %v", err)
		return false
	}
	if replaced {
		return false
	}
	var tsLimit int64
	if cfg.GetDouyinOnlyOnlineNotify() {
		tsLimit = c.cacheStartTs
	} else {
		tsLimit, err = c.StateManager.GetUidFirstTimestamp(uid)
		if err != nil {
			return true
		}
	}
	if int64(card.GetCreateTime()) < tsLimit {
		logger.WithField("uid", uid).
			WithField("dynamicId", card.GetAwemeId()).
			Trace("past news skip")
		return false
	}
	return true
}

func NewConcern(notifyChan chan<- concern.Notify) *Concern {
	// 默认是string格式的id
	c := &Concern{notify: notifyChan}
	sm := &StateManager{StateManager: concern.NewStateManagerWithStringID(Site, notifyChan), concern: c, ExtraKey: NewExtraKey()}
	// 如果要使用int64格式的id，可以用下面的
	c.StateManager = sm
	return c
}
