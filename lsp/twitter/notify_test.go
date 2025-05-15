package twitter

import (
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
	"github.com/stretchr/testify/assert"
	"testing"
)

// 可以添加更多边界条件测试
func TestToMessage_ErrorHandling(t *testing.T) {
	// 测试无效MirrorHost的情况
	tweet := Tweet{
		ID:         "1234567890",
		MirrorHost: "nitter.net",
		Media: []*Media{
			{Url: "/pic/media%2FGqbFhUuW0AAVRk4.jpg%3Fname%3Dsmall%26format%3Dwebp", Type: "image"},
			{Url: "/video/CDBABB50751BD/https%3A%2F%2Fvideo.twimg.com%2Famplify_video%2F1920766235832172544%2Fpl%2FFNAXoec5cT40vEaG.m3u8", Type: "video(m3u8)"},
		},
	}

	userInfo := &UserInfo{
		Id:              "1234567890",
		Name:            "testuser",
		ProfileImageUrl: "https://example.com/profile.jpg",
	}

	notify := &NewNotify{
		groupCode: 123456,
		NewsInfo: &NewsInfo{
			UserInfo: userInfo,
			Tweet:    tweet,
		},
	}

	pool := local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{
		{
			Type:  proxy_pool.PreferOversea,
			Proxy: "127.0.0.1:7897",
		},
	})
	proxy, err := pool.Get(proxy_pool.PreferOversea)
	if err != nil {
		t.Fatalf("Failed to get proxy: %v", err)
	}
	proxy_pool.Init(pool)

	msg := notify.ToMessage()
	// 验证在这种情况下不会panic，并且媒体元素为空
	assert.Len(t, msg.Elements(), 3) // 只有文本元素存在

	pool.Delete(proxy.ProxyString())
	pool.Stop()
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
