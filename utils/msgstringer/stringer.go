package msgstringer

import (
	"strconv"
	"strings"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/davecgh/go-spew/spew"
)

// 实际上at在这里
func MsgToString(elements []message.IMessageElement) string {
	var res strings.Builder
	for i, elem := range elements {
		if elem == nil {
			continue
		}
		// Print each element's type for debugging
		logger.Debugf(`Element %d is of type %T\n`, i, elem)
		//fmt.Printf("Element %d is of type %T\n", i, elem)
		switch e := elem.(type) {
		case *message.TextElement:
			res.WriteString(e.Content)
			//fmt.Printf("Content of TextElement: %s\n", e.Content)
			logger.Debugf(`Content of TextElement: %s\n`, e.Content)
		case *message.FaceElement:
			res.WriteString("[")
			res.WriteString(e.Name)
			res.WriteString("]")
		case *message.GroupImageElement:
			if e.Flash {
				res.WriteString("[Flash Image]")
			} else {
				res.WriteString("[Image]")
			}
		case *message.FriendImageElement:
			if e.Flash {
				res.WriteString("[Flash Image]")
			} else {
				res.WriteString("[Image]")
			}
		case *message.AtElement:
			res.WriteString("[CQ:at,qq=")
			if e.Target == 0 {
				res.WriteString("all")
			} else {
				res.WriteString(strconv.FormatInt(e.Target, 10))
			}
			res.WriteString("]")
		case *message.RedBagElement:
			res.WriteString("[RedBag:")
			res.WriteString(e.Title)
			res.WriteString("]")
		case *message.ReplyElement:
			//fmt.Printf("暂时不发送at:[Reply:%s]\n", strconv.FormatInt(int64(e.ReplySeq), 10))
			//logger.Infof(`暂时不发送at:[Reply:%s]\n`, strconv.FormatInt(int64(e.ReplySeq), 10))
			res.WriteString("[CQ:reply,id=")
			res.WriteString(strconv.FormatInt(int64(e.ReplySeq), 10))
			res.WriteString("]")
		case *message.GroupFileElement:
			res.WriteString("[File]")
			res.WriteString(e.Name)
		case *message.FriendFileElement:
			res.WriteString("[File]")
			res.WriteString(e.Name)
		case *message.ShortVideoElement:
			res.WriteString("[Video]")
		case *message.ForwardElement:
			res.WriteString("[Forward]")
		case *message.MusicShareElement:
			res.WriteString("[Music]")
		case *message.LightAppElement:
			res.WriteString("[LightApp]")
			res.WriteString(e.Content)
		case *message.ServiceElement:
			res.WriteString("[Service]")
			res.WriteString(e.Content)
		case *message.VoiceElement, *message.GroupVoiceElement:
			res.WriteString("[Voice]")
		case *mmsg.ImageBytesElement:
			res.WriteString("[Image]")
		case *mmsg.TypedElement:
			res.WriteString("[Typed]")
		case *mmsg.CutElement:
			res.WriteString("[CUT]")
		case *message.MarketFaceElement:
			res.WriteString(e.Name)
		case *message.DiceElement:
			res.WriteString(e.Name)
			res.WriteString(strconv.FormatInt(int64(e.Value), 10))
		case *message.AnimatedSticker:
			res.WriteString("[")
			res.WriteString(e.Name)
			res.WriteString("]")
		case *message.FingerGuessingElement:
			res.WriteString("[")
			res.WriteString(e.Name)
			res.WriteString("]")
		default:
			logger.WithField("content", spew.Sdump(elem)).Debug("found new element")
		}
	}
	return res.String()
}
