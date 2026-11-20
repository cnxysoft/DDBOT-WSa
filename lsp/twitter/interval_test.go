package twitter

import (
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/stretchr/testify/assert"
)

// TestParseInterval 覆盖 twitter.interval 的解析语义。
// 重点：无单位数字必须按「秒」解释——viper 的 GetDuration 会把它当纳秒，
// 配置 twitter.interval: 60 会变成 60ns，等于把限流彻底取消。
func TestParseInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
		want time.Duration
	}{
		{"未配置", nil, 0},
		{"空字符串", "", 0},
		{"带单位秒", "120s", 120 * time.Second},
		{"带单位分钟", "2m", 2 * time.Minute},
		{"纯数字字符串按秒", "60", 60 * time.Second},
		{"纯数字字符串带小数按秒", "1.5", 1500 * time.Millisecond},
		{"int 按秒", 60, 60 * time.Second},
		{"int64 按秒", int64(90), 90 * time.Second},
		{"float64 按秒", float64(2.5), 2500 * time.Millisecond},
		{"零值视为未配置", 0, 0},
		{"负数视为未配置", -5, 0},
		{"负时长视为未配置", "-5s", 0},
		{"无法解析", "abc", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseInterval(tt.raw))
		})
	}
}

// TestGetRefreshIntervalDefault 未配置 twitter.interval 时使用默认 120s。
// 注意：显式配置低于 120s 的值时不再钳制，只告警——该行为由 getRefreshInterval
// 内的分支保证，这里只锁定默认值这一条不变式。
func TestGetRefreshIntervalDefault(t *testing.T) {
	assert.Nil(t, config.GlobalConfig.Get("twitter.interval"), "测试前提：测试环境未配置 twitter.interval")
	assert.Equal(t, 120*time.Second, getRefreshInterval())
}
