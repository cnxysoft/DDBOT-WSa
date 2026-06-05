package youtube

import (
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/stretchr/testify/assert"
	"strconv"
	"testing"
)

func newLiveInfo(channelId string, live bool, liveStatusChanged bool, liveTitleChanged bool) *ConcernNotify {
	li := &ConcernNotify{
		VideoInfo: &VideoInfo{
			UserInfo: UserInfo{
				ChannelId: channelId,
			},
			liveStatusChanged: liveStatusChanged,
			liveTitleChanged:  liveTitleChanged,
			VideoType:         VideoType_Live,
		},
	}
	if live {
		li.VideoStatus = VideoStatus_Living
	} else {
		li.VideoStatus = VideoStatus_Waiting
	}
	return li
}

func newNewsInfo(channelId string) *ConcernNotify {
	li := &ConcernNotify{
		VideoInfo: &VideoInfo{
			UserInfo: UserInfo{
				ChannelId: channelId,
			},
			VideoType: VideoType_Video,
		},
	}
	return li
}

func newShortsInfo(channelId string) *ConcernNotify {
	return &ConcernNotify{
		VideoInfo: &VideoInfo{
			UserInfo: UserInfo{
				ChannelId: channelId,
			},
			VideoType:   VideoType_Shorts,
			VideoStatus: VideoStatus_Upload,
		},
	}
}

func TestNewGroupConcernConfig(t *testing.T) {
	g := NewGroupConcernConfig(new(concern.GroupConcernConfig))
	assert.NotNil(t, g)
}

func TestGroupConcernConfig_ShouldSendHook(t *testing.T) {
	var notify = []concern.Notify{
		// 直播预告 推
		newLiveInfo(test.NAME1, false, false, false),
		// 直播预告 标题变了 推
		newLiveInfo(test.NAME1, false, false, true),
		// 可能吗
		newLiveInfo(test.NAME1, false, true, false),
		// 可能吗
		newLiveInfo(test.NAME1, false, true, true),
		// 直播状态 什么也没变 不推
		newLiveInfo(test.NAME1, true, false, false),
		// 直播状态 改了标题 检查配置
		newLiveInfo(test.NAME1, true, false, true),
		// 开播了 推
		newLiveInfo(test.NAME1, true, true, false),
		// 开播了改了标题 推
		newLiveInfo(test.NAME1, true, true, true),
	}

	var testCase = []*GroupConcernConfig{
		{
			IConfig: &concern.GroupConcernConfig{},
		},
		{
			IConfig: &concern.GroupConcernConfig{
				GroupConcernNotify: concern.GroupConcernNotifyConfig{
					TitleChangeNotify: Live,
				},
			},
		},
	}
	var expected = [][]bool{
		{
			true, true, true, true,
			false, false, true, true,
		},
		{
			true, true, true, true,
			false, true, true, true,
		},
	}
	assert.Equal(t, len(expected), len(testCase))
	for index1, g := range testCase {
		assert.Equal(t, len(expected[index1]), len(notify))
		for index2, liveInfo := range notify {
			result := g.ShouldSendHook(liveInfo)
			assert.NotNil(t, result)
			assert.Equal(t, expected[index1][index2], result.Pass)
		}
	}
}

func TestGroupConcernConfig_AtBeforeHook(t *testing.T) {
	var notify = []concern.Notify{
		// 下播状态 什么也没变 不推
		newLiveInfo(test.NAME1, false, false, false),
		// 下播状态 标题变了 不推
		newLiveInfo(test.NAME1, false, false, true),
		// 下播了 检查配置
		newLiveInfo(test.NAME1, false, true, false),
		// 下播了 检查配置
		newLiveInfo(test.NAME1, false, true, true),
		// 直播状态 什么也没变 不推
		newLiveInfo(test.NAME1, true, false, false),
		// 直播状态 改了标题 检查配置
		newLiveInfo(test.NAME1, true, false, true),
		// 开播了 推
		newLiveInfo(test.NAME1, true, true, false),
		// 开播了改了标题 推
		newLiveInfo(test.NAME1, true, true, true),
	}
	var expected = []bool{
		false, false, false, false, false, false, true, true,
	}
	var config = &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
	for idx, n := range notify {
		hook := config.AtBeforeHook(n)
		assert.EqualValues(t, expected[idx], hook.Pass)
	}

}

func TestCheckTypeDefine(t *testing.T) {
	// Known string names should pass.
	assert.Empty(t, CheckTypeDefine([]string{YtVideo, YtShorts, YtLive, YtFirstLive}))

	// Known int values (0..3) should pass — even though some (Live, FirstLive)
	// aren't mapped in PredefinedType yet, callers may still configure them
	// numerically and FilterHook handles them via ParseInt.
	for i := 0; i <= 3; i++ {
		assert.Empty(t, CheckTypeDefine([]string{strconv.Itoa(i)}),
			"int %d should be a valid VideoType", i)
	}

	// Out-of-range ints and unknown strings should be flagged.
	assert.NotEmpty(t, CheckTypeDefine([]string{"bogus"}))
	assert.NotEmpty(t, CheckTypeDefine([]string{strconv.Itoa(99)}))
	assert.NotEmpty(t, CheckTypeDefine([]string{"video", "bogus"}),
		"any invalid entry should make the slice non-empty")
}

func TestGroupConcernConfig_Validate(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		assert.Nil(t, g.Validate())
	})

	t.Run("valid string types", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType,
			`{"type":["video","shorts"]}`)
		assert.Nil(t, g.Validate())
	})

	t.Run("valid int types", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType,
			`{"type":["2","3"]}`)
		assert.Nil(t, g.Validate())
	})

	t.Run("invalid type must fail", func(t *testing.T) {
		// Regression: previously Validate() returned the wrong err variable and
		// effectively always succeeded even with bogus type names.
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType,
			`{"type":["bogus"]}`)
		err := g.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
	})

	t.Run("unknown rule type rejected", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule("nonsense", `{}`)
		assert.ErrorIs(t, g.Validate(), concern.ErrConfigNotSupported)
	})
}

func TestGroupConcernConfig_FilterHook(t *testing.T) {
	t.Run("no filter passes everything", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		assert.True(t, g.FilterHook(newLiveInfo(test.NAME1, true, true, false)).Pass)
		assert.True(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.True(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
	})

	t.Run("type=video blocks live and shorts", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType, `{"type":["video"]}`)
		assert.True(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.False(t, g.FilterHook(newLiveInfo(test.NAME1, true, true, false)).Pass)
		assert.False(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
	})

	t.Run("type=shorts blocks video and live", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType, `{"type":["shorts"]}`)
		assert.True(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
		assert.False(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.False(t, g.FilterHook(newLiveInfo(test.NAME1, true, true, false)).Pass)
	})

	t.Run("not_type=shorts passes everything except shorts", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeNotType, `{"type":["shorts"]}`)
		assert.True(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.True(t, g.FilterHook(newLiveInfo(test.NAME1, true, true, false)).Pass)
		assert.False(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
	})

	t.Run("type=live passes live and firstlive, blocks video and shorts", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType, `{"type":["live"]}`)

		// Live (actively streaming) passes.
		assert.True(t, g.FilterHook(newLiveInfo(test.NAME1, true, true, false)).Pass)
		// FirstLive (premiere/waiting) also passes — YtLive covers both.
		assert.True(t, g.FilterHook(&ConcernNotify{
			VideoInfo: &VideoInfo{
				UserInfo:    UserInfo{ChannelId: test.NAME1},
				VideoType:   VideoType_FirstLive,
				VideoStatus: VideoStatus_Waiting,
			},
		}).Pass)
		// Regular video / shorts do not pass.
		assert.False(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.False(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
	})

	t.Run("type=firstlive only passes premiere/waiting", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType, `{"type":["firstlive"]}`)

		assert.True(t, g.FilterHook(&ConcernNotify{
			VideoInfo: &VideoInfo{
				UserInfo:    UserInfo{ChannelId: test.NAME1},
				VideoType:   VideoType_FirstLive,
				VideoStatus: VideoStatus_Waiting,
			},
		}).Pass)
		// Active live is NOT a firstlive.
		assert.False(t, g.FilterHook(newLiveInfo(test.NAME1, true, true, false)).Pass)
		assert.False(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.False(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
	})

	t.Run("int type values work via ParseInt", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		// VideoType_Video = 2
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType, `{"type":["2"]}`)
		assert.True(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.False(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
	})

	t.Run("multiple type names OR together", func(t *testing.T) {
		g := &GroupConcernConfig{IConfig: &concern.GroupConcernConfig{}}
		g.GetGroupConcernFilter().SetRule(concern.FilterTypeType,
			`{"type":["video","shorts"]}`)
		assert.True(t, g.FilterHook(newNewsInfo(test.NAME1)).Pass)
		assert.True(t, g.FilterHook(newShortsInfo(test.NAME1)).Pass)
		assert.False(t, g.FilterHook(newLiveInfo(test.NAME1, true, true, false)).Pass)
	})
}
