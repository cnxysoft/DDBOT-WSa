package mmsg

import (
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestImage(t *testing.T) {
	var im *ImageBytesElement
	e := im.PackToElement(NewGroupTarget(0))
	assert.Equal(t, e.(*adapter.TextSegment).Content, "[空图片]\n")

	im = NewImage(nil)
	im.Alternative("test")
	assert.EqualValues(t, ImageBytes, im.Type())
	e = im.PackToElement(NewGroupTarget(0))
	assert.Equal(t, e.(*adapter.TextSegment).Content, "test\n")

	assert.NotPanics(t, func() {
		im.Norm().Resize(100, 100)
	})
}
