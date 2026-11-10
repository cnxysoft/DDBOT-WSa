package twitter

import (
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/stretchr/testify/require"
)

func TestMirrorTemplateFieldsForRetweetQuoteAndVideo(t *testing.T) {
	body := `<html><head><meta property="og:title" content="订阅作者 (@subscriber)"></head><body>
<div class="timeline-item"><a class="tweet-link" href="/original/status/2119846862286930036#m"></a>
<div class="tweet-body"><div class="retweet-header">订阅作者 retweeted</div>
<div class="tweet-header"><div class="fullname-and-username"><a class="fullname">原作者</a><a class="username">@original</a></div>
<span class="tweet-date"><a title="Nov 9, 2026 · 5:37 PM UTC"></a></span></div>
<div class="tweet-content">原文正文</div><div class="attachments"><div class="gallery-video"><video data-url="https://video.twimg.com/test.m3u8"></video></div></div>
<div class="quote quote-big"><a class="quote-link" href="/quoted/status/2119800000000000000#m"></a>
<div class="fullname-and-username"><a class="fullname">引用作者</a><a class="username">@quoted</a></div>
<span class="tweet-date"><a title="Nov 9, 2026 · 4:00 PM UTC"></a></span>
<div class="quote-text">引用正文</div><div class="quote-media-container"><div class="attachment"><img src="https://pbs.twimg.com/media/test.jpg"></div></div></div>
</div></div></body></html>`
	profile, posts, challenge, err := ParseResp([]byte(body), "https://nitter.click/subscriber")
	require.NoError(t, err)
	require.Nil(t, challenge)
	require.Len(t, posts, 1)
	post := posts[0]
	require.Equal(t, "原作者", post.OrgUser.Name)
	require.Equal(t, "original", post.OrgUser.ScreenName)
	require.Equal(t, "原文正文", post.Content)
	require.Equal(t, "https://x.com/original/status/2119846862286930036", post.Url)
	require.Equal(t, RETWEET, post.RtType())
	require.Len(t, post.Media, 1)
	require.Equal(t, "video(m3u8)", post.Media[0].Type)
	require.NotNil(t, post.QuoteTweet)
	quote := post.QuoteTweet
	require.Equal(t, "引用作者", quote.OrgUser.Name)
	require.Equal(t, "引用正文", quote.Content)
	require.False(t, quote.CreatedAt.IsZero())
	require.Len(t, quote.Media, 1)

	// Execute the actual template with the parsed fields, without downloading media.
	dynamic := TwitterDynamic{
		User: TwitterUser{Name: profile.Name, Id: profile.ScreenName}, Type: post.RtType(),
		Content: post.Content, Date: CSTTime(post.CreatedAt).Format(time.DateTime), Url: post.Url,
		Retweet:   TwitterRetweet{User: TwitterUser{Name: post.OrgUser.Name, Id: post.OrgUser.ScreenName}},
		WithQuote: true,
		Quote: TwitterQuote{User: TwitterUser{Name: quote.OrgUser.Name, Id: quote.OrgUser.ScreenName},
			Content: quote.Content, Date: CSTTime(quote.CreatedAt).Format(time.DateTime),
			Media: []TwitterMedia{{Type: "image", Url: quote.Media[0].Url}}},
	}
	content, err := template.DefaultTemplateContent("notify.group.twitter.news")
	require.NoError(t, err)
	preview, err := template.RenderPreview(content, map[string]interface{}{"dynamic": dynamic})
	require.NoError(t, err)
	require.Contains(t, preview, "订阅作者 转发了原作者")
	require.Contains(t, preview, "引用了引用作者")
	require.Contains(t, preview, "原文正文")
	require.Contains(t, preview, "引用正文")
	require.Contains(t, preview, "2026-11-10 00:00:00")
	require.Contains(t, preview, post.Url)
	require.Contains(t, preview, "[img]")

	dynamic.Media = []TwitterMedia{{Type: post.Media[0].Type, Url: post.Media[0].Url}}
	videoPreview, err := template.RenderPreview(content, map[string]interface{}{"dynamic": dynamic})
	require.NoError(t, err)
	require.Contains(t, videoPreview, post.Url)
	require.Contains(t, videoPreview, "[元素]")
	dynamic.ShouldCompact = true
	preview, err = template.RenderPreview(content, map[string]interface{}{"dynamic": dynamic})
	require.NoError(t, err)
	require.Contains(t, preview, "订阅作者转发了原作者的推文")
}
