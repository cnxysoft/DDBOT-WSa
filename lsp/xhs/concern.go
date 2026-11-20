package xhs

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/tidwall/buntdb"
)

var logger = utils.GetModuleLogger("xhs-concern")

const (
	Site                                    = "xhs"
	LiveType              concern_type.Type = "live"
	webLiveRoomPrefix                       = "https://www.xiaohongshu.com/livestream/"
	webUserProfilePrefix                    = "https://www.xiaohongshu.com/user/profile/"
	profileFeedXsecSource                   = "pc_user"
	xhsRecentNoteWindow                     = 50
)

type Concern struct {
	*StateManager
	cacheStartTs int64
	client       *Client
	notify       chan<- concern.Notify
	stop         chan interface{}
}

type candidateNote struct {
	note           UserProfileNote
	canUseBoundary bool
}

func (c *Concern) Site() string {
	return Site
}

func (c *Concern) Types() []concern_type.Type {
	return []concern_type.Type{LiveType, NewsType}
}

func (c *Concern) ParseId(s string) (interface{}, error) {
	id := strings.TrimSpace(s)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	return id, nil
}

func (c *Concern) ResolveSubscribedID(groupCode int64, rawID string, ctype concern_type.Type) (interface{}, error) {
	keyword := strings.TrimSpace(rawID)
	if keyword == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := c.StateManager.CheckGroupConcern(groupCode, keyword, ctype); err == concern.ErrAlreadyExists {
		return keyword, nil
	}
	info, err := c.findCachedUserInfoByKeywordInGroup(groupCode, ctype, keyword)
	if err != nil {
		return nil, err
	}
	if info == nil || strings.TrimSpace(info.Uid) == "" {
		return nil, fmt.Errorf("local subscribed xhs user %q not found", keyword)
	}
	return info.Uid, nil
}

func (c *Concern) GetStateManager() concern.IStateManager {
	return c.StateManager
}

func (c *Concern) Start() error {
	cookies := make(map[string]string)
	if a1 := config.GlobalConfig.GetString("xhs.cookies.a1"); a1 != "" {
		cookies["a1"] = a1
	}
	if webSession := config.GlobalConfig.GetString("xhs.cookies.web_session"); webSession != "" {
		cookies["web_session"] = webSession
	}
	if webId := config.GlobalConfig.GetString("xhs.cookies.webId"); webId != "" {
		cookies["webId"] = webId
	}
	if len(cookies) > 0 {
		c.client.SetCookies(cookies)
	}

	c.StateManager.UseEmitQueueWithSiteInterval(Site)
	c.StateManager.UseFreshFunc(c.EmitQueueFresher(func(p concern_type.Type, id interface{}) ([]concern.Event, error) {
		return c.freshEventsForTypes(p, id.(string))
	}))
	c.StateManager.UseNotifyGeneratorFunc(c.notifyGenerator())
	return c.StateManager.Start()
}

func (c *Concern) Stop() {
	logger.Tracef("stopping %v concern", Site)
	c.StateManager.Stop()
	logger.Tracef("%v concern stopped", Site)
}

func (c *Concern) Add(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	keyword := _id.(string)
	info, err := c.resolveCanonicalUserInfo(keyword)
	if err != nil {
		logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("keyword", keyword).
			Errorf("resolveCanonicalUserInfo error %v", err)
		return nil, fmt.Errorf("failed to resolve xhs user %q: %w", keyword, err)
	}
	id := info.Uid
	log := logger.WithFields(localutils.GroupLogFields(groupCode)).WithField("id", id).WithField("keyword", keyword)

	err = c.StateManager.CheckGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}

	_, err = c.StateManager.AddGroupConcern(groupCode, id, ctype)
	if err != nil {
		return nil, err
	}

	if ctype.ContainAny(LiveType) {
		liveInfo, freshErr := c.freshLive(id)
		if freshErr != nil {
			log.Errorf("freshLive error %v", freshErr)
		} else if liveInfo != nil && liveInfo.Living() {
			if ctx == nil {
				log.Warn("live subscription resolved to already-living user but ctx is nil; skip immediate notify")
			} else if ctx.GetTarget().TargetType().IsGroup() {
				defer c.GroupWatchNotify(groupCode, id)
			} else if ctx.GetTarget().TargetType().IsPrivate() {
				defer ctx.Send(mmsg.NewText("detected that this user is already live; the current subscription was added in private chat, so this live will not be pushed into the group and only future live sessions will be notified."))
			}
		}
	}
	if ctype.ContainAny(NewsType) {
		if _, freshErr := c.freshNews(id); freshErr != nil {
			log.Errorf("freshNews error %v", freshErr)
		}
	}

	return info, nil
}

func (c *Concern) Remove(ctx mmsg.IMsgCtx, groupCode int64, _id interface{}, ctype concern_type.Type) (concern.IdentityInfo, error) {
	_ = ctx
	rawID := _id.(string)
	id := rawID
	localKnown := false
	if _, err := c.GetUserInfo(id); err == nil {
		localKnown = true
	}
	if !localKnown {
		if currentConcern, err := c.StateManager.GetConcern(id); err == nil && !currentConcern.Empty() {
			localKnown = true
		}
	}
	if !localKnown {
		info, err := c.resolveCanonicalUserInfo(rawID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve xhs user %q: %w", rawID, err)
		}
		id = info.Uid
	}

	identity, _ := c.Get(id)
	_, err := c.StateManager.RemoveGroupConcern(groupCode, id, ctype)
	if identity == nil {
		identity = concern.NewIdentity(id, "unknown")
	}
	if err != nil {
		return identity, err
	}
	remainingConcern, remainingErr := c.StateManager.GetConcern(id)
	if remainingErr != nil && !errors.Is(remainingErr, buntdb.ErrNotFound) {
		return identity, remainingErr
	}
	if remainingConcern.Empty() {
		if err := c.RemoveUserInfo(id); err != nil && !errors.Is(err, buntdb.ErrNotFound) {
			logger.WithField("uid", id).Errorf("RemoveUserInfo error %v", err)
		}
		if err := c.RemoveNewsInfo(id); err != nil && !errors.Is(err, buntdb.ErrNotFound) {
			logger.WithField("uid", id).Errorf("RemoveNewsInfo error %v", err)
		}
		if err := c.RemoveLiveInfo(id); err != nil && !errors.Is(err, buntdb.ErrNotFound) {
			logger.WithField("uid", id).Errorf("RemoveLiveInfo error %v", err)
		}
		return identity, nil
	}
	if !remainingConcern.ContainAny(NewsType) {
		if err := c.RemoveNewsInfo(id); err != nil && !errors.Is(err, buntdb.ErrNotFound) {
			logger.WithField("uid", id).Errorf("RemoveNewsInfo error %v", err)
		}
	}
	if !remainingConcern.ContainAny(LiveType) {
		if err := c.RemoveLiveInfo(id); err != nil && !errors.Is(err, buntdb.ErrNotFound) {
			logger.WithField("uid", id).Errorf("RemoveLiveInfo error %v", err)
		}
	}
	return identity, nil
}

func (c *Concern) GroupWatchNotify(groupCode int64, uid string) {
	liveInfo, _ := c.GetLiveInfo(uid)
	if liveInfo != nil && liveInfo.Living() {
		liveInfoForNotify := liveInfo.CloneWithStatusChanged()
		notify := NewConcernLiveNotify(groupCode, liveInfoForNotify)
		if c.StateManager.IsExtendNotify(notify) {
			notify.ExtendNotify = true
		}
		c.notify <- notify
	}
}

func (c *Concern) Get(id interface{}) (concern.IdentityInfo, error) {
	return c.GetUserInfo(id.(string))
}

func (c *Concern) FindOrLoadUserInfo(uid string) (*UserInfo, error) {
	info, _ := c.GetUserInfo(uid)
	if info != nil {
		return info, nil
	}
	return c.FindUserInfo(uid, true)
}

func (c *Concern) FindUserInfo(uid string, load bool) (*UserInfo, error) {
	var cached *UserInfo
	if info, err := c.GetUserInfo(uid); err == nil {
		cached = info
		if info != nil && !load {
			return info, nil
		}
	}
	info, _, err := c.fetchAndStoreUserInfo(uid, cached)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (c *Concern) freshLive(uid string) (*LiveInfo, error) {
	log := logger.WithField("uid", uid)

	oldInfo, _ := c.GetLiveInfo(uid)
	cachedUserInfo, _ := c.GetUserInfo(uid)
	userInfo, user, err := c.fetchAndStoreUserInfo(uid, cachedUserInfo)
	if err != nil {
		return nil, fmt.Errorf("FindOrLoadUserInfo error: %w", err)
	}

	liveInfo := &LiveInfo{
		UserInfo:  *userInfo,
		Status:    c.normalizeLiveStatus(user, oldInfo),
		LiveTitle: userInfo.Name,
		Cover:     userInfo.Face,
		Url:       userInfo.ProfileURL,
	}
	if oldInfo != nil {
		if liveInfo.LiveTitle == "" {
			liveInfo.LiveTitle = oldInfo.LiveTitle
		}
		if liveInfo.Cover == "" {
			liveInfo.Cover = oldInfo.Cover
		}
		if liveInfo.Url == "" {
			liveInfo.Url = oldInfo.Url
		}
	}
	if user.LiveInfo != nil {
		if user.LiveInfo.RoomID != "" {
			liveInfo.RoomId = user.LiveInfo.RoomID
			if liveInfo.Living() {
				liveInfo.Url = buildWebLiveRoomURL(user.LiveInfo.RoomID)
			}
		}
		if user.LiveInfo.UserID != "" {
			liveInfo.UserId = user.LiveInfo.UserID
		}
	}
	if liveInfo.Living() {
		if err := c.fillLiveDetailsFromRoomInfo(liveInfo); err != nil {
			return nil, err
		}
	}

	if oldInfo == nil {
		liveInfo.liveStatusChanged = liveInfo.Living()
	} else {
		if oldInfo.Status != liveInfo.Status && oldInfo.Living() != liveInfo.Living() {
			liveInfo.liveStatusChanged = true
		}
		if oldInfo.LiveTitle != liveInfo.LiveTitle {
			liveInfo.liveTitleChanged = true
		}
	}

	err = c.AddLiveInfo(liveInfo)
	if err != nil {
		log.Errorf("AddLiveInfo error %v", err)
	}

	return liveInfo, nil
}

func (c *Concern) freshNews(uid string) (*NewsInfo, error) {
	log := logger.WithField("uid", uid)
	pollTs := time.Now().Unix()

	userInfo, err := c.FindOrLoadUserInfo(uid)
	if err != nil {
		return nil, fmt.Errorf("FindOrLoadUserInfo error: %w", err)
	}

	profile, err := c.client.GetUserProfile(firstNonEmpty(userInfo.UserId, uid))
	if err != nil {
		return nil, fmt.Errorf("GetUserProfile error: %w", err)
	}

	userInfo = c.mergeUserInfoWithProfile(userInfo, profile, uid)
	if err := c.AddUserInfo(userInfo); err != nil {
		log.Errorf("AddUserInfo error %v", err)
	}

	newsInfo := &NewsInfo{UserInfo: *userInfo}
	notes := profile.FlattenNotes()
	if len(notes) > 0 {
		newsInfo.LatestNoteID = canonicalNoteID(notes[0])
	}

	oldNewsInfo, err := c.GetNewsInfo(uid)
	if err != nil {
		if errors.Is(err, buntdb.ErrNotFound) {
			newsInfo.RecentNoteIDs = buildRecentNoteIDs(notes, nil)
			newsInfo.LatestPublishedAt = pollTs
			if err := c.markExistingNotes(notes); err != nil {
				return nil, err
			}
			if err := c.AddNewsInfo(newsInfo); err != nil {
				log.Errorf("AddNewsInfo error %v", err)
				return nil, err
			}
			return newsInfo, nil
		}
		return nil, err
	}
	if newsInfo.LatestNoteID == "" {
		newsInfo.LatestNoteID = oldNewsInfo.LatestNoteID
	}
	newsInfo.LatestPublishedAt = oldNewsInfo.LatestPublishedAt
	newsInfo.RecentNoteIDs = append([]string(nil), oldNewsInfo.RecentNoteIDs...)

	candidates := collectCandidateNotes(notes, oldNewsInfo)
	retryableSkipped := make(map[string]struct{})
	latestPublishedAt := newsInfo.LatestPublishedAt
	onlyOnline := cfg.GetXHSOnlyOnlineNotify()
	for idx := len(candidates) - 1; idx >= 0; idx-- {
		noteInfo := c.buildNoteInfo(userInfo, candidates[idx].note)
		if noteInfo == nil {
			continue
		}
		if err := c.enrichNoteInfoWithFeed(noteInfo); err != nil {
			log.WithField("note_id", noteInfo.NoteID).Warnf("GetFeedNote fallback to profile note: %v", err)
		}
		pass, retryable, effectivePublishedAt := c.shouldNotifyNote(noteInfo, candidates[idx].canUseBoundary, latestPublishedAt, onlyOnline, pollTs)
		if retryable {
			retryableSkipped[noteInfo.NoteID] = struct{}{}
			continue
		}
		if !pass {
			continue
		}
		if effectivePublishedAt > 0 {
			noteInfo.PublishedAt = effectivePublishedAt
			if effectivePublishedAt > latestPublishedAt {
				latestPublishedAt = effectivePublishedAt
			}
		}
		replaced, err := c.MarkNoteId(noteInfo.NoteID)
		if err != nil {
			log.WithField("note_id", noteInfo.NoteID).Errorf("MarkNoteId error %v", err)
			return nil, err
		}
		if replaced {
			continue
		}
		newsInfo.Notes = append(newsInfo.Notes, noteInfo)
	}
	newsInfo.LatestPublishedAt = latestPublishedAt
	if len(notes) > 0 {
		newsInfo.RecentNoteIDs = buildRecentNoteIDs(notes, retryableSkipped)
	}

	if err := c.AddNewsInfo(newsInfo); err != nil {
		log.Errorf("AddNewsInfo error %v", err)
		return nil, err
	}
	return newsInfo, nil
}

func (c *Concern) freshEventsForTypes(types concern_type.Type, uid string) ([]concern.Event, error) {
	var events []concern.Event
	var firstErr error
	if types.ContainAny(LiveType) {
		liveInfo, err := c.freshLive(uid)
		if err != nil {
			firstErr = err
			logger.WithField("uid", uid).Warnf("freshLive error %v", err)
		} else if liveInfo != nil {
			events = append(events, liveInfo)
		}
	}
	if types.ContainAny(NewsType) {
		newsInfo, err := c.freshNews(uid)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logger.WithField("uid", uid).Warnf("freshNews error %v", err)
		} else if newsInfo != nil && len(newsInfo.Notes) > 0 {
			events = append(events, newsInfo)
		}
	}
	if len(events) > 0 {
		return events, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if len(events) == 0 {
		return nil, nil
	}
	return events, nil
}

func (c *Concern) fillLiveDetailsFromRoomInfo(liveInfo *LiveInfo) error {
	if liveInfo == nil {
		return fmt.Errorf("live info is nil")
	}
	if strings.TrimSpace(liveInfo.RoomId) == "" {
		return fmt.Errorf("room_id is required for room info lookup")
	}
	requestUserID := strings.TrimSpace(liveInfo.UserId)
	if requestUserID == "" {
		requestUserID = strings.TrimSpace(liveInfo.Uid)
	}
	if requestUserID == "" {
		return fmt.Errorf("request_user_id is required for room info lookup")
	}

	resp, err := c.client.GetCurrentRoomInfo(liveInfo.RoomId, requestUserID)
	if err != nil {
		return fmt.Errorf("GetCurrentRoomInfo error: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("current_room_info returned error: %s", resp.Msg)
	}
	if resp.Data == nil || resp.Data.RoomInfo == nil {
		return fmt.Errorf("current_room_info returned empty room data")
	}

	if host := resp.Data.HostInfo; host != nil {
		if host.NickName != "" {
			liveInfo.Name = host.NickName
		}
		if host.Avatar != "" {
			liveInfo.Face = host.Avatar
		}
		if host.UserID != "" {
			liveInfo.UserId = host.UserID
		}
	}
	room := resp.Data.RoomInfo
	if room.RoomID != "" {
		liveInfo.RoomId = room.RoomID
		liveInfo.Url = buildWebLiveRoomURL(room.RoomID)
	}
	if room.RoomTitle != "" {
		liveInfo.LiveTitle = room.RoomTitle
	}
	if room.RoomCover != "" {
		liveInfo.Cover = room.RoomCover
	}
	liveInfo.DisplayMemberCount = room.DisplayMemberCount
	liveInfo.DisplayPraiseCount = room.DisplayPraiseCount
	liveInfo.DisplayViewerCount = room.DisplayViewerCount
	if status := normalizeRawLiveStatus(room.Status); status.IsKnown() {
		liveInfo.Status = status
	}
	if !liveInfo.Living() {
		if liveInfo.ProfileURL != "" {
			liveInfo.Url = liveInfo.ProfileURL
		} else if liveInfo.UserId != "" {
			liveInfo.Url = buildWebUserProfileURL(liveInfo.UserId)
		} else {
			liveInfo.Url = buildWebUserProfileURL(liveInfo.Uid)
		}
	}
	return nil
}

func buildWebLiveRoomURL(roomID string) string {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return ""
	}
	return webLiveRoomPrefix + roomID
}

func buildWebUserProfileURL(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	return webUserProfilePrefix + userID
}

func buildWebNoteURL(_ string, noteID string, _ string) string {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return ""
	}
	return fmt.Sprintf("%sexplore/%s", WebBaseURL+"/", noteID)
}

func (c *Concern) resolveCanonicalUserInfo(keyword string) (*UserInfo, error) {
	trimmed := strings.TrimSpace(keyword)
	if trimmed == "" {
		return nil, fmt.Errorf("keyword is required")
	}
	if info, err := c.GetUserInfo(trimmed); err == nil && info != nil {
		return info, nil
	}
	if info, err := c.findCachedUserInfoByKeyword(trimmed); err != nil {
		return nil, err
	} else if info != nil {
		return info, nil
	}
	info, _, err := c.fetchAndStoreUserInfo(trimmed, nil)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (c *Concern) fetchAndStoreUserInfo(keyword string, cached *UserInfo) (*UserInfo, *SearchOneboxUser, error) {
	user, err := c.client.FindExactUser(keyword)
	if err != nil {
		return nil, nil, err
	}
	info := c.mergeUserInfo(cached, user)
	if err := c.AddUserInfo(info); err != nil {
		logger.WithField("uid", info.Uid).Errorf("AddUserInfo error %v", err)
	}
	return info, user, nil
}

func (c *Concern) mergeUserInfo(cached *UserInfo, user *SearchOneboxUser) *UserInfo {
	info := &UserInfo{}
	if cached != nil {
		*info = *cached
	}
	if user == nil {
		return info
	}
	info.Uid = user.ID
	if user.RedID != "" {
		info.RedID = user.RedID
	}
	if user.Title != "" {
		info.Name = user.Title
	}
	if user.Image != "" {
		info.Face = user.Image
	}
	if info.UserId == "" {
		info.UserId = user.ID
	}
	if user.LiveInfo != nil {
		if user.LiveInfo.RoomID != "" {
			info.RoomId = user.LiveInfo.RoomID
		} else if normalizeRawLiveStatus(user.LiveInfo.Status) == LiveStatus_NoLiving {
			info.RoomId = ""
		}
		if user.LiveInfo.UserID != "" {
			info.UserId = user.LiveInfo.UserID
		}
	}
	if info.UserId == "" {
		info.UserId = user.ID
	}
	info.ProfileURL = buildWebUserProfileURL(firstNonEmpty(info.UserId, user.ID))
	return info
}

func (c *Concern) mergeUserInfoWithProfile(cached *UserInfo, profile *UserProfilePageUser, uid string) *UserInfo {
	info := &UserInfo{}
	if cached != nil {
		*info = *cached
	}
	if info.Uid == "" {
		info.Uid = strings.TrimSpace(uid)
	}
	if info.UserId == "" {
		info.UserId = info.Uid
	}
	if profile != nil && profile.UserPageData != nil && profile.UserPageData.BasicInfo != nil {
		basicInfo := profile.UserPageData.BasicInfo
		if redID := strings.TrimSpace(basicInfo.RedID); redID != "" {
			info.RedID = redID
		}
		if nickname := strings.TrimSpace(basicInfo.Nickname); nickname != "" {
			info.Name = nickname
		}
		if face := normalizeMediaURL(firstNonEmpty(basicInfo.Images, basicInfo.ImageB)); face != "" {
			info.Face = face
		}
	}
	info.ProfileURL = buildWebUserProfileURL(info.UserId)
	return info
}

func (c *Concern) normalizeLiveStatus(user *SearchOneboxUser, oldInfo *LiveInfo) LiveStatus {
	if user == nil || user.LiveInfo == nil {
		if oldInfo != nil {
			return oldInfo.Status
		}
		return LiveStatus_Unknown
	}

	status := normalizeRawLiveStatus(user.LiveInfo.Status)
	if status.IsKnown() {
		return status
	}
	if oldInfo != nil {
		return oldInfo.Status
	}
	return status
}

func (c *Concern) findCachedUserInfoByKeyword(keyword string) (*UserInfo, error) {
	return c.findCachedUserInfoByKeywordInGroup(0, concern_type.Empty, keyword)
}

func (c *Concern) findCachedUserInfoByKeywordInGroup(targetGroupCode int64, targetType concern_type.Type, keyword string) (*UserInfo, error) {
	listConcernState := func() ([]interface{}, []concern_type.Type, error) {
		_, ids, ctypes, err := c.StateManager.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool {
			if targetGroupCode != 0 && targetGroupCode != groupCode {
				return false
			}
			return targetType.Empty() || p.ContainAll(targetType)
		})
		return ids, ctypes, err
	}

	ids, ctypes, err := listConcernState()
	if err != nil {
		if errors.Is(err, buntdb.ErrNotFound) {
			c.StateManager.FreshIndex()
			ids, ctypes, err = listConcernState()
			if errors.Is(err, buntdb.ErrNotFound) {
				return nil, nil
			}
		}
		if err != nil {
			if errors.Is(err, buntdb.ErrNotFound) {
				return nil, nil
			}
			return nil, err
		}
	}
	ids, _, err = c.StateManager.GroupTypeById(ids, ctypes)
	if err != nil {
		return nil, err
	}

	var exactIdentityMatch *UserInfo
	var redIDMatches []*UserInfo
	var titleMatches []*UserInfo
	for _, id := range ids {
		uid, ok := id.(string)
		if !ok || uid == "" {
			continue
		}
		info, err := c.GetUserInfo(uid)
		if err != nil || info == nil {
			continue
		}
		if keyword == strings.TrimSpace(info.Uid) || keyword == strings.TrimSpace(info.UserId) {
			if exactIdentityMatch != nil && exactIdentityMatch.Uid != info.Uid {
				return nil, fmt.Errorf("multiple cached xhs users match %q", keyword)
			}
			exactIdentityMatch = info
			continue
		}
		if keyword == strings.TrimSpace(info.RedID) {
			redIDMatches = append(redIDMatches, info)
			continue
		}
		if keyword == strings.TrimSpace(info.Name) {
			titleMatches = append(titleMatches, info)
		}
	}
	if exactIdentityMatch != nil {
		return exactIdentityMatch, nil
	}
	if len(redIDMatches) == 1 {
		return redIDMatches[0], nil
	}
	if len(redIDMatches) > 1 {
		return nil, fmt.Errorf("multiple cached xhs users match red_id %q", keyword)
	}
	if len(titleMatches) == 1 {
		return titleMatches[0], nil
	}
	if len(titleMatches) > 1 {
		return nil, fmt.Errorf("multiple cached xhs users match nickname %q", keyword)
	}
	return nil, nil
}

func normalizeRawLiveStatus(raw int) LiveStatus {
	switch raw {
	case int(LiveStatus_Living):
		return LiveStatus_Living
	case int(LiveStatus_NoLiving), 0:
		return LiveStatus_NoLiving
	default:
		return LiveStatus_Unknown
	}
}

func (c *Concern) notifyGenerator() concern.NotifyGeneratorFunc {
	return func(groupCode int64, ievent concern.Event) []concern.Notify {
		var result []concern.Notify
		switch live := ievent.(type) {
		case *LiveInfo:
			notify := NewConcernLiveNotify(groupCode, live)
			result = append(result, notify)
		case *NewsInfo:
			result = append(result, flattenNewsNotifies(NewConcernNewsNotify(groupCode, live))...)
		}
		return result
	}
}

func (c *Concern) markExistingNotes(notes []UserProfileNote) error {
	for _, note := range notes {
		noteID := canonicalNoteID(note)
		if noteID == "" {
			continue
		}
		if _, err := c.MarkNoteId(noteID); err != nil {
			return err
		}
	}
	return nil
}

func (c *Concern) shouldNotifyNote(note *NoteInfo, canUseBoundary bool, lastPublishedAt int64, onlyOnline bool, fallbackPublishedAt int64) (pass bool, retryable bool, effectivePublishedAt int64) {
	if note == nil || strings.TrimSpace(note.NoteID) == "" {
		return false, false, 0
	}
	publishedAt := note.PublishedAt
	if onlyOnline {
		if publishedAt <= 0 {
			return false, true, 0
		}
		if publishedAt <= c.cacheStartTs {
			return false, false, publishedAt
		}
	}
	if publishedAt <= 0 {
		if canUseBoundary && !onlyOnline {
			return true, false, fallbackPublishedAt
		}
		return false, true, 0
	}
	if lastPublishedAt > 0 && publishedAt <= lastPublishedAt {
		return false, false, publishedAt
	}
	return true, false, publishedAt
}

func collectCandidateNotes(notes []UserProfileNote, oldNewsInfo *NewsInfo) []candidateNote {
	if len(notes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(notes))
	if oldNewsInfo != nil {
		for _, noteID := range oldNewsInfo.RecentNoteIDs {
			noteID = strings.TrimSpace(noteID)
			if noteID == "" {
				continue
			}
			seen[noteID] = struct{}{}
		}
		if len(oldNewsInfo.RecentNoteIDs) == 0 && oldNewsInfo.LatestPublishedAt == 0 && oldNewsInfo.LatestNoteID != "" {
			seen[strings.TrimSpace(oldNewsInfo.LatestNoteID)] = struct{}{}
		}
	}
	firstSeenIndex := -1
	for idx, note := range notes {
		noteID := canonicalNoteID(note)
		if noteID == "" {
			continue
		}
		if _, ok := seen[noteID]; ok {
			firstSeenIndex = idx
			break
		}
	}
	var candidates []candidateNote
	for idx, note := range notes {
		noteID := canonicalNoteID(note)
		if noteID == "" {
			continue
		}
		if _, ok := seen[noteID]; ok {
			continue
		}
		candidates = append(candidates, candidateNote{
			note:           note,
			canUseBoundary: firstSeenIndex >= 0 && idx < firstSeenIndex,
		})
	}
	return candidates
}

func buildRecentNoteIDs(notes []UserProfileNote, excluded map[string]struct{}) []string {
	if len(notes) == 0 {
		return nil
	}
	recent := make([]string, 0, minInt(len(notes), xhsRecentNoteWindow))
	seen := make(map[string]struct{}, len(notes))
	for _, note := range notes {
		noteID := canonicalNoteID(note)
		if noteID == "" {
			continue
		}
		if excluded != nil {
			if _, ok := excluded[noteID]; ok {
				continue
			}
		}
		if _, ok := seen[noteID]; ok {
			continue
		}
		seen[noteID] = struct{}{}
		recent = append(recent, noteID)
		if len(recent) >= xhsRecentNoteWindow {
			break
		}
	}
	if len(recent) == 0 {
		return nil
	}
	return recent
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *Concern) buildNoteInfo(userInfo *UserInfo, note UserProfileNote) *NoteInfo {
	if userInfo == nil || note.NoteCard == nil {
		return nil
	}
	card := note.NoteCard
	userID := strings.TrimSpace(userInfo.UserId)
	if card.User != nil && strings.TrimSpace(card.User.UserID) != "" {
		userID = strings.TrimSpace(card.User.UserID)
	}
	info := &NoteInfo{
		UserInfo:    *userInfo,
		NoteID:      canonicalNoteID(note),
		XsecToken:   firstNonEmpty(card.XsecToken, note.XsecToken),
		NoteType:    strings.TrimSpace(card.Type),
		Title:       strings.TrimSpace(card.DisplayTitle),
		Cover:       resolveProfileNoteCover(card.Cover),
		Pictures:    compactURLs(resolveProfileNotePictures(card.Cover)),
		PublishedAt: 0,
	}
	if card.User != nil {
		if name := strings.TrimSpace(firstNonEmpty(card.User.Nickname, card.User.NickName)); name != "" {
			info.Name = name
		}
		if avatar := normalizeMediaURL(card.User.Avatar); avatar != "" {
			info.Face = avatar
		}
	}
	if userID != "" {
		info.UserId = userID
	}
	info.Url = buildWebNoteURL(info.UserId, info.NoteID, info.XsecToken)
	if info.Url == "" {
		info.Url = buildWebUserProfileURL(info.UserId)
	}
	return info
}

func (c *Concern) enrichNoteInfoWithFeed(noteInfo *NoteInfo) error {
	if noteInfo == nil {
		return fmt.Errorf("note info is nil")
	}
	if strings.TrimSpace(noteInfo.NoteID) == "" {
		return fmt.Errorf("note_id is required")
	}
	if strings.TrimSpace(noteInfo.XsecToken) == "" {
		return fmt.Errorf("xsec_token is required")
	}

	resp, err := c.client.GetFeedNote(noteInfo.NoteID, noteInfo.XsecToken, profileFeedXsecSource)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("feed returned error: %s", resp.Msg)
	}
	if resp.Data == nil || len(resp.Data.Items) == 0 || resp.Data.Items[0].NoteCard == nil {
		return fmt.Errorf("feed returned empty note data")
	}

	card := resp.Data.Items[0].NoteCard
	if noteID := strings.TrimSpace(firstNonEmpty(card.NoteID, resp.Data.Items[0].ID)); noteID != "" {
		noteInfo.NoteID = noteID
	}
	if noteType := strings.TrimSpace(card.Type); noteType != "" {
		noteInfo.NoteType = noteType
	}
	if title := strings.TrimSpace(card.Title); title != "" {
		noteInfo.Title = title
	}
	if desc := strings.TrimSpace(card.Desc); desc != "" {
		noteInfo.Desc = desc
	}
	if card.Time > 0 {
		noteInfo.PublishedAt = card.Time
	}
	if card.User != nil {
		if userID := strings.TrimSpace(card.User.UserID); userID != "" {
			noteInfo.UserId = userID
		}
		if name := strings.TrimSpace(card.User.Nickname); name != "" {
			noteInfo.Name = name
		}
		if avatar := normalizeMediaURL(card.User.Avatar); avatar != "" {
			noteInfo.Face = avatar
		}
		if token := strings.TrimSpace(card.User.XsecToken); token != "" {
			noteInfo.XsecToken = token
		}
	}
	pictures := resolveFeedPictures(card)
	if len(pictures) > 0 {
		noteInfo.Pictures = pictures
		noteInfo.Cover = pictures[0]
	}
	if url := buildWebNoteURL(noteInfo.UserId, noteInfo.NoteID, noteInfo.XsecToken); url != "" {
		noteInfo.Url = url
	}
	return nil
}

func resolveProfileNoteCover(cover *UserProfileNoteCover) string {
	if cover == nil {
		return ""
	}
	if url := normalizeMediaURL(cover.URLDefault); url != "" {
		return url
	}
	if url := normalizeMediaURL(cover.URLPre); url != "" {
		return url
	}
	if url := normalizeMediaURL(cover.URL); url != "" {
		return url
	}
	for _, info := range cover.InfoList {
		if url := normalizeMediaURL(info.URL); url != "" {
			return url
		}
	}
	return ""
}

func resolveProfileNotePictures(cover *UserProfileNoteCover) []string {
	if cover == nil {
		return nil
	}
	var pictures []string
	if url := normalizeMediaURL(cover.URLDefault); url != "" {
		pictures = append(pictures, url)
	}
	if url := normalizeMediaURL(cover.URLPre); url != "" {
		pictures = append(pictures, url)
	}
	if url := normalizeMediaURL(cover.URL); url != "" {
		pictures = append(pictures, url)
	}
	for _, info := range cover.InfoList {
		if url := normalizeMediaURL(info.URL); url != "" {
			pictures = append(pictures, url)
		}
	}
	return pictures
}

func resolveFeedPictures(card *FeedNoteCard) []string {
	if card == nil {
		return nil
	}
	var pictures []string
	for _, image := range card.ImageList {
		if url := normalizeMediaURL(image.URLDefault); url != "" {
			pictures = append(pictures, url)
			continue
		}
		if url := normalizeMediaURL(image.URLPre); url != "" {
			pictures = append(pictures, url)
			continue
		}
		if url := normalizeMediaURL(image.URL); url != "" {
			pictures = append(pictures, url)
			continue
		}
		for _, info := range image.InfoList {
			if url := normalizeMediaURL(info.URL); url != "" {
				pictures = append(pictures, url)
				break
			}
		}
	}
	return compactURLs(pictures)
}

func compactURLs(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}
	result := make([]string, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	for _, raw := range urls {
		url := normalizeMediaURL(raw)
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		result = append(result, url)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeMediaURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func canonicalNoteID(note UserProfileNote) string {
	if note.NoteCard == nil {
		return firstNonEmpty(note.ID)
	}
	return firstNonEmpty(note.NoteCard.NoteID, note.ID)
}

func flattenNewsNotifies(notifies []*ConcernNewsNotify) []concern.Notify {
	result := make([]concern.Notify, 0, len(notifies))
	for _, notify := range notifies {
		result = append(result, notify)
	}
	return result
}

func NewConcern(notify chan<- concern.Notify) *Concern {
	c := &Concern{
		StateManager: NewStateManager(notify),
		client:       NewClient(nil),
		notify:       notify,
		stop:         make(chan interface{}),
		cacheStartTs: time.Now().Unix(),
	}
	return c
}
