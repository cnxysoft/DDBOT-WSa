package twitter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToMessage_ErrorHandling(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "media unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	useTestMirrors(t, server.URL)
	// 下载失败时保留媒体 URL，视频仍需单独分段。
	tweet := Tweet{
		ID:      "1234567890",
		Content: "正文",
		Url:     "https://x.com/testuser/status/1234567890",
		Media: []*Media{
			{Url: server.URL + "/image.png", Type: "image"},
			{Url: server.URL + "/video.m3u8", Type: "video(m3u8)"},
		},
	}

	userInfo := &UserInfo{
		Id:   "1234567890",
		Name: "testuser",
	}

	notify := &ConcernNewsNotify{
		GroupCode: 123456,
		NewsInfo: &NewsInfo{
			UserInfo: userInfo,
			Tweet:    &tweet,
		},
	}

	msg := notify.ToMessage()
	require.NotNil(t, msg)
	packets := msg.ToMessage(mmsg.NewGroupTarget(notify.GroupCode))
	require.Len(t, packets, 2)
	require.Len(t, packets[0].Elements, 3)
	assert.Contains(t, packets[0].Elements[0].(*adapter.TextSegment).Content, tweet.Content)
	assert.Equal(t, tweet.Media[0].Url, packets[0].Elements[1].(*adapter.ImageSegment).File)
	assert.Contains(t, packets[0].Elements[2].(*adapter.TextSegment).Content, tweet.Url)
	require.Len(t, packets[1].Elements, 1)
	assert.Equal(t, tweet.Media[1].Url, packets[1].Elements[0].(*adapter.VideoSegment).Url)
}

func TestDecodeURIComponent(t *testing.T) {
	cases := []struct {
		encoded string
		decoded string
	}{
		// 单次编码
		{"/pic/%E6%B5%8B%E8%AF%95", "/pic/测试"},
		// 二次编码
		{"%252Fpic%252Ftest", "/pic/test"},
		// 混合编码
		{"%2Fpic%252F%25E6%2588%2591", "/pic/我"},
	}

	for _, c := range cases {
		result, err := processMediaURL(c.encoded)
		if assert.NoError(t, err) {
			assert.Equal(t, c.decoded, result)
		}
	}
}
