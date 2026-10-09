package lsp

import (
	"fmt"

	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

// get_group_member_info 返回的角色值
const (
	groupRoleOwner  = "owner"
	groupRoleAdmin  = "admin"
	groupRoleMember = "member"
)

// checkAtAllGroupAdministrator 实时查询 BOT 在群内的角色。
//
// 成品证据（notify.go:88 调用点 @cc2c11；at_all_permission.go:20，320B）：
//   - 入参 (groupCode int64, uin int64)，返回 (bool, string, error)；
//   - 该函数自身不写任何日志：成品 callees 只有 queryRealtimeGroupRole 一个 E8 调用，
//     "实时查询机器人群角色失败/完成" 两条文案由 ConcernNotify 按 admin_check 阶段输出；
//   - 返回的 role 供调用方 bot_group_member_role 字段使用。
func checkAtAllGroupAdministrator(groupCode int64, uin int64) (bool, string, error) {
	role, err := queryRealtimeGroupRole(groupCode, uin)
	if err != nil {
		return false, role, err
	}
	return role == groupRoleOwner || role == groupRoleAdmin, role, nil
}

// queryRealtimeGroupRole 调用 get_group_member_info 实时查询群成员角色。
// 返回值是 OneBot 的原始 role 字符串：owner / admin / member。
func queryRealtimeGroupRole(groupCode int64, userID int64) (string, error) {
	resp, err := localutils.GetBot().SendApi("get_group_member_info", map[string]interface{}{
		"group_id": groupCode,
		"user_id":  userID,
	})
	if err != nil {
		return "", err
	}
	data, ok := resp.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("get_group_member_info returned unexpected data type %T", resp)
	}
	respGroupID, _ := interfaceToInt64(data["group_id"])
	respUserID, _ := interfaceToInt64(data["user_id"])
	if respGroupID != groupCode || respUserID != userID {
		return "", fmt.Errorf("get_group_member_info returned identity group_id=%d user_id=%d for query group_id=%d user_id=%d",
			respGroupID, respUserID, groupCode, userID)
	}
	role, _ := data["role"].(string)
	return role, nil
}
