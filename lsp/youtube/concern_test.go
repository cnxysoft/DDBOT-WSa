package youtube

import (
	"context"
	miraiConfig "github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestConcern(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	testEventChan := make(chan concern.Event, 16)
	testNotifyChan := make(chan concern.Notify)

	c := NewConcern(testNotifyChan)

	assert.NotNil(t, c.GetStateManager())

	c.StateManager.UseNotifyGeneratorFunc(c.notifyGenerator())
	c.StateManager.UseFreshFunc(func(ctx context.Context, eventChan chan<- concern.Event) {
		for {
			select {
			case e := <-testEventChan:
				if e != nil {
					eventChan <- e
				}
			case <-ctx.Done():
				return
			}
		}
	})

	assert.Nil(t, c.StateManager.Start())
	defer c.Stop()
	defer close(testEventChan)

	_, err := c.ParseId(test.NAME1)
	assert.Nil(t, err)

	err = c.StateManager.AddInfo(&Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME1,
		},
	})
	assert.Nil(t, err)

	_, err = c.StateManager.AddGroupConcern(test.G1, test.NAME1, Live)
	assert.Nil(t, err)

	_, err = c.StateManager.AddGroupConcern(test.G2, test.NAME1, Live)
	assert.Nil(t, err)

	identityInfo, err := c.Get(test.NAME1)
	assert.Nil(t, err)
	assert.EqualValues(t, test.NAME1, identityInfo.GetUid())

	assert.NotNil(t, c.GetGroupConcernConfig(test.G1, test.NAME1))

	testEventChan <- &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME1,
		},
		VideoType:         VideoType_Live,
		VideoStatus:       VideoStatus_Living,
		liveStatusChanged: true,
	}

	time.Sleep(time.Millisecond * 500)

	var g1 = false
	var g2 = false

	for i := 0; i < 2; i++ {
		select {
		case notify := <-testNotifyChan:
			if notify.GetGroupCode() == test.G1 {
				g1 = true
				assert.Equal(t, test.G1, notify.GetGroupCode())
				assert.Equal(t, test.NAME1, notify.GetUid())
			}
			if notify.GetGroupCode() == test.G2 {
				g2 = true
				assert.Equal(t, test.G2, notify.GetGroupCode())
				assert.Equal(t, test.NAME1, notify.GetUid())
			}
		case <-time.After(time.Second):
			assert.Fail(t, "no notify received")
		}
	}

	assert.True(t, g1)
	assert.True(t, g2)

	select {
	case <-testNotifyChan:
		assert.Fail(t, "should no notify received")
	case <-time.After(time.Second):

	}

	_, err = c.Remove(nil, test.G1, test.NAME1, Live)
	assert.Nil(t, err)
	_, err = c.Remove(nil, test.G2, test.NAME1, Live)
	assert.Nil(t, err)
}

func TestConcern_diffInfo_NoOldInfo(t *testing.T) {
	c := NewConcern(nil)

	liveWaiting := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-waiting",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Waiting,
	}
	liveLiving := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-living",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}
	video := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     test.BVID1,
		VideoType:   VideoType_Video,
		VideoStatus: VideoStatus_Upload,
	}

	got := c.diffInfo(nil, &Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{liveWaiting, liveLiving, video},
	})

	assert.Len(t, got, 2)
	assert.Same(t, liveWaiting, got[0])
	assert.Same(t, liveLiving, got[1])
	assert.False(t, liveWaiting.LiveStatusChanged())
	assert.True(t, liveLiving.LiveStatusChanged())
}

func TestConcern_diffInfo_FirstLoadSkipsReplayLikeFalseLiving(t *testing.T) {
	c := NewConcern(nil)

	falseLiving := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:          "fake-living",
		VideoTitle:       "fake living",
		VideoType:        VideoType_Live,
		VideoStatus:      VideoStatus_Living,
		DurationSeconds:  739,
		PublishTimestamp: 0,
	}
	realWaiting := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "real-waiting",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Waiting,
	}
	realLiving := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "real-living",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}

	got := c.diffInfo(nil, &Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{falseLiving, realWaiting, realLiving},
	})

	assert.Len(t, got, 2)
	assert.Same(t, realWaiting, got[0])
	assert.Same(t, realLiving, got[1])
}

func TestConcern_filterCard_UsesPublishTimestampForVideo(t *testing.T) {
	miraiConfig.GlobalConfig.Set("youtube.onlyOnlineNotify", true)
	defer miraiConfig.GlobalConfig.Set("youtube.onlyOnlineNotify", false)

	c := NewConcern(nil)
	c.cacheStartTs = time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC).Unix()

	assert.True(t, c.filterCard(&VideoInfo{
		VideoType:        VideoType_Video,
		VideoStatus:      VideoStatus_Upload,
		PublishTimestamp: c.cacheStartTs + 1,
		DurationSeconds:  242,
		VideoTimestamp:   0,
	}))

	assert.False(t, c.filterCard(&VideoInfo{
		VideoType:        VideoType_Video,
		VideoStatus:      VideoStatus_Upload,
		PublishTimestamp: c.cacheStartTs - 1,
		VideoTimestamp:   0,
	}))
}

func TestConcern_Add_ImmediateNotifyForExistingLivingSubscription(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	testNotifyChan := make(chan concern.Notify, 4)
	c := NewConcern(testNotifyChan)

	living := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-living",
		VideoTitle:  "currently live",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}

	err := c.StateManager.AddInfo(&Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{living},
	})
	assert.Nil(t, err)
	assert.Nil(t, c.StateManager.AddVideo(living))

	_, err = c.StateManager.AddGroupConcern(test.G1, test.NAME1, Live)
	assert.Nil(t, err)

	identityInfo, err := c.Add(nil, test.G2, test.NAME1, Live)
	assert.Nil(t, err)
	assert.EqualValues(t, test.NAME1, identityInfo.GetUid())

	select {
	case notify := <-testNotifyChan:
		assert.EqualValues(t, test.G2, notify.GetGroupCode())
		assert.EqualValues(t, test.NAME1, notify.GetUid())
		if n, ok := notify.(*ConcernNotify); assert.True(t, ok) {
			assert.Equal(t, "currently live", n.VideoInfo.VideoTitle)
			assert.True(t, n.VideoInfo.IsLiving())
		}
	case <-time.After(time.Second):
		assert.Fail(t, "expected immediate notify for second group subscription")
	}
}

func TestConcern_Add_ImmediateNotifyForAllExistingLivingSubscriptions(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	testNotifyChan := make(chan concern.Notify, 4)
	c := NewConcern(testNotifyChan)

	livingA := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-a",
		VideoTitle:  "currently live a",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}
	livingB := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-b",
		VideoTitle:  "currently live b",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}

	err := c.StateManager.AddInfo(&Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{livingA, livingB},
	})
	assert.Nil(t, err)
	assert.Nil(t, c.StateManager.AddVideo(livingA))
	assert.Nil(t, c.StateManager.AddVideo(livingB))

	identityInfo, err := c.Add(nil, test.G2, test.NAME1, Live)
	assert.Nil(t, err)
	assert.EqualValues(t, test.NAME1, identityInfo.GetUid())

	gotTitles := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case notify := <-testNotifyChan:
			assert.EqualValues(t, test.G2, notify.GetGroupCode())
			assert.EqualValues(t, test.NAME1, notify.GetUid())
			if n, ok := notify.(*ConcernNotify); assert.True(t, ok) {
				gotTitles[n.VideoInfo.VideoTitle] = true
				assert.True(t, n.VideoInfo.IsLiving())
			}
		case <-time.After(time.Second):
			assert.Fail(t, "expected immediate notify for every existing living stream")
		}
	}

	assert.True(t, gotTitles["currently live a"])
	assert.True(t, gotTitles["currently live b"])
}

func TestConcern_Add_ImmediateNotifySkipsHeaderSummaryWhenRealLivesExist(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	testNotifyChan := make(chan concern.Notify, 4)
	c := NewConcern(testNotifyChan)

	headerSummary := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:       "live-a",
		VideoTitle:    test.NAME2,
		VideoType:     VideoType_Live,
		VideoStatus:   VideoStatus_Living,
		Cover:         "https://example.com/avatar.jpg",
		HeaderSummary: true,
	}
	realLiving := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-b",
		VideoTitle:  "real live",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
		Cover:       "https://example.com/cover.jpg",
	}

	err := c.StateManager.AddInfo(&Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{headerSummary, realLiving},
	})
	assert.Nil(t, err)
	assert.Nil(t, c.StateManager.AddVideo(headerSummary))
	assert.Nil(t, c.StateManager.AddVideo(realLiving))

	_, err = c.Add(nil, test.G2, test.NAME1, Live)
	assert.Nil(t, err)

	select {
	case notify := <-testNotifyChan:
		assert.EqualValues(t, test.G2, notify.GetGroupCode())
		if n, ok := notify.(*ConcernNotify); assert.True(t, ok) {
			assert.Equal(t, "real live", n.VideoInfo.VideoTitle)
			assert.Equal(t, "https://example.com/cover.jpg", n.VideoInfo.Cover)
			assert.False(t, n.VideoInfo.HeaderSummary)
		}
	case <-time.After(time.Second):
		assert.Fail(t, "expected immediate notify for real living stream")
	}

	select {
	case notify := <-testNotifyChan:
		assert.Failf(t, "unexpected header summary notify", "notify=%#v", notify)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestConcern_diffInfo_NewLivesNotLimitedByVideoNotifyCount(t *testing.T) {
	c := NewConcern(nil)

	oldInfo := &Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{
			{
				UserInfo: UserInfo{
					ChannelId:   test.NAME1,
					ChannelName: test.NAME2,
				},
				VideoId:     "old-video",
				VideoType:   VideoType_Video,
				VideoStatus: VideoStatus_Upload,
			},
		},
	}
	newLiveA := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-a",
		VideoTitle:  "live a",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}
	newLiveB := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-b",
		VideoTitle:  "live b",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}
	newVideoA := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:          "video-a",
		VideoType:        VideoType_Video,
		VideoStatus:      VideoStatus_Upload,
		PublishTimestamp: time.Now().Unix(),
	}
	newVideoB := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:          "video-b",
		VideoType:        VideoType_Video,
		VideoStatus:      VideoStatus_Upload,
		PublishTimestamp: time.Now().Unix(),
	}

	got := c.diffInfo(oldInfo, &Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{newLiveA, newVideoA, newLiveB, newVideoB},
	})

	assert.Len(t, got, 3)
	assert.Contains(t, got, newLiveA)
	assert.Contains(t, got, newLiveB)
	assert.Contains(t, got, newVideoA)
	assert.True(t, newLiveA.LiveStatusChanged())
	assert.True(t, newLiveB.LiveStatusChanged())
}

func TestConcern_diffInfo_VideoToLiveTransitionNotified(t *testing.T) {
	c := NewConcern(nil)

	oldVideo := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:          "same-id",
		VideoTitle:       "old video",
		VideoType:        VideoType_Video,
		VideoStatus:      VideoStatus_Upload,
		PublishTimestamp: time.Now().Add(-time.Hour).Unix(),
		DurationSeconds:  600,
	}
	newLive := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "same-id",
		VideoTitle:  "now live",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}

	got := c.diffInfo(&Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{oldVideo},
	}, &Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{newLive},
	})

	if assert.Len(t, got, 1) {
		assert.Same(t, newLive, got[0])
		assert.True(t, got[0].LiveStatusChanged())
	}
}

func TestLivingVideoInfos_SkipReplayLikeFalseLivingEntries(t *testing.T) {
	falseLiving := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:          "false-live",
		VideoTitle:       "replay-like false live",
		VideoType:        VideoType_Live,
		VideoStatus:      VideoStatus_Living,
		PublishTimestamp: time.Now().Add(-time.Hour).Unix(),
		DurationSeconds:  964,
	}
	realLiving := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "real-live",
		VideoTitle:  "actual live",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}

	got := livingVideoInfos(&Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{falseLiving, realLiving},
	})

	if assert.Len(t, got, 1) {
		assert.Same(t, realLiving, got[0])
	}
}

func TestConcern_RemoveLastLiveSubscription_ClearsLiveState(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	testNotifyChan := make(chan concern.Notify, 4)
	c := NewConcern(testNotifyChan)

	living := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     "live-living",
		VideoTitle:  "currently live",
		VideoType:   VideoType_Live,
		VideoStatus: VideoStatus_Living,
	}
	video := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoId:     test.BVID1,
		VideoTitle:  "normal video",
		VideoType:   VideoType_Video,
		VideoStatus: VideoStatus_Upload,
	}

	err := c.StateManager.AddInfo(&Info{
		UserInfo: UserInfo{
			ChannelId:   test.NAME1,
			ChannelName: test.NAME2,
		},
		VideoInfo: []*VideoInfo{living, video},
	})
	assert.Nil(t, err)
	assert.Nil(t, c.StateManager.AddVideo(living))
	assert.Nil(t, c.StateManager.AddVideo(video))

	_, err = c.StateManager.AddGroupConcern(test.G1, test.NAME1, Live)
	assert.Nil(t, err)

	_, err = c.Remove(nil, test.G1, test.NAME1, Live)
	assert.Nil(t, err)

	_, err = c.StateManager.GetVideo(test.NAME1, living.VideoId)
	assert.Error(t, err)

	info, err := c.StateManager.GetInfo(test.NAME1)
	assert.Nil(t, err)
	if assert.Len(t, info.VideoInfo, 1) {
		assert.Equal(t, video.VideoId, info.VideoInfo[0].VideoId)
		assert.True(t, info.VideoInfo[0].IsVideo())
	}

	_, err = c.Add(nil, test.G2, test.NAME1, Live)
	assert.Nil(t, err)

	select {
	case notify := <-testNotifyChan:
		assert.Failf(t, "unexpected immediate notify after live cache cleanup", "notify=%#v", notify)
	case <-time.After(300 * time.Millisecond):
	}
}
