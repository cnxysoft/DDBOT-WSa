package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	jsoniter "github.com/json-iterator/go"
)

// 批量直播状态：
// get_status_info_by_uids 一次拉取多个 UID 的房间/开播状态，
// 首次启动用它校准基线，WebSocket 风控或无房间 UID 复查时兜底轮询。
const PathGetStatusInfoByUIDs = "https://api.live.bilibili.com/room/v1/Room/get_status_info_by_uids"

// batchLiveStatusResponse 是批量直播状态响应；data 以 UID 字符串为键，
// 故先按 RawMessage 接收再二次解析。
type batchLiveStatusResponse struct {
	Code    int                 `json:"code"`
	Message string              `json:"message"`
	Msg     string              `json:"msg"`
	Data    jsoniter.RawMessage `json:"data"`
}

// BatchLiveRoomInfo 是批量直播状态接口中单个房间的信息（成品类型，取代重建
// 自创的 batchLiveStatusResult）。
//
// 成品证据（recovered/types/types-closure-x64.tsv:10918）：
//
//	struct 152 bilibili.BatchLiveRoomInfo struct {
//	    Title string "json:"title""; RoomID int64 "json:"room_id"";
//	    UID int64 "json:"uid""; LiveTime int64 "json:"live_time"";
//	    LiveStatus int32 "json:"live_status""; AreaV2ID int32 "json:"area_v2_id"";
//	    AreaV2Name string "json:"area_v2_name""; AreaV2ParentID int32 "json:"area_v2_parent_id"";
//	    AreaV2ParentName string "json:"area_v2_parent_name""; Uname string "json:"uname"";
//	    Face string "json:"face""; CoverFromUser string "json:"cover_from_user"";
//	    Keyframe string "json:"keyframe"" }
//
// 字段偏移经成品反汇编交叉验证：RoomID@+0x10、UID@+0x18 由
// (*Concern).userInfoFromBatch(@0xcea900) 读取；Title@+0x0、LiveTime@+0x20、
// LiveStatus@+0x28、CoverFromUser@+0x78、Keyframe@+0x88 由
// (*Concern).liveInfoFromBatch(@0xceac80) 读取（封面取 CoverFromUser，空则回落 Keyframe）。
type BatchLiveRoomInfo struct {
	Title            string `json:"title"`
	RoomID           int64  `json:"room_id"`
	UID              int64  `json:"uid"`
	LiveTime         int64  `json:"live_time"`
	LiveStatus       int32  `json:"live_status"`
	AreaV2ID         int32  `json:"area_v2_id"`
	AreaV2Name       string `json:"area_v2_name"`
	AreaV2ParentID   int32  `json:"area_v2_parent_id"`
	AreaV2ParentName string `json:"area_v2_parent_name"`
	Uname            string `json:"uname"`
	Face             string `json:"face"`
	CoverFromUser    string `json:"cover_from_user"`
	Keyframe         string `json:"keyframe"`
}

// GetLiveStatusByUIDs 批量获取直播状态，返回按 UID 索引的结果。
// 成品 GetLiveStatusByUIDs(RoomStatusBatch.go:44, @0xce93c0, 160B) 反汇编（2026-10-06 复核）：
//
//	入口保存 rax:rbx = ctx（context.Context 两字）、rcx:rdi:rsi = uids（[]int64 三字）；
//	+0x27 call cloneDefaultHTTPTransport(@0xd333a0) → 返回值置 rcx（*http.Transport）；
//	+0x34 rdi:rsi = PathGetStatusInfoByUIDs（0x10e841e, 0x42=66B）；r8:r9:r10 = 原 uids；
//	call getLiveStatusByUIDsFrom(@0xce9460)，即 (ctx, *http.Transport, url string, uids []int64)。
//
// 重建的 requests.PostJson 无 ctx 形参，故这里只对齐入参 ABI。
// TODO(lite): 待 requests 支持 ctx 后把 ctx 透传到请求层（当前行为与旧版一致）。
func GetLiveStatusByUIDs(ctx context.Context, uids []int64) (map[int64]*BatchLiveRoomInfo, error) {
	_ = ctx
	return getLiveStatusByUIDsFrom(uids)
}

// getLiveStatusByUIDsFrom 去重后调用批量接口并解析结果。
// 成品 getLiveStatusByUIDsFrom(@0xce9460, RoomStatusBatch.go:48, 1216B) 先自行
// 去重：makemap_small + mapaccess2_fast64/mapassign_fast64 建 uid 集合，
// makeslice + growslice 收集去重后的 UID，再交给 requestLiveStatusBatch；
// 请求返回后 mapIter + mapassign_fast64 建 map[int64]*BatchLiveRoomInfo。
func getLiveStatusByUIDsFrom(uids []int64) (map[int64]*BatchLiveRoomInfo, error) {
	deduped := uniquePositiveUIDs(uids)
	if len(deduped) == 0 {
		return make(map[int64]*BatchLiveRoomInfo), nil
	}
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", localutils.FuncName()).Tracef("cost %v", time.Since(st))
	}()
	return requestLiveStatusBatch(deduped)
}

// requestLiveStatusBatch 请求批量直播状态接口并解析为按 UID 索引的结果。
// 成品 requestLiveStatusBatch(@0xce9920, RoomStatusBatch.go:76, 4000B) 以 data 的
// 字符串 UID 为键解析为 map[string]*BatchLiveRoomInfo（类型表 :6690），再
// strconv.ParseInt + mapassign_fast64 折成 map[int64]*BatchLiveRoomInfo（:6693）。
func requestLiveStatusBatch(uids []int64) (map[int64]*BatchLiveRoomInfo, error) {
	if len(uids) == 0 {
		return nil, errors.New("批量直播状态转换结果为空")
	}
	var resp batchLiveStatusResponse
	statusCode := 0
	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(15 * time.Second),
		requests.RetryOption(3),
		AddUAOption(),
		AddReferOption("https://live.bilibili.com/"),
		requests.HttpCodeOption(&statusCode),
		delete412ProxyOption,
	}
	if verify := getVerify(); verify != nil {
		opts = append(opts, verify.VerifyOpts...)
	}
	// 成品 requestLiveStatusBatch 的 4 条格式串（funcstr @0xce9920）：
	//   非 200  → "批量直播状态API HTTP状态异常: %s"     @0x10d53cc,len 0x2a
	//   其余请求失败 → "解析批量直播状态响应失败: %w"      @0x10d2d03,len 0x28
	//   业务码  → "批量直播状态API失败: code=%d message=%s" @0x10dab39,len 0x2f
	//   data 解析 → "解析批量直播状态data失败: %w"          @0x10d00e7,len 0x26
	err := requests.PostJson(PathGetStatusInfoByUIDs, gout.H{
		"uids": uniquePositiveUIDs(uids),
	}, &resp, opts...)
	if err != nil {
		if statusCode != 0 && statusCode != http.StatusOK {
			return nil, fmt.Errorf("批量直播状态API HTTP状态异常: %s", strconv.Itoa(statusCode))
		}
		return nil, fmt.Errorf("解析批量直播状态响应失败: %w", err)
	}
	if statusCode != 0 && statusCode != http.StatusOK {
		return nil, fmt.Errorf("批量直播状态API HTTP状态异常: %s", strconv.Itoa(statusCode))
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("批量直播状态API失败: code=%d message=%s", resp.Code, resp.Message)
	}
	if len(resp.Data) == 0 {
		return nil, errors.New("批量直播状态转换结果为空")
	}
	var dataMap map[string]*BatchLiveRoomInfo
	if err := json.Unmarshal(resp.Data, &dataMap); err != nil {
		return nil, fmt.Errorf("解析批量直播状态data失败: %w", err)
	}
	result := make(map[int64]*BatchLiveRoomInfo, len(dataMap))
	for uidStr, item := range dataMap {
		uid, err := strconv.ParseInt(strings.TrimSpace(uidStr), 10, 64)
		if err != nil || item == nil {
			continue
		}
		if item.UID == 0 {
			item.UID = uid
		}
		result[uid] = item
	}
	if len(result) == 0 {
		return nil, errors.New("批量直播状态转换结果为空")
	}
	return result, nil
}

// userInfoFromBatch 从批量结果构造用户信息。
// 成品 (*Concern).userInfoFromBatch(@0xcea900, RoomStatusBatch.go:160, 896B) 反汇编：
// 签名 = (*Concern, *BatchLiveRoomInfo) -> (*UserInfo, error)，error 在 rax=itab 0x112be60、
// rbx=data；结果为空、UID<=0 或 RoomID<=0 时 return nil, errors.New(
// "批量直播状态中没有有效直播间" @0x10d53f6,len 0x2a=42)（三处跳同一 @0xceac13）。
// 成品该函数没有独立 uid 形参：入口 +0x12 先判 result==nil，+0x1b 直接取
// result.UID(+0x18) 判 <=0，+0x29 判 result.RoomID(+0x10)<=0，三处都 return nil,
// errors.New（同一 @0xceac13）。末尾 @+0x2ec 调 (*StateManager).AddUserInfo(@0xd49480)：
// 成功（@0xceac04）返回 ui,nil；error 非空（@0xceabf1 test 未命中）则 return nil, err。
// 其余：先按 result.UID 查 StateManager.GetUserInfo 取旧值回退，再以
// TrimSpace(Uname@+0x58)/TrimSpace(Face@+0x68) 覆盖；URL 由
// "https://live.bilibili.com/"（@0x10bd9c2,len 0x1a）+ FormatInt(RoomID) 拼出。
func (c *Concern) userInfoFromBatch(result *BatchLiveRoomInfo) (*UserInfo, error) {
	if result == nil {
		return nil, errors.New("批量直播状态中没有有效直播间")
	}
	uid := result.UID
	if uid <= 0 || result.RoomID <= 0 {
		return nil, errors.New("批量直播状态中没有有效直播间")
	}
	existing, _ := c.StateManager.GetUserInfo(uid)
	name := strings.TrimSpace(result.Uname)
	if name == "" && existing != nil {
		name = existing.Name
	}
	face := strings.TrimSpace(result.Face)
	if face == "" && existing != nil {
		face = existing.Face
	}
	url := "https://live.bilibili.com/" + strconv.FormatInt(result.RoomID, 10)
	userInfo := NewUserInfo(uid, result.RoomID, name, url, face)
	if err := c.StateManager.AddUserInfo(userInfo); err != nil {
		return nil, err
	}
	return userInfo, nil
}

// liveInfoFromBatch 从批量结果构造直播信息。
// 成品 (*Concern).liveInfoFromBatch(@0xceac80, RoomStatusBatch.go:186) 反汇编：
// 入口只保存 result(rbx) 后 +0x17 直接调 userInfoFromBatch(rax, rbx)——无独立 uid 形参；
// userInfo 为 nil 时返回 nil；封面取 CoverFromUser@+0x78，
// 为空则回落 Keyframe@+0x88；标题 Title@+0x0、LiveTime@+0x20、LiveStatus@+0x28。
func (c *Concern) liveInfoFromBatch(result *BatchLiveRoomInfo) *LiveInfo {
	if result == nil {
		return nil
	}
	userInfo, err := c.userInfoFromBatch(result)
	if err != nil || userInfo == nil {
		return nil
	}
	cover := result.CoverFromUser
	if cover == "" {
		cover = result.Keyframe
	}
	return NewLiveInfo(userInfo, result.Title, cover, LiveStatus(result.LiveStatus), result.LiveTime)
}

// uniquePositiveUIDs 去重并过滤非法 UID，保持原有顺序。
func uniquePositiveUIDs(uids []int64) []int64 {
	var result []int64
	seen := make(map[int64]struct{}, len(uids))
	for _, uid := range uids {
		if uid <= 0 {
			continue
		}
		if _, exist := seen[uid]; exist {
			continue
		}
		seen[uid] = struct{}{}
		result = append(result, uid)
	}
	return result
}
