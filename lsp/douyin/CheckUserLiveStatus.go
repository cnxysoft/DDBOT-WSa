package douyin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

// douyinLiveSnapshot 是从用户资料接口取得的直播快照：
// 直播状态改走带签名的用户资料接口（旧 check_user_live_status 端点已废弃）。
type douyinLiveSnapshot struct {
	Living   bool
	UserID   string
	SecUID   string
	Nickname string
	Avatar   string
	RoomID   string
	WebRID   string
	Title    string
	Cover    string
}

// douyinWebProfileResponse 是网页版用户资料接口响应。
type douyinWebProfileResponse struct {
	StatusCode int             `json:"status_code"`
	StatusMsg  json.RawMessage `json:"status_msg"`
	User       *struct {
		UID          string          `json:"uid"`
		SecUID       string          `json:"sec_uid"`
		Nickname     string          `json:"nickname"`
		UniqueID     string          `json:"unique_id"`
		Signature    string          `json:"signature"`
		LiveStatus   int             `json:"live_status"`
		RoomIDStr    string          `json:"room_id_str"`
		RoomData     json.RawMessage `json:"room_data"`
		AvatarMedium *douyinWebImage `json:"avatar_medium"`
		AvatarLarger *douyinWebImage `json:"avatar_larger"`
		AvatarThumb  *douyinWebImage `json:"avatar_thumb"`
	} `json:"user"`
}

// live_status 取值（沿用 enter 接口语义：2=直播中，0=未开播）。
const (
	douyinLiveStatusOffline = 0
	douyinLiveStatusLiving  = 2
)

// FreshLiveSnapshot 通过带 A-Bogus 的抖音用户资料接口读取直播快照。
// 成品 @0xd62b60（CheckUserLiveStatus.go:61，1760B）入口先 `strings.TrimSpace(secUID)`
// （callee +0x51），为空则返回 errors.New("sec_uid 不能为空")
// （分支 @0xd6317e，文案 rodata@0x10b516f,len20）——重建版此前缺这两步。
func FreshLiveSnapshot(secUID string) (*douyinLiveSnapshot, error) {
	secUID = strings.TrimSpace(secUID)
	if secUID == "" {
		return nil, errors.New("sec_uid 不能为空")
	}
	body, err := requestDouyinSignedUserProfileBody(secUID)
	if err != nil {
		return nil, err
	}
	return parseDouyinLiveProfile(body, secUID)
}

// requestDouyinSignedUserProfileBody 请求带签名用户资料接口并返回响应体。
// 认证失败计入 live scope 的 Cookie 失败计数。
func requestDouyinSignedUserProfileBody(secUID string) ([]byte, error) {
	rawURL := buildSignedDouyinUserProfileURL(secUID)
	trace := beginDouyinAPIRequest(douyinRequestTypeSignedUserProfile, "GET", rawURL)
	release, err := acquireDouyinRequestSlotWithWait(context.Background(), trace)
	if err != nil {
		return nil, fmt.Errorf("获取抖音用户资料失败: %w", err)
	}
	defer release()
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	statusCode := 0
	opts := douyinWebAPIRequestOptions(trace)
	opts = append(opts,
		requests.HeaderOption("Referer", BaseHost+"/user/"+secUID),
		requests.HttpCodeOption(&statusCode),
	)
	reqErr := requests.GetWithHeader(rawURL, nil, &resp, &respHeaders, opts...)
	trace.finish(reqErr)
	if reqErr != nil {
		// 成品 FreshLiveSnapshot callsite：isDouyinAuthFailure @+0x340（输入为请求 error，
		// 不是 statusCode），命中后 @+0x378 用 `err.Error()` 文本作为 reason 调
		// markDouyinCookieUnhealthyFor(live,...)。
		if isDouyinAuthFailure(reqErr) {
			markDouyinCookieUnhealthyFor(douyinFailureScopeLive, reqErr.Error())
		}
		return nil, fmt.Errorf("获取抖音用户资料失败: %w", reqErr)
	}
	body, err := utils.HtmlDecoder(respHeaders.ContentEncoding, resp)
	if err != nil {
		return nil, fmt.Errorf("解压缩抖音用户直播资料失败: %w", err)
	}
	markDouyinCookieHealthyFor(douyinFailureScopeLive)
	return body, nil
}

// parseDouyinLiveProfile 解析用户资料响应为直播快照。
func parseDouyinLiveProfile(body []byte, expectedSecUID string) (*douyinLiveSnapshot, error) {
	var profile douyinWebProfileResponse
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("解析抖音用户直播资料失败: %w", err)
	}
	if profile.StatusCode != 0 {
		logger.WithField("status_code", profile.StatusCode).
			WithField("status_msg", douyinStatusMessage(profile.StatusMsg)).
			Warnf("抖音用户资料接口返回 status_code=%d, status_msg=%s", profile.StatusCode, douyinStatusMessage(profile.StatusMsg))
		return nil, fmt.Errorf("抖音用户资料返回 status_code=%d", profile.StatusCode)
	}
	if profile.User == nil {
		return nil, fmt.Errorf("抖音用户资料接口未返回 user，不能据此判定下播")
	}
	if expectedSecUID != "" && profile.User.SecUID != expectedSecUID {
		return nil, fmt.Errorf("抖音用户资料接口返回了其他用户: expected=%s actual=%s", expectedSecUID, profile.User.SecUID)
	}
	snapshot := &douyinLiveSnapshot{
		Living:   profile.User.LiveStatus == douyinLiveStatusLiving,
		UserID:   profile.User.UID,
		SecUID:   profile.User.SecUID,
		Nickname: profile.User.Nickname,
	}
	if profile.User.AvatarLarger != nil && len(profile.User.AvatarLarger.URLList) > 0 {
		snapshot.Avatar = profile.User.AvatarLarger.URLList[0]
	} else if profile.User.AvatarMedium != nil && len(profile.User.AvatarMedium.URLList) > 0 {
		snapshot.Avatar = profile.User.AvatarMedium.URLList[0]
	}
	// 房间号/标题/封面来自 user.room_data（json:"room_data"），开播后可能才补全。
	// 成品 parseDouyinProfileRoom 返回 (*douyinRoomProfile, bool)，web_rid 取自
	// room_data.owner.web_rid（A18：douyinRoomProfile.Owner）。
	if room, ok := parseDouyinProfileRoom(profile.User.RoomData); ok {
		snapshot.RoomID = room.IDStr
		snapshot.WebRID = room.Owner.WebRID
		snapshot.Title = room.Title
		if room.Cover != nil && len(room.Cover.URLList) > 0 {
			snapshot.Cover = room.Cover.URLList[0]
		}
	}
	if profile.User.LiveStatus != douyinLiveStatusOffline && profile.User.LiveStatus != douyinLiveStatusLiving {
		logger.WithField("live_status", profile.User.LiveStatus).
			Warnf("抖音用户资料接口返回未知 live_status=%d", profile.User.LiveStatus)
	}
	if snapshot.Living && snapshot.WebRID == "" {
		logger.WithField("sec_uid", snapshot.SecUID).
			Info("抖音用户当前未返回直播间号，仍保留直播订阅；开播轮询取得 web_rid 后会自动补全")
	}
	return snapshot, nil
}

// parseDouyinProfileRoom 从 user.room_data 解析房间资料。
//
// 成品 @0xd639e0（CheckUserLiveStatus.go:145，384B）签名 (*douyinRoomProfile, bool)：
//   - bytes.TrimSpace 后为空或字面量 "null"（len==4 逐字节 memequal）→ (nil,false)；
//   - 再 TrimSpace，若首字节为 '"'（room_data 被双重编码成 JSON 字符串）则先
//     json.Unmarshal 成 string、strings.TrimSpace，空串 → (nil,false)；
//   - 最后 json.Unmarshal 到 *douyinRoomProfile（指针类型描述符 @0xebc920 →
//     struct @0xfebdc0 size=56），成功返回 (p,true)，失败 (nil,false)。
func parseDouyinProfileRoom(roomData []byte) (*douyinRoomProfile, bool) {
	trimmed := bytes.TrimSpace(roomData)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	trimmed = bytes.TrimSpace(roomData)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err != nil {
			return nil, false
		}
		inner = strings.TrimSpace(inner)
		if inner == "" {
			return nil, false
		}
		trimmed = []byte(inner)
	}
	profile := new(douyinRoomProfile)
	if err := json.Unmarshal(trimmed, profile); err != nil {
		return nil, false
	}
	return profile, true
}

// douyinStatusMessage 安全取 JSON status_msg（成品 0xd63b60，CheckUserLiveStatus.go:173）：
// TrimSpace 后为空或 "null" 时返回空串；否则 json.Unmarshal 成字符串，
// 失败时退回原始字节文本（日志用）。
func douyinStatusMessage(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(raw)
	}
	return s
}
