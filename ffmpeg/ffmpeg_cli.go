//go:build !cgo
// +build !cgo

package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func configureNativeLogging(level int) {}

func ConvMediaWithProxy(url, outputPath, proxyURL, mediaType string) error {
	return convMediaWithTimeout(url, outputPath, proxyURL, mediaType, mediaConversionTimeout)
}

func convMediaWithTimeout(url, outputPath, proxyURL, mediaType string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"-loglevel", "level+" + nativeLevelName(int(nativeLogLevel.Load())), "-nostats", "-nostdin", "-xerror", "-rw_timeout", strconv.FormatInt(min(timeout, inputReadTimeout).Microseconds(), 10)}
	if proxyURL != "" {
		args = append(args, "-http_proxy", proxyURL)
	}
	args = append(args, "-i", url)
	if mediaType == "mp4" {
		args = append(args, "-c", "copy", "-movflags", "+faststart")
	}
	args = append(args, "-f", mediaType, outputPath)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = nil
	logs := &cliLogWriter{}
	cmd.Stderr = logs
	defer logs.Flush()

	_, previousOutputErr := os.Stat(outputPath)
	if err := cmd.Run(); err != nil {
		if os.IsNotExist(previousOutputErr) {
			os.Remove(outputPath)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg CLI timed out: %w", ctx.Err())
		}
		return fmt.Errorf("ffmpeg CLI failed: %w", err)
	}
	return nil
}
