package youtube

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	template.InitTemplateLoader()
	m.Run()
}

func TestVideoInfo(t *testing.T) {
	vi := &VideoInfo{
		UserInfo:  *NewUserInfo(test.NAME1, test.NAME2),
		VideoId:   test.BVID1,
		VideoType: VideoType_Video,
	}
	assert.EqualValues(t, test.NAME2, vi.GetChannelName())
	assert.Equal(t, VideoType_Video, vi.VideoType)
	assert.Equal(t, Video, vi.Type())
	assert.True(t, vi.IsVideo())

	info := NewInfo([]*VideoInfo{vi}, false)
	assert.NotNil(t, info)

	notify := NewConcernNotify(test.G1, vi)
	assert.NotNil(t, notify)
	assert.Equal(t, test.G1, notify.GetGroupCode())
	assert.Equal(t, test.NAME1, notify.GetUid())
	assert.NotNil(t, notify.Logger())
	assert.Equal(t, Video, notify.Type())

	assert.Equal(t, Site, notify.Site())

	m := notify.ToMessage()
	assert.NotNil(t, m)

	notify.VideoType = VideoType_Live
	m = notify.ToMessage()
	assert.NotNil(t, m)

	notify.VideoStatus = VideoStatus_Living
	m = notify.ToMessage()
	assert.NotNil(t, m)

}

func TestVideoInfo_GetMSG_Video(t *testing.T) {
	vi := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   "test_channel_id",
			ChannelName: "TestChannel",
		},
		Cover:          "https://example.com/cover.jpg",
		VideoId:        "test_video_id",
		VideoTitle:     "Test Video Title",
		VideoType:      VideoType_Video,
		VideoStatus:    VideoStatus_Upload,
		VideoTimestamp: 1624126814,
		GroupCode:      test.G1,
	}

	assert.False(t, vi.IsLive())
	assert.False(t, vi.IsLiving())
	assert.False(t, vi.IsWaiting())
	assert.True(t, vi.IsVideo())
	assert.Equal(t, Video, vi.Type())

	m := vi.GetMSG()
	assert.NotNil(t, m)
}

func TestVideoInfo_GetMSG_LiveLiving(t *testing.T) {
	vi := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   "test_channel_id",
			ChannelName: "TestChannel",
		},
		Cover:          "https://example.com/cover.jpg",
		VideoId:        "test_live_id",
		VideoTitle:     "Test Live Title",
		VideoType:      VideoType_Live,
		VideoStatus:    VideoStatus_Living,
		VideoTimestamp: 1624126814,
		GroupCode:      test.G1,
	}

	assert.True(t, vi.IsLive())
	assert.True(t, vi.IsLiving())
	assert.False(t, vi.IsWaiting())
	assert.False(t, vi.IsVideo())
	assert.Equal(t, Live, vi.Type())

	m := vi.GetMSG()
	assert.NotNil(t, m)
}

func TestVideoInfo_GetMSG_LiveWaiting(t *testing.T) {
	vi := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   "test_channel_id",
			ChannelName: "TestChannel",
		},
		Cover:          "https://example.com/cover.jpg",
		VideoId:        "test_live_id",
		VideoTitle:     "Test Live Reservation",
		VideoType:      VideoType_FirstLive,
		VideoStatus:    VideoStatus_Waiting,
		VideoTimestamp: 1624126814,
		GroupCode:      test.G1,
	}

	assert.True(t, vi.IsLive())
	assert.False(t, vi.IsLiving())
	assert.True(t, vi.IsWaiting())
	assert.False(t, vi.IsVideo())
	assert.Equal(t, Live, vi.Type())

	m := vi.GetMSG()
	assert.NotNil(t, m)
}

func TestConcernNotify_ToMessage(t *testing.T) {
	vi := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   "test_channel_id",
			ChannelName: "TestChannel",
		},
		Cover:          "https://example.com/cover.jpg",
		VideoId:        "test_video_id",
		VideoTitle:     "Test Video",
		VideoType:      VideoType_Video,
		VideoStatus:    VideoStatus_Upload,
		VideoTimestamp: 1624126814,
	}

	notify := NewConcernNotify(test.G1, vi)
	assert.NotNil(t, notify)
	assert.Equal(t, test.G1, notify.GetGroupCode())

	m := notify.ToMessage()
	assert.NotNil(t, m)
	assert.Equal(t, test.G1, notify.VideoInfo.GroupCode)
}

func TestVideoInfo_GetMSG_Cache(t *testing.T) {
	vi := &VideoInfo{
		UserInfo: UserInfo{
			ChannelId:   "test_channel_id",
			ChannelName: "TestChannel",
		},
		Cover:          "https://example.com/cover.jpg",
		VideoId:        "test_video_id",
		VideoTitle:     "Test Video",
		VideoType:      VideoType_Video,
		VideoStatus:    VideoStatus_Upload,
		VideoTimestamp: 1624126814,
		GroupCode:      test.G1,
	}

	m1 := vi.GetMSG()
	m2 := vi.GetMSG()
	assert.Equal(t, m1, m2, "GetMSG should return cached message")
}
