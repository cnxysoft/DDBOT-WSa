package cfg

import (
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

type imageBudgetBot struct {
	adapter.BotCaller
	limit int
}

func (b *imageBudgetBot) GeneratedImageOutputLimitBytes() int { return b.limit }

func TestImageMergeOptionsUseRunningAdapterBudget(t *testing.T) {
	oldBot := utils.GetBot().Bot
	oldConfig := config.GlobalConfig
	t.Cleanup(func() { utils.GetBot().Bot = oldBot; config.GlobalConfig = oldConfig })
	config.GlobalConfig = &config.Config{Viper: viper.New()}
	utils.GetBot().Bot = &imageBudgetBot{limit: 5*1024*1024 + 1}
	assert.Equal(t, 5*1024*1024+1, GetImageMergeConfig("twitter").Options().MaxBytes)
	config.GlobalConfig.Set("adapter.mode", "onebot-v11")
	assert.Equal(t, 5*1024*1024+1, GetImageMergeConfig("twitter").Options().MaxBytes,
		"changing mode without restarting must not bypass the active connection's limit")
	utils.GetBot().Bot = nil
	assert.Zero(t, GetImageMergeConfig("twitter").Options().MaxBytes)
}

func TestImageMergeConfigurationPrecedenceAndReload(t *testing.T) {
	old := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = old })
	v := viper.New()
	config.GlobalConfig = &config.Config{Viper: v}
	expected := func(mode, layout string, legacy bool) ImageMergeConfig {
		return ImageMergeConfig{Mode: mode, Layout: layout, MaxWidth: 1440, MaxHeight: 16000, LegacyBilibili: legacy}
	}
	assert.Equal(t, expected("auto", "grid", true), GetImageMergeConfig("bilibili"))
	assert.Equal(t, expected("off", "grid", false), GetImageMergeConfig("twitter"))
	v.Set("bilibili.imageMergeMode", "only9")
	assert.Equal(t, expected("only9", "grid", true), GetImageMergeConfig("bilibili"))
	v.Set("twitter.imageMerge.mode", "always")
	v.Set("twitter.imageMerge.layout", "long")
	assert.Equal(t, expected("always", "long", false), GetImageMergeConfig("twitter"))
	v.Set("imageMerge.mode", "off")
	assert.Equal(t, "off", GetImageMergeConfig("twitter").Mode)
	assert.Equal(t, "off", GetImageMergeConfig("bilibili").Mode)
	v.Set("imageMerge.mode", "inherit")
	v.Set("imageMerge.layout", "grid")
	assert.Equal(t, expected("always", "grid", false), GetImageMergeConfig("twitter"))
	v.Set("imageMerge.layout", "inherit")
	assert.Equal(t, "long", GetImageMergeConfig("twitter").Layout)
	v.Set("bilibili.imageMerge.mode", "always")
	assert.Equal(t, expected("always", "grid", false), GetImageMergeConfig("bilibili"))
	config.GlobalConfig = nil
	assert.Equal(t, "auto", GetImageMergeConfig("bilibili").Mode)
}

func TestImageMergeDimensionOverridesAndBounds(t *testing.T) {
	old := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = old })
	v := viper.New()
	config.GlobalConfig = &config.Config{Viper: v}
	v.Set("twitter.imageMerge.maxWidth", 2000)
	v.Set("twitter.imageMerge.maxHeight", 28000)
	c := GetImageMergeConfig("twitter")
	assert.Equal(t, 2000, c.MaxWidth)
	assert.Equal(t, 28000, c.MaxHeight)
	assert.Equal(t, "off", c.Mode, "sizing does not enable merging")
	v.Set("imageMerge.maxWidth", 1000)
	v.Set("imageMerge.maxHeight", 29999)
	c = GetImageMergeConfig("twitter")
	assert.Equal(t, 1000, c.Options().Width)
	assert.Equal(t, 29999, c.Options().MaxHeight)
	v.Set("imageMerge.maxWidth", 0)
	v.Set("imageMerge.maxHeight", "inherit")
	assert.Equal(t, 2000, GetImageMergeConfig("twitter").MaxWidth)
	assert.Equal(t, 28000, GetImageMergeConfig("twitter").MaxHeight)
	v.Set("imageMerge.maxWidth", 100000)
	v.Set("imageMerge.maxHeight", 30000)
	assert.Equal(t, 29999, GetImageMergeConfig("twitter").MaxWidth)
	assert.Equal(t, 29999, GetImageMergeConfig("twitter").MaxHeight)
	v.Set("imageMerge.maxWidth", -1)
	v.Set("imageMerge.maxHeight", "bad")
	assert.Equal(t, 1440, GetImageMergeConfig("twitter").MaxWidth)
	assert.Equal(t, 16000, GetImageMergeConfig("twitter").MaxHeight)
	v.Set("imageMerge.maxWidth", 720)
	v.Set("imageMerge.maxHeight", 28000)
	assert.True(t, GetImageMergeConfig("bilibili").LegacyBilibili)
}
