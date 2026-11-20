package twitter

import (
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type filterTestNotify struct {
	content string
}

func (n *filterTestNotify) Site() string {
	return Site
}

func (n *filterTestNotify) Type() concern_type.Type {
	return Tweets
}

func (n *filterTestNotify) GetUid() interface{} {
	return "test"
}

func (n *filterTestNotify) Logger() *logrus.Entry {
	return logrus.WithField("Site", Site)
}

func (n *filterTestNotify) GetGroupCode() int64 {
	return 1
}

func (n *filterTestNotify) ToMessage() *mmsg.MSG {
	return mmsg.NewText(n.content)
}

func TestGroupConcernConfigFilterHookUsesTextRules(t *testing.T) {
	tests := []struct {
		name     string
		ruleType string
		keyword  string
		content  string
		wantPass bool
	}{
		{
			name:     "text未命中时拦截",
			ruleType: concern.FilterTypeText,
			keyword:  "壁紙配布",
			content:  "普通活动公告",
			wantPass: false,
		},
		{
			name:     "text命中时放行",
			ruleType: concern.FilterTypeText,
			keyword:  "壁紙配布",
			content:  "壁紙配布のお知らせ",
			wantPass: true,
		},
		{
			name:     "not_text命中时拦截",
			ruleType: concern.FilterTypeNotText,
			keyword:  "中奖",
			content:  "恭喜中奖",
			wantPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseConfig := new(concern.GroupConcernConfig)
			baseConfig.GetGroupConcernFilter().SetRule(tt.ruleType,
				(&concern.GroupConcernFilterConfigByText{Text: []string{tt.keyword}}).ToString())
			config := NewGroupConcernConfig(baseConfig, nil)

			result := config.FilterHook(&filterTestNotify{content: tt.content})

			assert.Equal(t, tt.wantPass, result.Pass)
		})
	}
}

func TestGroupConcernConfigFilterHookUsesDynamicTwitterText(t *testing.T) {
	originalMode := TwitterMode
	TwitterMode = ModeAPI
	defer func() { TwitterMode = originalMode }()

	createdAt := time.Date(2026, time.August, 9, 8, 30, 0, 0, time.UTC)
	newsInfo := &NewsInfo{
		UserInfo: &UserInfo{Id: "publisher_id", Name: "订阅作者"},
		Tweet: &Tweet{
			ID:        "123456",
			Content:   "正文关键词",
			CreatedAt: createdAt,
			IsRetweet: true,
			Url:       "https://x.com/publisher_id/status/123456",
			OrgUser: &UserProfile{
				Name:       "被转发作者",
				ScreenName: "retweeted_id",
			},
			QuoteTweet: &Tweet{
				Content:   "引用正文",
				CreatedAt: createdAt.Add(-time.Hour),
				OrgUser: &UserProfile{
					Name:       "引用作者",
					ScreenName: "quoted_id",
				},
			},
		},
	}
	notify := NewConcernNewsNotify(1, newsInfo, nil)

	tests := []struct {
		name     string
		ruleType string
		keyword  string
		wantPass bool
	}{
		{name: "正文参与匹配", ruleType: concern.FilterTypeText, keyword: "正文关键词", wantPass: true},
		{name: "订阅作者参与匹配", ruleType: concern.FilterTypeText, keyword: "订阅作者", wantPass: true},
		{name: "账号名参与匹配", ruleType: concern.FilterTypeText, keyword: "publisher_id", wantPass: true},
		{name: "推文URL参与匹配", ruleType: concern.FilterTypeText, keyword: "x.com/publisher_id/status", wantPass: true},
		{name: "转发作者参与匹配", ruleType: concern.FilterTypeText, keyword: "被转发作者", wantPass: true},
		{name: "引用正文参与匹配", ruleType: concern.FilterTypeText, keyword: "引用正文", wantPass: true},
		{name: "引用账号名参与匹配", ruleType: concern.FilterTypeText, keyword: "quoted_id", wantPass: true},
		{name: "固定模板文字不参与匹配", ruleType: concern.FilterTypeText, keyword: "发布了新推文", wantPass: false},
		{name: "固定模板文字不触发排除", ruleType: concern.FilterTypeNotText, keyword: "转发了", wantPass: true},
		{name: "动态作者仍可触发排除", ruleType: concern.FilterTypeNotText, keyword: "被转发作者", wantPass: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseConfig := new(concern.GroupConcernConfig)
			baseConfig.GetGroupConcernFilter().SetRule(tt.ruleType,
				(&concern.GroupConcernFilterConfigByText{Text: []string{tt.keyword}}).ToString())
			config := NewGroupConcernConfig(baseConfig, nil)

			result := config.FilterHook(notify)

			assert.Equal(t, tt.wantPass, result.Pass)
			assert.Empty(t, newsInfo.dynamic.Content, "过滤阶段不应提前渲染并缓存模板数据")
		})
	}
}
