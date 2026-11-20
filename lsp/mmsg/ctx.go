package mmsg

import (
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/sirupsen/logrus"
)

type IMsgCtx interface {
	TextSend(text string) interface{}
	TextReply(text string) interface{}
	Reply(m *MSG) interface{}
	Send(m *MSG) interface{}
	NoPermissionReply() interface{}
	GetLog() *logrus.Entry
	GetTarget() Target
	GetSender() *adapter.SenderInfo
}
