package utils

import (
	"fmt"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

type HackedBot struct {
	Bot        adapter.BotCaller
	testGroups []*adapter.GroupInfo
	testUin    int64
	testMode   bool
}

func (h *HackedBot) valid() bool {
	result := true
	if h == nil || h.Bot == nil || h.testMode {
		result = false
	}
	return result
}

func (h *HackedBot) FindFriend(uin int64) *adapter.FriendInfo {
	if !h.valid() {
		return nil
	}
	return h.Bot.FindFriend(uin)
}

func (h *HackedBot) FindGroup(code int64) *adapter.GroupInfo {
	if !h.valid() {
		for _, gi := range h.testGroups {
			if gi.Code == code {
				return gi
			}
		}
		return nil
	}
	return h.Bot.FindGroup(code)
}

func (h *HackedBot) SolveFriendRequest(flag string, accept bool) error {
	if !h.valid() {
		return fmt.Errorf("bot not valid")
	}
	return h.Bot.SetFriendAddRequest(flag, accept)
}

func (h *HackedBot) SolveGroupJoinRequest(i interface{}, accept bool, block bool, reason string) {
	if !h.valid() {
		return
	}
}

func (h *HackedBot) GetGroupList() []*adapter.GroupInfo {
	if !h.valid() {
		return h.testGroups
	}
	return h.Bot.GetGroupList()
}

func (h *HackedBot) GetFriendList() []*adapter.FriendInfo {
	if !h.valid() {
		return nil
	}
	return h.Bot.GetFriendList()
}

func (h *HackedBot) IsOnline() bool {
	return h.valid()
}

func (h *HackedBot) GetUin() int64 {
	if !h.valid() {
		return h.testUin
	}
	return h.Bot.GetUin()
}

var hackedBot = &HackedBot{Bot: nil}

func GetBot() *HackedBot {
	return hackedBot
}

func GetBotInstance() interface{} {
	if hackedBot.Bot != nil {
		return hackedBot.Bot
	}
	return nil
}

func (h *HackedBot) DownloadFile(url, base64, name string, headers []string) (string, error) {
	if !h.valid() {
		return "", fmt.Errorf("bot not valid")
	}
	return h.Bot.DownloadFile(url, base64, name, headers)
}

func (h *HackedBot) GetFileUrl(groupCode int64, fileId string) string {
	if !h.valid() {
		return ""
	}
	return h.Bot.GetFileUrl(groupCode, fileId)
}

func (h *HackedBot) GetMsg(msgId int32) (*adapter.GetMsgResult, error) {
	if !h.valid() {
		return nil, fmt.Errorf("bot not valid")
	}
	return h.Bot.GetMsg(msgId)
}

func (h *HackedBot) GetMsgOrg(msgId int32) (interface{}, error) {
	if !h.valid() {
		return nil, fmt.Errorf("bot not valid")
	}
	return h.Bot.GetMsgOrg(msgId)
}

func (h *HackedBot) RecallMsg(msgId int32) error {
	if !h.valid() {
		return fmt.Errorf("bot not valid")
	}
	return h.Bot.RecallMsg(msgId)
}

func (h *HackedBot) EditGroupCard(groupCode, memberUin int64, card string) error {
	if !h.valid() {
		return fmt.Errorf("bot not valid")
	}
	return h.Bot.EditGroupCard(groupCode, memberUin, card)
}

func (h *HackedBot) SendApi(api string, params map[string]interface{}) (interface{}, error) {
	if !h.valid() {
		return nil, fmt.Errorf("bot not valid")
	}
	return h.Bot.SendApi(api, params)
}

func (h *HackedBot) SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	if !h.valid() {
		return -1, "", fmt.Errorf("bot not valid")
	}
	return h.Bot.SendGroupForwardMessage(groupCode, nodes, options)
}

func (h *HackedBot) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	if !h.valid() {
		return -1, "", fmt.Errorf("bot not valid")
	}
	return h.Bot.SendPrivateForwardMessage(userID, nodes, options)
}

func (h *HackedBot) SetGroupLeave(groupCode int64, isDismiss bool) error {
	if !h.valid() {
		return fmt.Errorf("bot not valid")
	}
	return h.Bot.SetGroupLeave(groupCode, isDismiss)
}

func (h *HackedBot) SetGroupAddRequest(flag string, approve bool, reason string) error {
	if !h.valid() {
		return fmt.Errorf("bot not valid")
	}
	return h.Bot.SetGroupAddRequest(flag, approve, reason)
}

// SolveGroupInvitedRequest 处理加群邀请 (通过SendApi调用set_group_add_request)
func (h *HackedBot) SolveGroupInvitedRequest(flag string, approve bool, reason string) error {
	if !h.valid() {
		return fmt.Errorf("bot not valid")
	}
	return h.Bot.SetGroupAddRequest(flag, approve, reason)
}

func (h *HackedBot) TESTSetUin(uin int64) {
	h.testUin = uin
}

func (h *HackedBot) TESTAddGroup(groupCode int64) {
	for _, g := range h.testGroups {
		if g.Code == groupCode {
			return
		}
	}
	h.testGroups = append(h.testGroups, &adapter.GroupInfo{
		Uin:  groupCode,
		Code: groupCode,
	})
}

func (h *HackedBot) TESTAddMember(groupCode int64, uin int64, permission interface{}) {
	h.TESTAddGroup(groupCode)
	for _, g := range h.testGroups {
		if g.Code != groupCode {
			continue
		}
		for _, m := range g.Members {
			if m.Uin == uin {
				return
			}
		}
		g.Members = append(g.Members, &adapter.GroupMemberInfo{
			Group:      g,
			Uin:        uin,
			Permission: adapter.Member,
		})
	}
}

func (h *HackedBot) TESTReset() {
	h.testGroups = nil
	h.testUin = 0
	h.testMode = false
}

func (h *HackedBot) TESTSet() {
	h.testMode = true
}
