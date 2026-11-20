package cfg

import (
	"strconv"
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

type ImageMergeConfig struct {
	Mode                string
	Layout              string
	MaxWidth, MaxHeight int
	// LegacyBilibili restricts the implicit Bilibili default to its original
	// dynamic gallery. Explicit configuration applies to all image runs.
	LegacyBilibili bool
}

func (c ImageMergeConfig) Options() utils.ImageMergeOptions {
	o := utils.ImageMergeOptions{Layout: c.Layout, Width: c.MaxWidth, MaxHeight: c.MaxHeight}
	// Use the running adapter, so editing adapter.mode without restarting does
	// not change the upload budget of the active connection.
	if limiter, ok := utils.GetBot().Bot.(adapter.GeneratedImageOutputLimiter); ok {
		o.MaxBytes = limiter.GeneratedImageOutputLimitBytes()
	}
	return o
}

// GetImageMergeConfig resolves each option as global > site > legacy default.
// Read on every render so WatchConfig changes do not get stuck in sync.Once.
func GetImageMergeConfig(site string) ImageMergeConfig {
	c := ImageMergeConfig{Mode: "off", Layout: "grid", MaxWidth: utils.MergeImageWidth,
		MaxHeight: utils.MergeImageMaxHeight, LegacyBilibili: site == "bilibili"}
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
				return value
			}
		}
		return ""
	}
	mode := resolve("mode")
	if mode != "" {
		c.LegacyBilibili = false
	}
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
	layout := resolve("layout")
	if layout != "" {
		c.LegacyBilibili = false
	}
	switch layout {
	case "grid", "long":
		c.Layout = layout
	case "":
	default:
		logger.Warnf("invalid image merge layout %q for %s, using grid", layout, site)
	}
	// Zero, inherit and omitted dimensions defer to the site and then the
	// existing defaults. Sizing alone never broadens the legacy gallery policy.
	for option, target := range map[string]*int{"maxWidth": &c.MaxWidth, "maxHeight": &c.MaxHeight} {
		for _, prefix := range []string{"imageMerge.", site + ".imageMerge."} {
			if site == "" && strings.HasPrefix(prefix, ".") {
				continue
			}
			value := strings.ToLower(strings.TrimSpace(config.GlobalConfig.GetString(prefix + option)))
			if value == "" || value == "0" || value == "inherit" {
				continue
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				logger.Warnf("invalid image merge %s %q for %s, using %d", option, value, site, *target)
				break
			}
			if n > utils.MergeImageMaxDimension {
				logger.Warnf("image merge %s for %s capped at %d", option, site, utils.MergeImageMaxDimension)
			}
			*target = min(n, utils.MergeImageMaxDimension)
			break
		}
	}
	return c
}
