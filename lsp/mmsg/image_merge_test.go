package mmsg

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"sync"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mergeRunImage(t *testing.T, c color.Color) *ImageBytesElement {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return NewImage(b.Bytes())
}

func TestMergeImageRunsKeepsTextGIFAndSingleImages(t *testing.T) {
	red := mergeRunImage(t, color.NRGBA{255, 0, 0, 255})
	blue := mergeRunImage(t, color.NRGBA{0, 0, 255, 255})
	var animated bytes.Buffer
	require.NoError(t, gif.Encode(&animated, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black}), nil))
	g := NewImage(animated.Bytes())
	msg := NewMSG().Text("before").Append(red, blue, g, red, blue).Text("between").Append(red).Text("after")
	got := msg.MergeImageRuns("always", "long", nil).Elements()
	require.Len(t, got, 7)
	assert.Equal(t, "before", got[0].(*adapter.TextSegment).Content)
	assert.Same(t, g, got[2])
	assert.Equal(t, "between", got[4].(*adapter.TextSegment).Content)
	assert.Same(t, red, got[5])
	assert.Equal(t, "after", got[6].(*adapter.TextSegment).Content)
	for _, index := range []int{1, 3} {
		merged := got[index].(*ImageBytesElement)
		assert.True(t, merged.merged)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(merged.Buf))
		require.NoError(t, err)
		assert.Equal(t, 32, cfg.Width)
		assert.Equal(t, 80, cfg.Height)
		packed := merged.PackToElement(NewGroupTarget(1)).(*adapter.ImageSegment)
		assert.True(t, packed.Generated)
		require.NoError(t, adapter.ValidateGeneratedImages(packed.ToSendingMessage()))
	}
	assert.Len(t, msg.Elements(), 9, "original message is not mutated")
}

func TestMergeImageRunsFailureAndBoundaries(t *testing.T) {
	valid := mergeRunImage(t, color.White)
	invalid := NewImage([]byte("invalid"))
	msg := NewMSG().Append(valid, invalid).Text("still here").Append(valid)
	assert.Equal(t, msg.Elements(), msg.MergeImageRuns("always", "long", nil).Elements())
	assert.Same(t, msg, msg.MergeImageRuns("off", "grid", nil))
	cut := &CutElement{}
	msg = NewMSG().Append(valid, cut, valid)
	assert.Equal(t, msg.Elements(), msg.MergeImageRuns("always", "grid", nil).Elements())
	msg = NewMSG().Append(valid, valid)
	assert.Equal(t, msg.Elements(), msg.MergeImageRuns("auto", "grid", nil).Elements())
}

func TestMergeLegacyGalleryUsesMetadataAndRestrictsURLs(t *testing.T) {
	// Local file loading is covered by template integration tests. Metadata
	// eligibility must never cause downloads for a gallery rejected by policy.
	gallery := make(map[string]utils.ImageSize)
	msg := NewMSG()
	for _, url := range []string{"https://example.invalid/1", "https://example.invalid/2", "https://example.invalid/3", "https://example.invalid/4"} {
		gallery[url] = utils.ImageSize{Width: 500, Height: 1000}
		msg.Append(NewImage(nil, url))
	}
	avatar := NewImage(nil, "https://example.invalid/avatar")
	msg.Append(avatar)
	assert.Equal(t, msg.Elements(), msg.MergeImageRuns("auto", "grid", gallery).Elements())
}

func TestGalleryCacheSeparatesLayoutsAndConcurrentRenders(t *testing.T) {
	img := mergeRunImage(t, color.White)
	data := [][]byte{img.Buf, img.Buf}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			pages, err := composeGallery(data, utils.ImageMergeOptions{Layout: "long"})
			assert.NoError(t, err)
			if len(pages) != 1 {
				t.Errorf("unexpected page count %d", len(pages))
				return
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(pages[0]))
			assert.NoError(t, err)
			assert.Equal(t, 80, cfg.Height)
		})
	}
	wg.Wait()
	grid, err := composeGallery(data, utils.ImageMergeOptions{Layout: "grid"})
	require.NoError(t, err)
	require.Len(t, grid, 1)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(grid[0]))
	require.NoError(t, err)
	assert.Equal(t, 1440, cfg.Width)
	assert.Equal(t, 720, cfg.Height)
	// Changing dimensions must not reuse an image from the prior settings.
	pages, err := composeGallery(data, utils.ImageMergeOptions{Layout: "long", Width: 16, MaxHeight: 20})
	require.NoError(t, err)
	require.Len(t, pages, 2)
	for _, page := range pages {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(page))
		require.NoError(t, err)
		assert.Equal(t, 16, cfg.Width)
		assert.Equal(t, 20, cfg.Height)
	}
	_, err = composeGallery(data, utils.ImageMergeOptions{Layout: "long", MaxBytes: 1})
	require.Error(t, err, "a smaller transport budget must not reuse a cached larger image")
}
