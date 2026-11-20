package qqofficial

// @全体成员（<qqbot-at-everyone />）撞「每天 1 次」限额时的降级与冷却单测。
//
// 实测事实（2026-11-11）：
//  1. content 里带 <qqbot-at-everyone /> 就能 @全体，纯文本 msg_type=0 也认；
//  2. 同一机器人同一群，每天只能成功 1 次，第 2 次回
//     {"message":"消息发送失败，@全体成员次数超限","code":40034142,"err_code":40034142}；
//  3. 这时候**整条消息失败**，不是「@全体掉了正文照发」——所以必须剥掉标记重发，
//     否则一条带 @全体的推送整条丢掉。
//
// 全部走 httptest 假平台（回环地址），不联网、不碰真机；退避压到 1ms，不靠 sleep 等结果。
// 冷却账本是进程级的（Client 结构体在 api.go，不在本次改动范围），所以每个用例用
// 自己的 openid，用例之间不会串冷却。

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

const atAllTestTag = "<qqbot-at-everyone />"

// atAllTestGroup 是本案专用的群目标（openid 各不相同，隔开用例间的冷却账本）。
func atAllTestGroup(openID string) Target { return Target{Kind: TargetGroup, OpenID: openID} }

// atAllLimitReply 是实测的「@全体成员次数超限」回应。
func atAllLimitReply() fakeSendReply {
	return fakeSendReply{
		status: http.StatusBadRequest,
		body:   `{"message":"消息发送失败，@全体成员次数超限","code":40034142,"err_code":40034142}`,
	}
}

// atAllOKReply 是成功回应。
func atAllOKReply() fakeSendReply {
	return fakeSendReply{status: http.StatusOK, body: `{"id":"msg-ok","timestamp":"2026-11-12T10:00:00+08:00"}`}
}

// requestText 把一条请求里能看到的正文（content + markdown）拼起来，用来断言标记在不在。
func atAllRequestText(req sendMessageRequest) string {
	text := req.Content
	if req.Markdown != nil {
		text += req.Markdown.Content
	}
	return text
}

// TestSendDegradesAtEveryoneDailyLimit 是本案的主症状：
// 带 @全体 的推送撞 40034142 时整条会丢，客户端必须剥掉标记重发一次，
// 并且调用方拿到的应当是**成功**。
//
// 这里 markdown 里只有那一枚标记、正文放在 content 里：剥完 markdown 变空，
// 所以第二条请求必须切回纯文本 msg_type=0，正文照发。
func TestSendDegradesAtEveryoneDailyLimit(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	res, err := c.Send(context.Background(), atAllTestGroup("atall-degrade"), SendOptions{
		Content:  "开播啦",
		Markdown: atAllTestTag,
	})
	if err != nil {
		t.Fatalf("撞 @全体限额时应当剥标记重发，消息不该丢；实际失败：%v（假平台收到 %d 次请求）", err, fs.hitCount())
	}
	if got := fs.hitCount(); got != 2 {
		t.Fatalf("@全体降级只该多一次重发：假平台收到 %d 次请求，期望 2", got)
	}
	if got := fs.bodyAt(0).MsgType; got != MsgTypeMarkdown {
		t.Fatalf("第一条请求应当还是 markdown：msg_type = %d，期望 %d", got, MsgTypeMarkdown)
	}
	second := fs.bodyAt(1)
	if second.MsgType != MsgTypeText {
		t.Fatalf("剥完 markdown 变空后应当切回纯文本：msg_type = %d，期望 %d", second.MsgType, MsgTypeText)
	}
	if got := atAllRequestText(second); strings.Contains(got, "qqbot-at-everyone") {
		t.Fatalf("重发请求里还带着 @全体标记：%q", got)
	}
	if second.Content != "开播啦" {
		t.Fatalf("重发的正文不对：%q，期望 %q", second.Content, "开播啦")
	}
	if second.Markdown != nil {
		t.Fatalf("切成纯文本后不该再带 markdown 字段：%+v", second.Markdown)
	}
	if res.MsgID != "msg-ok" {
		t.Fatalf("成功响应的 msg id 没取回来：%q", res.MsgID)
	}
	if !res.AtAllDropped {
		t.Fatal("SendResult.AtAllDropped 应当标记为「这条的 @全体 被剥掉了」")
	}
}

// TestSendKeepsMarkdownWhenAtEveryoneStripped 是主症状的另一种形态：
// @全体 只是正文的一部分，剥掉之后 markdown 还有字，msg_type=2 应当保持。
func TestSendKeepsMarkdownWhenAtEveryoneStripped(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	body := "开播啦" + atAllTestTag
	if _, err := c.Send(context.Background(), atAllTestGroup("atall-keep-md"), SendOptions{Markdown: body}); err != nil {
		t.Fatalf("撞 @全体限额时应剥标记重发成功，实际失败：%v", err)
	}
	second := fs.bodyAt(1)
	if second.MsgType != MsgTypeMarkdown {
		t.Fatalf("markdown 剥完还有正文，msg_type 应当保持 %d，实际 %d", MsgTypeMarkdown, second.MsgType)
	}
	text := atAllRequestText(second)
	if strings.Contains(text, "qqbot-at-everyone") {
		t.Fatalf("重发请求里还带着 @全体标记：%q", text)
	}
	if !strings.Contains(text, "开播啦") {
		t.Fatalf("重发请求丢了正文：%q", text)
	}
}

// TestSendAtEveryoneCooldownSkipsTagForSameTarget 是第二层：
// 同一个目标撞过一次之后，当天余下的时间里不要再带 @全体——
// 不许「撞一次再降级」，第二次发送只该发一条请求。
func TestSendAtEveryoneCooldownSkipsTagForSameTarget(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))
	target := atAllTestGroup("atall-cooldown")

	opts := SendOptions{Markdown: "开播啦" + atAllTestTag}
	if _, err := c.Send(context.Background(), target, opts); err != nil {
		t.Fatalf("第一次撞限额应当降级成功，实际失败：%v", err)
	}
	before := fs.hitCount()

	if _, err := c.Send(context.Background(), target, opts); err != nil {
		t.Fatalf("冷却期内再发一条应当直接成功（只是不带 @全体），实际失败：%v", err)
	}
	if got := fs.hitCount() - before; got != 1 {
		t.Fatalf("冷却期内应当只发 1 条请求：实际 %d 条（说明又去撞了一次限额）", got)
	}
	last := fs.bodyAt(fs.hitCount() - 1)
	if text := atAllRequestText(last); strings.Contains(text, "qqbot-at-everyone") {
		t.Fatalf("冷却期内仍然带着 @全体标记发出去：%q", text)
	}
	if !strings.Contains(atAllRequestText(last), "开播啦") {
		t.Fatalf("冷却期内剥标记把正文也剥没了：%q", atAllRequestText(last))
	}
}

// TestSendAtEveryoneCooldownIsPerTarget 断言冷却不串目标：
// A 群撞了限额，B 群的 @全体 照样要带上。
func TestSendAtEveryoneCooldownIsPerTarget(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	opts := SendOptions{Markdown: "开播啦" + atAllTestTag}
	if _, err := c.Send(context.Background(), atAllTestGroup("atall-target-a"), opts); err != nil {
		t.Fatalf("A 群撞限额应当降级成功，实际失败：%v", err)
	}
	if _, err := c.Send(context.Background(), atAllTestGroup("atall-target-b"), opts); err != nil {
		t.Fatalf("B 群发送失败：%v", err)
	}
	last := fs.bodyAt(fs.hitCount() - 1)
	if text := atAllRequestText(last); !strings.Contains(text, "qqbot-at-everyone") {
		t.Fatalf("冷却串到别的目标了：B 群的请求里没有 @全体标记：%q", text)
	}
}

// TestSendAtEveryoneDegradeOnlyOnce 断言 40034142 只降级一次：
// 剥干净之后平台还回同一个码，就该报错收场，不许无限重试。
func TestSendAtEveryoneDegradeOnlyOnce(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(6))

	_, err := c.Send(context.Background(), atAllTestGroup("atall-once"), SendOptions{
		Markdown: "开播啦" + atAllTestTag,
	})
	if err == nil {
		t.Fatal("剥掉 @全体 之后平台还回 40034142，应当报错，不该假装成功")
	}
	if got := fs.hitCount(); got != 2 {
		t.Fatalf("@全体降级只能做一次：假平台收到 %d 次请求，期望 2（首发 + 一次剥标记重发）", got)
	}
	if text := atAllRequestText(fs.bodyAt(1)); strings.Contains(text, "qqbot-at-everyone") {
		t.Fatalf("第二次请求里还带着 @全体标记：%q", text)
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("应当把平台的 *APIError 交出来，实际：%v", err)
	}
	if apiErr.Code != ErrCodeAtEveryoneLimit && apiErr.ErrCode != ErrCodeAtEveryoneLimit {
		t.Fatalf("交出的错误码 = %d/%d，期望 %d", apiErr.Code, apiErr.ErrCode, ErrCodeAtEveryoneLimit)
	}
}

// TestSendAtEveryoneStripToEmptyDoesNotSendBlankRequest 断言兜底：
// 剥完两边都空时不许发空消息，按错误路径返回，而且一次空请求都不许多发。
func TestSendAtEveryoneStripToEmptyDoesNotSendBlankRequest(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	_, err := c.Send(context.Background(), atAllTestGroup("atall-empty"), SendOptions{Markdown: atAllTestTag})
	if err == nil {
		t.Fatal("剥完什么都不剩时应当报错，不该发空消息")
	}
	if got := fs.hitCount(); got != 1 {
		t.Fatalf("剥完为空时不该再发请求：假平台收到 %d 次请求，期望 1", got)
	}
}

// TestAtAllCooldownExpiresAtLocalMidnight 直接测冷却账本：
// 过期时间取次日零点（进程本地时区），过期项要能自然清掉、不留垃圾。
func TestAtAllCooldownExpiresAtLocalMidnight(t *testing.T) {
	store := newAtAllCooldown()
	now := time.Date(2026, 11, 12, 10, 30, 0, 0, time.Local)

	store.block("k1", now)
	if !store.cooling("k1", now) {
		t.Fatal("刚撞完限额的目标应当处于冷却中")
	}
	if !store.cooling("k1", time.Date(2026, 11, 12, 23, 59, 59, 0, time.Local)) {
		t.Fatal("当天 23:59:59 仍应处于冷却中")
	}
	if store.cooling("k1", time.Date(2026, 11, 13, 0, 0, 0, 0, time.Local)) {
		t.Fatal("跨过次日零点应当解除冷却")
	}
	if got := store.size(); got != 0 {
		t.Fatalf("过期的冷却项应当被顺手清掉：账本里还剩 %d 条", got)
	}

	// 写入时顺手打扫：前一天的过期项不该一直堆着。
	store.block("k2", now)
	store.block("k3", now.Add(25*time.Hour))
	if got := store.size(); got != 1 {
		t.Fatalf("写入时应当顺手清掉过期项：期望只剩 1 条，实际 %d 条", got)
	}
	if store.cooling("k2", now.Add(25*time.Hour)) {
		t.Fatal("k2 已经过期，不该还在冷却里")
	}
}

// TestSendNormalMessageIsNotTouchedByCooldown 是「误伤」的反向断言：
// 不带 @全体 的普通消息，哪怕同一目标正在冷却，也必须原样发出去——
// 不许把 <qqbot-at-user … />（@某个人）之类的标记也剥掉。
func TestSendNormalMessageIsNotTouchedByCooldown(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{atAllLimitReply(), atAllOKReply(), atAllOKReply()})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))
	target := atAllTestGroup("atall-no-harm")

	if _, err := c.Send(context.Background(), target, SendOptions{Markdown: "开播啦" + atAllTestTag}); err != nil {
		t.Fatalf("第一次撞限额应当降级成功，实际失败：%v", err)
	}
	atMark := `<qqbot-at-user id="OPENID_USER" />`
	if _, err := c.Send(context.Background(), target, SendOptions{Markdown: "开播啦" + atMark}); err != nil {
		t.Fatalf("普通消息发送失败：%v", err)
	}
	last := fs.bodyAt(fs.hitCount() - 1)
	if text := atAllRequestText(last); !strings.Contains(text, "qqbot-at-user") {
		t.Fatalf("冷却把 @某个人 也剥掉了（误伤）：%q", text)
	}
	if got := last.MsgType; got != MsgTypeMarkdown {
		t.Fatalf("普通消息的 msg_type 被改了：%d，期望 %d", got, MsgTypeMarkdown)
	}
}
