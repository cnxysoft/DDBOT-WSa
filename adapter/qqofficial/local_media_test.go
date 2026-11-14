package qqofficial

// 本地媒体（file:// 前缀）的单测。
//
// 缺陷形态（P0）：lsp/mmsg 的 image/video/record/file 元素在 Url 不是 http(s) 时，
// 一律包成 file:// + 路径（lsp/mmsg/image.go:108/132、video.go:70/94、
// record.go:70/94、file.go:90/114），Windows 上还会把反斜杠转义成双反斜杠。
// 而 newMediaFromSource 只认 http(s):// 与 base64://，其余**原样**塞进
// outboundMedia.path，uploadMedia 于是拿 os.ReadFile("file:///tmp/a.png") 去读，
// 必然失败 —— 所有本地图片/语音/视频/文件都发不出去。
//
// 这里钉住两件事：
//   1. 解析层把 file:// 剥成本地路径（含 Windows 的 file:///C:/... 与双反斜杠形态）；
//   2. 端到端：file:// 指向一个真实临时文件时，媒体确实被上传并发出，
//      以及读不到时报错里带文件名（不许把错误吞掉）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// TestNewMediaFromSourceStripsFileURL 钉住 file:// 的各种形态。
func TestNewMediaFromSourceStripsFileURL(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"unix 形态", "file:///tmp/cover.png", "/tmp/cover.png"},
		{"相对路径形态", "file://data/cover.png", "data/cover.png"},
		{"windows 三斜杠盘符", "file:///C:/tmp/cover.png", "C:/tmp/cover.png"},
		{"windows 裸盘符（lsp 只拼前缀并把反斜杠转义成两个）", "file://C:\\\\tmp\\\\cover.png", "C:\\tmp\\cover.png"},
		{"非 file:// 的本地路径保持原样", "/tmp/plain.png", "/tmp/plain.png"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			part := newMediaFromSource(FileTypeImage, c.source, "")
			if part.url != "" {
				t.Fatalf("本地路径不该走 URL 上传：%q", part.url)
			}
			if part.path != c.want {
				t.Fatalf("file:// 应剥成本地路径：source=%q 期望 path=%q，实际 %q"+
					"（uploadMedia 会拿它去 os.ReadFile，带前缀必失败）", c.source, c.want, part.path)
			}
			if part.fileName != "cover.png" && part.fileName != "plain.png" {
				t.Errorf("文件名应从路径推出来，实际 %q", part.fileName)
			}
		})
	}
}

// TestLocalFileURLMediaSendsRealBytes 端到端：file:// 指向真实临时文件时必须发得出去。
func TestLocalFileURLMediaSendsRealBytes(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	local := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(local, []byte("fake-image-bytes"), 0o600); err != nil {
		t.Fatalf("准备本地图片失败：%v", err)
	}

	msg := &adapter.SendingMessage{Elements: []adapter.IMessageElement{
		&adapter.ImageSegment{File: "file://" + local},
	}}
	if _, err := a.SendGroupMessage(groupID, msg); err != nil {
		t.Fatalf("file:// 本地图片必须能发出去（lsp/mmsg 产出的就是这个形态），实际报错：%v", err)
	}
	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("应只发出一条富媒体消息，实际 %d 条", len(sends))
	}
	if sends[0].fileInfo != "FILEINFO_TEST" {
		t.Errorf("本地文件应先上传再发（/files 拿到的 file_info 要进 media.file_info），实际 %q", sends[0].fileInfo)
	}
}

// TestLocalFileURLMediaMissingFileReportsName 读不到时要报错，且错误里带文件名。
func TestLocalFileURLMediaMissingFileReportsName(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	missing := filepath.Join(t.TempDir(), "ghost-image.png")
	msg := &adapter.SendingMessage{Elements: []adapter.IMessageElement{
		&adapter.ImageSegment{File: "file://" + missing},
	}}
	_, err := a.SendGroupMessage(groupID, msg)
	if err == nil {
		t.Fatal("本地文件不存在时必须返回错误，不能静默当成功")
	}
	if !strings.Contains(err.Error(), "ghost-image.png") {
		t.Errorf("读不到本地媒体时错误里要带文件名，实际：%v", err)
	}
}
