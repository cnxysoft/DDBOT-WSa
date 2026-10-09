package douyin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// LinkKind 是抖音链接目标类型。
type LinkKind string

// 成品 Kind 取值只有三个，target 与 LinkDetail 共用同一套：
//   - work    : 视频/图文作品（成品 canonicalDouyinWorkURL 再用 path 里的 "/note/" 区分 video/note 地址）；
//   - profile : 用户主页；
//   - live    : 直播间。
//
// 证据（linux-x64/DDBOT-Lite）：
//   - douyin.FetchLinkDetail @0xd8ad20 按 target.Kind 分派：len4 "live"@0xd8ad66 →
//     fetchDouyinLiveDetail(0xd8ca00)、len4 "work"@0xd8ad6e → fetchDouyinWorkDetail(0xd8ae20)、
//     len7 "profile"@0xd8ad92 → fetchDouyinProfileDetail(0xd8ce60)，其余返回包级 error；
//   - douyin.targetFromDouyinURL @0xd89dd0 写 Kind：0x109964b="work"（video/note 两处）、
//     0x10a31d7="profile"（user）、0x1099557="live"（live）；
//   - LinkDetail 构造点：parseDouyinWorkDetail Kind="work"(0x109964b)、
//     fetchDouyinLiveDetail Kind="live"(0x1099557)、fetchDouyinProfileDetail Kind="profile"(0x10a31d7)。
const (
	LinkKindWork    LinkKind = "work"
	LinkKindProfile LinkKind = "profile"
	LinkKindLive    LinkKind = "live"
)

// LinkTarget 是从链接中解析出的订阅目标（类型 + ID + 规范化地址）。
type LinkTarget struct {
	Kind LinkKind
	ID   string
	URL  string
}

// LinkDetail 是链接解析出的完整详情，供消息渲染使用。
type LinkDetail struct {
	Kind         LinkKind
	ID           string
	URL          string
	Name         string
	DisplayID    string
	Description  string
	Title        string
	Cover        string
	Images       []string
	GalleryItems []DouyinGalleryItem
	VideoURL     string
	CreatedAt    time.Time
	Living       bool
	StatusKnown  bool
	ViewerText   string
}

// DouyinGalleryItem 是图集单项（普通图或实况图，实况图带动态短片）。
type DouyinGalleryItem struct {
	Kind     string
	ClipType int
	ImageURL string
	VideoURL string
}

// douyinLinkRedirectMax 是短链跟随上限（文档 L118：跳转上限 8 次）。
const douyinLinkRedirectMax = 8

// 正则常量（均为二进制原文）。
var (
	// 通用 URL 提取（共享）。
	douyinURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	// 抖音域名+路径匹配（isAllowedDouyinHost/isDouyinShortHost 的域名白名单）。
	douyinHostPathPattern = regexp.MustCompile(`(?i)(?:[a-z0-9-]+\.)+(?:douyin\.com|iesdouyin\.com)(?::\d+)?/[^\s<>"']+`)
	// 视频/图文作品 ID。
	douyinWorkIDPattern = regexp.MustCompile(`(?i)/(?:share/)?(?:video|note)/(\d+)`)
	// 主页 SEC_UID。
	douyinUserIDPattern = regexp.MustCompile(`(?i)/user/([^/?#]+)`)
	// JS 跳转提取（双引号与单引号两式）。
	douyinJSRedirectPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?is)(?:window\.)?location\.href\s*=\s*"([^"]+)"`),
		regexp.MustCompile(`(?is)(?:window\.)?location\.href\s*=\s*'([^']+)'`),
		regexp.MustCompile(`(?is)(?:window\.)?location\.replace\(\s*"([^"]+)"\s*\)`),
		regexp.MustCompile(`(?is)(?:window\.)?location\.replace\(\s*'([^']+)'\s*\)`),
		regexp.MustCompile(`(?is)(?:window\.)?location\s*=\s*"([^"]+)"`),
		regexp.MustCompile(`(?is)(?:window\.)?location\s*=\s*'([^']+)'`),
	}
)

// ResolveLink 从聊天文本中解析第一个抖音链接目标。
func ResolveLink(text string) (*LinkTarget, error) {
	matched := douyinURLPattern.FindAllString(text, -1)
	for _, raw := range matched {
		raw = trimDouyinURL(raw)
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		if !isAllowedDouyinHost(u.Hostname()) {
			continue
		}
		target, err := targetFromDouyinURL(u)
		if err != nil {
			continue
		}
		return target, nil
	}
	// 兜底：无 scheme 的裸域名链接（如聊天中被拆断的分享文本）。
	if m := douyinHostPathPattern.FindString(text); m != "" {
		m = trimDouyinURL(m)
		u, err := url.Parse("https://" + m)
		if err == nil && u.Host != "" {
			if target, err := targetFromDouyinURL(u); err == nil {
				return target, nil
			}
		}
	}
	return nil, errors.New("仅支持抖音视频、图文、直播间和用户主页链接")
}

// trimDouyinURL 去掉链接尾部的中文标点等残留。
func trimDouyinURL(raw string) string {
	return strings.TrimRight(raw, "\"'.,!?;:)]}>，。！？；：、）》】」』")
}

// isAllowedDouyinHost 判断 host 是否为允许的 douyin.com/iesdouyin.com 子域。
func isAllowedDouyinHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if isDouyinDomain(host) {
		return true
	}
	if host == "iesdouyin.com" || strings.HasSuffix(host, ".iesdouyin.com") {
		return true
	}
	return false
}

// isDouyinShortHost 判断是否为抖音短链主机（v.douyin.com / s.douyin.com 等单字符前缀）。
// 二进制内没有完整短链字面量，按子域长度+形式判定。
func isDouyinShortHost(host string) bool {
	host = strings.ToLower(host)
	if !strings.HasSuffix(host, ".douyin.com") {
		return false
	}
	labels := strings.TrimSuffix(host, ".douyin.com")
	labels = strings.TrimPrefix(labels, "www.")
	return labels == "v" || labels == "s"
}

// targetFromDouyinURL 把抖音 URL 解析成链接目标。
func targetFromDouyinURL(u *url.URL) (*LinkTarget, error) {
	if u == nil || u.Host == "" {
		return nil, errors.New("仅支持抖音视频、图文、直播间和用户主页链接")
	}
	path := u.Path
	// 作品直链与主页链接。
	if m := douyinWorkIDPattern.FindStringSubmatch(path); len(m) >= 2 {
		id, err := numericDouyinID(m[1])
		if err != nil {
			return nil, err
		}
		// 成品：video 与 note 都是 Kind="work"，note/video 的地址差异交给
		// canonicalDouyinWorkURL 用 path 里的 "/note/" 判定（@0xd89e43/0xd89eb8 均为 "work"）。
		return &LinkTarget{Kind: LinkKindWork, ID: id, URL: canonicalDouyinWorkURL(id, path)}, nil
	}
	if m := douyinUserIDPattern.FindStringSubmatch(path); len(m) >= 2 {
		return &LinkTarget{Kind: LinkKindProfile, ID: m[1], URL: BaseHost + "/user/" + m[1]}, nil
	}
	// modal_id 参数兜底（分享短链解析出的作品 ID）。
	if modalID := u.Query().Get("modal_id"); modalID != "" {
		if id, err := numericDouyinID(modalID); err == nil {
			return &LinkTarget{Kind: LinkKindWork, ID: id, URL: canonicalDouyinWorkURL(id, path)}, nil
		}
	}
	// sec_anchor_id 参数（用户页锚点）。
	if secUID := u.Query().Get("sec_anchor_id"); secUID != "" {
		return &LinkTarget{Kind: LinkKindProfile, ID: secUID, URL: BaseHost + "/user/" + secUID}, nil
	}
	// 直播间：live.douyin.com/<web_rid> 或 /live/<web_rid>。
	if strings.HasPrefix(u.Hostname(), "live.") || strings.HasPrefix(path, "/live/") {
		roomID := strings.Trim(path, "/")
		if strings.HasPrefix(roomID, "live/") {
			roomID = strings.TrimPrefix(roomID, "live/")
		}
		if roomID != "" && !strings.Contains(roomID, "/") {
			return &LinkTarget{Kind: LinkKindLive, ID: roomID, URL: BaseLiveHost + "/" + roomID}, nil
		}
	}
	// 短链：先跟随跳转再解析。
	if isDouyinShortHost(u.Hostname()) || strings.HasPrefix(path, "/share/") {
		resolved, err := resolveDouyinShortURL(u.String())
		if err != nil {
			return nil, err
		}
		resolvedURL, err := url.Parse(resolved)
		if err != nil {
			return nil, err
		}
		return targetFromDouyinURL(resolvedURL)
	}
	return nil, errors.New("仅支持抖音视频、图文、直播间和用户主页链接")
}

// numericDouyinID 校验作品 ID 为纯数字。
func numericDouyinID(id string) (string, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", fmt.Errorf("无效的抖音作品ID: %s", id)
	}
	return id, nil
}

// canonicalDouyinWorkURL 生成规范化作品地址（path 含 "/note/" 用 note，否则 video）。
// 成品 @0xd8a320（224B，link_parser.go:174）签名 func(id, path string) string：
//   - 对第二个参数 path 调 strings.ToLower（call 0x53b860），再
//     strings.Contains(lower, "/note/")（0x109f7a2,len6；call 0x418560）；
//   - 命中 → 段名 "note"(0x109964f,len4)，否则 "video"(0x109a81b,len5)；
//   - concatstring4("https://www.douyin.com/"(0x10b94e5,len23), 段名, "/"(0x1098229,len1), id)。
//
// 因此 note/video 的区分已从 Kind 移到 path 上（成品 Kind 统一为 "work"）。
func canonicalDouyinWorkURL(id string, path string) string {
	segment := "video"
	if strings.Contains(strings.ToLower(path), "/note/") {
		segment = "note"
	}
	return BaseHost + "/" + segment + "/" + id
}

// resolveDouyinShortURL 跟随抖音短链跳转（上限 8 次，仅 HTTPS，目标须仍是 douyin.com/iesdouyin.com 子域）。
func resolveDouyinShortURL(rawURL string) (string, error) {
	trace := beginDouyinAPIRequest(douyinRequestTypeShortLink, http.MethodGet, rawURL)
	client := &http.Client{
		Timeout: getDouyinRequestTimeout(),
	}
	current := rawURL
	for step := 0; step < douyinLinkRedirectMax; step++ {
		u, err := url.Parse(current)
		if err != nil || u.Scheme != "https" {
			return "", fmt.Errorf("拒绝跟随非抖音 HTTPS 重定向: %s", current)
		}
		if !isAllowedDouyinHost(u.Hostname()) {
			return "", errors.New("抖音短链接跳转到了不受信任的地址")
		}
		req, err := http.NewRequest(http.MethodGet, current, nil)
		if err != nil {
			return "", err
		}
		setDouyinNavigationHeaders(req, BaseHost+"/", "", "")
		// 与成品一致：短链跟随不挂 CookieJar，手工带上按域名范围取出的 Cookie。
		if cookieHeader := douyinCookieHeaderFor(current); cookieHeader != "" {
			req.Header.Set("Cookie", cookieHeader)
		}
		// func1：禁止自动重定向，改由本函数手工跟随并校验每一跳。
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		resp, err := client.Do(req)
		if err != nil {
			trace.finish(err)
			return "", fmt.Errorf("解析抖音短链接失败: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			trace.finish(err)
			return "", fmt.Errorf("解析抖音短链接失败: %w", err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			next := resp.Header.Get("Location")
			if next == "" {
				return "", errors.New("抖音短链接未返回有效地址")
			}
			if ref, refErr := url.Parse(next); refErr == nil {
				current = u.ResolveReference(ref).String()
			} else {
				current = next
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			trace.finish(fmt.Errorf("HTTP %d", resp.StatusCode))
			return "", fmt.Errorf("解析抖音短链接失败: HTTP %d", resp.StatusCode)
		}
		// 页面内 JS 跳转兜底。
		if next := firstDouyinJSRedirect(string(body)); next != "" {
			if ref, refErr := url.Parse(next); refErr == nil {
				current = u.ResolveReference(ref).String()
				continue
			}
		}
		return current, nil
	}
	return "", errors.New("抖音短链接跳转次数过多")
}

// FetchLinkDetail 按目标类型拉取链接详情。
// 成品 @0xd8ad20（256B，link_parser.go:229）：入参是 LinkTarget 按值（入口保存
// rax/rbx/rcx/rdi/rsi/r8 共 6 个整型寄存器 = 3 个 string 字段），不是 *LinkTarget。
// 分派已对齐：work/live/profile（见 LinkKind 注释），未知 Kind 返回包级 error。
func FetchLinkDetail(target LinkTarget) (*LinkDetail, error) {
	if target.Kind == "" {
		return nil, errors.New("内部错误：抖音链接目标类型无效")
	}
	switch target.Kind {
	case LinkKindWork:
		return fetchDouyinWorkDetail(target)
	case LinkKindLive:
		return fetchDouyinLiveDetail(target)
	case LinkKindProfile:
		return fetchDouyinProfileDetail(target)
	default:
		return nil, errors.New("内部错误：抖音链接目标类型无效")
	}
}

// douyinWorkDetailReferer 是作品详情请求的 Referer 来源类型。
type douyinWorkDetailReferer struct {
	target LinkTarget
}

// String 实现 Referer 取值。
func (r *douyinWorkDetailReferer) String() string {
	if r == nil || r.target.URL == "" {
		return BaseHost + "/"
	}
	return r.target.URL
}

// fetchDouyinWorkDetail 拉取作品详情（带错峰重试）。
// 成品 @0xd8ae20（288B，link_parser.go:242）：入参 LinkTarget 按值（保存 6 个寄存器），
// 转手以同样的 6 个寄存器调 fetchDouyinWorkDetailWithRetry。
func fetchDouyinWorkDetail(target LinkTarget) (*LinkDetail, error) {
	detail, statusCode, err := fetchDouyinWorkDetailWithRetry(target)
	if err != nil {
		return nil, err
	}
	_ = statusCode
	return detail, nil
}

// fetchDouyinWorkDetailWithRetry 对临时风控（403/429）以新签名自动错峰重试（最多 3 次）。
// 成品 @0xd8af40（1216B，link_parser.go:260）：target 按值传入（入口先保存 r9/r10
// 再保存 rax/rbx/rcx/rdi/rsi/r8；r9 是 fetchDouyinWorkDetailOnce 的 func value、
// r10 是 time.Sleep 的 func value，成品把二者也做成了入参），随后在 +0x3c6 直接
// 调 parseDouyinWorkDetail(body, target) 按值传入 target。
// 重建保留 fetchDouyinWorkDetailOnce 承载请求+解析（成品 Once @0xd8b560 只被上述
// func value 间接调用，返回 []byte；此处为 [推断] 的等价拆分，行为一致）。
func fetchDouyinWorkDetailWithRetry(target LinkTarget) (*LinkDetail, int, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		detail, statusCode, err := fetchDouyinWorkDetailOnce(target)
		if err == nil {
			return detail, statusCode, nil
		}
		lastErr = err
		if !isDouyinRetryableRiskFailure(err) {
			break
		}
		logger.WithField("attempt", attempt).
			WithField("status_code", statusCode).
			Warnf("抖音作品详情触发风控，使用新签名错峰重试")
		// 错峰：随机短暂等待后以全新的动态签名重试。
		time.Sleep(time.Duration(500+secureDouyinInt(500)) * time.Millisecond)
	}
	return nil, 0, lastErr
}

// isDouyinRetryableRiskFailure 判断 error 是否为临时风控（HTTP 403/429），值得重试。
//
// 成品 @0xd8b400（352B，link_parser.go:281）签名 func(err error) bool：
//   - err==nil → false（入口 test %rax,%rax; je ret0）；
//   - msg := strings.ToLower(err.Error())（call 0x53b860）；逐个
//     internal/stringslite.Index(msg, needle)，6 个字面量：
//     "http code error 403"(0x10b39e2,len19)、"http 403"(0x10a4b16,len8)、
//     "status 403"(0x10a76dd,len10)、"http code error 429"(0x10b39f5,len19)、
//     "http 429"(0x10a4b1e,len8)、"status 429"(0x10a76e7,len10)，任一命中返回 true。
//     对照 isDouyinAuthFailure 的单个 "401"，本函数覆盖 403 与 429 两个 code；
//     汇编里 6 次 Index 按 3 个 OR 对排列，去重后与下面的循环等价。
func isDouyinRetryableRiskFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, code := range []string{"403", "429"} {
		if strings.Contains(msg, "http code error "+code) ||
			strings.Contains(msg, "http "+code) ||
			strings.Contains(msg, "status "+code) {
			return true
		}
	}
	return false
}

// fetchDouyinWorkDetailOnce 请求一次作品详情接口。
// 成品 @0xd8b560（1472B，link_parser.go:294）：入参 LinkTarget 按值（被 WithRetry 以
// 6 个寄存器间接调用）。成品该函数返回 []byte（body），解析由 WithRetry 完成；
// 重建把解析留在本函数内，返回 (*LinkDetail,int,error)——结构差异见 WithRetry 注释。
func fetchDouyinWorkDetailOnce(target LinkTarget) (*LinkDetail, int, error) {
	rawURL := buildSignedDouyinWorkDetailURL(target.ID)
	trace := beginDouyinAPIRequest(douyinRequestTypeDetailSource, http.MethodGet, rawURL)
	release, err := acquireDouyinRequestSlotWithWait(context.Background(), trace)
	if err != nil {
		return nil, 0, fmt.Errorf("获取抖音作品详情失败: %w", err)
	}
	defer release()
	referer := &douyinWorkDetailReferer{target: target}
	opts := douyinWebAPIRequestOptions(trace)
	opts = append(opts,
		requests.HeaderOption("Referer", referer.String()),
		requests.HeaderOption(douyinWebSignHeader, douyinWebSignStrategy),
	)
	var resp bytes.Buffer
	var respHeaders requests.RespHeader
	statusCode := 0
	opts = append(opts, requests.HttpCodeOption(&statusCode))
	reqErr := requests.GetWithHeader(rawURL, nil, &resp, &respHeaders, opts...)
	trace.finish(reqErr)
	if reqErr != nil {
		// 成品 fetchDouyinWorkDetail callsite：isDouyinAuthFailure @+0x6b（输入为请求 error），
		// 命中后 @+0x96 用 `err.Error()` 文本作为 reason 调
		// markDouyinCookieUnhealthyFor(link_parse,...)（第二参不是 statusCode）。
		if isDouyinAuthFailure(reqErr) {
			markDouyinCookieUnhealthyFor(douyinFailureScopeLinkParse, reqErr.Error())
		}
		return nil, statusCode, fmt.Errorf("获取抖音作品详情失败: %w", reqErr)
	}
	if statusCode != 0 && (statusCode < 200 || statusCode >= 300) {
		return nil, statusCode, fmt.Errorf("获取抖音作品详情失败: HTTP %d", statusCode)
	}
	detail, err := parseDouyinWorkDetail(resp.Bytes(), target)
	if err != nil {
		return nil, statusCode, err
	}
	markDouyinCookieHealthyFor(douyinFailureScopeLinkParse)
	return detail, statusCode, nil
}

// parseDouyinWorkDetail 解析作品详情 JSON 为链接详情。
// 成品 @0xd8bc20（link_parser.go:326，3552B）逐条证据：
//   - 顶层响应匿名结构（recovered/types/types-x64.tsv:7457）：
//     struct{StatusCode int `json:"status_code"`; StatusMsg string `json:"status_msg"`;
//     AwemeDetail map[string]interface{} `json:"aweme_detail"`}；Decode @+0x131；
//     StatusCode!=0 → fmt.Errorf("抖音作品详情返回 status_code=%d %s", code, msg)（+0x343）；
//     AwemeDetail 空（hmap.count 判空 @+0x147）→ errors.New("抖音作品详情为空，作品可能已删除或不可见")。
//   - mapaccess1_faststr（成品 offsets）："author"(len6)@+0x185、"video"(len5)@+0x4c6、
//     "cover"(len5)@+0x4f9、"dynamic_cover"(len13)@+0x52d、"create_time"(len11)@+0x6a0、
//     "nickname"(len8)@+0x6d4、"unique_id"(len9)@+0x713、"desc"(len4)@+0x752、
//     "play_addr"@+0xad0/+0xbe2/+0xcc0、"play_addr_h264"(len14)、"download_addr"(len13)、
//     "bit_rate"(len8)@+0x7a0。
//   - douyinString（link_parser.go:531）只有 3 处：+0x6e3 author["nickname"]、
//     +0x722 author["unique_id"]、+0x761 aweme["desc"]；douyinInt64 @+0x6af 只用于
//     aweme["create_time"]。
//   - firstDouyinImageURL（post_extra.go:82）只有 2 处：+0x508 video["cover"]、
//     +0x540 video["dynamic_cover"]；再空则以 Images[0] 兜底（+0x574）。
//   - firstDouyinMediaURL（link_parser.go:511）只有 2 处：+0xb52（video 的
//     play_addr→play_addr_h264→download_addr，三路顺序见栈常量 0x198/0x188/0x178）、
//     +0xccf（bit_rate[].play_addr）。
//   - Images 由图集条目的 ImageURL（DouyinGalleryItem +0x18）过滤非空得到
//     （+0x424 growslice 目标元素 0x10=string），不再单独解析 images 数组。
//   - 成品在 len(Images)>0 时跳过视频地址搜索（+0x6ff），VideoURL（0xb0/0xb8）
//     仅在 len(Images)==0 时填。
//   - LinkDetail 偏移：Kind="work"(+0x00/0x08)、ID=target.ID(+0x10/0x18)、
//     URL="https://www.douyin.com/note/"+ID 或 "/video/"+ID(+0x20/0x28，先 target.URL
//     后覆盖)、Name(+0x30/0x38)、DisplayID(+0x40/0x48)、Description(+0x50/0x58)、
//     Title="抖音图文"/"抖音视频"(+0x60/0x68)、Cover(+0x70/0x78)、
//     Images(+0x80/0x88/0x90)、GalleryItems(+0x98/0xa0/0xa8)、VideoURL(+0xb0/0xb8)、
//     CreatedAt=time.Unix(create_time,0) 仅 create_time>0(+0xc0/0xc8/0xd0)。
//
// 签名（成品 @0xd8bc20 入口保存 rax/rbx/rcx/rdi/rsi/r8/r9/r10/r11 共 9 个整型寄存器）：
//
//	body []byte   = rax/rbx/rcx
//	target LinkTarget = rdi/rsi(Kind) + r8/r9(ID) + r10/r11(URL)  —— 按值传入。
//
// 对照成品 fetchDouyinWorkDetailWithRetry +0x3c6 的直接调用点：rdi..r11 从保存的
// target 字段装载、rax/rbx/rcx 为 fetchDouyinWorkDetailOnce 返回的 body。
func parseDouyinWorkDetail(body []byte, target LinkTarget) (*LinkDetail, error) {
	var resp struct {
		StatusCode  int                    `json:"status_code"`
		StatusMsg   string                 `json:"status_msg"`
		AwemeDetail map[string]interface{} `json:"aweme_detail"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("解析抖音作品详情失败: %w", err)
	}
	if resp.StatusCode != 0 {
		return nil, fmt.Errorf("抖音作品详情返回 status_code=%d %s", resp.StatusCode, resp.StatusMsg)
	}
	aweme := resp.AwemeDetail
	if len(aweme) == 0 {
		return nil, errors.New("抖音作品详情为空，作品可能已删除或不可见")
	}
	author, _ := aweme["author"].(map[string]interface{})
	video, _ := aweme["video"].(map[string]interface{})

	galleryItems := collectDouyinGalleryItems(aweme)
	images := make([]string, 0, len(galleryItems))
	for _, item := range galleryItems {
		if item.ImageURL != "" {
			images = append(images, item.ImageURL)
		}
	}

	cover := firstDouyinImageURL(video["cover"])
	if cover == "" {
		cover = firstDouyinImageURL(video["dynamic_cover"])
	}
	if cover == "" && len(images) > 0 {
		cover = images[0]
	}

	videoURL := ""
	if len(images) == 0 {
		for _, key := range []string{"play_addr", "play_addr_h264", "download_addr"} {
			if u := firstDouyinMediaURL(video[key]); u != "" {
				videoURL = u
				break
			}
		}
		if videoURL == "" {
			for _, item := range douyinArrayAt(video, "bit_rate") {
				bitRate, _ := item.(map[string]interface{})
				if bitRate == nil {
					continue
				}
				if u := firstDouyinMediaURL(bitRate["play_addr"]); u != "" {
					videoURL = u
					break
				}
			}
		}
	}

	detail := &LinkDetail{
		Kind:         LinkKindWork,
		ID:           target.ID,
		URL:          target.URL,
		Name:         douyinString(author["nickname"]),
		DisplayID:    douyinString(author["unique_id"]),
		Description:  douyinString(aweme["desc"]),
		Cover:        cover,
		Images:       images,
		GalleryItems: galleryItems,
		VideoURL:     videoURL,
	}
	if len(images) > 0 {
		detail.Title = "抖音图文"
		detail.URL = "https://www.douyin.com/note/" + target.ID
	} else {
		detail.Title = "抖音视频"
		detail.URL = "https://www.douyin.com/video/" + target.ID
	}
	if createTime := douyinInt64(aweme["create_time"]); createTime > 0 {
		detail.CreatedAt = time.Unix(createTime, 0)
	}
	return detail, nil
}

// fetchDouyinLiveDetail 拉取直播间详情：
// 直播间链接需要先解析主播用户ID（取首屏 roomStore 数据），再走用户资料接口补全。
// 成品 @0xd8ca00：target 按值（6 寄存器），LinkDetail.Kind="live"(0x1099557,len4,@0xd8cc65)。
func fetchDouyinLiveDetail(target LinkTarget) (*LinkDetail, error) {
	snapshot, err := GetRoomDataWithRoomID(target.ID)
	if err != nil {
		return nil, fmt.Errorf("获取抖音直播间数据失败: %w", err)
	}
	if snapshot == nil || snapshot.SecUID == "" {
		return nil, errors.New("直播间链接需要先解析主播用户ID")
	}
	detail := &LinkDetail{
		Kind:        LinkKindLive,
		ID:          target.ID,
		URL:         target.URL,
		Name:        snapshot.Nickname,
		Title:       snapshot.Title,
		Cover:       snapshot.Cover,
		Living:      snapshot.Living,
		StatusKnown: true,
	}
	return detail, nil
}

// fetchDouyinProfileDetail 拉取用户主页详情（带签名用户资料接口）。
// 成品 @0xd8ce60：target 按值（6 寄存器）；LinkDetail.Kind="profile"
// (0x10a31d7,len7,@0xd8cf4e-0xd8cf5d)。
func fetchDouyinProfileDetail(target LinkTarget) (*LinkDetail, error) {
	body, err := requestDouyinSignedUserProfileBody(target.ID)
	if err != nil {
		return nil, err
	}
	var resp douyinWebProfileResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析抖音用户资料失败: %w", err)
	}
	if resp.User == nil {
		return nil, errors.New("抖音用户资料为空")
	}
	detail := &LinkDetail{
		Kind:        LinkKindProfile,
		ID:          target.ID,
		URL:         target.URL,
		Name:        resp.User.Nickname,
		DisplayID:   resp.User.UniqueID,
		Description: resp.User.Signature,
	}
	if resp.User.AvatarLarger != nil && len(resp.User.AvatarLarger.URLList) > 0 {
		detail.Cover = resp.User.AvatarLarger.URLList[0]
	} else if resp.User.AvatarMedium != nil && len(resp.User.AvatarMedium.URLList) > 0 {
		detail.Cover = resp.User.AvatarMedium.URLList[0]
	}
	return detail, nil
}

// collectDouyinGalleryItems 提取图集/实况图条目（成品 0xd8d0e0，1792B，link_parser.go:456）。
// 反汇编逐条证据（offsets 相对函数入口）：
//   - 外层固定顺序遍历 3 组键路径（[][]string 常量，元素 24B）：
//     {"images"}、{"image_album_info","images"}、{"image_post_info","images"}
//     （rodata 0x109f7a8="images",len6、0x10af5c5="image_album_info",len16、
//     0x10ae162="image_post_info",len15）；
//   - 每组按 mapaccess1_faststr 逐键下钻（+0x211），末值必须是 []interface{}
//     （type 0xed7100），否则整组跳过（+0x36b 比较类型后清零）；
//   - 元素为 map[string]interface{}（type 0xf65ce0）时取 elem["video"]
//     （键 0x109a81b,len5）子 map，再按
//     play_addr_h264(0x10acc2b,14) → play_addr(0x10a60ad,9) → download_addr(0x10ab71c,13)
//     依次 firstDouyinMediaURL（+0x461 call 0xd8d7e0）取第一个非空；
//   - 去重键 = media 非空取 media，否则取 image（map[string]struct{}：+0x4ef
//     mapaccess2_faststr 判存在、+0x531 mapassign_faststr 写入）；
//   - ClipType = douyinInt64(elem["clip_type"])（键 0x10a60bf,len9；+0x562 call 0xd8da60）；
//   - Kind：media 为空 → "image"(0x109a889,5)；media 非空且 clip_type==3||==5 →
//     "livephoto"(0x10a60c8,9)；其余 → "video"(0x109a81b,5)（+0x651/+0x657 比较 3、5）；
//   - 条目写入顺序 Kind(+0x00)/ClipType(+0x10)/ImageURL(+0x18)/VideoURL(+0x28)：
//     ImageURL = firstDouyinImageURL(elem)（+0x346 call 0xd952c0），VideoURL = media。
//
// 成品无独立 douyinGalleryItemFromImage 符号，其旧逻辑已被本函数取代。
func collectDouyinGalleryItems(raw map[string]interface{}) []DouyinGalleryItem {
	var items []DouyinGalleryItem
	seen := make(map[string]struct{})
	for _, keys := range [][]string{
		{"images"},
		{"image_album_info", "images"},
		{"image_post_info", "images"},
	} {
		var current interface{} = raw
		ok := true
		for _, key := range keys {
			node, isMap := current.(map[string]interface{})
			if !isMap {
				ok = false
				break
			}
			current = node[key]
		}
		if !ok {
			continue
		}
		arr, isArr := current.([]interface{})
		if !isArr {
			continue
		}
		for _, elem := range arr {
			imageURL := firstDouyinImageURL(elem)
			mediaURL := ""
			if obj, isMap := elem.(map[string]interface{}); isMap {
				if video, isVideo := obj["video"].(map[string]interface{}); isVideo {
					for _, key := range []string{"play_addr_h264", "play_addr", "download_addr"} {
						if u := firstDouyinMediaURL(video[key]); u != "" {
							mediaURL = u
							break
						}
					}
				}
			}
			preferred := imageURL
			if mediaURL != "" {
				preferred = mediaURL
			}
			if preferred == "" {
				continue
			}
			if _, dup := seen[preferred]; dup {
				continue
			}
			seen[preferred] = struct{}{}
			clipType := 0
			if obj, isMap := elem.(map[string]interface{}); isMap {
				clipType = int(douyinInt64(obj["clip_type"]))
			}
			kind := "image"
			switch {
			case mediaURL == "":
				kind = "image"
			case clipType == 3 || clipType == 5:
				kind = "livephoto"
			default:
				kind = "video"
			}
			items = append(items, DouyinGalleryItem{
				Kind:     kind,
				ClipType: clipType,
				ImageURL: imageURL,
				VideoURL: mediaURL,
			})
		}
	}
	return items
}

// firstDouyinMediaURL 递归取第一个媒体地址（成品 0xd8d7e0，link_parser.go:511）。
// 反汇编证据（offsets 相对函数入口）：
//   - 入参是 interface{}（rax=type、rbx=data）：map[string]interface{}（type 0xf65ce0）、
//     []interface{}（type 0xed7100）、string（type 0xee7100）三分支；
//   - map 依序取 4 个键递归（mapaccess1_faststr +0x172/+0x181，每次递归调用自身）：
//     url_list(0x10a4b36,8) → url(0x10987f4,3) → play_addr(0x10a60ad,9) →
//     download_addr(0x10ab71c,13)，返回第一个非空；
//   - slice 逐项递归（+0xec）；
//   - string 调 normalizeDouyinImageURL(0xd95500) 规范化后返回。
func firstDouyinMediaURL(value interface{}) string {
	switch node := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"url_list", "url", "play_addr", "download_addr"} {
			if u := firstDouyinMediaURL(node[key]); u != "" {
				return u
			}
		}
	case []interface{}:
		for _, item := range node {
			if u := firstDouyinMediaURL(item); u != "" {
				return u
			}
		}
	case string:
		return normalizeDouyinImageURL(node)
	}
	return ""
}

// valueAtDouyinPath 按路径逐层取 JSON 值；数字下标用于数组。
func valueAtDouyinPath(raw map[string]interface{}, path ...string) interface{} {
	var current interface{} = raw
	for _, key := range path {
		switch node := current.(type) {
		case map[string]interface{}:
			current = node[key]
		case []interface{}:
			idx, err := strconv.Atoi(key)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil
			}
			current = node[idx]
		default:
			return nil
		}
	}
	return current
}

// douyinArrayAt 取指定键下的数组。
func douyinArrayAt(raw map[string]interface{}, key string) []interface{} {
	if raw == nil {
		return nil
	}
	arr, _ := raw[key].([]interface{})
	return arr
}

// douyinString 取字符串值。
// 成品 @0xd8d9a0（192B，link_parser.go:531）签名 func(value interface{}) string
// （rax=type、rbx=data 两个寄存器），三分支，均已用 disasm + 类型名坐实：
//   - float64（_type 0xee74c0，hash 0x6ca6d294）：cvttsd2si 后
//     strconv.FormatInt(int64(v), 10)（call 0x40c300，base=0xa）；
//   - string（_type 0xee7100，hash 0xf88732b8，name "*string"）：直接返回；
//   - json.Number（_type 0xf63660，hash 0xbd2d7d09，name "*json.Number"）：
//     直接返回底层字符串（json.Number.String() 被内联）；
//   - 其余（含 nil）→ ""（0xd8da37: xor eax,ebx）。
//
// 成品仅 3 个调用点，全在 parseDouyinWorkDetail（+0x6e3/+0x722/+0x761）。
func douyinString(value interface{}) string {
	switch v := value.(type) {
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}

// douyinInt64 取整数值（兼容字符串数字）。
// 成品 @0xd8da60（224B，link_parser.go:544）签名 func(value interface{}) int64：
//   - float64（0xee74c0）：cvttsd2si → int64；
//   - string（0xee7100 / 0xf63660）：strconv.ParseInt(string, 10, 64)
//     （call 0x49d800，ecx=0xa、edi=0x40，错误丢弃）；
//     json.Number.Int64() 亦内联为同一 ParseInt；
//   - 其余（含 nil）→ 0。
//
// 成品仅 2 个调用点：parseDouyinWorkDetail +0x6af（create_time）、
// collectDouyinGalleryItems +0x562（clip_type）。
func douyinInt64(value interface{}) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}
