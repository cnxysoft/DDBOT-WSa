package huya

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

// huyaProfileRoomApi 虎牙移动端房间信息接口，返回结构化 JSON（约 2.5KB）。
const huyaProfileRoomApi = "https://mp.huya.com/cache.php?m=Live&do=profileRoom&roomid="

// 接口返回的直播状态取值
const (
	huyaLiveStatusOn     = "ON"     // 直播中
	huyaLiveStatusOff    = "OFF"    // 未开播
	huyaLiveStatusReplay = "REPLAY" // 回放中
)

type huyaProfileRoomResp struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Data    struct {
		LiveStatus  string `json:"liveStatus"`
		ProfileInfo struct {
			Nick      string `json:"nick"`
			Avatar180 string `json:"avatar180"`
		} `json:"profileInfo"`
		LiveData struct {
			RoomName     string `json:"roomName"`
			Introduction string `json:"introduction"`
		} `json:"liveData"`
	} `json:"data"`
}

func huyaRequestOpts() []requests.Option {
	return []requests.Option{
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.RetryOption(3),
		requests.TimeoutOption(time.Second * 10),
	}
}

func huyaFixURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

// RoomPage 查询房间信息。
// 优先走移动端接口（结构化、体积小）；接口不可用时退回直播页解析。
func RoomPage(roomId string) (*LiveInfo, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()

	ri, err := roomPageByApi(roomId)
	if err == nil {
		return ri, nil
	}
	logger.WithField("RoomId", roomId).Warnf("huya profile api failed %v, fallback to room page", err)
	return roomPageByHtml(roomId)
}

// roomPageByApi 通过移动端接口获取房间信息
func roomPageByApi(roomId string) (*LiveInfo, error) {
	var body = new(bytes.Buffer)
	if err := requests.Get(huyaProfileRoomApi+roomId, nil, body, huyaRequestOpts()...); err != nil {
		return nil, err
	}
	return parseProfileRoom(roomId, body.Bytes())
}

// parseProfileRoom 解析移动端接口返回。
// 房间不存在时接口返回的 data 是数组（[]）而非对象，与正常返回结构不同，
// 所以先只解出状态，确认正常后再解析 data。
func parseProfileRoom(roomId string, bin []byte) (*LiveInfo, error) {
	var head struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(bin, &head); err != nil {
		return nil, err
	}
	if head.Status != 200 {
		return nil, fmt.Errorf("profileRoom status=%v message=%v", head.Status, head.Message)
	}
	var resp huyaProfileRoomResp
	if err := json.Unmarshal(bin, &resp); err != nil {
		return nil, err
	}

	ri := &LiveInfo{
		RoomId:   roomId,
		RoomUrl:  HuyaPath(roomId),
		Name:     resp.Data.ProfileInfo.Nick,
		Avatar:   huyaFixURL(resp.Data.ProfileInfo.Avatar180),
		IsLiving: resp.Data.LiveStatus == huyaLiveStatusOn,
	}
	// 未开播/回放时 roomName 为空，退回房间简介
	ri.RoomName = resp.Data.LiveData.RoomName
	if ri.RoomName == "" {
		ri.RoomName = resp.Data.LiveData.Introduction
	}
	return ri, nil
}

// roomPageByHtml 退回直播页解析
func roomPageByHtml(roomId string) (*LiveInfo, error) {
	url := HuyaPath(roomId)
	var body = new(bytes.Buffer)
	if err := requests.Get(url, nil, body, huyaRequestOpts()...); err != nil {
		return nil, err
	}
	return parseRoomPage(roomId, body.Bytes())
}

// parseRoomPage 解析直播页
func parseRoomPage(roomId string, page []byte) (*LiveInfo, error) {
	log := logger.WithField("RoomId", roomId)
	raw := string(page)
	if strings.Contains(raw, "找不到这个主播") {
		return nil, ErrRoomNotExist
	}
	if strings.Contains(raw, "涉嫌违规") {
		return nil, ErrRoomBanned
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return nil, err
	}

	living, ok := huyaLivingFromClass(doc)
	if !ok {
		return nil, errors.New("can not determine live status")
	}
	ri := &LiveInfo{
		RoomId:   roomId,
		RoomUrl:  HuyaPath(roomId),
		IsLiving: living,
	}

	// 昵称
	if nameH3 := doc.Find(".host-name"); nameH3.Size() == 1 {
		if name, found := nameH3.Attr("title"); found {
			ri.Name = name
		}
	} else {
		log.Errorf(".host-name not found")
	}

	// 头像：旧选择器 #avatar-img 已不在页面中，现为 .host-pic img
	ri.Avatar = huyaFixURL(huyaAvatarFromHtml(doc))

	// 房间标题
	if titleH2 := doc.Find(".host-title"); titleH2.Size() == 1 {
		if title, found := titleH2.Attr("title"); found {
			ri.RoomName = title
		}
	} else {
		log.Errorf(".host-title not found")
	}

	return ri, nil
}

// huyaLivingFromClass 判断是否直播中。
// 虎牙把状态写在 body 的 liveStatus-<state> 上（页面里是小写 on / off / replay）。
// 页面同时渲染 host-spectator（热度）与 host-prevStartTime 时，不能再靠“唯一 span”判定；
// 老页面没有该 class 时才退回旧判定。
func huyaLivingFromClass(doc *goquery.Document) (living bool, ok bool) {
	class, _ := doc.Find("body").Attr("class")
	class = strings.ToLower(class)
	switch {
	case strings.Contains(class, "livestatus-on"):
		return true, true
	case strings.Contains(class, "livestatus-off"), strings.Contains(class, "livestatus-replay"):
		return false, true
	}
	prev := doc.Find("span.host-prevStartTime").Size()
	spectator := doc.Find("span.host-spectator").Size()
	if prev+spectator == 1 {
		return spectator == 1, true
	}
	return false, false
}

// huyaAvatarFromHtml 依次尝试已知的头像选择器
func huyaAvatarFromHtml(doc *goquery.Document) string {
	for _, sel := range []string{".host-pic img", "#avatar-img", ".room-hd-l img"} {
		node := doc.Find(sel).First()
		if node.Size() == 0 {
			continue
		}
		if src, found := node.Attr("src"); found && src != "" {
			return src
		}
	}
	return ""
}
