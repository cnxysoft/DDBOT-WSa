package youtube

import (
	"fmt"
	"strconv"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

const (
	// YtVideo represents regular YouTube video type
	YtVideo = "video"
	// YtShorts represents YouTube Shorts type
	YtShorts = "shorts"
	// YtLive matches any live status (live streaming or waiting/premiere).
	YtLive = "live"
	// YtFirstLive matches premiere/waiting livestreams only.
	YtFirstLive = "firstlive"
)

var PredefinedType = map[string][]VideoType{
	YtVideo:     {VideoType_Video},
	YtShorts:    {VideoType_Shorts},
	YtLive:      {VideoType_Live, VideoType_FirstLive},
	YtFirstLive: {VideoType_FirstLive},
}

type GroupConcernConfig struct {
	concern.IConfig
}

func (g *GroupConcernConfig) ShouldSendHook(notify concern.Notify) *concern.HookResult {
	if c, ok := notify.(*ConcernNotify); ok {
		// 直播预告也应该推送
		if c.IsWaiting() {
			return concern.HookResultPass
		}
	}
	return g.IConfig.ShouldSendHook(notify)
}

// Validate checks that only valid type filters are configured
func (g *GroupConcernConfig) Validate() error {
	for _, rule := range g.GetGroupConcernFilter().RulesNormalized() {
		switch rule.Type {
		case concern.FilterTypeNotType, concern.FilterTypeType:
			typeFilter, err := rule.GetFilterByType()
			if err != nil {
				return err
			}
			invalid := CheckTypeDefine(typeFilter.Type)
			if len(invalid) != 0 {
				return fmt.Errorf("invalid youtube type filter: %v (known: %v)",
					invalid, knownTypeNames())
			}
		case concern.FilterTypeText, concern.FilterTypeNotText:
			// base type supports text, nothing to validate
		default:
			return concern.ErrConfigNotSupported
		}
	}
	return g.GetGroupConcernFilter().ValidateTextConflict()
}

// knownTypeNames returns the user-facing names accepted by type filters.
// Keep in sync with PredefinedType and the VideoType enum.
func knownTypeNames() []string {
	names := make([]string, 0, len(PredefinedType))
	for k := range PredefinedType {
		names = append(names, k)
	}
	return names
}

// FilterHook filters notifications based on video type (video/shorts)
func (g *GroupConcernConfig) FilterHook(notify concern.Notify) *concern.HookResult {
	hook := new(concern.HookResult)

	switch n := notify.(type) {
	case *ConcernNotify:
		// If no filter configured, pass
		if g.GetGroupConcernFilter().Empty() {
			hook.Pass = true
			return hook
		}

		for _, rule := range g.GetGroupConcernFilter().RulesNormalized() {
			switch rule.Type {
			case concern.FilterTypeType, concern.FilterTypeNotType:
				typeFilter, err := rule.GetFilterByType()
				if err != nil {
					continue
				}

				var convTypes []VideoType
				for _, tp := range typeFilter.Type {
					if types, ok := PredefinedType[tp]; ok {
						convTypes = append(convTypes, types...)
					} else {
						// Try parsing as int32
						if t, err := strconv.ParseInt(tp, 10, 32); err == nil {
							convTypes = append(convTypes, VideoType(t))
						}
					}
				}

				var ok bool
				switch rule.Type {
				case concern.FilterTypeType:
					ok = false
					for _, tp := range convTypes {
						if n.VideoType == tp {
							ok = true
							break
						}
					}
				case concern.FilterTypeNotType:
					ok = true
					for _, tp := range convTypes {
						if n.VideoType == tp {
							ok = false
							break
						}
					}
				}

				if !ok {
					hook.Reason = "filtered by TypeFilter"
					return hook
				}
			case concern.FilterTypeText, concern.FilterTypeNotText:
				// Text filtering handled below via base config
				continue
			}
		}

		// Delegate text filtering to base config
		return g.IConfig.FilterHook(notify)
	default:
		hook.Pass = true
		return hook
	}
}

func NewGroupConcernConfig(g concern.IConfig) *GroupConcernConfig {
	return &GroupConcernConfig{g}
}

// CheckTypeDefine validates that all type names are defined
func CheckTypeDefine(types []string) (invalid []string) {
	for _, t := range types {
		if PredefinedType[t] != nil {
			continue
		}
		// Try parsing as int32 VideoType
		if tp, err := strconv.ParseInt(t, 10, 32); err == nil {
			if tp >= 0 && tp <= int64(VideoType_Shorts) {
				continue
			}
		}
		invalid = append(invalid, t)
	}
	return
}
