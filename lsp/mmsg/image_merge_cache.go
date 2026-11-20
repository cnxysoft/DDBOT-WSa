package mmsg

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/cnxysoft/DDBOT-WSa/utils"
	lru "github.com/hashicorp/golang-lru"
	"golang.org/x/sync/singleflight"
)

// Reuse small galleries across group renders without retaining an unbounded
// amount of image data. Large long-image pages only share concurrent work.
var mergedGalleryCache, _ = lru.New(3)
var mergedGalleryFlight singleflight.Group

func composeGallery(data [][]byte, options utils.ImageMergeOptions) ([][]byte, error) {
	h := sha256.New()
	h.Write([]byte(options.Layout))
	var size [8]byte
	for _, n := range []int{options.Width, options.MaxHeight, options.MaxPixels, options.MaxBytes} {
		binary.LittleEndian.PutUint64(size[:], uint64(n))
		h.Write(size[:])
	}
	for _, b := range data {
		binary.LittleEndian.PutUint64(size[:], uint64(len(b)))
		h.Write(size[:])
		h.Write(b)
	}
	key := string(h.Sum(nil))
	if cached, ok := mergedGalleryCache.Get(key); ok {
		return cached.([][]byte), nil
	}
	value, err, _ := mergedGalleryFlight.Do(key, func() (interface{}, error) {
		if cached, ok := mergedGalleryCache.Get(key); ok {
			return cached, nil
		}
		pages, err := utils.ComposeImages(data, options)
		if err != nil {
			return nil, err
		}
		bytes := 0
		for _, page := range pages {
			bytes += len(page)
		}
		if bytes <= 8*1024*1024 {
			mergedGalleryCache.Add(key, pages)
		}
		return pages, nil
	})
	if err != nil {
		return nil, err
	}
	// Cached buffers are immutable: image transformations replace Buf rather
	// than editing the shared byte slice.
	return value.([][]byte), nil
}
