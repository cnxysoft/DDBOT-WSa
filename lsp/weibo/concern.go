package weibo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	stdatomic "sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/tidwall/buntdb"
)

var logger = utils.GetModuleLogger("weibo-concern")

// runtimeSUB 维护运行时 SUB，可被扫码登录更新，避免周期刷新使用过期值
var (
	runtimeSUB   string
	runtimeSUBMu sync.RWMutex
)

// setRuntimeSUB 更新运行时 SUB
func setRuntimeSUB(sub string) {
	runtimeSUBMu.Lock()
	runtimeSUB = sub
	runtimeSUBMu.Unlock()
}

// getRuntimeSUB 获取当前运行时 SUB
func getRuntimeSUB() string {
	runtimeSUBMu.RLock()
	defer runtimeSUBMu.RUnlock()
	return runtimeSUB
}

type Concern struct {
	*StateManager
	cacheStartTs int64
	lifecycleMu  sync.Mutex
	started      bool
	stopped      bool
	stopRuntime  context.CancelFunc
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
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.started {
		return nil
	}
	settings := settingsFromSnapshot(config.GlobalConfig.Snapshot())
	refreshMu.Lock()
	prepared, err := prepareRuntime(settings)
	if err == nil {
		prepared.apply()
		settings = prepared.settings
	} else {
		logger.Warnf("微博初始会话尚不可用，模块等待后续配置/扫码更新: %v", err)
	}
	refreshMu.Unlock()
	reconcileSubAutoRefresh(settings.mode == "login" && settings.autoRefresh && settings.refreshURL != "")
	if settings.mode == "login" && settings.sub == "" && GetQRLoginEnable() {
		go func() {
			sub, err := RunQRLogin(QRLoginOption{OutputDir: ".", AutoOpen: true})
			if err == nil {
				current := settingsFromSnapshot(config.GlobalConfig.Snapshot())
				if current.mode != "login" || current.sub != "" {
					return
				}
				err = finishLogin(sub)
			}
			if err != nil {
				logger.Warnf("微博终端扫码未完成，可通过 WebUI 重试: %v", err)
			}
		}()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.stopRuntime = cancel
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				refreshMu.Lock()
				if ctx.Err() == nil && !cfg.IsWeiboAPIMode() {
					if err := freshCookieOptUnlocked(getRuntimeSUB()); err != nil {
						logger.Warnf("微博周期刷新失败: %v", err)
					}
				}
				refreshMu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
	// 使用 EmitQueue 进行轮询，间隔由 weibo.interval 配置控制
	c.StateManager.UseEmitQueueWithSiteInterval("weibo")
	c.StateManager.UseFreshFunc(c.EmitQueueFresher(func(p concern_type.Type, id interface{}) ([]concern.Event, error) {
		uid := id.(int64)
		if !cfg.IsWeiboAPIMode() && !isGuestMode() && getRuntimeSUB() == "" {
			return nil, nil
		}
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
		events := eventbus.BusObj.Subscribe("bot_online")
		wasOnline := false
		for {
			select {
			case msg := <-events:
				if state, ok := msg.(bool); ok {
					if state && !wasOnline {
						stdatomic.StoreInt64(&c.cacheStartTs, time.Now().Unix())
					}
					wasOnline = state
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	if err := c.StateManager.Start(); err != nil {
		cancel()
		return err
	}
	c.started = true
	return nil
}

func (c *Concern) Stop() {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if !c.started || c.stopped {
		return
	}
	c.stopped = true
	if c.stopRuntime != nil {
		c.stopRuntime()
	}
	logger.Tracef("正在停止%v concern", Site)
	logger.Tracef("正在停止%v StateManager", Site)

	// 停止 Cookie 监控
	StopCookieRefreshMonitor()

	// 停止 SUB 自动刷新
	StopSubAutoRefresh()

	c.StateManager.Stop()
	logger.Tracef("%v StateManager 已停止", Site)
	logger.Tracef("%v concern 已停止", Site)
}

// waitForWSAndAlert 等待 WS 就绪后发送 Cookie 告警，带 5 分钟超时
// 不依赖 bot_online 事件，直接轮询 WS 连接状态
func waitForWSAndAlert(isRecovery bool) {
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if canDeliverMessage() {
				if cfg.GetWeiboCookieAlertEnabled() {
					sendCookieAlertToAllGroups(isRecovery)
				}
				return
			}
		case <-timeout:
			logger.Warn("等待 WS 就绪超时（5分钟），Cookie 告警未发送")
			return
		}
	}
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
		// 如果是 JSON 解析错误（收到 HTML），可能是用户隐私设置或账号异常
		if strings.Contains(err.Error(), "invalid character '<'") {
			logger.Warnf("uid=%d: 无法获取微博数据，该用户可能设置了隐私保护、已注销或被封禁", uid)
			logger.Warnf("uid=%d: 建议取消订阅该用户", uid)
		}
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
		tsLimit = stdatomic.LoadInt64(&c.cacheStartTs)
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
		case *CookieAlertNotify:
			result = append(result, news)
		case *SUBExpiredNotify:
			result = append(result, news)
		}
		return result
	}
}

// ResubscribeAll 一键重新发起群组的所有微博订阅
// 会先删除该群当前所有订阅，然后重新添加
func (c *Concern) ResubscribeAll(ctx mmsg.IMsgCtx, groupCode int64) (int, error) {
	log := logger.WithFields(localutils.GroupLogFields(groupCode))
	log.Info("开始一键重新订阅")

	// 1. 获取该群所有微博订阅
	_, ids, ctypes, err := c.StateManager.ListConcernState(func(gc int64, id interface{}, ct concern_type.Type) bool {
		return gc == groupCode && ct.ContainAny(News)
	})
	if err != nil {
		log.Errorf("获取订阅列表失败：%v", err)
		return 0, err
	}

	if len(ids) == 0 {
		log.Info("该群暂无微博订阅")
		return 0, nil
	}

	log.Infof("找到 %d 个微博订阅", len(ids))

	// 2. 保存订阅 ID 和类型列表
	type subItem struct {
		id int64
		ct concern_type.Type
	}
	var subList []subItem
	for i, id := range ids {
		subList = append(subList, subItem{
			id: id.(int64),
			ct: ctypes[i],
		})
	}

	// 3. 删除所有订阅（从数据库中移除）
	log.Debug("开始删除旧订阅")
	for _, item := range subList {
		_, err := c.Remove(ctx, groupCode, item.id, item.ct)
		if err != nil {
			log.Errorf("删除订阅失败 uid=%d: %v", item.id, err)
		} else {
			log.Debugf("已删除订阅 uid=%d", item.id)
		}
	}

	// 4. 重新添加订阅
	log.Debug("开始重新添加订阅")
	var successCount int
	for _, item := range subList {
		uid := item.id
		log.Debugf("正在重新订阅用户 %d/%d: uid=%d", successCount+1, len(subList), uid)

		// 刷新用户信息
		_, err := c.FindOrLoadUserInfo(uid)
		if err != nil {
			log.Errorf("刷新用户信息失败 uid=%d: %v", uid, err)
			continue
		}

		// 验证用户微博是否可访问
		cardResp, err := ApiContainerGetIndexCards(uid)
		if err != nil {
			log.Errorf("验证用户微博失败 uid=%d: %v", uid, err)
			continue
		}
		if cardResp.GetOk() != 1 {
			log.Errorf("用户微博不可访问 uid=%d: ok=%d", uid, cardResp.GetOk())
			continue
		}

		// 添加订阅到数据库
		_, err = c.Add(ctx, groupCode, uid, item.ct)
		if err != nil {
			log.Errorf("添加订阅失败 uid=%d: %v", uid, err)
			continue
		}

		successCount++
	}

	log.Infof("一键重新订阅完成，成功 %d/%d", successCount, len(subList))
	return successCount, nil
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
