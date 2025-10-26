//go:build !cgo
// +build !cgo

package ffmpeg

import (
	"fmt"
	"os"
	"os/exec"
)

func ConvMediaWithProxy(url, outputPath, proxyURL, mediaType string) error {
	args := []string{"-v", "error", "-i", url, "-f", mediaType, outputPath}

	if mediaType == "mp4" {
		args = []string{"-v", "error", "-i", url, "-c", "copy", "-movflags", "+faststart", "-f", mediaType, outputPath}
	}

	cmd := exec.Command("ffmpeg", args...)
	if proxyURL != "" {
		cmd.Env = append(os.Environ(),
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			"rw_timeout=30000000",
		)
	}
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg CLI failed: %w", err)
	}
	return nil
}
