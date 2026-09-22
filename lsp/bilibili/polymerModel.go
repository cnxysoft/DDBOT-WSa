package bilibili

import (
	"strconv"
	"strings"
)

// 本文件定义网页新版动态（polymer）接口的原始结构。
// 字段名与 B 站返回保持一致，未使用的字段不声明，避免过度耦合。

// flexInt64 兼容同一字段既可能是 JSON 数字、也可能是 JSON 字符串的情况。
// 实测：feed/all 的 update_num 返回 "32"，feed/all/update 的 update_num 返回 0；
// 图片的 width/height 也会在数字与数字字符串之间摇摆。
// 解析失败时一律退化为 0，不让单个脏字段弄挂整份响应。
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt64(v)
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = flexInt64(v)
		return nil
	}
	*f = 0
	return nil
}

func (f flexInt64) Int64() int64 { return int64(f) }

// flexFloat64 与 flexInt64 同理，用于 size 这类浮点字段。
type flexFloat64 float64

func (f *flexFloat64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = flexFloat64(v)
		return nil
	}
	*f = 0
	return nil
}

func (f flexFloat64) Float64() float64 { return float64(f) }

// polymerFeedResponse 是 feed/all、feed/all/update、feed/space 共用的响应外壳。
type polymerFeedResponse struct {
	Code    int32            `json:"code"`
	Message string           `json:"message"`
	Data    *polymerFeedData `json:"data"`
}

func (r *polymerFeedResponse) GetCode() int32 {
	if r == nil {
		return 0
	}
	return r.Code
}

func (r *polymerFeedResponse) GetMessage() string {
	if r == nil {
		return ""
	}
	return r.Message
}

func (r *polymerFeedResponse) GetData() *polymerFeedData {
	if r == nil {
		return nil
	}
	return r.Data
}

// polymerFeedData 三个接口的 data 结构基本一致，只是 feed/all/update 只填 update_num。
type polymerFeedData struct {
	UpdateNum      flexInt64      `json:"update_num"`
	UpdateBaseline string         `json:"update_baseline"`
	Offset         string         `json:"offset"`
	HasMore        bool           `json:"has_more"`
	Total          flexInt64      `json:"total"`
	Items          []*polymerItem `json:"items"`
}

func (d *polymerFeedData) GetUpdateNum() int64 {
	if d == nil {
		return 0
	}
	return d.UpdateNum.Int64()
}

func (d *polymerFeedData) GetItems() []*polymerItem {
	if d == nil {
		return nil
	}
	return d.Items
}

// polymerItem 是一条动态。orig 是转发的原动态，结构与自身相同，因此递归定义。
type polymerItem struct {
	IDStr   string          `json:"id_str"`
	Type    string          `json:"type"`
	Basic   *polymerBasic   `json:"basic"`
	Modules *polymerModules `json:"modules"`
	Orig    *polymerItem    `json:"orig"`
}

type polymerBasic struct {
	RidStr  string `json:"rid_str"`
	JumpURL string `json:"jump_url"`
}

type polymerModules struct {
	ModuleAuthor  *polymerModuleAuthor  `json:"module_author"`
	ModuleDynamic *polymerModuleDynamic `json:"module_dynamic"`
	ModuleStat    *polymerModuleStat    `json:"module_stat"`
}

// polymerModuleAuthor 里 mid 是数字，但 pub_ts 是字符串，注意区分。
type polymerModuleAuthor struct {
	Mid       int64  `json:"mid"`
	Name      string `json:"name"`
	Face      string `json:"face"`
	PubTs     string `json:"pub_ts"`
	PubTime   string `json:"pub_time"`
	PubAction string `json:"pub_action"`
}

type polymerModuleDynamic struct {
	Topic *polymerTopic `json:"topic"`
	Desc  *polymerDesc  `json:"desc"`
	Major *polymerMajor `json:"major"`
}

type polymerTopic struct {
	Name string `json:"name"`
}

// polymerDesc 是动态正文。text 为纯文本，rich_text_nodes 内含表情、图片、专栏等节点。
type polymerDesc struct {
	Text          string        `json:"text"`
	RichTextNodes []interface{} `json:"rich_text_nodes"`
}

// polymerMajor 是动态主体。type 决定下面哪个字段有效，其余为 null。
type polymerMajor struct {
	Type      string            `json:"type"`
	Archive   *polymerArchive   `json:"archive"`
	PGC       *polymerPGC       `json:"pgc"`
	Courses   *polymerCourses   `json:"courses"`
	Draw      *polymerDraw      `json:"draw"`
	Article   *polymerArticle   `json:"article"`
	Music     *polymerMusic     `json:"music"`
	Common    *polymerCommon    `json:"common"`
	Live      *polymerLive      `json:"live"`
	LiveRcmd  *polymerLiveRcmd  `json:"live_rcmd"`
	Medialist *polymerMedialist `json:"medialist"`
	Opus      *polymerOpus      `json:"opus"`
	Blocked   *polymerBlocked   `json:"blocked"`
}

// polymerBlocked 仅用于判断“内容不可见”，字段本身不重要。
type polymerBlocked struct {
	Toast string `json:"toast"`
}

type polymerArchive struct {
	Bvid         string              `json:"bvid"`
	Aid          string              `json:"aid"`
	Cover        string              `json:"cover"`
	JumpURL      string              `json:"jump_url"`
	Title        string              `json:"title"`
	Desc         string              `json:"desc"`
	DurationText string              `json:"duration_text"`
	Stat         *polymerArchiveStat `json:"stat"`
}

type polymerArchiveStat struct {
	Danmaku string `json:"danmaku"`
	Play    string `json:"play"`
}

// polymerOpus 是当前网页端最常用的主体，图文动态也走这里。
type polymerOpus struct {
	JumpURL string        `json:"jump_url"`
	Title   string        `json:"title"`
	Summary *polymerDesc  `json:"summary"`
	Pics    []*polymerPic `json:"pics"`
}

type polymerPic struct {
	URL    string      `json:"url"`
	Width  flexInt64   `json:"width"`
	Height flexInt64   `json:"height"`
	Size   flexFloat64 `json:"size"`
}

// polymerDraw 是较早的图集主体，图片字段名与 opus 不同。
type polymerDraw struct {
	ID    flexInt64         `json:"id"`
	Items []*polymerDrawPic `json:"items"`
}

type polymerDrawPic struct {
	Src    string      `json:"src"`
	Width  flexInt64   `json:"width"`
	Height flexInt64   `json:"height"`
	Size   flexFloat64 `json:"size"`
}

type polymerArticle struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Desc    string   `json:"desc"`
	Covers  []string `json:"covers"`
	Cover   string   `json:"cover"`
	JumpURL string   `json:"jump_url"`
}

type polymerMusic struct {
	ID      flexInt64 `json:"id"`
	Title   string    `json:"title"`
	Cover   string    `json:"cover"`
	Intro   string    `json:"intro"`
	Author  string    `json:"author"`
	JumpURL string    `json:"jump_url"`
}

type polymerPGC struct {
	Title   string          `json:"title"`
	Cover   string          `json:"cover"`
	JumpURL string          `json:"jump_url"`
	Badge   *polymerPGCText `json:"badge"`
}

type polymerPGCText struct {
	Text string `json:"text"`
}

type polymerCourses struct {
	Title  string            `json:"title"`
	Cover  string            `json:"cover"`
	URL    string            `json:"url"`
	Badge  *polymerPGCText   `json:"badge"`
	UpInfo *polymerCoursesUp `json:"up_info"`
}

type polymerCoursesUp struct {
	Name string `json:"name"`
}

type polymerCommon struct {
	Title   string `json:"title"`
	Desc    string `json:"desc"`
	Cover   string `json:"cover"`
	JumpURL string `json:"jump_url"`
}

type polymerLive struct {
	Title      string    `json:"title"`
	Cover      string    `json:"cover"`
	RoomID     flexInt64 `json:"room_id"`
	Roomid     flexInt64 `json:"roomid"`
	UID        flexInt64 `json:"uid"`
	LiveStatus flexInt64 `json:"live_status"`
}

// polymerLiveRcmd 的 content 是一段被转义的 JSON 字符串，里面才是 live_play_info。
type polymerLiveRcmd struct {
	ReserveType flexInt64 `json:"reserve_type"`
	Content     string    `json:"content"`
}

type polymerLiveRcmdContent struct {
	Type         flexInt64            `json:"type"`
	LivePlayInfo *polymerLivePlayInfo `json:"live_play_info"`
}

type polymerLivePlayInfo struct {
	RoomID         flexInt64 `json:"room_id"`
	UID            flexInt64 `json:"uid"`
	LiveStatus     flexInt64 `json:"live_status"`
	Title          string    `json:"title"`
	Cover          string    `json:"cover"`
	Link           string    `json:"link"`
	LiveID         flexInt64 `json:"live_id"`
	AreaID         flexInt64 `json:"area_id"`
	AreaName       string    `json:"area_name"`
	ParentAreaID   flexInt64 `json:"parent_area_id"`
	ParentAreaName string    `json:"parent_area_name"`
	RoomType       flexInt64 `json:"room_type"`
}

type polymerMedialist struct {
	Title   string `json:"title"`
	Cover   string `json:"cover"`
	JumpURL string `json:"jump_url"`
}

type polymerModuleStat struct {
	Forward *polymerStatItem `json:"forward"`
	Comment *polymerStatItem `json:"comment"`
	Like    *polymerStatItem `json:"like"`
}

type polymerStatItem struct {
	Count flexInt64 `json:"count"`
}

// author 返回动态作者信息，缺失时返回空结构，避免调用方到处判空。
func (i *polymerItem) author() *polymerModuleAuthor {
	if i == nil || i.Modules == nil || i.Modules.ModuleAuthor == nil {
		return new(polymerModuleAuthor)
	}
	return i.Modules.ModuleAuthor
}

func (i *polymerItem) dynamic() *polymerModuleDynamic {
	if i == nil || i.Modules == nil || i.Modules.ModuleDynamic == nil {
		return new(polymerModuleDynamic)
	}
	return i.Modules.ModuleDynamic
}

func (i *polymerItem) major() *polymerMajor {
	if m := i.dynamic().Major; m != nil {
		return m
	}
	return new(polymerMajor)
}

// timestamp 把 pub_ts 字符串转成 Unix 秒。
func (i *polymerItem) timestamp() int64 {
	v, err := strconv.ParseInt(i.author().PubTs, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func (i *polymerItem) intId() int64 {
	v, err := strconv.ParseInt(i.IDStr, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// text 取正文：优先 desc，其次 opus.summary，最后 opus.title。
func (i *polymerItem) text() string {
	d := i.dynamic()
	if d.Desc != nil && d.Desc.Text != "" {
		return d.Desc.Text
	}
	if opus := i.major().Opus; opus != nil {
		if opus.Summary != nil && opus.Summary.Text != "" {
			return opus.Summary.Text
		}
		return opus.Title
	}
	return ""
}

// pictures 汇总图片，兼容 opus.pics 与 draw.items 两种形态。
func (i *polymerItem) pictures() []*polymerPic {
	m := i.major()
	if opus := m.Opus; opus != nil && len(opus.Pics) > 0 {
		return opus.Pics
	}
	if draw := m.Draw; draw != nil && len(draw.Items) > 0 {
		pics := make([]*polymerPic, 0, len(draw.Items))
		for _, it := range draw.Items {
			pics = append(pics, &polymerPic{
				URL:    it.Src,
				Width:  it.Width,
				Height: it.Height,
				Size:   it.Size,
			})
		}
		return pics
	}
	return nil
}
