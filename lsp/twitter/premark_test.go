package twitter

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/stretchr/testify/assert"
)

// TestResolveTwitterMode twitter.mode 缺省必须回退 mirror，
// 与历史版本兼容：存量用户配置里没有这个键时不应被静默切到 API 模式。
func TestResolveTwitterMode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "未配置缺省回退mirror", in: "", want: ModeMirror},
		{name: "显式api", in: "api", want: ModeAPI},
		{name: "显式mirror", in: "mirror", want: ModeMirror},
		{name: "未知值回退mirror", in: "whatever", want: ModeMirror},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveTwitterMode(tt.in))
		})
	}
}

// TestPreMarkUserTweetsMarksExisting 预标记的核心语义：预拉到的存量推文
// 逐条写入 MarkTweetId 后，同一批推文再次进入 filterTweet 时应被去重跳过。
// UserTweets 的真实拉取走生产路径验证，这里锁标记生效行为。
func TestPreMarkUserTweetsMarksExisting(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	tc := &twitterConcern{
		StateManager: &StateManager{
			StateManager: concern.NewStateManagerWithStringID(Site, nil),
			ExtraKey:     new(ExtraKey),
		},
	}

	tweets := []*Tweet{
		{ID: "111", Content: "a"},
		{ID: "222", Content: "b"},
	}
	for _, tweet := range tweets {
		tc.filterTweet(tweet)
	}

	// 首次订阅预标记完成后，下一轮轮询再拉到同一批推文应全部被去重
	assert.False(t, tc.filterTweet(&Tweet{ID: "111"}), "存量推文111应被标记去重")
	assert.False(t, tc.filterTweet(&Tweet{ID: "222"}), "存量推文222应被标记去重")
	assert.True(t, tc.filterTweet(&Tweet{ID: "333"}), "新推文333应正常放行")
}
