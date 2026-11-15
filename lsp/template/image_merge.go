package template

import (
	"bytes"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

// ImageMergeGallery lets a site describe its legacy gallery without importing
// the site package into the shared template/image processing layers.
type ImageMergeGallery interface {
	ImageMergeGallery() map[string]utils.ImageSize
}

// Preserve the legacy .dynamic.Image.Bytes field for existing Bilibili
// templates. The site supplies data only; merging and policy stay shared.
type legacyImageMergeData interface {
	ImageMergeGallery
	ImageMergeURLs() []string
	WithMergedImage([]byte) interface{}
}

func prepareLegacyImageMerge(name string, data interface{}) (interface{}, []byte) {
	if name != "notify.group.bilibili.news.tmpl" {
		return data, nil
	}
	c := cfg.GetImageMergeConfig("bilibili")
	if !c.LegacyBilibili || c.Mode == "off" {
		return data, nil
	}
	values, ok := data.(map[string]interface{})
	if !ok {
		return data, nil
	}
	d, ok := values["dynamic"].(legacyImageMergeData)
	if !ok {
		return data, nil
	}
	msg := mmsg.NewMSG()
	for _, uri := range d.ImageMergeURLs() {
		msg.Append(mmsg.NewImage(nil, uri))
	}
	elems := msg.MergeImageRuns(c.Mode, c.Layout, d.ImageMergeGallery()).Elements()
	if len(elems) != 1 {
		return data, nil
	}
	img, ok := elems[0].(*mmsg.ImageBytesElement)
	if !ok || !img.IsMerged() {
		return data, nil
	}
	copyValues := make(map[string]interface{}, len(values))
	for key, value := range values {
		copyValues[key] = value
	}
	copyValues["dynamic"] = d.WithMergedImage(img.Buf)
	return copyValues, img.Buf
}

func markPreparedImage(msg *mmsg.MSG, prepared []byte) *mmsg.MSG {
	if len(prepared) == 0 {
		return msg
	}
	result := mmsg.NewMSG()
	for _, elem := range msg.Elements() {
		if img, ok := elem.(*mmsg.ImageBytesElement); ok && img != nil && img.Url == "" && bytes.Equal(img.Buf, prepared) {
			result.Append(mmsg.NewMergedImage(prepared))
		} else {
			result.Append(elem)
		}
	}
	return result
}

func mergeRenderedImages(name string, data interface{}, msg *mmsg.MSG) *mmsg.MSG {
	parts := strings.Split(name, ".")
	if len(parts) < 4 || parts[0] != "notify" {
		return msg
	}
	site := parts[2]
	c := cfg.GetImageMergeConfig(site)
	if c.Mode == "off" {
		return msg
	}
	var eligible map[string]utils.ImageSize
	if c.LegacyBilibili {
		values, ok := data.(map[string]interface{})
		if !ok {
			return msg
		}
		gallery, ok := values["dynamic"].(ImageMergeGallery)
		if !ok {
			return msg
		}
		eligible = gallery.ImageMergeGallery()
		if len(eligible) == 0 {
			return msg
		}
	}
	return msg.MergeImageRuns(c.Mode, c.Layout, eligible)
}
