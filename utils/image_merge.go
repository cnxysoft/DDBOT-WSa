package utils

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/nfnt/resize"
)

const (
	ImageLayoutGrid = "grid"
	ImageLayoutLong = "long"
	// These are allocation/output safeguards, not claimed QQ resolution limits.
	MergeImageWidth           = 1440
	MergeImageMaxHeight       = 16000
	MergeImageMaxPixels       = 24_000_000
	MergeImageMaxSourcePixels = 64_000_000
)

type ImageSize struct{ Width, Height int }

// ShouldMergeImages preserves Bilibili's original auto/only9 rules. A single
// image is never recompressed by automatic gallery processing.
func ShouldMergeImages(mode string, sizes []ImageSize) bool {
	if len(sizes) < 2 {
		return false
	}
	switch mode {
	case "always":
		return true
	case "only9":
		return len(sizes) == 9
	case "auto":
	default:
		return false
	}
	if len(sizes) <= 3 {
		return false
	}
	if len(sizes) == 9 {
		return true
	}
	counts := make(map[int64]int)
	for _, s := range sizes {
		if s.Width > 250 && int64(s.Height) > 3*int64(s.Width) {
			return true
		}
		if s.Width <= 0 || s.Height <= 0 {
			continue
		}
		small, large := min(s.Width, s.Height), max(s.Width, s.Height)
		if float64(small)/float64(large) >= .95 {
			counts[int64(s.Width)*int64(s.Height)]++
		}
	}
	if len(sizes) == 4 || len(sizes) == 6 {
		for _, n := range counts {
			if 2*n > len(sizes) {
				return true
			}
		}
	}
	return false
}

type ImageMergeOptions struct {
	Layout string
	// MaxBytes is exclusive. Zero uses the QQ compatibility budget; callers may
	// lower it but cannot raise it above that budget.
	MaxBytes                    int
	Width, MaxHeight, MaxPixels int
}

func (o ImageMergeOptions) normalized() (ImageMergeOptions, error) {
	if o.Layout == "" {
		o.Layout = ImageLayoutGrid
	}
	if o.Layout != ImageLayoutGrid && o.Layout != ImageLayoutLong {
		return o, fmt.Errorf("unknown image layout %q", o.Layout)
	}
	if o.MaxBytes <= 0 || o.MaxBytes > adapter.QQImageOutputLimitBytes {
		o.MaxBytes = adapter.QQImageOutputLimitBytes
	}
	if o.Width <= 0 || o.Width > MergeImageWidth {
		o.Width = MergeImageWidth
	}
	if o.MaxHeight <= 0 || o.MaxHeight > MergeImageMaxHeight {
		o.MaxHeight = MergeImageMaxHeight
	}
	if o.MaxPixels <= 0 || o.MaxPixels > MergeImageMaxPixels {
		o.MaxPixels = MergeImageMaxPixels
	}
	o.MaxHeight = min(o.MaxHeight, o.MaxPixels/o.Width)
	if o.MaxHeight < 1 {
		return o, fmt.Errorf("image pixel budget too small")
	}
	return o, nil
}

func mergeImageConfigs(images [][]byte) ([]image.Config, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("no image given")
	}
	configs := make([]image.Config, len(images))
	for i, data := range images {
		c, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("image %d config: %w", i, err)
		}
		if c.Width <= 0 || c.Height <= 0 || int64(c.Width) > MergeImageMaxSourcePixels/int64(c.Height) {
			return nil, fmt.Errorf("image %d exceeds source pixel budget", i)
		}
		configs[i] = c
	}
	return configs, nil
}

// ComposeImages renders bounded pages and checks actual encoded bytes before
// returning anything. Long pages split without cropping away content. Sources
// are decoded once each; encoding retries never download or decode them again.
func ComposeImages(images [][]byte, options ImageMergeOptions) ([][]byte, error) {
	o, err := options.normalized()
	if err != nil {
		return nil, err
	}
	configs, err := mergeImageConfigs(images)
	if err != nil {
		return nil, err
	}
	if o.Layout == ImageLayoutLong {
		return composeLongImages(images, configs, o)
	}
	return composeGridImages(images, configs, o)
}

func whiteCanvas(w, h int) *image.NRGBA {
	bg := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(bg, bg.Bounds(), image.White, image.Point{}, draw.Src)
	return bg
}

func composeGridImages(images [][]byte, configs []image.Config, o ImageMergeOptions) ([][]byte, error) {
	columns := 2
	for columns < 6 && columns*columns < len(images) {
		columns++
	}
	cell := o.Width / columns
	if cell < 1 || o.MaxHeight < cell {
		return nil, fmt.Errorf("grid dimensions exceed image budget")
	}
	rowsPerPage := o.MaxHeight / cell
	var result [][]byte
	for start := 0; start < len(images); {
		end := min(len(images), start+rowsPerPage*columns)
		bg := whiteCanvas(o.Width, ((end-start+columns-1)/columns)*cell)
		for i := start; i < end; i++ {
			src, _, err := image.Decode(bytes.NewReader(images[i]))
			if err != nil {
				return nil, fmt.Errorf("image %d decode: %w", i, err)
			}
			c := configs[i]
			side := min(c.Width, c.Height)
			x := (c.Width - side) / 2 // Keep legacy top crop for portrait pictures.
			crop := imageCrop(src, image.Rect(x, 0, x+side, side))
			tile := resize.Resize(uint(cell), uint(cell), crop, resize.Lanczos3)
			pos := image.Pt(((i-start)%columns)*cell, ((i-start)/columns)*cell)
			draw.Draw(bg, image.Rect(pos.X, pos.Y, pos.X+cell, pos.Y+cell), tile, tile.Bounds().Min, draw.Src)
		}
		pages, err := encodeMergePage(bg, o.MaxBytes, false)
		if err != nil {
			return nil, err
		}
		result = append(result, pages...)
		start = end
	}
	return result, nil
}

func composeLongImages(images [][]byte, configs []image.Config, o ImageMergeOptions) ([][]byte, error) {
	width := 1
	for _, c := range configs {
		width = max(width, min(o.Width, c.Width))
	}
	var result [][]byte
	var bg *image.NRGBA
	y := 0
	flush := func() error {
		if y == 0 {
			return nil
		}
		pages, err := encodeMergePage(bg.SubImage(image.Rect(0, 0, width, y)), o.MaxBytes, true)
		if err != nil {
			return err
		}
		result = append(result, pages...)
		bg, y = nil, 0
		return nil
	}
	for i, data := range images {
		src, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("image %d decode: %w", i, err)
		}
		c := configs[i]
		w := min(width, c.Width)
		h := max(1, int(int64(c.Height)*int64(w)/int64(c.Width)))
		for offset := 0; offset < h; {
			if bg == nil {
				bg = whiteCanvas(width, o.MaxHeight)
			}
			take := min(h-offset, o.MaxHeight-y)
			from := int(int64(offset) * int64(c.Height) / int64(h))
			to := int(int64(offset+take) * int64(c.Height) / int64(h))
			to = max(from+1, to)
			crop := imageCrop(src, image.Rect(0, from, c.Width, min(to, c.Height)))
			tile := resize.Resize(uint(w), uint(take), crop, resize.Lanczos3)
			draw.Draw(bg, image.Rect((width-w)/2, y, (width-w)/2+w, y+take), tile, tile.Bounds().Min, draw.Over)
			y += take
			offset += take
			if y == o.MaxHeight {
				if err := flush(); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return result, nil
}

func imageCrop(src image.Image, r image.Rectangle) image.Image {
	r = r.Add(src.Bounds().Min)
	if s, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	return SubImage(src, r)
}

func encodeMergePage(src image.Image, limit int, split bool) ([][]byte, error) {
	for _, quality := range []int{100, 90} {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, src, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		if b.Len() < limit {
			return [][]byte{b.Bytes()}, nil
		}
	}
	if split && src.Bounds().Dy() > 1 {
		r := src.Bounds()
		middle := r.Min.Y + r.Dy()/2
		a, err := encodeMergePage(imageCrop(src, image.Rect(0, 0, r.Dx(), r.Dy()/2)), limit, true)
		if err != nil {
			return nil, err
		}
		b, err := encodeMergePage(imageCrop(src, image.Rect(0, middle-r.Min.Y, r.Dx(), r.Dy())), limit, true)
		if err != nil {
			return nil, err
		}
		return append(a, b...), nil
	}
	// A grid can reduce resolution; long pages retain content and split first.
	if src.Bounds().Dx() == 1 && src.Bounds().Dy() == 1 {
		return nil, fmt.Errorf("image byte budget too small: %d", limit)
	}
	w, h := max(1, src.Bounds().Dx()*3/4), max(1, src.Bounds().Dy()*3/4)
	return encodeMergePage(resize.Resize(uint(w), uint(h), src, resize.Lanczos3), limit, split)
}
