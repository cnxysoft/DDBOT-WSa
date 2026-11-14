package qqofficial

// 富媒体部分失败的单测（P1）。
//
// 缺陷形态：sendToWithEvent 的富媒体循环里，上传/发送失败且「前面已经发出过内容」时，
// 只打一条 Error 然后 break，最后 return nil —— 上层以为整条消息发出去了。
// 同时 firstErr 这个变量从来没被赋值过，if firstErr != nil 是死代码。
//
// 约束（别把修复做坏）：不能因为返回错误就让上层重发整条消息，正文就重复了，
// 所以错误文案里必须写明「已发出的部分不重发」。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// partialFailPlatform 是「按序号失败」的假平台：用来造「第一条成功、第二条失败」。
//
//   - allowMediaSends：允许成功发出去几条富媒体（msg_type=7），之后的都回 500；
//   - failUploadFrom：第几次上传（POST /files）开始回 500，0 表示都不失败。
type partialFailPlatform struct {
	*httptest.Server

	allowMediaSends int
	failUploadFrom  int

	mu             sync.Mutex
	mediaAttempts  int
	uploadAttempts int
	sends          []eventIDFakeSend
}

func newPartialFailPlatform(t *testing.T, allowMediaSends, failUploadFrom int) *partialFailPlatform {
	t.Helper()
	p := &partialFailPlatform{allowMediaSends: allowMediaSends, failUploadFrom: failUploadFrom}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", ContentTypeJSON)
		switch {
		case strings.HasSuffix(r.URL.Path, PathGetAppAccessToken):
			_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			p.mu.Lock()
			p.uploadAttempts++
			attempt := p.uploadAttempts
			p.mu.Unlock()
			if p.failUploadFrom > 0 && attempt >= p.failUploadFrom {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"上传失败（假平台）"}`))
				return
			}
			_, _ = w.Write([]byte(`{"file_uuid":"uuid-1","file_info":"FILEINFO_PART","ttl":86400}`))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			var req sendMessageRequest
			_ = json.Unmarshal(raw, &req)
			status := http.StatusOK
			if req.MsgType == 7 {
				p.mu.Lock()
				p.mediaAttempts++
				attempt := p.mediaAttempts
				p.mu.Unlock()
				if attempt > p.allowMediaSends {
					status = http.StatusInternalServerError
				}
			}
			p.mu.Lock()
			p.sends = append(p.sends, eventIDFakeSend{
				path:    r.URL.Path,
				msgType: req.MsgType,
				content: req.Content,
				status:  status,
				raw:     string(raw),
			})
			p.mu.Unlock()
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"网关内部错误（假平台）"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"MSG_PART","timestamp":"2026-11-12T19:30:00+08:00"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *partialFailPlatform) sent() []eventIDFakeSend {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]eventIDFakeSend(nil), p.sends...)
}

func (p *partialFailPlatform) dump() string {
	var b strings.Builder
	for i, s := range p.sent() {
		b.WriteString("[")
		b.WriteString(string(rune('0' + i)))
		b.WriteString("] msg_type=")
		b.WriteString(string(rune('0' + s.msgType)))
		b.WriteString(" status=")
		b.WriteString(string(rune('0' + s.status/100)))
		b.WriteString("xx ")
		b.WriteString(s.raw)
		b.WriteString(" ")
	}
	return b.String()
}

// twoImagesWithText 是「正文 + 两张图」：第一条媒体带正文（textInline），第二条是纯媒体。
func twoImagesWithText() []adapter.MessageSegment {
	return []adapter.MessageSegment{
		{Type: "text", Data: map[string]interface{}{"text": testBodyText}},
		{Type: "image", Data: map[string]interface{}{"url": "https://example.com/1.png", "name": "1.png"}},
		{Type: "image", Data: map[string]interface{}{"url": "https://example.com/2.png", "name": "2.png"}},
	}
}

// TestMediaSendPartialFailureReturnsError：第 2 条富媒体发送失败，必须返回错误。
func TestMediaSendPartialFailureReturnsError(t *testing.T) {
	p := newPartialFailPlatform(t, 1, 0)
	a := newSendTestAdapter(t, p.Server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	_, err := a.SendGroupMessage(groupID, twoImagesWithText())
	if err == nil {
		t.Fatalf("第 2 条富媒体发送失败时不能返回 nil（上层会以为整条消息发出去了）。"+
			"假平台收到 %d 条请求：%s", len(p.sent()), p.dump())
	}
	for _, want := range []string{"第 2/2 个媒体", "已成功发出 1 条", "已发出的部分不重发"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("部分失败的错误文案里要写清 %q，实际：%v", want, err)
		}
	}
}

// TestMediaUploadPartialFailureReturnsError：上传失败同样要报错（不是只打日志）。
func TestMediaUploadPartialFailureReturnsError(t *testing.T) {
	p := newPartialFailPlatform(t, 5, 2)
	a := newSendTestAdapter(t, p.Server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	_, err := a.SendGroupMessage(groupID, twoImagesWithText())
	if err == nil {
		t.Fatalf("第 2 个媒体上传失败时不能返回 nil。假平台收到 %d 条请求：%s", len(p.sent()), p.dump())
	}
	for _, want := range []string{"上传失败", "已成功发出 1 条", "已发出的部分不重发"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("部分失败的错误文案里要写清 %q，实际：%v", want, err)
		}
	}
}
