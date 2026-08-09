package onebot

import (
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/stretchr/testify/assert"
)

func TestContainsURIWithTypedMessageSegments(t *testing.T) {
	params := adapter.BuildMessageParams([]adapter.MessageSegment{
		{
			Type: "text",
			Data: map[string]interface{}{"text": "news"},
		},
		{
			Type: "image",
			Data: map[string]interface{}{"file": "https://example.com/image.png"},
		},
	})

	assert.True(t, containsURI(params))
}

func TestCalcSendTimeoutForTypedMessageSegments(t *testing.T) {
	defaultTimeout := 10 * time.Second
	a := NewOneBotAdapter(&adapter.AdapterConfig{Timeout: defaultTimeout})

	remoteImage := adapter.BuildMessageParams([]adapter.MessageSegment{{
		Type: "image",
		Data: map[string]interface{}{"url": "https://example.com/image.png"},
	}})
	plainText := adapter.BuildMessageParams([]adapter.MessageSegment{{
		Type: "text",
		Data: map[string]interface{}{"text": "hello"},
	}})

	assert.Equal(t, uriMessageTimeout, a.calcSendTimeout("send_group_msg", remoteImage))
	assert.Less(t, uriMessageTimeout, time.Minute)
	assert.Equal(t, defaultTimeout, a.calcSendTimeout("send_group_msg", plainText))
}
