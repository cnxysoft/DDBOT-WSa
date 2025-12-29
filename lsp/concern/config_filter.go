package concern

import (
	"encoding/json"
	"errors"
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
	if g.Type != "" && g.Config != "" {
		if len(g.Rules) == 0 {
			g.Rules = append(g.Rules, GroupConcernFilterRule{
				Type:   g.Type,
				Config: g.Config,
			})
		} else {
			g.Rules[0].Type = g.Type
			g.Rules[0].Config = g.Config
		}
	}
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
	for _, r := range g.Rules {
		if r.Type == FilterTypeType || r.Type == FilterTypeNotType {
			return r.GetFilterByType()
		}
	}
	return nil, nil
}

// GetFilterByText 获取首个文本过滤规则的配置
func (g *GroupConcernFilterConfig) GetFilterByText() (*GroupConcernFilterConfigByText, error) {
	g.ensureRulesFromLegacy()
	for _, r := range g.Rules {
		if r.Type == FilterTypeText || r.Type == FilterTypeNotText {
			return r.GetFilterByText()
		}
	}
	return nil, nil
}
