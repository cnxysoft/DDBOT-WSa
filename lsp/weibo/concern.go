package weibo

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/tidwall/buntdb"
)

var online bool
var logger = utils.GetModuleLogger("weibo-concern")

type Concern struct {
	*StateManager
	cacheStartTs int64
}

func (c *Concern) Site() string {
	return Site
}

func (c *Concern) Types() []concern_type.Type {
	return []concern_type.Type{News}
}

func (c *Concern) ParseId(s string) (interface{}, error) {
	return strconv.ParseInt(s, 10, 64)
}

func (c *Concern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

func (c *Concern) Start() error {
	mode := cfg.GetWeiboMode()
	isGuest := strings.EqualFold(mode, "guest")
	sub := ""
	if !isGuest {
		sub = GetSettingCookie()
		if sub == "" {
			if GetQRLoginEnable() {
				logger.Info("检测到 weibo.sub 为空，已启用 weibo.qrlogin，开始扫码登录以获取 SUB ...")
				obtained, err := RunQRLogin(QRLoginOption{OutputDir: ".", AutoOpen: true})
				if err != nil {
					logger.Errorf("扫码登录获取微博SUB失败: %v", err)
					logger.Warn("微博Cookie未设置，将关闭微博推送功能。")
					return nil
				}
				sub = obtained
				logger.Infof("扫码登录成功，已获取 SUB。请写入 application.yaml -> weibo.sub 以便下次启动：%s", sub)
			} else {
				logger.Warn("微博Cookie未设置，将关闭微博推送功能。开启 weibo.qrlogin 可自动扫码获取。")
				return nil
			}
		}
	}
	freshCookieOpt(sub)

	// 如果启用了 Cookie 刷新 API，启动自动监控
	if cfg.GetWeiboCookieRefreshEnable() {
		StartCookieRefreshMonitor(sub)
	}

	if !isGuest {
		// 测试微博cookie是否有效，并显示登录信息
		go func() {
			// 等待cookie刷新完成
			time.Sleep(2 * time.Second)

			// 微博没有直接获取当前登录用户信息的API
			// 通过访问一个测试用户页面来验证cookie有效性
			testUid := int64(5462373877) // 捞穹苍的信息试试[doge]
			profileResp, err := ApiContainerGetIndexProfile(testUid)
			if err != nil {
				logger.Errorf("微博Cookie验证失败 - %v，微博功能可能无法正常使用", err)
				return
			}

			if profileResp.GetOk() != 1 {
				logger.Errorf("微博Cookie验证失败 - 接口返回错误码：%v，微博功能可能无法正常使用", profileResp.GetOk())
				return
			}

			// 如果能够成功获取用户信息，说明cookie有效
			if profileResp.GetData() != nil && profileResp.GetData().GetUser() != nil {
				user := profileResp.GetData().GetUser()
				logger.Infof("微博启动成功，Cookie验证通过 uid=%d name=%s",
					user.GetId(),
					user.GetScreenName())
			} else {
				logger.Info("微博启动成功，Cookie验证通过")
			}
		}()
	}

	go func() {
		for range time.Tick(time.Hour) {
			freshCookieOpt(sub)
		}
	}()
	// 使用 EmitQueue 进行轮询，间隔由 weibo.interval 配置控制
	c.StateManager.UseEmitQueueWithSiteInterval("weibo")
	c.StateManager.UseFreshFunc(c.EmitQueueFresher(func(p concern_type.Type, id interface{}) ([]concern.Event, error) {
		uid := id.(int64)
		if p.ContainAny(News) {
			newsInfo, err := c.freshNews(uid)
			if err != nil {
				return nil, err
			}
			if len(newsInfo.Cards) == 0 {
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
					logger.Info("BOT已上线，刷新微博订阅模块启动时间")
				}
				online = m
			}
			logger.Debugf("模块 WEIBO 收到：bot_online: %v", msg)
		}
	}()
	return c.StateManager.Start()
}

func (c *Concern) Stop() {
	logger.Tracef("正在停止%v concern", Site)
	logger.Tracef("正在停止%v StateManager", Site)

	// 停止 Cookie 监控
	StopCookieRefreshMonitor()

	c.StateManager.Stop()
	logger.Tracef("%v StateManager 已停止", Site)
	logger.Tracef("%v concern 已停止", Site)
}

func (c *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	id := _id.(int64)
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("id", id)

	err := c.StateManager.CheckGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	info, err := c.FindOrLoadUserInfo(id)
	if err != nil {
		log.Errorf("FindOrLoadUserInfo error %v", err)
		return nil, fmt.Errorf("查询用户信息失败 %v - %v", id, err)
	}
	if r, _ := c.GetStateManager().GetConcern(id); r.Empty() {
		cardResp, err := ApiContainerGetIndexCards(id)
		if err != nil {
			log.Errorf("ApiContainerGetIndexCards error %v", err)
			return nil, fmt.Errorf("添加订阅失败 - 刷新用户微博失败")
		}
		if cardResp.GetOk() != 1 {
			log.WithField("respOk", cardResp.GetOk()).
				Errorf("ApiContainerGetIndexCards not ok")
			return nil, fmt.Errorf("添加订阅失败 - 无法查看用户微博")
		}
		// LatestNewsTs 第一次就手动塞一下时间戳，以此来过滤旧的微博
		err = c.AddNewsInfo(&NewsInfo{
			UserInfo:     info,
			LatestNewsTs: time.Now().Unix(),
		})
		if err != nil {
			log.Errorf("AddNewsInfo error %v", err)
			return nil, fmt.Errorf("添加订阅失败 - 内部错误")
		}
	}
	_, err = c.StateManager.AddGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (c *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	id := _id.(int64)
	identity, _ := c.Get(id)
	_, err := c.StateManager.RemoveGroupConcern(groupCode, id, ctype)
	if identity == nil {
		identity = concern.NewIdentity(_id, "unknown")
	}
	err = c.RemoveUserInfo(id)
	if err != nil {
		logger.Errorf("removeUserInfo error %v", err)
	}
	err = c.RemoveNewsInfo(id)
	if err != nil {
		logger.Errorf("removeNewsInfo error %v", err)
	}
	return identity, err
}

func (c *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	return c.GetUserInfo(id.(int64))
}

func (c *Concern) freshNews(uid int64) (*NewsInfo, error) {
	log := logger.WithField("uid", uid)
	userInfo, err := c.FindOrLoadUserInfo(uid)
	if err != nil {
		return nil, fmt.Errorf("FindOrLoadUserInfo error %v", err)
	}
	// 如果发现UID为0，重新刷新用户信息
	if userInfo.GetUid() == int64(0) {
		userInfo, err = c.FindUserInfo(uid, true)
		if err != nil {
			return nil, fmt.Errorf("user id is zero, get new user info %v", err)
		}
		if userInfo == nil {
			return nil, fmt.Errorf("new userInfo is nil")
		}
		if userInfo.GetUid() == int64(0) {
			userInfo.Uid = uid
			userInfo.Name = "weibo用户"
			logger.Warn("user id is zero, use default id")
		}
	}
	if userInfo == nil {
		return nil, fmt.Errorf("userInfo is nil")
	}
	cardResp, err := ApiContainerGetIndexCards(uid)
	if err != nil {
		log.Errorf("ApiContainerGetIndexCards error %v", err)
		return nil, err
	}
	if cardResp.GetOk() != 1 {
		log.WithField("respOk", cardResp.GetOk()).
			Errorf("ApiContainerGetIndexCards not ok")
		return nil, errors.New("ApiContainerGetIndexCards not success")
	}
	var lastTs int64
	var newsInfo = &NewsInfo{UserInfo: userInfo}
	oldNewsInfo, err := c.GetNewsInfo(uid)
	if err == buntdb.ErrNotFound {
		lastTs = time.Now().Unix()
		newsInfo.LatestNewsTs = lastTs
	} else {
		lastTs = oldNewsInfo.LatestNewsTs
		newsInfo.LatestNewsTs = lastTs
	}
	for _, card := range cardResp.GetData().GetList() {
		if pass, t := c.filterCard(card, lastTs); pass {
			newsInfo.Cards = append(newsInfo.Cards, card)
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

func (c *Concern) filterCard(card *Card, lastTs int64) (bool, int64) {
	uid := card.GetUser().GetId()
	// 应该用dynamic_id_str
	// 但好像已经没法保持向后兼容同时改动了
	// 只能相信概率论了，出问题的概率应该比较小，出问题会导致推送丢失
	replaced, err := c.MarkMblogId(strconv.FormatInt(card.GetId(), 10))
	if err != nil {
		logger.WithField("uid", uid).
			WithField("MblogId", card.GetId()).
			Errorf("MarkDynamicId error %v", err)
		return false, 0
	}
	if replaced {
		return false, 0
	}
	var tsLimit int64
	if cfg.GetWeiboOnlyOnlineNotify() {
		tsLimit = c.cacheStartTs
	} else {
		tsLimit = 0
	}
	t, err := time.Parse(time.RubyDate, card.GetCreatedAt())
	if err != nil {
		logger.WithField("time_string", card.GetCreatedAt()).
			Errorf("can not parse Mblog.CreatedAt %v", err)
		return false, 0
	} else if lastTs < 0 || t.Unix() < lastTs || t.Unix() < tsLimit {
		return false, 0
	}
	return true, t.Unix()
}

func (c *Concern) notifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, ievent concern.Event) []concern.Notify {
		var result []concern.Notify
		switch news := ievent.(type) {
		case *NewsInfo:
			if len(news.Cards) > 0 {
				for _, n := range NewConcernNewsNotify(groupCode, news) {
					result = append(result, n)
				}
			}
		}
		return result
	}
}

func (c *Concern) FindUserInfo(uid int64, load bool) (*UserInfo, error) {
	if load {
		profileResp, err := ApiContainerGetIndexProfile(uid)
		if err != nil {
			logger.WithField("uid", uid).Errorf("ApiContainerGetIndexProfile error %v", err)
			return nil, err
		}
		if profileResp.GetOk() != 1 {
			logger.WithField("respOk", profileResp.GetOk()).
				Errorf("ApiContainerGetIndexProfile not ok")
			return nil, errors.New("接口请求失败")
		}
		err = c.AddUserInfo(&UserInfo{
			Uid:             uid,
			Name:            profileResp.GetData().GetUser().GetScreenName(),
			ProfileImageUrl: profileResp.GetData().GetUser().GetProfileImageUrl(),
			ProfileUrl:      profileResp.GetData().GetUser().GetProfileUrl(),
		})
		if err != nil {
			logger.WithField("uid", uid).Errorf("AddUserInfo error %v", err)
		}
	}
	return c.GetUserInfo(uid)
}

func (c *Concern) FindOrLoadUserInfo(uid int64) (*UserInfo, error) {
	info, _ := c.FindUserInfo(uid, false)
	if info == nil {
		return c.FindUserInfo(uid, true)
	}
	return info, nil
}

func NewConcern(notify chan<- concern.Notify) *Concern {
	c := &Concern{
		StateManager: NewStateManager(notify),
	}
	return c
}
