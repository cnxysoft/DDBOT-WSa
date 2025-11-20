package bilibili

import (
	"bytes"
	"strings"
	"sync"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/cnxysoft/DDBOT-WSa/utils/blockCache"
	"github.com/sirupsen/logrus"
)

const PathWebDynamicDetail = "/x/polymer/web-dynamic/v1/detail"

type NewsInfo struct {
	UserInfo
	LastDynamicId int64   `json:"last_dynamic_id"`
	Timestamp     int64   `json:"timestamp"`
	Cards         []*Card `json:"-"`
}

func (n *NewsInfo) Site() string {
	return Site
}

func (n *NewsInfo) Type() concern_type.Type {
	return News
}

func (n *NewsInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":     Site,
		"Mid":      n.Mid,
		"Name":     n.Name,
		"CardSize": len(n.Cards),
		"Type":     n.Type().String(),
	})
}

type ConcernNewsNotify struct {
	GroupCode int64 `json:"group_code"`
	*UserInfo
	Card *CacheCard

	// 用于联合投稿和转发的时候防止多人同时推送
	shouldCompact bool
	compactKey    string
	concern       *Concern
}

func (notify *ConcernNewsNotify) IsLive() bool {
	return false
}

func (notify *ConcernNewsNotify) Living() bool {
	return false
}

type ConcernLiveNotify struct {
	GroupCode int64 `json:"group_code"`
	*LiveInfo
}

type UserStat struct {
	Mid int64 `json:"mid"`
	// 关注数
	Following int64 `json:"following"`
	// 粉丝数
	Follower int64 `json:"follower"`
}

type UserInfo struct {
	Mid     int64  `json:"mid"`
	Name    string `json:"name"`
	Face    string `json:"face"`
	RoomId  int64  `json:"room_id"`
	RoomUrl string `json:"room_url"`

	UserStat *UserStat `json:"-"`
}

func (ui *UserInfo) GetUid() interface{} {
	return ui.Mid
}

func (ui *UserInfo) GetName() string {
	if ui == nil {
		return ""
	}
	return ui.Name
}

type LiveInfo struct {
	UserInfo
	Status         LiveStatus `json:"status"`
	LiveTitle      string     `json:"live_title"`
	Cover          string     `json:"cover"`
	AreaId         int32      `json:"area_id"`
	AreaName       string     `json:"area_name"`
	ParentAreaId   int32      `json:"parent_area_id"`
	ParentAreaName string     `json:"parent_area_name"`
	LiveTime       int64      `json:"live_time"`
	ExtendNotify   bool       `json:"extend_notify"`
	GroupCode      int64      `json:"group_code"`

	once              sync.Once
	msgCache          *mmsg.MSG
	liveStatusChanged bool
	liveTitleChanged  bool
}

func (l *LiveInfo) GetMSG() *mmsg.MSG {
	if l == nil {
		return nil
	}
	// 现在直播url会带一个`?broadcast_type=0`，好像删掉也行
	cleanRoomUrl := func(url string) string {
		if pos := strings.Index(url, "?"); pos > 0 {
			return url[:pos]
		}
		return url
	}
	l.once.Do(func() {
		var data = map[string]interface{}{
			"live_info":        l,
			"uid":              l.Mid,
			"title":            l.LiveTitle,
			"name":             l.Name,
			"url":              cleanRoomUrl(l.RoomUrl),
			"cover":            l.Cover,
			"living":           l.Living(),
			"parent_area_name": l.ParentAreaName,
			"area_name":        l.AreaName,
			"live_time":        l.LiveTime,
			"extend_notify":    l.ExtendNotify,
			"group_code":       l.GroupCode,
			"title_changed":    l.liveTitleChanged,
		}
		var err error
		l.msgCache, err = template.LoadAndExec("notify.group.bilibili.live.tmpl", data)
		if err != nil {
			logger.Errorf("bilibili: LiveInfo LoadAndExec error %v", err)
		}
		return
	})
	return l.msgCache
}

func (l *LiveInfo) TitleChanged() bool {
	return l.liveTitleChanged
}

func (l *LiveInfo) LiveStatusChanged() bool {
	return l.liveStatusChanged
}

func (l *LiveInfo) IsLive() bool {
	return true
}

func (l *LiveInfo) SupportExtendNotify() bool {
	return true
}

func (l *LiveInfo) Site() string {
	return Site
}

func (l *LiveInfo) Living() bool {
	if l == nil {
		return false
	}
	return l.Status == LiveStatus_Living
}

func (l *LiveInfo) Type() concern_type.Type {
	return Live
}

func (l *LiveInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":   Site,
		"Mid":    l.Mid,
		"Name":   l.Name,
		"RoomId": l.RoomId,
		"Title":  l.LiveTitle,
		"Status": l.Status.String(),
		"Type":   l.Type().String(),
	})
}

func (l *LiveInfo) SetAreaData(AreaId int32, AreaName string, ParentAreaId int32, ParentAreaName string) {
	l.AreaId = AreaId
	l.AreaName = AreaName
	l.ParentAreaId = ParentAreaId
	l.ParentAreaName = ParentAreaName
}

func NewUserStat(mid, following, follower int64) *UserStat {
	return &UserStat{
		Mid:       mid,
		Following: following,
		Follower:  follower,
	}
}

func NewUserInfo(mid, roomId int64, name, url, face string) *UserInfo {
	return &UserInfo{
		Mid:     mid,
		RoomId:  roomId,
		Name:    name,
		Face:    face,
		RoomUrl: url,
	}
}

func NewLiveInfo(userInfo *UserInfo, liveTitle string, cover string, status LiveStatus, liveTime int64) *LiveInfo {
	if userInfo == nil {
		return nil
	}
	return &LiveInfo{
		UserInfo:  *userInfo,
		Status:    status,
		LiveTitle: liveTitle,
		Cover:     cover,
		LiveTime:  liveTime,
	}
}

func NewNewsInfo(userInfo *UserInfo, dynamicId int64, timestamp int64) *NewsInfo {
	if userInfo == nil {
		return nil
	}
	return &NewsInfo{
		UserInfo:      *userInfo,
		LastDynamicId: dynamicId,
		Timestamp:     timestamp,
	}
}

func NewNewsInfoWithDetail(userInfo *UserInfo, cards []*Card) *NewsInfo {
	var dynamicId int64
	var timestamp int64
	if len(cards) > 0 {
		dynamicId = cards[0].GetDesc().GetDynamicId()
		timestamp = cards[0].GetDesc().GetTimestamp()
	}
	return &NewsInfo{
		UserInfo:      *userInfo,
		LastDynamicId: dynamicId,
		Timestamp:     timestamp,
		Cards:         cards,
	}
}

func NewConcernNewsNotify(groupCode int64, newsInfo *NewsInfo, c *Concern) []*ConcernNewsNotify {
	if newsInfo == nil {
		return nil
	}
	var result []*ConcernNewsNotify
	for _, card := range newsInfo.Cards {
		result = append(result, &ConcernNewsNotify{
			GroupCode: groupCode,
			UserInfo:  &newsInfo.UserInfo,
			Card:      NewCacheCard(card),
			concern:   c,
		})
	}
	return result
}

func NewConcernLiveNotify(groupCode int64, liveInfo *LiveInfo) *ConcernLiveNotify {
	if liveInfo == nil {
		return nil
	}
	return &ConcernLiveNotify{
		GroupCode: groupCode,
		LiveInfo:  liveInfo,
	}
}

func (notify *ConcernNewsNotify) ToMessage() (m *mmsg.MSG) {
	var (
		card = notify.Card
		log  = notify.Logger()
		//dynamicUrl = DynamicUrl(card.GetDesc().GetDynamicIdStr())
		//date       = localutils.TimestampFormat(card.GetDesc().GetTimestamp())
	)
	// 推送一条简化动态防止刷屏，主要是联合投稿和转发的时候
	if notify.shouldCompact {
		// 通过回复之前消息的方式简化推送
		m = mmsg.NewMSG()
		msg, _ := notify.concern.GetNotifyMsg(notify.GroupCode, notify.compactKey)
		if msg != nil {
			card.orgMsg = msg
		}
		log.WithField("compact_key", notify.compactKey).Debug("compact notify")
	}
	notify.Card.GroupCode = notify.GroupCode
	m = notify.Card.GetMSG()
	return
}

func (notify *ConcernNewsNotify) Type() concern_type.Type {
	return News
}

func (notify *ConcernNewsNotify) Site() string {
	return Site
}

func (notify *ConcernNewsNotify) GetGroupCode() int64 {
	return notify.GroupCode
}
func (notify *ConcernNewsNotify) GetUid() interface{} {
	return notify.Mid
}

func (notify *ConcernNewsNotify) Logger() *logrus.Entry {
	if notify == nil {
		return logger
	}
	return logger.WithFields(localutils.GroupLogFields(notify.GroupCode)).
		WithFields(logrus.Fields{
			"Site":      Site,
			"Mid":       notify.Mid,
			"Name":      notify.Name,
			"DynamicId": notify.Card.GetDesc().GetDynamicIdStr(),
			"DescType":  notify.Card.GetDesc().GetType().String(),
			"Type":      notify.Type().String(),
		})
}

func (notify *ConcernLiveNotify) ToMessage() (m *mmsg.MSG) {
	notify.LiveInfo.GroupCode = notify.GroupCode
	return notify.LiveInfo.GetMSG()
}

func (notify *ConcernLiveNotify) Logger() *logrus.Entry {
	if notify == nil {
		return logger
	}
	return notify.LiveInfo.Logger().
		WithFields(localutils.GroupLogFields(notify.GroupCode))
}

func (notify *ConcernLiveNotify) GetGroupCode() int64 {
	return notify.GroupCode
}

// combineImageCache 是给combineImage用的cache，其他地方禁止使用
var combineImageCache = blockCache.NewBlockCache(5, 3)

var mode = "auto"
var modeSync sync.Once

func shouldCombineImage(pic []*CardWithImage_Item_Picture) bool {
	modeSync.Do(func() {
		if config.GlobalConfig == nil {
			return
		}
		switch config.GlobalConfig.GetString("bilibili.imageMergeMode") {
		case "auto":
			mode = "auto"
		case "off", "false":
			mode = "off"
		case "only9":
			mode = "only9"
		default:
			mode = "auto"
		}
	})
	if mode == "off" {
		return false
	} else if mode == "only9" {
		return len(pic) == 9
	}
	if len(pic) <= 3 {
		return false
	}
	if len(pic) == 9 {
		return true
	}
	// 有竖条形状的图
	for _, i := range pic {
		if i.ImgWidth > 250 && float64(i.ImgHeight) > 3*float64(i.ImgWidth) {
			return true
		}
	}
	// 有超过一半的近似矩形图片尺寸一样
	var size = make(map[int64]int)
	for _, i := range pic {
		var gap float64
		if i.ImgHeight < i.ImgWidth {
			gap = float64(i.ImgHeight) / float64(i.ImgWidth)
		} else {
			gap = float64(i.ImgWidth) / float64(i.ImgHeight)
		}
		if gap >= 0.95 {
			size[int64(i.ImgWidth)*int64(i.ImgHeight)] += 1
		}
	}
	var sizeMerge bool
	for _, count := range size {
		if 2*count > len(pic) {
			sizeMerge = true
		}
	}
	if sizeMerge && (len(pic) == 4 || len(pic) == 6 || len(pic) == 9) {
		return true
	}
	return false
}

func urlsMergeImage(urls []string) (result []byte, err error) {
	cacheR := combineImageCache.WithCacheDo(strings.Join(urls, "+"), func() blockCache.ActionResult {
		var imgBytes = make([][]byte, len(urls))
		for index, url := range urls {
			imgBytes[index], err = localutils.ImageGet(url)
			if err != nil {
				return blockCache.NewResultWrapper(nil, err)
			}
		}
		return blockCache.NewResultWrapper(localutils.MergeImages(imgBytes))
	})
	if cacheR.Err() != nil {
		return nil, cacheR.Err()
	}
	return cacheR.Result().([]byte), nil
}

type CacheCard struct {
	*Card
	GroupCode  int64
	once       sync.Once
	msgCache   *mmsg.MSG
	dynamic    DynamicInfo
	dynamicRaw map[string]interface{}
	orgMsg     *message.GroupMessage
}

func NewCacheCard(card *Card) *CacheCard {
	cacheCard := new(CacheCard)
	cacheCard.Card = card
	return cacheCard
}

type DynamicInfo struct {
	Type            DynamicDescType `json:"type"`
	Id              string          `json:"id"`
	WithOrigin      bool            `json:"with_origin"`
	OriginDyId      string          `json:"origin_dy_id"`
	OriginDyUrl     string          `json:"origin_dy_url"`
	Date            string          `json:"date"`
	Content         string          `json:"content"`
	Title           string          `json:"title"`
	OriginTitle     string          `json:"origin_title"`
	TopicName       string          `json:"topic_name"`
	OriginTopicName string          `json:"origin_topic_name"`
	DynamicUrl      string          `json:"dynamic_url"`
	Detail          DynamicDetail   `json:"detail"`
	OriginDetail    DynamicDetail   `json:"origin_detail"`

	User struct {
		Uid  int64  `json:"uid"`
		Name string `json:"name"`
		Face string `json:"face"`
	} `json:"user"`

	OriginUser struct {
		Uid  int64  `json:"uid,omitempty"`
		Name string `json:"name,omitempty"`
		Face string `json:"face,omitempty"`
	} `json:"origin_user,omitempty"`

	Image struct {
		ImageUrls   []string `json:"image_urls,omitempty"`
		Bytes       []byte   `json:"-"`
		Description string   `json:"description,omitempty"`
	} `json:"image,omitempty"`

	Text struct {
		Content string `json:"content,omitempty"`
	} `json:"text,omitempty"`

	Video struct {
		Title    string `json:"title,omitempty"`
		Desc     string `json:"desc,omitempty"`
		Dynamic  string `json:"dynamic,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
		Action   string `json:"action,omitempty"`
	} `json:"video,omitempty"`

	Post struct {
		Title     string   `json:"title,omitempty"`
		Summary   string   `json:"summary,omitempty"`
		ImageUrls []string `json:"image_urls,omitempty"`
	} `json:"post,omitempty"`

	Music struct {
		Title    string `json:"title,omitempty"`
		Intro    string `json:"intro,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
		Author   string `json:"author,omitempty"`
	} `json:"music,omitempty"`

	Sketch struct {
		Content  string `json:"content,omitempty"`
		Title    string `json:"title,omitempty"`
		DescText string `json:"desc_text,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
	} `json:"sketch,omitempty"`

	Live struct {
		Title    string `json:"title,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
	} `json:"live,omitempty"`

	MyList struct {
		Title    string `json:"title,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
	} `json:"my_list,omitempty"`

	Miss struct {
		Tips string `json:"tips,omitempty"`
	} `json:"miss,omitempty"`

	Course struct {
		Name     string `json:"name,omitempty"`
		Badge    string `json:"badge,omitempty"`
		Title    string `json:"title,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
	} `json:"course,omitempty"`

	Default struct {
		TypeName string `json:"type_name,omitempty"`
		Title    string `json:"title,omitempty"`
		Desc     string `json:"desc,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
	} `json:"default,omitempty"`

	Addons []Addon `json:"addons,omitempty"`
}

type Addon struct {
	Type AddOnCardShowType `json:"type"`

	Goods struct {
		AdMark   string `json:"ad_mark,omitempty"`
		Name     string `json:"name,omitempty"`
		ImageUrl string `json:"image_url,omitempty"`
	} `json:"goods,omitempty"`

	Reserve struct {
		Title   string `json:"title,omitempty"`
		Desc    string `json:"desc,omitempty"`
		Lottery string `json:"lottery,omitempty"`
	} `json:"reserve,omitempty"`

	Related struct {
		Type     string `json:"type,omitempty"`
		HeadText string `json:"head_text,omitempty"`
		Title    string `json:"title,omitempty"`
		Desc     string `json:"desc,omitempty"`
	} `json:"related,omitempty"`

	Vote struct {
		Index []int32  `json:"index,omitempty"`
		Desc  []string `json:"desc,omitempty"`
	} `json:"vote,omitempty"`

	Video struct {
		Title    string `json:"title,omitempty"`
		CoverUrl string `json:"cover_url,omitempty"`
		Desc     string `json:"desc,omitempty"`
		PlayUrl  string `json:"play_url,omitempty"`
	} `json:"video,omitempty"`
}

type DynamicDetail struct {
	Reserve struct {
		Title string `json:"title"`
		Desc1 string `json:"desc1"`
		Desc2 string `json:"desc2"`
		Desc3 string `json:"desc3"`
	} `json:"reserve,omitempty"`
	PGC struct {
		Type     string `json:"type"`
		Title    string `json:"title"`
		CoverUrl string `json:"cover_url"`
	} `json:"pgc,omitempty"`
	Title     string `json:"title"`
	TopicName string `json:"topic_name"`
	Content   string `json:"content"`
}

func (c *CacheCard) prepare() {
	var (
		card         = c.Card
		log          = logger
		Id           = card.GetDesc().GetDynamicIdStr()
		dynamicUrl   = DynamicUrl(Id)
		date         = localutils.TimestampFormat(card.GetDesc().GetTimestamp())
		name         = card.GetDesc().GetUserProfile().GetInfo().GetUname()
		detailResp   = SecAnalysis(Id)
		detail       = getDescContent(detailResp, false)
		originDetail = getDescContent(detailResp, true)
	)
	c.dynamic.Id = Id
	c.dynamic.Title = detail.Title
	c.dynamic.TopicName = detail.TopicName
	c.dynamic.User.Name = name
	c.dynamic.User.Uid = card.GetDesc().GetUserProfile().GetInfo().GetUid()
	c.dynamic.User.Face = card.GetDesc().GetUserProfile().GetInfo().GetFace()
	c.dynamic.Date = date
	c.dynamic.Detail = detail
	c.dynamicRaw = detailResp
	switch card.GetDesc().GetType() {
	case DynamicDescType_WithOrigin:
		c.dynamic.WithOrigin = true
		c.dynamic.OriginDyId = card.GetDesc().GetOrigDyIdStr()
		c.dynamic.OriginDyUrl = DynamicUrl(c.dynamic.OriginDyId)
		cardOrigin, err := card.GetCardWithOrig()
		if err != nil {
			log.WithField("card", card).Errorf("GetCardWithOrig failed %v", err)
			return
		}
		originName := cardOrigin.GetOriginUser().GetInfo().GetUname()
		c.dynamic.OriginUser.Name = originName
		c.dynamic.OriginUser.Uid = cardOrigin.GetOriginUser().GetInfo().GetUid()
		c.dynamic.OriginUser.Face = cardOrigin.GetOriginUser().GetInfo().GetFace()
		// very sb
		c.dynamic.OriginTitle = originDetail.Title
		c.dynamic.OriginTopicName = originDetail.TopicName
		c.dynamic.OriginDetail = originDetail
		switch cardOrigin.GetItem().GetOrigType() {
		case DynamicDescType_WithImage:
			c.dynamic.Type = DynamicDescType_WithImage
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithImage)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).
					Errorf("Unmarshal origin cardWithImage failed %v", err)
				return
			}
			c.dynamic.Image.Description = replaseDesc(origin.GetItem().GetDescription(), originDetail.Content)
			// 输出urls
			var urls = make([]string, len(origin.GetItem().GetPictures()))
			for index, pic := range origin.GetItem().GetPictures() {
				urls[index] = pic.GetImgSrc()
			}
			c.dynamic.Image.ImageUrls = urls
			// 多图合一
			if shouldCombineImage(origin.GetItem().GetPictures()) {
				var urls = make([]string, len(origin.GetItem().GetPictures()))
				for index, pic := range origin.GetItem().GetPictures() {
					urls[index] = pic.GetImgSrc()
				}
				resultByte, err := urlsMergeImage(urls)
				if err != nil {
					log.Errorf("urlsMergeImage failed %v", err)
				} else {
					c.dynamic.Image.Bytes = resultByte
				}
			}
		case DynamicDescType_TextOnly:
			c.dynamic.Type = DynamicDescType_TextOnly
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardTextOnly)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin cardWithText failed %v", err)
				return
			}
			c.dynamic.Text.Content = replaseDesc(origin.GetItem().GetContent(), originDetail.Content)
		case DynamicDescType_WithVideo:
			c.dynamic.Type = DynamicDescType_WithVideo
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithVideo)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin cardWithVideo failed %v", err)
				return
			}
			c.dynamic.Video.Title = origin.GetTitle()
			c.dynamic.Video.Desc = origin.GetDesc()
			c.dynamic.Video.Dynamic = replaseDesc(origin.GetDynamic(), originDetail.Content)
			c.dynamic.Video.CoverUrl = origin.GetPic()
			c.dynamic.Video.Action = c.Card.GetDisplay().GetOrigin().GetUsrActionTxt()
		case DynamicDescType_WithPost:
			c.dynamic.Type = DynamicDescType_WithPost
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithPost)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin cardWithPost failed %v", err)
				return
			}
			c.dynamic.Post.Title = origin.GetTitle()
			c.dynamic.Post.Summary = origin.GetSummary()
			if len(origin.GetImageUrls()) >= 1 {
				c.dynamic.Post.ImageUrls = origin.GetImageUrls()
			} else if len(origin.GetBannerUrl()) != 0 {
				c.dynamic.Post.ImageUrls = []string{origin.GetBannerUrl()}
			}
		case DynamicDescType_WithMusic:
			c.dynamic.Type = DynamicDescType_WithMusic
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithMusic)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin CardWithMusic failed %v", err)
				return
			}
			c.dynamic.Music.Title = origin.GetTitle()
			c.dynamic.Music.Intro = origin.GetIntro()
			c.dynamic.Music.Author = origin.GetAuthor()
			c.dynamic.Music.CoverUrl = origin.GetCover()
		case DynamicDescType_WithSketch:
			c.dynamic.Type = DynamicDescType_WithSketch
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithSketch)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin CardWithSketch failed %v", err)
				return
			}
			c.dynamic.Sketch.Content = origin.GetVest().GetContent()
			c.dynamic.Sketch.Title = origin.GetSketch().GetTitle()
			c.dynamic.Sketch.DescText = origin.GetSketch().GetDescText()
			if len(origin.GetSketch().GetCoverUrl()) != 0 {
				c.dynamic.Sketch.CoverUrl = origin.GetSketch().GetCoverUrl()
			}
		case DynamicDescType_WithLive:
			c.dynamic.Type = DynamicDescType_WithLive
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithLive)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin CardWithLive failed %v", err)
				return
			}
			c.dynamic.Live.Title = origin.GetTitle()
			c.dynamic.Live.CoverUrl = origin.GetCover()
		case DynamicDescType_WithLiveV2:
			c.dynamic.Type = DynamicDescType_WithLiveV2
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithLiveV2)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin CardWithLiveV2 failed %v", err)
				return
			}
			c.dynamic.Live.Title = origin.GetLivePlayInfo().GetTitle()
			c.dynamic.Live.CoverUrl = origin.GetLivePlayInfo().GetCover()
		case DynamicDescType_WithMylist:
			c.dynamic.Type = DynamicDescType_WithMylist
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithMylist)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin CardWithMylist failed %v", err)
				return
			}
			c.dynamic.MyList.Title = origin.GetTitle()
			c.dynamic.MyList.CoverUrl = origin.GetCover()
		case DynamicDescType_WithMiss:
			c.dynamic.Type = DynamicDescType_WithMiss
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			c.dynamic.Miss.Tips = cardOrigin.GetItem().GetTips()
		case DynamicDescType_WithOrigin:
			c.dynamic.Type = DynamicDescType_WithOrigin
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
		case DynamicDescType_WithCourse:
			c.dynamic.Type = DynamicDescType_WithCourse
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			origin := new(CardWithCourse)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err != nil {
				log.WithField("origin", cardOrigin.GetOrigin()).Errorf("Unmarshal origin CardWithCourse failed %v", err)
				return
			}
			c.dynamic.Course.Name = origin.GetUpInfo().GetName()
			c.dynamic.Course.Badge = origin.GetBadge().GetText()
			c.dynamic.Course.Title = origin.GetTitle()
			c.dynamic.Course.CoverUrl = origin.GetCover()
		default:
			c.dynamic.Type = DynamicDescType_DynamicDescTypeUnknown
			c.dynamic.Content = replaseDesc(cardOrigin.GetItem().GetContent(), detail.Content)
			// 试试media
			origin := new(CardWithMedia)
			err := json.Unmarshal([]byte(cardOrigin.GetOrigin()), origin)
			if err == nil && origin.GetApiSeasonInfo() != nil {
				var desc = origin.GetNewDesc()
				if len(desc) == 0 {
					desc = origin.GetIndex()
				}
				c.dynamic.Default.TypeName = origin.GetApiSeasonInfo().GetTypeName()
				c.dynamic.Default.Title = origin.GetApiSeasonInfo().GetTitle()
				c.dynamic.Default.CoverUrl = origin.GetCover()
			} else if originDetail.PGC.Title != "" {
				c.dynamic.Default.TypeName = originDetail.PGC.Type
				c.dynamic.Default.Title = originDetail.PGC.Title
				c.dynamic.Default.CoverUrl = originDetail.PGC.CoverUrl
			} else if cardOrigin.GetOrigin() == "源动态不见了" {
				c.dynamic.Default.TypeName = "不支持的"
				c.dynamic.Default.Title = "未知动态"
				c.dynamic.Default.Desc = "源动态不见了"
			} else {
				log.WithField("content", card.GetCard()).Info("found new type with origin")
				c.dynamic.OriginUser.Name = originName
			}
		}
	case DynamicDescType_WithImage:
		c.dynamic.Type = DynamicDescType_WithImage
		cardImage, err := card.GetCardWithImage()
		if err != nil {
			log.WithField("card", card).Errorf("GetCardWithImage cast failed %v", err)
			return
		}
		c.dynamic.Image.Description = replaseDesc(cardImage.GetItem().GetDescription(), detail.Content)
		// 输出urls
		var urls = make([]string, len(cardImage.GetItem().GetPictures()))
		for index, pic := range cardImage.GetItem().GetPictures() {
			urls[index] = pic.GetImgSrc()
		}
		c.dynamic.Image.ImageUrls = urls
		// 多图合一
		if shouldCombineImage(cardImage.GetItem().GetPictures()) {
			var urls = make([]string, len(cardImage.GetItem().GetPictures()))
			for index, pic := range cardImage.GetItem().GetPictures() {
				urls[index] = pic.GetImgSrc()
			}
			resultByte, err := urlsMergeImage(urls)
			if err != nil {
				log.Errorf("urlsMergeImage failed %v", err)
			} else {
				c.dynamic.Image.Bytes = resultByte
			}
		}
	case DynamicDescType_TextOnly:
		c.dynamic.Type = DynamicDescType_TextOnly
		cardText, err := card.GetCardTextOnly()
		if err != nil {
			log.WithField("card", card).Errorf("GetCardTextOnly cast failed %v", err)
			return
		}
		c.dynamic.Content = replaseDesc(cardText.GetItem().GetContent(), detail.Content)
	case DynamicDescType_WithVideo:
		c.dynamic.Type = DynamicDescType_WithVideo
		cardVideo, err := card.GetCardWithVideo()
		if err != nil {
			log.WithField("card", card).Errorf("GetCardWithVideo cast failed %v", err)
			return
		}
		description := strings.TrimSpace(cardVideo.GetDynamic())
		if description == "" {
			description = cardVideo.GetDesc()
		}
		if description == cardVideo.GetTitle() {
			description = ""
		}
		actionText := card.GetDisplay().GetUsrActionTxt()
		c.dynamic.Video.Action = actionText
		c.dynamic.Video.Title = cardVideo.GetTitle()
		c.dynamic.Video.Dynamic = replaseDesc(cardVideo.GetDynamic(), detail.Content)
		if len(description) != 0 {
			c.dynamic.Video.Desc = description
		}
		c.dynamic.Video.CoverUrl = cardVideo.GetPic()
	case DynamicDescType_WithPost:
		c.dynamic.Type = DynamicDescType_WithPost
		cardPost, err := card.GetCardWithPost()
		if err != nil {
			log.WithField("card", card).Errorf("GetCardWithPost cast failed %v", err)
			return
		}
		c.dynamic.Post.Title = cardPost.Title
		c.dynamic.Post.Summary = cardPost.Summary
		if len(cardPost.GetImageUrls()) >= 1 {
			c.dynamic.Post.ImageUrls = cardPost.GetImageUrls()
		} else if len(cardPost.GetBannerUrl()) != 0 {
			c.dynamic.Post.ImageUrls = []string{cardPost.GetBannerUrl()}
		}
	case DynamicDescType_WithMusic:
		c.dynamic.Type = DynamicDescType_WithMusic
		cardMusic, err := card.GetCardWithMusic()
		if err != nil {
			log.WithField("card", card).
				Errorf("GetCardWithMusic cast failed %v", err)
			return
		}
		c.dynamic.Music.Title = cardMusic.GetTitle()
		c.dynamic.Music.Intro = cardMusic.GetIntro()
		c.dynamic.Music.Author = cardMusic.GetAuthor()
		c.dynamic.Music.CoverUrl = cardMusic.GetCover()
	case DynamicDescType_WithSketch:
		c.dynamic.Type = DynamicDescType_WithSketch
		cardSketch, err := card.GetCardWithSketch()
		if err != nil {
			log.WithField("card", card).
				Errorf("GetCardWithSketch cast failed %v", err)
			return
		}
		c.dynamic.Sketch.Content = replaseDesc(cardSketch.GetVest().GetContent(), detail.Content)
		if cardSketch.GetSketch().GetTitle() == cardSketch.GetSketch().GetDescText() {
			c.dynamic.Sketch.Title = cardSketch.GetSketch().GetTitle()
		} else {
			c.dynamic.Sketch.Title = cardSketch.GetSketch().GetTitle()
			c.dynamic.Sketch.DescText = cardSketch.GetSketch().GetDescText()
		}
		if len(cardSketch.GetSketch().GetCoverUrl()) > 0 {
			c.dynamic.Sketch.CoverUrl = cardSketch.GetSketch().GetCoverUrl()
		}
	case DynamicDescType_WithLive:
		c.dynamic.Type = DynamicDescType_WithLive
		cardLive, err := card.GetCardWithLive()
		if err != nil {
			log.WithField("card", card).
				Errorf("GetCardWithLive cast failed %v", err)
			return
		}
		c.dynamic.Live.Title = cardLive.GetTitle()
		c.dynamic.Live.CoverUrl = cardLive.GetCover()
	case DynamicDescType_WithLiveV2:
		c.dynamic.Type = DynamicDescType_WithLiveV2
		// 2021-08-15 发现这个是系统推荐的直播间，应该不是人为操作，选择不推送，在filter中过滤
		cardLiveV2, err := card.GetCardWithLiveV2()
		if err != nil {
			log.WithField("card", card).
				Errorf("GetCardWithLiveV2 case failed %v", err)
			return
		}
		c.dynamic.Live.Title = cardLiveV2.GetLivePlayInfo().GetTitle()
		c.dynamic.Live.CoverUrl = cardLiveV2.GetLivePlayInfo().GetCover()
	case DynamicDescType_WithMiss:
		c.dynamic.Type = DynamicDescType_WithMiss
		cardWithMiss, err := card.GetCardWithOrig()
		if err != nil {
			log.WithField("card", card).
				Errorf("GetCardWithOrig case failed %v", err)
			return
		}
		c.dynamic.Content = replaseDesc(cardWithMiss.GetItem().GetContent(), detail.Content)
		c.dynamic.Miss.Tips = cardWithMiss.GetItem().GetTips()
	default:
		c.dynamic.Type = DynamicDescType_DynamicDescTypeUnknown
		log.WithField("content", card.GetCard()).Info("found new DynamicDescType")
	}

	// 2021/04/16发现了有新增一个预约卡片
	for _, addons := range [][]*Card_Display_AddOnCardInfo{
		card.GetDisplay().GetAddOnCardInfo(),
		card.GetDisplay().GetOrigin().GetAddOnCardInfo(),
	} {
		i := 0
		for _, addon := range addons {
			var addOn Addon
			switch addon.AddOnCardShowType {
			case AddOnCardShowType_goods:
				addOn.Type = AddOnCardShowType_goods
				goodsCard := new(Card_Display_AddOnCardInfo_GoodsCard)
				if err := json.Unmarshal([]byte(addon.GetGoodsCard()), goodsCard); err != nil {
					log.WithField("goods", addon.GetGoodsCard()).Errorf("Unmarshal goods card failed %v", err)
					continue
				}
				if len(goodsCard.GetList()) == 0 {
					continue
				}
				var item = goodsCard.GetList()[0]
				addOn.Goods.AdMark = item.AdMark
				addOn.Goods.Name = item.Name
				addOn.Goods.ImageUrl = item.GetImg()
			case AddOnCardShowType_reserve:
				if len(addon.GetReserveAttachCard().GetReserveLottery().GetText()) == 0 {
					addOn.Reserve.Title = addon.GetReserveAttachCard().GetTitle()
					addOn.Reserve.Desc = addon.GetReserveAttachCard().GetDescFirst().GetText()
				} else {
					addOn.Reserve.Title = addon.GetReserveAttachCard().GetTitle()
					addOn.Reserve.Desc = addon.GetReserveAttachCard().GetDescFirst().GetText()
					addOn.Reserve.Lottery = addon.GetReserveAttachCard().GetReserveLottery().GetText()
				}
			case AddOnCardShowType_match:
			// TODO 暂时没必要
			case AddOnCardShowType_related:
				aCard := addon.GetAttachCard()
				// 游戏应该不需要
				if aCard.GetType() != "game" {
					addOn.Related.Type = aCard.GetType()
					addOn.Related.Title = aCard.GetTitle()
					addOn.Related.HeadText = aCard.GetHeadText()
					addOn.Related.Desc = aCard.GetDescFirst()
				}
			case AddOnCardShowType_vote:
				var Idx []int32
				var Desc []string
				textCard := new(Card_Display_AddOnCardInfo_TextVoteCard)
				if err := json.Unmarshal([]byte(addon.GetVoteCard()), textCard); err == nil {
					for _, opt := range textCard.GetOptions() {
						Idx = append(Idx, opt.GetIdx())
						Desc = append(Desc, opt.GetDesc())
					}
				} else {
					log.WithField("content", addon.GetVoteCard()).Info("found new VoteCard")
				}
				addOn.Vote.Index = Idx
				addOn.Vote.Desc = Desc
			case AddOnCardShowType_video:
				ugcCard := addon.GetUgcAttachCard()
				addOn.Video.Title = ugcCard.GetTitle()
				addOn.Video.CoverUrl = ugcCard.GetImageUrl()
				addOn.Video.Desc = ugcCard.GetDescSecond()
				addOn.Video.PlayUrl = ugcCard.GetPlayUrl()
			default:
				if b, err := json.Marshal(card.GetDisplay()); err != nil {
					log.WithField("content", card).Errorf("found new AddOnCardShowType but marshal failed %v", err)
				} else {
					log.WithField("content", string(b)).Info("found new AddOnCardShowType")
				}
			}
			c.dynamic.Addons = append(c.dynamic.Addons, addOn)
			i++
		}
	}
	c.dynamic.DynamicUrl = dynamicUrl
}

func (c *CacheCard) GetMSG() *mmsg.MSG {
	c.once.Do(func() {
		c.prepare()
		var data = map[string]interface{}{
			"dynamic":     c.dynamic,
			"msg":         c.orgMsg,
			"group_code":  c.GroupCode,
			"parse_post":  config.GlobalConfig.GetBool("bilibili.autoParsePosts"),
			"dynamic_raw": c.dynamicRaw,
		}
		var err error
		c.msgCache, err = template.LoadAndExec("notify.group.bilibili.news.tmpl", data)
		if err != nil {
			logger.Errorf("bilibili: NewsInfo LoadAndExec error %v", err)
		}
		return
	})
	return c.msgCache
}

func SecAnalysis(id string) (result map[string]interface{}) {
	if !config.GlobalConfig.GetBool("bilibili.secAnalysis") {
		return
	}
	Url := BPath(PathWebDynamicDetail)
	params := map[string]string{
		"id":       id,
		"features": "itemOpusStyle,opusBigCover,onlyfansVote,endFooterHidden,decorationCard,onlyfansAssetsV2,ugcDelete,onlyfansQaCard,editable,opusPrivateVisible,avatarAutoTheme",
	}
	var resp bytes.Buffer
	var opts = []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(3),
	}
	err := requests.Get(Url, params, &resp, opts...)
	if err != nil {
		logger.WithField("url", Url).Errorf("SecAnalysis get failed %v", err)
		return
	}
	if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
		logger.WithError(err).Error("SecAnalysis unmarshal failed")
		return
	}
	return
}

func getDescContent(resp map[string]interface{}, repost bool) (result DynamicDetail) {
	code, ok := resp["code"].(float64)
	if !ok || code != 0 {
		return
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		return
	}
	item, ok := data["item"].(map[string]interface{})
	if !ok {
		return
	}
	searchDesc := func(modules map[string]interface{}) (res DynamicDetail) {
		if modules == nil {
			return
		}
		dynamic, ok := modules["module_dynamic"].(map[string]interface{})
		if !ok {
			return
		}

		if topic, ok := dynamic["topic"].(map[string]interface{}); ok {
			if name, ok := topic["name"].(string); ok {
				res.TopicName = "#" + name + "#"
			}
		}

		if desc, ok := dynamic["desc"].(map[string]interface{}); ok {
			text, ok := desc["text"].(string)
			if ok {
				res.Content = text
				return
			}
		}

		if major, ok := dynamic["major"].(map[string]interface{}); ok {
			if opus, ok := major["opus"].(map[string]interface{}); ok {
				if title, ok := opus["title"].(string); ok {
					res.Title = title
				}
				if summary, ok := opus["summary"].(map[string]interface{}); ok {
					text := summary["text"].(string)
					res.Content = text
				}
			}

			if pgc, ok := major["pgc"].(map[string]interface{}); ok {
				if badge, ok := pgc["badge"].(map[string]interface{}); ok {
					res.PGC.Type = badge["text"].(string)
				}
				if title, ok := pgc["title"].(string); ok {
					res.PGC.Title = title
				}
				if cover, ok := pgc["cover"].(string); ok {
					res.PGC.CoverUrl = cover
				}
			}
		}

		if additional, ok := dynamic["additional"].(map[string]interface{}); ok {
			if additional["type"] == "ADDITIONAL_TYPE_RESERVE" {
				if reserve, ok := additional["reserve"].(map[string]interface{}); ok {
					res.Reserve.Title = reserve["title"].(string)
					res.Reserve.Desc1 = reserve["desc1"].(map[string]interface{})["text"].(string)
					res.Reserve.Desc2 = reserve["desc2"].(map[string]interface{})["text"].(string)
					if reserve["desc3"] != nil {
						res.Reserve.Desc3 = reserve["desc3"].(map[string]interface{})["text"].(string)
					}
				}
			}
		}
		return
	}

	if repost {
		if orig, ok := item["orig"].(map[string]interface{}); ok {
			modules := orig["modules"].(map[string]interface{})
			result = searchDesc(modules)
		}
	} else {
		if modules, ok := item["modules"].(map[string]interface{}); ok {
			result = searchDesc(modules)
		}
	}
	return
}

func replaseDesc(text string, newText string) string {
	if newText == "" {
		return text
	}
	return newText
}
