package lsp

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/stretchr/testify/require"
)

type messageRecordingAdapter struct {
	adapter.Adapter
	groupMessages   [][]adapter.MessageSegment
	privateMessages [][]adapter.MessageSegment
}

func (*messageRecordingAdapter) IsConnected() bool { return true }

func (a *messageRecordingAdapter) SendGroupMessage(_ int64, message interface{}) (int32, error) {
	a.groupMessages = append(a.groupMessages, message.([]adapter.MessageSegment))
	return 1, nil
}

func (a *messageRecordingAdapter) SendPrivateMessage(_ int64, message interface{}) (int32, error) {
	a.privateMessages = append(a.privateMessages, message.([]adapter.MessageSegment))
	return 1, nil
}

func TestSendMsgSkipsMissingTemplateReply(t *testing.T) {
	const cover = "https://example.com/cover.jpg"
	impl := &messageRecordingAdapter{}
	installForwardBot(t, impl)
	l := newForwardLsp(t)

	tmpl, err := template.New("live").Parse(`{{ reply .msg }}{{ .title }}`)
	require.NoError(t, err)
	msg := mmsg.NewMSG()
	require.NoError(t, tmpl.Execute(msg, map[string]interface{}{"title": "直播开始了"}))
	msg.Append(&adapter.ImageSegment{File: cover})
	msg.AtAll(true)
	require.Len(t, msg.Elements(), 3, "template output must not contain a nil reply")

	result := l.AGM(l.SendMsg(msg, mmsg.NewGroupTarget(123)))
	require.Len(t, result, 1)
	require.EqualValues(t, 1, result[0].ID)
	require.Len(t, impl.groupMessages, 1)
	require.Equal(t, []adapter.MessageSegment{
		{Type: "at", Data: map[string]interface{}{"qq": "all"}},
		{Type: "text", Data: map[string]interface{}{"text": "直播开始了"}},
		{Type: "image", Data: map[string]interface{}{"file": cover, "url": ""}},
	}, impl.groupMessages[0])
}

func TestSendFiltersNilElementsAndKeepsValidReply(t *testing.T) {
	impl := &messageRecordingAdapter{}
	installForwardBot(t, impl)
	l := newForwardLsp(t)

	newMessage := func() *adapter.SendingMessage {
		return &adapter.SendingMessage{Elements: []adapter.IMessageElement{
			nil,
			(*adapter.ReplySegment)(nil),
			&adapter.ReplySegment{ReplySeq: 42},
			(*adapter.TextSegment)(nil),
			&adapter.TextSegment{Content: "正文"},
			(*adapter.ImageSegment)(nil),
		}}
	}
	expected := []adapter.MessageSegment{
		{Type: "reply", Data: map[string]interface{}{"id": int32(42)}},
		{Type: "text", Data: map[string]interface{}{"text": "正文"}},
	}

	require.EqualValues(t, 1, l.sendGroupMessage(123, newMessage()).ID)
	require.Equal(t, [][]adapter.MessageSegment{expected}, impl.groupMessages)
	require.EqualValues(t, 1, l.sendPrivateMessage(456, newMessage()).ID)
	require.Equal(t, [][]adapter.MessageSegment{expected}, impl.privateMessages)
}
