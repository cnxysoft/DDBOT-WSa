package qqofficial

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// 本文件是 media.go 的纯逻辑单测：stubMediaClient 顶替真实 Client，**不发任何真实请求**，
// 也不建 WebSocket 连接（包里躺一个没门控的真连接测试会拖死 CI，这条是硬要求）。
// 时间全部走 mediaFakeClock，不靠 sleep。
// 符号统一带 media 前缀，避免与 idmap_test.go / dedup_test.go（以及后续 message_test.go）撞名。

// ---------- 打桩与工具 ----------

type stubMediaClient struct {
	mu            sync.Mutex
	calls         int
	paths         []string
	bodies        []uploadFileRequest
	fail          error
	ttl           int
	emptyFileInfo bool
}

func (s *stubMediaClient) PostJSON(ctx context.Context, path string, body any, out any) error {
	req, _ := body.(uploadFileRequest)
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.paths = append(s.paths, path)
	s.bodies = append(s.bodies, req)
	fail := s.fail
	ttl := s.ttl
	empty := s.emptyFileInfo
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if fail != nil {
		return fail
	}
	resp, ok := out.(*uploadFileResponse)
	if !ok {
		return fmt.Errorf("stub: out 期望 *uploadFileResponse，实际 %T", out)
	}
	resp.FileUUID = fmt.Sprintf("uuid-%d", n)
	if !empty {
		resp.FileInfo = fmt.Sprintf("fileinfo-%d", n)
	}
	resp.TTL = ttl
	return nil
}

func (s *stubMediaClient) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubMediaClient) pathAt(t *testing.T, i int) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.paths) {
		t.Fatalf("stub 只被调用 %d 次，取不到第 %d 次", len(s.paths), i)
	}
	return s.paths[i]
}

func (s *stubMediaClient) bodyAt(t *testing.T, i int) uploadFileRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.bodies) {
		t.Fatalf("stub 只被调用 %d 次，取不到第 %d 次的请求体", len(s.bodies), i)
	}
	return s.bodies[i]
}

type mediaFakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newMediaFakeClock() *mediaFakeClock {
	return &mediaFakeClock{t: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
}

func (c *mediaFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *mediaFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newMediaTestUploader(client MediaClient, capacity int) (*Uploader, *mediaFakeClock) {
	clock := newMediaFakeClock()
	u := NewUploaderWithCapacity(client, capacity)
	u.now = clock.Now
	return u, clock
}

func mediaMustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际报错: %v", err)
	}
}

// ---------- 样本数据：魔数写全，方便格式校验用例 ----------

func mediaPNGBytes(payload int) []byte {
	data := make([]byte, 0, len(pngMagic)+payload)
	data = append(data, pngMagic...)
	for i := 0; i < payload; i++ {
		data = append(data, byte(i%251))
	}
	return data
}

func mediaJPGBytes() []byte { return append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 12)...) }

func mediaGIFBytes() []byte { return append([]byte("GIF89a"), make([]byte, 16)...) }

func mediaWEBPBytes() []byte {
	data := append([]byte("RIFF"), 0, 0, 0, 0)
	return append(append(data, []byte("WEBP")...), make([]byte, 8)...)
}

func mediaMP4Bytes() []byte {
	data := []byte{0x00, 0x00, 0x00, 0x18}
	return append(append(data, []byte("ftypisom")...), make([]byte, 8)...)
}

func mediaSILKBytes() []byte { return append([]byte{0x02}, []byte("#!SILK_V3")...) }

func mediaUnknownBytes() []byte { return []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02} }

// ---------- 1. 缓存命中 ----------

func TestMediaCacheHitAvoidsReupload(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, clock := newMediaTestUploader(client, 8)
	ctx := context.Background()
	data := mediaPNGBytes(64)

	first, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if first.FileInfo == "" || first.FileUUID == "" {
		t.Fatalf("上传结果不完整: %+v", first)
	}
	if first.TTL != 86400 {
		t.Errorf("TTL 应取响应里的 86400，实际 %d", first.TTL)
	}

	second, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if second.FileInfo != first.FileInfo {
		t.Errorf("缓存命中应返回同一份 file_info：%q vs %q", first.FileInfo, second.FileInfo)
	}
	if got := client.callCount(); got != 1 {
		t.Fatalf("第二次应命中缓存，实际累计上传 %d 次", got)
	}

	// 换一个内容相同的新切片，也应当命中（比对的是内容哈希，不是切片地址）
	same := mediaPNGBytes(64)
	third, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", same)
	mediaMustNoErr(t, err)
	if third.FileInfo != first.FileInfo || client.callCount() != 1 {
		t.Errorf("内容相同的另一份切片应命中缓存，实际上传 %d 次", client.callCount())
	}

	// 12 小时后仍在 ttl 内：还要命中
	clock.Advance(12 * time.Hour)
	fourth, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if fourth.FileInfo != first.FileInfo || client.callCount() != 1 {
		t.Errorf("ttl 内（12 小时）应命中缓存，实际上传 %d 次", client.callCount())
	}

	// 请求体自查
	body := client.bodyAt(t, 0)
	if body.FileType != FileTypeImage {
		t.Errorf("file_type 应为 %d，实际 %d", FileTypeImage, body.FileType)
	}
	if body.FileName != "cover.png" {
		t.Errorf("file_name 应为 cover.png，实际 %q", body.FileName)
	}
	if body.SrvSendMsg {
		t.Error("srv_send_msg 必须恒为 false：置 true 会顺手发消息并占用主动消息频次")
	}
	if body.FileData != base64.StdEncoding.EncodeToString(data) {
		t.Error("file_data 应当是原始字节的 base64")
	}
	if body.URL != "" {
		t.Errorf("直传不应带 url 字段，实际 %q", body.URL)
	}
	if got := client.pathAt(t, 0); got != "/v2/groups/GROUP_A/files" {
		t.Errorf("群聊上传路径应为 /v2/groups/GROUP_A/files，实际 %s", got)
	}
}

// ---------- 2. TTL 过期后重新上传 ----------

func TestMediaCacheExpiryForcesReupload(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, clock := newMediaTestUploader(client, 8)
	ctx := context.Background()
	data := mediaPNGBytes(32)

	first, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)

	clock.Advance(12 * time.Hour)
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("ttl 内应命中: %v", err)
	}
	if client.callCount() != 1 {
		t.Fatalf("ttl 内不应重新上传，实际 %d 次", client.callCount())
	}

	// 推进到只剩 30 秒寿命：已进入到期余量（DefaultMediaExpiryMargin = 60s），不再复用
	clock.Advance(12*time.Hour - 30*time.Second)
	second, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if client.callCount() != 2 {
		t.Fatalf("进入到期余量后必须重新上传，实际 %d 次", client.callCount())
	}
	if second.FileInfo == first.FileInfo {
		t.Error("重新上传应拿到新的 file_info")
	}
	if second.TTL != 86400 {
		t.Errorf("重新上传的 TTL 应为 86400，实际 %d", second.TTL)
	}

	// 再过整整一个 ttl：旧条目早已过期，仍要重新上传
	clock.Advance(86400*time.Second + time.Second)
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("过期后重新上传失败: %v", err)
	}
	if client.callCount() != 3 {
		t.Errorf("过期后应重新上传，实际累计 %d 次", client.callCount())
	}

	// 过期条目不应堆积：每次 get 到过期条目都会删掉，容量不会被死条目占满
	if u.CacheLen() != 1 {
		t.Errorf("同一 key 应只留一条缓存，实际 %d 条", u.CacheLen())
	}
}

func TestMediaTTLFallbackWhenResponseOmitsTTL(t *testing.T) {
	// 响应没给 ttl（或给 0，文档口径「长期」）时兜底成 MediaTTLFallback（实测值 86400）
	client := &stubMediaClient{} // ttl 默认 0
	u, clock := newMediaTestUploader(client, 4)
	ctx := context.Background()
	data := mediaPNGBytes(16)

	info, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	want := int(MediaTTLFallback / time.Second)
	if info.TTL != want {
		t.Fatalf("ttl 缺失时兜底应为 %d，实际 %d", want, info.TTL)
	}

	// 兜底 ttl 也要真的参与缓存判定：12 小时后仍命中
	clock.Advance(12 * time.Hour)
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("ttl 内应命中: %v", err)
	}
	if client.callCount() != 1 {
		t.Errorf("兜底 ttl 内的复用应命中，实际上传 %d 次", client.callCount())
	}
}

// ---------- 3. key 隔离（同图不同目标不串用） ----------

func TestMediaCacheKeyIsolation(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 16)
	ctx := context.Background()
	data := mediaPNGBytes(48)

	steps := []struct {
		name     string
		kind     TargetKind
		openID   string
		fileType int
		fileName string
		wantCall int
		wantPath string
	}{
		{"群 A 首传", TargetGroup, "GROUP_A", FileTypeImage, "cover.png", 1, "/v2/groups/GROUP_A/files"},
		{"群 A 复传：命中缓存", TargetGroup, "GROUP_A", FileTypeImage, "cover.png", 1, ""},
		{"群 B：同字节不同群，必须另传", TargetGroup, "GROUP_B", FileTypeImage, "cover.png", 2, "/v2/groups/GROUP_B/files"},
		{"同 openid 走单聊：目标类型不同，必须另传", TargetUser, "GROUP_A", FileTypeImage, "cover.png", 3, "/v2/users/GROUP_A/files"},
		{"同一份字节当文件传：file_type 不同，必须另传", TargetGroup, "GROUP_A", FileTypeFile, "cover.png", 4, "/v2/groups/GROUP_A/files"},
		{"同一份字节换文件名：必须另传", TargetGroup, "GROUP_A", FileTypeImage, "other.png", 5, "/v2/groups/GROUP_A/files"},
	}

	infos := make([]string, 0, len(steps))
	for _, step := range steps {
		info, err := u.UploadBytes(ctx, step.kind, step.openID, step.fileType, step.fileName, data)
		mediaMustNoErr(t, err)
		if got := client.callCount(); got != step.wantCall {
			t.Fatalf("%s: 期望累计上传 %d 次，实际 %d 次", step.name, step.wantCall, got)
		}
		if step.wantPath != "" {
			if got := client.pathAt(t, step.wantCall-1); got != step.wantPath {
				t.Errorf("%s: 路径应为 %s，实际 %s", step.name, step.wantPath, got)
			}
		}
		infos = append(infos, info.FileInfo)
	}

	if infos[1] != infos[0] {
		t.Error("命中缓存的那一步应返回同一份 file_info")
	}
	// 五次真上传必须拿到五份不同的 file_info——一串联用就是 bug
	seen := make(map[string]bool, len(infos))
	for _, fi := range infos {
		seen[fi] = true
	}
	if len(seen) != 5 {
		t.Errorf("五次真上传应有 5 份不同 file_info，实际 %d 份", len(seen))
	}
	if u.CacheLen() != 5 {
		t.Errorf("缓存应有 5 条（各目标各一份），实际 %d 条", u.CacheLen())
	}
}

// ---------- 4. 超 5M 提前拒绝 ----------

func TestMediaRejectsOversizeDirectUpload(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 4)
	ctx := context.Background()

	data := make([]byte, MaxMediaDirectUploadBytes+1)
	info, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeFile, "big.bin", data)
	if info != nil {
		t.Errorf("超限时不应返回 MediaInfo，实际 %+v", info)
	}
	if !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("超限应返回 ErrMediaTooLarge，实际 %v", err)
	}
	var tooLarge *MediaTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("超限错误应是 *MediaTooLargeError，实际 %T", err)
	}
	if tooLarge.Size != len(data) || tooLarge.Limit != MaxMediaDirectUploadBytes {
		t.Errorf("错误应带体积与上限：Size=%d Limit=%d", tooLarge.Size, tooLarge.Limit)
	}
	if got := client.callCount(); got != 0 {
		t.Fatalf("超限必须在发请求前就拒掉，实际发了 %d 次", got)
	}

	// 正好等于上限：放行（文档口径是「不能超过 5M」）
	edge := make([]byte, MaxMediaDirectUploadBytes)
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeFile, "edge.bin", edge); err != nil {
		t.Fatalf("恰好等于上限应放行，实际 %v", err)
	}
	if got := client.callCount(); got != 1 {
		t.Errorf("等于上限的那次应该真的发出去，实际调用 %d 次", got)
	}
}

// ---------- 5. 并发安全 ----------

func TestMediaConcurrentSameContentUploadsOnce(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 8)
	data := mediaPNGBytes(128)

	const goroutines = 32
	start := make(chan struct{})
	results := make([]*MediaInfo, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 屏障：尽量让 32 个调用撞在同一瞬间
			results[i], errs[i] = u.UploadBytes(context.Background(), TargetGroup, "GROUP_C", FileTypeImage, "cover.png", data)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("第 %d 个并发调用失败: %v", i, errs[i])
		}
		if results[i] == nil || results[i].FileInfo == "" {
			t.Fatalf("第 %d 个并发调用拿到的结果不完整: %+v", i, results[i])
		}
		if results[i].FileInfo != results[0].FileInfo {
			t.Errorf("并发同一份内容应共享同一份 file_info：第 %d 个是 %q，第 0 个是 %q", i, results[i].FileInfo, results[0].FileInfo)
		}
	}
	if got := client.callCount(); got != 1 {
		t.Errorf("并发的同一份内容只应真上传一次，实际 %d 次", got)
	}
	if u.CacheLen() != 1 {
		t.Errorf("缓存应只有一条，实际 %d 条", u.CacheLen())
	}
}

func TestMediaConcurrentDistinctTargetsStayIsolated(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 64)
	data := mediaPNGBytes(96)

	const groups = 16
	start := make(chan struct{})
	results := make([]*MediaInfo, groups)
	errs := make([]error, groups)
	var wg sync.WaitGroup
	for i := 0; i < groups; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			openID := fmt.Sprintf("GROUP_%02d", i)
			<-start
			results[i], errs[i] = u.UploadBytes(context.Background(), TargetGroup, openID, FileTypeImage, "cover.png", data)
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[string]int, groups)
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("第 %d 个并发调用失败: %v", i, errs[i])
		}
		seen[results[i].FileInfo]++
	}
	if got := client.callCount(); got != groups {
		t.Errorf("%d 个不同目标应各传一次（共 %d 次），实际 %d 次", groups, groups, got)
	}
	if len(seen) != groups {
		t.Errorf("%d 个目标应拿到 %d 份不同 file_info，实际 %d 份", groups, groups, len(seen))
	}
	for fi, n := range seen {
		if n != 1 {
			t.Errorf("file_info %q 被 %d 个目标共用，存在串用", fi, n)
		}
	}

	// 再跑一遍：全部命中，一次请求都不该多发
	for i := 0; i < groups; i++ {
		openID := fmt.Sprintf("GROUP_%02d", i)
		info, err := u.UploadBytes(context.Background(), TargetGroup, openID, FileTypeImage, "cover.png", data)
		mediaMustNoErr(t, err)
		if info.FileInfo != results[i].FileInfo {
			t.Errorf("目标 %s 第二轮应命中自己的缓存", openID)
		}
	}
	if got := client.callCount(); got != groups {
		t.Errorf("第二轮应全部命中，实际累计 %d 次", got)
	}
}

// ---------- 6. URL 上传 ----------

func TestMediaUploadURL(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, clock := newMediaTestUploader(client, 8)
	ctx := context.Background()
	urlA := "https://example.com/cover.png"
	urlB := "https://example.com/other.png"

	first, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", urlA)
	mediaMustNoErr(t, err)
	second, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", urlA)
	mediaMustNoErr(t, err)
	if second.FileInfo != first.FileInfo || client.callCount() != 1 {
		t.Errorf("相同地址应命中缓存，实际上传 %d 次", client.callCount())
	}

	body := client.bodyAt(t, 0)
	if body.URL != urlA {
		t.Errorf("URL 上传的请求体应带 url=%s，实际 %q", urlA, body.URL)
	}
	if body.FileData != "" {
		t.Error("URL 上传不应同时带 file_data")
	}
	if body.SrvSendMsg {
		t.Error("srv_send_msg 必须为 false")
	}
	if got := client.pathAt(t, 0); got != "/v2/groups/GROUP_A/files" {
		t.Errorf("路径应为 /v2/groups/GROUP_A/files，实际 %s", got)
	}

	// 另一个地址：新上传
	if _, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", urlB); err != nil {
		t.Fatalf("换地址上传失败: %v", err)
	}
	if client.callCount() != 2 {
		t.Errorf("换地址应重新上传，实际 %d 次", client.callCount())
	}

	// 同一地址换目标（单聊）：必须另传，且走单聊路径
	if _, err := u.UploadURL(ctx, TargetUser, "GROUP_A", FileTypeImage, "cover.png", urlA); err != nil {
		t.Fatalf("单聊上传失败: %v", err)
	}
	if client.callCount() != 3 {
		t.Errorf("换目标应重新上传，实际 %d 次", client.callCount())
	}
	if got := client.pathAt(t, 2); got != "/v2/users/GROUP_A/files" {
		t.Errorf("单聊路径应为 /v2/users/GROUP_A/files，实际 %s", got)
	}

	// 地址与字节的缓存空间不共用：内容恰好是 urlA 这段字节的直传，理应另算一次
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", []byte(urlA)); err != nil {
		t.Fatalf("直传失败: %v", err)
	}
	if client.callCount() != 4 {
		t.Errorf("URL 缓存与字节缓存不应共用 key，实际 %d 次", client.callCount())
	}

	// 过期后重传
	clock.Advance(86400*time.Second + time.Second)
	if _, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", urlA); err != nil {
		t.Fatalf("过期后重传失败: %v", err)
	}
	if client.callCount() != 5 {
		t.Errorf("URL 缓存过期后应重新上传，实际 %d 次", client.callCount())
	}
}

// ---------- 7. 参数与格式校验 ----------

func TestMediaParamValidation(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 4)
	ctx := context.Background()
	data := mediaPNGBytes(16)

	cases := []struct {
		name string
		want error
		call func() error
	}{
		{"零值目标类型（忘了填）", ErrMediaTargetInvalid, func() error {
			_, err := u.UploadBytes(ctx, TargetUnknown, "GROUP_A", FileTypeImage, "a.png", data)
			return err
		}},
		{"openid 为空", ErrMediaTargetOpenIDEmpty, func() error {
			_, err := u.UploadBytes(ctx, TargetGroup, "", FileTypeImage, "a.png", data)
			return err
		}},
		{"file_type=0", ErrMediaFileTypeInvalid, func() error {
			_, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", 0, "a.png", data)
			return err
		}},
		{"file_type=5", ErrMediaFileTypeInvalid, func() error {
			_, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", 5, "a.png", data)
			return err
		}},
		{"file_name 为空", ErrMediaFileNameEmpty, func() error {
			_, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "", data)
			return err
		}},
		{"数据为空", ErrMediaDataEmpty, func() error {
			_, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "a.png", nil)
			return err
		}},
		{"url 为空", ErrMediaURLEmpty, func() error {
			_, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "a.png", "")
			return err
		}},
		{"url 非 http(s)", ErrMediaURLInvalid, func() error {
			_, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "a.png", "ftp://example.com/a.png")
			return err
		}},
		{"url 是相对路径", ErrMediaURLInvalid, func() error {
			_, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "a.png", "/a.png")
			return err
		}},
		{"URL 上传但目标类型无效", ErrMediaTargetInvalid, func() error {
			_, err := u.UploadURL(ctx, TargetUnknown, "GROUP_A", FileTypeImage, "a.png", "https://example.com/a.png")
			return err
		}},
	}

	for _, tc := range cases {
		err := tc.call()
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 期望 %v，实际 %v", tc.name, tc.want, err)
		}
	}
	if got := client.callCount(); got != 0 {
		t.Errorf("参数校验失败的调用一次都不该发出去，实际 %d 次", got)
	}
}

// 校验只拦「族别错配」，图片不做格式白名单。
//
// 2026-11-11 直接在编译机上打 POST /v2/groups/{openid}/files（file_type=1）实测：
// png / jpg / gif（单帧）/ gif（动图）/ webp / bmp / tiff **全部 200 + file_info + ttl=86400**。
// 文档那句「图片只支持 png/jpg」不完整 —— 照它写死的结果是 gif 被本地拦掉、/倒放 连请求都发不出去。
func TestMediaFormatCheck(t *testing.T) {
	animatedGif := append(append([]byte{}, mediaGIFBytes()...), mediaGIFBytes()...)
	cases := []struct {
		name     string
		fileType int
		data     []byte
		wantErr  bool
	}{
		{"png 声明图片", FileTypeImage, mediaPNGBytes(8), false},
		{"jpg 声明图片", FileTypeImage, mediaJPGBytes(), false},
		{"gif 单帧 声明图片：实测平台收，放行", FileTypeImage, mediaGIFBytes(), false},
		{"gif 动图 声明图片：实测平台收，放行", FileTypeImage, animatedGif, false},
		{"webp 声明图片：实测平台收，放行", FileTypeImage, mediaWEBPBytes(), false},
		{"认不出魔数（bmp/tiff 之类）：放行交给平台判", FileTypeImage, mediaUnknownBytes(), false},
		{"mp4 声明图片：族别错配，仍拦", FileTypeImage, mediaMP4Bytes(), true},
		{"silk 声明图片：族别错配，仍拦", FileTypeImage, mediaSILKBytes(), true},
		{"png 声明视频：族别错配，仍拦", FileTypeVideo, mediaPNGBytes(8), true},
		{"gif 声明视频：族别错配，仍拦", FileTypeVideo, mediaGIFBytes(), true},
		{"mp4 声明视频", FileTypeVideo, mediaMP4Bytes(), false},
		{"silk 声明语音", FileTypeVoice, mediaSILKBytes(), false},
		{"mp4 声明语音：族别错配，仍拦", FileTypeVoice, mediaMP4Bytes(), true},
		{"任意字节当文件发：放行", FileTypeFile, mediaGIFBytes(), false},
	}

	for i, tc := range cases {
		client := &stubMediaClient{ttl: 86400}
		u, _ := newMediaTestUploader(client, 4)
		_, err := u.UploadBytes(context.Background(), TargetGroup, fmt.Sprintf("GROUP_%d", i), tc.fileType, "f.bin", tc.data)
		if !tc.wantErr {
			if err != nil {
				t.Errorf("%s: 期望放行，实际 %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrMediaFormatUnsupported) {
			t.Errorf("%s: 期望 ErrMediaFormatUnsupported，实际 %v", tc.name, err)
			continue
		}
		var formatErr *MediaFormatError
		if !errors.As(err, &formatErr) {
			t.Errorf("%s: 期望 *MediaFormatError，实际 %T", tc.name, err)
		} else if formatErr.Detected == "" {
			t.Errorf("%s: 错误里应带探测到的格式", tc.name)
		} else {
			// 文案要求：这是我们自己的客户端校验，不许写成「发送侧/平台不接受」。
			msg := formatErr.Error()
			if !strings.Contains(msg, "客户端校验") {
				t.Errorf("%s: 报错应说明是本地客户端校验，实际 %q", tc.name, msg)
			}
			if strings.Contains(msg, "发送侧不接受") {
				t.Errorf("%s: 报错不许把锅甩给平台，实际 %q", tc.name, msg)
			}
		}
		if got := client.callCount(); got != 0 {
			t.Errorf("%s: 格式不符应在发请求前拒掉，实际发了 %d 次", tc.name, got)
		}
	}
}

// ---------- 8. 容量上限与淘汰 ----------

func TestMediaCacheCapacityEvictsLRU(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 2)
	ctx := context.Background()
	data := mediaPNGBytes(24)

	openIDs := []string{"GROUP_1", "GROUP_2", "GROUP_3"}
	for i, openID := range openIDs {
		if _, err := u.UploadBytes(ctx, TargetGroup, openID, FileTypeImage, "cover.png", data); err != nil {
			t.Fatalf("第 %d 次上传失败: %v", i+1, err)
		}
	}
	if got := client.callCount(); got != 3 {
		t.Fatalf("三个不同目标应各传一次，实际 %d 次", got)
	}
	if u.CacheLen() != 2 {
		t.Fatalf("容量上限 2，实际 %d 条", u.CacheLen())
	}

	// GROUP_1 是最久未用的，已被挤出：应重新上传
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_1", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if got := client.callCount(); got != 4 {
		t.Errorf("被挤出的条目应重新上传，实际累计 %d 次", got)
	}

	// GROUP_3 还在缓存里：命中
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_3", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if got := client.callCount(); got != 4 {
		t.Errorf("仍在缓存里的条目应命中，实际累计 %d 次", got)
	}
}

func TestMediaCacheDefaultCapacityWhenNonPositive(t *testing.T) {
	u := NewUploaderWithCapacity(&stubMediaClient{}, 0)
	if u.cache.capacity != DefaultMediaCacheCapacity {
		t.Errorf("容量 <= 0 时应退回默认 %d，实际 %d", DefaultMediaCacheCapacity, u.cache.capacity)
	}
}

// ---------- 9. Invalidate 与失败处理 ----------

func TestMediaInvalidateForcesReupload(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 4)
	ctx := context.Background()
	data := mediaPNGBytes(16)

	first, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if u.CacheLen() != 1 {
		t.Fatalf("应有一条缓存，实际 %d", u.CacheLen())
	}

	// 平台回报「图片已过期/任务不存在」时调用方主动作废
	u.InvalidateBytes(TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	if u.CacheLen() != 0 {
		t.Errorf("作废后缓存应为空，实际 %d 条", u.CacheLen())
	}

	second, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if second.FileInfo == first.FileInfo {
		t.Error("作废后重新上传应拿到新的 file_info")
	}
	if got := client.callCount(); got != 2 {
		t.Errorf("作废后应重新上传，实际 %d 次", got)
	}

	// URL 版本同理
	urlA := "https://example.com/cover.png"
	if _, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", urlA); err != nil {
		t.Fatalf("URL 上传失败: %v", err)
	}
	// 这一发是第 3 次（直传 2 次 + URL 首传 1 次）
	if got := client.callCount(); got != 3 {
		t.Fatalf("URL 首传后累计应为 3 次，实际 %d 次", got)
	}
	u.InvalidateURL(TargetGroup, "GROUP_A", FileTypeImage, "cover.png", urlA)
	if got := client.callCount(); got != 3 {
		t.Errorf("作废动作本身不该发请求，实际 %d 次", got)
	}
	if _, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", urlA); err != nil {
		t.Fatalf("URL 重传失败: %v", err)
	}
	if got := client.callCount(); got != 4 {
		t.Errorf("URL 作废后应重新上传，实际 %d 次", got)
	}

	// 参数非法时 Invalidate 静默返回，不该 panic
	u.InvalidateBytes(TargetUnknown, "", 0, "", data)
	u.InvalidateURL(TargetGroup, "GROUP_A", FileTypeImage, "cover.png", "")
}

func TestMediaFailuresAreNotCached(t *testing.T) {
	apiErr := &APIError{HTTPStatus: 400, Message: "图片格式不支持", Code: ErrCodeImageFormat, ErrCode: ErrCodeImageFormat}
	client := &stubMediaClient{ttl: 86400, fail: apiErr}
	u, _ := newMediaTestUploader(client, 4)
	ctx := context.Background()
	data := mediaPNGBytes(16)

	_, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	if !errors.Is(err, apiErr) {
		t.Fatalf("错误链上应能取到原始 *APIError，实际 %v", err)
	}
	var got *APIError
	if !errors.As(err, &got) || got.Code != ErrCodeImageFormat {
		t.Errorf("应保留 APIError 的码，实际 %+v", got)
	}
	if u.CacheLen() != 0 {
		t.Errorf("失败的调用不该写缓存，实际 %d 条", u.CacheLen())
	}

	// 失败不进缓存：下一次会重新尝试
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data); err == nil {
		t.Fatal("stub 仍在报错，这里不该成功")
	}
	if got := client.callCount(); got != 2 {
		t.Errorf("失败后重试应真的再发一次，实际 %d 次", got)
	}
}

func TestMediaMissingFileInfoIsFailure(t *testing.T) {
	client := &stubMediaClient{ttl: 86400, emptyFileInfo: true}
	u, _ := newMediaTestUploader(client, 4)

	_, err := u.UploadBytes(context.Background(), TargetGroup, "GROUP_A", FileTypeImage, "cover.png", mediaPNGBytes(16))
	if !errors.Is(err, ErrMediaFileInfoMissing) {
		t.Fatalf("响应缺 file_info 应报 ErrMediaFileInfoMissing，实际 %v", err)
	}
	if u.CacheLen() != 0 {
		t.Errorf("缺 file_info 的响应不该写缓存，实际 %d 条", u.CacheLen())
	}
}

func TestMediaNilClientIsRejected(t *testing.T) {
	u := NewUploader(nil)
	_, err := u.UploadBytes(context.Background(), TargetGroup, "GROUP_A", FileTypeImage, "cover.png", mediaPNGBytes(16))
	if !errors.Is(err, ErrMediaClientNil) {
		t.Fatalf("空客户端应报 ErrMediaClientNil，实际 %v", err)
	}
	if u.CacheLen() != 0 {
		t.Errorf("缓存应为空，实际 %d 条", u.CacheLen())
	}
}

func TestMediaContextCanceledIsSurfaced(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", mediaPNGBytes(16))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 ctx 应把 context.Canceled 传出来，实际 %v", err)
	}
	if u.CacheLen() != 0 {
		t.Errorf("取消的调用不该写缓存，实际 %d 条", u.CacheLen())
	}
}

// ---------- 10. TargetKind 本身 ----------

func TestTargetKindZeroValueIsInvalid(t *testing.T) {
	var zero TargetKind
	if zero.Valid() {
		t.Error("零值 TargetKind 必须无效：忘了填就报错，不能默认落到某个目标")
	}
	if zero.String() != "unknown" {
		t.Errorf("零值 String 应为 unknown，实际 %q", zero.String())
	}
	if !TargetUser.Valid() || !TargetGroup.Valid() {
		t.Error("TargetUser / TargetGroup 应当有效")
	}
	if TargetUser.String() != "user" || TargetGroup.String() != "group" {
		t.Errorf("String 映射不对：%q / %q", TargetUser.String(), TargetGroup.String())
	}
	if TargetUser == TargetGroup {
		t.Error("单聊与群聊必须是不同的取值：两者接口不互通")
	}
}
