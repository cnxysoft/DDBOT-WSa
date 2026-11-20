package qqlog

import (
	"strconv"

	"github.com/sirupsen/logrus"
)

// Logger is the qq-logs logger exported for use by adapters and other packages
var Logger *logrus.Entry

// Enabled indicates whether qq-logs is currently enabled
var Enabled bool

func Init(logger *logrus.Entry) {
	Logger = logger
}

// FormatGroupSendLog formats a group message send log
func FormatGroupSendLog(groupID int64, groupName, content string) string {
	return "发送 群消息 给 " + groupName + "(" + strconv.FormatInt(groupID, 10) + "): " + content
}

// FormatPrivateSendLog formats a private message send log
func FormatPrivateSendLog(userID int64, nickname, content string) string {
	return "发送 私聊消息 给 " + nickname + "(" + strconv.FormatInt(userID, 10) + "): " + content
}
