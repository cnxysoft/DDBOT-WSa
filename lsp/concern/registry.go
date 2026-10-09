package concern

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

var globalCenter = newConcernCenter()
var notifyChan = make(chan Notify, 25)

type option struct {
}

// OptFunc 预留的扩展字段，暂无用处
type OptFunc func(opt *option) *option

type center struct {
	concernList []Concern
	// for cache
	concernMap     map[string]Concern
	concernTypeMap map[string]concern_type.Type
	concernSites   []string
}

func (gc *center) freshCache() {
	var concernMap = make(map[string]Concern)
	var concernSites []string
	var concernTypeMap = make(map[string]concern_type.Type)
	for _, c := range gc.concernList {
		concernMap[c.Site()] = c
		concernSites = append(concernSites, c.Site())
		concernTypeMap[c.Site()] = concern_type.Empty.Add(c.Types()...)
	}
	gc.concernMap = concernMap
	gc.concernSites = concernSites
	gc.concernTypeMap = concernTypeMap
}

func (gc *center) GetConcernMap() map[string]Concern {
	return gc.concernMap
}

func (gc *center) GetConcernSites() []string {
	return gc.concernSites
}

func (gc *center) GetConcernTypeMap() map[string]concern_type.Type {
	return gc.concernTypeMap
}

func (gc *center) GetConcernList() []Concern {
	return gc.concernList
}

func (gc *center) GetConcernBySite(site string) (Concern, error) {
	if c, found := gc.GetConcernMap()[site]; found {
		return c, nil
	}
	return nil, ErrSiteNotSupported
}

func (gc *center) CheckSiteAndType(site string, ctype concern_type.Type) error {
	if combineType, found := gc.GetConcernTypeMap()[site]; found {
		if combineType.ContainAll(ctype) {
			return nil
		}
		return ErrTypeNotSupported
	}
	return ErrSiteNotSupported
}

func (gc *center) Register(c Concern, opts ...OptFunc) {
	if c == nil {
		panic("Concern: Register <nil> concern")
	}
	site := c.Site()

	for _, concern := range gc.GetConcernList() {
		if concern.Site() == site {
			panic(fmt.Sprintf("Concern %v: is already registered", site))
		}
	}

	for _, ctype := range c.Types() {
		if !ctype.IsTrivial() {
			panic(fmt.Sprintf("Concern %v: Type %v IsTrivial() must be True", site, ctype.String()))
		}
	}
	if concern_type.Empty.Add(c.Types()...).Empty() {
		panic(fmt.Sprintf("Concern %v: register with empty types", site))
	}
	gc.concernList = append(gc.concernList, c)
	gc.freshCache()
}

func (gc *center) StartAll() {
	// 成品 x64 0xaeec80：StartAll 直接调 resolveSiteStartupSelection(gc.concernList)，
	// 返回值第一个即本次真正启动的 Concern 列表（成品该函数返回 5 个值），随后写回 concernList。
	startList, _, _, unknown, configured := resolveSiteStartupSelection(gc.concernList)
	gc.concernList = startList
	switch {
	case !configured:
		logger.Info("未配置 sites.enabled，启动全部已注册站点")
	case len(unknown) > 0:
		logger.WithField("unknown", unknown).Warn("sites.enabled 包含不支持的站点名称")
	default:
		logger.Info("站点启动白名单已生效")
	}
	// 成品 0xaeec80 +0xb8：写回 concernList 后立即重建一次 cache；末尾 wg.Wait 后再调一次（+0x886）。
	gc.freshCache()
	var wg sync.WaitGroup
	var errConcern = make([]int, len(startList))
	for idx, c := range startList {
		wg.Add(1)
		go func(idx int, c Concern) {
			defer wg.Done()
			c.FreshIndex()
			logger.WithField("site", c.Site()).Info("正在启动站点模块")
			err := c.Start()
			if err != nil {
				logger.Errorf("启动Concern %v 失败 - %v", c.Site(), err)
				errConcern[idx] = 1
				return
			}
			logger.WithField("site", c.Site()).Info("站点模块启动成功")
		}(idx, c)
	}
	wg.Wait()
	logger.Info("站点模块启动完成")
	var newConcern []Concern
	for idx, v := range errConcern {
		if v == 0 {
			newConcern = append(newConcern, startList[idx])
		}
	}
	gc.concernList = newConcern
	gc.freshCache()
	return
}

// resolveSiteStartupSelection 解析 sites.enabled 配置得到本次启动的站点选集。
//
// 返回：本次启动的 Concern 列表、计划启动的站点名、已停用站点名、sites.enabled 中不支持的站点名，
// 以及是否显式配置了 sites.enabled。
// 兼容模式（未配置 sites.enabled）下 startList 为全部已注册 Concern，enabled 为全部站点名；
// 显式配置 enabled: [] 时 startList/enabled 为空，表示一个站点都不启动。
//
// 证据（DDBOT-Lite x64 成品，concern.resolveSiteStartupSelection @ registry.go:148，1120B）：
// callees = cfg.GetEnabledSites(+0x32)、filterConcernsBySite(+0xe0，本函数唯一调用者)、
// strings.ToLower×2(+0x1d0/+0x339)、sort.Strings×2(+0x266/+0x3d2)；StartAll(+0x33)、
// StartupSiteSelection(+0x2c) 调它，StartAll 把返回的第一个 slice 直接写回 gc.concernList。
func resolveSiteStartupSelection(concerns []Concern) (startList []Concern, enabled, disabled, unknown []string, configured bool) {
	selection, configured := cfg.GetEnabledSites()
	if !configured {
		// 兼容模式：全部 Concern 启动，enabled 为全部站点名（ToLower + 排序）。
		startList = append([]Concern(nil), concerns...)
		enabled = make([]string, 0, len(startList))
		for _, c := range startList {
			enabled = append(enabled, strings.ToLower(c.Site()))
		}
		sort.Strings(enabled)
		return startList, enabled, nil, nil, false
	}
	startList, disabled, unknown = filterConcernsBySite(concerns, selection)
	enabled = make([]string, 0, len(startList))
	for _, c := range startList {
		enabled = append(enabled, strings.ToLower(c.Site()))
	}
	sort.Strings(enabled)
	return startList, enabled, disabled, unknown, true
}

// filterConcernsBySite 按 sites.enabled 选集过滤 Concern，返回命中的 Concern 列表、
// 已注册但未选中的站点名（disabled）、选集里未注册的站点名（unknown）。
// 匹配大小写不敏感（TrimSpace+ToLower），选集含 "*" 时全部命中。
// 证据（成品 x64 0xaeffe0，2048B）：makemap + TrimSpace + ToLower + mapassign 建选集，
// 逐 Concern 调 Site()+ToLower 比对，map 迭代求 unknown，末尾两次 sort.Strings 排序。
func filterConcernsBySite(concerns []Concern, selection []string) (startList []Concern, disabled, unknown []string) {
	selectionSet := make(map[string]struct{}, len(selection))
	wildcard := false
	for _, s := range selection {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "*" {
			wildcard = true
			continue
		}
		if s == "" {
			continue
		}
		selectionSet[s] = struct{}{}
	}
	siteSet := make(map[string]struct{}, len(concerns))
	for _, c := range concerns {
		site := strings.ToLower(c.Site())
		siteSet[site] = struct{}{}
		if wildcard {
			startList = append(startList, c)
			continue
		}
		if _, ok := selectionSet[site]; ok {
			startList = append(startList, c)
		} else {
			disabled = append(disabled, site)
		}
	}
	if !wildcard {
		for site := range selectionSet {
			if _, ok := siteSet[site]; !ok {
				unknown = append(unknown, site)
			}
		}
	}
	sort.Strings(disabled)
	sort.Strings(unknown)
	return startList, disabled, unknown
}

// containsSite 判断 name 是否存在于 list 中。
func containsSite(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

func (gc *center) StopAll() {
	for _, c := range gc.GetConcernList() {
		c.Stop()
	}
	close(notifyChan)
}

func newConcernCenter() *center {
	return &center{}
}

// RegisterConcern 向DDBOT注册一个 Concern。
func RegisterConcern(c Concern, opts ...OptFunc) {
	globalCenter.Register(c, opts...)
}

// ClearConcern 现阶段仅用于测试，如果用于其他目的将导致不可预料的后果。
func ClearConcern() {
	globalCenter = newConcernCenter()
}

// StartAll 启动所有 Concern，正常情况下框架会负责启动。
func StartAll() error {
	globalCenter.StartAll()
	return nil
}

// StopAll 停止所有Concern模块，正常情况下框架会负责停止。
// 会关闭notifyChan，所以停止后禁止再向notifyChan中写入数据。
func StopAll() {
	globalCenter.StopAll()
}

// ListConcern 返回所有注册过的 Concern。
func ListConcern() []Concern {
	return globalCenter.GetConcernList()
}

// GetConcernBySite 根据site返回 Concern。
// 如果site没有注册过，则会返回 ErrSiteNotSupported。
func GetConcernBySite(site string) (Concern, error) {
	return globalCenter.GetConcernBySite(site)
}

// GetConcernBySiteAndType 根据site和ctype返回 Concern。
// 如果site没有注册过，则会返回 ErrSiteNotSupported；
// 如果site注册过，但不支持指定的ctype，则会返回 ErrTypeNotSupported。
func GetConcernBySiteAndType(site string, ctype concern_type.Type) (Concern, error) {
	if err := globalCenter.CheckSiteAndType(site, ctype); err != nil {
		return nil, err
	}
	return globalCenter.GetConcernBySite(site)
}

// StartupSiteSelection 返回 StartAll 时解析出的站点启动选集，供启动计划日志消费。
//
// 返回计划启动的站点、已停用的已注册站点、sites.enabled 中不支持的站点名称，
// 以及是否显式配置了 sites.enabled。
// 兼容模式（未配置 sites.enabled）下 enabled 为全部已注册站点。
//
// 证据（DDBOT-Lite x64 成品）：唯一调用方 bot.logSiteStartupPlan @ bot.go:142
// 按 3 个 []string + 1 个 bool 消费（logrus 字段 enabled_sites/disabled_sites/unknown_sites）。
func StartupSiteSelection() (enabled, disabled, unknown []string, configured bool) {
	// 成品 x64 0xaf08c0：取 globalCenter.concernList 调 resolveSiteStartupSelection，
	// 丢弃返回的 startList，只回传 3 个站点名 slice + configured（bot.logSiteStartupPlan 消费）。
	_, enabled, disabled, unknown, configured = resolveSiteStartupSelection(globalCenter.concernList)
	return
}

// ListSite 返回所有注册的 Concern 支持的site。
func ListSite() []string {
	return globalCenter.GetConcernSites()
}

// GetNotifyChan 推送 channel，所有推送需要发送到这个channel中。
func GetNotifyChan() chan<- Notify {
	return notifyChan
}

// ReadNotifyChan 读取推送 channel，应该只由框架负责调用。
func ReadNotifyChan() <-chan Notify {
	return notifyChan
}

// GetConcernTypes 根据site查询 Concern，返回 Concern.Types。
// 如果site没有注册过，则返回 ErrSiteNotSupported。
func GetConcernTypes(site string) (concern_type.Type, error) {
	if _, err := globalCenter.GetConcernBySite(site); err != nil {
		return concern_type.Empty, err
	}
	return globalCenter.GetConcernTypeMap()[site], nil
}

// ParseRawSite 解析string格式的site，可以安全处理用户输入的site。
// 返回匹配的site，如果没有匹配上，返回 ErrSiteNotSupported。
func ParseRawSite(rawSite string) (string, error) {
	var (
		found bool
		site  string
	)

	rawSite = strings.Trim(rawSite, `"`)
	site, found = utils.PrefixMatch(ListSite(), rawSite)
	if !found {
		return "", ErrSiteNotSupported
	}
	return site, nil
}

// ParseRawSiteAndType 尝试解析string格式的 site 和 ctype，可以安全处理用户输入的site和ctype。
// 如果site合法，rawType为空，则默认返回注册时的第一个type。
// rawSite 和 rawType 默认为前缀匹配模式，即bi可以匹配bilibili，n可以匹配news。
func ParseRawSiteAndType(rawSite string, rawType string) (string, concern_type.Type, error) {
	var (
		site  string
		_type string
		found bool
		err   error
	)
	rawSite = strings.Trim(rawSite, `"`)
	rawType = strings.Trim(rawType, `"`)
	site, err = ParseRawSite(rawSite)
	if err != nil {
		return "", concern_type.Empty, err
	}
	var sTypes []string
	ctypes, _ := GetConcernTypes(site)
	// 如果没有指定type，则默认注册时的第一个type
	if rawType == "" {
		return site, ctypes.Split()[0], nil
	}
	for _, t := range ctypes.Split() {
		sTypes = append(sTypes, t.String())
	}
	_type, found = utils.PrefixMatch(sTypes, rawType)
	if !found {
		return "", concern_type.Empty, ErrTypeNotSupported
	}
	return site, concern_type.Type(_type), nil
}

// GetConcernByParseSite 尝试解析string格式的 site，默认为前缀匹配模式。
func GetConcernByParseSite(rawSite string) (Concern, error) {
	site, err := ParseRawSite(rawSite)
	if err != nil {
		return nil, err
	}
	return GetConcernBySite(site)
}

// GetConcernByParseSiteAndType 尝试解析string格式的 site 和 ctype，可以安全处理用户输入的site和ctype，
// 并返回 Concern，site，ctype。
// 默认为前缀匹配模式
func GetConcernByParseSiteAndType(rawSite, rawType string) (Concern, string, concern_type.Type, error) {
	site, ctypes, err := ParseRawSiteAndType(rawSite, rawType)
	if err != nil {
		return nil, "", concern_type.Empty, err
	}
	cm, _ := GetConcernBySiteAndType(site, ctypes)
	return cm, site, ctypes, nil
}
