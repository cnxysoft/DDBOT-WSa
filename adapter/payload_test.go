package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func payloadJSONSize(t *testing.T, value interface{}) int {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return len(encoded)
}

func TestSplitChunksByPayload(t *testing.T) {
	// JSON escaping must count toward the limit, not just the source string.
	segment := MessageSegment{Type: "image", Data: map[string]interface{}{"file": strings.Repeat("<&\"\n", 20)}}
	two := []MessageSegment{segment, segment}
	limit := payloadJSONSize(t, two)
	chunks := [][]MessageSegment{{segment, segment, segment}, {segment}}
	result := splitChunksByPayload(chunks, limit)
	require.Len(t, result, 3)
	assert.Equal(t, two, result[0])
	assert.Equal(t, []MessageSegment{segment}, result[1])
	assert.Equal(t, []MessageSegment{segment}, result[2])
	for _, chunk := range result {
		assert.LessOrEqual(t, payloadJSONSize(t, chunk), limit)
	}
	assert.Len(t, splitChunksByPayload([][]MessageSegment{two}, limit), 1)
	assert.Len(t, splitChunksByPayload([][]MessageSegment{two}, limit-1), 2)
	assert.Equal(t, chunks, splitChunksByPayload(chunks, 0))
}

func TestSplitChunksByPayloadPreservesOversizedElementAndFollowingContent(t *testing.T) {
	large := MessageSegment{Type: "image", Data: map[string]interface{}{"file": strings.Repeat("a", 1000)}}
	text := MessageSegment{Type: "text", Data: map[string]interface{}{"text": "after image"}}
	result := splitChunksByPayload([][]MessageSegment{{large, text}}, 100)
	require.Len(t, result, 2)
	assert.Equal(t, []MessageSegment{large}, result[0])
	assert.Equal(t, []MessageSegment{text}, result[1])
}

func TestSplitChunksByPayloadPreservesSerializationError(t *testing.T) {
	chunks := [][]MessageSegment{{{Type: "image", Data: map[string]interface{}{"file": make(chan int)}}}}
	result := splitChunksByPayload(chunks, 100)
	assert.Equal(t, chunks, result)
	_, err := json.Marshal(result)
	require.Error(t, err)
}

type payloadLimitedAdapter struct {
	*mockAdapter
	limit int
}

func (a *payloadLimitedAdapter) MaxMessagePayloadBytes() int { return a.limit }

func TestBuildMessageChunksUsesAdapterPayloadLimit(t *testing.T) {
	m := &Messenger{Adapter: newMockAdapter()}
	msg := &SendingMessage{Elements: []IMessageElement{
		&ImageSegment{File: "base64://" + strings.Repeat("a", 300)},
		&ImageSegment{File: "base64://" + strings.Repeat("b", 300)},
	}}
	unlimited := m.buildMessageChunks(msg)
	require.Len(t, unlimited, 1)
	limit := payloadJSONSize(t, unlimited[0][:1])
	m.Adapter = &payloadLimitedAdapter{mockAdapter: newMockAdapter(), limit: limit + messagePayloadEnvelopeBytes}
	limited := m.buildMessageChunks(msg)
	require.Len(t, limited, 2)
	assert.Equal(t, unlimited[0][:1], limited[0])
	assert.Equal(t, unlimited[0][1:], limited[1])
}

func TestOneBotPayloadRejectedBeforeWriting(t *testing.T) {
	client := newTestWSClient("onebot-v11", WSModeReverse, "ws://localhost:3001")
	data := make([]byte, MaxOneBotPayloadBytes+1)
	err := client.SendRawData(data)
	require.ErrorIs(t, err, ErrRequestRejected)
	assert.NotErrorIs(t, err, ErrRequestResultUnknown)
	assert.ErrorIs(t, client.SendRawData(data[:MaxOneBotPayloadBytes]), ErrRequestNotSent)
}
