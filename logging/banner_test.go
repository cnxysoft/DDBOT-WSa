package logging

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

var testAnsiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestBanner_Plain 验证字符画：有 DDBOT 的轮廓、纯 ASCII、无颜色码。
func TestBanner_Plain(t *testing.T) {
	out := renderBanner(false)

	assert.Contains(t, out, "____")
	assert.NotContains(t, out, "\x1b[")
	assert.True(t, strings.HasSuffix(out, "\n\n"), "字符画后应留一个空行")

	for _, line := range bannerArt {
		for _, r := range line {
			assert.Less(t, int(r), 128, "字符画必须是纯 ASCII，避免 Windows 控制台错位：%q", line)
		}
	}
}

// TestBanner_Colors 验证着色版剥掉 ANSI 后与纯文本版完全一致。
func TestBanner_Colors(t *testing.T) {
	plain := renderBanner(false)
	colored := renderBanner(true)

	assert.Contains(t, colored, "\x1b[36m", "字符画应该是青色")
	assert.Equal(t, plain, testAnsiRe.ReplaceAllString(colored, ""))
}

// TestVersionLine 验证启动日志里的版本行：缺失项省略，全缺时兜底。
func TestVersionLine(t *testing.T) {
	line := VersionLine(BannerInfo{Version: "v1.2.3", Commit: "0123456789abcdef", BuildTime: "2026-11-15T00:00:00Z"})
	assert.Equal(t, "版本 v1.2.3 · 0123456 · 2026-11-15T00:00:00Z", line)

	assert.Equal(t, "版本未标注", VersionLine(BannerInfo{Version: "UNKNOWN", Commit: "UNKNOWN"}))
}

// TestVersion_Output 验证 --version 与改造前逐字一致（老脚本按行首字段取值）。
func TestVersion_Output(t *testing.T) {
	out := renderVersion(BannerInfo{Version: "v1.2.3", Commit: "abc1234", BuildTime: "2026-11-15"})
	assert.Equal(t, "Tags: v1.2.3\nCOMMIT_ID: abc1234\nBUILD_TIME: 2026-11-15\n", out)

	// 缺值兜底成 UNKNOWN，字段仍在行首，grep 不受影响
	out = renderVersion(BannerInfo{})
	assert.Equal(t, "Tags: UNKNOWN\nCOMMIT_ID: UNKNOWN\nBUILD_TIME: UNKNOWN\n", out)
	assert.NotContains(t, out, "\x1b[", "版本输出不应带颜色，避免污染脚本")
}
