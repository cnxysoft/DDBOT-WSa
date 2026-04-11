package mmsg

import (
	"encoding/base64"
	"os"
	"strings"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
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

func NewRecordByLocal(path string) *RecordElement {
	r := &RecordElement{}
	b, err := os.ReadFile(path)
	if err == nil {
		r.Buf = b
	} else {
		logger.WithField("filepath", path).Errorf("ReadFile error %v", err)
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

// GetFile 返回可用于发送/转发的文件字符串
// 优先级：Url > base64(Buf) > alternative
func (r *RecordElement) GetFile() string {
	if r == nil {
		return ""
	}
	if r.Url != "" {
		if strings.HasPrefix(r.Url, "http://") || strings.HasPrefix(r.Url, "https://") {
			return r.Url
		}
		return "file://" + strings.ReplaceAll(r.Url, `\`, `\\`)
	}
	if r.Buf != nil && len(r.Buf) > 0 {
		return "base64://" + base64.StdEncoding.EncodeToString(r.Buf)
	}
	return r.alternative
}

func (r *RecordElement) Type() message.ElementType {
	return Record
}

func (r *RecordElement) PackToElement(target Target) message.IMessageElement {
	m := message.NewRecord("")
	if r == nil {
		return message.NewText("[空语音]\n")
	} else if r.Url != "" {
		if strings.HasPrefix(r.Url, "http://") || strings.HasPrefix(r.Url, "https://") {
			m.File = r.Url
		} else {
			m.File = "file://" + strings.ReplaceAll(r.Url, `\`, `\\`)
		}
		return m
	} else if r.Buf == nil {
		logger.WithField("Target", target.TargetCode()).
			WithField("TargetType", target.TargetType()).
			Debug("PackToElement failed: nil record buf")
		return nil
	}
	logger.Debugf("转换base64语音")
	base64Record := base64.StdEncoding.EncodeToString(r.Buf) // 这里进行转换
	m.File = "base64://" + base64Record
	return m
}
