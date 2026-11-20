//go:build cgo

package twitter

import "testing"

// The CGO backend uses the linked static libraries, with no external executable.
func requirePipelineBackend(t *testing.T) { t.Helper() }
