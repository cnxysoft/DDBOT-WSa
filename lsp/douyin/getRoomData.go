package douyin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

// douyinLivePageImage 是房间封面的图片列表。
type douyinLivePageImage struct {
	URLList []string `json:"url_list"`
}

// douyinWebImage 是网页接口返回的通用图片结构。
// 成品 reflect 元数据（types-closure-x64.tsv:6978）只有 URLList 一个字段，size=24。
type douyinWebImage struct {
	URLList []string `json:"url_list"`
}

// douyinLivePageRoom 是直播页/直播间接口解析出的房间信息。
type douyinLivePageRoom struct {
	IDStr         string                   `json:"id_str"`
	Status        int32                    `json:"status"`
	StatusStr     string                   `json:"status_str"`
	Title         string                   `json:"title"`
	UserCountStr  string                   `json:"user_count_str"`
	Cover         *douyinLivePageImage     `json:"cover"`
	Owner         *douyinLivePageOwner     `json:"owner"`
	RoomViewStats *douyinLivePageViewStats `json:"room_view_stats"`
}

// douyinLivePageOwner 是房间主播信息。
// 成品 reflect 元数据（types-closure-x64.tsv:9472）size=56：IDStr@0/SecUID@16/
// Nickname@32/AvatarThumb *douyinLivePageImage@48——无 WebRID 字段。
type douyinLivePageOwner struct {
	IDStr       string               `json:"id_str"`
	SecUID      string               `json:"sec_uid"`
	Nickname    string               `json:"nickname"`
	AvatarThumb *douyinLivePageImage `json:"avatar_thumb"`
}

// douyinLivePageViewStats 是展示用的观众数文本。
// 成品 reflect 元数据（types-closure-x64.tsv:9473）size=56：
// DisplayShort@0/DisplayMiddle@16/DisplayLong@32/DisplayValue int32@48。
type douyinLivePageViewStats struct {
	DisplayShort  string `json:"display_short"`
	DisplayMiddle string `json:"display_middle"`
	DisplayLong   string `json:"display_long"`
	DisplayValue  int32  `json:"display_value"`
}

// douyinRoomProfile 是用户资料接口 user.room_data 解析出的房间资料。
// 成品 reflect 元数据（types-closure-x64.tsv:9474）size=56：
// IDStr@0 / Title@16 / Owner struct{WebRID}@32 / Cover *douyinWebImage@48。
// 成品由 parseDouyinProfileRoom 直接 json.Unmarshal 到该类型（0xd639e0），
// WebRID 归属这里而不是 douyinLivePageOwner。
type douyinRoomProfile struct {
	IDStr string `json:"id_str"`
	Title string `json:"title"`
	Owner struct {
		WebRID string `json:"web_rid"`
	} `json:"owner"`
	Cover *douyinWebImage `json:"cover"`
}

// 房间状态常量（status=2 为开播，沿用本地分支语义）。
const douyinRoomStatusLiving = 2

// toProtoRoom 转换为 proto Room 结构。
func (r *douyinLivePageRoom) toProtoRoom() *Room {
	if r == nil {
		return nil
	}
	room := &Room{
		IdStr:        r.IDStr,
		Status:       r.Status,
		StatusStr:    r.StatusStr,
		Title:        r.Title,
		UserCountStr: r.UserCountStr,
	}
	if r.Cover != nil {
		room.Cover = &Cover{UrlList: r.Cover.URLList}
	}
	if r.Owner != nil {
		owner := &Owner{
			IdStr:    r.Owner.IDStr,
			SecUid:   r.Owner.SecUID,
			Nickname: r.Owner.Nickname,
		}
		if r.Owner.AvatarThumb != nil {
			owner.AvatarThumb = &Avatar{UrlList: r.Owner.AvatarThumb.URLList}
		}
		room.Owner = owner
	}
	if r.RoomViewStats != nil {
		room.RoomViewStats = &RoomViewStats{
			DisplayShort:  r.RoomViewStats.DisplayShort,
			DisplayMiddle: r.RoomViewStats.DisplayMiddle,
			DisplayLong:   r.RoomViewStats.DisplayLong,
			DisplayValue:  r.RoomViewStats.DisplayValue,
		}
	}
	return room
}

// GetRoomDataWithRoomID 按三级回退获取直播间详情：
// 精简直播接口 -> 签名接口 -> 直播页首屏数据。
func GetRoomDataWithRoomID(webRID string) (*douyinLiveSnapshot, error) {
	// 成品 GetRoomDataWithRoomID @0xd84dc0 +0x61 判空（strings.TrimSpace 后）→
	// fmt.Errorf("web_rid 不能为空")(@0x10b51e7,len20)。空 web_rid 直接返回错误。
	if strings.TrimSpace(webRID) == "" {
		return nil, errors.New("web_rid 不能为空")
	}
	// 成品 @0xd84dc0 含重试循环（cmp $0x3 上界，字段 attempt/room_id/web_rid/detail_source）：
	// 首轮标题或封面为空时重试，重试成功走 Info("抖音直播间标题和封面重试获取成功",
	// @0x1126180/0x10dbbcd,len48)；[推断] 重试条件取「标题与封面任一为空」。
	var (
		room   *Room
		source string
		err    error
	)
	attempt := 0
	for attempt = 1; attempt <= 3; attempt++ {
		room, source, err = getDouyinRoomDataWithFallback(webRID)
		if err != nil {
			continue
		}
		if room.GetTitle() != "" && firstDouyinRoomCoverURL(room) != "" {
			break
		}
	}
	if room == nil {
		if err == nil {
			err = errors.New("抖音直播间标题和封面为空")
		}
		return nil, err
	}
	snapshot := &douyinLiveSnapshot{
		Living: room.GetStatus() == douyinRoomStatusLiving,
		RoomID: room.GetIdStr(),
		WebRID: webRID,
		Title:  room.GetTitle(),
		Cover:  firstDouyinRoomCoverURL(room),
	}
	if owner := room.GetOwner(); owner != nil {
		snapshot.SecUID = owner.GetSecUid()
		snapshot.Nickname = owner.GetNickname()
		if owner.GetIdStr() != "" {
			snapshot.UserID = owner.GetIdStr()
		}
		if avatar := owner.GetAvatarThumb(); avatar != nil && len(avatar.GetUrlList()) > 0 {
			snapshot.Avatar = avatar.GetUrlList()[0]
		}
	}
	// 成品重试成功（attempt>1）用 Info + 四字段；首轮成功沿用原有 Debug 成功日志。
	if attempt > 1 {
		logger.WithField("web_rid", webRID).
			WithField("room_id", snapshot.RoomID).
			WithField("attempt", attempt).
			WithField("detail_source", source).
			Info("抖音直播间标题和封面重试获取成功")
	} else {
		logger.WithField("enter_source", source).
			WithField("roomId", snapshot.RoomID).
			WithField("webRid", snapshot.WebRID).
			Debug("抖音直播间详情获取成功")
	}
	return snapshot, nil
}

// getDouyinRoomDataWithFallback 执行三级回退，返回房间与数据源标记。
func getDouyinRoomDataWithFallback(webRID string) (*Room, string, error) {
	room, err := getDouyinRoomDataFromMinimalAPI(webRID)
	if err == nil {
		return room, douyinRequestTypeMinimalRoomAPI, nil
	}
	logger.WithError(err).Debug("抖音精简直播接口不可用，已由签名接口取得标题和封面")

	room, err = getDouyinRoomDataFromSignedAPI(webRID)
	if err == nil {
		return room, douyinRequestTypeSignedRoomAPI, nil
	}
	logger.WithError(err).Debug("抖音直播详情 API 不可用，已从直播页首屏数据取得标题和封面")

	room, err = getDouyinRoomDataFromPage(webRID)
	if err != nil {
		return nil, "", fmt.Errorf("抖音直播详情的精简API、签名API与直播页回退均失败: %w", err)
	}
	return room, douyinRequestTypeLivePageSSR, nil
}

// getDouyinRoomDataFromMinimalAPI 走精简直播接口（参数最少化）。
func getDouyinRoomDataFromMinimalAPI(webRID string) (*Room, error) {
	return requestDouyinRoomData(buildMinimalDouyinLiveRoomURL(webRID), douyinRequestTypeMinimalRoomAPI)
}

// buildMinimalDouyinLiveRoomURL 构造精简 enter 接口地址。
func buildMinimalDouyinLiveRoomURL(webRID string) string {
	return BaseLiveHost + "/webcast/room/web/enter/?" +
		"aid=" + douyinAidValue +
		"&device_platform=webapp" +
		"&web_rid=" + webRID
}

// getDouyinRoomDataFromSignedAPI 走带 a_bogus 签名的 enter 接口。
// 成品同一路径由 buildSignedDouyinLiveRoomURL 构造签名地址（abogus.go:122）。
func getDouyinRoomDataFromSignedAPI(webRID string) (*Room, error) {
	return requestDouyinRoomData(buildSignedDouyinLiveRoomURL(webRID), douyinRequestTypeSignedRoomAPI)
}

// requestDouyinRoomData 请求并解析 enter 接口响应。
func requestDouyinRoomData(rawURL string, requestType string) (*Room, error) {
	trace := beginDouyinAPIRequest(requestType, "GET", rawURL)
	release, err := acquireDouyinRequestSlotWithWait(context.Background(), trace)
	if err != nil {
		return nil, fmt.Errorf("获取抖音直播间数据失败: %w", err)
	}
	defer release()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	// 成品按调用路径分派 options：getDouyinRoomDataFromMinimalAPI(@getRoomData.go:81 +0xa9)
	// 用 douyinMinimalLiveRequestOptions；getDouyinRoomDataFromSignedAPI(@getRoomData.go:102 +0xf5)
	// 用 douyinWebAPIRequestOptions，随后都把 options 作为参数传入 requestDouyinRoomData
	// (@+0xea / +0x136)。重建版由 requestType 选择同一套 options（requestDouyinRoomData
	// 自身只追加 Referer）。
	var opts []requests.Option
	if requestType == douyinRequestTypeSignedRoomAPI {
		opts = douyinWebAPIRequestOptions(trace)
	} else {
		opts = douyinMinimalLiveRequestOptions(trace)
	}
	opts = append(opts, requests.HeaderOption("Referer", BaseLiveHost+"/"))
	reqErr := requests.GetWithHeader(rawURL, nil, &resp, &respHeaders, opts...)
	trace.finish(reqErr)
	if reqErr != nil {
		return nil, fmt.Errorf("获取抖音直播间数据失败: %w", reqErr)
	}
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		return nil, fmt.Errorf("解压缩抖音直播间响应失败: %w", err)
	}
	// 成品 requestDouyinRoomData @getRoomData.go:110 +0x459 调 normalizeDouyinRoomJSON
	// 做响应体校验/规整（BOM 去壳 + TrimSpace + 空响应/非 JSON 两条错误分支）。
	body, err = normalizeDouyinRoomJSON(body, respHeaders.ContentType, respHeaders.ContentLength, respHeaders.ContentEncoding)
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析抖音直播间数据失败: %w", err)
	}
	// [推断] 成品二进制里 douyinInt64 只有 2 个调用点（link_parser.go），
	// 本处是重建自身的 status_code 读取；签名对齐为 douyinInt64(value interface{}) 后
	// 等价改写（行为不变）。
	statusCode := int(douyinInt64(raw["status_code"]))
	room := findDouyinLivePageRoom(raw)
	if room == nil {
		return nil, fmt.Errorf("抖音直播间接口返回 status_code=%d，room_count=%d", statusCode, 0)
	}
	return room.toProtoRoom(), nil
}

// getDouyinRoomDataFromPage 拉取并解析直播页首屏 SSR 数据。
func getDouyinRoomDataFromPage(webRID string) (*Room, error) {
	rawURL := BaseLiveHost + "/" + webRID
	trace := beginDouyinAPIRequest(douyinRequestTypeRoomPage, "GET", rawURL)
	release, err := acquireDouyinRequestSlotWithWait(context.Background(), trace)
	if err != nil {
		return nil, fmt.Errorf("获取抖音直播页失败: %w", err)
	}
	defer release()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	opts := douyinWebPageRequestOptions(trace)
	reqErr := requests.GetWithHeader(rawURL, nil, &resp, &respHeaders, opts...)
	trace.finish(reqErr)
	if reqErr != nil {
		return nil, fmt.Errorf("获取抖音直播页失败: %w", reqErr)
	}
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		return nil, fmt.Errorf("解压缩抖音直播页失败: %w", err)
	}
	room, err := parseDouyinLivePageRoom(string(body))
	if err != nil {
		return nil, err
	}
	return room, nil
}

// parseDouyinLivePageRoom 从直播页 HTML 首屏解析房间数据：
// 先按 self.__pace_f.push(...) 水合块扫描，再按 "roomStore" 锚点兜底。
func parseDouyinLivePageRoom(body string) (*Room, error) {
	room, err := parseDouyinPaceStateRoom(body)
	if err == nil && room != nil {
		return room.toProtoRoom(), nil
	}
	idx := strings.Index(body, "\"roomStore\"")
	if idx < 0 {
		return nil, errors.New("抖音直播页未包含可用的 roomStore.roomInfo.room 数据")
	}
	container := firstJSONContainerIndex(body[idx:], '{')
	if container < 0 {
		return nil, errors.New("直播页首屏脚本缺少结束标记")
	}
	segment := body[idx+container:]
	end := douyinJSONContainerEnd(segment, '{', '}')
	if end < 0 {
		return nil, errors.New("直播页首屏脚本缺少结束标记")
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(segment[:end+1]), &raw); err != nil {
		return nil, fmt.Errorf("解析抖音直播页首屏数据失败: %w", err)
	}
	room = roomFromDouyinRoomStore(raw)
	if room == nil {
		return nil, errors.New("首屏状态中没有可用的直播间详情")
	}
	return room.toProtoRoom(), nil
}

// parseDouyinPaceStateRoom 扫描 self.__pace_f.push([...]) 水合块寻找房间数据。
func parseDouyinPaceStateRoom(body string) (*douyinLivePageRoom, error) {
	marker := "self.__pace_f.push("
	offset := 0
	for {
		idx := strings.Index(body[offset:], marker)
		if idx < 0 {
			// 成品 parseDouyinPaceStateRoom @0xd87040 唯一中文串：
			// "首屏状态中没有可用的直播间详情"(@0x10d8a9f)。
			return nil, errors.New("首屏状态中没有可用的直播间详情")
		}
		start := offset + idx + len(marker)
		jsonStart := firstJSONContainerIndex(body[start:], '[')
		if jsonStart < 0 {
			return nil, errors.New("直播页首屏脚本缺少结束标记")
		}
		segment := body[start+jsonStart:]
		end := douyinJSONContainerEnd(segment, '[', ']')
		if end < 0 {
			return nil, errors.New("直播页首屏脚本缺少结束标记")
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(segment[:end+1]), &raw); err == nil {
			if roomJSON := findDouyinLivePageRoom(raw); roomJSON != nil {
				return roomJSON, nil
			}
		}
		offset = start + end
	}
}

// firstJSONContainerIndex 返回第一个 JSON 容器起始下标（open 为 '{' 或 '['）。
func firstJSONContainerIndex(s string, open byte) int {
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			return i
		}
	}
	return -1
}

// douyinJSONContainerEnd 返回与首字符配对的容器结束下标。
func douyinJSONContainerEnd(s string, open byte, close byte) int {
	if len(s) == 0 || s[0] != open {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// findDouyinLivePageRoom 在解析后的 JSON 树中定位 roomStore.roomInfo.room。
// 成品 @getRoomData.go:301 直接返回 *douyinLivePageRoom（callees：roomFromDouyinRoomStore
// 与自身递归），不再单独暴露 map。
func findDouyinLivePageRoom(root map[string]interface{}) *douyinLivePageRoom {
	if root == nil {
		return nil
	}
	if room := roomFromDouyinRoomStore(root); room != nil {
		return room
	}
	for _, value := range root {
		switch node := value.(type) {
		case map[string]interface{}:
			if room := findDouyinLivePageRoom(node); room != nil {
				return room
			}
		case []interface{}:
			for _, item := range node {
				if obj, ok := item.(map[string]interface{}); ok {
					if room := findDouyinLivePageRoom(obj); room != nil {
						return room
					}
				}
			}
		}
	}
	return nil
}

// roomFromDouyinRoomStore 从 roomStore 数据中取出 roomInfo.room 并转为 douyinLivePageRoom。
// 成品 @getRoomData.go:324（352B）callees：mapaccess1_faststr / mapaccess2_faststr /
// encoding/json.Marshal / runtime.newobject / encoding/json.Unmarshal，即取出子 map 后
// json.Marshal→json.Unmarshal 到 *douyinLivePageRoom，失败返回 nil。
func roomFromDouyinRoomStore(root map[string]interface{}) *douyinLivePageRoom {
	if root == nil {
		return nil
	}
	raw, _ := valueAtDouyinPath(root, "roomStore", "roomInfo", "room").(map[string]interface{})
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	room := new(douyinLivePageRoom)
	if err := json.Unmarshal(data, room); err != nil {
		return nil
	}
	return room
}

// douyinRoomJSONBOM 是需剥离的 UTF-8 BOM（成品 @0xd87eca 写入栈字节 ef bb bf）。
var douyinRoomJSONBOM = []byte{0xEF, 0xBB, 0xBF}

// douyinRoomJSONBodyReplacer 把 CR/LF/TAB 替换为空格（成品 @0xd87ffd 用 6 元素 []string
// 构造 strings.Replacer：pair(\r," ")、pair(\n," ")、pair(\t," ")，三处 new 均为 0x11240b8=' '），
// 仅用于把非 JSON 响应体清洗成可安全写进错误文案的 prefix。
var douyinRoomJSONBodyReplacer = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")

// douyinRoomJSONPrefixRunes 是错误文案里 prefix 的最大 rune 数（成品 @0xd88105 cmp $0x50）。
const douyinRoomJSONPrefixRunes = 80

// normalizeDouyinRoomJSON 校验/规整抖音房间接口的响应体（成品 @0xd87ea0，getRoomData.go:374，1376B）。
//
// 反汇编逐段取证：
//   - @0xd87ec5 bytes.TrimSpace(raw)；
//   - @0xd87ed6 若 len>=3 且前三字节 memequal 栈上 {ef,bb,bf}，则剥离 BOM
//     （@0xd87f09 按 cap-3 重切片，@0xd87f54 再 bytes.TrimSpace）；
//   - @0xd87f60 len==0 → fmt.Errorf("抖音直播间接口返回空响应
//     (content_type=%q, content_length=%q, content_encoding=%q)", contentType,
//     contentLength, contentEncoding)（格式串 @0x10ef0f0,len0x5e，@0xd882bd 装箱 3 个栈参）；
//   - @0xd87f6c/@0xd87f75 首字节为 '{' 或 '[' → 原样返回 (b,nil)（@0xd882b0）；
//   - 否则 @0xd87f86 slicebytetostring → @0xd87f97 strings.ToValidUTF8(s,"?")
//     （替换串 0x1121778='?'）→ @0xd880e0 Replacer.Replace 清洗 CR/LF/TAB；
//   - @0xd88100 []rune 截断到 80 runes（>80 才 slicerunetostring，<=80 直接复用原串）；
//   - @0xd881c0 strconv.appendQuotedWith(dst, s, '"', false, false)（即 strconv.Quote）；
//   - @0xd88267 fmt.Errorf("抖音直播间接口返回非JSON内容 (content_type=%q, prefix=%s)",
//     contentType, prefix)（格式串 @0x10e9807,len0x45）。
func normalizeDouyinRoomJSON(raw []byte, contentType string, contentLength string, contentEncoding string) ([]byte, error) {
	b := bytes.TrimSpace(raw)
	b = bytes.TrimPrefix(b, douyinRoomJSONBOM)
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, fmt.Errorf("抖音直播间接口返回空响应 (content_type=%q, content_length=%q, content_encoding=%q)",
			contentType, contentLength, contentEncoding)
	}
	if b[0] == '{' || b[0] == '[' {
		return b, nil
	}
	sanitized := douyinRoomJSONBodyReplacer.Replace(strings.ToValidUTF8(string(b), "?"))
	runes := []rune(sanitized)
	if len(runes) > douyinRoomJSONPrefixRunes {
		runes = runes[:douyinRoomJSONPrefixRunes]
	}
	return nil, fmt.Errorf("抖音直播间接口返回非JSON内容 (content_type=%q, prefix=%s)",
		contentType, strconv.Quote(string(runes)))
}

// firstDouyinRoomCoverURL 取房间封面第一张地址（入参为 proto Room）。
func firstDouyinRoomCoverURL(room *Room) string {
	if room == nil || room.GetCover() == nil || len(room.GetCover().GetUrlList()) == 0 {
		return ""
	}
	return room.GetCover().GetUrlList()[0]
}
