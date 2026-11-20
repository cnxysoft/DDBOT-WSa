package twitter

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"
)

func TestNitterTSRetweetQuoteAndMedia(t *testing.T) {
	body := `<title>Subscriber (@subscriber) | nitter</title>
<a class="profile-card-fullname">Subscriber</a><a class="profile-card-username">@subscriber</a>
<div class="timeline-item"><a class="tweet-link" href="/author/status/2119846862286930036"></a>
<div class="tweet-body"><div class="retweet-header">Subscriber retweeted</div>
<div class="tweet-header"><div class="fullname-and-username"><a class="fullname">Author</a><a class="username">@author</a></div></div>
<div class="tweet-content">First line<br><br>Third line</div>
<div class="attachments"><div class="gallery-row mixed-row">
<a class="attachment still-image"><img src="https://pbs.twimg.com/photo.jpg"></a>
<div class="attachment video-container"><video data-url="https://video.twimg.com/main.m3u8"></video></div>
</div></div>
<blockquote class="quote quote-big"><a class="quote-link" href="/quoted/status/2119800000000000000"></a>
<div class="fullname-and-username"><a class="fullname">Quoted Author</a><a class="username">@quoted</a></div>
<div class="quote-text">Quoted<br>text</div><div class="quote-media-container">
<div class="attachments media-gif"><div class="attachment video-container"><video muted loop><source src="https://video.twimg.com/quote.mp4"></video></div></div>
</div></blockquote></div></div>`
	profile, posts, challenge, err := ParseResp([]byte(body), "https://nitterts.pages.dev/subscriber")
	require.NoError(t, err)
	require.Nil(t, challenge)
	require.Equal(t, "subscriber", profile.ScreenName)
	require.Equal(t, "Subscriber", profile.Name)
	require.Len(t, posts, 1)
	post := posts[0]
	require.Equal(t, "First line\n\nThird line", post.Content)
	require.True(t, post.IsRetweet)
	require.Equal(t, "Author", post.OrgUser.Name)
	require.Equal(t, "author", post.OrgUser.ScreenName)
	require.Equal(t, "https://x.com/author/status/2119846862286930036", post.Url)
	require.False(t, post.CreatedAt.IsZero(), "missing titles must fall back to the tweet ID timestamp")
	require.Equal(t, []*Media{{Type: "image", Url: "https://pbs.twimg.com/photo.jpg"},
		{Type: "video(m3u8)", Url: "https://video.twimg.com/main.m3u8"}}, post.Media)
	require.NotNil(t, post.QuoteTweet)
	require.Equal(t, "Quoted Author", post.QuoteTweet.OrgUser.Name)
	require.Equal(t, "quoted", post.QuoteTweet.OrgUser.ScreenName)
	require.Equal(t, "Quoted\ntext", post.QuoteTweet.Content)
	require.False(t, post.QuoteTweet.CreatedAt.IsZero())
	require.Equal(t, []*Media{{Type: "gif", Url: "https://video.twimg.com/quote.mp4"}}, post.QuoteTweet.Media)
}

func TestMirrorMediaPlaybackVariants(t *testing.T) {
	for _, tc := range []struct {
		name, html, kind, url string
	}{
		{"upstream gif", `<video class="gif"><source src="/gif.mp4"></video>`, "gif", "/gif.mp4"},
		{"nitterts gif", `<video loop><source src="/gif.mp4"></video>`, "gif", "/gif.mp4"},
		{"mp4", `<video controls><source src="/video.mp4"></video>`, "video", "/video.mp4"},
		{"direct video", `<video src="/video.mp4"></video>`, "video", "/video.mp4"},
		{"hls", `<video data-url="/video.m3u8?token=sample"><source src="/fallback.mp4"></video>`, "video(m3u8)", "/video.m3u8?token=sample"},
		{"poster is not another image", `<div class="gallery-video"><img src="/cover.jpg"><video><source src="/video.mp4"></video></div>`, "video", "/video.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tc.html))
			require.NoError(t, err)
			require.Equal(t, []*Media{{Type: tc.kind, Url: tc.url}}, parseMirrorMedia(doc.Selection))
		})
	}
}
