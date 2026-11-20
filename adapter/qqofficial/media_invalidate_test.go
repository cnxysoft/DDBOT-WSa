package qqofficial

// file_info 缓存的**失效清理**契约。
//
// 背景：file_info 的 TTL（实测 86400 秒）是平台侧的寿命，本地缓存只是照着它记了个到期时间。
// 平台一旦提前把素材作废（换实例、内部清理、素材被判失效），本地还以为它新鲜 —— 最长 24 小时内
// 每一次推送都拿同一枚坏 file_info 去发、每一次都失败，不会自愈。
// errors.go:186-188 把 40035003 / 40035008 / 40035009 明确归为「file_info / upload_id 已失效：
// 要重新上传，原样重试必失败」，所以正解是**把那条缓存删掉**，而不是等 TTL。
//
// 两个要点各自钉住：
//  1. 判据只收那批「file_info 失效」码 —— 内容类/权限类的失败删缓存只会白多一次上传；
//  2. 清理必须**精确到这次用的那条 key**：MediaInfo 带着 cacheKey 回来，同目标同内容的另一条
//     缓存（比如另一个群）不许被连坐，也绝不做「全清」。
//
// 复用 media_test.go 的 stubMediaClient / newMediaTestUploader / mediaPNGBytes。

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// mediaStaleAPIError 造一枚平台业务错误（HTTP 200、业务码落在 body 里，实测就是这种形状）。
func mediaStaleAPIError(code int) *APIError {
	return &APIError{HTTPStatus: 200, Code: code, ErrCode: code, Message: "素材已失效"}
}

// 命中缓存拿到同一枚 file_info；平台说它过期之后，这条缓存必须被清掉，
// 下一次上传要真的重新打接口，而不是继续复用坏 file_info。
func TestMediaSendFailureInvalidatesStaleCacheEntry(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 8)
	ctx := context.Background()
	data := mediaPNGBytes(64)

	first, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	reused, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if client.callCount() != 1 {
		t.Fatalf("第二次应命中缓存，实际累计上传 %d 次", client.callCount())
	}
	if reused.cacheKey == "" {
		t.Fatal("缓存命中返回的 MediaInfo 必须带着 cacheKey：发送失败要靠它精确清缓存")
	}
	if reused.cacheKey != first.cacheKey {
		t.Fatalf("同一条缓存两次拿到的 key 应当一致：%q vs %q", reused.cacheKey, first.cacheKey)
	}

	// 平台回报「图片已过期或已删除」（实测码 40035009）
	if !u.InvalidateOnSendFailure(reused, mediaStaleAPIError(ErrCodeImageExpired)) {
		t.Fatal("40035009 属于「file_info 已失效」，必须清掉这条缓存")
	}
	if got := u.CacheLen(); got != 0 {
		t.Fatalf("清完之后缓存应当是空的，实际还有 %d 条", got)
	}

	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("清掉缓存后重新上传应当成功：%v", err)
	}
	if got := client.callCount(); got != 2 {
		t.Fatalf("清掉缓存后必须重新上传（打到接口），实际累计上传 %d 次", got)
	}
}

// 不是「file_info 失效」那批码（权限 / 内容 / 网络）不许动缓存：重传同一份内容只会再失败一次。
func TestMediaSendFailureKeepsCacheForOtherErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"主动消息无权限 40034105", mediaStaleAPIError(ErrCodeProactiveNoPermission)},
		{"富媒体格式不支持 40034002", mediaStaleAPIError(ErrCodeMediaFormatUnsupportedAlt)},
		{"引用链接无效 40035010（不属于本机器人，不是过期）", mediaStaleAPIError(ErrCodeImageRefInvalid)},
		{"网络层错误", errors.New("dial tcp: connection reset by peer")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &stubMediaClient{ttl: 86400}
			u, _ := newMediaTestUploader(client, 8)
			data := mediaPNGBytes(64)
			info, err := u.UploadBytes(context.Background(), TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
			mediaMustNoErr(t, err)

			if u.InvalidateOnSendFailure(info, tc.err) {
				t.Fatalf("%s 不该清缓存（内容/权限问题重传不会变好）", tc.name)
			}
			if got := u.CacheLen(); got != 1 {
				t.Fatalf("%s 之后缓存条目应当还在，实际 %d 条", tc.name, got)
			}
		})
	}
}

// 只清这次用的那一条：同内容发到两个群是两条独立缓存，别连坐，更不许「全清」。
func TestMediaSendFailureInvalidatesOnlyItsOwnKey(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 8)
	ctx := context.Background()
	data := mediaPNGBytes(64)

	groupA, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	groupB, err := u.UploadBytes(ctx, TargetGroup, "GROUP_B", FileTypeImage, "cover.png", data)
	mediaMustNoErr(t, err)
	if groupA.cacheKey == groupB.cacheKey {
		t.Fatal("不同群的同内容必须是两条不同缓存（单聊/群聊不互通、目标维度在 key 里）")
	}
	if client.callCount() != 2 {
		t.Fatalf("两个群各上传一次，实际 %d 次", client.callCount())
	}

	if !u.InvalidateOnSendFailure(groupA, mediaStaleAPIError(ErrCodeMediaTaskNotFound)) {
		t.Fatal("40035003 属于「file_info 已失效」，必须清掉这条缓存")
	}
	if got := u.CacheLen(); got != 1 {
		t.Fatalf("只该清掉 A 那一条，实际缓存还剩 %d 条", got)
	}

	// B 仍然命中缓存，不该多打一次接口
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_B", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("B 重新取应当命中缓存：%v", err)
	}
	if client.callCount() != 2 {
		t.Fatalf("B 的缓存不该被连坐清掉，实际累计上传 %d 次", client.callCount())
	}
	// A 需要重新上传
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("A 重新取应当成功：%v", err)
	}
	if client.callCount() != 3 {
		t.Fatalf("A 的缓存已清，必须重新上传，实际累计上传 %d 次", client.callCount())
	}
}

// URL 上传走另一条哈希前缀，失效清理也要精确作用到它自己那条 key。
func TestMediaSendFailureInvalidatesURLEntry(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 8)
	ctx := context.Background()
	const coverURL = "https://example.com/cover.png"

	info, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", coverURL)
	mediaMustNoErr(t, err)
	if _, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", coverURL); err != nil {
		t.Fatalf("第二次应命中缓存：%v", err)
	}
	if client.callCount() != 1 {
		t.Fatalf("URL 上传第二次应命中缓存，实际 %d 次", client.callCount())
	}

	if !u.InvalidateOnSendFailure(info, mediaStaleAPIError(ErrCodeImageNotFound)) {
		t.Fatal("40035008 属于「file_info 已失效」，必须清掉这条 URL 缓存")
	}
	if _, err := u.UploadURL(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", coverURL); err != nil {
		t.Fatalf("清掉后重新上传应当成功：%v", err)
	}
	if client.callCount() != 2 {
		t.Fatalf("清掉 URL 缓存后必须重新上传，实际 %d 次", client.callCount())
	}
}

// 拿不到 key（info 为 nil，或 MediaInfo 不是本进程缓存给的）时只做判断、不猜不清，
// 尤其**不许**退化成「全清」。
func TestMediaSendFailureWithoutKeyTouchesNothing(t *testing.T) {
	client := &stubMediaClient{ttl: 86400}
	u, _ := newMediaTestUploader(client, 8)
	ctx := context.Background()
	data := mediaPNGBytes(64)
	if _, err := u.UploadBytes(ctx, TargetGroup, "GROUP_A", FileTypeImage, "cover.png", data); err != nil {
		t.Fatalf("上传失败：%v", err)
	}

	if u.InvalidateOnSendFailure(nil, mediaStaleAPIError(ErrCodeImageExpired)) {
		t.Fatal("info 为 nil 时没有可清的条目，不该返回 true")
	}
	handMade := &MediaInfo{FileUUID: "x", FileInfo: "y", TTL: 86400}
	if u.InvalidateOnSendFailure(handMade, mediaStaleAPIError(ErrCodeImageExpired)) {
		t.Fatal("没有 cacheKey 的 MediaInfo 不该清掉任何别人的条目")
	}
	if got := u.CacheLen(); got != 1 {
		t.Fatalf("这两次调用都不该动缓存，实际还剩 %d 条", got)
	}
}

// 判据本身：只认 errors.go 归为「file_info / upload_id 已失效」的三枚码，
// code 与 err_code 两侧都看，包了 %w 也要认得出来。
func TestMediaFileInfoStaleCodeJudgement(t *testing.T) {
	stale := []int{
		ErrCodeMediaTaskNotFound, // 40035003
		ErrCodeImageNotFound,     // 40035008
		ErrCodeImageExpired,      // 40035009
	}
	for _, code := range stale {
		if !isFileInfoStaleError(mediaStaleAPIError(code)) {
			t.Errorf("code=%d 属于「file_info 已失效」，必须判为失效", code)
		}
		// 业务码落在 err_code 侧（实测两种放法都有）
		if !isFileInfoStaleError(&APIError{HTTPStatus: 200, Code: 0, ErrCode: code}) {
			t.Errorf("err_code=%d 属于「file_info 已失效」，必须判为失效", code)
		}
		// 被上层包装过（partialSendError 用 %w 保住了 *APIError）
		wrapped := fmt.Errorf("第 1/1 个媒体发送失败：%w", mediaStaleAPIError(code))
		if !isFileInfoStaleError(wrapped) {
			t.Errorf("code=%d 被 fmt.Errorf 包一层之后仍要判为失效（errors.As 必须能穿过包装）", code)
		}
	}

	notStale := []error{
		mediaStaleAPIError(ErrCodeProactiveNoPermission),     // 40034105 权限
		mediaStaleAPIError(ErrCodeMediaFormatUnsupported),    // 850019 内容
		mediaStaleAPIError(ErrCodeMediaFormatUnsupportedAlt), // 40034002
		mediaStaleAPIError(ErrCodeImageRefInvalid),           // 40035010
		mediaStaleAPIError(ErrCodeMediaServerError),          // 40035006 服务端错误
		errors.New("网络错误"),
		nil,
	}
	for _, err := range notStale {
		if isFileInfoStaleError(err) {
			t.Errorf("%v 不属于「file_info 已失效」，不该清缓存", err)
		}
	}
}
