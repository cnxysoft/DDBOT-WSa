package bilibili

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

// 直播间号解析：
// 先用无 Cookie 的 Master/info 取主播信息，失败时回退公开名片 card 接口，
// 两者都失败时给出分类状态，供订阅与批量复查选择不同节奏。
const (
	PathLiveMasterInfo = "https://api.live.bilibili.com/live_user/v1/Master/info"
	PathPublicUserCard = "https://api.bilibili.com/x/web-interface/card"
)

// liveRoomResolutionStatus 是解析结果的分类状态码。
type liveRoomResolutionStatus string

// 解析状态码（成品 resolveLiveRoomFrom @0xce7b40 反汇编实证：Status 只有这 4 种）。
// reconcile(@0xd24900) 只用 status 分派：room_found → 建连；account_exists_without_room →
// 记 noRoom/roomRetry；account_deleted → 清 noRoom/roomRetry；retry → 下轮再说。
const (
	liveRoomStatusRoomFound                liveRoomResolutionStatus = "room_found"                  // @0x10a7511,10B，返回点 0xce7e73
	liveRoomStatusRetry                    liveRoomResolutionStatus = "retry"                       // @0x109a820,5B，返回点 0xce818d/0xce8412
	liveRoomStatusAccountDeleted           liveRoomResolutionStatus = "account_deleted"             // @0x10adedd,15B，返回点 0xce8479
	liveRoomStatusAccountExistsWithoutRoom liveRoomResolutionStatus = "account_exists_without_room" // @0x10bf273,27B，返回点 0xce82a4
)

// 解析原因串（成品 Reason 字段原文；reconcile 只用 memequal 比较 Status，不比较 Reason）。
const (
	liveRoomReasonLiveMasterRoomFound          = "live_master_room_found"                // @0x10b7d4c,22B
	liveRoomReasonLiveMasterAPIFailed          = "live_master_api_failed"                // @0x10b7d62,22B
	liveRoomReasonLiveMasterRequestFailed      = "live_master_request_failed"            // @0x10bd9dc,26B
	liveRoomReasonPublicCardRequestFailed      = "public_card_request_failed"            // @0x10bd9f6,26B
	liveRoomReasonPublicCardInconclusive       = "public_card_inconclusive"              // @0x10bacfd,24B
	liveRoomReasonPublicCardExplicitNotFound   = "public_card_explicit_not_found"        // @0x10c3dc1,30B
	liveRoomReasonAccountAndRoomUnknown        = "account_and_room_status_unknown"       // @0x10c53f2,31B
	liveRoomReasonAccountExistsButMasterFailed = "account_exists_but_live_master_failed" // @0x10cea51,37B
	liveRoomReasonAccountExistsWithoutRoom     = "account_exists_without_room"           // @0x10bf273,27B
)

// liveRoomResolution 是直播间号解析结果。
type liveRoomResolution struct {
	Status liveRoomResolutionStatus
	User   *UserInfo
	Reason string
}

// liveMasterInfoResponse 是 Master/info 响应。
type liveMasterInfoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Msg     string `json:"msg"`
	Data    struct {
		Info struct {
			UID   int64  `json:"uid"`
			Uname string `json:"uname"`
			Face  string `json:"face"`
		} `json:"info"`
		RoomID int64 `json:"room_id"`
	} `json:"data"`
}

// publicUserCardResponse 是公开名片 card 响应。
type publicUserCardResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *struct {
		Card struct {
			Mid  string `json:"mid"`
			Name string `json:"name"`
			Face string `json:"face"`
		} `json:"card"`
	} `json:"data"`
}

// resolveLiveRoom 解析一个直播间号，内部按双源策略执行。
func resolveLiveRoom(roomID int64) *liveRoomResolution {
	return resolveLiveRoomFrom(roomID)
}

// resolveLiveRoomFrom 从 UID 解析主播：
// Master/info 优先，账号注销等显式消息单独分类；失败回退公开名片。
// Status/Reason 归属按成品 resolveLiveRoomFrom(@0xce7b40) 反汇编校准：
//   - Master/info 有 UID 且 room_id>0 → room_found（0xce7d3b 判 room 字段，0xce7e73 返回）；
//   - Master/info 显式注销文案，或 card 显式不存在 → account_deleted（0xce8465）；
//   - card 命中拿到 UID 但房间号未知 → account_exists_without_room（0xce82a4）；
//   - 其余请求/业务失败 → retry（0xce818d/0xce8412），细节串进 Reason。
//
// 成品 resolveLiveRoomFrom @0xce7b40 以 int64 UID 为入参：入口 uid<=0 时
// fmt.Errorf("无效B站UID: %d")（@0xce84cb,len 0x11）。重建把该错误折叠进 Reason
// （reconcile 只按 Status 分派，不比较 Reason）。
func resolveLiveRoomFrom(uid int64) *liveRoomResolution {
	if uid <= 0 {
		return &liveRoomResolution{
			Status: liveRoomStatusRetry,
			Reason: fmt.Sprintf("无效B站UID: %d", uid),
		}
	}
	roomID := strconv.FormatInt(uid, 10)
	var resolveErrs []error
	masterResp, masterErr := getNoCookieBilibiliJSON(PathLiveMasterInfo, map[string]string{"id": roomID}, new(liveMasterInfoResponse))
	if masterErr != nil {
		resolveErrs = append(resolveErrs, masterErr, errors.New(liveRoomReasonLiveMasterRequestFailed))
	} else {
		master := masterResp.(*liveMasterInfoResponse)
		if code := masterErrOrCode(master); code != "" {
			if isExplicitMissingAccountMessage(master.Message + master.Msg) {
				return &liveRoomResolution{
					Status: liveRoomStatusAccountDeleted,
					Reason: liveRoomReasonPublicCardExplicitNotFound,
				}
			}
			resolveErrs = append(resolveErrs, errors.New(code))
		} else if master.Data.Info.UID > 0 && master.Data.RoomID > 0 {
			return &liveRoomResolution{
				Status: liveRoomStatusRoomFound,
				User:   NewUserInfo(master.Data.Info.UID, master.Data.RoomID, master.Data.Info.Uname, "https://live.bilibili.com/"+strconv.FormatInt(master.Data.RoomID, 10), master.Data.Info.Face),
				Reason: liveRoomReasonLiveMasterRoomFound,
			}
		} else if master.Data.Info.UID > 0 {
			// 账号存在但没解析出直播间号：成品 0xce7d3b 对 room 字段 <=0 继续走 card 兜底。
			resolveErrs = append(resolveErrs, errors.New(liveRoomReasonAccountExistsButMasterFailed))
		}
	}
	// 公开名片兜底：card 接口对房间号同样有效
	cardResp, cardErr := getNoCookieBilibiliJSON(PathPublicUserCard, map[string]string{"mid": roomID}, new(publicUserCardResponse))
	if cardErr != nil {
		resolveErrs = append(resolveErrs, cardErr, errors.New(liveRoomReasonPublicCardRequestFailed))
		return &liveRoomResolution{
			Status: liveRoomStatusRetry,
			Reason: joinResolveErrors(resolveErrs),
		}
	}
	card := cardResp.(*publicUserCardResponse)
	if code := cardErrOrCode(card); code != "" {
		if isExplicitMissingAccountMessage(card.Message) {
			return &liveRoomResolution{
				Status: liveRoomStatusAccountDeleted,
				Reason: liveRoomReasonPublicCardExplicitNotFound,
			}
		}
		resolveErrs = append(resolveErrs, errors.New(code))
		return &liveRoomResolution{
			Status: liveRoomStatusRetry,
			Reason: joinResolveErrors(resolveErrs),
		}
	}
	if card.Data == nil || card.Data.Card.Mid == "" {
		return &liveRoomResolution{
			Status: liveRoomStatusRetry,
			Reason: joinResolveErrors(resolveErrs),
		}
	}
	mid, err := strconv.ParseInt(card.Data.Card.Mid, 10, 64)
	if err != nil || mid <= 0 {
		return &liveRoomResolution{
			Status: liveRoomStatusRetry,
			Reason: joinResolveErrors(resolveErrs),
		}
	}
	return &liveRoomResolution{
		Status: liveRoomStatusAccountExistsWithoutRoom,
		User:   NewUserInfo(mid, 0, card.Data.Card.Name, "", card.Data.Card.Face),
		Reason: liveRoomReasonAccountExistsWithoutRoom,
	}
}

// getNoCookieBilibiliJSON 不带登录 Cookie 请求B站公开 JSON 接口。
// 返回值为 out 同类型指针。
func getNoCookieBilibiliJSON(target string, params map[string]string, out interface{}) (interface{}, error) {
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", localutils.FuncName()).Tracef("cost %v", time.Since(st))
	}()
	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(15 * time.Second),
		requests.RetryOption(3),
		requests.HeaderOption("User-Agent", bilibiliWebUserAgent),
		requests.HeaderOption("Referer", "https://live.bilibili.com/"),
		delete412ProxyOption,
	}
	statusCode := 0
	opts = append(opts, requests.HttpCodeOption(&statusCode))
	var respHeaders requests.RespHeader
	err := requests.GetWithHeader(target, params, out, &respHeaders, opts...)
	if err != nil {
		// 成品 getNoCookieBilibiliJSON 对非 200 状态返回
		// fmt.Errorf("B站匿名查询HTTP状态异常: %s")（@0xce8bae,len 0x24），
		// 其余失败按 JSON 解析失败
		// fmt.Errorf("解析B站匿名查询响应失败: %w")（@0xce8d48,len 0x26）。
		if statusCode != 0 && statusCode != http.StatusOK {
			return nil, fmt.Errorf("B站匿名查询HTTP状态异常: %s", strconv.Itoa(statusCode))
		}
		return nil, fmt.Errorf("解析B站匿名查询响应失败: %w", err)
	}
	if statusCode != 0 && statusCode != http.StatusOK {
		return nil, fmt.Errorf("B站匿名查询HTTP状态异常: %s", strconv.Itoa(statusCode))
	}
	return out, nil
}

// isExplicitMissingAccountMessage 判断响应是否为账号注销/不存在等显式提示。
func isExplicitMissingAccountMessage(message string) bool {
	message = strings.TrimSpace(message)
	if message == "" {
		return false
	}
	return strings.Contains(message, "账号已注销") ||
		strings.Contains(message, "账户已注销") ||
		strings.Contains(message, "用户不存在") ||
		strings.Contains(message, "账号不存在")
}

// masterErrOrCode 提取 Master/info 的业务错误码描述；成功返回空串。
func masterErrOrCode(resp *liveMasterInfoResponse) string {
	if resp == nil {
		return liveRoomReasonLiveMasterRequestFailed
	}
	if resp.Code != 0 {
		return fmt.Sprintf("直播主播资料API失败: code=%d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Info.UID <= 0 {
		return liveRoomReasonLiveMasterRequestFailed
	}
	return ""
}

// cardErrOrCode 提取 card 接口的业务错误码描述；成功返回空串。
func cardErrOrCode(resp *publicUserCardResponse) string {
	if resp == nil {
		return liveRoomReasonPublicCardRequestFailed
	}
	if resp.Code != 0 {
		return fmt.Sprintf("用户公开名片API未能确认账号状态: code=%d message=%s", resp.Code, resp.Message)
	}
	return ""
}

// joinResolveErrors 合并解析过程中的错误描述。
func joinResolveErrors(errs []error) string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		messages = append(messages, err.Error())
	}
	if len(messages) == 0 {
		return ""
	}
	return strings.Join(messages, "; ")
}
