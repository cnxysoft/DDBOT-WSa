package template

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testLegacyGallery map[string]utils.ImageSize

func (g testLegacyGallery) ImageMergeGallery() map[string]utils.ImageSize { return g }

type testLegacyImageData struct {
	urls  []string
	sizes testLegacyGallery
	image []byte
}

func (d testLegacyImageData) ImageMergeGallery() map[string]utils.ImageSize { return d.sizes }
func (d testLegacyImageData) ImageMergeURLs() []string                      { return d.urls }
func (d testLegacyImageData) WithMergedImage(data []byte) interface{}       { d.image = data; return d }

func TestImageMergeAllSitesAndGlobalOverride(t *testing.T) {
	old := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = old })
	v := viper.New()
	config.GlobalConfig = &config.Config{Viper: v}
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 24, 36))))
	path := filepath.Join(t.TempDir(), "image.png")
	require.NoError(t, os.WriteFile(path, b.Bytes(), 0600))
	images := []string{path, path, path, path}
	for _, site := range []string{"twitter", "weibo", "xhs", "future-site"} {
		name := "notify.group." + site + ".news.tmpl"
		msg := mmsg.NewMSG()
		for range 4 {
			msg.Append(mmsg.NewImage(nil, path))
		}
		assert.Len(t, mergeRenderedImages(name, nil, msg).Elements(), 4)
		v.Set(site+".imageMerge.mode", "always")
		v.Set(site+".imageMerge.layout", "long")
		got := mergeRenderedImages(name, nil, msg).Elements()
		require.Len(t, got, 1)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(got[0].(*mmsg.ImageBytesElement).Buf))
		require.NoError(t, err)
		assert.Equal(t, 24, cfg.Width)
		assert.Equal(t, 144, cfg.Height)
		v.Set("imageMerge.mode", "off")
		assert.Len(t, mergeRenderedImages(name, nil, msg).Elements(), 4)
		v.Set("imageMerge.mode", "inherit")
	}
	// Actual built-in rendering must invoke the shared postprocessor.
	msg, err := LoadAndExec("notify.group.xhs.news.tmpl", map[string]interface{}{
		"is_video": false, "name": "test", "title": "title", "desc": "desc", "pictures": images, "url": "https://example.invalid/note",
	})
	require.NoError(t, err)
	count := 0
	for _, e := range msg.Elements() {
		if _, ok := e.(*mmsg.ImageBytesElement); ok {
			count++
		}
	}
	assert.Equal(t, 1, count)
	msg = mmsg.NewMSG().Append(mmsg.NewImage(nil, path), mmsg.NewImage(nil, path))
	assert.Len(t, mergeRenderedImages("trigger.group.poke.tmpl", nil, msg).Elements(), 2)
	// Dimension changes reach real rendering and invalidate cached compositions.
	v.Set("xhs.imageMerge.maxWidth", 12)
	v.Set("xhs.imageMerge.maxHeight", 24)
	msg, err = LoadAndExec("notify.group.xhs.news.tmpl", map[string]interface{}{
		"is_video": false, "name": "test", "title": "title", "desc": "desc", "pictures": images, "url": "https://example.invalid/note",
	})
	require.NoError(t, err)
	count = 0
	totalHeight := 0
	for _, e := range msg.Elements() {
		if img, ok := e.(*mmsg.ImageBytesElement); ok {
			c, _, err := image.DecodeConfig(bytes.NewReader(img.Buf))
			require.NoError(t, err)
			assert.Equal(t, 12, c.Width)
			assert.LessOrEqual(t, c.Height, 24)
			totalHeight += c.Height
			count++
		}
	}
	assert.Equal(t, 3, count)
	assert.Equal(t, 72, totalHeight)
}

func TestImplicitBilibiliOnlyProcessesLegacyGallery(t *testing.T) {
	old := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = old })
	config.GlobalConfig = &config.Config{Viper: viper.New()}
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 24, 24))))
	path := filepath.Join(t.TempDir(), "image.png")
	require.NoError(t, os.WriteFile(path, b.Bytes(), 0600))
	msg := mmsg.NewMSG()
	for range 4 {
		msg.Append(mmsg.NewImage(nil, path))
	}
	name := "notify.group.bilibili.news.tmpl"
	assert.Len(t, mergeRenderedImages(name, nil, msg).Elements(), 4)
	got := mergeRenderedImages(name, map[string]interface{}{"dynamic": testLegacyGallery{path: {Width: 500, Height: 500}}}, msg)
	assert.Len(t, got.Elements(), 1)
	data := map[string]interface{}{"dynamic": testLegacyImageData{
		urls: []string{path, path, path, path}, sizes: testLegacyGallery{path: {Width: 500, Height: 500}},
	}}
	preparedData, prepared := prepareLegacyImageMerge(name, data)
	require.NotEmpty(t, prepared)
	assert.Empty(t, data["dynamic"].(testLegacyImageData).image, "original data is not mutated")
	assert.Equal(t, prepared, preparedData.(map[string]interface{})["dynamic"].(testLegacyImageData).image)
	rendered := markPreparedImage(mmsg.NewMSG().Append(mmsg.NewImage(prepared)), prepared)
	assert.True(t, rendered.Elements()[0].(*mmsg.ImageBytesElement).IsMerged())
}
