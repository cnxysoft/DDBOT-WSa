package weibo

import (
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

const (
	News concern_type.Type = "news"
)

type UserInfo struct {
	Uid             int64  `json:"uid"`
	Name            string `json:"name"`
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

type CacheCard struct {
	*Card
	Name string

	once     sync.Once
	msgCache *mmsg.MSG
}

func NewCacheCard(card *Card, name string) *CacheCard {
	return &CacheCard{Card: card, Name: name}
}

func (c *CacheCard) prepare() {
	m := mmsg.NewMSG()
	var createdTime string
	newsTime, err := time.Parse(time.RubyDate, c.Card.GetMblog().GetCreatedAt())
	if err == nil {
		createdTime = newsTime.Format("2006-01-02 15:04:05")
	} else {
		createdTime = c.Card.GetMblog().GetCreatedAt()
	}
	if c.Card.GetMblog().GetRetweetedStatus() != nil {
		m.Textf("weibo-%v转发了%v的微博：\n%v",
			c.Name,
			c.Card.GetMblog().GetRetweetedStatus().GetUser().GetScreenName(),
			createdTime,
		)
	} else {
		m.Textf("weibo-%v发布了新微博：\n%v",
			c.Name,
			createdTime,
		)
	}
	switch c.Card.GetCardType() {
	case CardType_Normal:
		var firstVideoPic bool
		if len(c.Card.GetMblog().GetRawText()) > 0 {
			rawText := parseHTML(c.Card.GetMblog().GetRawText())
			m.Textf("\n%v", localutils.RemoveHtmlTag(rawText))
		} else {
			Text := parseHTML(c.Card.GetMblog().GetText())
			m.Textf("\n%v", localutils.RemoveHtmlTag(Text))
		}
		for _, pic := range c.Card.GetMblog().GetPics() {
			if pic.GetType() == "video" && !firstVideoPic {
				firstVideoPic = true
				continue
			}
			m.ImageByUrl(pic.GetLarge().GetUrl(), "")
		}
		if c.Card.GetMblog().GetPageInfo() != nil {
			m.ImageByUrl(c.Card.GetMblog().GetPageInfo().GetPagePic().GetUrl(), "")
			switch c.Card.GetMblog().GetPageInfo().GetType() {
			case "video":
				m.Textf("%s - %s\n", c.Card.GetMblog().GetPageInfo().GetContent1(),
					c.Card.GetMblog().GetPageInfo().GetPlayCount())
			case "article":
				m.Textf("%s\n", c.Card.GetMblog().GetPageInfo().GetContent1())
			default:
				logger.WithField("Type", c.Card.GetMblog().GetPageInfo().GetType()).
					Debugf("found new page_info_type")
			}
		}
		if c.Card.GetMblog().GetRetweetedStatus() != nil {
			if len(c.Card.GetMblog().GetRetweetedStatus().GetRawText()) > 0 {
				rawText := parseHTML(c.Card.GetMblog().GetRetweetedStatus().GetRawText())
				m.Textf("\n\n原微博：\n%v", localutils.RemoveHtmlTag(rawText))
			} else {
				Text := parseHTML(c.Card.GetMblog().GetRetweetedStatus().GetText())
				m.Textf("\n\n原微博：\n%v", localutils.RemoveHtmlTag(Text))
			}
			for _, pic := range c.Card.GetMblog().GetRetweetedStatus().GetPics() {
				if pic.GetType() == "video" && !firstVideoPic {
					firstVideoPic = true
					continue
				}
				m.ImageByUrl(pic.GetLarge().GetUrl(), "")
			}
			if c.Card.GetMblog().GetRetweetedStatus().GetPageInfo() != nil {
				m.ImageByUrl(c.Card.GetMblog().GetRetweetedStatus().GetPageInfo().GetPagePic().GetUrl(), "")
				switch c.Card.GetMblog().GetRetweetedStatus().GetPageInfo().GetType() {
				case "video":
					m.Textf("%s - %s\n", c.Card.GetMblog().GetRetweetedStatus().GetPageInfo().GetContent1(),
						c.Card.GetMblog().GetRetweetedStatus().GetPageInfo().GetPlayCount())
				case "article":
					m.Textf("%s\n", c.Card.GetMblog().GetRetweetedStatus().GetPageInfo().GetContent1())
				default:
					logger.WithField("Type", c.Card.GetMblog().GetRetweetedStatus().GetPageInfo().GetType()).
						Debugf("found new page_info_type")
				}
			}
		}
	default:
		logger.WithField("Type", c.CardType.String()).Debug("found new card_types")
	}
	m.Textf("\n%s", createWeiboUrl(c.Card.GetMblog().GetUser().GetId(), c.Card.GetMblog().GetBid()))
	c.msgCache = m
}

func (c *CacheCard) GetMSG() *mmsg.MSG {
	c.once.Do(c.prepare)
	return c.msgCache
}

func parseHTML(text string) string {
	text = strings.ReplaceAll(text, "<br />", "\n")
	text = html.UnescapeString(text)
	return text
}

func createWeiboUrl(uid int64, bid string) string {
	Uid := strconv.FormatInt(uid, 10)
	return "https://weibo.com/" + Uid + "/" + bid
}
