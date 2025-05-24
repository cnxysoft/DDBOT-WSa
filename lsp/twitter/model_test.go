package twitter_test

import (
	"math"
	"strconv"
	"testing"
	"time"
)

// 假设这些常量在代码中已定义
const (
	timestampLeftShift = 22
	epoch              = 1288834974657 // Snowflake的起始时间戳（毫秒）
)

// 被测函数（假设已定义）
func ParseSnowflakeTimestamp(id string) (time.Time, error) {
	Id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	timestamp := (Id >> timestampLeftShift) + epoch
	return time.UnixMilli(timestamp), nil
}

// 单元测试
func TestParseSnowflakeTimestamp(t *testing.T) {
	testCases := []struct {
		name     string
		id       string
		expected time.Time
		err      bool
	}{
		{
			name:     "有效ID",
			id:       strconv.FormatInt(1000<<timestampLeftShift, 10),
			expected: time.UnixMilli(epoch + 1000),
			err:      false,
		},
		{
			name:     "非数字字符串",
			id:       "abc",
			expected: time.Time{},
			err:      true,
		},
		{
			name:     "超出int64范围",
			id:       "9223372036854775808",
			expected: time.Time{},
			err:      true,
		},
		{
			name:     "最小ID",
			id:       "0",
			expected: time.UnixMilli(epoch),
			err:      false,
		},
		{
			name:     "最大ID",
			id:       strconv.FormatInt(math.MaxInt64, 10),
			expected: time.UnixMilli((math.MaxInt64 >> timestampLeftShift) + epoch),
			err:      false,
		},
		{
			name:     "正常ID",
			id:       "1908619999121727828",
			expected: time.UnixMilli((1908619999121727828 >> timestampLeftShift) + epoch),
			err:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := ParseSnowflakeTimestamp(tc.id)
			if (err != nil) != tc.err {
				t.Errorf("测试用例 %s: 预期错误 %v，实际错误 %v", tc.name, tc.err, err)
			}
			if !actual.Equal(tc.expected) {
				t.Errorf("测试用例 %s: 预期时间 %v，实际时间 %v", tc.name, tc.expected, actual)
			}
			t.Logf("测试用例 %s: 预期时间 %v，实际时间 %v", tc.name, tc.expected, actual)
		})
	}
}
