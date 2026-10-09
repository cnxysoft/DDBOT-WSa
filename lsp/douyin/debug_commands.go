package douyin

import (
	"errors"
	"fmt"
	"sort"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
)

// 抖音调试指令（诊断指令，文档限定测试群使用）：
// "强制拉取开播推送" / "强制拉取第一个动态"。
// 由 lsp 层调试框架（douyinDebugController）注册调用。

// debugSubscribedIDsForGroup 列出群内指定订阅类型的全部去重 id（成品 0xd7ccc0）。
// 由调试指令用于校验目标 id 是否属于该群。
func (d *Concern) debugSubscribedIDsForGroup(groupCode int64, ctype concern_type.Type) ([]string, error) {
	_, ids, _, err := d.StateManager.ListConcernState(func(g int64, cid interface{}, p concern_type.Type) bool {
		return g == groupCode && p.ContainAll(ctype)
	})
	// 成品 debugSubscribedIDsForGroup @0xd7ccc0 +0x39c fmt.Errorf(
	// "该群没有对应类型的抖音订阅：%s", ctype)(@0x10d79bb,len44)；+0x418 另有静态文案
	// "该群没有对应类型的抖音订阅"(@0x10d1b2b)，调用方在 id 不在清单时返回。
	if err != nil {
		return nil, fmt.Errorf("该群没有对应类型的抖音订阅：%s", ctype)
	}
	seen := make(map[string]bool, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		uid, _ := id.(string)
		if uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		result = append(result, uid)
	}
	sort.Strings(result)
	// 成品 @0xd7d0c4/0xd7d0fc 分支：群内该类型清单为空时返回带类型的
	// "该群没有对应类型的抖音订阅：%s"；非空但目标 id 不在清单时由调用方返回静态文案。
	if len(result) == 0 {
		return nil, fmt.Errorf("该群没有对应类型的抖音订阅：%s", ctype)
	}
	return result, nil
}

// douyinDebugIDsContain 判断 id 是否在调试订阅清单中。
func douyinDebugIDsContain(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// 调试指令的「模块已启动」前置检查在 lsp 层完成：成品 lsp/groupCommand.go:214
// runDouyinDebugCommand 于 0xc94240 用 runtime.mapaccess2_faststr 查全局 concern 注册表
// （key "douyin"，0x109f5ec,len6），未命中才 reply "抖音模块当前未启动"（0xc94285）。
// douyin 包内不做该检查；成品的 douyin.(*Concern).DebugForceLiveStart/DebugForceLatestNews
// 中没有任何 moduleStarted 读取（全局 0x1bf43ab 全二进制 xref 只有 4 处写入）。

// DebugForceLiveStart 强制拉取开播推送并提交（调试用）。
func (d *Concern) DebugForceLiveStart(groupCode int64, id string) (string, error) {
	ids, err := d.debugSubscribedIDsForGroup(groupCode, Live)
	if err != nil {
		return "", err
	}
	if !douyinDebugIDsContain(ids, id) {
		return "", errors.New("该群没有对应类型的抖音订阅")
	}
	info, err := d.FindOrLoadUserInfo(id)
	if err != nil {
		return "", fmt.Errorf("%s 用户资料: %w", id, err)
	}
	snapshot, err := FreshLiveSnapshot(id)
	if err != nil {
		return "", fmt.Errorf("%s 直播状态: %w", id, err)
	}
	if !snapshot.Living {
		return "", errors.New("该群当前没有正在直播的抖音订阅")
	}
	roomID := info.GetRoomId()
	if roomID == "" {
		roomID = snapshot.WebRID
	}
	if roomID == "" {
		return "", fmt.Errorf("%s 当前开播但没有 web_rid", id)
	}
	room, err := GetRoomDataWithRoomID(roomID)
	if err != nil {
		return "", fmt.Errorf("%s 直播间详情: %w", id, err)
	}
	if room == nil {
		return "", fmt.Errorf("%s 直播间详情为空", id)
	}
	live := &LiveInfo{
		UserInfo:          *info,
		IsLiving:          true,
		LiveTitle:         snapshot.Title,
		Cover:             snapshot.Cover,
		liveStatusChanged: true,
	}
	if room.Title != "" {
		live.LiveTitle = room.Title
	}
	if room.Cover != "" {
		live.Cover = room.Cover
	}
	d.notify <- NewConcernLiveNotify(groupCode, live)
	logger.WithField("debug_group", groupCode).
		WithField("id", id).
		WithField("title_present", live.LiveTitle != "").
		WithField("cover_present", live.Cover != "").
		Info("抖音调试：已强制拉取并提交开播推送")
	return fmt.Sprintf("已强制拉取并提交开播推送：%s（%s）", info.NikeName, id), nil
}

// DebugForceLatestNews 强制拉取第一条动态并提交（调试用）。
func (d *Concern) DebugForceLatestNews(groupCode int64, id string) (string, error) {
	ids, err := d.debugSubscribedIDsForGroup(groupCode, News)
	if err != nil {
		return "", err
	}
	if !douyinDebugIDsContain(ids, id) {
		return "", errors.New("该群没有对应类型的抖音订阅")
	}
	info, err := d.FindOrLoadUserInfo(id)
	if err != nil {
		return "", fmt.Errorf("%s 用户资料: %w", id, err)
	}
	resp, raw, err := requestDouyinSignedUserPosts(info.SecUid)
	if err != nil {
		return "", fmt.Errorf("%s 作品列表: %w", id, err)
	}
	if resp.GetStatusCode() != 0 {
		return "", fmt.Errorf("%s 作品列表 status_code=%d", id, resp.GetStatusCode())
	}
	rememberDouyinPostExtras(raw)
	var resCards []*UserPostsResponse_AwemeList
	for _, card := range resp.GetAwemeList() {
		// 遇到审核中的视频就跳过
		if isUnderReview(card.GetStatus()) {
			continue
		}
		resCards = append(resCards, card)
		break
	}
	if len(resCards) == 0 {
		return "", errors.New("该群订阅暂未取得可推送的抖音动态")
	}
	newsInfo := NewNewsInfoWithDetail(info, resCards)
	notifies := NewConcernNewsNotify(groupCode, newsInfo, d)
	for _, notify := range notifies {
		d.notify <- notify
	}
	logger.WithField("debug_group", groupCode).
		WithField("id", id).
		WithField("aweme_id", resCards[0].GetAwemeId()).
		Info("抖音调试：已强制拉取并提交最新动态")
	return fmt.Sprintf("已强制拉取并提交最新动态：%s（%s）", info.NikeName, info.SecUid), nil
}
