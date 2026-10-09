package missevan

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/sirupsen/logrus"
)

const (
	// fm API 域名，直播状态接口与直播间页面均在此域下
	apiHost = "https://fm.missevan.com"

	// 个人主页域，用户主页、声音列表与声音详情页均在此域下
	personalHost = "https://www.missevan.com"

	// 直播状态接口路径，完整URL形如 %s/api/v2/live/%d
	liveAPIPathFormat = "%s/api/v2/live/%d"

	// 个人主页声音列表页，参数直接拼在尾部问号之后
	personSoundListURL = "https://www.missevan.com/person/getusersound?"

	// 声音详情页
	soundURLFormat = "%s/sound/%d"

	// Cookie 所属域
	missevanDomain = ".missevan.com"

	defaultRequestSpacing = time.Millisecond * 500
	maxRequestSpacing     = time.Second * 10
	defaultRequestTimeout = time.Second * 15

	// requestRetry 成品 RetryOption 的捕获值恒为 0（disasm request 0xdaf6b6 lea func5、
	// 0xdaf6bd mov 0x40(%rsp)、0xdaf6c2 movq $0x0,0x48(%rsp)），即 0 次重试；不要改回非 0。
	requestRetry = 0

	defaultLiveInterval = time.Second * 15
	minLiveInterval     = time.Second * 5
	defaultNewsInterval = time.Second * 30
	minNewsInterval     = time.Second * 10

	defaultProfileInterval = time.Minute * 30
	minProfileInterval     = time.Minute

	// 声音列表单页条数，FetchSounds 入参 <= 0 时回退该默认值
	defaultSoundPageSize = 20
)

// Client 是猫耳FM的HTTP客户端，所有请求严格串行并保持最小间隔。
type Client struct {
	mu        sync.Mutex
	nextStart time.Time
}

// Sound 是猫耳FM的一条声音投稿。
type Sound struct {
	ID         int64  `json:"id"`
	CreateTime int64  `json:"create_time"`
	Title      string `json:"soundstr"`
	DurationMS int64  `json:"duration"`
	Cover      string `json:"front_cover"`
}

// SoundList 是个人主页声音列表接口的响应。
type SoundList struct {
	Success bool `json:"success"`
	Info    struct {
		Data       []*Sound `json:"Datas"`
		Pagination struct {
			Page     int  `json:"p"`
			Count    int  `json:"count"`
			MaxPage  int  `json:"maxpage"`
			PageSize int  `json:"pagesize"`
			HasMore  bool `json:"hasMore"`
		} `json:"pagination"`
	} `json:"info"`
}

// Profile 是从个人主页HTML中解析出的用户资料。
type Profile struct {
	UserID        int64
	Name          string
	Avatar        string
	RoomID        int64
	PageLive      bool
	PageLiveTitle string
	PageLiveCover string
}

// LiveSnapshot 是直播状态接口返回的主播与直播间快照。
type LiveSnapshot struct {
	RoomID     int64
	CreatorID  int64
	Name       string
	Avatar     string
	Title      string
	Cover      string
	Living     bool
	Online     int64
	Popularity int64
}

// liveResponse 是 /api/v2/live/{roomID} 的响应结构。
type liveResponse struct {
	Code int `json:"code"`
	Info struct {
		Room struct {
			RoomID    int64  `json:"room_id"`
			CreatorID int64  `json:"creator_id"`
			Name      string `json:"name"`
			CoverURL  string `json:"cover_url"`
			Status    struct {
				Open         int  `json:"open"`
				Broadcasting bool `json:"broadcasting"`
			} `json:"status"`
			Statistics struct {
				Online int64 `json:"online"`
				Score  int64 `json:"score"`
			} `json:"statistics"`
		} `json:"room"`
		Creator struct {
			UserID   int64  `json:"user_id"`
			Username string `json:"username"`
			IconURL  string `json:"iconurl"`
		} `json:"creator"`
	} `json:"info"`
}

// getRequestSpacing 返回相邻请求之间的最小间隔，默认500ms，负数按0，最大按10s。
func getRequestSpacing() time.Duration {
	spacing := defaultRequestSpacing
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("missevan.requestSpacing"); v != 0 {
			spacing = v
		}
	}
	if spacing < 0 {
		spacing = 0
	}
	if spacing > maxRequestSpacing {
		spacing = maxRequestSpacing
	}
	return spacing
}

// request 串行地发起一次GET请求，所有猫耳FM接口请求都必须经过这里。
// name 是这次请求的类型标记，仅用于日志字段 request_type。
// accept 是这次请求的 Accept 头：成品把每个调用点的字面量作为第 4 个参数传入
// （FetchProfile @0xdb02d7 载入 0x10c5601 len 0x1f；FetchSounds @0xdb1172 /
// FetchLive @0xdb1380 载入 0x10c8953 len 0x21），request 在 +0x1c0 构造
// requests.HeaderOption("Accept", accept)（rodata 键 @0x109f6a6 len 6）。
//
// 成品 request 在 +0x1c0 构造的 option 切片共 7 项，按 @0x1c8..0x1f8 顺序为：
//
//	TimeoutOption(timeout)                                   （闭包 0xdb0100）
//	AddUAOption()                                            （闭包 0xdb0000，user-agent/Chrome UA）
//	HeaderOption("Accept", accept)                           （闭包 0xdaff00）
//	HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")（闭包 0xdafe20，键 @0x10adeec len15 / 值 @0x10b9347 len23）
//	HeaderOption("Referer", "https://www.missevan.com/")     （闭包 0xdafd40，键 @0x10a30db len7 / 值 @0x10bc576 len25）
//	RetryOption(0)                                           （闭包 0xdafd20；request 0xdaf6bd 把 func5 写入
//	                                                          0x40(%rsp)、0xdaf6c2 movq $0x0,0x48(%rsp)，捕获值 0）
//	HttpCodeOption(&code)                                    （闭包 0xdafcc0）
//
// 两个 HeaderOption 都是常量、无条件构造，故 FetchProfile/FetchSounds/FetchLive 每次请求
// 都带这两个头；Referer 固定 www 域、带尾斜杠，FetchLive 也不例外。成品没有
// RequestAutoRefererOption（无任何按 host 动态生成 Referer 的闭包/字符串拼接）。
func (c *Client) request(name string, url string, out interface{}, accept string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if wait := time.Until(c.nextStart); wait > 0 {
		time.Sleep(wait)
	}

	timeout := defaultRequestTimeout
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("missevan.requestTimeout"); v > 0 {
			timeout = v
		}
	}

	start := time.Now()
	var code int
	err := requests.Get(url, nil, out,
		requests.TimeoutOption(timeout),
		requests.AddUAOption(),
		requests.HeaderOption("Accept", accept),
		requests.HeaderOption("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8"),
		requests.HeaderOption("Referer", "https://www.missevan.com/"),
		requests.RetryOption(requestRetry),
		requests.HttpCodeOption(&code),
	)
	c.nextStart = time.Now().Add(getRequestSpacing())
	if err != nil {
		logger.WithError(err).WithField("url", url).Errorf("猫耳FM接口请求失败")
		return err
	}
	logger.WithFields(logrus.Fields{
		"request_type": name,
		"api_host":     endpointHost(url),
		"api_path":     endpointPath(url),
		"http_status":  code,
		"request_cost": time.Since(start),
	}).Tracef("猫耳FM接口请求完成")
	return nil
}

// endpointHost 解析一次请求URL并返回其中的host（不含端口）。
func endpointHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return ""
	}
	return u.Hostname()
}

// endpointPath 解析一次请求URL并返回其中的转义路径。
func endpointPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return ""
	}
	return u.EscapedPath()
}

// FetchProfile 抓取并解析指定用户的个人主页。
// 成品格式串 "%s/%d/"（rodata @0x109f7cc len 6，FetchProfile+0x6f 以 2 参
// fmt.Sprintf 载入）带尾斜杠，故主页 URL 形如 https://www.missevan.com/<uid>/。
func (c *Client) FetchProfile(uid int64) (*Profile, error) {
	var body bytes.Buffer
	url := fmt.Sprintf("%s/%d/", personalHost, uid)
	if err := c.request("profile", url, &body, "text/html,application/xhtml+xml"); err != nil {
		return nil, fmt.Errorf("获取猫耳FM个人主页失败: %w", err)
	}
	return parseProfileHTML(body.Bytes(), uid)
}

// parseProfileHTML 从个人主页HTML中解析用户资料。
// 页面以服务端渲染为主，全部字段来自 goquery 选择器的文本或属性。
func parseProfileHTML(html []byte, uid int64) (*Profile, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("解析猫耳FM个人主页失败: %w", err)
	}

	var userID int64
	if v, ok := doc.Find("#user-mark").Eq(0).Attr("data-page"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			userID = n
		}
	}
	// 页面上的用户ID必须有效；调用方指定了uid时还必须是同一个用户
	if userID <= 0 || (uid > 0 && userID != uid) {
		return nil, errors.New("猫耳FM个人主页未返回目标用户")
	}

	profile := new(Profile)
	profile.UserID = userID

	nameSelection := doc.Find("#t_u_n > a").Eq(0)
	name := nameSelection.Text()
	if title, ok := nameSelection.Attr("title"); ok {
		name = title
	}
	profile.Name = strings.TrimSpace(name)

	if src, ok := doc.Find("#topusermainicon img").Eq(0).Attr("src"); ok {
		profile.Avatar = absoluteMissevanURL(src)
	}

	livePanel := doc.Find(".home-right-panel.live").Eq(0)
	if v, ok := livePanel.Attr("data-id"); ok {
		if roomID, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			profile.RoomID = roomID
		}
	}
	if v, ok := livePanel.Attr("data-status"); ok {
		profile.PageLive = strings.TrimSpace(v) == "1"
	}
	if title, ok := doc.Find(".home-detail-name").Eq(0).Attr("title"); ok {
		profile.PageLiveTitle = strings.TrimSpace(title)
	}
	if style, ok := doc.Find(".home-body-cover").Eq(0).Attr("style"); ok {
		profile.PageLiveCover = absoluteMissevanURL(backgroundImageURL(style))
	}

	if profile.Name == "" {
		return nil, errors.New("猫耳FM个人主页未返回用户名")
	}
	return profile, nil
}

// FetchSounds 拉取指定用户的声音列表，pageSize <= 0 时按默认页大小。
func (c *Client) FetchSounds(uid int64, pageSize int) (*SoundList, error) {
	if pageSize <= 0 {
		pageSize = defaultSoundPageSize
	}
	params := url.Values{}
	params.Set("user_id", strconv.FormatInt(uid, 10))
	params.Set("page_size", strconv.Itoa(pageSize))

	var list SoundList
	url := personSoundListURL + params.Encode()
	if err := c.request("sound_list", url, &list, "application/json, text/plain, */*"); err != nil {
		return nil, fmt.Errorf("获取猫耳FM声音列表失败: %w", err)
	}
	if !list.Success {
		return nil, errors.New("猫耳FM声音列表接口返回失败")
	}
	return &list, nil
}

// FetchLive 查询指定直播间的状态快照。
func (c *Client) FetchLive(roomID int64) (*LiveSnapshot, error) {
	if roomID <= 0 {
		return nil, errors.New("猫耳FM直播间号为空")
	}
	var resp liveResponse
	url := fmt.Sprintf(liveAPIPathFormat, apiHost, roomID)
	if err := c.request("live_status", url, &resp, "application/json, text/plain, */*"); err != nil {
		return nil, fmt.Errorf("获取猫耳FM直播状态失败: %w", err)
	}
	if resp.Code != 0 || resp.Info.Room.RoomID <= 0 {
		return nil, fmt.Errorf("猫耳FM直播接口返回异常 code=%d", resp.Code)
	}
	return parseLiveResponse(&resp)
}

// parseLiveResponse 校验直播接口响应并转换为直播间快照。
func parseLiveResponse(resp *liveResponse) (*LiveSnapshot, error) {
	if resp == nil {
		return nil, nil
	}
	// 优先用直播间自带的 creator_id，缺失时退回 creator.user_id
	creatorID := resp.Info.Room.CreatorID
	if creatorID <= 0 {
		creatorID = resp.Info.Creator.UserID
	}
	return &LiveSnapshot{
		RoomID:    resp.Info.Room.RoomID,
		CreatorID: creatorID,
		Name:      resp.Info.Creator.Username,
		Avatar:    absoluteMissevanURL(resp.Info.Creator.IconURL),
		Title:     resp.Info.Room.Name,
		Cover:     absoluteMissevanURL(resp.Info.Room.CoverURL),
		// 复核 recovered/types（liveResponse size=128）：Status.Broadcasting 位于
		// 结构体偏移 0x40；成品 parseLiveResponse(0xdb1520) 以 movzbl 0x40(%rcx)
		// 直接读入 LiveSnapshot.Living，来源即 Status.Broadcasting，与重建一致。
		Living:     resp.Info.Room.Status.Broadcasting,
		Online:     resp.Info.Room.Statistics.Online,
		Popularity: resp.Info.Room.Statistics.Score,
	}, nil
}

// absoluteMissevanURL 把协议相对的图片/页面地址补成绝对地址。
func absoluteMissevanURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

// backgroundImageURL 从 CSS background-image 的 url(...) 包装中剥出裸地址。
func backgroundImageURL(u string) string {
	idx := strings.Index(u, "url(")
	if idx < 0 {
		return ""
	}
	u = strings.TrimSpace(u[idx+4:])
	if i := strings.Index(u, ")"); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimSpace(u)
	return strings.Trim(u, "\"'")
}
