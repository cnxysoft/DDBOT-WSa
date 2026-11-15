package bilibili

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/sirupsen/logrus"
)

// 合集（系列）订阅：B站「合集/系列」页面的新稿件推送。
//
// 为什么需要单独一类订阅：合集里的稿件（尤其是直播回放合集）不一定产生动态，
// 只订阅动态会漏推，因此直接轮询合集稿件列表比对新增。
// 订阅 id 形如 <mid>/<series_id>，因为合集稿件接口必须同时带 mid 和 series_id。

const (
	// PathSeriesArchives 合集内稿件列表，需要 mid 与 series_id
	PathSeriesArchives = "/x/series/archives"
	// PathSeriesMeta 合集元信息（名称、稿件总数等），需要 mid 与 series_id
	PathSeriesMeta = "/x/series/series"

	// SeriesIdSeparator 合集订阅 id 的分隔符
	SeriesIdSeparator = "/"
	// SeriesArchivesPageSize 每轮拉取的合集稿件条数（用于比对新增）
	SeriesArchivesPageSize = 30
)

var seriesUrlRe = regexp.MustCompile(`space\.bilibili\.com/(\d+)/lists/(\d+)`)

// SeriesArchive 合集内的一个稿件
type SeriesArchive struct {
	Aid     int64  `json:"aid"`
	Bvid    string `json:"bvid"`
	Title   string `json:"title"`
	PubDate int64  `json:"pubdate"`
	Cover   string `json:"pic"`
	UpMid   int64  `json:"upMid"`
}

// SeriesMeta 合集元信息
type SeriesMeta struct {
	SeriesId     int64  `json:"series_id"`
	Mid          int64  `json:"mid"`
	Name         string `json:"name"`
	Total        int32  `json:"total"`
	LastUpdateTs int64  `json:"last_update_ts"`
}

// SeriesInfo 合集订阅的本地状态
type SeriesInfo struct {
	Mid      int64  `json:"mid"`
	SeriesId int64  `json:"series_id"`
	Name     string `json:"name"`
	Total    int32  `json:"total"`
	// LastAid 已推送过的最新稿件 aid，用于比对新增
	LastAid int64 `json:"last_aid"`
}

// GetUid 返回订阅 id（<mid>/<series_id>），用于框架按订阅关系分发推送
func (s *SeriesInfo) GetUid() interface{} {
	if s == nil {
		return ""
	}
	return SeriesSubId(s.Mid, s.SeriesId)
}

// SeriesSubId 生成合集订阅 id
func SeriesSubId(mid, seriesId int64) string {
	return fmt.Sprintf("%d%s%d", mid, SeriesIdSeparator, seriesId)
}

// IsSeriesSubId 判断字符串是否为合集订阅 id（形如 <mid>/<series_id> 或合集链接）
func IsSeriesSubId(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if seriesUrlRe.MatchString(s) {
		return true
	}
	if !strings.Contains(s, SeriesIdSeparator) {
		return false
	}
	_, _, err := ParseSeriesSubId(s)
	return err == nil
}

// ParseSeriesSubId 解析合集订阅 id，支持：
//   - 123895/3945451
//   - https://space.bilibili.com/123895/lists/3945451?type=series
func ParseSeriesSubId(s string) (mid, seriesId int64, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, errors.New("合集订阅 id 为空")
	}
	if m := seriesUrlRe.FindStringSubmatch(s); len(m) == 3 {
		mid, err = strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("解析合集链接中的 mid 失败 %v", err)
		}
		seriesId, err = strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("解析合集链接中的 series_id 失败 %v", err)
		}
		return mid, seriesId, nil
	}
	parts := strings.Split(s, SeriesIdSeparator)
	if len(parts) != 2 {
		return 0, 0, errors.New("合集订阅 id 格式应为 <mid>/<series_id> 或合集链接")
	}
	mid, err = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("解析合集订阅 id 的 mid 失败 %v", err)
	}
	seriesId, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("解析合集订阅 id 的 series_id 失败 %v", err)
	}
	if mid <= 0 || seriesId <= 0 {
		return 0, 0, errors.New("合集订阅 id 中的 mid 与 series_id 必须为正整数")
	}
	return mid, seriesId, nil
}

// NewSeriesArchives 从稿件列表中筛选出比 lastAid 更新的稿件，
// 按 aid 升序返回（旧→新），保证推送顺序与发布顺序一致。
func NewSeriesArchives(archives []*SeriesArchive, lastAid int64) []*SeriesArchive {
	var result []*SeriesArchive
	for _, a := range archives {
		if a == nil || a.Aid <= 0 {
			continue
		}
		if a.Aid > lastAid {
			result = append(result, a)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Aid < result[j].Aid })
	return result
}

type seriesArchivesResponse struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Archives []*SeriesArchive `json:"archives"`
	} `json:"data"`
}

type seriesMetaResponse struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Meta *SeriesMeta `json:"meta"`
	} `json:"data"`
}

// GetSeriesArchives 拉取合集内最新的稿件列表。
func GetSeriesArchives(mid, seriesId int64, ps int) ([]*SeriesArchive, error) {
	if ps <= 0 {
		ps = SeriesArchivesPageSize
	}
	params := map[string]string{
		"mid":         strconv.FormatInt(mid, 10),
		"series_id":   strconv.FormatInt(seriesId, 10),
		"only_normal": "true",
		"sort":        "desc",
		"pn":          "1",
		"ps":          strconv.Itoa(ps),
	}
	var buf bytes.Buffer
	if err := requests.Get(BPath(PathSeriesArchives), params, &buf, seriesRequestOptions()...); err != nil {
		return nil, err
	}
	return parseSeriesArchives(buf.Bytes())
}

// GetSeriesMeta 拉取合集元信息（用于校验合集是否存在、获取合集名）。
func GetSeriesMeta(mid, seriesId int64) (*SeriesMeta, error) {
	params := map[string]string{
		"mid":       strconv.FormatInt(mid, 10),
		"series_id": strconv.FormatInt(seriesId, 10),
	}
	var buf bytes.Buffer
	if err := requests.Get(BPath(PathSeriesMeta), params, &buf, seriesRequestOptions()...); err != nil {
		return nil, err
	}
	return parseSeriesMeta(buf.Bytes())
}

func seriesRequestOptions() []requests.Option {
	opts := []requests.Option{
		AddUAOption(),
		requests.TimeoutOption(time.Second * 15),
		requests.RequestAutoHostOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.WithCookieJar(cj.Load()),
	}
	return append(opts, GetVerifyOption()...)
}

// parseSeriesArchives 解析合集稿件列表响应（独立出来便于单测覆盖失败分支）。
func parseSeriesArchives(body []byte) ([]*SeriesArchive, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("合集稿件列表为空响应")
	}
	var resp seriesArchivesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析合集稿件列表失败 %v", err)
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("查询合集稿件失败 code %v msg %v", resp.Code, resp.Message)
	}
	return resp.Data.Archives, nil
}

// parseSeriesMeta 解析合集元信息响应（独立出来便于单测覆盖失败分支）。
func parseSeriesMeta(body []byte) (*SeriesMeta, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("合集信息为空响应")
	}
	var resp seriesMetaResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析合集信息失败 %v", err)
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("查询合集信息失败 code %v msg %v", resp.Code, resp.Message)
	}
	if resp.Data.Meta == nil || resp.Data.Meta.SeriesId <= 0 {
		return nil, errors.New("合集不存在或查询不到合集信息")
	}
	return resp.Data.Meta, nil
}

// SeriesNewInfo 合集新增稿件事件
type SeriesNewInfo struct {
	*SeriesInfo
	// UpName UP主名字，用于推送文案
	UpName  string
	Archive *SeriesArchive
}

func (s *SeriesNewInfo) Site() string {
	return Site
}

func (s *SeriesNewInfo) Type() concern_type.Type {
	return Series
}

func (s *SeriesNewInfo) GetUid() interface{} {
	return SeriesSubId(s.Mid, s.SeriesId)
}

func (s *SeriesNewInfo) Logger() *logrus.Entry {
	if s == nil {
		return logger
	}
	return logger.WithFields(logrus.Fields{
		"Site":     Site,
		"SeriesId": SeriesSubId(s.Mid, s.SeriesId),
		"Name":     s.Name,
		"Aid":      s.Archive.GetAid(),
		"Bvid":     s.Archive.GetBvid(),
	})
}

// GetAid 空安全地取稿件 aid
func (a *SeriesArchive) GetAid() int64 {
	if a == nil {
		return 0
	}
	return a.Aid
}

// GetBvid 空安全地取稿件 bvid
func (a *SeriesArchive) GetBvid() string {
	if a == nil {
		return ""
	}
	return a.Bvid
}

// GetTitle 空安全地取稿件标题
func (a *SeriesArchive) GetTitle() string {
	if a == nil {
		return ""
	}
	return a.Title
}

// GetUrl 稿件播放页地址
func (a *SeriesArchive) GetUrl() string {
	if a == nil || a.Bvid == "" {
		return ""
	}
	return BVIDUrl(a.Bvid)
}

// ConcernSeriesNotify 合集新增稿件的推送
type ConcernSeriesNotify struct {
	GroupCode int64 `json:"group_code"`
	*SeriesNewInfo

	msgCache *mmsg.MSG
}

func NewConcernSeriesNotify(groupCode int64, info *SeriesNewInfo) *ConcernSeriesNotify {
	return &ConcernSeriesNotify{
		GroupCode:     groupCode,
		SeriesNewInfo: info,
	}
}

func (n *ConcernSeriesNotify) GetGroupCode() int64 {
	return n.GroupCode
}

// TextFilterContent 关键词过滤使用的文本内容
func (n *ConcernSeriesNotify) TextFilterContent() string {
	if n == nil || n.SeriesNewInfo == nil {
		return ""
	}
	return strings.Join([]string{
		n.UpName, n.Name, n.Archive.GetTitle(),
	}, " ")
}

func (n *ConcernSeriesNotify) ToMessage() *mmsg.MSG {
	if n == nil || n.SeriesNewInfo == nil {
		return nil
	}
	var data = map[string]interface{}{
		"up_name":     n.UpName,
		"series_name": n.Name,
		"title":       n.Archive.GetTitle(),
		"url":         n.Archive.GetUrl(),
		"cover":       n.Archive.Cover,
		"pub_time":    n.Archive.PubDate,
		"group_code":  n.GroupCode,
	}
	var err error
	n.msgCache, err = template.LoadAndExec("notify.group.bilibili.series.tmpl", data)
	if err != nil {
		logger.Errorf("bilibili: ConcernSeriesNotify LoadAndExec error %v", err)
	}
	return n.msgCache
}
