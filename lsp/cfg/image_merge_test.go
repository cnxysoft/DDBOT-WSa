package cfg

import (
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestImageMergeConfigurationPrecedenceAndReload(t *testing.T) {
	old := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = old })
	v := viper.New()
	config.GlobalConfig = &config.Config{Viper: v}
	assert.Equal(t, ImageMergeConfig{"auto", "grid", true}, GetImageMergeConfig("bilibili"))
	assert.Equal(t, ImageMergeConfig{"off", "grid", false}, GetImageMergeConfig("twitter"))
	v.Set("bilibili.imageMergeMode", "only9")
	assert.Equal(t, ImageMergeConfig{"only9", "grid", true}, GetImageMergeConfig("bilibili"))
	v.Set("twitter.imageMerge.mode", "always")
	v.Set("twitter.imageMerge.layout", "long")
	assert.Equal(t, ImageMergeConfig{"always", "long", false}, GetImageMergeConfig("twitter"))
	v.Set("imageMerge.mode", "off")
	assert.Equal(t, "off", GetImageMergeConfig("twitter").Mode)
	assert.Equal(t, "off", GetImageMergeConfig("bilibili").Mode)
	v.Set("imageMerge.mode", "inherit")
	v.Set("imageMerge.layout", "grid")
	assert.Equal(t, ImageMergeConfig{"always", "grid", false}, GetImageMergeConfig("twitter"))
	v.Set("imageMerge.layout", "inherit")
	assert.Equal(t, "long", GetImageMergeConfig("twitter").Layout)
	v.Set("bilibili.imageMerge.mode", "always")
	assert.Equal(t, ImageMergeConfig{"always", "grid", false}, GetImageMergeConfig("bilibili"))
	config.GlobalConfig = nil
	assert.Equal(t, "auto", GetImageMergeConfig("bilibili").Mode)
}
