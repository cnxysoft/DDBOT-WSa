package utils

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mergeTestPNG(t *testing.T, w, h int, fill color.Color, noise bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := fill
			if noise {
				c = color.NRGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestShouldMergeImagesLegacyRules(t *testing.T) {
	square := ImageSize{500, 500}
	portrait := ImageSize{500, 1000}
	tests := []struct {
		mode  string
		sizes []ImageSize
		want  bool
	}{
		{"always", []ImageSize{square}, false},
		{"always", []ImageSize{square, portrait}, true},
		{"off", make([]ImageSize, 9), false},
		{"only9", make([]ImageSize, 9), true},
		{"only9", make([]ImageSize, 8), false},
		{"auto", []ImageSize{square, square, square}, false},
		{"auto", []ImageSize{square, square, square, portrait}, true},
		{"auto", []ImageSize{square, square, portrait, portrait}, false},
		{"auto", []ImageSize{portrait, portrait, portrait, {251, 754}, portrait}, true},
		{"auto", make([]ImageSize, 9), true},
		{"auto", []ImageSize{{0, 0}, {0, 0}, {0, 0}, {0, 0}}, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ShouldMergeImages(tt.mode, tt.sizes), "%s %v", tt.mode, tt.sizes)
	}
}

func TestGridPreservesLayoutAndCrop(t *testing.T) {
	red := mergeTestPNG(t, 30, 60, color.NRGBA{255, 0, 0, 255}, false)
	green := mergeTestPNG(t, 60, 30, color.NRGBA{0, 255, 0, 255}, false)
	pages, err := ComposeImages([][]byte{red, green, red, green, red, green, red, green, red}, ImageMergeOptions{})
	require.NoError(t, err)
	require.Len(t, pages, 1)
	img, err := jpeg.Decode(bytes.NewReader(pages[0]))
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 1440, 1440), img.Bounds())
	r, g, _, _ := img.At(240, 240).RGBA()
	assert.Greater(t, r, uint32(60000))
	assert.Less(t, g, uint32(1000))
	r, g, _, _ = img.At(720, 240).RGBA()
	assert.Less(t, r, uint32(1000))
	assert.Greater(t, g, uint32(60000))
	assert.Less(t, len(pages[0]), adapter.QQImageOutputLimitBytes)
}

func TestGridKeepsLegacyTopAndCenteredCrop(t *testing.T) {
	// Crops already equal to a grid cell exercise non-zero SubImage bounds:
	// resize returns them directly rather than allocating a new origin.
	wide := image.NewNRGBA(image.Rect(0, 0, 1440, 480))
	portrait := image.NewNRGBA(image.Rect(0, 0, 480, 1440))
	colors := []color.Color{color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{0, 0, 255, 255}}
	for i, c := range colors {
		draw.Draw(wide, image.Rect(i*480, 0, (i+1)*480, 480), &image.Uniform{C: c}, image.Point{}, draw.Src)
		draw.Draw(portrait, image.Rect(0, i*480, 480, (i+1)*480), &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	var w, p bytes.Buffer
	require.NoError(t, png.Encode(&w, wide))
	require.NoError(t, png.Encode(&p, portrait))
	pages, err := ComposeImages([][]byte{w.Bytes(), p.Bytes(), w.Bytes(), p.Bytes(), w.Bytes()}, ImageMergeOptions{})
	require.NoError(t, err)
	require.Len(t, pages, 1)
	img, err := jpeg.Decode(bytes.NewReader(pages[0]))
	require.NoError(t, err)
	r, g, b, _ := img.At(240, 240).RGBA()
	assert.Greater(t, g, r)
	assert.Greater(t, g, b)
	r, g, b, _ = img.At(720, 240).RGBA()
	assert.Greater(t, r, g)
	assert.Greater(t, r, b)
}

func TestLongPagesPreserveOrderAndEveryRow(t *testing.T) {
	red := mergeTestPNG(t, 64, 150, color.NRGBA{255, 0, 0, 255}, false)
	blue := mergeTestPNG(t, 64, 90, color.NRGBA{0, 0, 255, 255}, false)
	pages, err := ComposeImages([][]byte{red, blue}, ImageMergeOptions{Layout: ImageLayoutLong, MaxHeight: 100})
	require.NoError(t, err)
	require.Len(t, pages, 3)
	heights := []int{100, 100, 40}
	for i, page := range pages {
		img, err := jpeg.Decode(bytes.NewReader(page))
		require.NoError(t, err)
		assert.Equal(t, image.Rect(0, 0, 64, heights[i]), img.Bounds())
		r, _, b, _ := img.At(32, 10).RGBA()
		if i < 2 {
			assert.Greater(t, r, b)
		} else {
			assert.Greater(t, b, r)
		}
		if i == 1 {
			r, _, b, _ = img.At(32, 80).RGBA()
			assert.Greater(t, b, r)
		}
	}
}

func TestEncodedByteBudgetSplitsLongPages(t *testing.T) {
	input := mergeTestPNG(t, 128, 300, color.White, true)
	base, err := ComposeImages([][]byte{input, input}, ImageMergeOptions{Layout: ImageLayoutLong})
	require.NoError(t, err)
	require.Len(t, base, 1)
	// Equality must not pass the exclusive budget.
	pages, err := ComposeImages([][]byte{input, input}, ImageMergeOptions{Layout: ImageLayoutLong, MaxBytes: len(base[0])})
	require.NoError(t, err)
	for _, page := range pages {
		assert.Less(t, len(page), len(base[0]))
	}
	pages, err = ComposeImages([][]byte{input, input}, ImageMergeOptions{Layout: ImageLayoutLong, MaxBytes: 12000})
	require.NoError(t, err)
	require.Greater(t, len(pages), 1)
	height := 0
	for _, page := range pages {
		assert.Less(t, len(page), 12000)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(page))
		require.NoError(t, err)
		assert.Equal(t, 128, cfg.Width)
		height += cfg.Height
	}
	assert.Equal(t, 600, height)
}

func TestLongMixedDimensionsPreserveScalePaddingAndPageOrder(t *testing.T) {
	wide := image.NewNRGBA(image.Rect(0, 0, 240, 80))
	for i, c := range []color.Color{
		color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{0, 0, 255, 255},
	} {
		draw.Draw(wide, image.Rect(i*80, 0, (i+1)*80, 80), &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, wide))
	narrow := mergeTestPNG(t, 32, 96, color.NRGBA{255, 0, 0, 255}, false)
	portrait := mergeTestPNG(t, 64, 200, color.NRGBA{0, 0, 255, 255}, false)
	pages, err := ComposeImages([][]byte{encoded.Bytes(), narrow, portrait}, ImageMergeOptions{
		Layout: ImageLayoutLong, Width: 120, MaxHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, pages, 4)
	var decoded []image.Image
	totalHeight := 0
	for i, page := range pages {
		img, err := jpeg.Decode(bytes.NewReader(page))
		require.NoError(t, err)
		wantHeight := 100
		if i == 3 {
			wantHeight = 36
		}
		assert.Equal(t, image.Rect(0, 0, 120, wantHeight), img.Bounds())
		assert.Less(t, len(page), adapter.QQImageOutputLimitBytes)
		decoded = append(decoded, img)
		totalHeight += img.Bounds().Dy()
	}
	// Only the wide source shrinks (240x80 -> 120x40). Narrow sources retain
	// their width and height, centered on white, including across page cuts.
	assert.Equal(t, 40+96+200, totalHeight)
	assertRGB := func(x, y int, want color.NRGBA) {
		t.Helper()
		r, g, b, _ := decoded[y/100].At(x, y%100).RGBA()
		assert.InDelta(t, want.R, r>>8, 8, "red at (%d,%d)", x, y)
		assert.InDelta(t, want.G, g>>8, 8, "green at (%d,%d)", x, y)
		assert.InDelta(t, want.B, b>>8, 8, "blue at (%d,%d)", x, y)
	}
	for _, y := range []int{8, 32} {
		assertRGB(8, y, color.NRGBA{255, 0, 0, 255})
		assertRGB(60, y, color.NRGBA{0, 255, 0, 255})
		assertRGB(111, y, color.NRGBA{0, 0, 255, 255})
	}
	for _, y := range []int{48, 96, 104, 128} {
		assertRGB(40, y, color.NRGBA{255, 255, 255, 255})
		assertRGB(48, y, color.NRGBA{255, 0, 0, 255})
		assertRGB(71, y, color.NRGBA{255, 0, 0, 255})
		assertRGB(80, y, color.NRGBA{255, 255, 255, 255})
	}
	for _, y := range []int{144, 192, 208, 296, 304, 328} {
		assertRGB(20, y, color.NRGBA{255, 255, 255, 255})
		assertRGB(36, y, color.NRGBA{0, 0, 255, 255})
		assertRGB(84, y, color.NRGBA{0, 0, 255, 255})
		assertRGB(100, y, color.NRGBA{255, 255, 255, 255})
	}
}

func TestGridByteAndPixelBudgets(t *testing.T) {
	input := mergeTestPNG(t, 64, 64, color.White, true)
	pages, err := ComposeImages([][]byte{input, input, input, input}, ImageMergeOptions{MaxBytes: 2000, Width: 128})
	require.NoError(t, err)
	for _, page := range pages {
		assert.Less(t, len(page), 2000)
	}
	pages, err = ComposeImages([][]byte{input, input, input, input}, ImageMergeOptions{Width: 128, MaxPixels: 8192})
	require.NoError(t, err)
	require.Len(t, pages, 2)
	for _, page := range pages {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(page))
		require.NoError(t, err)
		assert.LessOrEqual(t, cfg.Width*cfg.Height, 8192)
	}
	_, err = ComposeImages([][]byte{input}, ImageMergeOptions{Layout: "bad"})
	require.Error(t, err)
	_, err = ComposeImages([][]byte{nil}, ImageMergeOptions{})
	require.Error(t, err)
	_, err = ComposeImages(nil, ImageMergeOptions{})
	require.Error(t, err)
	_, err = ComposeImages([][]byte{input}, ImageMergeOptions{MaxBytes: 1, Width: 8})
	require.Error(t, err)
}

func TestImageMergeCustomDimensionsKeepHardBudgets(t *testing.T) {
	defaults, err := (ImageMergeOptions{}).normalized()
	require.NoError(t, err)
	assert.Equal(t, 1440, defaults.Width)
	assert.Equal(t, 16000, defaults.MaxHeight)
	assert.Equal(t, 24_000_000, defaults.MaxPixels)
	raised, err := (ImageMergeOptions{Width: 100000, MaxHeight: 100000, MaxBytes: 100_000_000}).normalized()
	require.NoError(t, err)
	assert.Equal(t, 29999, raised.Width)
	assert.Equal(t, 29999, raised.MaxHeight)
	assert.Equal(t, 48_000_000, raised.MaxPixels)
	assert.Equal(t, adapter.QQImageOutputLimitBytes, raised.MaxBytes)
	// A higher requested width must not reduce page height when actual sources
	// are narrower; account for pixels using the width that will be rendered.
	input := mergeTestPNG(t, 32, 9000, color.White, false)
	pages, err := ComposeImages([][]byte{input, input}, ImageMergeOptions{
		Layout: ImageLayoutLong, Width: 29999, MaxHeight: 28000,
	})
	require.NoError(t, err)
	require.Len(t, pages, 1)
	c, _, err := image.DecodeConfig(bytes.NewReader(pages[0]))
	require.NoError(t, err)
	assert.Equal(t, 32, c.Width)
	assert.Equal(t, 18000, c.Height)
	// Enforce the configured pixel budget in both layouts, even when it is
	// smaller than a requested row or grid cell.
	for _, layout := range []string{ImageLayoutLong, ImageLayoutGrid} {
		pages, err := ComposeImages([][]byte{input, input}, ImageMergeOptions{
			Layout: layout, Width: 2000, MaxHeight: 28000, MaxPixels: 4000,
		})
		require.NoError(t, err)
		for _, page := range pages {
			c, _, err := image.DecodeConfig(bytes.NewReader(page))
			require.NoError(t, err)
			assert.LessOrEqual(t, c.Width*c.Height, 4000)
			assert.LessOrEqual(t, c.Height, 28000)
		}
	}
}
