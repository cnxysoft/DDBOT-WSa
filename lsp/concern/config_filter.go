package concern

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	FilterTypeType    = "type"
	FilterTypeNotType = "not_type"
	FilterTypeText    = "text"
	FilterTypeNotText = "not_text"
)

type GroupConcernFilterConfigByType struct {
	Type []string `json:"type"`
}

func (g *GroupConcernFilterConfigByType) ToString() string {
	b, _ := json.Marshal(g)
	return string(b)
}

type GroupConcernFilterConfigByText struct {
	Text []string `json:"text"`
}

func (g *GroupConcernFilterConfigByText) ToString() string {
	b, _ := json.Marshal(g)
	return string(b)
}

// GroupConcernFilterRule 单条过滤规则
type GroupConcernFilterRule struct {
	Type   string `json:"type"`
	Config string `json:"config"`
}

func (g GroupConcernFilterRule) GetFilterByType() (*GroupConcernFilterConfigByType, error) {
	if g.Type != FilterTypeType && g.Type != FilterTypeNotType {
		return nil, errors.New("filter type mismatched")
	}
	var result = new(GroupConcernFilterConfigByType)
	err := json.Unmarshal([]byte(g.Config), result)
	return result, err
}

func (g GroupConcernFilterRule) GetFilterByText() (*GroupConcernFilterConfigByText, error) {
	if g.Type != FilterTypeText && g.Type != FilterTypeNotText {
		return nil, errors.New("filter type mismatched")
	}
	var result = new(GroupConcernFilterConfigByText)
	err := json.Unmarshal([]byte(g.Config), result)
	return result, err
}

// GroupConcernFilterConfig 过滤器配置，兼容旧版单条规则，同时支持多条规则
type GroupConcernFilterConfig struct {
	// legacy 字段，兼容老版本存储
	Type   string `json:"type"`
	Config string `json:"config"`
	// 新版多规则
	Rules []GroupConcernFilterRule `json:"rules"`
}

// ensureRulesFromLegacy 将旧版单规则数据迁移到 Rules 中，保持向前兼容
func (g *GroupConcernFilterConfig) ensureRulesFromLegacy() {
	if g.Type == "" && g.Config == "" {
		// legacy字段缺失或为空，视为无规则并清空
		g.Rules = nil
		return
	}
	if len(g.Rules) == 0 {
		// legacy 存在但规则为空，迁移到 Rules
		g.Rules = append(g.Rules, GroupConcernFilterRule{
			Type:   g.Type,
			Config: g.Config,
		})
		return
	}
	// 如果已有规则但 legacy 有值，更新首条保持兼容
	g.Rules[0].Type = g.Type
	g.Rules[0].Config = g.Config
}

// syncLegacyFields 用于在保存时把第一条规则同步到旧字段，兼容旧结构的读取
func (g *GroupConcernFilterConfig) syncLegacyFields() {
	if len(g.Rules) == 0 {
		g.Type = ""
		g.Config = ""
		return
	}
	g.Type = g.Rules[0].Type
	g.Config = g.Rules[0].Config
}

func (g *GroupConcernFilterConfig) Empty() bool {
	g.ensureRulesFromLegacy()
	return len(g.Rules) == 0
}

// RulesNormalized 返回保证包含Rules的规则集合
func (g *GroupConcernFilterConfig) RulesNormalized() []GroupConcernFilterRule {
	g.ensureRulesFromLegacy()
	return g.Rules
}

// SetRule 设置或替换指定类型的规则，并同步旧字段
func (g *GroupConcernFilterConfig) SetRule(ruleType, config string) {
	g.ensureRulesFromLegacy()
	for idx, r := range g.Rules {
		if r.Type == ruleType {
			g.Rules[idx].Config = config
			g.syncLegacyFields()
			return
		}
	}
	g.Rules = append(g.Rules, GroupConcernFilterRule{
		Type:   ruleType,
		Config: config,
	})
	g.syncLegacyFields()
}

// Clear 清空全部规则
func (g *GroupConcernFilterConfig) Clear() {
	g.Rules = nil
	g.Type = ""
	g.Config = ""
}

// GetFilterByType 获取首个类型过滤规则的配置
func (g *GroupConcernFilterConfig) GetFilterByType() (*GroupConcernFilterConfigByType, error) {
	g.ensureRulesFromLegacy()
	if len(g.Rules) == 0 {
		return nil, nil
	}
	for _, r := range g.Rules {
		if r.Type == FilterTypeType || r.Type == FilterTypeNotType {
			return r.GetFilterByType()
		}
	}
	// 存在规则但无类型过滤规则，返回空配置表示“当前有规则但非类型规则”
	return &GroupConcernFilterConfigByType{}, nil
}

// GetFilterByText 获取首个文本过滤规则的配置
func (g *GroupConcernFilterConfig) GetFilterByText() (*GroupConcernFilterConfigByText, error) {
	g.ensureRulesFromLegacy()
	if len(g.Rules) == 0 {
		return nil, nil
	}
	for _, r := range g.Rules {
		if r.Type == FilterTypeText || r.Type == FilterTypeNotText {
			return r.GetFilterByText()
		}
	}
	// 存在规则但无文本过滤规则，按照旧语义返回类型不匹配错误
	return nil, errors.New("filter type mismatched")
}

// ValidateTextConflict 检查 text 与 not_text 规则之间是否存在必然拦截全部推送的矛盾配置。
// FilterHook 对多条规则取 AND（每条都必须通过）、规则内关键词取 OR，
// 因此只要某条 text 规则的每个关键词都被某个 not_text 关键词覆盖
// （not_text 词是 text 词的子串，完全相同是特例），
// 则任何消息都不可能同时通过这两条规则，推送会被全部拦截。
// 返回 nil 表示无矛盾；返回非 nil 时应拒绝本次配置改动。
func (g *GroupConcernFilterConfig) ValidateTextConflict() error {
	var allowRules [][]string // 每条 text 规则的关键词（规则内 OR）
	var denyWords []string    // 全部 not_text 关键词
	for _, r := range g.RulesNormalized() {
		if r.Type != FilterTypeText && r.Type != FilterTypeNotText {
			continue
		}
		textFilter, err := r.GetFilterByText()
		if err != nil {
			return fmt.Errorf("解析过滤规则失败: %v", err)
		}
		if textFilter == nil || len(textFilter.Text) == 0 {
			return ErrFilterKeywordEmpty
		}
		for _, keyword := range textFilter.Text {
			if strings.TrimSpace(keyword) == "" {
				return ErrFilterKeywordEmpty
			}
		}
		if r.Type == FilterTypeText {
			allowRules = append(allowRules, textFilter.Text)
		} else {
			denyWords = append(denyWords, textFilter.Text...)
		}
	}
	if len(denyWords) == 0 {
		return nil
	}
	for _, allows := range allowRules {
		if len(allows) == 0 {
			continue
		}
		allDenied := true
		for _, a := range allows {
			covered := false
			for _, d := range denyWords {
				if d != "" && strings.Contains(a, d) {
					covered = true
					break
				}
			}
			if !covered {
				allDenied = false
				break
			}
		}
		if allDenied {
			return fmt.Errorf("%w：text 关键词 %v 均被 not_text 覆盖，将导致所有推送被过滤",
				ErrFilterRuleConflict, allows)
		}
	}
	return nil
}
