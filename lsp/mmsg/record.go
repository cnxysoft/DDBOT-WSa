package mmsg

import (
	"encoding/base64"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"strings"
)

type RecordElement struct {
	Url         string
	Buf         []byte
	alternative string
}

func NewRecord(url string, Buf ...any) *RecordElement {
	r := &RecordElement{}
	if url != "" {
		r.Url = url
	}
	if len(Buf) > 0 {
		r.Buf = Buf[0].([]byte)
	}
	return r
}

func NewRecordByUrl(url string, opts ...requests.Option) *RecordElement {
	var r = NewRecord("")
	// 使用LRU缓存
	//b, hd, err := utils.FileGet(url, opts...)
	// 不使用LRU缓存
	b, hd, err := utils.FileGetWithoutCache(url, opts...)
	if err == nil && hd != nil {
		r.Buf = b
	} else {
		logger.WithField("url", url).Errorf("RecordGet error %v", err)
	}
	return r
}

func (r *RecordElement) Alternative(s string) *RecordElement {
	r.alternative = s
	return r
}

func (r *RecordElement) Type() message.ElementType {
	return Record
}

func (r *RecordElement) PackToElement(target Target) message.IMessageElement {
	if r == nil {
		return message.NewText("[空语音]\n")
	} else if r.Url != "" {
		var base64Text string
		if strings.HasPrefix(r.Url, "http://") || strings.HasPrefix(r.Url, "https://") {
			base64Text = "[CQ:record,file=" + r.Url + "]"
		} else {
			base64Text = "[CQ:record,file=file://" + strings.ReplaceAll(r.Url, `\`, `\\`) + "]"
		}
		return message.NewText(base64Text)
	} else if r.Buf == nil {
		logger.Debugf("TargetPrivate %v nil record buf", target.TargetCode())
		return nil
	}
	logger.Debugf("转换base64语音")
	base64Record := base64.StdEncoding.EncodeToString(r.Buf)       // 这里进行转换
	base64Text := "[CQ:record,file=base64://" + base64Record + "]" // Base64 文本格式
	return message.NewText(base64Text)
}
