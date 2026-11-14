package qqofficial

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// 本文件负责富媒体上传与 file_info 缓存。
//
// 链路（均为实测结论）：
//
//	群聊：POST /v2/groups/{group_openid}/files
//	单聊：POST /v2/users/{user_openid}/files
//	请求：{file_type, file_data(或 url), file_name, srv_send_msg:false}
//	响应：{file_uuid, file_info, ttl, id}
//	发送：由 message.go 用 msg_type=7 + media.file_info（file_info 是序列化二进制，必须原样透传）
//
// 三条实测硬结论直接决定了这里的设计：
//
//  1. **单聊与群聊的上传接口不互通**——同一个文件要分别上传。所以缓存 key 必须带目标维度
//     （目标类型 + openid）：把单聊拿到的 file_info 发给群聊，平台侧只会失败。
//  2. **ttl 实测 86400 秒（24 小时）**，不是文档示例里的 300；同一个 file_info 还能跨消息反复复用
//     （实测连续发两次都 200）。所以缓存的价值很大：DDBOT 反复推同一张封面图不必重复上传。
//  3. **直传 file_data 上限 5 MiB**（文档写的「5M」按 MiB 算）。超了在这里就报错，不发出去让平台拒——
//     分片上传还没做，所以 >5 MiB 的本地媒体只能改成外链（URL 上传）。
//
// TODO(未实现-分片上传)：当前只支持「二进制直传」与「URL 上传」两条，>5M 的正路是分片：
//
//	POST /v2/{users|groups}/{openid}/upload_prepare  {file_type, file_size, file_name, md5, sha1, md5_10m}
//	  -> {upload_id, block_size, parts:[{index, presigned_url, block_size}], upload_config}
//	逐片 PUT 到 presigned_url -> POST .../upload_part_finish {upload_id, part_index, block_size, md5}
//	全部分片完成后带 upload_id 调 /files 合并（图片/视频/语音此时还会多返回 raw_url）。
//
// 超限的调用方会拿到 *MediaTooLargeError（含文件名、实际体积与上限），错误文案直接说
// 「这个文件超过 5 MiB，要改成外链（URL 上传）」。分片上传落地之前，外链是唯一的正路。

// file_type 取值。**格式限制一律以实测为准，不照文档写死**：
//
//	FileTypeImage (1)：实测 2026-11-11 直接在编译机上打 POST /v2/groups/{openid}/files
//	                   （file_type=1），拿到 200 + file_info + ttl=86400 的有：
//	                   png ✓ jpg ✓ gif（单帧）✓ gif（动图）✓ webp ✓ bmp ✓ tiff ✓
//	                   —— 文档那句「图片只支持 png/jpg」**不完整**，所以客户端**不做格式白名单**。
//	                   软限 20MB，超软限平台会降级成文件类型上传。
//	FileTypeVideo (2)：软限 30MB。文档写只支持 mp4；本地只拦「族别错配」（拿图片字节当视频）。
//	FileTypeVoice (3)：软限 20MB。文档写只支持 silk；同上，只拦族别错配。
//	FileTypeFile  (4)：任意文件，不挑内容；软限 200MB（超硬限平台直接报错）。
const (
	FileTypeImage = 1
	FileTypeVideo = 2
	FileTypeVoice = 3
	FileTypeFile  = 4
)

const (
	// MaxMediaDirectUploadBytes 是 file_data 二进制直传的上限。
	// 文档口径「不能超过 5M」，这里按 5 MiB 算：等于上限放行，超过即拒。
	MaxMediaDirectUploadBytes = 5 << 20

	// DefaultMediaCacheCapacity 是 file_info 缓存的默认条目上限（LRU 淘汰）。
	// 一条 file_info 约 1KB，256 条顶天几百 KB。
	DefaultMediaCacheCapacity = 256

	// DefaultMediaExpiryMargin 是复用缓存时的到期安全余量：file_info 距离过期不足这个时间就不再复用，
	// 免得刚拿到手、发消息时已经过期（上传与发送不是同一时刻）。
	DefaultMediaExpiryMargin = 60 * time.Second
)

// 本文件用到的哨兵错误。全是「参数不对」或「平台回的东西不对」，属**不可原样重试**那类。
var (
	ErrMediaTargetInvalid     = errors.New("qqofficial: 媒体上传的目标类型无效")
	ErrMediaTargetOpenIDEmpty = errors.New("qqofficial: 媒体上传的 openid 为空")
	ErrMediaFileTypeInvalid   = errors.New("qqofficial: file_type 无效（只支持 1图片/2视频/3语音/4文件）")
	ErrMediaFileNameEmpty     = errors.New("qqofficial: file_name 不能为空（平台会把文件名展示给用户）")
	ErrMediaDataEmpty         = errors.New("qqofficial: 上传数据为空")
	ErrMediaTooLarge          = errors.New("qqofficial: file_data 超过 5 MiB 直传上限")
	ErrMediaFormatUnsupported = errors.New("qqofficial: 文件格式与声明的 file_type 不符（发送侧不支持）")
	ErrMediaURLEmpty          = errors.New("qqofficial: url 上传的 url 为空")
	ErrMediaURLInvalid        = errors.New("qqofficial: url 不是合法的 http/https 地址")
	ErrMediaFileInfoMissing   = errors.New("qqofficial: 上传响应没有 file_info")
	ErrMediaClientNil         = errors.New("qqofficial: Uploader 没有可用的 HTTP 客户端")
)

// MediaTooLargeError 说明这次直传为什么被拒：**哪个文件**、多大、上限多少，以及出路。
//
// 为什么要带文件名：这条错误最终会出现在用户的推送失败日志里。只说「改用 URL 上传」
// 帮不上忙——用户既不知道是哪个文件，也没有「上传方式」可改。能执行的下一步只有
// 「把这个文件改成外链」。
type MediaTooLargeError struct {
	Name  string // 上传时的文件名（平台展示给用户的就是它）
	Size  int    // 实际字节数
	Limit int    // 直传上限（MaxMediaDirectUploadBytes）
}

func (e *MediaTooLargeError) Error() string {
	name := e.Name
	if name == "" {
		name = "（未命名的本地媒体）"
	}
	return fmt.Sprintf("qqofficial: 本地媒体 %s（%d 字节，约 %s MiB）超过平台 5 MiB 直传上限（%d 字节），"+
		"这条发不出去；本地媒体超过 5 MiB 需要改成外链 —— 把文件放到可公网访问的 http/https 地址"+
		"（图床 / 对象存储 / 自己的静态站），再用 URL 上传发那个地址。5 MiB 是平台限制，改不了",
		name, e.Size, humanMiB(e.Size), e.Limit)
}

// humanMiB 把字节数说成人话（一位小数）；只用于错误文案，不参与任何判断。
func humanMiB(n int) string {
	return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64)
}

// Is 让 errors.Is(err, ErrMediaTooLarge) 成立。
func (e *MediaTooLargeError) Is(target error) bool { return target == ErrMediaTooLarge }

// MediaFormatError 说明格式为什么不放行。
type MediaFormatError struct {
	Declared int    // 调用方声明的 file_type
	Detected string // 魔数认出来的真实格式，如 gif
}

func (e *MediaFormatError) Error() string {
	return fmt.Sprintf("qqofficial: 【本 adapter 的客户端校验，不是平台报的】声明 file_type=%d(%s)，"+
		"但字节魔数是 %s —— 族别对不上（比如把视频/语音的字节当图片发），发出去平台只会当垃圾处理，"+
		"所以先在这里拦下来。请改用对应的 file_type（图片 1 / 视频 2 / 语音 3 / 文件 4）；"+
		"如果确实要原样发送，用 file_type=4(文件)。"+
		"另外：图片**没有格式白名单**了，png/jpg/gif（含动图）/webp/bmp/tiff 都实测可发。",
		e.Declared, fileTypeName(e.Declared), e.Detected)
}

// Is 让 errors.Is(err, ErrMediaFormatUnsupported) 成立。
func (e *MediaFormatError) Is(target error) bool { return target == ErrMediaFormatUnsupported }

func fileTypeName(fileType int) string {
	switch fileType {
	case FileTypeImage:
		return "图片"
	case FileTypeVideo:
		return "视频"
	case FileTypeVoice:
		return "语音"
	case FileTypeFile:
		return "文件"
	}
	return "未知"
}

// TargetKind 是消息/媒体的目标类型：单聊还是群聊。
//
// 放在本文件只是因为上传路径先用到它；message.go 复用同一份定义。
// **包内只允许有这一份**：两边各定义一个会直接编译冲突。
//
// 单聊与群聊的接口路径、以及上传接口的互通性都不同（实测不互通），所以这个维度必须贯穿到底。
type TargetKind uint8

const (
	// TargetUnknown 是零值。调用方忘了填就直接报错，绝不默认成某个目标——
	// 把群消息发到单聊接口（或反过来）是静默失败，比报错难查得多。
	TargetUnknown TargetKind = iota
	TargetUser
	TargetGroup
)

// Valid 判断目标类型是否已明确。
func (k TargetKind) Valid() bool { return k == TargetUser || k == TargetGroup }

func (k TargetKind) String() string {
	switch k {
	case TargetUser:
		return "user"
	case TargetGroup:
		return "group"
	}
	return "unknown"
}

// MediaInfo 是一次上传的结果，直接给 message.go 的 msg_type=7 用。
//
// TTL 是响应里的 ttl（秒）——**实测 86400**，不是文档示例的 300；响应没给或给 0（文档口径「长期」）时
// 兜底成 MediaTTLFallback（86400）。同一份 MediaInfo 在 ttl 内可以跨消息反复复用（实测连续发两次都 200）。
type MediaInfo struct {
	FileUUID string
	FileInfo string
	TTL      int

	// cacheKey 是这枚 MediaInfo 在 Uploader 缓存里的 key（见 uploadParams.key）。
	//
	// 为什么必须带在返回值上：**发送**富媒体失败时，如果平台回报的是「这枚 file_info 已失效」，
	// 就得把对应的缓存条目精确删掉（见 InvalidateOnSendFailure），否则本地会一直复用同一枚
	// 坏 file_info，最长 24 小时内的每一次推送都失败、不会自愈。而发送侧手里只剩 MediaInfo
	// （file_info 交给 Client.SendMedia 时，上传参数早已出了作用域），拿不到 key 就只能「全清」，
	// 那会白丢别的媒体的缓存、让它们重新上传。所以 key 跟着结果一起回去。
	//
	// 空串表示这枚 MediaInfo 不是本进程缓存给的（比如单测自己拼的、或从别处反序列化来的）。
	cacheKey string
}

// MediaClient 是本文件需要的 HTTP 能力面：只有 PostJSON 一个方法。
// 用接口而不是具体类型，是为了单测能打桩（单测里绝不发真实请求）。
// *Client 天然满足它（签名已冻结：PostJSON(ctx context.Context, path string, body any, out any) error）。
type MediaClient interface {
	PostJSON(ctx context.Context, path string, body any, out any) error
}

// uploadFileRequest 是 /files 的请求体。
//
// 两条硬事实：
//   - file_data 与 url 二选一（直传 / 让平台去下载转存）；
//   - srv_send_msg 恒为 false：我们只要 file_info，消息由 message.go 自己发。
//     置 true 会顺手发一条消息、并**占用主动消息频次**，那个副作用不是上传该干的事。
type uploadFileRequest struct {
	FileType   int    `json:"file_type"`
	FileData   string `json:"file_data,omitempty"`
	URL        string `json:"url,omitempty"`
	FileName   string `json:"file_name"`
	SrvSendMsg bool   `json:"srv_send_msg"`
}

// uploadFileResponse 是 /files 的响应体。
type uploadFileResponse struct {
	FileUUID string `json:"file_uuid"`
	FileInfo string `json:"file_info"`
	TTL      int    `json:"ttl"`

	// ID：文档写「仅 srv_send_msg=true 时返回」，实测 false 也返回，留字段备查。
	ID string `json:"id"`
	// RawURL：只有分片合并且 file_type 为图片/视频/语音时才有，直传没有。
	RawURL string `json:"raw_url"`
}

// Uploader 负责上传与 file_info 缓存，并发安全。
type Uploader struct {
	client MediaClient
	cache  *mediaCache
	now    func() time.Time

	mu       sync.Mutex
	inflight map[string]*inflightUpload
}

// inflightUpload 是一次「还没回来」的上传。同 key 的并发调用者在这里等同一个结果，
// 免得同一张图在同一目标上被同时上传好几遍。
type inflightUpload struct {
	done chan struct{}
	info MediaInfo
	err  error
}

// NewUploader 用默认缓存容量（DefaultMediaCacheCapacity）建一个 Uploader。
func NewUploader(client MediaClient) *Uploader {
	return NewUploaderWithCapacity(client, DefaultMediaCacheCapacity)
}

// NewUploaderWithCapacity 指定缓存条目上限（LRU）；capacity <= 0 时退回默认值。
func NewUploaderWithCapacity(client MediaClient, capacity int) *Uploader {
	if capacity <= 0 {
		capacity = DefaultMediaCacheCapacity
	}
	return &Uploader{
		client:   client,
		cache:    newMediaCache(capacity),
		now:      time.Now,
		inflight: make(map[string]*inflightUpload),
	}
}

// UploadBytes 走二进制直传（file_data）。data 超过 5M 直接返回 *MediaTooLargeError，不发请求。
//
// 命中缓存时完全不碰网络。缓存 key = 目标类型 + openid + file_type + 文件名 + 内容哈希：
// 单聊/群聊不互通，所以目标维度必须在 key 里；文件名也在 key 里，是因为 file_type=4（文件）时
// 平台会把文件名展示给用户，同一份字节换个名字上传不能串用。
func (u *Uploader) UploadBytes(ctx context.Context, kind TargetKind, openID string, fileType int, fileName string, data []byte) (*MediaInfo, error) {
	p, err := newUploadParams(kind, openID, fileType, fileName, bytesMediaDigest(data))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, ErrMediaDataEmpty
	}
	// 先卡体积再算别的：超限的调用方不该为一次注定失败的调用付 CPU。
	if len(data) > MaxMediaDirectUploadBytes {
		return nil, &MediaTooLargeError{Name: fileName, Size: len(data), Limit: MaxMediaDirectUploadBytes}
	}
	if err := checkMediaFormat(fileType, data); err != nil {
		return nil, err
	}
	// 只有真要发请求时才做 base64（命中缓存时省掉这份开销，5M 数据的 base64 不便宜）。
	return u.upload(ctx, p, func() (any, error) {
		return uploadFileRequest{
			FileType:   fileType,
			FileData:   base64.StdEncoding.EncodeToString(data),
			FileName:   fileName,
			SrvSendMsg: false,
		}, nil
	})
}

// UploadURL 走 URL 上传：把地址给平台，由它去下载转存。没有 5M 限制，但对端下载可能失败。
//
// 缓存与直传共用一张表，但哈希前缀不同（url-sha256: 对 sha256:），所以同一份字节
// 与「内容恰好是这段字节的地址」不会撞车。地址不变就复用同一个 file_info（TTL 内）。
func (u *Uploader) UploadURL(ctx context.Context, kind TargetKind, openID string, fileType int, fileName, rawURL string) (*MediaInfo, error) {
	if rawURL == "" {
		return nil, ErrMediaURLEmpty
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrMediaURLInvalid, rawURL)
	}
	p, err := newUploadParams(kind, openID, fileType, fileName, urlMediaDigest(rawURL))
	if err != nil {
		return nil, err
	}
	return u.upload(ctx, p, func() (any, error) {
		return uploadFileRequest{
			FileType:   fileType,
			URL:        rawURL,
			FileName:   fileName,
			SrvSendMsg: false,
		}, nil
	})
}

// ---------- 失效清理 ----------
//
// 为什么必须有这一节：file_info 的 TTL（实测 86400 秒）是**平台侧**的寿命，本地缓存只是照着它
// 记了个到期时间。平台一旦提前把素材作废（换实例、内部清理、素材被判失效），本地还以为它新鲜，
// 于是最长 24 小时内每一次推送都拿同一枚坏 file_info 去发、每一次都失败，**不会自愈**。
// errors.go 把 40035003 / 40035008 / 40035009 明确归为「file_info / upload_id 已失效：要重新上传，
// 原样重试必失败」，所以正解是把那条缓存删掉——下一次（或调用方的重试）就会重新上传。
//
// 两条入口，按调用方手里有什么选：
//   - InvalidateOnSendFailure(info, err)：**发送**富媒体失败后用。它有 MediaInfo，key 就在里面，
//     判据（是不是那批失效码）也在本文件里，调用方只需要传错误；
//   - InvalidateBytes / InvalidateURL：手里只有原始上传参数（目标、文件名、内容/URL）时用。

// isFileInfoStaleError 判断这次失败是不是「file_info 本身失效了，必须重新上传」。
//
// 只收 errors.go:186-188 那三枚被明确归为「file_info / upload_id 已失效」的码：
//
//	ErrCodeMediaTaskNotFound (40035003) 上传任务不存在 / 已过期 / 不属于当前机器人
//	ErrCodeImageNotFound     (40035008) 图片不存在或已删除
//	ErrCodeImageExpired      (40035009) 图片已过期或已删除
//
// 故意**不收**的（收进来只会把一枚坏缓存换成另一枚坏缓存，白多一次上传）：
//   - 40035010（引用链接无效 / 不属于当前机器人）：errors.go 把它归普通参数错误，不是「过期」；
//   - 40034002 / 850019（富媒体格式不支持，实测过小的图也落这里）：内容问题，重传同一份字节
//     只会再被拒一次；
//   - 40034105 / 40034006 等权限、内容类：跟 file_info 没关系。
//
// code 与 err_code 两侧都查（实测业务码放哪一侧都有）；错误被包装过也认得出来（AsAPIError 走 errors.As）。
func isFileInfoStaleError(err error) bool {
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr == nil {
		return false
	}
	for _, c := range []int{apiErr.Code, apiErr.ErrCode} {
		switch c {
		case ErrCodeMediaTaskNotFound, ErrCodeImageNotFound, ErrCodeImageExpired:
			return true
		}
	}
	return false
}

// InvalidateOnSendFailure 在**发送富媒体失败**之后调用：平台说这枚 file_info 已失效就清掉它的缓存。
//
// 返回是否真的删掉了一条缓存条目（调用方可以据此打日志或者计数）。
//
// 拿不到 key 时什么都不删：info 为 nil（上传就没成功）、或 cacheKey 为空（不是本进程缓存给的）。
// 这里**故意不做「全清」兜底**——那会把别的媒体、别的群的缓存一起打掉，让它们无谓重传。
func (u *Uploader) InvalidateOnSendFailure(info *MediaInfo, cause error) bool {
	if u == nil || info == nil || !isFileInfoStaleError(cause) {
		return false
	}
	return u.invalidateStaleKey(info.cacheKey, cause, "发送富媒体失败")
}

// InvalidateBytes 丢掉一条直传缓存（调用方手里只有原始上传参数时用；有 MediaInfo 时用 InvalidateOnSendFailure）。
func (u *Uploader) InvalidateBytes(kind TargetKind, openID string, fileType int, fileName string, data []byte) {
	p, err := newUploadParams(kind, openID, fileType, fileName, bytesMediaDigest(data))
	if err != nil {
		return
	}
	u.cache.remove(p.key())
}

// InvalidateURL 丢掉一条 URL 上传缓存（同 InvalidateBytes：有 MediaInfo 时用 InvalidateOnSendFailure）。
func (u *Uploader) InvalidateURL(kind TargetKind, openID string, fileType int, fileName, rawURL string) {
	p, err := newUploadParams(kind, openID, fileType, fileName, urlMediaDigest(rawURL))
	if err != nil {
		return
	}
	u.cache.remove(p.key())
}

// invalidateStaleKey 是失效清理的公共落点（发送失败、上传失败都走这里）：确认 cause 属于
// 「file_info 失效」之后删掉 key。
//
// 删的时候打一条 Warn 带上失效码：这是**自愈动作**，运维看日志时要能看出「平台判它失效了、
// 我们已经丢掉缓存、下一次会重新上传」，而不是以为推送失败就此钉死。
// 返回是否真的删掉了一条条目；key 为空时不猜、不清，直接返回 false。
func (u *Uploader) invalidateStaleKey(key string, cause error, phase string) bool {
	if u == nil || key == "" || !isFileInfoStaleError(cause) {
		return false
	}
	removed := u.cache.remove(key)
	apiErr, _ := AsAPIError(cause)
	detail := "非业务错误"
	if apiErr != nil {
		detail = fmt.Sprintf("code=%d err_code=%d", apiErr.Code, apiErr.ErrCode)
	}
	logger.Warnf("%s: %s：平台回报 file_info 已失效（%s），已清掉这条缓存（命中条目=%v），"+
		"下一次会重新上传拿新的 file_info", ConfigKeyPrefix, phase, detail, removed)
	return removed
}

// CacheLen 是当前缓存条目数（观测用）。
func (u *Uploader) CacheLen() int {
	if u == nil || u.cache == nil {
		return 0
	}
	return u.cache.len()
}

// upload 是两条上传路径的公共骨架：查缓存 → 合并并发 → 再查缓存 → 打接口 → 写缓存。
func (u *Uploader) upload(ctx context.Context, p uploadParams, build func() (any, error)) (*MediaInfo, error) {
	if u == nil || u.client == nil {
		return nil, ErrMediaClientNil
	}
	key := p.key()
	if info, ok := u.cache.get(key, u.now(), DefaultMediaExpiryMargin); ok {
		info.cacheKey = key
		return &info, nil
	}

	call, leader := u.beginInflight(key)
	if !leader {
		// 已经有人在传同一份东西：等它的结果，不再发一遍。
		select {
		case <-call.done:
			if call.err != nil {
				return nil, call.err
			}
			info := call.info
			info.cacheKey = key
			return &info, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// 拿到领头资格后**必须再查一次缓存**。
	//
	// 上面那次 get 与这里登记成功之间存在窗口：可能刚好有人把同一份内容传完、
	// 写进缓存并注销了 inflight 记录，于是这次 get 看到 miss、登记时又没人在飞，
	// 结果就是一次纯粹的重复上传（-race 下的并发用例抓到过）。
	// 只要 put 一定发生在注销 inflight 之前，这一次补查就能把窗口关掉。
	if info, ok := u.cache.get(key, u.now(), DefaultMediaExpiryMargin); ok {
		info.cacheKey = key
		u.finishInflight(key, &info, nil)
		return &info, nil
	}

	var (
		info *MediaInfo
		err  error
	)
	body, buildErr := build()
	if buildErr != nil {
		err = buildErr
	} else {
		info, err = u.performUpload(ctx, p, body)
	}
	u.finishInflight(key, info, err)
	if err != nil {
		// 上传失败时看一眼：如果平台回报的是「file_info / 上传任务已失效」，就把同 key 的缓存条目
		// 也清掉。**正常路径下这里本来就是空操作**（走到这里说明刚查过缓存是 miss，而且本次请求
		// 带的是字节或 URL，根本没有 file_info 可失效），留着它只为一种情况：并发的另一次上传
		// 刚好在我们查缓存之后、这次失败之前把同一 key 写进缓存，那枚条目不该留在那儿。
		u.invalidateStaleKey(key, err, "上传媒体失败")
		return nil, err
	}
	return info, nil
}

// performUpload 真正打一次 /files。
func (u *Uploader) performUpload(ctx context.Context, p uploadParams, body any) (*MediaInfo, error) {
	var resp uploadFileResponse
	if err := u.client.PostJSON(ctx, p.path(), body, &resp); err != nil {
		return nil, fmt.Errorf("上传媒体到%s失败: %w", p.targetLabel(), err)
	}
	if resp.FileInfo == "" {
		// 200 但没有 file_info：拿它发消息也没用，当失败处理，不写缓存。
		return nil, fmt.Errorf("%w: file_uuid=%q", ErrMediaFileInfoMissing, resp.FileUUID)
	}
	ttl := resp.TTL
	if ttl <= 0 {
		// 文档说 0 表示「长期」，但实测一律 86400。宁可多传一次，也不发一个可能已过期的 file_info。
		ttl = int(MediaTTLFallback / time.Second)
	}
	info := &MediaInfo{FileUUID: resp.FileUUID, FileInfo: resp.FileInfo, TTL: ttl, cacheKey: p.key()}
	u.cache.put(p.key(), *info, u.now().Add(time.Duration(ttl)*time.Second))
	return info, nil
}

// beginInflight 登记一次进行中的上传；返回值第二项为 false 表示自己不是领头的那个。
func (u *Uploader) beginInflight(key string) (*inflightUpload, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if call, ok := u.inflight[key]; ok {
		return call, false
	}
	call := &inflightUpload{done: make(chan struct{})}
	u.inflight[key] = call
	return call, true
}

// finishInflight 收尾：先写结果再关闭 channel，等待者靠 channel 关闭建立可见性（happens-before）。
//
// 顺序要点：performUpload 里的 cache.put 在删 inflight 记录之前完成，所以不存在
// 「缓存里没有、inflight 也查不到」的空窗——并发的同一份内容最多只会真上传一次。
func (u *Uploader) finishInflight(key string, info *MediaInfo, err error) {
	u.mu.Lock()
	call := u.inflight[key]
	delete(u.inflight, key)
	if call != nil {
		if err == nil && info != nil {
			call.info = *info
		}
		call.err = err
	}
	u.mu.Unlock()
	if call != nil {
		close(call.done)
	}
}

// uploadParams 是缓存 key 与请求路径的原料。
type uploadParams struct {
	kind     TargetKind
	openID   string
	fileType int
	fileName string
	digest   string
}

func newUploadParams(kind TargetKind, openID string, fileType int, fileName, digest string) (uploadParams, error) {
	if !kind.Valid() {
		return uploadParams{}, fmt.Errorf("%w: %d", ErrMediaTargetInvalid, int(kind))
	}
	if openID == "" {
		return uploadParams{}, ErrMediaTargetOpenIDEmpty
	}
	if fileType < FileTypeImage || fileType > FileTypeFile {
		return uploadParams{}, fmt.Errorf("%w: %d", ErrMediaFileTypeInvalid, fileType)
	}
	if fileName == "" {
		return uploadParams{}, ErrMediaFileNameEmpty
	}
	return uploadParams{kind: kind, openID: openID, fileType: fileType, fileName: fileName, digest: digest}, nil
}

// key 是缓存 key，五个维度缺一不可：
//
//	目标类型 + openid  —— 单聊/群聊上传不互通，实测同一份字节要分别上传；
//	file_type          —— 同一份字节当图片传与当文件传是不同的 file_info；
//	文件名             —— file_type=4 时平台会把文件名展示给用户，不能串用；
//	内容哈希           —— 同目标下内容一样才谈得上复用。
//
// openid 与文件名用 strconv.Quote 包一层再拼接：转义后里面不可能出现分隔符，
// 不会有两个不同的 key 拼成同一个串（也顺带让 key 可读）。
func (p uploadParams) key() string {
	return fmt.Sprintf("%d|%s|%d|%s|%s",
		p.kind, strconv.Quote(p.openID), p.fileType, strconv.Quote(p.fileName), p.digest)
}

// path 拼上传路径。openid 是十六进制串，转义后基本原样。
func (p uploadParams) path() string {
	if p.kind == TargetGroup {
		return fmt.Sprintf(PathGroupFiles, url.PathEscape(p.openID))
	}
	return fmt.Sprintf(PathUserFiles, url.PathEscape(p.openID))
}

func (p uploadParams) targetLabel() string {
	if p.kind == TargetGroup {
		return "群 " + p.openID
	}
	return "单聊 " + p.openID
}

func bytesMediaDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func urlMediaDigest(rawURL string) string {
	// 前缀与 bytesMediaDigest 不同：字节相同但来源不同（一个是内容一个是地址）不能共用缓存。
	sum := sha256.Sum256([]byte(rawURL))
	return "url-sha256:" + hex.EncodeToString(sum[:])
}

// pngMagic 是 PNG 的 8 字节魔数（写成字节字面量，避免源码里的转义序列看错）。
var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// checkMediaFormat 只做「族别错配」这一件事，不再做格式白名单。
//
// 历史教训（实测踩出来的）：这里原来按文档写死「图片只支持 png/jpg」，结果 gif 被本地拦掉，
// /倒放 处理完动图后**连请求都没发出去**就报错，功能等于被我们自己的校验废掉。
// 实测（2026-11-11，直接打 POST /v2/groups/{openid}/files，file_type=1）确认平台接受
// png / jpg / gif（含动图）/ webp / bmp / tiff —— 文档那句「只支持 png/jpg」不完整。
//
// 所以现在：
//   - **不做格式白名单**：认得出的图片格式一律放行；认不出魔数的一律放行（比如不带 ftyp 的 mp4、
//     加壳的 silk、bmp/tiff），交给平台判——平台拒了会返回明确错误码，我们照实报；
//   - 只拦**族别错配**：声明 file_type=1(图片) 字节却是 mp4/silk 这类，那不是格式偏好问题，
//     是发出去必然不对，本地拦掉能给出比平台错误码清楚得多的提示（这是百分百确定的判断，不是猜文档）；
//   - file_type=4（文件）不挑内容，直接放行。
func checkMediaFormat(declared int, data []byte) error {
	if declared == FileTypeFile {
		return nil
	}
	format, family, ok := sniffMediaFormat(data)
	if !ok {
		return nil
	}
	if family != declared {
		return &MediaFormatError{Declared: declared, Detected: format}
	}
	return nil
}

// sniffMediaFormat 只看最保险的魔数；认不出来返回 ok=false。
func sniffMediaFormat(data []byte) (format string, fileType int, ok bool) {
	switch {
	case bytes.HasPrefix(data, pngMagic):
		return "png", FileTypeImage, true
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "jpg", FileTypeImage, true
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif", FileTypeImage, true
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && string(data[8:12]) == "WEBP":
		return "webp", FileTypeImage, true
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return "mp4", FileTypeVideo, true
	case bytes.Contains(headBytes(data, 16), []byte("#!SILK")):
		// silk 有 0x02 打头的版本，所以在开头 16 字节里找标记，而不是做前缀比对。
		return "silk", FileTypeVoice, true
	}
	return "", 0, false
}

func headBytes(data []byte, n int) []byte {
	if len(data) < n {
		return data
	}
	return data[:n]
}

// ---------- file_info 缓存 ----------

type mediaCacheEntry struct {
	key       string
	info      MediaInfo
	expiresAt time.Time
}

// mediaCache 是按 key 索引的 LRU + TTL 缓存，自带互斥锁，可并发使用。
//
// 淘汰策略：容量满了从队尾（最久未用）丢。过期条目不主动全表清理——它们在下一次 get
// 被查到、或作为队尾被挤出时消失；真取到过期条目会当场删掉，绝不返回给调用方。
type mediaCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	lru      *list.List // 队首 = 最近使用；Value 是 *mediaCacheEntry
}

func newMediaCache(capacity int) *mediaCache {
	if capacity <= 0 {
		capacity = DefaultMediaCacheCapacity
	}
	return &mediaCache{
		capacity: capacity,
		entries:  make(map[string]*list.Element),
		lru:      list.New(),
	}
}

// get 取一条**还够新鲜**的缓存。margin 是到期余量：剩余寿命不足 margin 就当作不可用。
func (c *mediaCache) get(key string, now time.Time, margin time.Duration) (MediaInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return MediaInfo{}, false
	}
	entry := elem.Value.(*mediaCacheEntry)
	if !now.Before(entry.expiresAt.Add(-margin)) {
		// 过期或已进入余量区：删掉，绝不返回。
		c.lru.Remove(elem)
		delete(c.entries, key)
		return MediaInfo{}, false
	}
	c.lru.MoveToFront(elem)
	return entry.info, true
}

func (c *mediaCache) put(key string, info MediaInfo, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		entry := elem.Value.(*mediaCacheEntry)
		entry.info = info
		entry.expiresAt = expiresAt
		c.lru.MoveToFront(elem)
		return
	}
	c.entries[key] = c.lru.PushFront(&mediaCacheEntry{key: key, info: info, expiresAt: expiresAt})
	for c.lru.Len() > c.capacity {
		back := c.lru.Back()
		if back == nil {
			return
		}
		entry := back.Value.(*mediaCacheEntry)
		c.lru.Remove(back)
		delete(c.entries, entry.key)
	}
}

// remove 删掉一条缓存，返回是否真的删到了（失效清理要靠这个返回值区分「清掉了」和「本来就没有」）。
func (c *mediaCache) remove(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.lru.Remove(elem)
		delete(c.entries, key)
		return true
	}
	return false
}

func (c *mediaCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
