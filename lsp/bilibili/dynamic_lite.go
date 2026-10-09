package bilibili

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// Lite 动态刷新改为网页新版（polymer/web-dynamic）增量探测：
// 启动时先拉一次 feed/all 建立基线并记录 id_str，不推送历史；
// 之后按 probeInterval 调 feed/all/update，update_num 为 0 直接结束，
// 大于 0 或到达兜底周期时才拉完整列表，id_str 去重后转回旧版 Card，
// 订阅筛选、去重、过滤、模板、@、发送等后续链路保持不变。
const (
	PathWebDynamicFeedAll       = "/x/polymer/web-dynamic/v1/feed/all"
	PathWebDynamicFeedAllUpdate = "/x/polymer/web-dynamic/v1/feed/all/update"

	// 动态请求的 origin/referer 取值（二进制原文）。
	WebDynamicOrigin = "https://t.bilibili.com"
)

const (
	defaultDynamicLiteProbeInterval = 10 * time.Second
	defaultDynamicLiteMaxBackoff    = 15 * time.Minute
	minDynamicLiteMaxBackoff        = time.Minute
	defaultDynamicLiteReconcile     = time.Minute
	minDynamicLiteReconcile         = 30 * time.Second
	defaultDynamicLiteSeenLimit     = 5000
	minDynamicLiteSeenLimit         = 100

	// 失败退避上限：按 1、2、4、8、15 分钟递增，最大退避由配置收口。
	dynamicLiteBackoffCap = 15 * time.Minute

	// 转发动态的最大嵌套层级，超过视为异常。
	webDynamicForwardMaxDepth = 4
)

// bilibiliWebUserAgent 是动态探测、扫码与直播 WS 共用的 Chrome 143 UA（二进制原文）。
const bilibiliWebUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"

// bilibiliWebSecChUa 是配套的 Sec-Ch-Ua 头（二进制原文）。
const bilibiliWebSecChUa = "\"Google Chrome\";v=\"143\", \"Chromium\";v=\"143\", \"Not_A Brand\";v=\"24\""

// dynamicLiteConfig 是网页动态增量探测的全部节奏参数。
// Interval 未单独设置 dynamic.probeInterval 时沿用 bilibili.interval。
type dynamicLiteConfig struct {
	Interval          time.Duration
	Jitter            time.Duration
	MaxBackoff        time.Duration
	ReconcileInterval time.Duration
	SeenLimit         int
}

// loadDynamicLiteConfig 读取 bilibili.dynamic 配置段并按文档约束收口：
// jitter 最大有效值为 probeInterval 的一半；maxBackoff 默认 15m 强制最小 1m；
// reconcileInterval 默认 1m 强制最小 30s；seenLimit 默认 5000 强制最小 100。
func loadDynamicLiteConfig() dynamicLiteConfig {
	var cfg dynamicLiteConfig
	probeConfigured := false
	if config.GlobalConfig != nil {
		cfg.Interval = config.GlobalConfig.GetDuration("bilibili.interval")
		probeConfigured = config.GlobalConfig.IsSet("bilibili.dynamic.probeInterval")
		if config.GlobalConfig.IsSet("bilibili.dynamic.jitter") {
			cfg.Jitter = config.GlobalConfig.GetDuration("bilibili.dynamic.jitter")
		}
		cfg.MaxBackoff = config.GlobalConfig.GetDuration("bilibili.dynamic.maxBackoff")
		cfg.ReconcileInterval = config.GlobalConfig.GetDuration("bilibili.dynamic.reconcileInterval")
		cfg.SeenLimit = config.GlobalConfig.GetInt("bilibili.dynamic.seenLimit")
	}
	// 未单独设置 dynamic.probeInterval 时，网页动态增量探测沿用 bilibili.interval
	if probeConfigured {
		cfg.Interval = dynamicLiteProbeInterval()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultDynamicLiteProbeInterval
	}
	half := cfg.Interval / 2
	if cfg.Jitter > half {
		cfg.Jitter = half
	}
	if cfg.Jitter < 0 {
		cfg.Jitter = 0
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultDynamicLiteMaxBackoff
	}
	if cfg.MaxBackoff < minDynamicLiteMaxBackoff {
		cfg.MaxBackoff = minDynamicLiteMaxBackoff
	}
	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = defaultDynamicLiteReconcile
	}
	if cfg.ReconcileInterval < minDynamicLiteReconcile {
		cfg.ReconcileInterval = minDynamicLiteReconcile
	}
	if cfg.SeenLimit <= 0 {
		cfg.SeenLimit = defaultDynamicLiteSeenLimit
	}
	if cfg.SeenLimit < minDynamicLiteSeenLimit {
		cfg.SeenLimit = minDynamicLiteSeenLimit
	}
	return cfg
}

// dynamicLiteProbeInterval 解析 bilibili.dynamic.probeInterval：
// 纯数字按“秒”解释（3 就是 3 秒），也兼容 3s、500ms 等写法；
// 0、负数、空值或无效值均回退 10s。
func dynamicLiteProbeInterval() time.Duration {
	if config.GlobalConfig == nil {
		return defaultDynamicLiteProbeInterval
	}
	raw := strings.TrimSpace(config.GlobalConfig.GetString("bilibili.dynamic.probeInterval"))
	if raw == "" {
		return defaultDynamicLiteProbeInterval
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if seconds <= 0 {
			return defaultDynamicLiteProbeInterval
		}
		return time.Duration(seconds) * time.Second
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return defaultDynamicLiteProbeInterval
	}
	return parsed
}

// dynamicLiteAPI 抽象网页动态接口，便于测试替换。
type dynamicLiteAPI interface {
	FetchFeed(param string) (*webDynamicFeedResponse, error)
	Probe(baseline string) (*webDynamicUpdateResponse, error)
}

// bilibiliDynamicLiteAPI 是默认实现，直接调用包内请求函数。
type bilibiliDynamicLiteAPI struct{}

// FetchFeed 实现 dynamicLiteAPI。
func (bilibiliDynamicLiteAPI) FetchFeed(param string) (*webDynamicFeedResponse, error) {
	return WebDynamicFeedAll(param)
}

// Probe 实现 dynamicLiteAPI。
func (bilibiliDynamicLiteAPI) Probe(baseline string) (*webDynamicUpdateResponse, error) {
	return WebDynamicFeedUpdate(baseline)
}

// dynamicLitePoller 是网页动态增量探测轮询器：
// 维护基线、内存 seen 队列与下次探测时间，失败时按退避序列重试。
type dynamicLitePoller struct {
	config dynamicLiteConfig
	api    dynamicLiteAPI
	now    func() time.Time

	baseline    string
	initialized bool

	seen      map[string]struct{}
	seenOrder []string

	nextRun    time.Time
	lastFeedAt time.Time
	failures   int
}

// newDynamicLitePoller 创建轮询器；api 传 nil 时使用默认实现。
func newDynamicLitePoller(config dynamicLiteConfig, api dynamicLiteAPI) *dynamicLitePoller {
	if api == nil {
		api = bilibiliDynamicLiteAPI{}
	}
	poller := &dynamicLitePoller{
		config: config,
		api:    api,
		now:    time.Now,
		seen:   make(map[string]struct{}),
	}
	poller.nextRun = poller.now()
	return poller
}

// nextDelay 计算距下次探测的等待时长。
func (p *dynamicLitePoller) nextDelay() time.Duration {
	delay := p.nextRun.Sub(p.now())
	if delay < 0 {
		return 0
	}
	return delay
}

// poll 等待到期后执行一轮探测。
// 返回值为本轮新发现的动态条目；首轮只建立基线（不推送历史）返回 nil，
// 无增量或失败时同样返回 nil，由 scheduleSuccess/scheduleFailure 安排下次探测。
func (p *dynamicLitePoller) poll(ctx context.Context) []*webDynamicFeedItem {
	if err := waitContext(ctx, p.nextDelay()); err != nil {
		return nil
	}
	if !p.initialized {
		resp, err := p.api.FetchFeed("")
		if err != nil || validateWebDynamicFeed(resp) != nil {
			p.scheduleFailure(err)
			return nil
		}
		if resp.Data.UpdateBaseline == "" {
			logger.WithField("web_incremental", false).
				Warn("B站动态完整列表未返回 update_baseline")
			p.scheduleFailure(errors.New("B站动态完整列表未返回 update_baseline"))
			return nil
		}
		p.baseline = resp.Data.UpdateBaseline
		for index := range resp.Data.Items {
			p.remember(resp.Data.Items[index].IDStr)
		}
		p.initialized = true
		p.lastFeedAt = p.now()
		p.scheduleSuccess()
		logger.WithFields(map[string]interface{}{
			"web_dynamic_feed_update": false,
			"initial_feed":            true,
			"initial_items":           len(resp.Data.Items),
			"update_baseline":         p.baseline,
			"probe_interval":          p.config.Interval.String(),
			"next_probe":              p.nextRun.Format(time.RFC3339),
		}).Info("B站动态网页刷新首次全量基线建立完成")
		return nil
	}

	updateResp, err := p.api.Probe(p.baseline)
	if err != nil || validateWebDynamicUpdate(updateResp) != nil {
		p.scheduleFailure(err)
		return nil
	}
	// 成品 webDynamicUpdateResponseData(recovered/types/types-closure-x64.tsv:6971)
	// 只有 update_num 一个字段，增量响应不再回传 update_baseline，故此处不校验基线。
	reconcileDue := !p.lastFeedAt.IsZero() && p.now().Sub(p.lastFeedAt) >= p.config.ReconcileInterval
	if updateResp.Data.UpdateNum == 0 && !reconcileDue {
		logger.WithFields(map[string]interface{}{
			"web_dynamic_feed_update": true,
			"increment_probe":         true,
			"update_num":              int64(updateResp.Data.UpdateNum),
			"update_baseline":         p.baseline,
			"probe_interval":          p.config.Interval.String(),
			"reconcile_every":         p.config.ReconcileInterval.String(),
			"next_probe":              p.nextRun.Format(time.RFC3339),
		}).Info("B站动态网页探测完成：无增量，不拉取完整列表")
		p.scheduleSuccess()
		return nil
	}
	feedTrigger := "increment_probe"
	if updateResp.Data.UpdateNum > 0 {
		logger.WithFields(map[string]interface{}{
			"web_dynamic_feed_update": true,
			"feed_trigger":            feedTrigger,
			"update_num":              int64(updateResp.Data.UpdateNum),
			"reported_update_num":     int64(updateResp.Data.UpdateNum),
		}).Info("B站动态网页探测发现增量，开始拉取完整列表")
	} else {
		feedTrigger = "periodic_reconcile"
		logger.WithFields(map[string]interface{}{
			"web_dynamic_feed_update": true,
			"feed_trigger":            feedTrigger,
			"reconcile_every":         p.config.ReconcileInterval.String(),
		}).Info("B站动态网页周期兜底校准，开始拉取完整列表")
	}

	feedResp, err := p.api.FetchFeed("")
	if err != nil || validateWebDynamicFeed(feedResp) != nil {
		p.scheduleFailure(err)
		return nil
	}
	if feedResp.Data.UpdateBaseline == "" {
		logger.Warn("B站动态完整列表未返回 update_baseline")
		p.scheduleFailure(errors.New("B站动态完整列表未返回 update_baseline"))
		return nil
	}
	var unseen []*webDynamicFeedItem
	for index := range feedResp.Data.Items {
		item := &feedResp.Data.Items[index]
		if _, exist := p.seen[item.IDStr]; exist {
			continue
		}
		unseen = append(unseen, item)
	}
	p.baseline = feedResp.Data.UpdateBaseline
	for _, item := range unseen {
		p.remember(item.IDStr)
	}
	p.lastFeedAt = p.now()
	p.scheduleSuccess()
	logger.WithFields(map[string]interface{}{
		"web_dynamic_feed_update": true,
		"process_items":           len(feedResp.Data.Items),
		"unseen_items":            len(unseen),
		"update_baseline":         p.baseline,
		"baseline_kept":           false,
		"history_pushed":          false,
		"full_feed_call":          true,
		"last_full_feed":          p.lastFeedAt.Format(time.RFC3339),
		"feed_trigger":            feedTrigger,
	}).Info("B站动态增量列表处理完成，已提交新基线")
	return unseen
}

// remember 把一条动态 id 记入内存去重队列，超出 seenLimit 时淘汰最早记录。
func (p *dynamicLitePoller) remember(idStr string) {
	if idStr == "" {
		return
	}
	if _, exist := p.seen[idStr]; exist {
		return
	}
	p.seen[idStr] = struct{}{}
	p.seenOrder = append(p.seenOrder, idStr)
	for len(p.seenOrder) > p.config.SeenLimit {
		evict := p.seenOrder[0]
		p.seenOrder = p.seenOrder[1:]
		delete(p.seen, evict)
	}
}

// scheduleSuccess 一轮成功：按探测间隔与抖动安排下次探测并清零失败计数。
func (p *dynamicLitePoller) scheduleSuccess() {
	delay := p.config.Interval
	if p.config.Jitter > 0 {
		delay += time.Duration(p.now().UnixNano() % int64(p.config.Jitter))
	}
	p.nextRun = p.now().Add(delay)
	p.failures = 0
}

// scheduleFailure 一轮失败：保留原基线，按 1、2、4、8、15 分钟退避安排重试，
// 并以 maxBackoff 收口。
func (p *dynamicLitePoller) scheduleFailure(err error) {
	previous := p.failures
	p.failures++
	backoff := (time.Duration(1) << uint(p.failures-1)) * time.Minute
	if backoff > dynamicLiteBackoffCap {
		backoff = dynamicLiteBackoffCap
	}
	if backoff > p.config.MaxBackoff {
		backoff = p.config.MaxBackoff
	}
	p.nextRun = p.now().Add(backoff)
	fields := map[string]interface{}{
		"web_dynamic_feed_update":       false,
		"baseline_kept":                 true,
		"update_baseline":               p.baseline,
		"previous_consecutive_failures": previous,
		"consecutive_fails":             p.failures,
		"next_probe":                    p.nextRun.Format(time.RFC3339),
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.WithFields(fields).Warn("B站动态网页刷新失败，保留原基线并按退避时间重试")
}

// validateWebDynamicUpdate 校验增量探测响应的基本有效性。
func validateWebDynamicUpdate(resp *webDynamicUpdateResponse) error {
	if resp == nil {
		return errors.New("B站动态增量探测响应为空")
	}
	return nil
}

// validateWebDynamicFeed 校验完整列表响应的基本有效性。
func validateWebDynamicFeed(resp *webDynamicFeedResponse) error {
	if resp == nil {
		return errors.New("B站动态完整列表响应为空")
	}
	return nil
}

// flexibleInt64 兼容数字与字符串数字的整型字段。
type flexibleInt64 int64

// UnmarshalJSON 实现 json.Unmarshaler：接受数字、字符串数字与 null。
func (v *flexibleInt64) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == "" {
		*v = 0
		return nil
	}
	if strings.HasPrefix(text, "\"") {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
		if err != nil {
			return fmt.Errorf("无效的整型字段值 %q: %w", str, err)
		}
		*v = flexibleInt64(parsed)
		return nil
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("无效的整型字段值 %s: %w", text, err)
	}
	*v = flexibleInt64(parsed)
	return nil
}

// flexibleFloat64 兼容数字与字符串数字的浮点型字段。
type flexibleFloat64 float64

// UnmarshalJSON 实现 json.Unmarshaler：接受数字、字符串数字与 null。
func (v *flexibleFloat64) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == "" {
		*v = 0
		return nil
	}
	if strings.HasPrefix(text, "\"") {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(str), 64)
		if err != nil {
			return fmt.Errorf("无效的浮点字段值 %q: %w", str, err)
		}
		*v = flexibleFloat64(parsed)
		return nil
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("无效的浮点字段值 %s: %w", text, err)
	}
	*v = flexibleFloat64(parsed)
	return nil
}

// webDynamicPicture 是网页动态中的单张图片。
type webDynamicPicture struct {
	Src    string          `json:"src"`
	URL    string          `json:"url"`
	Width  flexibleInt64   `json:"width"`
	Height flexibleInt64   `json:"height"`
	Size   flexibleFloat64 `json:"size"`
}

// sourceURL 返回图片可用地址：src 与 url 均为独立字段，优先 src；
// 两条分支都会补全协议并去掉压缩尾缀。成品 dynamic_lite.go:499 是值接收者
// （符号名 bilibili.webDynamicPicture.sourceURL，192B），内部调用 strings.TrimSpace
// 与 normalizeBilibiliImageURL。
func (p webDynamicPicture) sourceURL() string {
	if src := strings.TrimSpace(p.Src); src != "" {
		return normalizeBilibiliImageURL(src)
	}
	return normalizeBilibiliImageURL(p.URL)
}

// normalizeBilibiliImageURL 归一化图片地址：补 https 并去掉压缩参数尾缀。
func normalizeBilibiliImageURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	rawURL = strings.TrimSuffix(rawURL, "@webp")
	if strings.HasPrefix(rawURL, "//") {
		return "https:" + rawURL
	}
	return rawURL
}

// webDynamicAPIError 描述网页动态接口的业务错误。
type webDynamicAPIError struct {
	Code    int32
	Message string
}

// Error 实现 error 接口。
func (e *webDynamicAPIError) Error() string {
	if e == nil {
		return "B站动态API返回空响应"
	}
	return fmt.Sprintf("B站动态API返回 %d: %s", e.Code, e.Message)
}

// webDynamicDescription 是动态正文描述。
type webDynamicDescription struct {
	Text string `json:"text"`
}

// webDynamicAuthor 是动态作者模块。
type webDynamicAuthor struct {
	Mid   flexibleInt64 `json:"mid"`
	Name  string        `json:"name"`
	Face  string        `json:"face"`
	PubTs flexibleInt64 `json:"pub_ts"`
}

// webDynamicArchive 是视频稿件主要卡片。
type webDynamicArchive struct {
	Bvid  string `json:"bvid"`
	Title string `json:"title"`
	Desc  string `json:"desc"`
	Cover string `json:"cover"`
}

// webDynamicDraw 是图片主要卡片。
type webDynamicDraw struct {
	Items []webDynamicPicture `json:"items"`
}

// webDynamicArticle 是专栏主要卡片。
type webDynamicArticle struct {
	Title  string   `json:"title"`
	Desc   string   `json:"desc"`
	Covers []string `json:"covers"`
}

// webDynamicOpus 是图文（opus）主要卡片。
type webDynamicOpus struct {
	Title   string                `json:"title"`
	Summary webDynamicDescription `json:"summary"`
	Pics    []webDynamicPicture   `json:"pics"`
}

// webDynamicMusic 是音乐主要卡片。
type webDynamicMusic struct {
	Title string `json:"title"`
	Cover string `json:"cover"`
	Label string `json:"label"`
}

// webDynamicLive 是直播主要卡片。
type webDynamicLive struct {
	Title string        `json:"title"`
	Cover string        `json:"cover"`
	ID    flexibleInt64 `json:"id"`
}

// webDynamicLiveRCMD 是直播分享主要卡片，content 为内嵌 JSON 字符串。
type webDynamicLiveRCMD struct {
	Content string `json:"content"`
}

// webDynamicCommon 是通用主要卡片。
type webDynamicCommon struct {
	Title string `json:"title"`
	Desc  string `json:"desc"`
	Cover string `json:"cover"`
}

// webDynamicMajor 是动态主要卡片集合。
type webDynamicMajor struct {
	Type     string              `json:"type"`
	Archive  *webDynamicArchive  `json:"archive"`
	Draw     *webDynamicDraw     `json:"draw"`
	Article  *webDynamicArticle  `json:"article"`
	Opus     *webDynamicOpus     `json:"opus"`
	Music    *webDynamicMusic    `json:"music"`
	Live     *webDynamicLive     `json:"live"`
	LiveRCMD *webDynamicLiveRCMD `json:"live_rcmd"`
	Common   *webDynamicCommon   `json:"common"`
}

// webDynamicModule 是 module_dynamic 模块。
type webDynamicModule struct {
	Desc  *webDynamicDescription `json:"desc"`
	Major *webDynamicMajor       `json:"major"`
}

// webDynamicModules 是动态的 modules 集合。
type webDynamicModules struct {
	Author  webDynamicAuthor `json:"module_author"`
	Dynamic webDynamicModule `json:"module_dynamic"`
}

// webDynamicFeedItem 是完整列表中的单条动态。
type webDynamicFeedItem struct {
	IDStr   string              `json:"id_str"`
	Type    string              `json:"type"`
	Visible *bool               `json:"visible"`
	Modules webDynamicModules   `json:"modules"`
	Orig    *webDynamicFeedItem `json:"orig"`
}

// webDynamicFeedData 是 feed/all 的 data。
type webDynamicFeedData struct {
	UpdateNum      flexibleInt64        `json:"update_num"`
	UpdateBaseline string               `json:"update_baseline"`
	Offset         string               `json:"offset"`
	HasMore        bool                 `json:"has_more"`
	Items          []webDynamicFeedItem `json:"items"`
}

// webDynamicFeedResponse 是 feed/all 完整响应。
type webDynamicFeedResponse struct {
	Code    int32              `json:"code"`
	Message string             `json:"message"`
	Data    webDynamicFeedData `json:"data"`
}

// webDynamicUpdateResponseData 是 feed/all/update 的 data。
type webDynamicUpdateResponseData struct {
	UpdateNum flexibleInt64 `json:"update_num"`
}

// webDynamicUpdateResponse 是 feed/all/update 完整响应。
type webDynamicUpdateResponse struct {
	Code    int32                        `json:"code"`
	Message string                       `json:"message"`
	Data    webDynamicUpdateResponseData `json:"data"`
}

// webDynamicRequestOptions 组装网页动态接口的公共请求选项：
// Chrome 143 UA、t.bilibili.com 来源与设备头、登录 Cookie。
func webDynamicRequestOptions() []requests.Option {
	var opts []requests.Option
	opts = append(opts,
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(15*time.Second),
		requests.RetryOption(3),
		requests.HeaderOption("User-Agent", bilibiliWebUserAgent),
		requests.HeaderOption("Sec-Ch-Ua", bilibiliWebSecChUa),
		requests.HeaderOption("Sec-Ch-Ua-Mobile", "?0"),
		requests.HeaderOption("Sec-CH-UA-Platform", "\"Windows\""),
		requests.HeaderOption("Origin", WebDynamicOrigin),
		requests.HeaderOption("Referer", WebDynamicOrigin+"/"),
		requests.HeaderOption("x-bili-device-req-json", "{\"platform\":\"web\",\"device\":\"pc\"}"),
		requests.HeaderOption("x-bili-locale-json", "{\"clocale\":\"zh-CN\",\"tlocale\":\"zh-CN\"}"),
	)
	if verify := getVerify(); verify != nil {
		opts = append(opts, verify.VerifyOpts...)
	}
	return opts
}

// WebDynamicFeedAll 拉取网页动态完整列表。
func WebDynamicFeedAll(param string) (*webDynamicFeedResponse, error) {
	var params map[string]string
	if param != "" {
		params = map[string]string{"offset": param}
	}
	var resp webDynamicFeedResponse
	err := requestDynamicJSON(BPath(PathWebDynamicFeedAll), params, &resp, webDynamicRequestOptions())
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// WebDynamicFeedUpdate 按基线探测网页动态增量。
func WebDynamicFeedUpdate(baseline string) (*webDynamicUpdateResponse, error) {
	if strings.TrimSpace(baseline) == "" {
		return nil, errors.New("B站动态增量探测缺少 update_baseline")
	}
	params := map[string]string{
		"update_baseline": baseline,
		"type":            "all",
	}
	var resp webDynamicUpdateResponse
	err := requestDynamicJSON(BPath(PathWebDynamicFeedAllUpdate), params, &resp, webDynamicRequestOptions())
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// mapWebDynamicType 把网页动态类型映射为旧版 DescType；
// DYNAMIC_TYPE_DRAW 携带视频稿件时归一为 WithVideo。
func mapWebDynamicType(item *webDynamicFeedItem) DynamicDescType {
	if item == nil {
		return DynamicDescType_DynamicDescTypeUnknown
	}
	switch item.Type {
	case "DYNAMIC_TYPE_WORD":
		return DynamicDescType_TextOnly
	case "DYNAMIC_TYPE_DRAW":
		if item.Modules.Dynamic.Major != nil && item.Modules.Dynamic.Major.Archive != nil {
			return DynamicDescType_WithVideo
		}
		return DynamicDescType_WithImage
	case "DYNAMIC_TYPE_ARTICLE":
		return DynamicDescType_WithPost
	case "DYNAMIC_TYPE_MUSIC":
		return DynamicDescType_WithMusic
	case "DYNAMIC_TYPE_FORWARD":
		return DynamicDescType_WithOrigin
	case "DYNAMIC_TYPE_LIVE":
		return DynamicDescType_WithLive
	case "DYNAMIC_TYPE_LIVE_RCMD":
		return DynamicDescType_WithLiveV2
	case "DYNAMIC_TYPE_NONE", "DYNAMIC_TYPE_COMMON_SQUARE":
		return DynamicDescType_DynamicDescTypeUnknown
	default:
		return DynamicDescType_DynamicDescTypeUnknown
	}
}

// newsFromWebDynamicItems 把增量探测发现的新动态转回旧版 Card 并按 UID 组装 NewsInfo，
// 交给原有通知生成链路；未订阅的 UID 直接丢弃。
func (c *Concern) newsFromWebDynamicItems(items []*webDynamicFeedItem) []*NewsInfo {
	var newsMap = make(map[int64][]*Card)
	var processed int
	var pictureCount int
	for _, item := range items {
		if item == nil {
			continue
		}
		if item.Visible != nil && !*item.Visible {
			continue
		}
		card, err := webDynamicItemToCard(item)
		if err != nil || card == nil {
			logFields := map[string]interface{}{"dynamic_id_str": item.IDStr}
			if err != nil {
				logFields["error"] = err.Error()
			}
			logger.WithFields(logFields).Warn("B站动态增量卡片转换失败，本卡片已跳过")
			continue
		}
		processed++
		pictureCount += countWebDynamicPictures(item)
		if !c.filterCard(card) {
			continue
		}
		uid := card.GetDesc().GetUid()
		newsMap[uid] = append(newsMap[uid], card)
	}
	var result []*NewsInfo
	for uid, cards := range newsMap {
		userInfo, err := c.StateManager.GetUserInfo(uid)
		if err != nil {
			logger.WithField("mid", uid).Debugf("find user info error %v", err)
			continue
		}
		if len(cards) > 0 {
			author := cards[0].GetDesc().GetUserProfile().GetInfo()
			userInfo.Name = author.GetUname()
			userInfo.Face = author.GetFace()
		}
		if len(cards) > 3 {
			// 有时候b站抽风会刷屏
			cards = cards[:3]
		}
		result = append(result, NewNewsInfoWithDetail(userInfo, cards))
	}
	for _, news := range result {
		_ = c.MarkLatestActive(news.Mid, news.Timestamp)
		_ = c.AddUserInfo(&news.UserInfo)
	}
	if len(items) > 0 {
		logger.WithFields(map[string]interface{}{
			"process_items":  processed,
			"notify_batches": len(result),
			"pictures_count": pictureCount,
			"dynamic_id_str": firstDynamicIDStr(items),
		}).Trace("newsFromWebDynamicItems done")
	}
	return result
}

// countWebDynamicPictures 统计一条动态携带的图片数，仅用于日志。
func countWebDynamicPictures(item *webDynamicFeedItem) int {
	if item == nil {
		return 0
	}
	var count int
	if major := item.Modules.Dynamic.Major; major != nil {
		if major.Draw != nil {
			count += len(major.Draw.Items)
		}
		if major.Opus != nil {
			count += len(major.Opus.Pics)
		}
		if major.Article != nil {
			count += len(major.Article.Covers)
		}
	}
	return count
}

// firstDynamicIDStr 取第一条动态的 id_str，仅用于日志定位。
func firstDynamicIDStr(items []*webDynamicFeedItem) string {
	for _, item := range items {
		if item != nil && item.IDStr != "" {
			return item.IDStr
		}
	}
	return ""
}

// webDynamicItemToCard 把网页新版动态转换为旧版 Card：
// Desc 携带 id_str/type/timestamp/uid 等主键信息，Card 字段携带旧版内层 JSON，
// 使 CacheCard 的既有解析与模板渲染逻辑无需改动。
func webDynamicItemToCard(item *webDynamicFeedItem) (*Card, error) {
	if item == nil || item.IDStr == "" {
		return nil, errors.New("动态卡片为空")
	}
	dynamicId, err := strconv.ParseInt(item.IDStr, 10, 64)
	if err != nil {
		// 成品 webDynamicItemToCard @0xd1daa2：fmt.Errorf("无效的动态ID %q", item.IDStr)（len 0x14）。
		return nil, fmt.Errorf("无效的动态ID %q", item.IDStr)
	}
	author := item.Modules.Author
	if author.Mid <= 0 {
		// 成品 webDynamicItemToCard @0xd1db48：fmt.Errorf("动态 %s 缺少作者UID", item.IDStr)（len 0x19）。
		return nil, fmt.Errorf("动态 %s 缺少作者UID", item.IDStr)
	}
	desc := &Card_Desc{
		DynamicId:    dynamicId,
		DynamicIdStr: item.IDStr,
		Type:         mapWebDynamicType(item),
		Timestamp:    int64(author.PubTs),
		Uid:          int64(author.Mid),
		UserProfile: &Card_Desc_UserProfile{
			Info: &Card_Desc_UserProfile_Info{
				Uid:   int64(author.Mid),
				Uname: author.Name,
				Face:  author.Face,
			},
		},
	}
	if item.Orig != nil {
		if origId, err := strconv.ParseInt(item.Orig.IDStr, 10, 64); err == nil {
			desc.OrigDyId = origId
			desc.OrigDyIdStr = item.Orig.IDStr
			desc.OrigType = mapWebDynamicType(item.Orig)
		}
	}
	if major := item.Modules.Dynamic.Major; major != nil && major.Archive != nil {
		desc.Bvid = major.Archive.Bvid
		desc.RidStr = major.Archive.Bvid
	}
	payload, err := legacyPayloadForWebDynamic(item, 0)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化旧版卡片失败: %w", err)
	}
	return &Card{
		Card: string(raw),
		Desc: desc,
	}, nil
}

// legacyPayloadForWebDynamic 把网页新版动态转回旧版 v1 内层卡片 JSON，
// 仅填充 CacheCard.prepare 实际消费的字段。
// depth 用于限制转发嵌套层级，超过上限返回“转发动态嵌套层级异常”。
func legacyPayloadForWebDynamic(item *webDynamicFeedItem, depth int) (map[string]interface{}, error) {
	if item == nil {
		return nil, errors.New("动态卡片为空")
	}
	if depth > webDynamicForwardMaxDepth {
		return nil, errors.New("转发动态嵌套层级异常")
	}
	content := item.Modules.Dynamic.Desc.Text
	major := item.Modules.Dynamic.Major
	switch item.Type {
	case "DYNAMIC_TYPE_DRAW":
		if major != nil && major.Archive != nil {
			payload := map[string]interface{}{}
			setIfNotEmpty(payload, "desc", major.Archive.Desc)
			setIfNotEmpty(payload, "title", major.Archive.Title)
			setIfNotEmpty(payload, "pic", major.Archive.Cover)
			setIfNotEmpty(payload, "dynamic", content)
			return payload, nil
		}
		payload := map[string]interface{}{
			"item": map[string]interface{}{
				"content": content,
			},
		}
		if major != nil && major.Draw != nil && len(major.Draw.Items) > 0 {
			pictures := make([]map[string]interface{}, 0, len(major.Draw.Items))
			for _, pic := range major.Draw.Items {
				pictures = append(pictures, map[string]interface{}{
					"img_src":    normalizeBilibiliImageURL(pic.sourceURL()),
					"img_width":  int64(pic.Width),
					"img_height": int64(pic.Height),
					"img_size":   float64(pic.Size),
				})
			}
			payload["item"] = map[string]interface{}{
				"description":    content,
				"pictures":       pictures,
				"pictures_count": len(pictures),
			}
		}
		if major != nil && major.Opus != nil && len(major.Opus.Pics) > 0 {
			inner := map[string]interface{}{}
			setIfNotEmpty(inner, "title", major.Opus.Title)
			setIfNotEmpty(inner, "summary", major.Opus.Summary.Text)
			var urls []string
			for _, pic := range major.Opus.Pics {
				if normalized := normalizeBilibiliImageURL(pic.sourceURL()); normalized != "" {
					urls = append(urls, normalized)
				}
			}
			if len(urls) > 0 {
				inner["image_urls"] = urls
			}
			payload["item"] = inner
		}
		return payload, nil
	case "DYNAMIC_TYPE_ARTICLE":
		payload := map[string]interface{}{
			"item": map[string]interface{}{},
		}
		if major != nil && major.Article != nil {
			inner := map[string]interface{}{
				"title":   major.Article.Title,
				"summary": major.Article.Desc,
			}
			if len(major.Article.Covers) > 0 {
				inner["image_urls"] = major.Article.Covers
			}
			payload["item"] = inner
		}
		return payload, nil
	case "DYNAMIC_TYPE_MUSIC":
		payload := map[string]interface{}{}
		if major != nil && major.Music != nil {
			payload["title"] = major.Music.Title
			payload["intro"] = major.Music.Label
			payload["cover"] = major.Music.Cover
		}
		return payload, nil
	case "DYNAMIC_TYPE_LIVE":
		payload := map[string]interface{}{}
		if major != nil && major.Live != nil {
			payload["roomid"] = int64(major.Live.ID)
			payload["title"] = major.Live.Title
			payload["cover"] = major.Live.Cover
		}
		return payload, nil
	case "DYNAMIC_TYPE_LIVE_RCMD":
		payload := map[string]interface{}{}
		if major != nil && major.LiveRCMD != nil && major.LiveRCMD.Content != "" {
			// content 本身是 live_play_info 的 JSON 字符串，解包后透传给旧版解析
			var playInfo map[string]interface{}
			if err := json.Unmarshal([]byte(major.LiveRCMD.Content), &playInfo); err == nil && playInfo != nil {
				payload["live_play_info"] = playInfo
			}
		}
		return payload, nil
	case "DYNAMIC_TYPE_COMMON_SQUARE":
		payload := map[string]interface{}{
			"item": map[string]interface{}{
				"content": content,
			},
		}
		if major != nil && major.Common != nil {
			setIfNotEmpty(payload, "title", major.Common.Title)
			setIfNotEmpty(payload, "desc", major.Common.Desc)
			setIfNotEmpty(payload, "cover", major.Common.Cover)
		}
		return payload, nil
	case "DYNAMIC_TYPE_FORWARD":
		payload := map[string]interface{}{
			"item": map[string]interface{}{
				"orig_type": int32(legacyOriginType(item)),
				"content":   content,
			},
		}
		if item.Orig == nil {
			// 原动态已删除时沿用旧版标记串，CacheCard 会尝试从二次解析补救
			payload["origin"] = "源动态不见了"
			payload["item"].(map[string]interface{})["tips"] = "该动态已失效"
			return payload, nil
		}
		originPayload, err := legacyPayloadForWebDynamic(item.Orig, depth+1)
		if err != nil {
			return nil, err
		}
		originRaw, err := json.Marshal(originPayload)
		if err != nil {
			// 成品 legacyPayloadForWebDynamic @0xd1fb4c：
			// fmt.Errorf("编码动态 %s 兼容卡片失败: %w", item.IDStr, err)（len 0x26）。
			return nil, fmt.Errorf("编码动态 %s 兼容卡片失败: %w", item.IDStr, err)
		}
		payload["origin"] = string(originRaw)
		payload["origin_user"] = map[string]interface{}{
			"info": map[string]interface{}{
				"uid":   int64(item.Orig.Modules.Author.Mid),
				"uname": item.Orig.Modules.Author.Name,
				"face":  item.Orig.Modules.Author.Face,
			},
		}
		return payload, nil
	default:
		return map[string]interface{}{
			"item": map[string]interface{}{
				"content": content,
			},
		}, nil
	}
}

// legacyOriginType 计算转发卡 item.orig_type 的取值。
func legacyOriginType(item *webDynamicFeedItem) DynamicDescType {
	if item == nil || item.Orig == nil {
		return DynamicDescType_DynamicDescTypeUnknown
	}
	return mapWebDynamicType(item.Orig)
}

// setIfNotEmpty 值非空时写入 map，避免旧版卡片被空字段污染。
func setIfNotEmpty(payload map[string]interface{}, key string, value string) {
	if value != "" {
		payload[key] = value
	}
}
