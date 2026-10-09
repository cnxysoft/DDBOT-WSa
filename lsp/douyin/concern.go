package douyin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/cookiejar"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	templUtils "github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/adapter"

	"github.com/cnxysoft/DDBOT-WSa/lsp/authprompt"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
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

	// 带签名作品列表请求的作品数量。
	douyinUserPostsCount = 15
)

var (
	logger   = templUtils.GetModuleLogger(ConcernName)
	Cookie   *cookiejar.Jar
	online   bool
	BasePath = map[string]string{
		PathGetUserInfo:        BaseHost,
		PathGetUserPosts:       BaseHost,
		PathWebcastUserProfile: BaseLiveHost,
	}

	// 模块是否已启动（调试指令前置检查）。
	douyinModuleStarted atomic.Bool
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

func (c *StateManager) SetNotifyMsg(notifyKey string, msg *adapter.GroupMessage) error {
	tmp := &adapter.GroupMessage{
		ID:        msg.ID,
		GroupCode: msg.GroupCode,
		Sender:    msg.Sender,
		Time:      msg.Time,
		Elements: localutils.AdapterMessageFilter(msg.Elements, func(e adapter.IMessageElement) bool {
			return e.Type() == adapter.ElementTypeText || e.Type() == adapter.ElementTypeImage
		}),
	}
	value, err := localutils.SerializationAdapterGroupMsg(tmp)
	if err != nil {
		return err
	}
	return c.Set(c.NotifyMsgKey(tmp.GroupCode, notifyKey), value,
		localdb.SetExpireOpt(CompactExpireTime), localdb.SetNoOverWriteOpt())
}

func (c *StateManager) GetNotifyMsg(groupCode int64, notifyKey string) (*adapter.GroupMessage, error) {
	value, err := c.Get(c.NotifyMsgKey(groupCode, notifyKey))
	if err != nil {
		return nil, err
	}
	return localutils.DeserializationAdapterGroupMsg(value)
}

func (c *StateManager) MarkDynamicId(dynamic string) (bool, error) {
	var isOverwrite bool
	err := c.Set(c.DynamicIdKey(dynamic), "",
		localdb.SetExpireOpt(time.Hour*120), localdb.SetGetIsOverwriteOpt(&isOverwrite))
	return isOverwrite, err
}

// SetNotLiveConfirm 记录 uid 的下播连续确认次数（成品用 KV 存储，键见 NotLiveConfirmKey）。
func (d *Concern) SetNotLiveConfirm(uid string, count int) error {
	return d.SetInt64(d.NotLiveConfirmKey(uid), int64(count))
}

// GetNotLiveConfirm 读取 uid 的下播连续确认次数。
func (d *Concern) GetNotLiveConfirm(uid string) (int, error) {
	count, err := d.GetInt64(d.NotLiveConfirmKey(uid))
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// ResetNotLiveConfirm 清零 uid 的下播连续确认次数。
func (d *Concern) ResetNotLiveConfirm(uid string) error {
	_, err := d.Delete(d.NotLiveConfirmKey(uid), localdb.IgnoreNotFoundOpt())
	return err
}

// removeNotLiveConfirm 删除 uid 的下播确认状态（退订时调用）。
func (d *Concern) removeNotLiveConfirm(id string) error {
	return d.ResetNotLiveConfirm(id)
}

// removeUidFirstTimestamp 删除 uid 的首见时间戳。
func (d *Concern) removeUidFirstTimestamp(id string) error {
	_, err := d.Delete(d.UidFirstTimestamp(id), localdb.IgnoreNotFoundOpt())
	return err
}

type Concern struct {
	*StateManager
	notify       chan<- concern.Notify
	cacheStartTs int64
	// 成品 reflect 元数据（types-closure-x64.tsv:11269）size=72，尾部 4 个同步原语：
	//   authStop chan struct{} @+24（鉴权/扫码流程的停止信号）
	//   authStopOnce sync.Once @+32
	//   liveRefreshReadyOnce sync.Once @+44（live 首刷就绪）
	//   newsRefreshReadyOnce sync.Once @+56（news 首刷就绪）
	// 与成品 (*Concern).Start(@0xd73a40, size 7328) 的启动流程对应。
	authStop             chan struct{}
	authStopOnce         sync.Once
	liveRefreshReadyOnce sync.Once
	newsRefreshReadyOnce sync.Once
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
	// 成品 Add @0xd6dd5b：WithField("currently_living", liveInfo != nil)
	// → Info("抖音订阅添加成功")(@0x1125e40,len24,level=Info)。
	log.WithField("currently_living", liveInfo != nil).Info("抖音订阅添加成功")
	return info, nil
}

func (d *Concern) GroupWatchNotify(groupCode int64, mid string) {
	userInfo, _ := d.GetUserInfo(mid)
	if userInfo.WebRoomId != "" {
		var liveInfo = &LiveInfo{
			IsLiving: true,
			UserInfo: *userInfo,
		}
		d.notify <- NewConcernLiveNotify(groupCode, liveInfo)
	}
}

func (d *Concern) removeUserInfo(id string) error {
	_, err := d.Delete(d.UserInfoKey(id), localdb.IgnoreNotFoundOpt())
	return err
}

func (d *Concern) removeCurrentLive(id string) error {
	_, err := d.Delete(d.CurrentLiveKey(id), localdb.IgnoreNotFoundOpt())
	return err
}

func (d *Concern) removeFresh(id string) error {
	_, err := d.Delete(d.FreshKey(id), localdb.IgnoreNotFoundOpt())
	return err
}

// clearDouyinSubscriptionRuntimeState 清理 uid 的抖音运行状态（当前直播/用户信息/作品基线）。
// 成品 Remove @0xd6ed02 在任一清理失败时统一记录 Error("清理抖音订阅运行状态失败")。
// remove* 内部均带 localdb.IgnoreNotFoundOpt()，返回的 error 都是真实失败。
func (d *Concern) clearDouyinSubscriptionRuntimeState(id string) error {
	if err := d.removeCurrentLive(id); err != nil {
		return err
	}
	if err := d.removeUserInfo(id); err != nil {
		return err
	}
	if err := d.removeFresh(id); err != nil {
		return err
	}
	return nil
}

func (d *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	// 大部分时候简单的删除即可
	// 如果还有更复杂的逻辑可以自由实现
	identity, _ := d.Get(id)
	// 成品 Remove 用 RemoveGroupConcern 的返回值作为 remaining_types 字段。
	remainingTypes, err := d.GetStateManager().RemoveGroupConcern(groupCode, id.(string), ctype)
	if err != nil {
		return nil, err
	}

	// 成品 Remove @0xd6ed02：清理运行状态失败统一 Error("清理抖音订阅运行状态失败", level=Error)。
	if err = d.clearDouyinSubscriptionRuntimeState(id.(string)); err != nil {
		logger.WithError(err).Error("清理抖音订阅运行状态失败")
	}

	idStr, _ := id.(string)
	_ = d.removeNotLiveConfirm(idStr)
	_ = d.removeUidFirstTimestamp(idStr)

	if identity == nil {
		identity = concern.NewIdentity(id, "unknown")
	}
	// 成品 Remove @0xd6efb4：WithField("remaining_types", remaining)
	// → Info("抖音订阅删除成功")(@0x1125e60,len24,level=Info)。
	logger.WithField("remaining_types", remainingTypes).Info("抖音订阅删除成功")
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

// subscribedIDs 列出某订阅类型的全部去重 uid。
// 成品 (*Concern).subscribedIDs @concern.go:414 返回 ([]string, error)：
// ListConcernState 出错时直接返回 (nil, err)（disasm @0xd6f5c0 test err.type; jne 返回；
// Start @0xd74e19/d74e44 与 refreshSubscribedType @0xd6f956 均消费第二个返回值）。
func (d *Concern) subscribedIDs(ctype concern_type.Type) ([]string, error) {
	_, ids, _, err := d.StateManager.ListConcernState(func(g int64, id interface{}, p concern_type.Type) bool {
		return p.ContainAll(ctype)
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		uid, _ := id.(string)
		if uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		result = append(result, uid)
	}
	return result, nil
}

// refreshSubscribedType 对一类订阅执行一轮刷新，返回成功/失败计数。
func (d *Concern) refreshSubscribedType(ctx context.Context, ctype concern_type.Type, eventChan chan<- concern.Event) (int, int, bool, error) {
	ids, err := d.subscribedIDs(ctype)
	if err != nil {
		logger.WithField("type", ctype.String()).WithError(err).
			Warnf("读取抖音订阅清单失败：%v", err)
		return 0, 0, false, err
	}
	success := 0
	failed := 0
	// risk 表示本轮出现过可重试风控（403/429）失败，供 runScheduledRefresh 退避。
	// 成品 refreshSubscribedType @0xd6f8c0 +0x221 调 isDouyinRetryableRiskFailure。
	risk := false
	var lastErr error
	spacing := getDouyinRequestSpacing()
	for _, uid := range ids {
		select {
		case <-ctx.Done():
			return success, failed, risk, lastErr
		default:
		}
		events, err := d.freshInfo(ctype, uid, eventChan)
		if err != nil {
			failed++
			if lastErr == nil {
				lastErr = err
			}
			if isDouyinRetryableRiskFailure(err) {
				risk = true
			}
			logger.WithField("uid", uid).
				WithField("type", ctype.String()).
				WithError(err).
				Warnf("抖音订阅刷新失败：%v", err)
			continue
		}
		success++
		for _, e := range events {
			select {
			case eventChan <- e:
			case <-ctx.Done():
				return success, failed, risk, lastErr
			}
		}
		if spacing > 0 {
			select {
			case <-time.After(spacing):
			case <-ctx.Done():
				return success, failed, risk, lastErr
			}
		}
	}
	return success, failed, risk, lastErr
}

// logDouyinRefreshStats 输出一轮刷新统计（成品 (*Concern).logDouyinRefreshStats，concern.go:469）。
// 字段：type/subscriptions/successful/failed/cost；subscriptions 由本轮订阅清单长度得到。
//
// 成品在本轮有成功项时用 liveRefreshReadyOnce/newsRefreshReadyOnce 标记「首次刷新成功」：
// disasm @0xd6ffbb 取 ctype 与 "live" 比较后选 once（news 失败时 @+0x38=newsRefreshReadyOnce，
// 命中 live 时 @+0x2c=liveRefreshReadyOnce），@0xd70021 call sync.(*Once).doSlow，
// Once 闭包 logDouyinRefreshStats.func1 @0xd70240 以 Info 打印
// "抖音订阅首次刷新成功"(@0x1125e90,len30)；随后本级日志文案为
// "抖音订阅刷新完成"(@0x1125e80,len24)，level=Debug(5)。
func (d *Concern) logDouyinRefreshStats(ctype concern_type.Type, start time.Time, success int, failed int, lastErr error) {
	_, ids, _, _ := d.StateManager.ListConcernState(func(g int64, id interface{}, p concern_type.Type) bool {
		return p.ContainAll(ctype)
	})
	log := logger.WithField("type", ctype.String()).
		WithField("subscriptions", len(ids)).
		WithField("successful", success).
		WithField("failed", failed).
		WithField("cost", time.Since(start))
	// 成品分支 @0xd6ff9d：failed(0x338)<=0 且 subscriptions(0x328)>0 才进入 Once 分支；
	// failed>0 走 @0xd7006e 的另一条日志分支。这里按成品条件判定。
	if failed <= 0 && len(ids) > 0 {
		once := &d.newsRefreshReadyOnce
		if ctype.ContainAny(Live) {
			once = &d.liveRefreshReadyOnce
		}
		once.Do(func() {
			log.Info("抖音订阅首次刷新成功")
		})
	}
	if failed > 0 {
		// 成品 logDouyinRefreshStats @0xd701a5 failed>0 分支：Warn
		// "抖音订阅刷新部分失败，下轮将继续重试"(@0x1125e70,len54)；lastErr 非空时
		// 追加 error 字段（全局 0x1b91b60）；failed<=0 走 Debug("抖音订阅刷新完成")。
		if lastErr != nil {
			log.WithError(lastErr).Warn("抖音订阅刷新部分失败，下轮将继续重试")
		} else {
			log.Warn("抖音订阅刷新部分失败，下轮将继续重试")
		}
		return
	}
	log.Debug("抖音订阅刷新完成")
}

// runScheduledRefresh 按类型各自的刷新间隔循环刷新。
//
// 成品 @0xd702a0 的风控退避（disasm +0x41f..+0x4b2 逐段取证）：
//   - risk（本轮命中 403/429 可重试风控）时把连续计数 +1，n=min(旧计数,4)，
//     backoff = 60s << n（@0xdf8475800=60s，shl 后 imul），上限 15min（@0xd18c2e2800）；
//   - next = max(base, backoff)；仅当 backoff > base 时打 Warn
//     "抖音接口触发风控，本类型轮询已自动退避；另一类型不受影响"(@0x1125ea0,len84)，
//     字段 base_interval/next_interval/consecutive_risk_failures（level=Warn）；
//   - 非 risk 时连续计数清零，next 回落 base。
func (d *Concern) runScheduledRefresh(ctx context.Context, ctype concern_type.Type, eventChan chan<- concern.Event, interval time.Duration) {
	baseInterval := interval
	consecutiveRiskFailures := 0
	timer := time.NewTimer(time.Second * 3)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		start := time.Now()
		success, failed, risk, lastErr := d.refreshSubscribedType(ctx, ctype, eventChan)
		d.logDouyinRefreshStats(ctype, start, success, failed, lastErr)
		nextInterval := baseInterval
		if risk {
			consecutiveRiskFailures++
			n := consecutiveRiskFailures - 1
			if n > 4 {
				n = 4
			}
			backoff := time.Duration(1<<uint(n)) * time.Minute
			if backoff > 15*time.Minute {
				backoff = 15 * time.Minute
			}
			if backoff > baseInterval {
				nextInterval = backoff
				logger.WithField("base_interval", baseInterval).
					WithField("next_interval", nextInterval).
					WithField("consecutive_risk_failures", consecutiveRiskFailures).
					Warn("抖音接口触发风控，本类型轮询已自动退避；另一类型不受影响")
			}
		} else {
			consecutiveRiskFailures = 0
		}
		timer.Reset(nextInterval)
	}
}

func (d *Concern) fresh() concern.FreshFunc {
	return func(ctx context.Context, eventChan chan<- concern.Event) {
		liveInterval := getDouyinLiveRefreshInterval()
		newsInterval := getDouyinNewsRefreshInterval()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			d.runScheduledRefresh(ctx, Live, eventChan, liveInterval)
		}()
		go func() {
			defer wg.Done()
			d.runScheduledRefresh(ctx, News, eventChan, newsInterval)
		}()
		wg.Wait()
	}
}

// getDouyinLiveRefreshInterval 直播刷新间隔：douyin.liveInterval，回退 douyin.interval，默认30s。
func getDouyinLiveRefreshInterval() time.Duration {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("douyin.liveInterval"); v > 0 {
			return v
		}
		if v := config.GlobalConfig.GetDuration("douyin.interval"); v > 0 {
			return v
		}
	}
	return time.Second * 30
}

// getDouyinNewsRefreshInterval 动态刷新间隔：douyin.newsInterval，回退 douyin.interval，默认2m。
func getDouyinNewsRefreshInterval() time.Duration {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("douyin.newsInterval"); v > 0 {
			return v
		}
		if v := config.GlobalConfig.GetDuration("douyin.interval"); v > 0 {
			return v
		}
	}
	return time.Minute * 2
}

// getDouyinRequestSpacing 相邻请求间的全局间隔：douyin.requestSpacing，默认2s，负值归零，最大10s。
func getDouyinRequestSpacing() time.Duration {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("douyin.requestSpacing"); v != 0 {
			if v < 0 {
				return 0
			}
			if v > 10*time.Second {
				return 10 * time.Second
			}
			return v
		}
	}
	return time.Second * 2
}

// getNotLiveConfirmTimes 下播需要连续确认的次数：douyin.notLiveConfirmTimes，默认3，最大10。
// 钳制逻辑统一由 cfg.GetDouyinNotLiveConfirmTimes 承担（成品该 getter 的唯一 caller 是
// douyin.(*Concern).freshInfo @ concern.go:547），此处委托以保持接线与成品一致。
func getNotLiveConfirmTimes() int {
	return cfg.GetDouyinNotLiveConfirmTimes()
}

// requestDouyinSignedUserPosts 拉取带 a_bogus 签名的作品列表，并返回原始 JSON 供图集解析。
// 单次请求实现已迁到 getUserPost.go 的 getPostsOnce（成品同名符号 @ getUserPost.go:54）。
func requestDouyinSignedUserPosts(secUID string) (*UserPostsResponse, json.RawMessage, error) {
	return getPostsOnce(secUID)
}

func (d *Concern) freshInfo(ctype concern_type.Type, id interface{}, eventChan chan<- concern.Event) ([]concern.Event, error) {
	var start = time.Now()
	var result []concern.Event
	userId := id.(string)
	usrInfo, err := d.FindOrLoadUserInfo(userId)
	if err != nil {
		return nil, err
	}
	if ctype.ContainAll(Live) {
		snapshot, err := FreshLiveSnapshot(userId)
		if err != nil {
			return nil, err
		}
		isLive := snapshot.Living
		oldIsLive, err := d.GetCurrentLive(userId)
		if err != nil && err.Error() != ErrNotFound {
			logger.WithError(err).Debug("读取抖音直播缓存状态失败，本次订阅不补发当前直播")
			return nil, err
		}
		oldFreshTime, err := d.GetFreshTime(userId)
		if err != nil && err.Error() != ErrNotFound {
			return nil, err
		}
		// 用快照刷新昵称/头像，昵称变化也反映到订阅信息。
		if snapshot.Nickname != "" {
			usrInfo.NikeName = snapshot.Nickname
		}
		if snapshot.Avatar != "" {
			usrInfo.AvatarUrl = snapshot.Avatar
		}
		if snapshot.WebRID != "" && usrInfo.WebRoomId == "" {
			usrInfo.WebRoomId = snapshot.WebRID
		}
		// 成品 freshInfo @0xd70a00：以 snapshot.Living（sil=snapshot+0）为条件，
		// 在标题/封面非空时写入 UserInfo.LastLiveTitle/LastLiveCover
		// （@0xd70a05-0xd70a6c：snapshot+0x68/0x70 → usrInfo+0x78/0x80、
		//  snapshot+0x78/0x80 → usrInfo+0x88/0x90）。这两个字段随 AddUserInfo 持久化。
		if snapshot.Living {
			if snapshot.Title != "" {
				usrInfo.LastLiveTitle = snapshot.Title
			}
			if snapshot.Cover != "" {
				usrInfo.LastLiveCover = snapshot.Cover
			}
		}
		if oldIsLive != isLive {
			// 成品 freshInfo @0xd71b02：WithFields{old_living,living,will_notify}
			// → Info("抖音直播状态发生变化")(@0x1125ed0,len30,level=Info)。
			// will_notify 取「该状态变化是否真正产生推送」（受 30min fresh 抑制影响）。
			willNotify := true
			if !isLive {
				if time.Since(time.Unix(oldFreshTime, 0)) < 30*time.Minute && oldFreshTime != 0 {
					willNotify = false
				}
			} else {
				if !(time.Since(time.Unix(oldFreshTime, 0)) < 30*time.Minute || oldFreshTime == 0) {
					willNotify = false
				}
			}
			logger.WithFields(logrus.Fields{
				"old_living":  oldIsLive,
				"living":      isLive,
				"will_notify": willNotify,
			}).Info("抖音直播状态发生变化")
			if !isLive {
				// 下播需要连续确认，避免接口抖动造成误报。
				confirmTimes := getNotLiveConfirmTimes()
				count, _ := d.GetNotLiveConfirm(userId)
				if count < confirmTimes-1 {
					_ = d.SetNotLiveConfirm(userId, count+1)
					logger.WithField("uid", userId).
						WithField("count", count+1).
						WithField("need", confirmTimes).
						Info("抖音直播下播状态等待连续确认")
					return result, nil
				}
				_ = d.ResetNotLiveConfirm(userId)
				_ = d.SetCurrentLive(userId, isLive)
				if time.Since(time.Unix(oldFreshTime, 0)) < 30*time.Minute && oldFreshTime != 0 {
					logger.WithField("uid", userId).Trace("noliving notify suppressed (recent fresh)")
					return result, nil
				}
				live := &LiveInfo{
					UserInfo:          *usrInfo,
					IsLiving:          false,
					liveStatusChanged: true,
				}
				result = append(result, live)
			} else {
				_ = d.ResetNotLiveConfirm(userId)
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
					live.LiveTitle = snapshot.Title
					live.Cover = snapshot.Cover
					// 开播后补取一次直播间详情（标题/封面以直播间为准）。
					roomID := usrInfo.GetRoomId()
					if roomID == "" {
						roomID = snapshot.WebRID
					}
					if roomID != "" {
						room, err := GetRoomDataWithRoomID(roomID)
						if err != nil {
							// 成品 freshInfo @0xd71443：WithField("room_id_str", roomID)
							// (+ WithError(err)) → Warn("抖音直播间详情获取失败，继续使用用户主页中的标题和封面推送")
							//（@0x1125ec0,len87,level=Warn）。
							logger.WithField("room_id_str", roomID).
								WithError(err).
								Warn("抖音直播间详情获取失败，继续使用用户主页中的标题和封面推送")
						} else if room != nil {
							if room.Title != "" {
								live.LiveTitle = room.Title
							}
							if room.Cover != "" {
								live.Cover = room.Cover
							}
							if (room.Title == "" || room.Cover == "") && (live.LiveTitle == "" || live.Cover == "") {
								logger.WithField("roomId", roomID).
									Warnf("%s 详情不完整（标题或直播封面为空）", roomID)
							}
						}
					}
					result = append(result, live)
				}
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
		// 成品 freshInfo @concern.go:547 通过全局函数指针（0x1b90040，取值后 call *%rcx）
		// 调用 GetPosts；GetPosts 内部完成 news 认证告警、健康标记与 rememberDouyinPostExtras(raw)。
		resp, err := GetPosts(usrInfo.SecUid)
		if err != nil {
			return nil, err
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
		if len(resCards) > 0 {
			// 成品 freshInfo @0xd726eb：WithFields{sec_uid,new_posts}
			// → Info("检测到抖音新作品")(@0x1125ef0,len24,level=Info)。
			// [推断] new_posts 取本轮新作品数（成品该值类型为 string，无法坐实取值）。
			logger.WithFields(logrus.Fields{
				"sec_uid":   usrInfo.SecUid,
				"new_posts": len(resCards),
			}).Info("检测到抖音新作品")
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

// douyinBaseRequestOptions 是所有抖音 API 请求的通用选项（使用共享 Cookie Jar 与严格串行调度）。
func douyinBaseRequestOptions(trace *douyinAPIRequestTrace) []requests.Option {
	_ = trace
	return []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(getDouyinRequestTimeout()),
		requests.AddUAOption(activeDouyinUserAgent()),
		requests.RequestAutoHostOption(),
		requests.HeaderOption("Connection", "keep-alive"),
		requests.HeaderOption("Accept", "*/*"),
		requests.HeaderOption("Accept-Encoding", "gzip, deflate, br, zstd"),
		requests.HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"),
		requests.RetryOption(3),
	}
}

func (d *Concern) Start() error {
	// 成品 (*Concern).Start @concern.go:780 入口先把 moduleStarted(0x1bf43ab) 置 0
	//（d73a65: movb $0x0）。
	douyinModuleStarted.Store(false)
	// 初始化登录态：从配置/状态文件装载 Cookie，并输出就绪日志。
	// 成品 error 分支 @0xd74eda：仍置 moduleStarted=1、只 Error 记录文案后 return nil，
	// 不把 error 交回框架（registry.StartAll 会移除返回非 nil error 的 Concern）。
	hasCookie, err := initializeDouyinAuth()
	if err != nil {
		douyinModuleStarted.Store(true)
		logger.WithError(err).Error("抖音登录初始化失败，已单独停用抖音模块；B站和微博不受影响")
		return nil
	}
	// 成品 (*Concern).Start 在 hasCookie 判定之前处理 cookiecloud（d73a80 取 mode →
	// d73ac5 loadDouyinCookieCloudConfig）。配置加载失败时按 hasCookie 分流（d73b78 je）：
	//   - 无本地 Cookie：moduleStarted=1 + Error("抖音Cookie来源已设为 CookieCloud，但配置不完整，
	//     已单独停用抖音模块"，@0x1125f60,len91；field cookie_source=cookiecloud) 后 return nil
	//     （d73ce7 是 moduleStarted 4 处写入中唯一位于 Start 前段的一处）；
	//   - 有本地 Cookie：Error("抖音 CookieCloud 配置不完整，将临时使用本地备用Cookie"，
	//     @0x1125f70,len70) 后继续走本地 Cookie 路径。
	if cfg.GetDouyinCookieMode() == authprompt.ModeCookieCloud {
		cc, ccErr := loadDouyinCookieCloudConfig()
		if ccErr != nil {
			if !hasCookie {
				douyinModuleStarted.Store(true)
				logger.WithField("cookie_source", cfg.GetDouyinCookieMode()).
					Error("抖音 Cookie来源已设为 CookieCloud，但配置不完整，已单独停用抖音模块")
				return nil
			}
			logger.WithField("cookie_source", cfg.GetDouyinCookieMode()).
				Error("抖音 CookieCloud 配置不完整，将临时使用本地备用Cookie")
		} else {
			interval := cc.Interval
			go func() {
				// 成品 Start CookieCloud 分支诊断日志：
				//   @0xd740ad WithFields{cookie_source,server_host,refresh_interval,crypto_type}
				//   → Info("抖音 CookieCloud 客户端已启动，将立即获取并定时刷新Cookie"@0x1125f80,len76)。
				logger.WithFields(logrus.Fields{
					"cookie_source":    cfg.GetDouyinCookieMode(),
					"server_host":      douyinCookieCloudServerHostForLog(cc.Endpoint),
					"refresh_interval": cc.Interval,
					"crypto_type":      cc.CryptoType,
				}).Info("抖音 CookieCloud 客户端已启动，将立即获取并定时刷新Cookie")
				d.refreshDouyinCookieCloud()
				// 成品 Start @0xd7440a（refresh 无错分支）诊断日志：
				//   @0xd744c6 currentDouyinCookieJar → douyinCookiesForState（cookie_count）
				//   @0xd74548 currentDouyinCookieJar → douyinCookieNames（cookie_names）
				//   @0xd745c8 extractDouyinBrowserVersion(activeDouyinUserAgent)（browser_version）
				//   → Info("抖音 CookieCloud 当前会话已就绪"@0x1125fa0,len40)。
				if jar := currentDouyinCookieJar(); jar != nil && hasDouyinLoginCookie(jar) {
					cookies := douyinCookiesForState(jar)
					logger.WithFields(logrus.Fields{
						"cookie_count":    len(cookies),
						"cookie_names":    douyinCookieNames(cookies),
						"browser_version": extractDouyinBrowserVersion(activeDouyinUserAgent()),
					}).Info("抖音 CookieCloud 当前会话已就绪")
				}
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for range ticker.C {
					d.refreshDouyinCookieCloud()
				}
			}()
		}
	}
	// 成品 (*Concern).Start：无 Cookie 时先判 cookieMode==qrcode 且扫码开启。
	if !hasCookie {
		if cfg.GetDouyinCookieMode() == authprompt.ModeQrcode && isDouyinQRLoginEnabled() {
			// callsite +0xdab（reportDouyinCookieFailure 的两个 caller 之一，另一个是
			// markDouyinCookieUnhealthyFor +0x294）：用固定 reason "missing_cookie"
			//（rodata 0x10ac8ff,len14）告警，随后 Warn「已私聊管理员…未确认前模块保持等待」。
			reportDouyinCookieFailure("missing_cookie")
			logger.Warn("抖音 Cookie 未设置，已私聊管理员询问是否生成二维码；未确认前模块保持等待")
		} else {
			// 成品 qr_disabled 路径 @0xd74e87（77B 文案 @0x1125fc0 指向 0x10ebf7e）：
			// movb $0x1,moduleStarted → logger.Warn → xor eax/eax;xor ebx/ebx;ret，
			// 即只告警、置已启动、return nil，停止后续初始化但不从框架移除本 Concern。
			douyinModuleStarted.Store(true)
			logger.Warn("抖音 Cookie 未设置且扫码登录已关闭，将关闭抖音推送功能")
			return nil
		}
	}
	// 成品 (*Concern).Start 调用点(@concern.go:780 +0x13c0)：当前 Jar 缺少登录会话时
	// 按 general 维度记录一次 Cookie 失败，reason 为固定的中文提示。
	// 成品 (*Concern).Start @+0x139a 传 jar 给 hasDouyinLoginCookie（形参 http.CookieJar）。
	// 成品 (*Concern).Start 在启用日志处先 WithFields 打印一份启动参数表
	// （disasm @0xd74845 makemap hint=15，@0xd74d7f WithFields → @0xd74dbe level=Info(4)
	// → "抖音监听模块已启用"@0x1126030,len27）。15 个键/值逐项反汇编取证：
	//   live_interval=getDouyinLiveRefreshInterval、news_interval=getDouyinNewsRefreshInterval、
	//   request_spacing=getDouyinRequestSpacing（三者 convT64 装箱 Duration）；
	//   request_spacing_scope="all_douyin_api"、schedule_mode="strict_serial_shared_session"、
	//   live_status_source="signed_user_profile"、news_source="signed_user_posts"、
	//   news_signature="a_bogus+webSign"、web_sign_runtime="byted-secsdk-strategy-1.0.40"；
	//   complete_cookie_header/news_supported/live_supported 为常量 bool true
	//   （@0xd74a7c/@0xd74bfd/@0xd74c43 均 lea 0x115cc48）;
	//   login_cookie_ready=hasDouyinLoginCookie(currentDouyinCookieJar())（@0xd74c47/@0xd74c56）；
	//   link_parse=messageLinkParser.Enabled(false)（@0xd74cb7 xor eax）；
	//   link_parse_private=messageLinkParser.Enabled(true)（@0xd74d20 mov $1,eax）。
	logger.WithFields(logrus.Fields{
		"live_interval":          getDouyinLiveRefreshInterval(),
		"news_interval":          getDouyinNewsRefreshInterval(),
		"request_spacing":        getDouyinRequestSpacing(),
		"request_spacing_scope":  "all_douyin_api",
		"schedule_mode":          "strict_serial_shared_session",
		"complete_cookie_header": true,
		"live_status_source":     "signed_user_profile",
		"news_source":            "signed_user_posts",
		"news_signature":         "a_bogus+webSign",
		"web_sign_runtime":       "byted-secsdk-strategy-1.0.40",
		"news_supported":         true,
		"live_supported":         true,
		"login_cookie_ready":     hasDouyinLoginCookie(currentDouyinCookieJar()),
		"link_parse":             douyinMessageLinkParser.Enabled(false),
		"link_parse_private":     douyinMessageLinkParser.Enabled(true),
	}).Info("抖音监听模块已启用")
	if !hasDouyinLoginCookie(currentDouyinCookieJar()) {
		markDouyinCookieUnhealthyFor(douyinFailureScopeGeneral, "Cookie 中没有 sessionid/sessionid_ss/sid_guard")
	}
	// 成品 (*Concern).Start @0xd74e05-@0xd755a8：调用 subscribedIDs("live"/"news")，
	// 把两次返回的 error（rdi/rsi）收进一个 []error，按 ucomisd/计数分支：
	//   - 有错：logger.WithField("error", errs)
	//     .Warn("读取抖音订阅清单失败，轮询阶段会继续重试"，@0x1126040,len60)；
	//   - 无错：logger.WithFields{live_unique_users=len(live), news_unique_users=len(news)}
	//     .Info("抖音订阅清单已加载（相同主播跨群已去重请求）"，@0x1126050,len66)。
	// 键串 live_unique_users/news_unique_users @0x10b0b34/@0x10b0b45；字段名 "error"
	// 来自全局字符串 0x1b91b60（len5）。两次调用的 slice 长度（rbx）分别用于两个计数。
	liveIDs, liveErr := d.subscribedIDs(Live)
	newsIDs, newsErr := d.subscribedIDs(News)
	var subErrs []error
	if liveErr != nil {
		subErrs = append(subErrs, liveErr)
	}
	if newsErr != nil {
		subErrs = append(subErrs, newsErr)
	}
	if len(subErrs) > 0 {
		logger.WithField("error", subErrs).
			Warn("读取抖音订阅清单失败，轮询阶段会继续重试")
	} else {
		logger.WithFields(logrus.Fields{
			"live_unique_users": len(liveIDs),
			"news_unique_users": len(newsIDs),
		}).Info("抖音订阅清单已加载（相同主播跨群已去重请求）")
	}
	// 成品正常成功路径不写 moduleStarted：对全局 0x1bf43ab 做全二进制 xref 扫描只有
	// 4 处写入 —— d73a65=0（Start 入口）与 d73ce7/d74e87/d74ee2=1（三处均为「不完整/
	// 停用」路径）。因此这里不再 Store(true)，正常启动后该标志保持 0。

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
	// 成品 (*Concern).Stop @0xd76040 +0x51 call sync.(*Once).doSlow，闭包 Stop.func1
	// @0xd76380 读 Concern+0x18（authStop）并判非 nil 后 call runtime.closechan。
	// 即 authStopOnce.Do(func(){ if d.authStop != nil { close(d.authStop) } })。
	d.authStopOnce.Do(func() {
		if d.authStop != nil {
			close(d.authStop)
		}
	})
	logger.Tracef("正在停止%v concern", Site)
	logger.Tracef("正在停止%v StateManager", Site)
	d.StateManager.Stop()
	logger.Tracef("%v StateManager已停止", Site)
	// 停止前固化 Cookie 状态，供下次启动装载。
	defer d.persistAuthState("停止抖音模块时保存 Cookie 状态失败")
	logger.Tracef("%v concern已停止", Site)
}

func (d *Concern) GetStateManager() concern.IStateManager {
	return d.StateManager
}

func (c *Concern) filterCard(card *UserPostsResponse_AwemeList) bool {
	// 成品 filterCard @0xd76e42：空作品 Warn("抖音作品列表包含空作品，已安全跳过")
	//（@0x1126090,len51,level=Warn）后返回 false（disasm 后接 xor eax,eax; ret）。
	if card == nil {
		logger.Warn("抖音作品列表包含空作品，已安全跳过")
		return false
	}
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
			// 成品 filterCard @0xd767fa：GetUidFirstTimestamp 缺失时补建基线，补建失败
			// Warn("补建抖音作品订阅时间基线失败")(@0x11260a0,len42,level=Warn)。[推断] 补建值取作品发布时间。
			if setErr := c.StateManager.SetUidFirstTimestampIfNotExist(uid, int64(card.GetCreateTime())); setErr != nil {
				logger.WithField("uid", uid).
					WithError(setErr).
					Warn("补建抖音作品订阅时间基线失败")
			}
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

func NewConcern(notifyChan chan<- concern.Notify) *Concern {
	// 默认是string格式的id
	// 成品 NewConcern @0xd76ec0 +0x20 call runtime.makechan：构造时创建 authStop 通道。
	c := &Concern{notify: notifyChan, authStop: make(chan struct{})}
	sm := &StateManager{StateManager: concern.NewStateManagerWithStringID(Site, notifyChan), concern: c, ExtraKey: NewExtraKey()}
	// 如果要使用int64格式的id，可以用下面的
	c.StateManager = sm
	return c
}

// douyinAuthPersistMutex 串行化状态文件写入（定时同步与停止流程可能并发）。
var douyinAuthPersistMutex sync.Mutex

// persistAuthState 把当前 Cookie Jar 固化到状态文件（原子替换）。
// 证据：成品存在 (*Concern).persistAuthState @ concern.go:963（Lite 增量，640B，
// deferwrap1 @ :965）；关联文案"定时保存抖音 Cookie 状态失败"（CookieCloud 定时同步后）
// 与"停止抖音模块时保存 Cookie 状态失败"（Stop 流程）。
// [推断] 成品签名无法从符号表复原，按最小实现取 reason 字符串参数；调用点见
// cookiecloud.go 的 refreshDouyinCookieCloud 与 concern.go 的 Stop。
func (d *Concern) persistAuthState(reason string) {
	douyinAuthPersistMutex.Lock()
	defer douyinAuthPersistMutex.Unlock()
	if err := saveCurrentDouyinCookieState(); err != nil {
		logger.WithError(err).Warn(reason)
	}
}
