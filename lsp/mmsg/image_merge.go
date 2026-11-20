package mmsg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"net/url"
	"os"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

// MergeImageRuns processes a complete rendered message before sending. Text,
// cuts, video and GIFs separate galleries; a failed gallery keeps its originals.
// eligible may restrict the implicit legacy Bilibili policy to known URLs.
func (m *MSG) MergeImageRuns(mode, layout string, eligible map[string]utils.ImageSize, options ...utils.ImageMergeOptions) *MSG {
	if m == nil || mode == "off" {
		return m
	}
	result := NewMSG()
	o := utils.ImageMergeOptions{Layout: layout}
	if len(options) > 0 {
		o = options[0]
		o.Layout = layout
	}
	var run []*ImageBytesElement
	flush := func() {
		defer func() { run = nil }()
		if len(run) < 2 {
			for _, e := range run {
				result.Append(e)
			}
			return
		}
		merged, err := mergeImageRun(run, mode, o, eligible)
		if err != nil {
			logger.WithError(err).Warn("image gallery merge failed; keeping original images")
		}
		if merged == nil {
			for _, e := range run {
				result.Append(e)
			}
			return
		}
		result.Append(merged...)
	}
	for _, elem := range m.Elements() {
		img, ok := elem.(*ImageBytesElement)
		if ok && img != nil && !img.merged {
			if eligible == nil {
				run = append(run, img)
				continue
			}
			if _, found := eligible[img.Url]; found {
				run = append(run, img)
				continue
			}
		}
		flush()
		result.Append(elem)
	}
	flush()
	return result
}

func imageElementBytes(e *ImageBytesElement) ([]byte, error) {
	// Match GetFile/PackToElement precedence.
	if e.Url == "" {
		if len(e.Buf) > 0 {
			return e.Buf, nil
		}
		return nil, fmt.Errorf("empty image")
	}
	if strings.HasPrefix(e.Url, "http://") || strings.HasPrefix(e.Url, "https://") {
		return utils.ImageGet(e.Url)
	}
	if strings.HasPrefix(e.Url, "base64://") {
		return base64.StdEncoding.DecodeString(strings.TrimPrefix(e.Url, "base64://"))
	}
	path := e.Url
	if strings.HasPrefix(path, "file://") {
		u, err := url.Parse(path)
		if err != nil {
			return nil, err
		}
		path = u.Path
		if u.Host != "" {
			path = "//" + u.Host + path
		}
		if len(path) > 2 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
	}
	return os.ReadFile(path)
}

func mergeImageRun(run []*ImageBytesElement, mode string, options utils.ImageMergeOptions, eligible map[string]utils.ImageSize) ([]adapter.IMessageElement, error) {
	if mode == "auto" && len(run) <= 3 || mode == "only9" && len(run) < 9 {
		return nil, nil
	}
	if eligible != nil {
		sizes := make([]utils.ImageSize, len(run))
		for i, e := range run {
			sizes[i] = eligible[e.Url]
		}
		if !utils.ShouldMergeImages(mode, sizes) {
			return nil, nil
		}
	}
	data := make([][]byte, len(run))
	sizes := make([]utils.ImageSize, len(run))
	formats := make([]string, len(run))
	for i, e := range run {
		var err error
		data[i], err = imageElementBytes(e)
		if err != nil {
			return nil, err
		}
		c, format, err := image.DecodeConfig(bytes.NewReader(data[i]))
		if err != nil {
			return nil, err
		}
		formats[i], sizes[i] = format, utils.ImageSize{Width: c.Width, Height: c.Height}
		if eligible != nil {
			sizes[i] = eligible[e.Url]
		}
	}
	var output []adapter.IMessageElement
	for start := 0; start < len(run); {
		if formats[start] == "gif" {
			output = append(output, run[start])
			start++
			continue
		}
		end := start + 1
		for end < len(run) && formats[end] != "gif" {
			end++
		}
		if utils.ShouldMergeImages(mode, sizes[start:end]) {
			pages, err := composeGallery(data[start:end], options)
			if err != nil {
				return nil, err
			}
			for _, page := range pages {
				output = append(output, NewMergedImage(page))
			}
		} else {
			for _, e := range run[start:end] {
				output = append(output, e)
			}
		}
		start = end
	}
	return output, nil
}
