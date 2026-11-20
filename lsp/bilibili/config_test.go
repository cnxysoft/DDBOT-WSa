package bilibili

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/stretchr/testify/assert"
)

func newLiveInfo(uid int64, living bool, liveStatusChanged bool, liveTitleChanged bool) *ConcernLiveNotify {
	notify := &ConcernLiveNotify{
		LiveInfo: &LiveInfo{
			UserInfo: UserInfo{
				Mid: uid,
			},
			liveStatusChanged: liveStatusChanged,
			liveTitleChanged:  liveTitleChanged,
		},
	}
	if living {
		notify.Status = LiveStatus_Living
	} else {
		notify.Status = LiveStatus_NoLiving
	}
	return notify
}

func newNewsInfo(uid int64, cardTypes ...DynamicDescType) []*ConcernNewsNotify {

	var result []*ConcernNewsNotify
	for _, t := range cardTypes {
		notify := &ConcernNewsNotify{
			UserInfo: &UserInfo{
				Mid: uid,
			},
			concern: NewConcern(concern.GetNotifyChan()),
		}
		notify.Card = NewCacheCard(&Card{
			Desc: &Card_Desc{
				Type: t,
			},
		})
		result = append(result, notify)
	}
	return result
}

func TestNewGroupConcernConfig(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := initConcern(t)

	g := c.GetGroupConcernConfig(test.G1, test.UID1)

	assert.NotNil(t, g)
	assert.Nil(t, g.Validate())

	g.GetGroupConcernFilter().Type = concern.FilterTypeNotType
	g.GetGroupConcernFilter().Config = (&concern.GroupConcernFilterConfigByType{Type: []string{"q", "a"}}).ToString()

	assert.NotNil(t, g.Validate())
	g.GetGroupConcernFilter().Config = "wrong"
	assert.NotNil(t, g.Validate())
	g.GetGroupConcernFilter().Config = ""
	g.GetGroupConcernFilter().Type = ""
	assert.Nil(t, g.Validate())

	g = c.GetGroupConcernConfig(test.G1, test.UID1)
	err := c.OperateGroupConcernConfig(test.G1, test.UID1, g, func(concernConfig concern.IConfig) bool {
		concernConfig.GetGroupConcernFilter().Type = concern.FilterTypeNotType
		concernConfig.GetGroupConcernFilter().Config = (&concern.GroupConcernFilterConfigByType{Type: []string{"wrong"}}).ToString()
		return true
	})
	assert.NotNil(t, err)

	g = c.GetGroupConcernConfig(test.G1, test.UID1)
	err = c.OperateGroupConcernConfig(test.G1, test.UID1, g, func(concernConfig concern.IConfig) bool {
		concernConfig.GetGroupConcernFilter().Type = concern.FilterTypeNotType
		concernConfig.GetGroupConcernFilter().Config = (&concern.GroupConcernFilterConfigByType{Type: []string{Tougao}}).ToString()
		return true
	})
	assert.Nil(t, err)
}

func TestGroupConcernConfig_ShouldSendHook(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	var notify = []concern.Notify{
		// 下播状态 什么也没变 不推
		newLiveInfo(test.UID1, false, false, false),
		// 下播状态 标题变了 不推
		newLiveInfo(test.UID1, false, false, true),
		// 下播了 检查配置
		newLiveInfo(test.UID1, false, true, false),
		// 下播了 检查配置
		newLiveInfo(test.UID1, false, true, true),
		// 直播状态 什么也没变 不推
		newLiveInfo(test.UID1, true, false, false),
		// 直播状态 改了标题 检查配置
		newLiveInfo(test.UID1, true, false, true),
		// 开播了 推
		newLiveInfo(test.UID1, true, true, false),
		// 开播了改了标题 推
		newLiveInfo(test.UID1, true, true, true),
		// 无法处理news，应该pass
		newNewsInfo(test.UID1, DynamicDescType_TextOnly)[0],
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
		{
			IConfig: &concern.GroupConcernConfig{
				GroupConcernNotify: concern.GroupConcernNotifyConfig{
					OfflineNotify: Live,
				},
			},
		},
		{
			IConfig: &concern.GroupConcernConfig{
				GroupConcernNotify: concern.GroupConcernNotifyConfig{
					OfflineNotify:     Live,
					TitleChangeNotify: Live,
				},
			},
		},
	}
	var expected = [][]bool{
		{
			false, false, false, false,
			false, false, true, true,
			true,
		},
		{
			false, false, false, false,
			false, true, true, true,
			true,
		},
		{
			false, false, true, true,
			false, false, true, true,
			true,
		},
		{
			false, false, true, true,
			false, true, true, true,
			true,
		},
	}
	assert.Equal(t, len(expected), len(testCase))
	for index1, g := range testCase {
		assert.Equal(t, len(expected[index1]), len(notify))
		for index2, liveInfo := range notify {
			result := g.ShouldSendHook(liveInfo)
			assert.NotNil(t, result)
			assert.Equalf(t, expected[index1][index2], result.Pass, "%v and %v check fail", index1, index2)
		}
	}
}

func TestGroupConcernConfig_AtBeforeHook(t *testing.T) {
	var liveInfos = []concern.Notify{
		// 下播状态 什么也没变 不推
		newLiveInfo(test.UID1, false, false, false),
		// 下播状态 标题变了 不推
		newLiveInfo(test.UID1, false, false, true),
		// 下播了 检查配置
		newLiveInfo(test.UID1, false, true, false),
		// 下播了 检查配置
		newLiveInfo(test.UID1, false, true, true),
		// 直播状态 什么也没变 不推
		newLiveInfo(test.UID1, true, false, false),
		// 直播状态 改了标题 检查配置
		newLiveInfo(test.UID1, true, false, true),
		// 开播了 推
		newLiveInfo(test.UID1, true, true, false),
		// 开播了改了标题 推
		newLiveInfo(test.UID1, true, true, true),
		// news 默认pass
		newNewsInfo(test.UID1, DynamicDescType_TextOnly)[0],
	}
	var g = NewGroupConcernConfig(new(concern.GroupConcernConfig), NewConcern(concern.GetNotifyChan()))
	var expected = []bool{
		false, false, false, false,
		false, false, true, true,
		true,
	}
	assert.Equal(t, len(expected), len(liveInfos))
	for index, liveInfo := range liveInfos {
		result := g.AtBeforeHook(liveInfo)
		assert.Equalf(t, expected[index], result.Pass, "%v check fail", index)
	}

	g.concern.unsafeStart.Store(true)
	for index, liveInfo := range liveInfos {
		result := g.AtBeforeHook(liveInfo)
		assert.Equalf(t, false, result.Pass, "%v check fail", index)
	}
}

func TestGroupConcernConfig_NewsFilterHook(t *testing.T) {
	var notifies = newNewsInfo(test.UID1, DynamicDescType_WithOrigin, DynamicDescType_WithImage, DynamicDescType_TextOnly)
	var g = NewGroupConcernConfig(new(concern.GroupConcernConfig), nil)

	// 默认应该不过滤
	for _, notify := range notifies {
		assert.True(t, g.FilterHook(notify).Pass)
	}

	var typeFilter = []*concern.GroupConcernFilterConfigByType{
		{
			Type: []string{
				Zhuanfa,
			},
		},
		{
			Type: []string{
				Tupian,
			},
		},
		{
			Type: []string{
				Tupian, Wenzi,
			},
		},
		{
			Type: []string{
				Zhibofenxiang,
			},
		},
	}

	var expectedType = [][]DynamicDescType{
		{
			DynamicDescType_WithOrigin,
		},
		{
			DynamicDescType_WithImage,
		},
		{
			DynamicDescType_WithImage, DynamicDescType_TextOnly,
		},
		nil,
	}

	var expectedNotType = [][]DynamicDescType{
		{
			DynamicDescType_WithImage, DynamicDescType_TextOnly,
		},
		{
			DynamicDescType_WithOrigin, DynamicDescType_TextOnly,
		},
		{
			DynamicDescType_WithOrigin,
		},
		{
			DynamicDescType_WithOrigin, DynamicDescType_WithImage, DynamicDescType_TextOnly,
		},
	}

	testFn := func(index int, tp string, expected []DynamicDescType) {
		notifies := newNewsInfo(test.UID1, DynamicDescType_WithOrigin, DynamicDescType_WithImage, DynamicDescType_TextOnly)
		var g = NewGroupConcernConfig(&concern.GroupConcernConfig{
			GroupConcernFilter: concern.GroupConcernFilterConfig{
				Type:   tp,
				Config: typeFilter[index].ToString(),
			},
		}, nil)
		assert.Nil(t, g.Validate())

		var resultType []DynamicDescType
		for _, notify := range notifies {
			hookResult := g.FilterHook(notify)
			if hookResult.Pass {
				resultType = append(resultType, notify.Card.GetDesc().GetType())
			}
		}
		assert.EqualValues(t, expected, resultType)
	}

	for index := range typeFilter {
		testFn(index, concern.FilterTypeType, expectedType[index])
		testFn(index, concern.FilterTypeNotType, expectedNotType[index])
	}

	live := newLiveInfo(test.UID1, true, false, false)
	g.FilterHook(live)
}

func TestCheckTypeDefine(t *testing.T) {
	result := CheckTypeDefine([]string{"invalid", Zhuanlan, "1024", "0", "9"})
	assert.Len(t, result, 3)
	assert.EqualValues(t, []string{"invalid", "0", "9"}, result)
}

func TestGroupConcernConfig_NotifyBeforeCallback(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := initConcern(t)

	_, err := c.GetNotifyMsg(test.G1, test.BVID1)
	assert.True(t, localdb.IsNotFound(err))

	var notify = newNewsInfo(test.UID1, DynamicDescType_WithOrigin)[0]
	notify.Card.Desc.OrigDyIdStr = test.BVID1

	var g = new(GroupConcernConfig)
	g.concern = c
	g.NotifyBeforeCallback(notify)
	assert.False(t, notify.shouldCompact)
	g.NotifyBeforeCallback(notify)
	assert.True(t, notify.shouldCompact)

	notify = newNewsInfo(test.UID1, DynamicDescType_WithVideo)[0]
	notify.Card.Desc.Bvid = test.BVID2

	g.NotifyBeforeCallback(notify)
	assert.False(t, notify.shouldCompact)
	g.NotifyBeforeCallback(notify)
	assert.True(t, notify.shouldCompact)

	notify = newNewsInfo(test.UID1, DynamicDescType_TextOnly)[0]
	notify.Card.Desc.DynamicIdStr = strconv.FormatInt(test.DynamicID1, 10)

	g.NotifyBeforeCallback(notify)
	assert.False(t, notify.shouldCompact)
	g.NotifyBeforeCallback(notify)
	assert.False(t, notify.shouldCompact)

	live := newLiveInfo(test.UID1, true, false, false)
	g.NotifyBeforeCallback(live)
}

func newFilteredCompactVideo(c *Concern, groupCode int64, bvid, action string) (*ConcernNewsNotify, *GroupConcernConfig) {
	notify := newNewsInfo(test.UID1, DynamicDescType_WithVideo)[0]
	notify.GroupCode = groupCode
	notify.concern = c
	notify.Card.Desc.Bvid = bvid
	notify.Card.Desc.UserProfile = &Card_Desc_UserProfile{
		Info: &Card_Desc_UserProfile_Info{Uname: "投稿用户"},
	}
	notify.Card.Display = &Card_Display{
		UsrActionTxt: action,
		AddOnCardInfo: []*Card_Display_AddOnCardInfo{
			{AddOnCardShowType: AddOnCardShowType_match},
		},
	}

	baseConfig := new(concern.GroupConcernConfig)
	baseConfig.GetGroupConcernFilter().SetRule(
		concern.FilterTypeNotText,
		(&concern.GroupConcernFilterConfigByText{Text: []string{"never-match"}}).ToString(),
	)
	return notify, NewGroupConcernConfig(baseConfig, c)
}

func TestGroupConcernConfig_CompactHitAfterFilterCache(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	template.InitTemplateLoader()

	c := initConcern(t)
	notify, g := newFilteredCompactVideo(c, test.G1, test.BVID1, "联合投稿了视频")

	assert.Nil(t, c.SetGroupCompactMarkIfNotExist(test.G1, test.BVID1))
	assert.NoError(t, c.SetNotifyMsg(test.BVID1, &adapter.GroupMessage{
		ID:        1,
		GroupCode: test.G1,
		Elements: []adapter.IMessageElement{
			&adapter.TextSegment{Content: "原消息"},
		},
	}))
	assert.True(t, g.FilterHook(notify).Pass)
	fullMessage := notify.Card.msgCache
	assert.NotNil(t, fullMessage)
	assert.Len(t, notify.Card.dynamic.Addons, 1)

	g.NotifyBeforeCallback(notify)
	assert.True(t, notify.shouldCompact)
	compactMessage := notify.ToMessage()
	assert.False(t, notify.Card.compactMiss)
	assert.NotSame(t, fullMessage, notify.Card.msgCache)
	assert.Len(t, notify.Card.dynamic.Addons, 1)

	content := msgstringer.AdapterMsgToString(compactMessage.Elements())
	assert.Contains(t, content, "投稿用户联合投稿了视频：")
	assert.NotContains(t, content, "投稿用户转发了的视频：")
}

func TestGroupConcernConfig_CompactMissAfterFilterCache(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	template.InitTemplateLoader()

	c := initConcern(t)
	notify, g := newFilteredCompactVideo(c, test.G1, test.BVID1, "发布了动态")

	assert.Nil(t, c.SetGroupCompactMarkIfNotExist(test.G1, test.BVID1))
	assert.True(t, g.FilterHook(notify).Pass)
	fullMessage := notify.Card.msgCache
	assert.NotNil(t, fullMessage)
	assert.Len(t, notify.Card.dynamic.Addons, 1)

	g.NotifyBeforeCallback(notify)
	assert.True(t, notify.shouldCompact)
	compactMessage := notify.ToMessage()
	assert.True(t, notify.Card.compactMiss)
	assert.NotSame(t, fullMessage, notify.Card.msgCache)
	assert.Len(t, notify.Card.dynamic.Addons, 1)

	content := msgstringer.AdapterMsgToString(compactMessage.Elements())
	assert.Contains(t, content, "投稿用户发布了动态视频：")
	assert.NotContains(t, content, "转发了的动态")
}

func TestCompactMissTemplateSuppressesCommonFooter(t *testing.T) {
	template.InitTemplateLoader()

	var dynamic DynamicInfo
	dynamic.Type = DynamicDescType_WithVideo
	dynamic.User.Name = "联合投稿用户"
	dynamic.Video.Action = "联合投稿了视频"
	dynamic.DynamicUrl = "https://t.bilibili.com/test"

	addon := Addon{Type: AddOnCardShowType(1)}
	addon.Goods.Name = "不应出现的附加卡片"
	dynamic.Addons = []Addon{addon}
	dynamic.Detail.Reserve.Title = "不应出现的预约"
	dynamic.Detail.Vote = &VoteInfo{Title: "不应出现的投票"}

	msg, err := template.LoadAndExec("notify.group.bilibili.news.tmpl", map[string]interface{}{
		"dynamic":      dynamic,
		"msg":          nil,
		"compact_miss": true,
		"group_code":   test.G1,
		"parse_post":   false,
		"dynamic_raw":  nil,
	})
	assert.NoError(t, err)
	content := msgstringer.AdapterMsgToString(msg.Elements())
	assert.Contains(t, content, "联合投稿用户联合投稿了视频")
	assert.Contains(t, content, dynamic.DynamicUrl)
	assert.NotContains(t, content, "转发了的动态")
	assert.NotContains(t, content, addon.Goods.Name)
	assert.NotContains(t, content, dynamic.Detail.Reserve.Title)
	assert.NotContains(t, content, dynamic.Detail.Vote.Title)
}

func TestGroupConcernConfig_NotifyAfterCallback(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	c := initConcern(t)

	_, err := c.GetNotifyMsg(test.G1, test.BVID1)
	assert.True(t, localdb.IsNotFound(err))

	var notify = newNewsInfo(test.UID1, DynamicDescType_WithOrigin)[0]
	notify.compactKey = test.BVID1
	var msg = &adapter.GroupMessage{
		ID:        1,
		GroupCode: test.G1,
		Elements: []adapter.IMessageElement{
			&adapter.TextSegment{Content: "asd"},
		},
	}
	var g = new(GroupConcernConfig)
	g.concern = c

	g.NotifyAfterCallback(notify, msg)

	msg2, err := c.GetNotifyMsg(test.G1, test.BVID1)
	assert.Nil(t, err)
	assert.EqualValues(t, msg, msg2)

	notify.shouldCompact = true
	g.NotifyAfterCallback(notify, msg)

	live := newLiveInfo(test.UID1, true, false, false)
	g.NotifyAfterCallback(live, nil)
}

func TestConfigJson(t *testing.T) {
	var g = NewGroupConcernConfig(&concern.GroupConcernConfig{
		GroupConcernFilter: concern.GroupConcernFilterConfig{},
	}, nil)
	fmt.Println(json.MarshalToString(g))

}
