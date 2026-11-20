package adapter

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// QQImageOutputLimitBytes is the exclusive file-size budget for generated
// images. It is independent of JSON/base64 transport limits. The observed
// 32 MiB Highway boundary is not a compatibility guarantee for every client.
const QQImageOutputLimitBytes = 30_000_000

// GeneratedImageOutputLimiter supplies an exclusive byte budget for generated
// local images. A transport's smaller upload budget takes precedence over the
// QQ compatibility budget, without changing original media handling.
type GeneratedImageOutputLimiter interface {
	GeneratedImageOutputLimitBytes() int
}

func (m *Messenger) GeneratedImageOutputLimitBytes() int {
	limit := QQImageOutputLimitBytes
	if m != nil {
		if limiter, ok := m.Adapter.(GeneratedImageOutputLimiter); ok {
			if n := limiter.GeneratedImageOutputLimitBytes(); n > 0 {
				limit = min(limit, n)
			}
		}
	}
	return limit
}

// ValidateGeneratedImages runs before any part of a message is queued or sent.
// Original images and GIFs retain their existing delivery behavior.
func ValidateGeneratedImages(msg *SendingMessage, budgets ...int) error {
	if msg == nil {
		return nil
	}
	limit := QQImageOutputLimitBytes
	for _, n := range budgets {
		if n > 0 {
			limit = min(limit, n)
		}
	}
	for _, elem := range msg.Elements {
		img, ok := elem.(*ImageSegment)
		if !ok || img == nil || !img.Generated {
			continue
		}
		if !strings.HasPrefix(img.File, "base64://") {
			return fmt.Errorf("%w: generated image must contain encoded bytes", ErrRequestRejected)
		}
		encoded := strings.TrimPrefix(img.File, "base64://")
		if len(encoded) > base64.StdEncoding.EncodedLen(limit) {
			return fmt.Errorf("%w: generated image exceeds %d byte budget", ErrRequestRejected, limit)
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("%w: invalid generated image base64: %v", ErrRequestRejected, err)
		}
		if len(data) == 0 || len(data) >= limit {
			return fmt.Errorf("%w: generated image is %d bytes; must be below %d", ErrRequestRejected, len(data), limit)
		}
	}
	return nil
}
