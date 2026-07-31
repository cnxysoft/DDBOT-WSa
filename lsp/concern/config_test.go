package concern

import (
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGroupConcernAtConfig_CheckAtAll(t *testing.T) {
	var g *GroupConcernAtConfig
	assert.False(t, g.CheckAtAll(test.BibiliLive))

	g = &GroupConcernAtConfig{
		AtAll: test.BilibiliNews,
	}
	assert.True(t, g.CheckAtAll(test.BilibiliNews))
	assert.False(t, g.CheckAtAll(test.BibiliLive))
}

func TestNewGroupConcernConfigFromString(t *testing.T) {
	var testCase = []string{
		`{"group_concern_at":{"at_all":"bilibiliLive","at_someone":[{"ctype":"bilibiliLive", "at_list":[1,2,3,4,5]}]}}`,
	}
	var expected = []*GroupConcernConfig{
		{
			GroupConcernAt: GroupConcernAtConfig{
				AtAll: test.BibiliLive,
				AtSomeone: []*AtSomeone{
					{
						Ctype:  test.BibiliLive,
						AtList: []int64{1, 2, 3, 4, 5},
					},
				},
			},
		},
	}
	assert.Equal(t, len(testCase), len(expected))
	for i := 0; i < len(testCase); i++ {
		g, err := NewGroupConcernConfigFromString(testCase[i])
		assert.Nil(t, err)
		assert.EqualValues(t, expected[i], g)
	}
}

func TestGroupConcernConfig_ToString(t *testing.T) {
	var testCase = []*GroupConcernConfig{
		{
			GroupConcernAt: GroupConcernAtConfig{
				AtAll: test.BibiliLive,
				AtSomeone: []*AtSomeone{
					{
						Ctype:  test.BibiliLive,
						AtList: []int64{1, 2, 3, 4, 5},
					},
				},
			},
			GroupConcernNotify: GroupConcernNotifyConfig{
				TitleChangeNotify: test.BibiliLive,
				OfflineNotify:     test.DouyuLive,
			},
		},
	}
	var expected = []string{
		`{
			"group_concern_at":{
				"at_all":"bilibiliLive",
				"at_someone":[{"ctype":"bilibiliLive", "at_list":[1,2,3,4,5]}]
			},
			"group_concern_notify":{
				"title_change_notify": "bilibiliLive", "offline_notify": "douyuLive", "extend_notify": ""
			},
			"group_concern_filter": {
				"type": "", "config":"", "rules": null
			}
		}`,
	}
	assert.Equal(t, len(testCase), len(expected))
	for i := 0; i < len(testCase); i++ {
		assert.JSONEq(t, expected[i], testCase[i].ToString())
	}
}

func TestGroupConcernAtConfig_GetAtSomeoneList(t *testing.T) {
	var testCase = []*GroupConcernConfig{
		{
			GroupConcernAt: GroupConcernAtConfig{
				AtAll: test.BibiliLive,
				AtSomeone: []*AtSomeone{
					{
						Ctype:  test.BibiliLive,
						AtList: []int64{1, 2, 3, 4, 5},
					},
				},
			},
		},
		{
			GroupConcernAt: GroupConcernAtConfig{
				AtAll:     test.BibiliLive,
				AtSomeone: nil,
			},
		},
	}
	var expected = [][]int64{
		{1, 2, 3, 4, 5},
		nil,
	}
	assert.Equal(t, len(testCase), len(expected))
	for i := 0; i < len(testCase); i++ {
		assert.EqualValues(t, expected[i], testCase[i].GroupConcernAt.GetAtSomeoneList(test.BibiliLive))
	}

	var g *GroupConcernAtConfig
	assert.Nil(t, g.GetAtSomeoneList(test.BibiliLive))
}

func TestGroupConcernNotifyConfig_CheckTitleChangeNotify(t *testing.T) {
	var g = &GroupConcernNotifyConfig{
		TitleChangeNotify: concern_type.Empty.Add(test.BibiliLive, test.DouyuLive),
	}
	assert.True(t, g.CheckTitleChangeNotify(test.BibiliLive))
	assert.True(t, g.CheckTitleChangeNotify(test.DouyuLive))
	assert.False(t, g.CheckTitleChangeNotify(test.HuyaLive))
}

func TestGroupConcernAtConfig_ClearAtSomeoneList(t *testing.T) {
	var g = &GroupConcernAtConfig{
		AtAll: concern_type.Empty,
		AtSomeone: []*AtSomeone{
			{
				Ctype:  test.BibiliLive,
				AtList: []int64{1, 2, 3, 4},
			},
		},
	}
	g.ClearAtSomeoneList(test.DouyuLive)
	for i := 1; i <= 4; i++ {
		assert.Contains(t, g.GetAtSomeoneList(test.BibiliLive), int64(i))
	}
	g.ClearAtSomeoneList(test.BibiliLive)
	assert.Equal(t, 0, len(g.GetAtSomeoneList(test.BibiliLive)))
}

func TestGroupConcernAtConfig_RemoveAtSomeoneList(t *testing.T) {
	var g = &GroupConcernAtConfig{
		AtAll: concern_type.Empty,
		AtSomeone: []*AtSomeone{
			{
				Ctype:  test.BibiliLive,
				AtList: []int64{1, 2, 3, 4},
			},
		},
	}
	g.RemoveAtSomeoneList(test.DouyuLive, []int64{1, 2, 3, 4})
	assert.EqualValues(t, []int64{1, 2, 3, 4}, g.GetAtSomeoneList(test.BibiliLive))
	g.RemoveAtSomeoneList(test.BibiliLive, []int64{3})
	for i := 1; i <= 4; i++ {
		if i != 3 {
			assert.Contains(t, g.GetAtSomeoneList(test.BibiliLive), int64(i))
		}
	}
	assert.NotContains(t, g.GetAtSomeoneList(test.BibiliLive), int64(3))
}

func TestGroupConcernAtConfig_MergeAtSomeoneList(t *testing.T) {
	var g *GroupConcernAtConfig
	g.MergeAtSomeoneList("", nil)
	g.SetAtSomeoneList("", nil)
	g.RemoveAtSomeoneList("", nil)
	g.ClearAtSomeoneList("")
	g = &GroupConcernAtConfig{
		AtAll:     concern_type.Empty,
		AtSomeone: nil,
	}

	g.MergeAtSomeoneList(test.BibiliLive, []int64{1, 2, 3, 4})
	for i := 1; i <= 4; i++ {
		assert.Contains(t, g.GetAtSomeoneList(test.BibiliLive), int64(i))
	}
	g.MergeAtSomeoneList(test.BibiliLive, []int64{3, 4, 5})
	for i := 1; i <= 5; i++ {
		assert.Contains(t, g.GetAtSomeoneList(test.BibiliLive), int64(i))
	}
	assert.EqualValues(t, 0, len(g.GetAtSomeoneList(test.DouyuLive)))
}

func TestGroupConcernAtConfig_SetAtSomeoneList(t *testing.T) {
	var g = &GroupConcernAtConfig{
		AtAll:     concern_type.Empty,
		AtSomeone: nil,
	}

	g.SetAtSomeoneList(test.BibiliLive, []int64{1, 2, 3, 4})
	for i := 1; i <= 4; i++ {
		assert.Contains(t, g.GetAtSomeoneList(test.BibiliLive), int64(i))
	}

	g.SetAtSomeoneList(test.BibiliLive, []int64{5, 6})
	for i := 1; i <= 6; i++ {
		if i <= 4 {
			assert.NotContains(t, g.GetAtSomeoneList(test.BibiliLive), int64(i))
		} else {
			assert.Contains(t, g.GetAtSomeoneList(test.BibiliLive), int64(i))
		}
	}
}

func TestGroupConcernNotifyConfig_CheckOfflineNotify(t *testing.T) {
	var g = &GroupConcernNotifyConfig{
		OfflineNotify: concern_type.Empty.Add(test.BibiliLive, test.DouyuLive),
	}
	assert.True(t, g.CheckOfflineNotify(test.BibiliLive))
	assert.True(t, g.CheckOfflineNotify(test.DouyuLive))
	assert.False(t, g.CheckOfflineNotify(test.HuyaLive))
}

func TestGroupConcernFilterConfig_GetFilter(t *testing.T) {
	var g GroupConcernConfig
	assert.NotNil(t, g.GetGroupConcernNotify())
	assert.NotNil(t, g.GetGroupConcernAt())
	assert.NotNil(t, g.GetGroupConcernFilter())

	resType, err := g.GetGroupConcernFilter().GetFilterByType()
	assert.Nil(t, err)
	assert.Nil(t, resType)

	assert.True(t, g.GroupConcernFilter.Empty())

	g.GetGroupConcernFilter().Type = FilterTypeType
	g.GetGroupConcernFilter().Config = new(GroupConcernFilterConfigByType).ToString()

	_, err = g.GetGroupConcernFilter().GetFilterByType()
	assert.Nil(t, err)

	_, err = g.GetGroupConcernFilter().GetFilterByText()
	assert.NotNil(t, err)

	assert.False(t, g.GetGroupConcernFilter().Empty())

	g.GetGroupConcernFilter().Type = FilterTypeText
	g.GetGroupConcernFilter().Config = new(GroupConcernFilterConfigByText).ToString()

	_, err = g.GetGroupConcernFilter().GetFilterByText()
	assert.Nil(t, err)

	resType, err = g.GetGroupConcernFilter().GetFilterByType()
	assert.NotNil(t, resType)
	assert.Nil(t, err)

	assert.False(t, g.GetGroupConcernFilter().Empty())
}

func TestGroupConcernConfig_Validate(t *testing.T) {
	var g GroupConcernConfig
	assert.Nil(t, g.Validate())
	g.GetGroupConcernFilter().Type = FilterTypeType
	g.GetGroupConcernFilter().Config = "wrong"
	assert.NotNil(t, g.Validate())
}

func TestGroupConcernFilterConfig_ValidateTextConflict(t *testing.T) {
	textRule := func(words ...string) string {
		return (&GroupConcernFilterConfigByText{Text: words}).ToString()
	}

	// text 与 not_text 含相同关键词：必然全部拦截，应报错
	var g GroupConcernConfig
	g.GetGroupConcernFilter().SetRule(FilterTypeText, textRule("原神"))
	g.GetGroupConcernFilter().SetRule(FilterTypeNotText, textRule("原神", "旅人纪行"))
	err := g.Validate()
	assert.NotNil(t, err)
	assert.ErrorIs(t, err, ErrFilterRuleConflict)

	// not_text 词是 text 词的子串：包含 text 词的消息必然也含 not_text 词，应报错
	g.GetGroupConcernFilter().SetRule(FilterTypeText, textRule("原神星"))
	g.GetGroupConcernFilter().SetRule(FilterTypeNotText, textRule("原神"))
	err = g.Validate()
	assert.NotNil(t, err)
	assert.ErrorIs(t, err, ErrFilterRuleConflict)

	// 无交集：正常配置，应通过
	g.GetGroupConcernFilter().SetRule(FilterTypeText, textRule("原神"))
	g.GetGroupConcernFilter().SetRule(FilterTypeNotText, textRule("旅人纪行"))
	assert.Nil(t, g.Validate())

	// not_text 词是 text 词的超串：不含 not_text 词的 text 消息仍可通过，应通过
	g.GetGroupConcernFilter().SetRule(FilterTypeText, textRule("原神"))
	g.GetGroupConcernFilter().SetRule(FilterTypeNotText, textRule("原神星"))
	assert.Nil(t, g.Validate())

	// text 规则内任一关键词未被覆盖即可放行，应通过
	g.GetGroupConcernFilter().SetRule(FilterTypeText, textRule("原神", "星穹铁道"))
	g.GetGroupConcernFilter().SetRule(FilterTypeNotText, textRule("原神"))
	assert.Nil(t, g.Validate())

	// 仅 not_text：无矛盾，应通过
	var onlyDeny GroupConcernConfig
	onlyDeny.GetGroupConcernFilter().SetRule(FilterTypeNotText, textRule("原神"))
	assert.Nil(t, onlyDeny.Validate())

	// 非法的规则配置 JSON：应报错
	var broken GroupConcernConfig
	broken.GetGroupConcernFilter().SetRule(FilterTypeText, "wrong")
	assert.NotNil(t, broken.Validate())
}

type testInfo struct {
	isLive        bool
	living        bool
	titleChanged  bool
	statusChanged bool
	uid           int64
	groupCode     int64
	t             concern_type.Type
}

func (t *testInfo) Site() string {
	return testSite
}

func (t *testInfo) Type() concern_type.Type {
	if t.t.Empty() {
		return test.BibiliLive
	}
	return t.t
}

func (t *testInfo) GetUid() interface{} {
	return t.uid
}

func (t *testInfo) Logger() *logrus.Entry {
	return logrus.WithField("Site", t.Site())
}

func (t *testInfo) GetGroupCode() int64 {
	return t.groupCode
}

func (t *testInfo) ToMessage() *mmsg.MSG {
	return mmsg.NewMSG()
}

func (t *testInfo) TitleChanged() bool {
	return t.titleChanged
}

func (t *testInfo) IsLive() bool {
	return t.isLive
}

func (t *testInfo) Living() bool {
	return t.living
}

func (t *testInfo) LiveStatusChanged() bool {
	return t.statusChanged
}

func newLiveInfo(uid int64, living, liveStatusChanged, liveTitleChanged bool) *testInfo {
	return &testInfo{
		isLive:        true,
		living:        living,
		titleChanged:  liveTitleChanged,
		statusChanged: liveStatusChanged,
		uid:           uid,
	}
}

func TestGroupConcernConfig_ShouldSendHook(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	var notify = []Notify{
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
	}
	// 其他类型应该pass
	notify = append(notify, &testInfo{
		uid: test.UID2,
		t:   test.BilibiliNews,
	})
	var testCase = []*GroupConcernConfig{
		{},
		{
			GroupConcernNotify: GroupConcernNotifyConfig{
				TitleChangeNotify: test.BibiliLive,
			},
		},
		{

			GroupConcernNotify: GroupConcernNotifyConfig{
				OfflineNotify: test.BibiliLive,
			},
		},
		{
			GroupConcernNotify: GroupConcernNotifyConfig{
				OfflineNotify:     test.BibiliLive,
				TitleChangeNotify: test.BibiliLive,
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
			if index1 == 1 && index2 == 5 {
				result = g.ShouldSendHook(liveInfo)
			}
			assert.NotNil(t, result)
			assert.Equalf(t, expected[index1][index2], result.Pass, "%v and %v check fail", index1, index2)
		}
	}
}

func TestGroupConcernConfig_AtBeforeHook(t *testing.T) {
	var liveInfos = []Notify{
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
		&testInfo{
			t: test.BilibiliNews,
		},
	}
	var g = &GroupConcernConfig{}
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
}

func TestGroupConcernConfig_FilterHook(t *testing.T) {
	var g = &GroupConcernConfig{}
	result := g.FilterHook(newLiveInfo(test.UID1, true, true, true))
	assert.True(t, result.Pass)
}
