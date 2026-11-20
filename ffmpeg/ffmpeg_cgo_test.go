//go:build cgo

package ffmpeg

import (
	"bytes"
	"fmt"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHLSMPEGTSRemuxToMP4(t *testing.T) {
	// A one-second H.264/AAC MPEG-TS segment, generated from lavfi testsrc2
	// (64x64, 4 fps) and sine (440 Hz). No external FFmpeg binary is needed.
	server := httptest.NewServer(http.FileServer(http.Dir("testdata/hls")))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "converted.mp4")
	if err := ConvMediaWithProxy(server.URL+"/index.m3u8", output, "", "mp4"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, box := range []string{"ftyp", "moov", "mdat", "avc1", "mp4a"} {
		if !bytes.Contains(data, []byte(box)) {
			t.Errorf("converted MP4 is missing %s", box)
		}
	}
}

func TestHLSMasterSelectsOneVideoAndAudio(t *testing.T) {
	files := http.FileServer(http.Dir("testdata/hls"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/master.m3u8" {
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100000\n/low/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=200000\n/high/index.m3u8\n")
			return
		}
		r.URL.Path = strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/low"), "/high")
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "selected.mp4")
	if err := ConvMediaWithProxy(server.URL+"/master.m3u8", output, "", "mp4"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if tracks := bytes.Count(data, []byte("trak")); tracks != 2 {
		t.Fatalf("MP4 has %d tracks; want one video and one audio track", tracks)
	}
}

func TestRemuxRejectsTruncatedHTTPInput(t *testing.T) {
	segment, err := os.ReadFile("testdata/hls/segment.ts")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Content-Length", fmt.Sprint(len(segment)+1024))
		w.Write(segment)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "truncated.mp4")
	err = ConvMediaWithProxy(server.URL+"/truncated.ts", output, "", "mp4")
	if err == nil {
		t.Fatal("truncated HTTP input was reported as a successful conversion")
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("failed conversion left a partial output: %v", statErr)
	}
}

func TestInputDeadlineWithoutProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	started := time.Now()
	err := convMediaWithTimeout(server.URL+"/stalled.m3u8", filepath.Join(t.TempDir(), "stalled.mp4"), "", "mp4", 200*time.Millisecond)
	if err == nil {
		t.Fatal("stalled input did not fail")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("input deadline was not enforced: %s", elapsed)
	}
}

func TestGIFTranscodeKeepsAnimation(t *testing.T) {
	_, hook := captureFFmpegLogs(t)
	require.NoError(t, SetLogLevel("quiet"))
	output := filepath.Join(t.TempDir(), "animated.gif")
	if err := ConvMediaWithProxy("testdata/hls/segment.ts", output, "", "gif"); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	animation, err := gif.DecodeAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(animation.Image) < 2 {
		t.Fatalf("GIF lost its animation: %d frames", len(animation.Image))
	}
	require.Empty(t, hook.AllEntries(), "quiet must also suppress Go-side GIF pipeline logs")
}

func TestNativeCallbackFiltersBeforeForwarding(t *testing.T) {
	_, hook := captureFFmpegLogs(t)
	segment, err := os.ReadFile("testdata/hls/segment.ts")
	require.NoError(t, err)
	files := http.FileServer(http.Dir("testdata/hls"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/failure.ts" {
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Content-Length", fmt.Sprint(len(segment)+1024))
			w.Write(segment)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, tc := range []struct {
		level, path string
		failure     bool
		logged      bool
	}{
		{"error", "/failure.ts", true, true},
		{"quiet", "/failure.ts", true, false},
		{"error", "/index.m3u8", false, false},
		{"info", "/index.m3u8", false, true},
	} {
		t.Run(tc.level+tc.path, func(t *testing.T) {
			hook.Reset()
			require.NoError(t, SetLogLevel(tc.level))
			err := ConvMediaWithProxy(server.URL+tc.path, filepath.Join(t.TempDir(), "output.mp4"), "", "mp4")
			require.Equal(t, tc.failure, err != nil)
			require.Equal(t, tc.logged, len(hook.AllEntries()) > 0)
			for _, entry := range hook.AllEntries() {
				require.Equal(t, "cgo", entry.Data["backend"])
				level, ok := parseLogLevel(entry.Data["ffmpeg_level"].(string))
				require.True(t, ok)
				require.LessOrEqual(t, level, int(nativeLogLevel.Load()))
			}
		})
	}
}
