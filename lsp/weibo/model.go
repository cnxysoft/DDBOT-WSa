package weibo

import (
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

const (
	News concern_type.Type = "news"
)

type UserInfo struct {
	Uid             int64  `json:"id"`
	Name            string `json:"screen_name"`
	ProfileImageUrl string `json:"profile_image_url"`
	ProfileUrl      string `json:"profile_url"`
}

func (u *UserInfo) Site() string {
	return Site
}

func (u *UserInfo) GetUid() interface{} {
	return u.Uid
}

func (u *UserInfo) GetName() string {
	return u.Name
}

func (u *UserInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site": Site,
		"Uid":  u.Uid,
		"Name": u.Name,
	})
}

type NewsInfo struct {
	*UserInfo
	LatestNewsTs int64   `json:"latest_news_time"`
	Cards        []*Card `json:"-"`
}

func (n *NewsInfo) Type() concern_type.Type {
	return News
}

func (n *NewsInfo) Logger() *logrus.Entry {
	return n.UserInfo.Logger().WithFields(logrus.Fields{
		"Type":     n.Type().String(),
		"CardSize": len(n.Cards),
	})
}

type ConcernNewsNotify struct {
	GroupCode int64 `json:"group_code"`
	*UserInfo
	Card *CacheCard
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
	return c.Card.GetMSG()
}

func NewConcernNewsNotify(groupCode int64, info *NewsInfo) []*ConcernNewsNotify {
	var result []*ConcernNewsNotify
	for _, card := range info.Cards {
		result = append(result, &ConcernNewsNotify{
			GroupCode: groupCode,
			UserInfo:  info.UserInfo,
			Card:      NewCacheCard(card, info.GetName()),
		})
	}
	return result
}

// WeiboDynamic 微博动态数据结构，用于模板渲染
type WeiboDynamic struct {
	User        WeiboUser    `json:"user"`
	Type        CardType     `json:"type"`
	Content     string       `json:"content"`
	Date        string       `json:"date"`
	Url         string       `json:"url"`
	Images      []string     `json:"images"`
	Video       WeiboVideo   `json:"video"`
	WithRetweet bool         `json:"with_retweet"`
	Retweet     WeiboRetweet `json:"retweet"`
	Page        WeiboPage    `json:"page"`
}

// WeiboUser 用户信息
type WeiboUser struct {
	Name string `json:"name"`
	Id   int64  `json:"id"`
}

// WeiboVideo 视频信息
type WeiboVideo struct {
	Title       string `json:"title"`
	CoverUrl    string `json:"cover_url"`
	PublishTime string `json:"publish_time"`
	OnlineUsers string `json:"online_users"`
}

// WeiboRetweet 转发信息
type WeiboRetweet struct {
	User    WeiboUser `json:"user"`
	Content string    `json:"content"`
	Images  []string  `json:"images"`
}

// WeiboPage 页面信息（视频、文章等）
type WeiboPage struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	CoverUrl string `json:"cover_url"`
	Content1 string `json:"content1"`
}

type CacheCard struct {
	*Card
	Name string

	once     sync.Once
	msgCache *mmsg.MSG
	dynamic  WeiboDynamic
}

func NewCacheCard(card *Card, name string) *CacheCard {
	return &CacheCard{Card: card, Name: name}
}

func (c *CacheCard) prepare() {
	// 初始化动态数据结构
	dynamic := WeiboDynamic{}

	// 填充用户信息
	dynamic.User = WeiboUser{
		Name: c.Name,
		Id:   c.Card.GetUser().GetId(),
	}

	// 填充微博基本信息
	dynamic.Type = c.Card.GetMblogtype()
	dynamic.Date = getTimeString(c.Card.GetCreatedAt())
	dynamic.Url = getWeiboUrl(c.Card.GetUser().GetId(), c.Card.Mblogid)

	// 填充微博内容
	if len(c.Card.GetRawText()) > 0 {
		rawText := parseHTML(c.Card.GetRawText())
		dynamic.Content = localutils.RemoveHtmlTag(rawText)
	} else {
		text := parseHTML(c.Card.GetText())
		dynamic.Content = localutils.RemoveHtmlTag(text)
	}

	// 收集微博图片
	dynamic.Images = findPicUrlsForCard(c.Card.GetPicInfos())

	// 处理转发微博
	if c.Card.GetRetweetedStatus() != nil {
		dynamic.WithRetweet = true
		retweetStatus := c.Card.GetRetweetedStatus()

		// 填充转发用户信息
		dynamic.Retweet.User = WeiboUser{
			Name: retweetStatus.GetUser().GetScreenName(),
			Id:   retweetStatus.GetUser().GetId(),
		}

		// 填充转发内容
		if len(retweetStatus.GetRawText()) > 0 {
			rawText := parseHTML(retweetStatus.GetRawText())
			dynamic.Retweet.Content = localutils.RemoveHtmlTag(rawText)
		} else {
			text := parseHTML(retweetStatus.GetText())
			dynamic.Retweet.Content = localutils.RemoveHtmlTag(text)
		}

		// 收集转发微博图片
		dynamic.Retweet.Images = findPicUrlsForCard(retweetStatus.GetPicInfos())
		if retweetStatus.GetMixMediaInfo() != nil {
			dynamic.Retweet.Images = append(dynamic.Retweet.Images, findPicUrlsForMix(retweetStatus.GetMixMediaInfo().GetItems())...)
		}
	}

	// 处理页面信息（视频、文章等）
	if c.GetPageInfo() != nil {
		dynamic.Page.Type = c.GetPageInfo().GetObjectType()
		dynamic.Page.CoverUrl = c.GetPageInfo().GetPagePic()
		dynamic.Page.Content1 = c.GetPageInfo().GetContent1()

		// 处理视频页面
		if c.GetPageInfo().GetObjectType() == "video" {
			mediaInfo := c.GetPageInfo().GetMediaInfo()
			dynamic.Video.Title = mediaInfo.GetName()
			dynamic.Video.CoverUrl = c.GetPageInfo().GetPagePic()
			dynamic.Video.PublishTime = time.Unix(mediaInfo.GetVideoPublishTime(), 0).Format(time.DateTime)
			dynamic.Video.OnlineUsers = mediaInfo.GetOnlineUsers()
		}
	} else if c.Card.GetMixMediaInfo() != nil {
		// 处理混合媒体信息
		dynamic.Images = append(dynamic.Images, findPicUrlsForMix(c.Card.GetMixMediaInfo().GetItems())...)
	}

	// 保存动态数据
	c.dynamic = dynamic
}

func (c *CacheCard) GetMSG() *mmsg.MSG {
	c.once.Do(func() {
		c.prepare()
		var data = map[string]interface{}{
			"dynamic": c.dynamic,
		}
		var err error
		c.msgCache, err = template.LoadAndExec("notify.group.weibo.news.tmpl", data)
		if err != nil {
			logger.Errorf("weibo: NewsInfo LoadAndExec error %v", err)
			// 如果模板加载失败，回退到默认消息
			c.fallbackMSG()
		}
		return
	})
	return c.msgCache
}

// fallbackMSG 模板加载失败时的回退消息生成
func (c *CacheCard) fallbackMSG() {
	m := mmsg.NewMSG()
	createdTime := getTimeString(c.Card.GetCreatedAt())
	if c.Card.GetRetweetedStatus() != nil {
		m.Textf("weibo-%v转发了%v的微博：\n%v",
			c.Name,
			c.Card.GetRetweetedStatus().GetUser().GetScreenName(),
			createdTime,
		)
	} else {
		m.Textf("weibo-%v发布了新微博：\n%v",
			c.Name,
			createdTime,
		)
	}
	switch c.Card.GetMblogtype() {
	case CardType_Normal, CardType_Text, CardType_Top:
		if len(c.Card.GetRawText()) > 0 {
			rawText := parseHTML(c.Card.GetRawText())
			m.Textf("\n%v", localutils.RemoveHtmlTag(rawText))
		} else {
			Text := parseHTML(c.Card.GetText())
			m.Textf("\n%v", localutils.RemoveHtmlTag(Text))
		}
		findPicForCard(c.Card.GetPicInfos(), m)
		if c.Card.GetRetweetedStatus() != nil {
			if len(c.Card.GetRetweetedStatus().GetRawText()) > 0 {
				rawText := parseHTML(c.Card.GetRetweetedStatus().GetRawText())
				m.Textf("\n\n原微博：\n%v", localutils.RemoveHtmlTag(rawText))
			} else {
				Text := parseHTML(c.Card.GetRetweetedStatus().GetText())
				m.Textf("\n\n原微博：\n%v", localutils.RemoveHtmlTag(Text))
			}
			if c.Card.GetRetweetedStatus().GetMixMediaInfo() != nil {
				findPicForMix(c.Card.GetRetweetedStatus().GetMixMediaInfo().GetItems(), m)
				findVideoForMix(c.Card.GetRetweetedStatus().GetMixMediaInfo().GetItems(), m)
			}
			findPicForCard(c.Card.GetRetweetedStatus().GetPicInfos(), m)
		}
		if c.GetPageInfo() != nil {
			m.ImageByUrl(c.GetPageInfo().GetPagePic(), "")
			switch c.GetPageInfo().GetObjectType() {
			case "video":
				m.Textf("%s\n%s - %s\n", c.GetPageInfo().GetMediaInfo().GetName(),
					time.Unix(c.GetPageInfo().GetMediaInfo().GetVideoPublishTime(), 0).Format(time.DateTime),
					c.GetPageInfo().GetMediaInfo().GetOnlineUsers())
			case "article":
				m.Textf("%s\n", c.GetPageInfo().GetContent1())
			default:
				logger.Debugf("found page_info new type: %s", c.GetPageInfo().GetObjectType())
			}
		} else if c.Card.GetMixMediaInfo() != nil {
			findPicForMix(c.Card.GetMixMediaInfo().GetItems(), m)
			findVideoForMix(c.Card.GetMixMediaInfo().GetItems(), m)
		}
		m.Text("\n" + getWeiboUrl(c.Card.GetUser().GetId(), c.Card.Mblogid))
	default:
		logger.WithField("Type", c.Mblogtype.String()).Debug("found new card_types")
	}
	c.msgCache = m
}

func parseHTML(text string) string {
	text = strings.ReplaceAll(text, "<br />", "\n")
	text = html.UnescapeString(text)
	return text
}

func getWeiboUrl(uid int64, mblogId string) string {
	return "https://weibo.com/" + strconv.FormatInt(uid, 10) + "/" + mblogId
}

func getTimeString(t string) string {
	var ti string
	newsTime, err := time.Parse(time.RubyDate, t)
	if err == nil {
		ti = newsTime.Format("2006-01-02 15:04:05")
	} else {
		ti = t
	}
	return ti
}

// findPicUrlsForCard 收集卡片中的图片URL
func findPicUrlsForCard(picInfos map[string]*Card_PicInfo) []string {
	var urls []string
	for _, pic := range picInfos {
		switch pic.Type {
		case "pic":
			urls = append(urls, pic.GetLarge().GetUrl())
		case "gif":
			urls = append(urls, pic.GetOriginal().GetUrl())
		}
	}
	return urls
}

// findPicUrlsForMix 收集混合媒体中的图片URL
func findPicUrlsForMix(items []*Card_MixMediaInfo_Items) []string {
	var urls []string
	for _, item := range items {
		raw := item.Data.AsMap()
		switch item.Type {
		case "pic", "gif":
			var pic Card_PicInfo
			b, _ := json.Marshal(raw)
			err := json.Unmarshal(b, &pic)
			if err != nil {
				logger.Errorf("found pic failed. %v,", err)
				continue
			}
			if item.Type == "gif" {
				urls = append(urls, pic.GetOriginal().GetUrl())
			} else {
				urls = append(urls, pic.GetLarge().GetUrl())
			}
		}
	}
	return urls
}

// 保留原函数用于向后兼容
func findPicForCard(picInfos map[string]*Card_PicInfo, m *mmsg.MSG) {
	for _, url := range findPicUrlsForCard(picInfos) {
		m.ImageByUrl(url, "")
	}
}

// 保留原函数用于向后兼容
func findPicForMix(Item []*Card_MixMediaInfo_Items, m *mmsg.MSG) {
	for _, url := range findPicUrlsForMix(Item) {
		m.ImageByUrl(url, "")
	}
}

func findVideoForMix(Item []*Card_MixMediaInfo_Items, m *mmsg.MSG) {
	for _, item := range Item {
		raw := item.Data.AsMap()
		switch item.Type {
		case "video":
			var video Card_PageInfo
			b, _ := json.Marshal(raw)
			err := json.Unmarshal(b, &video)
			if err != nil {
				logger.Errorf("found video failed. %v,", err)
			}
			m.ImageByUrl(video.GetPagePic(), "")
			m.Textf("%s\n%s - %s\n", video.GetMediaInfo().GetName(),
				time.Unix(video.GetMediaInfo().GetVideoPublishTime(), 0).Format(time.DateTime),
				video.GetMediaInfo().GetOnlineUsers())
		}
	}
}
