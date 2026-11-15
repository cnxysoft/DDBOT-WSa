package qqofficial

import (
	"encoding/base64"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/stretchr/testify/require"
)

func TestGeneratedImageBudgetMatchesInclusiveDirectUploadLimit(t *testing.T) {
	a := &QQOfficialAdapter{}
	m := &adapter.Messenger{Adapter: a}
	img := &adapter.ImageSegment{Generated: true,
		File: "base64://" + base64.StdEncoding.EncodeToString(make([]byte, MaxMediaDirectUploadBytes))}
	require.NoError(t, adapter.ValidateGeneratedImages(img.ToSendingMessage(), m.GeneratedImageOutputLimitBytes()))
	img.File = "base64://" + base64.StdEncoding.EncodeToString(make([]byte, MaxMediaDirectUploadBytes+1))
	require.ErrorIs(t, adapter.ValidateGeneratedImages(img.ToSendingMessage(), m.GeneratedImageOutputLimitBytes()), adapter.ErrRequestRejected)
}
