package twitter

import (
	"testing"

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
