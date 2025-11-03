package mmsg

import (
	"encoding/base64"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"os"
	"strings"
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

func (v *VideoElement) Type() message.ElementType {
	return Video
}

func (v *VideoElement) PackToElement(target Target) message.IMessageElement {
	m := message.NewVideo("")
	if v == nil {
		return message.NewText("[空视频]\n")
	} else if v.Url != "" {
		if strings.HasPrefix(v.Url, "http://") || strings.HasPrefix(v.Url, "https://") {
			m.File = v.Url
		} else {
			m.File = "file://" + strings.ReplaceAll(v.Url, `\`, `\\`)
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
	m.File = "base64://" + base64Video
	return m
}
