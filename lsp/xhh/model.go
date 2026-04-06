package xhh

import (
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

const Site = "xhh"

var logger = utils.GetModuleLogger("xhh-concern")

const (
	News concern_type.Type = "news"
)

// UserInfo 用户信息
type UserInfo struct {
	Userid   string `json:"userid"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

func (u *UserInfo) Site() string {
	return Site
}

func (u *UserInfo) GetUid() interface{} {
	return u.Userid
}

func (u *UserInfo) GetName() string {
	return u.Username
}

func (u *UserInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site": Site,
		"Uid":  u.Userid,
		"Name": u.Username,
	})
}

// Moment 动态数据结构
type Moment struct {
	Linkid      int64    `json:"linkid"`
	Userid      int64    `json:"userid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	CreateAt    int64    `json:"create_at"`
	ModifyAt    int64    `json:"modify_at"`
	HasVideo    int      `json:"has_video"`
	Imgs        []string `json:"imgs"`
	User        *UserInfo `json:"user"`
	ShareURL    string   `json:"share_url"`
}

// XHHDynamic 用于模板渲染的动态数据结构
type XHHDynamic struct {
	Title       string
	Description string
	Date        string
	User        struct {
		Name string
	}
	Images    []string
	HasVideo  bool
	ShareURL  string
}

// NewsInfo 最新动态信息
type NewsInfo struct {
	*UserInfo
	LatestNewsTs int64     `json:"latest_news_time"`
	Moments     []*Moment `json:"-"`
}

func (n *NewsInfo) Type() concern_type.Type {
	return News
}

func (n *NewsInfo) Logger() *logrus.Entry {
	return n.UserInfo.Logger().WithFields(logrus.Fields{
		"Type":       n.Type().String(),
		"MomentSize": len(n.Moments),
	})
}

// CacheMoment 用于缓存模板渲染结果的包装器
type CacheMoment struct {
	*Moment
	Name string
	once     sync.Once
	msgCache *mmsg.MSG
	dynamic  XHHDynamic
}

func NewCacheMoment(moment *Moment, name string) *CacheMoment {
	return &CacheMoment{Moment: moment, Name: name}
}

func (c *CacheMoment) prepare() {
	c.dynamic.User.Name = c.Name
	if c.Moment != nil {
		if c.User != nil {
			c.dynamic.User.Name = c.User.Username
		}
		c.dynamic.Title = c.Moment.Title
		c.dynamic.Description = c.Moment.Description
		c.dynamic.Images = c.Moment.Imgs
		c.dynamic.HasVideo = c.Moment.HasVideo == 1
		c.dynamic.ShareURL = c.Moment.ShareURL
		if c.Moment.CreateAt > 0 {
			c.dynamic.Date = time.Unix(c.Moment.CreateAt, 0).Format("2006-01-02 15:04:05")
		}
	}
}

func (c *CacheMoment) GetMSG() *mmsg.MSG {
	c.once.Do(func() {
		c.prepare()
		data := map[string]interface{}{
			"dynamic": c.dynamic,
		}
		var err error
		c.msgCache, err = template.LoadAndExec("notify.group.xhh.news.tmpl", data)
		if err != nil {
			logger.Errorf("xhh: NewsInfo LoadAndExec error %v", err)
		}
	})
	return c.msgCache
}

// ConcernNewsNotify 推送通知结构
type ConcernNewsNotify struct {
	GroupCode int64   `json:"group_code"`
	*UserInfo
	Moment *Moment
}

func (c *ConcernNewsNotify) Type() concern_type.Type {
	return News
}

func (c *ConcernNewsNotify) GetGroupCode() int64 {
	return c.GroupCode
}

func (c *ConcernNewsNotify) Logger() *logrus.Entry {
	return c.UserInfo.Logger().WithFields(localutils.GroupLogFields(c.GroupCode))
}

func (c *ConcernNewsNotify) ToMessage() (m *mmsg.MSG) {
	return NewCacheMoment(c.Moment, c.Username).GetMSG()
}

// NewConcernNewsNotify 创建推送通知列表
func NewConcernNewsNotify(groupCode int64, info *NewsInfo) []*ConcernNewsNotify {
	var result []*ConcernNewsNotify
	for _, moment := range info.Moments {
		result = append(result, &ConcernNewsNotify{
			GroupCode: groupCode,
			UserInfo:  info.UserInfo,
			Moment:    moment,
		})
	}
	return result
}

// EventsResponse API响应结构
type EventsResponse struct {
	Msg    string    `json:"msg"`
	Result *Result `json:"result"`
}

type Result struct {
	Moments []*Moment `json:"moments"`
}

func (r *EventsResponse) GetOk() int {
	if r.Result == nil {
		return 0
	}
	return 1
}

// ParseId 解析用户ID
func ParseId(s string) (interface{}, error) {
	return s, nil
}
