package utils

import (
	"bytes"
	"image"
	_ "image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageTestURL(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	require.NoError(t, jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1080, 2340)), nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data.Bytes())
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestImageNormSize(t *testing.T) {
	b, err := ImageGet(imageTestURL(t))
	assert.Nil(t, err)
	assert.NotEmpty(t, b)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	assert.Nil(t, err)
	assert.EqualValues(t, 1080, cfg.Width)
	assert.EqualValues(t, 2340, cfg.Height)

	b, err = ImageNormSize(b)
	assert.Nil(t, err)
	cfg, _, err = image.DecodeConfig(bytes.NewReader(b))
	assert.Nil(t, err)
	assert.EqualValues(t, 553, cfg.Width)
	assert.EqualValues(t, 1200, cfg.Height)
}

func TestImageGet(t *testing.T) {
	imageUrl := imageTestURL(t)
	_, err := ImageGetWithoutCache(imageUrl)
	assert.Nil(t, err)
	b, err := ImageGet(imageUrl)
	assert.Nil(t, err)
	assert.NotEmpty(t, b)
	b, err = ImageNormSize(b)
	assert.Nil(t, err)
	assert.NotEmpty(t, b)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	assert.Nil(t, err)
	assert.EqualValues(t, 553, cfg.Width)
	assert.EqualValues(t, 1200, cfg.Height)

	b, err = ImageResize(b, 500, 500)
	assert.Nil(t, err)
	cfg, _, err = image.DecodeConfig(bytes.NewReader(b))
	assert.Nil(t, err)
	assert.EqualValues(t, 500, cfg.Width)
	assert.EqualValues(t, 500, cfg.Height)

	format, err := ImageFormat(b)
	assert.Nil(t, err)
	assert.EqualValues(t, "jpeg", format)

	_, err = ImageReserve(b)
	assert.NotNil(t, err)

	img, _, err := image.Decode(bytes.NewReader(b))
	assert.Nil(t, err)
	subImg := SubImage(img, image.Rect(0, 0, 10, 10))
	assert.NotNil(t, subImg)
}

func TestImageSuffix(t *testing.T) {
	assert.True(t, ImageSuffix("png"))
	assert.True(t, ImageSuffix("jpg"))
	assert.False(t, ImageSuffix("tar"))
}
