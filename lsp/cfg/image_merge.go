package cfg

import (
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
)

type ImageMergeConfig struct {
	Mode   string
	Layout string
	// LegacyBilibili restricts the implicit Bilibili default to its original
	// dynamic gallery. Explicit configuration applies to all image runs.
	LegacyBilibili bool
}

// GetImageMergeConfig resolves each option as global > site > legacy default.
// Read on every render so WatchConfig changes do not get stuck in sync.Once.
func GetImageMergeConfig(site string) ImageMergeConfig {
	c := ImageMergeConfig{Mode: "off", Layout: "grid", LegacyBilibili: site == "bilibili"}
	if site == "bilibili" {
		c.Mode = "auto"
	}
	if config.GlobalConfig == nil {
		return c
	}
	resolve := func(option string) string {
		for _, key := range []string{"imageMerge." + option, site + ".imageMerge." + option} {
			if site == "" && strings.HasPrefix(key, ".") {
				continue
			}
			value := strings.ToLower(strings.TrimSpace(config.GlobalConfig.GetString(key)))
			if value != "" && value != "inherit" {
				c.LegacyBilibili = false
				return value
			}
		}
		return ""
	}
	mode := resolve("mode")
	if mode == "" && site == "bilibili" {
		mode = strings.ToLower(config.GlobalConfig.GetString("bilibili.imageMergeMode"))
	}
	switch mode {
	case "auto", "only9", "always", "off":
		c.Mode = mode
	case "false":
		c.Mode = "off"
	case "true":
		c.Mode = "always"
	case "", "inherit":
	default:
		logger.Warnf("invalid image merge mode %q for %s, using %s", mode, site, c.Mode)
	}
	switch layout := resolve("layout"); layout {
	case "grid", "long":
		c.Layout = layout
	case "":
	default:
		logger.Warnf("invalid image merge layout %q for %s, using grid", layout, site)
	}
	return c
}
