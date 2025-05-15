package mmsg

import (
	"encoding/base64"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"strconv"
	"strings"
)

type FileElement struct {
	Url         string
	Buf         []byte
	alternative string
	name        string
	length      int64
}

func NewFile(url string, Buf ...any) *FileElement {
	f := &FileElement{}
	if url != "" {
		f.Url = url
	}
	if len(Buf) > 0 {
		f.Buf = Buf[0].([]byte)
	}
	return f
}

func NewFileByUrl(url string, opts ...requests.Option) *FileElement {
	var f = NewFile("")
	// 使用LRU缓存
	//b, hd, err := utils.FileGet(url, opts...)
	// 不使用LRU缓存
	b, hd, err := utils.FileGetWithoutCache(url, opts...)
	if err == nil && hd != nil {
		f.Buf = b
		if name, err2 := utils.ParseDisposition(hd.ContentDisposition); err2 == nil {
			f.name = name
		}
		if length, err2 := strconv.ParseInt(hd.ContentLength, 10, 64); err2 == nil {
			f.length = length
		}
	} else {
		logger.WithField("url", url).Errorf("FileGet error %v", err)
	}
	return f
}

func (f *FileElement) Alternative(s string) *FileElement {
	f.alternative = s
	return f
}

func (f *FileElement) Name(s string) *FileElement {
	f.name = s
	return f
}

func (f *FileElement) Length(s string) *FileElement {
	i, _ := strconv.ParseInt(s, 10, 64)
	f.length = i
	return f
}

func (f *FileElement) Type() message.ElementType {
	return File
}

func (f *FileElement) PackToElement(target Target) message.IMessageElement {
	if f == nil {
		return message.NewText("[空文件]\n")
	} else if f.Url != "" {
		var base64Text string
		if strings.HasPrefix(f.Url, "http://") || strings.HasPrefix(f.Url, "https://") {
			base64Text = "[CQ:file,file=" + f.Url + setName(f.alternative, f.name) + "]"
		} else {
			base64Text = "[CQ:file,file=file://" + strings.ReplaceAll(f.Url, `\`, `\\`) + setName(f.alternative, f.name) + "]"
		}
		return message.NewText(base64Text)
	} else if f.Buf == nil {
		logger.Debugf("TargetPrivate %v nil file buf", target.TargetCode())
		return nil
	}
	logger.Debugf("转换base64文件")
	base64File := base64.StdEncoding.EncodeToString(f.Buf)                                      // 这里进行转换
	base64Text := "[CQ:file,file=base64://" + base64File + setName(f.alternative, f.name) + "]" // Base64 文本格式
	return message.NewText(base64Text)
}

func setName(s1 string, s2 string) string {
	base := ",name="
	if s1 != "" {
		return base + EscapeCQCode(s1)
	} else if s2 != "" {
		return base + EscapeCQCode(s2)
	} else {
		return ""
	}
}

func EscapeCQCode(s string) string {
	var builder strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			builder.WriteString("&amp;")
		case '[':
			builder.WriteString("&#91;")
		case ']':
			builder.WriteString("&#93;")
		case ',':
			builder.WriteString("&#44;")
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
