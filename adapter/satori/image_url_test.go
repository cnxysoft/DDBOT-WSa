package satori

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageURLAttributesRoundTrip(t *testing.T) {
	uri := "https://example.invalid/image?appid=1407&rkey=a%2Bb&spec=0"
	content := `<img src="https://example.invalid/image?appid=1407&amp;rkey=a%2Bb&#38;spec=0"/>`
	segments := parseSatoriContent(content)
	require.Len(t, segments, 1)
	assert.Equal(t, uri, segments[0].Data["url"])
	segments = parseSatoriContentWithMap(content, nil, nil, "")
	require.Len(t, segments, 1)
	assert.Equal(t, uri, segments[0].Data["url"])
	// Decode one entity layer: the encoded literal &amp; remains in the value.
	assert.Equal(t, "a&amp;b", parseAttrs(`title="a&amp;amp;b"`)["title"])
}
