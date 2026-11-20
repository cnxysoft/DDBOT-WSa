package youtube

import (
	"errors"
	"fmt"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"time"
)

var online bool
var logger = utils.GetModuleLogger("youtube")

type Concern struct {
	*StateManager
	cacheStartTs int64
	notify       chan<- concern.Notify
}

func (c *Concern) Site() string {
	return "youtube"
}

func (c *Concern) Types() []concern_type.Type {
	return []concern_type.Type{Live, Video}
}

func (c *Concern) ParseId(s string) (interface{}, error) {
	return s, nil
}

func (c *Concern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

func (c *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	id := _id.(string)
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("id", id)

	err := c.StateManager.CheckGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	info, err := c.FindOrLoad(id, true)
	if err != nil {
		log.Errorf("FindOrLoad error %v", err)
		return nil, fmt.Errorf("查询channel信息失败 %v - %v", id, err)
	}
	for _, v := range info.VideoInfo {
		if err := c.StateManager.AddVideo(v); err != nil {
			log.WithError(err).WithField("video_id", v.VideoId).
				Warn("youtube: AddVideo failed during Add()")
		}
	}
	_, err = c.StateManager.AddGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}
	if ctype.ContainAny(Live) && groupCode != 0 {
		for _, living := range livingVideoInfos(info) {
			living.liveStatusChanged = true
			// Use a non-blocking send: the LSP notify channel is shared and may
			// be saturated; blocking here would deadlock Add() and prevent any
			// future subscription on this group from completing.
			select {
			case c.notify <- NewConcernNotify(groupCode, living):
			default:
				log.WithField("video_id", living.VideoId).
					Warn("youtube notify channel full; drop immediate live notify")
			}
		}
	}
	return concern.NewIdentity(info.ChannelId, info.ChannelName), nil
}

func (c *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	id := _id.(string)
	identity, _ := c.Get(id)
	_, err := c.StateManager.RemoveGroupConcern(groupCode, id, ctype)
	if err == nil {
		allCtype, getErr := c.StateManager.GetConcern(id)
		if getErr != nil || !allCtype.ContainAny(Live) {
			if clearErr := c.StateManager.DeleteLiveState(id); clearErr != nil {
				return identity, clearErr
			}
		}
	}
	if identity == nil {
		identity = concern.NewIdentity(_id, "unknown")
	}
	return identity, err
}

func (c *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	info, err := c.FindInfo(id.(string), false, false)
	if err != nil {
		return nil, err
	}
	return concern.NewIdentity(info.ChannelId, info.ChannelName), nil
}

func (c *Concern) Stop() {
	logger.Trace("正在停止youtube concern")
	logger.Trace("正在停止youtube StateManager")
	c.StateManager.Stop()
	logger.Trace("youtube StateManager已停止")
	logger.Trace("youtube concern已停止")
}

func (c *Concern) Start() error {
	c.UseEmitQueue()
	c.UseFreshFunc(c.fresh())
	c.UseNotifyGeneratorFunc(c.notifyGenerator())
	go func() {
		for msg := range eventbus.BusObj.Subscribe("bot_online") {
			if m, ok := msg.(bool); ok {
				if !online && m {
					c.cacheStartTs = time.Now().Unix()
					logger.Info("BOT已上线，刷新油管订阅模块启动时间")
				}
				online = m
			}
			logger.Debugf("模块 YOUTUBE 收到：bot_online: %v", msg)
		}
	}()
	return c.StateManager.Start()
}

func (c *Concern) fresh() concern.FreshFunc {
	return c.EmitQueueFresher(func(ctype concern_type.Type, id interface{}) ([]concern.Event, error) {
		if ctype.ContainAny(Live.Add(Video)) {
			channelId, ok := id.(string)
			if !ok {
				return nil, errors.New("canst fresh id to string failed")
			}
			infos, err := c.freshInfo(channelId)
			if err != nil {
				return nil, err
			}
			var result []concern.Event
			for _, event := range infos {
				prev, getErr := c.StateManager.GetVideo(event.ChannelId, event.VideoId)
				if err := c.StateManager.AddVideo(event); err != nil {
					event.Logger().Errorf("add video err %v", err)
				}
				if getErr == nil {
					if prev.VideoStatus == event.VideoStatus && prev.VideoType == event.VideoType &&
						prev.VideoTimestamp == event.VideoTimestamp && prev.VideoTitle == event.VideoTitle {
						continue
					}
				}
				result = append(result, event)
			}
			return result, nil
		}
		return nil, fmt.Errorf("unknown concern_type %v", ctype.String())
	})
}

func (c *Concern) notifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, ievent concern.Event) []concern.Notify {
		switch event := ievent.(type) {
		case *VideoInfo:
			log := event.Logger()
			if event.IsVideo() {
				log.WithFields(localutils.GroupLogFields(groupCode)).Debugf("video notify")
			} else if event.IsLive() {
				if event.IsWaiting() {
					log.WithFields(localutils.GroupLogFields(groupCode)).Debugf("live waiting notify")
				} else if event.IsLiving() {
					log.WithFields(localutils.GroupLogFields(groupCode)).Debugf("living notify")
				}
			}
			return []concern.Notify{NewConcernNotify(groupCode, event)}
		default:
			logger.Errorf("unknown EventType %+v", event)
			return nil
		}
	}
}

func (c *Concern) filterCard(card *VideoInfo) bool {
	var tsLimit int64
	if cfg.GetYoutubeOnlyOnlineNotify() {
		tsLimit = c.cacheStartTs
	} else {
		tsLimit = 0
	}
	if card.EffectiveTimestamp() < tsLimit {
		return false
	}
	return true
}

func (c *Concern) freshInfo(channelId string) (result []*VideoInfo, err error) {
	log := logger.WithField("channel_id", channelId)
	oldInfo, _ := c.FindInfo(channelId, false, false)
	newInfo, err := c.FindInfo(channelId, true, false)
	if err != nil {
		log.Errorf("load newInfo failed %v", err)
		return
	}
	return c.diffInfo(oldInfo, newInfo), nil
}

func (c *Concern) diffInfo(oldInfo, newInfo *Info) (result []*VideoInfo) {
	if newInfo == nil {
		return nil
	}
	if oldInfo == nil || oldInfo.VideoInfo == nil {
		// first load, only notify live items and mark active live status changes
		for _, newV := range newInfo.VideoInfo {
			if shouldNotifyLive(newV) {
				if newV.IsLiving() {
					newV.liveStatusChanged = true
				}
				result = append(result, newV)
			}
		}
	} else {
		var videoNotifyCount = 0
		for _, newV := range newInfo.VideoInfo {
			var found bool
			for _, oldV := range oldInfo.VideoInfo {
				if newV.VideoId == oldV.VideoId {
					found = true
					// Handle status transitions: live→video (offline) or video→live (started streaming)
					if newV.IsVideo() && oldV.IsLive() {
						// Live ended, notify as video
						result = append(result, newV)
					} else if newV.IsLive() && oldV.IsVideo() {
						if !shouldNotifyLive(newV) {
							continue
						}
						if newV.IsLiving() {
							newV.liveStatusChanged = true
						}
						result = append(result, newV)
					} else if newV.IsLive() && oldV.IsLive() {
						if !shouldNotifyLive(newV) {
							continue
						}
						if newV.IsWaiting() && oldV.IsWaiting() && newV.VideoTimestamp != oldV.VideoTimestamp {
							// live time changed, notify
							result = append(result, newV)
						} else if newV.IsLiving() && oldV.IsWaiting() {
							// live begin
							newV.liveStatusChanged = true
							result = append(result, newV)
						} else if newV.VideoTitle != oldV.VideoTitle {
							newV.liveTitleChanged = true
							result = append(result, newV)
						}
					}
				}
			}
			if !found {
				if shouldNotifyLive(newV) {
					if newV.IsLiving() {
						newV.liveStatusChanged = true
					}
					result = append(result, newV)
					continue
				}
				if videoNotifyCount == 0 && c.filterCard(newV) {
					result = append(result, newV)
					videoNotifyCount += 1
					// notify video most once
				}
			}
		}
	}
	return result
}

func shouldNotifyLive(v *VideoInfo) bool {
	if v == nil || !v.IsLive() {
		return false
	}
	if v.IsWaiting() {
		return true
	}
	if v.IsLiving() && (v.PublishTimestamp != 0 || v.DurationSeconds != 0) {
		return false
	}
	return v.IsLiving()
}

func (c *Concern) FindInfo(channelId string, load bool, addMode bool) (*Info, error) {
	var info *Info
	if load {
		vi, err := XFetchInfo(channelId)
		if err != nil {
			return nil, err
		}
		info = NewInfo(vi, addMode)
		if err := c.StateManager.AddInfo(info); err != nil {
			logger.WithField("channel_id", channelId).
				WithError(err).Warn("youtube: AddInfo failed during FindInfo()")
		}
	}

	if info != nil {
		return info, nil
	}
	return c.GetInfo(channelId)
}

func (c *Concern) FindOrLoad(channelId string, addMode bool) (*Info, error) {
	info, _ := c.FindInfo(channelId, false, addMode)
	if info == nil {
		return c.FindInfo(channelId, true, addMode)
	} else {
		return info, nil
	}
}

func livingVideoInfos(info *Info) []*VideoInfo {
	if info == nil {
		return nil
	}
	var preferred []*VideoInfo
	var fallback []*VideoInfo
	preferredIndexByID := make(map[string]int)
	fallbackIndexByID := make(map[string]int)
	for _, v := range info.VideoInfo {
		if !shouldNotifyLive(v) {
			continue
		}
		target := &preferred
		indexByID := preferredIndexByID
		if v.HeaderSummary {
			target = &fallback
			indexByID = fallbackIndexByID
		}
		if idx, ok := indexByID[v.VideoId]; ok {
			if videoInfoQualityScore(v) > videoInfoQualityScore((*target)[idx]) {
				(*target)[idx] = v
			}
			continue
		}
		indexByID[v.VideoId] = len(*target)
		*target = append(*target, v)
	}
	if len(preferred) > 0 {
		return preferred
	}
	return fallback
}

func NewConcern(notify chan<- concern.Notify) *Concern {
	return &Concern{
		notify:       notify,
		StateManager: NewStateManager(notify),
	}
}
