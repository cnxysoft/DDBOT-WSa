package lsp

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	jsoniter "github.com/json-iterator/go"
)

// atAllRemainingThresholdDefault 是 atAll.remainingThreshold 未配置时的默认阈值。
const atAllRemainingThresholdDefault int64 = 10

// atAllRemainingCooldown 是同群剩余次数查询结果的缓存时间，
// 避免 @全体 决策对 OneBot 的 get_group_at_all_remain 造成压力。
const atAllRemainingCooldown = 30 * time.Second

// atAllNotify 是 @全体 决策所需的最小通知接口。
// 成品 checkAtAllRemaining 的入参只有一个通知接口（itab 偏移 0x18=GetGroupCode、
// 0x20=GetUid、0x30=Site），因此这里只声明这三个方法。
type atAllNotify interface {
	GetGroupCode() int64
	GetUid() interface{}
	Site() string
}

// atAllRemainingEntry 记录一次剩余次数查询的结果与时间。
type atAllRemainingEntry struct {
	count     int64
	queriedAt time.Time
	invalid   bool
}

var (
	atAllRemainingMu    sync.Mutex
	atAllRemainingCache = make(map[int64]atAllRemainingEntry)
)

// atAllRemainingDecision 是 checkAtAllRemaining 的返回结构。
//
// 成品证据（notify.go:119 调用点 @cc34b8）：该函数返回 56 字节结构体，
// ConcernNotify 按偏移取字段打日志：
//   - 0x180 allowed（bool）
//   - 0x181 remaining_limiter_enabled（bool）
//   - 0x182 whitelisted（bool）
//   - 0x188 threshold（int64）
//   - 0x190 *remaining（*int64，查询失败时为 nil）
//   - 0x198/0x1a0 reason（string）
//   - 0x1a8 error（error）
//
// 分层（成品 at_all_limiter.go:29/42/64）：
//
//	checkAtAllRemaining -> decideAtAllWithLimiter -> decideAtAll
//
// 本层不再写任何日志，所有文案由 ConcernNotify 按 at_all_stage 输出。
type atAllRemainingDecision struct {
	Allowed                 bool
	RemainingLimiterEnabled bool
	Whitelisted             bool
	Threshold               int64
	Remaining               *int64
	Reason                  string
	Err                     error
}

// checkAtAllRemaining 是通知 @全体 的决策总入口：读取 limiter 开关与白名单，
// 再交给 decideAtAllWithLimiter 分层判定。
//
// 成品证据（at_all_limiter.go:29，608B）：E8 callees =
// cfg.IsAtAllRemainingLimiterEnabled / GetAtAllRemainingThreshold /
// GetAtAllBilibiliUIDWhitelist / GetAtAllDouyinUIDWhitelist + decideAtAllWithLimiter；
// 入参是通知接口，直接调用 GetGroupCode(itab+0x18) / GetUid(+0x20) / Site(+0x30)。
func checkAtAllRemaining(inotify atAllNotify) atAllRemainingDecision {
	if inotify == nil {
		return atAllRemainingDecision{}
	}
	// 与成品一致的读取顺序：limiter 开关 -> 通知字段 -> 阈值 -> 两份白名单。
	limiterEnabled := cfg.IsAtAllRemainingLimiterEnabled()
	site := inotify.Site()
	uid := inotify.GetUid()
	groupCode := inotify.GetGroupCode()
	threshold := cfg.GetAtAllRemainingThreshold()
	bilibiliWhitelist := cfg.GetAtAllBilibiliUIDWhitelist()
	douyinWhitelist := cfg.GetAtAllDouyinUIDWhitelist()
	return decideAtAllWithLimiter(groupCode, site, uid, limiterEnabled, threshold, bilibiliWhitelist, douyinWhitelist)
}

// decideAtAllWithLimiter 是 limiter 开关层：关闭时直接放行 @全体，启用时才进入
// decideAtAll 做白名单与剩余次数判定。
//
// 成品证据（at_all_limiter.go:42，608B）：唯一 E8 callee 是 decideAtAll；
// enabled=false 分支返回 allowed=true + remaining_limiter_enabled=false，
// reason="at_all_not_configured"（ConcernNotify 据此输出「未配置 atAll」）。
func decideAtAllWithLimiter(groupCode int64, site string, uid interface{}, limiterEnabled bool, threshold int64, bilibiliWhitelist []int64, douyinWhitelist []string) atAllRemainingDecision {
	if !limiterEnabled {
		// 完全不写 atAll 整块时保持基线行为：不启用剩余次数保护，也不调用 get_group_at_all_remain。
		return atAllRemainingDecision{
			Allowed:                 true,
			RemainingLimiterEnabled: false,
			Reason:                  "at_all_not_configured",
		}
	}
	return decideAtAll(groupCode, site, uid, threshold, bilibiliWhitelist, douyinWhitelist)
}

// decideAtAll 执行白名单判定与剩余次数阈值判定。
//
// 成品证据（at_all_limiter.go:64，1152B）：E8 callees 只有
// isBilibiliUIDWhitelisted / isDouyinUIDWhitelisted；白名单未命中时经全局
// funcval（.rodata @0x111d630，word0 = queryGroupAtAllRemainingCount）间接调用
// 剩余次数查询，再把返回的 int64 与阈值比较（cmp %rdx,%rax; jge）。
// reason 字面量：bilibili_uid_whitelist @0x10b7bd6 / douyin_uid_whitelist @0x10b4edb /
// remaining_enough @0x10af265 / remaining_below_threshold @0x10bc111 /
// remaining_query_failed @0x10b7bec。
//
// 说明：成品是把查询函数值一路传到 decideAtAll 再 call，这里直接调用重建的
// 带缓存实现 groupAtAllRemainingCount，行为等价。[推断]
func decideAtAll(groupCode int64, site string, uid interface{}, threshold int64, bilibiliWhitelist []int64, douyinWhitelist []string) atAllRemainingDecision {
	decision := atAllRemainingDecision{
		RemainingLimiterEnabled: true,
		Threshold:               threshold,
	}
	if isBilibiliUIDWhitelisted(site, uid, bilibiliWhitelist) {
		decision.Allowed = true
		decision.Whitelisted = true
		decision.Reason = "bilibili_uid_whitelist"
		return decision
	}
	if isDouyinUIDWhitelisted(site, uid, douyinWhitelist) {
		decision.Allowed = true
		decision.Whitelisted = true
		decision.Reason = "douyin_uid_whitelist"
		return decision
	}

	remaining, err := groupAtAllRemainingCount(groupCode)
	if err != nil {
		// 腾讯剩余次数接口查询失败时宁可保守，禁止本次 @全体。
		decision.Allowed = false
		decision.Reason = "remaining_query_failed"
		decision.Err = err
		return decision
	}
	decision.Remaining = &remaining
	if remaining < threshold {
		decision.Allowed = false
		decision.Reason = "remaining_below_threshold"
		return decision
	}
	decision.Allowed = true
	decision.Reason = "remaining_enough"
	return decision
}

// isBilibiliUIDWhitelisted 判断通知是否来自白名单内的 B站主播。
//
// 成品证据（at_all_limiter.go:109，256B）：
//   - 先 strings.EqualFold(site, "bilibili")（字面量 @0x10a48b6，len 8），
//     因此非 bilibili 的 site 直接返回 false；
//   - 再 interfaceToInt64(uid)，与白名单切片逐项做 8 字节（int64）比较。
//
// 成品证据（cfg.GetAtAllBilibiliUIDWhitelist @0xae82e0 用 makeslice(type = int64
// @0xee72c0) 返回 []int64，已去空白、去重、仅正数）：本函数逐项按 8 字节
// int64 直接比较（@0xc8fa7e: mov (%rdx,%rbx,8),%rsi; cmp %rax,%rsi）。
func isBilibiliUIDWhitelisted(site string, uid interface{}, whitelist []int64) bool {
	if !strings.EqualFold(site, "bilibili") {
		return false
	}
	id, ok := interfaceToInt64(uid)
	if !ok {
		return false
	}
	for _, item := range whitelist {
		if item == id {
			return true
		}
	}
	return false
}

// isDouyinUIDWhitelisted 判断通知是否来自白名单内的抖音主播。
//
// 成品证据（at_all_limiter.go:126，256B）：
//   - 先 strings.EqualFold(site, "douyin")（字面量 @0x109f5ec，len 6）；
//   - 再要求 uid 的动态类型是 string（type descriptor @0xee7100），TrimSpace 后
//     与 []string 白名单逐项按长度 + memequal 比较。
func isDouyinUIDWhitelisted(site string, uid interface{}, whitelist []string) bool {
	if !strings.EqualFold(site, "douyin") {
		return false
	}
	id, ok := uid.(string)
	if !ok {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, item := range whitelist {
		// 白名单已由 cfg.GetAtAllDouyinUIDWhitelist 去过首尾空白并去重，成品循环不再 TrimSpace。
		if item == id {
			return true
		}
	}
	return false
}

// queryGroupAtAllRemainingCount 调用 get_group_at_all_remain 查询群剩余 @全体 次数。
//
// 注意（report 18 复核）：成品里它 E8 调用者确实为 0，但并非死代码——
// decideAtAll 通过全局 funcval（.rodata @0x111d630）间接调用它，因此
// cg.py 的 E8 扫描看不到调用点。两个函数都必须保留，不能删除。
// 另外成品命名与重建相反：成品 decideAtAll -> queryGroupAtAllRemainingCount
// （薄包装，构造传输闭包）-> groupAtAllRemainingCount（缓存+协议实现），
// 重建则是 decideAtAll -> groupAtAllRemainingCount（缓存）-> queryGroupAtAllRemainingCount
// （直接发 API）；调用结果等价，仅角色命名相反。[推断]
func queryGroupAtAllRemainingCount(groupCode int64) (int64, error) {
	resp, err := localutils.GetBot().SendApi("get_group_at_all_remain", map[string]interface{}{
		"group_id": groupCode,
	})
	if err != nil {
		return 0, err
	}
	data, ok := resp.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("get_group_at_all_remain returned unexpected data type %T", resp)
	}
	// 兼容 count 与 remain_at_all_count 两种字段名
	if count, ok := interfaceToInt64(data["count"]); ok {
		return count, nil
	}
	if count, ok := interfaceToInt64(data["remain_at_all_count"]); ok {
		return count, nil
	}
	if count, ok := interfaceToInt64(data["remain_at_all_count_for_uin"]); ok {
		return count, nil
	}
	return 0, fmt.Errorf("get_group_at_all_remain response missing count field")
}

// groupAtAllRemainingCount 带冷却缓存的剩余次数查询：同群在冷却期内复用上次结果。
// 查询失败同样计入冷却，避免对失败接口反复重试。
// 本层不写日志，失败信息通过返回的 error 由 ConcernNotify 统一输出。
func groupAtAllRemainingCount(groupCode int64) (int64, error) {
	atAllRemainingMu.Lock()
	entry, found := atAllRemainingCache[groupCode]
	atAllRemainingMu.Unlock()
	if found && time.Since(entry.queriedAt) < atAllRemainingCooldown {
		if entry.invalid {
			return 0, fmt.Errorf("group %d at all remaining in cooldown after failed query", groupCode)
		}
		return entry.count, nil
	}

	count, err := queryGroupAtAllRemainingCount(groupCode)
	atAllRemainingMu.Lock()
	if err != nil {
		atAllRemainingCache[groupCode] = atAllRemainingEntry{
			count:     0,
			queriedAt: time.Now(),
			invalid:   true,
		}
	} else {
		atAllRemainingCache[groupCode] = atAllRemainingEntry{
			count:     count,
			queriedAt: time.Now(),
		}
	}
	atAllRemainingMu.Unlock()
	return count, err
}

// interfaceToInt64 尽力把模板/API 返回的动态值转换为 int64。
// JSON 反序列化出来的数字一律是 float64，因此 float 走 floatToInt64 处理。
func interfaceToInt64(v interface{}) (int64, bool) {
	switch val := v.(type) {
	case nil:
		return 0, false
	case int:
		return int64(val), true
	case int8:
		return int64(val), true
	case int16:
		return int64(val), true
	case int32:
		return int64(val), true
	case int64:
		return val, true
	case uint:
		return int64(val), true
	case uint8:
		return int64(val), true
	case uint16:
		return int64(val), true
	case uint32:
		return int64(val), true
	case uint64:
		return int64(val), true
	case float32:
		return floatToInt64(float64(val)), true
	case float64:
		return floatToInt64(val), true
	case jsoniter.Number:
		if i, err := val.Int64(); err == nil {
			return i, true
		}
		f, err := val.Float64()
		if err != nil {
			return 0, false
		}
		return floatToInt64(f), true
	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return floatToInt64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// floatToInt64 把浮点数安全地截断为 int64，NaN/Inf 一律回退为 0。
func floatToInt64(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f >= math.MaxInt64 {
		return math.MaxInt64
	}
	if f <= math.MinInt64 {
		return math.MinInt64
	}
	return int64(f)
}
