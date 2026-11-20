package mmsg

import (
	"encoding/base64"
	"os"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

type VideoElement struct {
	Url         string
	Buf         []byte
	alternative string
}

func NewVideo(url string, Buf ...any) *VideoElement {
	v := &VideoElement{}
	if url != "" {
		v.Url = url
	}
	if len(Buf) > 0 {
		v.Buf = Buf[0].([]byte)
	}
	return v
}

func NewVideoByLocal(path string) *VideoElement {
	v := &VideoElement{}
	b, err := os.ReadFile(path)
	if err == nil {
		v.Buf = b
	} else {
		logger.WithField("filepath", path).Errorf("ReadFile error %v", err)
	}
	return v
}

func NewVideoByUrl(url string, opts ...requests.Option) *VideoElement {
	var v = NewVideo("")
	// 使用LRU缓存
	b, hd, err := utils.FileGet(url, opts...)
	// 不使用LRU缓存
	//b, hd, err := utils.FileGetWithoutCache(url, opts...)
	if err == nil && hd != nil {
		v.Buf = b
	} else {
		logger.WithField("url", url).Errorf("VideoGet error %v", err)
	}
	return v
}

func (v *VideoElement) Alternative(s string) *VideoElement {
	v.alternative = s
	return v
}

// GetFile 返回可用于发送/转发的文件字符串
// 优先级：Url > base64(Buf) > alternative
func (v *VideoElement) GetFile() string {
	if v == nil {
		return ""
	}
	if v.Url != "" {
		if strings.HasPrefix(v.Url, "http://") || strings.HasPrefix(v.Url, "https://") {
			return v.Url
		}
		return "file://" + strings.ReplaceAll(v.Url, `\`, `\\`)
	}
	if v.Buf != nil && len(v.Buf) > 0 {
		return "base64://" + base64.StdEncoding.EncodeToString(v.Buf)
	}
	return v.alternative
}

func (v *VideoElement) Type() adapter.ElementType {
	return Video
}

func (v *VideoElement) ToSendingMessage() *adapter.SendingMessage {
	return &adapter.SendingMessage{Elements: []adapter.IMessageElement{v}}
}

func (v *VideoElement) PackToElement(target Target) adapter.IMessageElement {
	m := &adapter.VideoSegment{}
	if v == nil {
		return &adapter.TextSegment{Content: "[空视频]\n"}
	} else if v.Url != "" {
		if strings.HasPrefix(v.Url, "http://") || strings.HasPrefix(v.Url, "https://") {
			m.Url = v.Url
		} else {
			m.Url = "file://" + strings.ReplaceAll(v.Url, `\`, `\\`)
		}
		return m
	} else if v.Buf == nil {
		logger.WithField("Target", target.TargetCode()).
			WithField("TargetType", target.TargetType()).
			Debug("PackToElement failed: nil video buf")
		return nil
	}
	logger.Debugf("转换base64视频")
	base64Video := base64.StdEncoding.EncodeToString(v.Buf) // 这里进行转换
	m.Url = "base64://" + base64Video
	return m
}
