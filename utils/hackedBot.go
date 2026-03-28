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

func (h *HackedBot) SolveFriendRequest(req interface{}, accept bool) {
	if !h.valid() {
		return
	}
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

func (h *HackedBot) GetMsg(msgId int32) (interface{}, error) {
	if !h.valid() {
		return nil, fmt.Errorf("bot not valid")
	}
	return h.Bot.GetMsg(msgId)
}

func (h *HackedBot) RecallMsg(msgId int32) error {
	if !h.valid() {
		return fmt.Errorf("bot not valid")
	}
	return h.Bot.RecallMsg(msgId)
}

func (h *HackedBot) SendApi(api string, params map[string]interface{}) (interface{}, error) {
	if !h.valid() {
		return nil, fmt.Errorf("bot not valid")
	}
	return h.Bot.SendApi(api, params)
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
