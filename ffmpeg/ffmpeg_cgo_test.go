//go:build cgo

package ffmpeg

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
