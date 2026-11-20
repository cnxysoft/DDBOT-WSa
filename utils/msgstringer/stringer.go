package msgstringer

import (
	"strconv"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/davecgh/go-spew/spew"
)

// AdapterMsgToString converts adapter message elements to a human-readable string.
func AdapterMsgToString(elements []adapter.IMessageElement) string {
	var res strings.Builder
	for i, elem := range elements {
		if adapter.IsNilMessageElement(elem) {
			continue
		}
		logger.Debugf(`Element %d is of type %T\n`, i, elem)
		switch e := elem.(type) {
		case *adapter.TextSegment:
			res.WriteString(e.Content)
		case *adapter.FaceSegment:
			res.WriteString("[")
			res.WriteString(e.Name)
			res.WriteString("]")
		case *adapter.ImageSegment:
			res.WriteString("[图片]")
		case *adapter.AtSegment:
			if e.Target == 0 {
				res.WriteString("[艾特全体]")
			} else {
				res.WriteString("[艾特:" + strconv.FormatInt(e.Target, 10) + "]")
			}
		case *adapter.ReplySegment:
			res.WriteString("[回复:")
			res.WriteString(strconv.FormatInt(int64(e.ReplySeq), 10))
			res.WriteString("]")
		case *adapter.FileSegment:
			res.WriteString("[文件]")
			res.WriteString(e.Name)
		case *adapter.VideoSegment:
			res.WriteString("[视频]")
		case *adapter.VoiceSegment:
			res.WriteString("[语音]")
		case *adapter.ForwardSegment:
			res.WriteString("[聊天记录]")
		case *adapter.JsonSegment:
			res.WriteString("[小程序]")
			res.WriteString(e.Content)
		default:
			logger.WithField("content", spew.Sdump(elem)).Debug("found new adapter element")
		}
	}
	return res.String()
}
