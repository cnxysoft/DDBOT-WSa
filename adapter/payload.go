package adapter

import "encoding/json"

// MaxOneBotPayloadBytes is NapCatQQ's WebSocket maxPayload (50 MiB), including
// the JSON envelope and base64 data. It is not a limit on an image file.
const MaxOneBotPayloadBytes = 50 * 1024 * 1024

// Leave room for the action, target ID, echo and other request fields.
const messagePayloadEnvelopeBytes = 4 * 1024

// MessagePayloadLimiter optionally advertises an adapter's request size limit.
// Adapters without an explicit limit retain their existing chunking behavior.
type MessagePayloadLimiter interface {
	MaxMessagePayloadBytes() int
}

// splitChunksByPayload preserves existing message boundaries and element order.
// An indivisible oversized element is preserved for transport rejection before
// writing; it is never silently truncated or discarded.
func splitChunksByPayload(chunks [][]MessageSegment, limit int) [][]MessageSegment {
	if limit <= 0 {
		return chunks
	}
	var result [][]MessageSegment
	for _, chunk := range chunks {
		start, size := 0, 2 // JSON array brackets
		for i, segment := range chunk {
			encoded, err := json.Marshal(segment)
			if err != nil {
				// Keep the original request so the transport can return its normal
				// serialization error without silently dropping any content.
				return chunks
			}
			extra := len(encoded)
			if i > start {
				extra++ // comma
			}
			if size+extra > limit && i > start {
				result = append(result, chunk[start:i])
				start, size = i, 2
				extra = len(encoded)
			}
			size += extra
		}
		if start < len(chunk) {
			result = append(result, chunk[start:])
		}
	}
	return result
}
