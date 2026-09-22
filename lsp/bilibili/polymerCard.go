package bilibili

import (
	stdjson "encoding/json"
	"errors"
	"strconv"
	"strings"
)

// 本文件把网页新版动态的 item 转换成下游依赖的 *Card。
//
// 下游（CacheCard.prepare、filterCard、模板渲染、合并转发去重）全部按旧版
// 动态接口的 Card 结构编写：Desc 描述类型与作者，Card 字段是一段 JSON 字符串，
// 由 prepare() 按 Desc.Type 反序列化成 CardWithImage / CardTextOnly / ... 等结构。
// 因此这里必须把新版数据重新组装成那套旧结构，而不是改写下游。

// ErrPolymerItemInvalid 表示 item 缺少最小必要字段，无法转换。
var ErrPolymerItemInvalid = errors.New("polymer item 缺少 id_str，无法转换")

// missingOriginPlaceholder 是原动态不可见时写入 origin 字段的占位文案，
// prepare() 依赖这个固定值识别“源动态已删除”。
const missingOriginPlaceholder = "源动态不见了"

// polymerItemsToCards 批量转换，忽略单条失败，避免一条脏数据拖垮整轮刷新。
func polymerItemsToCards(items []*polymerItem) []*Card {
	cards := make([]*Card, 0, len(items))
	for _, item := range items {
		card, err := polymerItemToCard(item)
		if err != nil {
			logger.WithField("id_str", item.GetIDStr()).Warnf("跳过无法转换的动态 %v", err)
			continue
		}
		cards = append(cards, card)
	}
	return cards
}

func (i *polymerItem) GetIDStr() string {
	if i == nil {
		return ""
	}
	return i.IDStr
}

// polymerItemToCard 是转换入口。
func polymerItemToCard(item *polymerItem) (*Card, error) {
	if item == nil || len(item.IDStr) == 0 {
		return nil, ErrPolymerItemInvalid
	}
	author := item.author()
	descType := polymerDescType(item)

	cardJSON, err := buildLegacyCardJSON(item, descType)
	if err != nil {
		return nil, err
	}

	desc := &Card_Desc{
		DynamicId:    item.intId(),
		DynamicIdStr: item.IDStr,
		Timestamp:    item.timestamp(),
		Type:         descType,
		Uid:          author.Mid,
		UserProfile: &Card_Desc_UserProfile{
			Info: &Card_Desc_UserProfile_Info{
				Uid:   author.Mid,
				Uname: author.Name,
				Face:  author.Face,
			},
		},
		RidStr: item.basicRidStr(),
	}

	// 统计数字：旧接口直接放在 desc 里，这里从 module_stat 补上
	if stat := item.stat(); stat != nil {
		if stat.Comment != nil {
			desc.Comment = int32(stat.Comment.Count.Int64())
		}
		if stat.Like != nil {
			desc.Like = int32(stat.Like.Count.Int64())
		}
		if stat.Forward != nil {
			desc.Repost = int32(stat.Forward.Count.Int64())
		}
	}
	if archive := item.major().Archive; archive != nil {
		desc.Bvid = archive.Bvid
	}
	// 转发动态需要原动态 id，下游用它做合并转发去重
	if descType == DynamicDescType_WithOrigin && item.Orig != nil {
		desc.OrigDyId = item.Orig.intId()
		desc.OrigDyIdStr = item.Orig.IDStr
		desc.OrigType = polymerDescType(item.Orig)
	}

	return &Card{
		Card:    cardJSON,
		Desc:    desc,
		Display: &Card_Display{UsrActionTxt: author.PubAction},
	}, nil
}

// polymerDescType 推断动态类型。
//
// 注意 MAJOR_TYPE_MEDIALIST / COURSES / PGC / UGC_SEASON / COMMON 这几类，
// 旧接口虽然会把 desc.type 标成 WithMylist / WithCourse / WithAnime，
// 但 prepare() 的顶层 switch 并没有对应分支，最终会落到 default 分支渲染成空白。
// 它们都有 title/cover/desc，因此这里统一降级为 WithPost（旧版专栏结构），
// 让卡片仍有标题和封面可看，而不是推一条空消息。这是有意为之的偏差。
func polymerDescType(item *polymerItem) DynamicDescType {
	if item == nil {
		return DynamicDescType_DynamicDescTypeUnknown
	}
	if item.Type == "DYNAMIC_TYPE_FORWARD" {
		return DynamicDescType_WithOrigin
	}

	major := item.major()
	switch major.Type {
	case "MAJOR_TYPE_ARCHIVE":
		return DynamicDescType_WithVideo
	case "MAJOR_TYPE_ARTICLE":
		return DynamicDescType_WithPost
	case "MAJOR_TYPE_MUSIC":
		return DynamicDescType_WithMusic
	case "MAJOR_TYPE_OPUS", "MAJOR_TYPE_DRAW":
		if len(item.pictures()) > 0 {
			return DynamicDescType_WithImage
		}
		return DynamicDescType_TextOnly
	case "MAJOR_TYPE_LIVE_RCMD":
		// 与旧接口一致：系统推荐的直播间，下游 filterCard 会直接过滤
		return DynamicDescType_WithLiveV2
	case "MAJOR_TYPE_LIVE":
		return DynamicDescType_WithLive
	case "MAJOR_TYPE_MEDIALIST", "MAJOR_TYPE_COURSES", "MAJOR_TYPE_PGC",
		"MAJOR_TYPE_UGC_SEASON", "MAJOR_TYPE_COMMON":
		return DynamicDescType_WithPost
	case "MAJOR_TYPE_BLOCKED", "MAJOR_TYPE_NONE":
		return DynamicDescType_WithMiss
	}

	// 未知 major 类型：按内容降级，尽量别丢动态
	if len(item.pictures()) > 0 {
		return DynamicDescType_WithImage
	}
	if item.text() != "" || item.Type == "DYNAMIC_TYPE_WORD" {
		return DynamicDescType_TextOnly
	}
	return DynamicDescType_DynamicDescTypeUnknown
}

// buildLegacyCardJSON 生成对应类型的旧版 card JSON 字符串。
func buildLegacyCardJSON(item *polymerItem, t DynamicDescType) (string, error) {
	switch t {
	case DynamicDescType_WithOrigin:
		return buildOriginCardJSON(item)
	case DynamicDescType_WithImage:
		return marshalLegacy(map[string]interface{}{"item": imageItemMap(item)})
	case DynamicDescType_TextOnly:
		return marshalLegacy(map[string]interface{}{"item": textItemMap(item)})
	case DynamicDescType_WithVideo:
		return marshalLegacy(videoCardMap(item))
	case DynamicDescType_WithPost:
		return marshalLegacy(postCardMap(item))
	case DynamicDescType_WithMusic:
		return marshalLegacy(musicCardMap(item))
	case DynamicDescType_WithLive:
		return marshalLegacy(liveCardMap(item))
	case DynamicDescType_WithLiveV2:
		return buildLiveV2CardJSON(item)
	case DynamicDescType_WithMiss:
		return marshalLegacy(map[string]interface{}{
			"item": map[string]interface{}{
				"content":   item.text(),
				"timestamp": item.timestamp(),
				"reply":     0,
				"miss":      0,
				"tips":      item.blockedTips(),
			},
		})
	}
	// 其余类型下游没有分支，给个空对象保证反序列化不报错
	return "{}", nil
}

// buildOriginCardJSON 生成转发动态的 CardWithOrig。
// origin 字段本身是一段 JSON 字符串；原动态不可见时写入占位文案，
// prepare() 会识别这个占位值并走“源动态已删除”的兜底逻辑。
func buildOriginCardJSON(item *polymerItem) (string, error) {
	origType := DynamicDescType_WithMiss
	originJSON := missingOriginPlaceholder
	originUser := map[string]interface{}{
		"info": map[string]interface{}{"uid": 0, "uname": "", "face": ""},
	}

	if orig := item.Orig; orig != nil {
		converted, err := buildLegacyCardJSON(orig, polymerDescType(orig))
		if err != nil {
			logger.WithField("id_str", orig.IDStr).Warnf("原动态转换失败，按不可见处理 %v", err)
		} else {
			origType = polymerDescType(orig)
			originJSON = converted
			oa := orig.author()
			originUser = map[string]interface{}{
				"info": map[string]interface{}{
					"uid":   oa.Mid,
					"uname": oa.Name,
					"face":  oa.Face,
				},
			}
		}
	}

	return marshalLegacy(map[string]interface{}{
		"item": map[string]interface{}{
			"content":   item.text(),
			"timestamp": item.timestamp(),
			"orig_type": int32(origType),
			"reply":     0,
			"miss":      0,
			"tips":      "",
		},
		"origin":      originJSON,
		"origin_user": originUser,
	})
}

func imageItemMap(item *polymerItem) map[string]interface{} {
	pics := item.pictures()
	pictures := make([]map[string]interface{}, 0, len(pics))
	for _, p := range pics {
		pictures = append(pictures, map[string]interface{}{
			"img_src":    p.URL,
			"img_width":  int32(p.Width.Int64()),
			"img_height": int32(p.Height.Int64()),
			"img_size":   p.Size.Float64(),
		})
	}
	title := ""
	if opus := item.major().Opus; opus != nil {
		title = opus.Title
	}
	return map[string]interface{}{
		"id":             item.intId(),
		"title":          title,
		"description":    item.text(),
		"category":       "",
		"pictures":       pictures,
		"pictures_count": len(pictures),
		"upload_time":    item.timestamp(),
	}
}

func textItemMap(item *polymerItem) map[string]interface{} {
	return map[string]interface{}{
		"rp_id":     item.intId(),
		"uid":       item.author().Mid,
		"content":   item.text(),
		"ctrl":      "",
		"timestamp": item.timestamp(),
		"reply":     0,
	}
}

func videoCardMap(item *polymerItem) map[string]interface{} {
	archive := item.major().Archive
	if archive == nil {
		archive = new(polymerArchive)
	}
	return map[string]interface{}{
		"desc":     archive.Desc,
		"duration": parseDurationSeconds(archive.DurationText),
		"dynamic":  item.text(),
		"pubdate":  item.timestamp(),
		"title":    archive.Title,
		"tname":    "",
		"videos":   1,
		"pic":      archive.Cover,
		"origin": map[string]interface{}{
			"uid":            0,
			"type":           0,
			"dynamic_id_str": "",
			"bvid":           archive.Bvid,
		},
	}
}

func postCardMap(item *polymerItem) map[string]interface{} {
	var (
		title   string
		summary string
		covers  []string
		banner  string
	)
	major := item.major()
	if art := major.Article; art != nil {
		title = art.Title
		summary = art.Desc
		covers = art.Covers
		banner = art.Cover
	} else if common := major.Common; common != nil {
		title = common.Title
		summary = common.Desc
		banner = common.Cover
	} else if ml := major.Medialist; ml != nil {
		title = ml.Title
		banner = ml.Cover
	} else if courses := major.Courses; courses != nil {
		title = courses.Title
		banner = courses.Cover
	} else if pgc := major.PGC; pgc != nil {
		title = pgc.Title
		banner = pgc.Cover
	}
	if len(covers) == 0 && len(banner) > 0 {
		covers = []string{banner}
	}
	if summary == "" {
		summary = item.text()
	}
	return map[string]interface{}{
		"title":        title,
		"summary":      summary,
		"image_urls":   covers,
		"banner_url":   banner,
		"publish_time": item.timestamp(),
	}
}

func musicCardMap(item *polymerItem) map[string]interface{} {
	music := item.major().Music
	if music == nil {
		music = new(polymerMusic)
	}
	return map[string]interface{}{
		"author": music.Author,
		"cover":  music.Cover,
		"ctime":  item.timestamp(),
		"id":     music.ID.Int64(),
		"intro":  music.Intro,
		"title":  music.Title,
	}
}

func liveCardMap(item *polymerItem) map[string]interface{} {
	live := item.major().Live
	if live == nil {
		live = new(polymerLive)
	}
	roomID := live.RoomID.Int64()
	if roomID == 0 {
		roomID = live.Roomid.Int64()
	}
	return map[string]interface{}{
		"roomid":       roomID,
		"uid":          live.UID.Int64(),
		"uname":        item.author().Name,
		"cover":        live.Cover,
		"title":        live.Title,
		"live_status":  live.LiveStatus.Int64(),
		"round_status": 0,
	}
}

// buildLiveV2CardJSON 的 live_rcmd.content 是被转义的 JSON 字符串，需要再解一层。
func buildLiveV2CardJSON(item *polymerItem) (string, error) {
	rcmd := item.major().LiveRcmd
	info := map[string]interface{}{}
	style := int64(0)
	if rcmd != nil && rcmd.Content != "" {
		var content polymerLiveRcmdContent
		if err := stdjson.Unmarshal([]byte(rcmd.Content), &content); err != nil {
			logger.WithField("id_str", item.IDStr).Warnf("解析 live_rcmd.content 失败 %v", err)
		} else {
			style = content.Type.Int64()
			if lp := content.LivePlayInfo; lp != nil {
				info = map[string]interface{}{
					"cover":            lp.Cover,
					"title":            lp.Title,
					"room_id":          lp.RoomID.Int64(),
					"live_status":      lp.LiveStatus.Int64(),
					"link":             lp.Link,
					"uid":              lp.UID.Int64(),
					"live_id":          lp.LiveID.Int64(),
					"area_id":          lp.AreaID.Int64(),
					"area_name":        lp.AreaName,
					"parent_area_id":   lp.ParentAreaID.Int64(),
					"parent_area_name": lp.ParentAreaName,
					"room_type":        lp.RoomType.Int64(),
				}
			}
		}
	}
	return marshalLegacy(map[string]interface{}{
		"live_play_info": info,
		"style":          style,
		"type":           0,
	})
}

func (i *polymerItem) basicRidStr() string {
	if i.Basic == nil {
		return ""
	}
	return i.Basic.RidStr
}

func (i *polymerItem) stat() *polymerModuleStat {
	if i.Modules == nil {
		return nil
	}
	return i.Modules.ModuleStat
}

func (i *polymerItem) blockedTips() string {
	if b := i.major().Blocked; b != nil {
		return b.Toast
	}
	return ""
}

func marshalLegacy(v interface{}) (string, error) {
	b, err := stdjson.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseDurationSeconds 把 "01:48" / "1:02:03" 这类时长文本转成秒。
func parseDurationSeconds(text string) int32 {
	if text == "" {
		return 0
	}
	var total int64
	for _, part := range strings.Split(text, ":") {
		v, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return 0
		}
		total = total*60 + v
	}
	return int32(total)
}
