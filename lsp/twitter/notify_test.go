package twitter

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureTwitterLogs(t *testing.T) *logtest.Hook {
	t.Helper()
	oldLogger := logger
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.SetLevel(logrus.DebugLevel)
	hook := logtest.NewLocal(log)
	logger = log.WithField("module", "twitter-concern")
	t.Cleanup(func() { logger = oldLogger })
	return hook
}

func requireMediaWarnings(t *testing.T, hook *logtest.Hook, mediaURL string, types ...string) {
	t.Helper()
	parsed, err := url.Parse(mediaURL)
	require.NoError(t, err)
	var warnings []*logrus.Entry
	for _, entry := range hook.AllEntries() {
		assert.NotContains(t, entry.Data, "stack", "ordinary media errors must not include a stack")
		assert.NotContains(t, entry.Message, "文件读取失败", "failed downloads must not read an empty path")
		assert.Greater(t, entry.Level, logrus.ErrorLevel, "ordinary failures should be warnings")
		if entry.Level == logrus.WarnLevel {
			warnings = append(warnings, entry)
		}
	}
	require.Len(t, warnings, len(types), "one warning per failed media")
	for i, entry := range warnings {
		assert.Equal(t, "media", entry.Data["stage"])
		assert.Equal(t, parsed.Host, entry.Data["media_host"])
		assert.Equal(t, types[i], entry.Data["media_type"])
		assert.Contains(t, entry.Data, "error")
		assert.Contains(t, entry.Data, "duration_ms")
	}
}

func TestToMessage_ErrorHandling(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "media unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	useTestMirrors(t, server.URL)
	hook := captureTwitterLogs(t)
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
	requireMediaWarnings(t, hook, server.URL, "image", "mp4")
}

func TestBuildTwitterDynamic_QuotedMediaFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	useTestMirrors(t, server.URL)
	hook := captureTwitterLogs(t)
	notify := &ConcernNewsNotify{NewsInfo: &NewsInfo{
		UserInfo: &UserInfo{Id: "subscriber", Name: "Subscriber"},
		Tweet: &Tweet{
			Media: []*Media{{Url: server.URL + "/image.png", Type: "image"}},
			QuoteTweet: &Tweet{
				OrgUser: &UserProfile{ScreenName: "quoted", Name: "Quoted Author"},
				Media: []*Media{
					{Url: server.URL + "/gif.mp4", Type: "gif"},
					{Url: server.URL + "/video.m3u8", Type: "video(m3u8)"},
				},
			},
		},
	}}
	dynamic := notify.buildTwitterDynamic()
	require.Len(t, dynamic.Media, 1)
	require.Len(t, dynamic.Quote.Media, 2)
	assert.Equal(t, notify.Tweet.Media[0].Url, dynamic.Media[0].Url)
	assert.Empty(t, dynamic.Media[0].Base64)
	for i, media := range dynamic.Quote.Media {
		assert.Equal(t, notify.Tweet.QuoteTweet.Media[i].Url, media.Url)
		assert.Empty(t, media.Base64)
	}
	requireMediaWarnings(t, hook, server.URL, "image", "gif", "mp4")
	files, err := os.ReadDir("tmp")
	require.NoError(t, err)
	assert.Empty(t, files, "failed conversions must not leave partial files")
}

func TestFallbackMediaFailureLogsOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	useTestMirrors(t, server.URL)
	hook := captureTwitterLogs(t)
	msg := mmsg.NewMSG().Text("正文\n")
	addedURL := false
	addMedia(&Tweet{
		ID: "123", Url: "https://x.com/subscriber/status/123",
		Media: []*Media{
			{Url: server.URL + "/image.png", Type: "image"},
			{Url: server.URL + "/gif.mp4", Type: "gif"},
			{Url: server.URL + "/video.m3u8", Type: "video(m3u8)"},
		},
	}, msg, true, &addedURL)
	requireMediaWarnings(t, hook, server.URL, "image", "gif", "mp4")
}

func TestImageSizeFallbackLogsFinalResult(t *testing.T) {
	for _, succeeds := range []bool{false, true} {
		t.Run(map[bool]string{false: "all fail", true: "large succeeds"}[succeeds], func(t *testing.T) {
			t.Chdir(t.TempDir())
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if succeeds && r.URL.Query().Get("name") == "large" {
					w.Header().Set("Content-Type", "image/png")
					w.Write([]byte("image fixture"))
					return
				}
				http.Error(w, "unavailable", http.StatusBadGateway)
			}))
			defer server.Close()
			useTestMirrors(t, server.URL)
			hook := captureTwitterLogs(t)
			path, err := tryDownloadBestImage(server.URL + "/image.png?name=orig")
			if succeeds {
				require.NoError(t, err)
				assert.FileExists(t, path)
				assert.Equal(t, int32(2), requests.Load())
				requireMediaWarnings(t, hook, server.URL)
				entry := hook.LastEntry()
				require.NotNil(t, entry)
				assert.Equal(t, logrus.DebugLevel, entry.Level)
				assert.Equal(t, int64(len("image fixture")), entry.Data["bytes"])
			} else {
				require.Error(t, err)
				assert.Empty(t, path)
				assert.Equal(t, int32(3), requests.Load())
				requireMediaWarnings(t, hook, server.URL, "image")
			}
		})
	}
}

func TestDownloadMedia_DirectoryFailureLogsOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	useTestMirrors(t, "http://127.0.0.1")
	require.NoError(t, os.WriteFile("tmp", []byte("not a directory"), 0600))
	hook := captureTwitterLogs(t)
	path, err := downloadMedia("http://127.0.0.1/video.mp4", false)
	require.Error(t, err)
	assert.Empty(t, path)
	requireMediaWarnings(t, hook, "http://127.0.0.1", "mp4")
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
