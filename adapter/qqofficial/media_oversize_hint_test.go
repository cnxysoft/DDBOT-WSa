package qqofficial

// media.go 直传超限提示的单测。
//
// 契约：错误信息必须能直接指导操作——带上**哪个文件**、**多大**、**上限多少**，
// 并说清出路是「改成外链（URL 上传）」。只说「请改用 URL 上传，或等分片上传实现」
// 对一个只会点发送的用户毫无帮助：他不知道是哪个文件，也没有「上传方式」可改。
//
// 5 MiB 上限本身是平台限制，不动；本文件只钉住文案。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestMediaOversizeErrorNamesFileAndPath 复现 P2：直传超限的错误只说「改用 URL 上传，
// 或等分片上传实现」，没有文件名、没有体积、没有可执行的下一步。
func TestMediaOversizeErrorNamesFileAndPath(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 4)
	ctx := context.Background()

	data := make([]byte, MaxMediaDirectUploadBytes+1)
	_, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeFile, "直播回放.part1.mp4", data)
	if err == nil {
		t.Fatal("超过 5 MiB 的本地直传应当被拒")
	}
	if !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("超限应返回 ErrMediaTooLarge，实际 %v", err)
	}
	var tooLarge *MediaTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("超限错误应是 *MediaTooLargeError，实际 %T", err)
	}
	// 结构体上也得能读到文件名，别只塞进文案
	if tooLarge.Name != "直播回放.part1.mp4" {
		t.Fatalf("MediaTooLargeError.Name = %q，期望 直播回放.part1.mp4", tooLarge.Name)
	}
	if tooLarge.Size != len(data) || tooLarge.Limit != MaxMediaDirectUploadBytes {
		t.Fatalf("错误应带体积与上限：Size=%d Limit=%d", tooLarge.Size, tooLarge.Limit)
	}

	msg := err.Error()
	for _, want := range []string{
		"直播回放.part1.mp4", // 哪个文件
		"5242881",        // 实际体积（字节）
		"5 MiB",          // 上限口径
		"外链",             // 可执行的出路
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("超限提示必须包含 %q，实际：%s", want, msg)
		}
	}
	if strings.Contains(msg, "等分片上传实现") && !strings.Contains(msg, "外链") {
		t.Fatalf("不许只把用户推给「等分片上传实现」：%s", msg)
	}
	if got := client.callCount(); got != 0 {
		t.Fatalf("超限必须在发请求前就拒掉，实际发了 %d 次", got)
	}
}
