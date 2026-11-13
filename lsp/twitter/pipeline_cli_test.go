//go:build !cgo

package twitter

import (
	"os"
	"os/exec"
	"testing"
)

func requirePipelineBackend(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if os.Getenv("DDBOT_REQUIRE_MEDIA_TESTS") == "1" {
			t.Fatalf("CLI pipeline requires ffmpeg in PATH: %v", err)
		}
		t.Skip("CLI pipeline requires ffmpeg in PATH")
	}
}
