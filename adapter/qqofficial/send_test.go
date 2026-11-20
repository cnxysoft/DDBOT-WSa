package qqofficial

// 发送路的单测：起一个假平台（httptest，回环地址，不联网、不碰真机），把 /messages
// 的请求体全部录下来，断言「正文只出现一次」和「@ 语义不许丢」。
//
// 为什么必须录请求体：正文重复是**两条独立请求**造成的，只在 sendTo 内部断言看不出来，
// 必须数平台真正收到几条、每条里有没有正文。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// fakeSendRecord 是一条被假平台收到的消息发送请求。
type fakeSendRecord struct {
	path     string
	msgType  int
	content  string
	markdown string
	fileInfo string
	msgID    string
}

// body 返回这条请求里能看到的全部文本（content + markdown），用来数「正文出现了几次」。
func (r fakeSendRecord) body() string { return r.content + r.markdown }

type fakeQQPlatform struct {
	server *httptest.Server

	mu        sync.Mutex
	sends     []fakeSendRecord
	rawBodies []string
	badBodies []string
}

// newFakeQQPlatform 起一个只认三条路径的假平台：取 token / 发消息 / 上传媒体。
func newFakeQQPlatform(t *testing.T) *fakeQQPlatform {
	t.Helper()
	// 同上：进程级 @全体 冷却必须按轮清干净，否则 -count=N 会串。
	resetAtAllCooldowns()
	p := &fakeQQPlatform{}
	p.server = httptest.NewServer(http.HandlerFunc(p.handle))
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeQQPlatform) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	w.Header().Set("Content-Type", ContentTypeJSON)

	switch {
	case strings.HasSuffix(path, PathGetAppAccessToken):
		_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":7200}`))
	case strings.HasSuffix(path, "/files"):
		_, _ = w.Write([]byte(`{"file_uuid":"uuid-1","file_info":"FILEINFO_TEST","ttl":86400}`))
	case strings.HasSuffix(path, "/messages"):
		var req struct {
			MsgType  int    `json:"msg_type"`
			Content  string `json:"content"`
			Markdown *struct {
				Content string `json:"content"`
			} `json:"markdown"`
			Media *struct {
				FileInfo string `json:"file_info"`
			} `json:"media"`
			MsgID string `json:"msg_id"`
		}
		rec := fakeSendRecord{path: path}
		if err := json.Unmarshal(raw, &req); err != nil {
			p.mu.Lock()
			p.badBodies = append(p.badBodies, string(raw))
			p.mu.Unlock()
		} else {
			rec.msgType, rec.content, rec.msgID = req.MsgType, req.Content, req.MsgID
			if req.Markdown != nil {
				rec.markdown = req.Markdown.Content
			}
			if req.Media != nil {
				rec.fileInfo = req.Media.FileInfo
			}
		}
		p.mu.Lock()
		p.sends = append(p.sends, rec)
		p.rawBodies = append(p.rawBodies, string(raw))
		p.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"MSG_FAKE_1","timestamp":"2026-11-11T16:35:34+08:00","ext_info":{"ref_idx":"REFIDX_FAKE_1"}}`))
	default:
		// bot_state / info 之类（后台自解析任务会打）：给空对象，别让它报错刷屏。
		_, _ = w.Write([]byte(`{}`))
	}
}

func (p *fakeQQPlatform) sent() []fakeSendRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]fakeSendRecord(nil), p.sends...)
}

func (p *fakeQQPlatform) bad() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.badBodies...)
}

func (p *fakeQQPlatform) rawList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.rawBodies...)
}

// newSendTestAdapter 建一个把 API 打到假平台的 adapter（token / 发消息 / 上传都走它）。
func newSendTestAdapter(t *testing.T, baseURL string) *QQOfficialAdapter {
	t.Helper()
	config.GlobalConfig.Set(ConfigKeyPrefix+"."+ConfigKeyAPIBaseURL, baseURL)
	t.Cleanup(func() { config.GlobalConfig.Set(ConfigKeyPrefix+"."+ConfigKeyAPIBaseURL, "") })
	return newTestAdapter(t)
}

// countBodyContaining 数「有多少条请求里含这段文本」。
func countBodyContaining(sends []fakeSendRecord, want string) int {
	n := 0
	for _, s := range sends {
		if strings.Contains(s.body(), want) {
			n++
		}
	}
	return n
}

const testBodyText = "B站开播了：测试直播间"

// 正文 + 一张图的推送里，正文在群里只能出现一次。
//
// 错误的做法：先独立发一条纯文本（msg_type=0，content=正文），再把同一段正文塞进第一条
// 富媒体的 content（msg_type=7）—— 两条请求都带正文，群里就重复了。
func TestQQOfficialTextWithImageSendsBodyOnce(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{
		{Type: "text", Data: map[string]interface{}{"text": testBodyText}},
		{Type: "image", Data: map[string]interface{}{"url": "https://example.com/cover.png", "name": "cover.png"}},
	}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if bad := p.bad(); len(bad) != 0 {
		t.Fatalf("请求体不是合法 JSON：%v", bad)
	}

	sends := p.sent()
	if n := countBodyContaining(sends, testBodyText); n != 1 {
		t.Fatalf("正文必须在平台侧只出现一次，实际出现 %d 次（共 %d 条请求）：%v", n, len(sends), p.rawList())
	}

	// 纯文本正文（无 @ 标记）并入第一条富媒体消息：被动名额只吃 1 个（群上限 5 次）。
	if len(sends) != 1 {
		t.Fatalf("正文 + 1 张图应只发 1 条（正文并入媒体条），实际 %d 条：%v", len(sends), p.rawList())
	}
	only := sends[0]
	if only.fileInfo != "FILEINFO_TEST" {
		t.Fatalf("这一条必须是富媒体消息（带 media.file_info），实际 %+v", only)
	}
	if only.msgType != MsgTypeMedia {
		t.Errorf("msg_type 应为 %d（富媒体），实际 %d", MsgTypeMedia, only.msgType)
	}
	if only.content != testBodyText {
		t.Errorf("富媒体条的 content 应带上正文 %q，实际 %q", testBodyText, only.content)
	}
}

// 多张媒体时正文也只能出现一次（第一条媒体带正文，其余不带）。
func TestQQOfficialTextWithMultipleImagesSendsBodyOnce(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{
		{Type: "text", Data: map[string]interface{}{"text": testBodyText}},
		{Type: "image", Data: map[string]interface{}{"url": "https://example.com/1.png", "name": "1.png"}},
		{Type: "image", Data: map[string]interface{}{"url": "https://example.com/2.png", "name": "2.png"}},
	}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	sends := p.sent()
	if n := countBodyContaining(sends, testBodyText); n != 1 {
		t.Fatalf("正文必须在平台侧只出现一次，实际出现 %d 次（共 %d 条请求）：%v", n, len(sends), p.rawList())
	}
	if len(sends) != 2 {
		t.Fatalf("正文 + 2 张图应发 2 条（2 张媒体 + 正文并入第一条），实际 %d 条：%v", len(sends), p.rawList())
	}
	for i, s := range sends {
		if s.fileInfo == "" {
			t.Errorf("第 %d 条应是富媒体消息，实际 %+v", i, s)
		}
	}
	if sends[0].content != testBodyText {
		t.Errorf("第一条媒体应带正文 %q，实际 %q", testBodyText, sends[0].content)
	}
	if sends[1].content != "" {
		t.Errorf("第二条媒体不该再带正文，实际 %q", sends[1].content)
	}
}

// 有 @ 标记时必须走 markdown 单独成条：群里 @用户 只在 markdown 消息里渲染，
// 富媒体消息（msg_type=7）的 content 是纯文本，标记塞进去会原样显示成代码。
//
// 修之前：正文（markdown 条 + 媒体条的纯文本 content）发了两遍，且媒体那份丢了 @。
func TestQQOfficialMarkdownWithImageKeepsMentionAndBodyOnce(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)
	userID := a.ids.ToInt64(testAppID, testOwnerOpen)

	const text = "开播了"
	msg := []adapter.MessageSegment{
		{Type: "at", Data: map[string]interface{}{"qq": float64(userID)}},
		{Type: "text", Data: map[string]interface{}{"text": text}},
		{Type: "image", Data: map[string]interface{}{"url": "https://example.com/cover.png", "name": "cover.png"}},
	}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	sends := p.sent()

	wantAt := `<qqbot-at-user id="` + testOwnerOpen + `" />`
	if n := countBodyContaining(sends, wantAt); n != 1 {
		t.Fatalf("@ 标记必须恰好出现在 1 条 markdown 消息里，实际 %d 次（共 %d 条请求）：%v", n, len(sends), p.rawList())
	}
	if len(sends) != 2 {
		t.Fatalf("@（markdown）+ 1 张图应发 2 条：markdown 正文一条 + 媒体一条，实际 %d 条：%v", len(sends), p.rawList())
	}

	var md, media *fakeSendRecord
	for i := range sends {
		switch {
		case sends[i].markdown != "":
			md = &sends[i]
		case sends[i].fileInfo != "":
			media = &sends[i]
		}
	}
	if md == nil {
		t.Fatalf("必须有一条 markdown 正文（msg_type=2），实际 %v", p.rawList())
	}
	if md.msgType != MsgTypeMarkdown {
		t.Errorf("带 @ 的正文 msg_type 应为 %d（markdown），实际 %d", MsgTypeMarkdown, md.msgType)
	}
	if !strings.Contains(md.markdown, wantAt) || !strings.Contains(md.markdown, text) {
		t.Errorf("markdown 正文应同时含 @ 标记与正文，实际 %q", md.markdown)
	}
	if media == nil {
		t.Fatalf("必须有一条富媒体消息，实际 %v", p.rawList())
	}
	if media.content != "" {
		t.Errorf("媒体条不许再带一遍正文（纯文本 content 会让正文重复且 @ 变成代码），实际 %q", media.content)
	}
	if strings.Contains(media.markdown, "qqbot-at") {
		t.Errorf("媒体条不许带 markdown 形态的 @（msg_type=7 不渲染它），实际 %q", media.markdown)
	}
	if got := countBodyContaining(sends, text); got != 1 {
		// 正文只该出现在 markdown 那一条里（媒体条 content 为空 = 不再重复一遍）
		t.Errorf("正文应只出现在 markdown 条里（媒体条应为空），实际命中 %d 条请求：%v", got, p.rawList())
	}
}

// 只有媒体、没有正文时不许凭空塞 content。
func TestQQOfficialMediaOnlySendsNoContent(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{
		{Type: "image", Data: map[string]interface{}{"url": "https://example.com/only.png", "name": "only.png"}},
	}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	sends := p.sent()
	if len(sends) != 1 || sends[0].content != "" || sends[0].fileInfo == "" {
		t.Fatalf("只发媒体时应是 1 条不带 content 的富媒体消息，实际 %v", p.rawList())
	}
}

// 一条消息里带 6 个以上媒体：一个都不许丢。
//
// 修之前：adapter.go 有个 maxMediaPerMessage = 5，第 6 个起直接被丢掉，只打一句 Warn ——
// 用户看到的是一组图少了几张，而且没有任何提示。官方一条消息只有一个 media 字段，
// 多出来的本来就是拆成多条发；被动回复的次数限制由 message.go 的被动账本负责降级转主动，
// 不需要在 media 收集这一段再截一次。
func TestQQOfficialManyMediaAllSent(t *testing.T) {
	const mediaCount = 7

	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := make([]adapter.MessageSegment, 0, mediaCount)
	for i := 0; i < mediaCount; i++ {
		msg = append(msg, adapter.MessageSegment{
			Type: "image",
			Data: map[string]interface{}{
				"url":  fmt.Sprintf("https://example.com/%d.png", i+1),
				"name": fmt.Sprintf("%d.png", i+1),
			},
		})
	}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	sends := p.sent()
	if len(sends) != mediaCount {
		t.Fatalf("%d 个媒体必须发 %d 条（一个都不许丢），实际 %d 条：%v",
			mediaCount, mediaCount, len(sends), p.rawList())
	}
	for i, s := range sends {
		if s.fileInfo == "" {
			t.Errorf("第 %d 条没有 media.file_info，媒体被漏掉了：%+v", i+1, s)
		}
	}
}
