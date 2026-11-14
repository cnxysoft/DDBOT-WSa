package qqofficial

// 下载超上限的检测（P2）。
//
// 缺陷形态：DownloadFile 用 io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes)) 读，
// 读满 200MB 就停，然后照常写盘、返回成功 —— 上层拿到的是**半个文件**，
// 后面解析/上传出来的东西全是错的，而且没有任何一处会提示「被截断了」。
//
// 判定口径：读满上限之后还能再读到字节，就是被截断（比单看 Content-Length 严，
// 分块传输没有 Content-Length 时同样能抓到）。

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// downloadTestServer 回一份固定长度的响应；withLength 控制要不要带 Content-Length
// （false = 分块传输，模拟没有长度头的下载源）。
func downloadTestServer(t *testing.T, size int, withLength bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !withLength {
			w.(http.Flusher).Flush()
		}
		_, _ = w.Write(bytes.Repeat([]byte("A"), size))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDownloadFileRejectsTruncatedBody：超过上限时**必须报错**，且不许留下半个文件。
func TestDownloadFileRejectsTruncatedBody(t *testing.T) {
	for _, c := range []struct {
		name       string
		withLength bool
	}{
		{"带 Content-Length", true},
		{"分块传输（没有 Content-Length）", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := downloadTestServer(t, 4096, c.withLength)
			a := newTestAdapter(t)
			a.downloadLimit = 1024 // 测试用小上限，等价于真机的 maxDownloadBytes

			path, err := a.DownloadFile(srv.URL+"/big.bin", "", "big.bin", nil)
			if err == nil {
				t.Fatalf("被上限截断时必须返回错误（上层会把半个文件当成品用），实际返回成功、路径 %q", path)
			}
			if !strings.Contains(err.Error(), "上限") {
				t.Errorf("错误文案要说明是上限截断，实际：%v", err)
			}
			if path != "" {
				if _, statErr := os.Stat(path); statErr == nil {
					t.Errorf("截断时不该把半个文件写到盘上：%s", path)
				}
			}
		})
	}
}

// TestDownloadFileUnderLimitSucceeds：没超上限的下载行为不变（回归）。
func TestDownloadFileUnderLimitSucceeds(t *testing.T) {
	srv := downloadTestServer(t, 512, true)
	a := newTestAdapter(t)
	a.downloadLimit = 1024

	path, err := a.DownloadFile(srv.URL+"/small.bin", "", "small.bin", nil)
	if err != nil {
		t.Fatalf("没超上限的下载不该报错：%v", err)
	}
	st, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("下载应落地成文件：%v", statErr)
	}
	if st.Size() != 512 {
		t.Errorf("下载文件大小应为 512，实际 %d", st.Size())
	}
}
