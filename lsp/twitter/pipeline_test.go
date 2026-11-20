package twitter

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Capture the actual adapter packets without opening an IM connection.
type pipelineCapture struct {
	adapter.Adapter
	packets [][]adapter.MessageSegment
}

func (c *pipelineCapture) IsConnected() bool { return true }
func (c *pipelineCapture) SendGroupMessage(_ int64, message interface{}) (int32, error) {
	segments, ok := message.([]adapter.MessageSegment)
	if !ok {
		return 0, fmt.Errorf("unexpected packet %T", message)
	}
	c.packets = append(c.packets, segments)
	return int32(len(c.packets)), nil
}

func pipelineMediaBytes(t *testing.T, segment adapter.MessageSegment) []byte {
	t.Helper()
	file, ok := segment.Data["file"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(file, "base64://"), "media must be downloaded, got %s", file)
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(file, "base64://"))
	require.NoError(t, err)
	require.NotEmpty(t, data)
	return data
}

func capturePipelineMessage(t *testing.T, notify *ConcernNewsNotify) ([][]adapter.MessageSegment, string, int, int) {
	t.Helper()
	msg := notify.ToMessage()
	require.NotNil(t, msg)
	capture := &pipelineCapture{}
	messenger := &adapter.Messenger{Adapter: capture}
	messenger.Online.Store(true)
	for _, sending := range msg.ToMessage(mmsg.NewGroupTarget(123)) {
		result := messenger.SendGroupMessage(123, sending, "pipeline regression")
		require.NoError(t, result.Error)
	}
	var text strings.Builder
	images, videos := 0, 0
	for _, packet := range capture.packets {
		for _, segment := range packet {
			switch segment.Type {
			case "text":
				content, ok := segment.Data["text"].(string)
				require.True(t, ok)
				text.WriteString(content)
			case "image":
				data := pipelineMediaBytes(t, segment)
				decoded, _, err := image.Decode(bytes.NewReader(data))
				require.NoError(t, err)
				assert.Equal(t, image.Rect(0, 0, 64, 64), decoded.Bounds())
				images++
			case "video":
				data := pipelineMediaBytes(t, segment)
				assert.Contains(t, string(data[:min(len(data), 32)]), "ftyp", "must be MP4")
				for _, box := range []string{"moov", "mdat", "avc1"} {
					assert.Contains(t, string(data), box)
				}
				assert.Len(t, packet, 1, "each video must be sent separately")
				videos++
			default:
				t.Errorf("unexpected segment type %q", segment.Type)
			}
		}
	}
	return capture.packets, text.String(), images, videos
}

func assertPipelineAnimation(t *testing.T, packets [][]adapter.MessageSegment) {
	t.Helper()
	for _, packet := range packets {
		for _, segment := range packet {
			if segment.Type == "image" {
				animation, err := gif.DecodeAll(bytes.NewReader(pipelineMediaBytes(t, segment)))
				require.NoError(t, err)
				assert.Greater(t, len(animation.Image), 1, "GIF must keep its animation")
				return
			}
		}
	}
	t.Fatal("missing GIF image")
}

func assertTextAfterVideo(t *testing.T, packets [][]adapter.MessageSegment, text string) {
	t.Helper()
	seenVideo := false
	for _, packet := range packets {
		for _, segment := range packet {
			if segment.Type == "video" {
				seenVideo = true
			}
			if seenVideo && segment.Type == "text" {
				if content, _ := segment.Data["text"].(string); strings.Contains(content, text) {
					return
				}
			}
		}
	}
	t.Fatalf("missing text after video: %s", text)
}

func TestMirrorNotificationPipeline(t *testing.T) {
	requirePipelineBackend(t)
	fixtures, err := filepath.Abs("testdata/pipeline")
	require.NoError(t, err)
	require.DirExists(t, fixtures)
	t.Chdir(t.TempDir())
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	var fresh atomic.Bool
	var failed atomic.Bool
	var afterFailover atomic.Bool
	item := func(id, header, content, attachments, quote string) string {
		return fmt.Sprintf(`<div class="timeline-item"><a class="tweet-link" href="/subscriber/status/%s#m"></a><div class="tweet-body">%s<div class="tweet-header"><a class="fullname">Original Author</a><a class="username">@original</a><span class="tweet-date"><a title="Nov 9, 2026 · 5:37 PM UTC"></a></span></div><div class="tweet-content">%s</div><div class="attachments">%s</div>%s</div></div>`, id, header, content, attachments, quote)
	}
	cases := []struct {
		name, id       string
		images, videos int
		quoted         bool
	}{
		{"text", "2119846862286930901", 0, 0, false},
		{"image", "2119846862286930902", 1, 0, false},
		{"mp4", "2119846862286930903", 0, 1, false},
		{"hls", "2119846862286930904", 0, 1, false},
		{"gif", "2119846862286930905", 1, 0, false},
		{"retweet", "2119846862286930906", 1, 0, false},
		{"quote-image", "2119846862286930907", 2, 0, true},
		{"video-with-quote", "2119846862286930908", 1, 1, true},
		{"quote-video", "2119846862286930909", 1, 1, true},
		{"two-videos", "2119846862286930910", 0, 2, false},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			http.StripPrefix("/assets/", http.FileServer(http.Dir(fixtures))).ServeHTTP(w, r)
			return
		}
		origin := "http://" + r.Host
		image := func() string { return `<img src="` + origin + `/assets/image.png">` }
		video := func(file string) string { return `<video src="` + origin + `/assets/` + file + `"></video>` }
		quote := func(media string) string {
			return `<div class="quote-big"><a class="quote-link" href="/quoted/status/2119800000000000000#m"></a><div class="fullname-and-username"><a class="fullname">Quoted Author</a><a class="username">@quoted</a></div><span class="tweet-date"><a title="Nov 9, 2026 · 4:00 PM UTC"></a></span><div class="quote-text">Quoted content<br>Second quote line</div><div class="quote-media-container">` + media + `</div></div>`
		}
		var feed strings.Builder
		feed.WriteString(`<html><head><meta property="og:title" content="Subscriber (@subscriber)"></head><body>`)
		feed.WriteString(item("2119846862286930900", "", "Existing post", "", ""))
		if fresh.Load() {
			for _, c := range cases {
				header, attachments, quoted := "", "", ""
				switch c.name {
				case "image":
					attachments = image()
				case "mp4":
					attachments = video("source.mp4")
				case "hls":
					attachments = video("source.m3u8")
				case "gif":
					attachments = `<div class="media-gif">` + video("source.mp4") + `</div>`
				case "retweet":
					header = `<div class="retweet-header">Subscriber retweeted</div>`
					attachments = image()
				case "quote-image":
					attachments, quoted = image(), quote(image())
				case "video-with-quote":
					attachments, quoted = video("source.mp4"), quote(image())
				case "quote-video":
					attachments, quoted = image(), quote(video("source.mp4"))
				case "two-videos":
					attachments = video("source.mp4") + video("source.mp4")
				}
				feed.WriteString(item(c.id, header, c.name+`<br>Second line`, attachments, quoted))
			}
		}
		if afterFailover.Load() {
			feed.WriteString(item("2119846862286930911", "", "New post after failover", "", ""))
		}
		feed.WriteString(`</body></html>`)
		fmt.Fprint(w, feed.String())
	}
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed.Load() && r.URL.Path == "/subscriber" {
			http.Error(w, "bad gateway", 502)
			return
		}
		handler(w, r)
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(handler))
	defer backup.Close()
	useTestMirrors(t, primary.URL, backup.URL)
	oldMode := TwitterMode
	TwitterMode = ModeMirror
	t.Cleanup(func() { TwitterMode = oldMode })
	tc := newConcern(nil)
	tc.StateManager.FreshIndex(123)
	_, err = tc.Add(nil, 123, "subscriber", Tweets)
	require.NoError(t, err)
	events, err := tc.freshNewsInfo(Tweets, "subscriber")
	require.NoError(t, err)
	require.Empty(t, events, "watch must premark old posts")
	fresh.Store(true)
	events, err = tc.freshNewsInfo(Tweets, "subscriber")
	require.NoError(t, err)
	require.Len(t, events, len(cases))
	for i, event := range events {
		c := cases[i]
		t.Run(c.name, func(t *testing.T) {
			news := event.(*NewsInfo)
			require.Equal(t, c.id, news.Tweet.ID)
			packets, content, images, videos := capturePipelineMessage(t, NewConcernNewsNotify(123, news, tc))
			assert.Contains(t, content, c.name+"\nSecond line")
			assert.Contains(t, content, "Subscriber")
			if c.name != "retweet" {
				assert.Contains(t, content, "2026-11-10 01:37:00")
			}
			assert.Equal(t, 1, strings.Count(content, news.Tweet.Url), "original URL must appear exactly once")
			assert.Equal(t, c.images, images)
			assert.Equal(t, c.videos, videos)
			if c.name == "retweet" {
				assert.Contains(t, content, "转发了Original Author")
			}
			if c.name == "gif" {
				assertPipelineAnimation(t, packets)
			}
			if c.name == "video-with-quote" {
				assertTextAfterVideo(t, packets, "Quoted content")
			}
			if c.quoted {
				assert.Contains(t, content, "引用了Quoted Author")
				assert.Contains(t, content, "Quoted content\nSecond quote line")
				assert.Contains(t, content, "2026-11-10 00:00:00")
			}
		})
	}
	events, err = tc.freshNewsInfo(Tweets, "subscriber")
	require.NoError(t, err)
	require.Empty(t, events, "same poll must not push twice")
	failed.Store(true)
	events, err = tc.freshNewsInfo(Tweets, "subscriber")
	require.NoError(t, err)
	require.Empty(t, events, "switching mirror must not push twice")
	require.Equal(t, uint64(1), tc.mirrorNext.Load())
	afterFailover.Store(true)
	events, err = tc.freshNewsInfo(Tweets, "subscriber")
	require.NoError(t, err)
	require.Len(t, events, 1, "backup must deliver new posts as well as suppress duplicates")
	news := events[0].(*NewsInfo)
	require.Equal(t, "2119846862286930911", news.Tweet.ID)
	_, content, _, _ := capturePipelineMessage(t, NewConcernNewsNotify(123, news, tc))
	assert.Contains(t, content, "New post after failover")
	events, err = tc.freshNewsInfo(Tweets, "subscriber")
	require.NoError(t, err)
	require.Empty(t, events)
}
