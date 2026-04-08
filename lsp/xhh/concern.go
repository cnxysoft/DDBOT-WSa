package xhh

import (
	"errors"
	"fmt"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/tidwall/buntdb"
)

var online bool

type Concern struct {
	*StateManager
	cacheStartTs int64
	smidv2       string
}

func (c *Concern) Site() string {
	return Site
}

func (c *Concern) Types() []concern_type.Type {
	return []concern_type.Type{News}
}

func (c *Concern) ParseId(s string) (interface{}, error) {
	return s, nil
}

func (c *Concern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

func (c *Concern) Start() error {
	// 初始化 smidv2，优先使用配置文件的 token，否则使用持久化的或生成新的
	smidv2, err := GetSmidV2()
	if err != nil {
		logger.Warnf("初始化 smidv2 失败: %v", err)
	}
	c.smidv2 = smidv2
	logger.Infof("小黑盒启动成功，smidv2: %s", smidv2[:20]+"...")

	// 使用 EmitQueue 进行轮询，间隔由 heybox.interval 配置控制
	c.StateManager.UseEmitQueueWithSiteInterval("heybox")
	c.StateManager.UseFreshFunc(c.EmitQueueFresher(func(p concern_type.Type, id interface{}) ([]concern.Event, error) {
		userid := id.(string)
		if p.ContainAny(News) {
			newsInfo, err := c.freshNews(userid)
			if err != nil {
				return nil, err
			}
			if len(newsInfo.Moments) == 0 {
				return nil, nil
			}
			return []concern.Event{newsInfo}, nil
		}
		return nil, nil
	}))
	c.StateManager.UseNotifyGeneratorFunc(c.notifyGenerator())
	go func() {
		for msg := range eventbus.BusObj.Subscribe("bot_online") {
			if m, ok := msg.(bool); ok {
				if !online && m {
					c.cacheStartTs = time.Now().Unix()
					logger.Info("BOT已上线，刷新小黑盒订阅模块启动时间")
				}
				online = m
			}
			logger.Debugf("模块 XHH 收到：bot_online: %v", msg)
		}
	}()
	return c.StateManager.Start()
}

func (c *Concern) Stop() {
	logger.Tracef("正在停止%v concern", Site)
	logger.Tracef("正在停止%v StateManager", Site)
	c.StateManager.Stop()
	logger.Tracef("%v StateManager 已停止", Site)
	logger.Tracef("%v concern 已停止", Site)
}

func (c *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	userid := _id.(string)
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("userid", userid)

	err := c.StateManager.CheckGroupConcern(groupCode, userid, ctype)
	if err != nil {
		return nil, err
	}

	info, err := c.FindOrLoadUserInfo(userid)
	if err != nil {
		log.Errorf("FindOrLoadUserInfo error %v", err)
		return nil, fmt.Errorf("查询用户信息失败 %v - %v", userid, err)
	}

	if r, _ := c.GetStateManager().GetConcern(userid); r.Empty() {
		eventsResp, err := GetProfileEvents(c.smidv2, userid)
		if err != nil {
			log.Errorf("GetProfileEvents error %v", err)
			return nil, fmt.Errorf("获取用户动态失败 - %v", err)
		}
		if eventsResp.GetOk() != 1 {
			log.WithField("respOk", eventsResp.GetOk()).Errorf("GetProfileEvents not ok")
			return nil, fmt.Errorf("获取用户动态失败")
		}
		// 第一次手动塞一下时间戳，以此来过滤旧的动态
		err = c.AddNewsInfo(&NewsInfo{
			UserInfo:     info,
			LatestNewsTs: time.Now().Unix(),
		})
		if err != nil {
			log.Errorf("AddNewsInfo error %v", err)
			return nil, fmt.Errorf("内部错误")
		}
	}

	_, err = c.StateManager.AddGroupConcern(groupCode, userid, ctype)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (c *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	userid := _id.(string)
	identity, _ := c.Get(userid)
	_, err := c.StateManager.RemoveGroupConcern(groupCode, userid, ctype)
	if identity == nil {
		identity = concern.NewIdentity(_id, "unknown")
	}
	err = c.RemoveUserInfo(userid)
	if err != nil {
		logger.Errorf("removeUserInfo error %v", err)
	}
	err = c.RemoveNewsInfo(userid)
	if err != nil {
		logger.Errorf("removeNewsInfo error %v", err)
	}
	return identity, err
}

func (c *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	return c.GetUserInfo(id.(string))
}

func (c *Concern) freshNews(userid string) (*NewsInfo, error) {
	log := logger.WithField("userid", userid)

	eventsResp, err := GetProfileEvents(c.smidv2, userid)
	if err != nil {
		log.Errorf("GetProfileEvents error %v，尝试替换 smidv2", err)
		// 请求失败时生成新的 smidv2 替换
		newSmidV2, _, refreshErr := GetAndRefreshSmidV2(c.smidv2)
		if refreshErr != nil {
			log.Errorf("GetAndRefreshSmidV2 error %v", refreshErr)
			return nil, err
		}
		c.smidv2 = newSmidV2
		log.Infof("smidv2 已替换，新值前20位: %s，尝试重新请求", newSmidV2[:20])
		eventsResp, err = GetProfileEvents(c.smidv2, userid)
		if err != nil {
			log.Errorf("GetProfileEvents (after smidv2 refresh) error %v", err)
			return nil, err
		}
		if eventsResp.GetOk() != 1 {
			log.WithField("respOk", eventsResp.GetOk()).Errorf("GetProfileEvents not ok")
			return nil, errors.New("GetProfileEvents not success")
		}
	}
	if eventsResp.GetOk() != 1 {
		log.WithField("respOk", eventsResp.GetOk()).Errorf("GetProfileEvents not ok")
		return nil, errors.New("GetProfileEvents not success")
	}

	var lastTs int64
	var newsInfo = &NewsInfo{}
	oldNewsInfo, err := c.GetNewsInfo(userid)
	if err == buntdb.ErrNotFound {
		lastTs = time.Now().Unix()
		newsInfo.LatestNewsTs = lastTs
		// 加载用户信息
		userInfo, err := c.FindOrLoadUserInfo(userid)
		if err != nil {
			log.Errorf("FindOrLoadUserInfo error %v", err)
			return nil, fmt.Errorf("获取用户信息失败")
		}
		newsInfo.UserInfo = userInfo
	} else {
		lastTs = oldNewsInfo.LatestNewsTs
		newsInfo.LatestNewsTs = lastTs
		newsInfo.UserInfo = oldNewsInfo.UserInfo
	}

	for _, moment := range eventsResp.Result.Moments {
		if pass, t := c.filterMoment(moment, lastTs); pass {
			newsInfo.Moments = append(newsInfo.Moments, moment)
			if t > newsInfo.LatestNewsTs {
				newsInfo.LatestNewsTs = t
			}
		}
	}

	err = c.AddNewsInfo(newsInfo)
	if err != nil {
		log.Errorf("AddNewsInfo error %v", err)
		return nil, err
	}
	return newsInfo, nil
}

func (c *Concern) filterMoment(moment *Moment, lastTs int64) (bool, int64) {
	// 使用linkid作为唯一ID进行去重
	replaced, err := c.MarkMomentId(fmt.Sprintf("%d", moment.Linkid))
	if err != nil {
		logger.WithField("userid", moment.Userid).
			WithField("linkid", moment.Linkid).
			Errorf("MarkMomentId error %v", err)
		return false, 0
	}
	if replaced {
		return false, 0
	}

	var tsLimit int64
	if cfg.GetHeyboxOnlyOnlineNotify() {
		tsLimit = c.cacheStartTs
	} else {
		tsLimit = 0
	}

	t := moment.CreateAt
	if t == 0 {
		t = moment.ModifyAt
	}

	if t == 0 {
		logger.WithField("linkid", moment.Linkid).Warnf("moment has no valid timestamp")
		return false, 0
	}

	if t < lastTs || t < tsLimit {
		return false, 0
	}
	return true, t
}

func (c *Concern) notifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, ievent concern.Event) []concern.Notify {
		var result []concern.Notify
		switch news := ievent.(type) {
		case *NewsInfo:
			if len(news.Moments) > 0 {
				for _, n := range NewConcernNewsNotify(groupCode, news) {
					result = append(result, n)
				}
			}
		}
		return result
	}
}

func (c *Concern) FindUserInfo(userid string, load bool) (*UserInfo, error) {
	if load {
		eventsResp, err := GetProfileEvents(c.smidv2, userid)
		if err != nil {
			logger.WithField("userid", userid).Errorf("GetProfileEvents error %v", err)
			return nil, err
		}
		if eventsResp.GetOk() != 1 {
			logger.WithField("respOk", eventsResp.GetOk()).Errorf("GetProfileEvents not ok")
			return nil, errors.New("接口请求失败")
		}
		if len(eventsResp.Result.Moments) > 0 {
			moment := eventsResp.Result.Moments[0]
			err = c.AddUserInfoWithKey(userid, moment.User)
			if err != nil {
				logger.WithField("userid", userid).Errorf("AddUserInfoWithKey error %v", err)
			}
			return moment.User, nil
		}
	}
	return c.GetUserInfo(userid)
}

func (c *Concern) FindOrLoadUserInfo(userid string) (*UserInfo, error) {
	info, _ := c.FindUserInfo(userid, false)
	if info == nil {
		return c.FindUserInfo(userid, true)
	}
	return info, nil
}

func NewConcern(notify chan<- concern.Notify) *Concern {
	c := &Concern{
		StateManager: NewStateManager(notify),
	}
	return c
}
