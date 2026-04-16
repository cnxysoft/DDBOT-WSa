package utils

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/stretchr/testify/assert"
)

func TestGetBot(t *testing.T) {
	defer GetBot().TESTReset()

	bot := GetBot()
	bot.TESTSetUin(test.UID1)
	assert.EqualValues(t, test.UID1, bot.GetUin())
	assert.Nil(t, bot.FindFriend(1))
	assert.Nil(t, bot.FindGroup(test.G1))
	assert.False(t, bot.IsOnline())
	assert.Empty(t, bot.GetFriendList())
	assert.Empty(t, bot.GetGroupList())
	bot.SolveFriendRequest("", false)
	bot.SolveGroupJoinRequest(nil, false, false, "")

	bot.TESTAddGroup(123)
	bot.TESTAddGroup(test.G2)
	bot.TESTAddGroup(test.G1)
	bot.TESTAddMember(test.G1, test.UID1, adapter.Member)
	bot.TESTAddMember(test.G1, test.UID2, adapter.Member)
	bot.TESTAddMember(test.G1, test.UID1, adapter.Member)
	bot.TESTAddMember(test.G2, test.UID2, adapter.Member)
	assert.Len(t, bot.GetGroupList(), 3)
	bot.TESTReset()
	assert.Empty(t, bot.GetGroupList())

	test.InitMirai()
	defer test.CloseMirai()
}
