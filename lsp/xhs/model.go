package xhs

import (
	"strings"
	"sync"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/sirupsen/logrus"
)

type UserInfo struct {
	Uid        string `json:"uid"`
	RedID      string `json:"red_id"`
	Name       string `json:"name"`
	Face       string `json:"face"`
	RoomId     string `json:"room_id"`
	UserId     string `json:"user_id"`
	ProfileURL string `json:"profile_url"`
}

func (u *UserInfo) GetUid() interface{} {
	return u.Uid
}

func (u *UserInfo) GetName() string {
	if u == nil {
		return ""
	}
	return u.Name
}

func (u *UserInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site": Site,
		"Uid":  u.Uid,
		"Name": u.Name,
	})
}

type LiveStatus int

const (
	NewsType            concern_type.Type = "news"
	LiveStatus_Living   LiveStatus        = 2
	LiveStatus_NoLiving LiveStatus        = 3
	LiveStatus_Unknown  LiveStatus        = 5
	NoteTypeVideo                         = "video"
	NoteTypeNormal                        = "normal"
)

func (s LiveStatus) String() string {
	switch s {
	case LiveStatus_Living:
		return "living"
	case LiveStatus_NoLiving:
		return "not_living"
	default:
		return "unknown"
	}
}

func (s LiveStatus) IsKnown() bool {
	switch s {
	case LiveStatus_Living, LiveStatus_NoLiving:
		return true
	default:
		return false
	}
}

type LiveInfo struct {
	UserInfo
	Status             LiveStatus `json:"status"`
	LiveTitle          string     `json:"live_title"`
	Url                string     `json:"url"`
	Cover              string     `json:"cover"`
	DisplayMemberCount string     `json:"display_member_count"`
	DisplayPraiseCount string     `json:"display_praise_count"`
	DisplayViewerCount string     `json:"display_viewer_count"`
	once               sync.Once  `json:"-"`
	msgCache           *mmsg.MSG  `json:"-"`
	liveStatusChanged  bool
	liveTitleChanged   bool
}

func (l *LiveInfo) Site() string {
	return Site
}

func (l *LiveInfo) Type() concern_type.Type {
	return LiveType
}

func (l *LiveInfo) Living() bool {
	if l == nil {
		return false
	}
	return l.Status == LiveStatus_Living
}

func (l *LiveInfo) TitleChanged() bool {
	return l.liveTitleChanged
}

func (l *LiveInfo) CloneWithStatusChanged() *LiveInfo {
	if l == nil {
		return nil
	}
	return &LiveInfo{
		UserInfo:            l.UserInfo,
		Status:              l.Status,
		LiveTitle:           l.LiveTitle,
		Url:                 l.Url,
		Cover:               l.Cover,
		DisplayMemberCount:  l.DisplayMemberCount,
		DisplayPraiseCount:  l.DisplayPraiseCount,
		DisplayViewerCount:  l.DisplayViewerCount,
		once:                sync.Once{},
		msgCache:            nil,
		liveStatusChanged:   true,
		liveTitleChanged:    l.liveTitleChanged,
	}
}

func (l *LiveInfo) LiveStatusChanged() bool {
	return l.liveStatusChanged
}

func (l *LiveInfo) GetUrl() string {
	return l.Url
}

func (l *LiveInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":   Site,
		"Uid":    l.Uid,
		"Name":   l.Name,
		"Title":  l.LiveTitle,
		"Status": l.Status.String(),
		"Type":   l.Type().String(),
	})
}

func (l *LiveInfo) IsLive() bool {
	return true
}

func (l *LiveInfo) SupportExtendNotify() bool {
	return true
}

func (l *LiveInfo) GetMSG() *mmsg.MSG {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		data := map[string]interface{}{
			"live_info":            l,
			"uid":                  l.Uid,
			"name":                 l.Name,
			"title":                l.LiveTitle,
			"cover":                l.Cover,
			"url":                  l.Url,
			"living":               l.Living(),
			"display_member_count": l.DisplayMemberCount,
			"display_praise_count": l.DisplayPraiseCount,
			"display_viewer_count": l.DisplayViewerCount,
		}
		var err error
		l.msgCache, err = template.LoadAndExec("notify.group.xhs.live.tmpl", data)
		if err != nil {
			logger.Errorf("xhs: LiveInfo LoadAndExec error %v", err)
		}
	})
	return l.msgCache
}

type ConcernLiveNotify struct {
	GroupCode    int64 `json:"group_code"`
	ExtendNotify bool  `json:"extend_notify"`
	*LiveInfo
}

func NewConcernLiveNotify(groupCode int64, liveInfo *LiveInfo) *ConcernLiveNotify {
	return &ConcernLiveNotify{
		GroupCode: groupCode,
		LiveInfo:  liveInfo,
	}
}

func (n *ConcernLiveNotify) GetGroupCode() int64 {
	return n.GroupCode
}

func (n *ConcernLiveNotify) ToMessage() *mmsg.MSG {
	return n.LiveInfo.GetMSG()
}

func (n *ConcernLiveNotify) Logger() *logrus.Entry {
	if n == nil {
		return logger
	}
	return n.LiveInfo.Logger().WithField("GroupCode", n.GroupCode)
}

type NoteInfo struct {
	UserInfo
	NoteID      string    `json:"note_id"`
	XsecToken   string    `json:"xsec_token"`
	NoteType    string    `json:"note_type"`
	Title       string    `json:"title"`
	Desc        string    `json:"desc"`
	Cover       string    `json:"cover"`
	Pictures    []string  `json:"pictures"`
	PublishedAt int64     `json:"published_at"`
	Url         string    `json:"url"`
	once        sync.Once `json:"-"`
	msgCache    *mmsg.MSG `json:"-"`
}

func (n *NoteInfo) Site() string {
	return Site
}

func (n *NoteInfo) Type() concern_type.Type {
	return NewsType
}

func (n *NoteInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":   Site,
		"Uid":    n.Uid,
		"Name":   n.Name,
		"NoteID": n.NoteID,
		"Type":   n.NoteType,
	})
}

func (n *NoteInfo) IsVideo() bool {
	return n != nil && strings.EqualFold(strings.TrimSpace(n.NoteType), NoteTypeVideo)
}

func (n *NoteInfo) GetMSG() *mmsg.MSG {
	if n == nil {
		return nil
	}
	n.once.Do(func() {
		data := map[string]interface{}{
			"note":     n,
			"uid":      n.Uid,
			"name":     n.Name,
			"title":    n.Title,
			"desc":     n.Desc,
			"cover":    n.Cover,
			"pictures": n.Pictures,
			"url":      n.Url,
			"type":     n.NoteType,
			"is_video": n.IsVideo(),
		}
		var err error
		n.msgCache, err = template.LoadAndExec("notify.group.xhs.news.tmpl", data)
		if err != nil {
			logger.Errorf("xhs: NoteInfo LoadAndExec error %v", err)
		}
	})
	return n.msgCache
}

type NewsInfo struct {
	UserInfo
	LatestNoteID      string      `json:"latest_note_id"`
	RecentNoteIDs     []string    `json:"recent_note_ids"`
	LatestPublishedAt int64       `json:"latest_published_at"`
	Notes             []*NoteInfo `json:"-"`
}

func (n *NewsInfo) Site() string {
	return Site
}

func (n *NewsInfo) Type() concern_type.Type {
	return NewsType
}

func (n *NewsInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":     Site,
		"Uid":      n.Uid,
		"Name":     n.Name,
		"NoteSize": len(n.Notes),
		"Type":     n.Type().String(),
	})
}

type ConcernNewsNotify struct {
	GroupCode int64 `json:"group_code"`
	*NoteInfo
}

func NewConcernNewsNotify(groupCode int64, newsInfo *NewsInfo) []*ConcernNewsNotify {
	if newsInfo == nil {
		return nil
	}
	result := make([]*ConcernNewsNotify, 0, len(newsInfo.Notes))
	for _, note := range newsInfo.Notes {
		result = append(result, &ConcernNewsNotify{
			GroupCode: groupCode,
			NoteInfo:  note,
		})
	}
	return result
}

func (n *ConcernNewsNotify) GetGroupCode() int64 {
	return n.GroupCode
}

func (n *ConcernNewsNotify) ToMessage() *mmsg.MSG {
	return n.NoteInfo.GetMSG()
}

func (n *ConcernNewsNotify) Logger() *logrus.Entry {
	if n == nil {
		return logger
	}
	return n.NoteInfo.Logger().WithField("GroupCode", n.GroupCode)
}
