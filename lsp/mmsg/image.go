package mmsg

import (
	"encoding/base64"
	"os"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

type ImageBytesElement struct {
	Url         string
	Buf         []byte
	alternative string
	merged      bool
}

func NewImage(buf []byte, url ...any) *ImageBytesElement {
	v := &ImageBytesElement{}
	if buf != nil {
		v.Buf = buf
	}
	if len(url) > 0 {
		v.Url = url[0].(string)
	}
	return v
}

func NewMergedImage(buf []byte) *ImageBytesElement {
	i := NewImage(buf)
	i.merged = true
	return i
}

func (i *ImageBytesElement) IsMerged() bool { return i != nil && i.merged }

// NewImageByUrl 默认会对相同的url使用缓存
func NewImageByUrl(url string, opts ...requests.Option) *ImageBytesElement {
	var img = NewImage(nil)
	b, err := utils.ImageGet(url, opts...)
	if err == nil {
		img.Buf = b
	} else {
		logger.WithField("url", url).Errorf("ImageGet error %v", err)
	}
	return img
}

// NewImageByUrlWithoutCache 默认情况下相同的url会存在缓存，
// 如果url会随机返回不同的图片，则需要禁用缓存
// 这个函数就是不使用缓存的版本
func NewImageByUrlWithoutCache(url string, opts ...requests.Option) *ImageBytesElement {
	var img = NewImage(nil)
	b, err := utils.ImageGetWithoutCache(url, opts...)
	if err == nil {
		img.Buf = b
	} else {
		logger.WithField("url", url).Errorf("ImageGet error %v", err)
	}
	return img
}

func NewImageByLocal(filepath string) *ImageBytesElement {
	var img = NewImage(nil)
	b, err := os.ReadFile(filepath)
	if err == nil {
		img.Buf = b
	} else {
		logger.WithField("filepath", filepath).Errorf("ReadFile error %v", err)
	}
	return img
}

func (i *ImageBytesElement) Norm() *ImageBytesElement {
	if i == nil || i.Buf == nil {
		return i
	}
	b, err := utils.ImageNormSize(i.Buf)
	if err == nil {
		i.Buf = b
	} else {
		logger.Errorf("mmsg: ImageBytesElement Norm error %v", err)
	}
	return i
}

func (i *ImageBytesElement) Resize(width, height uint) *ImageBytesElement {
	if i == nil || i.Buf == nil {
		return i
	}
	b, err := utils.ImageResize(i.Buf, width, height)
	if err == nil {
		i.Buf = b
	} else {
		logger.Errorf("mmsg: ImageBytesElement Resize error %v", err)
	}
	return i
}

func (i *ImageBytesElement) Alternative(s string) *ImageBytesElement {
	i.alternative = s
	return i
}

// GetFile 返回可用于发送/转发的文件字符串
// 优先级：Url > base64(Buf) > alternative
func (i *ImageBytesElement) GetFile() string {
	if i == nil {
		return ""
	}
	if i.Url != "" {
		if strings.HasPrefix(i.Url, "http://") || strings.HasPrefix(i.Url, "https://") {
			return i.Url
		}
		return "file://" + strings.ReplaceAll(i.Url, `\`, `\\`)
	}
	if i.Buf != nil && len(i.Buf) > 0 {
		return "base64://" + base64.StdEncoding.EncodeToString(i.Buf)
	}
	return i.alternative
}

func (i *ImageBytesElement) Type() adapter.ElementType {
	return ImageBytes
}

func (i *ImageBytesElement) ToSendingMessage() *adapter.SendingMessage {
	return &adapter.SendingMessage{Elements: []adapter.IMessageElement{i}}
}

func (i *ImageBytesElement) PackToElement(target Target) adapter.IMessageElement {
	m := &adapter.ImageSegment{Generated: i != nil && i.merged}
	if i == nil {
		return &adapter.TextSegment{Content: "[空图片]\n"}
	} else if i.Url != "" {
		if strings.HasPrefix(i.Url, "http://") || strings.HasPrefix(i.Url, "https://") {
			m.File = i.Url
		} else {
			m.File = "file://" + strings.ReplaceAll(i.Url, `\`, `\\`)
		}
		return m
	} else if i.Buf == nil {
		if target.TargetType() == TargetGroup && target.TargetCode() == 0 {
			if i.alternative != "" {
				return &adapter.TextSegment{Content: i.alternative + "\n"}
			}
			return m
		}
		logger.WithField("Target", target.TargetCode()).
			WithField("TargetType", target.TargetType()).
			Debug("PackToElement failed: nil image buf")
		return m
	}
	logger.Debugf("转换base64图片")
	base64Image := base64.StdEncoding.EncodeToString(i.Buf) // 这里进行转换
	m.File = "base64://" + base64Image
	return m
}
