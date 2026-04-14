package acfun

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/cnxysoft/DDBOT-WSa/utils/expirable"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/MiraiGo-Template/utils"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/tidwall/buntdb"
	"golang.org/x/sync/errgroup"
)

var online bool
var logger = utils.GetModuleLogger("acfun-concern")

const (
	Live concern_type.Type = "live"
	News concern_type.Type = "news"
)

type Concern struct {
	*StateManager
	wg                     sync.WaitGroup
	stop                   chan interface{}
	notify                 chan<- concern.Notify
	cacheStartTs           int64
	attentionListExpirable *expirable.Expirable
}

func (c *Concern) Site() string {
	return Site
}

func (c *Concern) Types() []concern_type.Type {
	return []concern_type.Type{Live, News}
}

func (c *Concern) ParseId(s string) (interface{}, error) {
	return strconv.ParseInt(s, 10, 64)
}

func (c *Concern) Start() error {
	Init()
	c.UseNotifyGeneratorFunc(c.notifyGenerator())
	c.UseFreshFunc(c.fresh())
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.SyncSub()
		tick := time.Tick(time.Hour)
		for {
			select {
			case <-tick:
				c.SyncSub()
			case <-c.stop:
				return
			}
		}
	}()
	go func() {
		for msg := range eventbus.BusObj.Subscribe("bot_online") {
			if m, ok := msg.(bool); ok {
				if !online && m {
					c.cacheStartTs = time.Now().Unix()
					logger.Infof("BOT已上线，刷新A站订阅模块启动时间")
				}
				online = m
			}
			logger.Debugf("模块 ACFUN 收到：bot_online: %v", msg)
		}
	}()
	return c.StateManager.Start()
}

func (c *Concern) Stop() {
	logger.Trace("正在停止acfun concern")
	if c.stop != nil {
		close(c.stop)
	}
	logger.Trace("正在停止acfun StateManager")
	c.StateManager.Stop()
	logger.Trace("acfun StateManager已停止")
	c.wg.Wait()
	logger.Trace("acfun concern已停止")
}

func (c *Concern) ModifyUserRelation(uid int64, act int) (*FollowResponse, error) {
	var resp *FollowResponse
	var err error
	resp, err = SetFollow(uid, act)
	if err != nil {
		return nil, err
	}
	if resp.GetResult() != 0 {
		logger.WithField("code", resp.GetResult()).
			WithField("message", resp.GetErrorMsg()).
			WithField("act", act).
			WithField("uid", uid).
			Errorf("ModifyUserRelation error")
	} else {
		logger.WithField("uid", uid).WithField("act", act).Debug("modify relation")
	}
	return resp, nil
}

func (c *Concern) SyncSub() {
	defer logger.Debug("SyncSub done")
	resp, err := GetAttentionList()
	if err != nil {
		logger.Errorf("SyncSub error %v", err)
		return
	}
	var midSet = make(map[int64]bool)
	var attentionMidSet = make(map[int64]bool)
	_, _, _, err = c.StateManager.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool {
		midSet[id.(int64)] = true
		return true
	})

	if err != nil {
		logger.Errorf("SyncSub ListConcernState all error %v", err)
		return
	}
	for _, attention := range resp {
		uid, _ := strconv.ParseInt(attention.GetUserId(), 10, 64)
		attentionMidSet[uid] = true
	}

	var disableSub = false
	if config.GlobalConfig.GetBool("acfun.disableSub") {
		disableSub = true
	}

	var actType = ActSub
	for uid := range midSet {
		if uid == accountUid.Load() {
			continue
		}
		if _, found := attentionMidSet[uid]; !found {
			if disableSub {
				logger.Warnf("检测到存在未关注的订阅目标 UID:%v，同时禁用了A站自动关注，将无法推送该用户", uid)
				continue
			}
			resp, err := c.ModifyUserRelation(uid, actType)
			if err == nil {
				switch resp.GetResult() {
				case 0:
				default:
					logger.WithField("ModifyUserRelation Code", resp.GetResult()).
						WithField("ModifyUserRelation Message", resp.GetErrorMsg()).
						WithField("uid", uid).
						Errorf("ModifyUserRelation failed, remove concern")
					c.RemoveAllById(uid)
				}
			} else {
				logger.Errorf("ModifyUserRelation error %v", err)
			}
			time.Sleep(time.Second * 3)
			select {
			case <-c.stop:
				return
			default:
			}
		}
	}
}

func (c *Concern) notifyGenerator() concern.NotifyGeneratorFunc {
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
			notifies := NewConcernNewsNotify(groupCode, event, c)
			log.WithFields(localutils.GroupLogFields(groupCode)).
				WithField("Size", len(notifies)).Trace("news notify")
			for _, notify := range notifies {
				result = append(result, notify)
			}
		default:
			log.Errorf("unknown concern_type %v", ievent.Type().String())
		}
		return
	}
}

func (c *Concern) fresh() concern.FreshFunc {
	return func(ctx context.Context, eventChan chan<- concern.Event) {
		t := time.NewTimer(time.Second * 3)
		var interval time.Duration
		if config.GlobalConfig != nil {
			interval = config.GlobalConfig.GetDuration("acfun.interval")
		}
		if interval == 0 {
			interval = time.Second * 20
		}
		for {
			select {
			case <-t.C:
			case <-ctx.Done():
				return
			}
			var start = time.Now()
			var errGroup errgroup.Group

			errGroup.Go(func() error {
				defer func() { logger.WithField("cost", time.Now().Sub(start)).Tracef("watchCore live fresh done") }()

				_, ids, types, err := c.StateManager.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool {
					return p.ContainAny(Live)
				})
				if err != nil {
					logger.Errorf("ListConcernState error %v", err)
					return err
				}
				ids, types, err = c.GroupTypeById(ids, types)
				if err != nil {
					logger.Errorf("GroupTypeById error %v", err)
					return err
				}
				if len(ids) == 0 {
					// 没有订阅的话，就不要刷新了
					logger.Trace("no live concern, skip fresh")
					return nil
				}

				liveInfo, err := c.freshLiveInfo()
				if err != nil {
					return err
				}
				var liveInfoMap = make(map[int64]*LiveInfo)
				for _, info := range liveInfo {
					liveInfoMap[info.Uid] = info
				}

				sendLiveInfo := func(info *LiveInfo) {
					addLiveInfoErr := c.AddLiveInfo(info)
					if addLiveInfoErr != nil {
						// 如果因为系统原因add失败，会造成重复推送
						// 按照ddbot的原则，选择不推送，而非重复推送
						logger.WithField("uid", info.Uid).Errorf("add live info error %v", err)
					} else {
						eventChan <- info
					}
				}
				for _, id := range ids {
					uid := id.(int64)
					oldInfo, _ := c.GetLiveInfo(uid)
					if oldInfo == nil {
						// first live info
						if newInfo, found := liveInfoMap[uid]; found {
							newInfo.liveStatusChanged = true
							sendLiveInfo(newInfo)
						}
						continue
					}
					if !oldInfo.Living() {
						if newInfo, found := liveInfoMap[uid]; found {
							// notliving -> living
							newInfo.liveStatusChanged = true
							sendLiveInfo(newInfo)
						}
					} else {
						if newInfo, found := liveInfoMap[uid]; !found {
							// living -> notliving
							if count := c.IncNotLiveCount(uid); count < 3 {
								logger.WithField("uid", uid).WithField("name", oldInfo.UserInfo.Name).
									WithField("notlive_count", count).
									Debug("notlive counting")
								continue
							} else {
								logger.WithField("uid", uid).WithField("name", oldInfo.UserInfo.Name).
									Debug("notlive count done, notlive confirmed")
							}
							c.ClearNotLiveCount(uid)
							newInfo = &LiveInfo{
								UserInfo:          oldInfo.UserInfo,
								LiveId:            oldInfo.LiveId,
								Title:             oldInfo.Title,
								Cover:             oldInfo.Cover,
								StartTs:           oldInfo.StartTs,
								IsLiving:          false,
								liveStatusChanged: true,
							}
							sendLiveInfo(newInfo)
						} else {
							c.ClearNotLiveCount(uid)
							if newInfo.Title != oldInfo.Title {
								// live title change
								newInfo.liveTitleChanged = true
								sendLiveInfo(newInfo)
							}
						}
					}
				}
				return nil
			})

			errGroup.Go(func() error {
				defer func() {
					logger.WithField("cost", time.Now().Sub(start)).
						Tracef("watchCore dynamic fresh done")
				}()

				_, ids, types, err := c.StateManager.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool {
					return p.ContainAny(News)
				})
				if err != nil {
					logger.Errorf("ListConcernState error %v", err)
					return err
				}
				ids, types, err = c.GroupTypeById(ids, types)
				if err != nil {
					logger.Errorf("GroupTypeById error %v", err)
					return err
				}
				if len(ids) == 0 {
					// 没有订阅的话，就不要刷新了
					logger.Trace("no news concern, skip fresh")
					return nil
				}

				newsList, err := c.freshDynamicNew()
				if err != nil {
					logger.Errorf("freshDynamicNew failed %v", err)
					return err
				} else {
					for _, news := range newsList {
						eventChan <- news
					}
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
			t.Reset(interval)
		}
	}
}

func (c *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	var err error
	var uid = id.(int64)
	selfUid := accountUid.Load()
	var watchSelf = selfUid != 0 && selfUid == uid
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("id", id)

	err = c.StateManager.CheckGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}

	var liveInfo *LiveInfo
	var userInfo *UserInfo
	switch ctype {
	case Live:
		liveInfo, _ = c.GetLiveInfo(uid)
		userInfo, err = c.FindOrLoadUserInfo(uid, Live)
		if err != nil {
			log.Errorf("FindOrLoadUserInfo error %v", err)
			return nil, fmt.Errorf("查询用户信息失败 %v - %v", id, err)
		}
	case News:
		if !IsVerifyGiven() {
			return nil, fmt.Errorf("添加订阅失败 - 未配置A站")
		}
		userInfo, err = c.FindOrLoadUserInfo(uid, News)
		if err != nil {
			log.Errorf("FindOrLoadUserInfo error %v", err)
			return nil, fmt.Errorf("查询用户信息失败 %v - %v", id, err)
		}
	}

	if !c.EmitQueueEnabled() {
		if !watchSelf && ctype == News {
			oldCtype, err := c.StateManager.GetConcern(uid)
			if err != nil {
				log.Errorf("GetConcern error %v", err)
			} else if oldCtype.Empty() {
				if c.checkRelation(uid) {
					log.Infof("当前A站账户已关注该用户，跳过关注")
				} else {
					if cfg.GetAcfunDisableSub() {
						return nil, fmt.Errorf("关注用户失败 - 该用户未在关注列表内，请联系管理员")
					}
					var actType = ActSub
					resp, err := c.ModifyUserRelation(uid, actType)
					if err != nil {
						if err == ErrVerifyRequired {
							log.Errorf("ModifyUserRelation error %v", err)
							return nil, fmt.Errorf("关注用户失败 - 未配置A站")
						} else {
							log.WithField("action", actType).Errorf("ModifyUserRelation error %v", err)
							return nil, fmt.Errorf("关注用户失败 - 内部错误")
						}
					}
					if resp.GetResult() != 0 {
						log.Errorf("关注用户失败 %v - %v", resp.GetResult(), resp.GetErrorMsg())
						return nil, fmt.Errorf("关注用户失败 - %v", resp.GetErrorMsg())
					}
				}
			}
		} else if selfUid != 0 {
			log.Debug("正在订阅账号自己，跳过关注")
		}
	}

	_, err = c.StateManager.AddGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	err = c.StateManager.SetUidFirstTimestampIfNotExist(uid, time.Now().Add(-time.Second*30).Unix())
	if err != nil && !localdb.IsRollback(err) {
		log.Errorf("SetUidFirstTimestampIfNotExist failed %v", err)
	}
	if ctype.ContainAny(Live) {
		// 其他群关注了同一uid，并且推送过Living，那么给新watch的群也推一份
		if liveInfo != nil && liveInfo.Living() {
			if ctx.GetTarget().TargetType().IsGroup() {
				defer c.GroupWatchNotify(groupCode, uid)
			}
			if ctx.GetTarget().TargetType().IsPrivate() {
				defer ctx.Send(mmsg.NewText("检测到该用户正在直播，但由于您目前处于私聊模式，" +
					"因此不会在群内推送本次直播，将在该用户下次直播时推送"))
			}
		}
	}
	return concern.NewIdentity(userInfo.Uid, userInfo.GetName()), nil
}

func (c *Concern) Remove(ctx mmsg.IMsgCtx,
	groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	mid := id.(int64)
	var identityInfo concern.IdentityInfo
	var allCtype concern_type.Type
	err := c.StateManager.RWCoverTx(func(tx *buntdb.Tx) error {
		var err error
		identityInfo, _ = c.Get(mid)
		_, err = c.StateManager.RemoveGroupConcern(groupCode, mid, ctype)
		if err != nil {
			return err
		}
		allCtype, err = c.StateManager.GetConcern(mid)
		if err != nil {
			return err
		}
		// 如果已经没有watch live的了，此时应该把liveinfo删掉，否则会无法刷新到livelinfo
		// 如果此时liveinfo是living状态，则此状态会一直保留，下次watch时会以为在living错误推送
		if !allCtype.ContainAll(Live) {
			err = c.StateManager.DeleteLiveInfo(mid)
			if err == buntdb.ErrNotFound {
				err = nil
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && cfg.GetAcfunUnsub() && allCtype.Empty() {
		c.unsubUser(mid)
	}
	if identityInfo == nil {
		identityInfo = concern.NewIdentity(id, "unknown")
	}
	return identityInfo, err
}

func (c *Concern) unsubUser(mid int64) {
	resp, err := c.ModifyUserRelation(mid, ActUnsub)
	if err != nil {
		logger.Errorf("取消关注失败 - %v", err)
	} else if resp.GetResult() != 0 {
		logger.Errorf("取消关注失败 - %v - %v", resp.GetResult(), resp.GetErrorMsg())
	} else {
		logger.WithField("uid", mid).Info("取消关注成功")
	}
}

func (c *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	return c.FindUserInfo(id.(int64), false, Live)
}

func (c *Concern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

func (c *Concern) FindUserInfo(uid int64, load bool, ctype concern_type.Type) (*UserInfo, error) {
	if load {
		switch ctype {
		case Live:
			resp, err := LivePage(uid)
			if err != nil {
				return nil, err
			}
			userInfo := &UserInfo{
				Uid:      uid,
				Name:     resp.GetLiveInfo().GetUser().GetName(),
				Followed: int(resp.GetLiveInfo().GetUser().GetFanCountValue()),
				UserImg:  resp.GetLiveInfo().GetUser().GetHeadUrl(),
				LiveUrl:  LiveUrl(uid),
			}
			err = c.AddUserInfo(userInfo)
			if err != nil {
				return nil, err
			}
		case News:
			resp, err := GetUserInfo(uid)
			if err != nil {
				return nil, err
			}
			followed, _ := strconv.ParseInt(resp.GetProfile().GetFollowed(), 10, 64)
			userInfo := &UserInfo{
				Uid:      uid,
				Name:     resp.GetProfile().GetName(),
				Followed: int(followed),
				UserImg:  resp.GetProfile().GetHeadUrl(),
				LiveUrl:  LiveUrl(uid),
			}
			err = c.AddUserInfo(userInfo)
			if err != nil {
				return nil, err
			}
		}
	}
	return c.StateManager.GetUserInfo(uid)
}

func (c *Concern) FindOrLoadUserInfo(uid int64, ctype concern_type.Type) (*UserInfo, error) {
	userInfo, _ := c.FindUserInfo(uid, false, ctype)
	if userInfo == nil {
		return c.FindUserInfo(uid, true, ctype)
	}
	return userInfo, nil
}

func (c *Concern) GroupWatchNotify(groupCode, mid int64) {
	liveInfo, _ := c.GetLiveInfo(mid)
	if liveInfo.Living() {
		liveInfo.liveStatusChanged = true
		c.notify <- NewConcernLiveNotify(groupCode, liveInfo)
	}
}

func (c *Concern) freshDynamicNew() ([]*NewsInfo, error) {
	var start = time.Now()
	resp, err := GetFollowFeedV2()
	if err != nil {
		logger.Errorf("DynamicSvrDynamicNew error %v", err)
		return nil, err
	}
	var newsMap = make(map[int64][]*FeedItem)
	if resp.GetResult() != 0 {
		if resp.GetResult() == -401 {
			logger.Errorf("刷新动态列表失败，可能是cookie失效，将尝试重新获取cookie: %v", resp.GetErrorMsg())
			ClearCookieInfo(username)
			atomicVerifyInfo.Store(new(VerifyInfo))
		} else {
			logger.WithField("RespCode", resp.GetResult()).
				WithField("RespMsg", resp.GetErrorMsg()).
				Errorf("DynamicSvrDynamicNew failed")
		}
		return nil, fmt.Errorf("DynamicSvrDynamicNew failed %v - %v", resp.GetResult(), resp.GetErrorMsg())
	}
	var cards []*FeedItem
	cards = append(cards, resp.GetFeedList()...)
	logger.WithField("cost", time.Now().Sub(start)).Trace("freshDynamicNew cost 1")
	for _, card := range cards {
		uid := card.GetUser().GetUserId()
		if c.filterCard(card) {
			newsMap[uid] = append(newsMap[uid], card)
		}
	}
	logger.WithField("cost", time.Now().Sub(start)).Trace("freshDynamicNew cost 2")
	var result []*NewsInfo
	for uid, cards := range newsMap {
		userInfo, err := c.StateManager.GetUserInfo(uid)
		if err == buntdb.ErrNotFound {
			continue
		} else if err != nil {
			logger.WithField("uid", uid).Debugf("find user info error %v", err)
			continue
		}
		if len(cards) > 0 {
			// 如果更新了名字，有机会在这里捞回来
			userInfo.Name = cards[0].GetUser().GetUserName()
			userInfo.UserImg = cards[0].GetUser().GetUserHead()
		}
		result = append(result, NewNewsInfoWithDetail(userInfo, cards))
	}
	for _, news := range result {
		_ = c.AddUserInfo(&news.UserInfo)
	}
	logger.WithField("cost", time.Now().Sub(start)).
		WithField("NewsInfo Size", len(result)).
		Trace("freshDynamicNew done")
	return result, nil
}

func (c *Concern) freshLiveInfo() ([]*LiveInfo, error) {
	var liveInfos []*LiveInfo
	var pcursor string
	var count = 0
	for pcursor != "no_more" && count < 10 {
		count++
		resp, err := ApiChannelList(100, pcursor)
		if err != nil {
			logger.Errorf("freshLiveInfo error %v", err)
			return nil, err
		}
		pcursor = resp.GetChannelListData().GetPcursor()
		for _, liveItem := range resp.GetChannelListData().GetLiveList() {
			_uid, err := c.ParseId(liveItem.GetUser().GetId())
			if err != nil {
				logger.Errorf("parse id <%v> error %v", liveItem.GetUser().GetId(), err)
				continue
			}
			var cover string
			if len(liveItem.GetCoverUrls()) > 0 {
				cover = liveItem.GetCoverUrls()[0]
			}
			if len(cover) == 0 {
				cover = liveItem.GetUser().GetHeadUrl()
				if pos := strings.Index(cover, "?"); pos > 0 {
					cover = cover[:pos]
				}
			}
			uid := _uid.(int64)
			liveInfos = append(liveInfos, &LiveInfo{
				UserInfo: UserInfo{
					Uid:      uid,
					Name:     liveItem.GetUser().GetName(),
					Followed: int(liveItem.GetUser().GetFanCountValue()),
					UserImg:  liveItem.GetUser().GetHeadUrl(),
					LiveUrl:  LiveUrl(uid),
				},
				LiveId:   liveItem.GetLiveId(),
				Cover:    cover,
				Title:    liveItem.GetTitle(),
				StartTs:  liveItem.GetCreateTime(),
				IsLiving: true,
			})
		}
	}
	if count >= 10 {
		logger.Errorf("ACFUN刷新直播状态分页溢出，是真的有这么多直播吗？如果是真的有这么多直播，可能acfun已经橄榄blive了")
	}
	return liveInfos, nil
}

func (c *Concern) checkRelation(mid int64) bool {
	var atr = c.attentionListExpirable.Do()
	if atr == nil {
		return false
	}
	var matr = atr.(map[int64]interface{})
	if _, found := matr[mid]; found {
		return true
	} else {
		return false
	}
}

func (c *Concern) filterCard(card *FeedItem) bool {
	uid := card.GetUser().GetUserId()
	// 应该用dynamic_id_str
	// 但好像已经没法保持向后兼容同时改动了
	// 只能相信概率论了，出问题的概率应该比较小，出问题会导致推送丢失
	replaced, err := c.MarkDynamicId(card.GetResourceId())
	if err != nil {
		logger.WithField("uid", uid).
			WithField("dynamicId", card.GetMoment().GetMomentId()).
			Errorf("MarkDynamicId error %v", err)
		return false
	}
	if replaced {
		return false
	}
	var tsLimit int64
	if cfg.GetAcfunOnlyOnlineNotify() {
		tsLimit = c.cacheStartTs
	} else {
		tsLimit, err = c.StateManager.GetUidFirstTimestamp(uid)
		if err != nil {
			return true
		}
	}
	if card.GetCreateTime() < tsLimit {
		logger.WithField("uid", uid).
			WithField("dynamicId", card.GetMoment().GetMomentId()).
			Trace("past news skip")
		return false
	}
	return true
}

func NewConcern(notifyChan chan<- concern.Notify) *Concern {
	c := &Concern{
		notify:       notifyChan,
		stop:         make(chan interface{}),
		cacheStartTs: time.Now().Unix(),
		attentionListExpirable: expirable.NewExpirable(time.Second*20, func() interface{} {
			var m = make(map[int64]interface{})
			resp, err := GetAttentionList()
			if err != nil {
				logger.Errorf("GetAttentionList error %v", err)
				return m
			}
			if len(resp) == 0 {
				logger.Error("GetAttentionList error <nil follow list>")
				return m
			}
			for _, id := range resp {
				uid, _ := strconv.ParseInt(id.GetUserId(), 10, 64)
				m[uid] = struct{}{}
			}
			return m
		}),
	}
	c.StateManager = NewStateManager(c)
	return c
}
