package xhs

import (
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/stretchr/testify/require"
)

func TestKeySetParsesStringConcernKey(t *testing.T) {
	key := buntdb.XHSGroupConcernStateKey(test.G1, "65f41f45000000000500f2c2")
	groupCode, id, err := NewKeySet().ParseGroupConcernStateKey(key)
	require.NoError(t, err)
	require.EqualValues(t, test.G1, groupCode)
	require.Equal(t, "65f41f45000000000500f2c2", id)
}

func TestStateManagerUsesStringKeys(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	sm := NewStateManager(nil)
	userInfo := &UserInfo{
		Uid:        "65f41f45000000000500f2c2",
		RedID:      "632666029",
		Name:       "target-nick",
		Face:       "avatar",
		RoomId:     "570281955281372701",
		UserId:     "65f41f45000000000500f2c2",
		ProfileURL: "https://www.xiaohongshu.com/user/profile/65f41f45000000000500f2c2",
	}
	require.NoError(t, sm.AddUserInfo(userInfo))

	gotUserInfo, err := sm.GetUserInfo(userInfo.Uid)
	require.NoError(t, err)
	require.Equal(t, userInfo, gotUserInfo)

	liveInfo := &LiveInfo{
		UserInfo:  *userInfo,
		Status:    LiveStatus_Living,
		LiveTitle: "target-nick",
		Url:       "https://www.xiaohongshu.com/livestream/570281955281372701",
		Cover:     "avatar",
	}
	require.NoError(t, sm.AddLiveInfo(liveInfo))

	gotLiveInfo, err := sm.GetLiveInfo(userInfo.Uid)
	require.NoError(t, err)
	require.Equal(t, liveInfo.Uid, gotLiveInfo.Uid)
	require.Equal(t, liveInfo.RoomId, gotLiveInfo.RoomId)
	require.Equal(t, liveInfo.Status, gotLiveInfo.Status)

	newsInfo := &NewsInfo{
		UserInfo:     *userInfo,
		LatestNoteID: "note-1",
	}
	require.NoError(t, sm.AddNewsInfo(newsInfo))

	gotNewsInfo, err := sm.GetNewsInfo(userInfo.Uid)
	require.NoError(t, err)
	require.Equal(t, newsInfo.Uid, gotNewsInfo.Uid)
	require.Equal(t, newsInfo.LatestNoteID, gotNewsInfo.LatestNoteID)

	replaced, err := sm.MarkNoteId("note-1")
	require.NoError(t, err)
	require.False(t, replaced)

	replaced, err = sm.MarkNoteId("note-1")
	require.NoError(t, err)
	require.True(t, replaced)

	require.NoError(t, sm.RemoveUserInfo(userInfo.Uid))
	_, err = sm.GetUserInfo(userInfo.Uid)
	require.Error(t, err)

	require.NoError(t, sm.RemoveNewsInfo(userInfo.Uid))
	_, err = sm.GetNewsInfo(userInfo.Uid)
	require.Error(t, err)
}

func TestStateManagerMarkNoteIdSetsTTL(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	sm := NewStateManager(nil)
	replaced, err := sm.MarkNoteId("note-ttl")
	require.NoError(t, err)
	require.False(t, replaced)

	var ttl time.Duration
	_, err = sm.Get(sm.MarkNoteIdKey("note-ttl"), buntdb.GetTTLOpt(&ttl))
	require.NoError(t, err)
	require.Greater(t, ttl, time.Hour*119)
	require.LessOrEqual(t, ttl, xhsMarkNoteTTL)
}
