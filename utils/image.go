package utils

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils/blockCache"
	"github.com/ericpauley/go-quantize/quantize"
	"github.com/nfnt/resize"
)

func encodeImage(img image.Image, format string, resizedImageBuffer *bytes.Buffer) (err error) {
	switch format {
	case "jpeg":
		err = jpeg.Encode(resizedImageBuffer, img, &jpeg.Options{Quality: 100})
	case "gif":
		err = gif.Encode(resizedImageBuffer, img, &gif.Options{
			Quantizer: quantize.MedianCutQuantizer{},
		})
	case "png":
		err = png.Encode(resizedImageBuffer, img)
	default:
		err = fmt.Errorf("unknown format %v", format)
	}
	return err
}

var imageGetCache = blockCache.NewBlockCache(16, 25)

// ImageGet 默认会对相同的url使用缓存
func ImageGet(url string, opt ...requests.Option) ([]byte, error) {
	if url == "" {
		return nil, errors.New("empty url")
	}
	result := imageGetCache.WithCacheDo(url, func() blockCache.ActionResult {
		opts := []requests.Option{
			requests.TimeoutOption(time.Second * 15),
			requests.RetryOption(3),
		}
		opts = append(opts, opt...)

		var body = new(bytes.Buffer)

		err := requests.Get(url, nil, body, opts...)
		if err != nil {
			return blockCache.NewResultWrapper(nil, err)
		}
		return blockCache.NewResultWrapper(body.Bytes(), nil)
	})
	if result.Err() != nil {
		return nil, result.Err()
	}
	return result.Result().([]byte), nil
}

// ImageGetWithoutCache 默认情况下相同的url会存在缓存，
// 如果url会随机返回不同的图片，则需要禁用缓存
// 这个函数就是不使用缓存的版本
func ImageGetWithoutCache(url string, opt ...requests.Option) ([]byte, error) {
	if url == "" {
		return nil, errors.New("empty url")
	}
	var result []byte
	var err error
	opts := []requests.Option{
		requests.TimeoutOption(time.Second * 15),
		requests.RetryOption(3),
	}
	opts = append(opts, opt...)
	var body = new(bytes.Buffer)
	err = requests.Get(url, nil, body, opts...)
	if err != nil {
		return nil, err
	}
	result, err = body.Bytes(), nil
	return result, err
}

func ImageNormSize(origImage []byte) ([]byte, error) {
	dImage, format, err := image.Decode(bytes.NewReader(origImage))
	if err != nil {
		return nil, fmt.Errorf("image decode failed %v", err)
	}
	resizedImage := resize.Thumbnail(1200, 1200, dImage, resize.Lanczos3)
	buf := bytes.NewBuffer(nil)
	err = encodeImage(resizedImage, format, buf)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ImageResize(origImage []byte, width, height uint) ([]byte, error) {
	dImage, format, err := image.Decode(bytes.NewReader(origImage))
	if err != nil {
		return nil, fmt.Errorf("image decode failed %v", err)
	}
	resizedImage := resize.Resize(width, height, dImage, resize.Lanczos3)
	buf := bytes.NewBuffer(nil)
	err = encodeImage(resizedImage, format, buf)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ImageFormat(origImage []byte) (string, error) {
	_, format, err := image.DecodeConfig(bytes.NewReader(origImage))
	return format, err
}

func ImageReserve(imgBytes []byte) ([]byte, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", FuncName()).Tracef("cost %v", ed.Sub(st))
	}()

	format, err := ImageFormat(imgBytes)
	if err != nil {
		return nil, err
	} else if format != "gif" {
		return nil, errors.New("不是动图")
	}
	img, err := DecodeGifWithCompleteFrame(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, err
	}
	length := len(img.Image)
	for idx := range img.Image {
		oidx := length - 1 - idx
		if idx >= oidx {
			break
		}
		tmp := img.Image[idx]
		img.Image[idx] = img.Image[oidx]
		img.Image[oidx] = tmp
	}
	var result = bytes.NewBuffer(nil)
	err = gif.EncodeAll(result, img)
	if err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func DecodeGifWithCompleteFrame(r io.Reader) (g *gif.GIF, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Error while decoding: %s", r)
		}
	}()
	g, err = gif.DecodeAll(r)
	if err != nil {
		return nil, err
	}

	imgWidth, imgHeight := GetGifDimensions(g)

	overpaintImage := image.NewRGBA(image.Rect(0, 0, imgWidth, imgHeight))

	for index, srcImg := range g.Image {
		switch g.Disposal[index] {
		case gif.DisposalNone:
			draw.Draw(overpaintImage, overpaintImage.Bounds(), srcImg, image.Point{}, draw.Over)
			draw.Draw(srcImg, overpaintImage.Bounds(), overpaintImage, image.Point{}, draw.Src)
		case gif.DisposalBackground:
			// TODO is it correct?
			//draw.Draw(overpaintImage, overpaintImage.Bounds(), srcImg, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			// TODO how?
			draw.Draw(overpaintImage, overpaintImage.Bounds(), srcImg, image.Point{}, draw.Over)
			draw.Draw(srcImg, overpaintImage.Bounds(), overpaintImage, image.Point{}, draw.Src)
		}

	}

	return g, nil
}

func GetGifDimensions(gif *gif.GIF) (x, y int) {
	var lowestX int
	var lowestY int
	var highestX int
	var highestY int

	for _, img := range gif.Image {
		if img.Rect.Min.X < lowestX {
			lowestX = img.Rect.Min.X
		}
		if img.Rect.Min.Y < lowestY {
			lowestY = img.Rect.Min.Y
		}
		if img.Rect.Max.X > highestX {
			highestX = img.Rect.Max.X
		}
		if img.Rect.Max.Y > highestY {
			highestY = img.Rect.Max.Y
		}
	}

	return highestX - lowestX, highestY - lowestY
}

func ImageSuffix(name string) bool {
	for _, suf := range []string{"jpg", "png", "jpeg"} {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

// MergeImages is the legacy single-page grid API used by picm/picmx.
// Gallery processing uses ComposeImages so large collections can return pages.
func MergeImages(images [][]byte) ([]byte, error) {
	pages, err := ComposeImages(images, ImageMergeOptions{Layout: ImageLayoutGrid})
	if err != nil {
		return nil, err
	}
	if len(pages) != 1 {
		return nil, fmt.Errorf("grid requires %d pages; use ComposeImages", len(pages))
	}
	return pages[0], nil
}

func SubImage(img image.Image, r image.Rectangle) image.Image {
	var bgSize = image.Rect(0, 0, r.Dx(), r.Dy())
	var bg = image.NewNRGBA(bgSize)
	draw.Draw(bg, bg.Bounds(), img, r.Min, draw.Src)
	return bg
}
