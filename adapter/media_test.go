package adapter

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedImageExclusiveBudget(t *testing.T) {
	image := &ImageSegment{Generated: true, File: "base64://" + base64.StdEncoding.EncodeToString(make([]byte, QQImageOutputLimitBytes-1))}
	require.NoError(t, ValidateGeneratedImages(image.ToSendingMessage()))
	image.File = "base64://" + base64.StdEncoding.EncodeToString(make([]byte, QQImageOutputLimitBytes))
	require.ErrorIs(t, ValidateGeneratedImages(image.ToSendingMessage()), ErrRequestRejected)
	image.Generated = false
	require.NoError(t, ValidateGeneratedImages(image.ToSendingMessage()), "raw originals retain existing behavior")
}

func TestGeneratedImageRejectedBeforeAnyMessageIsSent(t *testing.T) {
	m := &Messenger{}
	msg := &SendingMessage{Elements: []IMessageElement{
		&TextSegment{Content: "must not send before validation"},
		&ImageSegment{Generated: true, File: "base64://invalid"},
	}}
	// An adapter call would panic because no adapter was installed.
	assert.ErrorIs(t, m.SendGroupMessage(1, msg, "").Error, ErrRequestRejected)
	assert.ErrorIs(t, m.SendPrivateMessage(1, msg, "").Error, ErrRequestRejected)
	for _, file := range []string{"https://example.invalid/image", "base64://"} {
		msg.Elements = []IMessageElement{&ImageSegment{Generated: true, File: file}}
		assert.ErrorIs(t, ValidateGeneratedImages(msg), ErrRequestRejected)
	}
}
