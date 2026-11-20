package qqofficial

// @全体（<qqbot-at-everyone />）与正文同一条发出去的形态单测。
//
// 缺陷形态：buildOutbound 把「所有」at 标记都只写进 markdown，于是 md != plain
// ⇒ MsgType=2 单独发一条正文，媒体再逐条发（msg_type=7，content 为空）
// ⇒ 群里看到的是「文本一条、图片一条」，一次推送拆成两条。
//
// 依据（哪条是实测、哪条是保守）：
//   - <qqbot-at-everyone />：实测 2026-11-12 用 {"content":"<qqbot-at-everyone /> …","msg_type":0}
//     发到测试群 http=200，实测确认「确实是全体级艾特」；官方能力表也写
//     「@全部成员：群聊 文本消息、markdown 消息」。所以它进纯文本是安全的。
//   - <qqbot-at-user id="…" />：官方能力表只写「@用户：群聊 markdown 消息」，我们没有
//     纯文本渲染的实证（参考实现注释记「纯文本里会显示成代码原文」）。保守：仍只进 markdown。
//
// 全部走 httptest 假平台（回环地址），不联网、不碰真机。

import (
	"context"
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

const atAllBodyText = "B站开播了：测试直播间"

// atAllSegment / atUserSegment 造 at 段：前者目标 "all"，后者是具体 openid。
func atAllSegment() adapter.MessageSegment {
	return adapter.MessageSegment{Type: "at", Data: map[string]interface{}{"qq": "all"}}
}

func atUserSegment(openID string) adapter.MessageSegment {
	return adapter.MessageSegment{Type: "at", Data: map[string]interface{}{"openid": openID}}
}

func textSegment(text string) adapter.MessageSegment {
	return adapter.MessageSegment{Type: "text", Data: map[string]interface{}{"text": text}}
}

func imageSegment(name string) adapter.MessageSegment {
	return adapter.MessageSegment{Type: "image", Data: map[string]interface{}{"url": "https://example.com/" + name, "name": name}}
}

// 主症状：@全体 + 正文 + 1 张图必须是**一条**富媒体消息，正文与标签都在它的 content 里。
func TestQQOfficialAtEveryoneWithImageSendsOneMessage(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{atAllSegment(), textSegment(atAllBodyText), imageSegment("cover.png")}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if bad := p.bad(); len(bad) != 0 {
		t.Fatalf("请求体不是合法 JSON：%v", bad)
	}

	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("带 @全体 的推送应只发 1 条（正文与标记并入媒体条），实际 %d 条：%v", len(sends), p.rawList())
	}
	only := sends[0]
	if only.msgType != MsgTypeMedia {
		t.Fatalf("这一条的 msg_type 应为 %d（富媒体），实际 %d：%v", MsgTypeMedia, only.msgType, p.rawList())
	}
	if only.fileInfo != "FILEINFO_TEST" {
		t.Fatalf("这一条必须带 media.file_info，实际 %+v", only)
	}
	if !strings.Contains(only.content, atAllTestTag) {
		t.Errorf("媒体条 content 里应含 @全体 标记 %q，实际 %q", atAllTestTag, only.content)
	}
	if !strings.Contains(only.content, atAllBodyText) {
		t.Errorf("媒体条 content 里应含正文 %q，实际 %q", atAllBodyText, only.content)
	}
	if n := countBodyContaining(sends, atAllBodyText); n != 1 {
		t.Errorf("正文只该出现一次，实际命中 %d 条请求：%v", n, p.rawList())
	}
}

// @某个人 的行为**不变**：markdown 单独一条（带 @ 与正文），媒体一条且 content 为空。
//
// 为什么不能一起并进媒体条：msg_type=7 的 content 是纯文本，<qqbot-at-user/> 塞进去
// 只会原样显示成代码（无纯文本渲染实证，维持保守）。
func TestQQOfficialAtUserWithImageStaysTwoMessages(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{atUserSegment(testOwnerOpen), textSegment(atAllBodyText), imageSegment("cover.png")}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	sends := p.sent()
	if len(sends) != 2 {
		t.Fatalf("@某个人 + 正文 + 1 张图仍应是 2 条（markdown 正文 + 媒体），实际 %d 条：%v", len(sends), p.rawList())
	}
	wantAt := `<qqbot-at-user id="` + testOwnerOpen + `" />`
	var md *fakeSendRecord
	for i := range sends {
		if sends[i].markdown != "" {
			md = &sends[i]
		}
	}
	if md == nil {
		t.Fatalf("必须有 markdown 正文（msg_type=2），实际 %v", p.rawList())
	}
	if md.msgType != MsgTypeMarkdown {
		t.Errorf("带 @某个人 的正文 msg_type 应为 %d，实际 %d", MsgTypeMarkdown, md.msgType)
	}
	if !strings.Contains(md.markdown, wantAt) || !strings.Contains(md.markdown, atAllBodyText) {
		t.Errorf("markdown 正文应同时含 @ 标记与正文，实际 %q", md.markdown)
	}
	for _, s := range sends {
		if s.fileInfo != "" && s.content != "" {
			t.Errorf("媒体条 content 必须为空（正文已在 markdown 条里），实际 %q", s.content)
		}
	}
}

// @全体 + @某个人 + 正文 + 1 张图：@某个人 只在 markdown 里渲染，所以仍是一条 markdown
// 正文 + 一条媒体，共 2 条；两个标签都必须出现在 markdown 那条里。
func TestQQOfficialAtEveryoneAndUserWithImageKeepsBothTagsInMarkdown(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{atAllSegment(), atUserSegment(testOwnerOpen), textSegment(atAllBodyText), imageSegment("cover.png")}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	sends := p.sent()
	if len(sends) != 2 {
		t.Fatalf("@全体 + @某个人 + 正文 + 1 张图应是 2 条（markdown 正文 + 媒体），实际 %d 条：%v", len(sends), p.rawList())
	}
	var md *fakeSendRecord
	for i := range sends {
		if sends[i].markdown != "" {
			md = &sends[i]
		}
	}
	if md == nil {
		t.Fatalf("必须有一条 markdown 正文（@某个人 只能走 markdown），实际 %v", p.rawList())
	}
	wantUser := `<qqbot-at-user id="` + testOwnerOpen + `" />`
	if !strings.Contains(md.markdown, atAllTestTag) {
		t.Errorf("markdown 条里应有 @全体 标记 %q，实际 %q", atAllTestTag, md.markdown)
	}
	if !strings.Contains(md.markdown, wantUser) {
		t.Errorf("markdown 条里应有 @某个人 标记 %q，实际 %q", wantUser, md.markdown)
	}
	if !strings.Contains(md.markdown, atAllBodyText) {
		t.Errorf("markdown 条里应有正文 %q，实际 %q", atAllBodyText, md.markdown)
	}
	if n := countBodyContaining(sends, atAllBodyText); n != 1 {
		t.Errorf("正文只该出现一次，实际命中 %d 条请求：%v", n, p.rawList())
	}
}

// 只有 @全体 + 正文、没有媒体：仍然是一条 msg_type=0 的纯文本，标签就在 content 里
// （实测纯文本认这枚标签，群里能渲染成真 @）。
func TestQQOfficialAtEveryoneWithoutMediaSendsSingleTextMessage(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{atAllSegment(), textSegment(atAllBodyText)}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("@全体 + 正文（无媒体）应只发 1 条，实际 %d 条：%v", len(sends), p.rawList())
	}
	only := sends[0]
	if only.msgType != MsgTypeText {
		t.Fatalf("msg_type 应为 %d（纯文本），实际 %d：%v", MsgTypeText, only.msgType, p.rawList())
	}
	if !strings.Contains(only.content, atAllTestTag) {
		t.Errorf("纯文本 content 里应含 @全体 标记 %q，实际 %q", atAllTestTag, only.content)
	}
	if !strings.Contains(only.content, atAllBodyText) {
		t.Errorf("纯文本 content 里应含正文 %q，实际 %q", atAllBodyText, only.content)
	}
}

// 回归：@全体 撞 40034142 时，剥掉标记后的重发请求里 content 与 markdown **两边都不许**再有它。
//
// 这里的形态就是 buildOutbound 改完之后的样子：content 与 markdown 都带同一枚标签。
func TestSendAtEveryoneStripsTagFromBothContentAndMarkdown(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	body := "开播啦" + atAllTestTag
	if _, err := c.Send(context.Background(), atAllTestGroup("atall-both"), SendOptions{Content: body, Markdown: body}); err != nil {
		t.Fatalf("撞 @全体限额时应当剥标记重发，实际失败：%v", err)
	}
	if got := fs.hitCount(); got != 2 {
		t.Fatalf("@全体降级只该多一次重发：假平台收到 %d 次请求，期望 2", got)
	}
	second := fs.bodyAt(1)
	if strings.Contains(second.Content, "qqbot-at-everyone") {
		t.Fatalf("重发请求的 content 里还带着 @全体 标记：%q", second.Content)
	}
	if second.Markdown != nil && strings.Contains(second.Markdown.Content, "qqbot-at-everyone") {
		t.Fatalf("重发请求的 markdown 里还带着 @全体 标记：%q", second.Markdown.Content)
	}
	if !strings.Contains(atAllRequestText(second), "开播啦") {
		t.Fatalf("重发请求丢了正文：%q", atAllRequestText(second))
	}
}

// 回归：@全体 + 媒体的形态（msg_type=7，标签在 content 里）撞 40034142 时，
// 重发的媒体请求里 content 不许再带标签，正文与 media.file_info 都要保留。
func TestSendAtEveryoneMediaStripsTagFromContent(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	body := "开播啦" + atAllTestTag
	if _, err := c.Send(context.Background(), atAllTestGroup("atall-media"), SendOptions{
		Content:  body,
		FileInfo: "FILEINFO_TEST",
	}); err != nil {
		t.Fatalf("富媒体撞 @全体限额时应当剥标记重发，实际失败：%v", err)
	}
	first := fs.bodyAt(0)
	if first.MsgType != MsgTypeMedia {
		t.Fatalf("第一条应是富媒体消息：msg_type = %d，期望 %d", first.MsgType, MsgTypeMedia)
	}
	if !strings.Contains(first.Content, "qqbot-at-everyone") {
		t.Fatalf("首发请求的 content 里本该带着 @全体 标记：%q", first.Content)
	}
	second := fs.bodyAt(1)
	if second.MsgType != MsgTypeMedia {
		t.Fatalf("剥掉标记后仍应保持富媒体：msg_type = %d，期望 %d", second.MsgType, MsgTypeMedia)
	}
	if strings.Contains(second.Content, "qqbot-at-everyone") {
		t.Fatalf("重发的媒体请求 content 里还带着 @全体 标记：%q", second.Content)
	}
	if !strings.Contains(second.Content, "开播啦") {
		t.Fatalf("重发的媒体请求丢了正文：%q", second.Content)
	}
	if second.Media == nil || second.Media.FileInfo != "FILEINFO_TEST" {
		t.Fatalf("重发的媒体请求丢了 media.file_info：%+v", second.Media)
	}
}
