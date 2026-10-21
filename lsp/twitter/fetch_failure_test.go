package twitter

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/stretchr/testify/assert"
)

// stubTwitterRecoveryState 隔离恢复循环与阈值，测试结束后复原。
// 退避设为一小时：循环体不会在测试期间真正跑起来，避免干扰断言。
func stubTwitterRecoveryState(t *testing.T) {
	t.Helper()
	originalVerify := twitterVerifyFunc
	originalBackoff := twitterRecoveryBackoff
	originalAuthThreshold := twitterAuthFailThreshold
	originalFetchThreshold := twitterFetchFailThreshold
	originalMinDuration := twitterFetchFailMinDuration

	twitterVerifyFunc = func() (string, string, error) { return "", "", assert.AnError }
	twitterRecoveryBackoff = []time.Duration{time.Hour}

	t.Cleanup(func() {
		twitterVerifyFunc = originalVerify
		twitterRecoveryBackoff = originalBackoff
		twitterAuthFailThreshold = originalAuthThreshold
		twitterFetchFailThreshold = originalFetchThreshold
		twitterFetchFailMinDuration = originalMinDuration
		twitterRecovering.Store(false)
		twitterRecoveringKind.Store(0)
		resetTwitterFetchFailures()
	})
}

// TestRecordTwitterFetchResultAuthFailureAlertsAsCookie：401/403 属于登录态失效，
// 少量次数就要告警，且归为 Cookie 故障（twitterIncidentAuth）。
func TestRecordTwitterFetchResultAuthFailureAlertsAsCookie(t *testing.T) {
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool { return true })
	stubTwitterRecoveryState(t)
	twitterAuthFailThreshold = 2

	authErr := classifyTwitterHTTPError(http.StatusUnauthorized, errors.New("bad response"))
	recordTwitterFetchResult(authErr)
	assert.False(t, twitterRecovering.Load(), "首次失败不应立即告警")

	recordTwitterFetchResult(authErr)
	assert.True(t, twitterRecovering.Load())
	assert.EqualValues(t, int32(twitterIncidentAuth), twitterRecoveringKind.Load())
}

// TestRecordTwitterFetchResultParseErrorIsNotCookie：线上真实现象——响应无法解码，
// 旧实现把它算成 Cookie 失效并每 100 分钟推一次告警。现在应归为普通抓取失败：
// 既要达到次数阈值，也要持续足够久，且告警类型不是 Cookie。
func TestRecordTwitterFetchResultParseErrorIsNotCookie(t *testing.T) {
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool { return true })
	stubTwitterRecoveryState(t)
	twitterFetchFailThreshold = 3
	twitterFetchFailMinDuration = time.Hour

	parseErr := errors.New("failed to parse response: invalid character '\\u0083' looking for beginning of value")
	for range 5 {
		recordTwitterFetchResult(parseErr)
	}
	assert.False(t, twitterRecovering.Load(), "持续时间不足时不应告警")

	twitterFetchFailMinDuration = 0
	recordTwitterFetchResult(parseErr)
	assert.True(t, twitterRecovering.Load())
	assert.EqualValues(t, int32(twitterIncidentFetch), twitterRecoveringKind.Load(),
		"解析失败不能归类为 Cookie 失效")
}

// TestRecordTwitterFetchResultSuccessResetsBothCounters：任意一次成功都要清空计数与起始时间。
func TestRecordTwitterFetchResultSuccessResetsBothCounters(t *testing.T) {
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool { return true })
	stubTwitterRecoveryState(t)

	recordTwitterFetchResult(classifyTwitterHTTPError(http.StatusForbidden, assert.AnError))
	recordTwitterFetchResult(errors.New("network"))
	assert.Positive(t, twitterAuthFailures.Load())
	assert.Positive(t, twitterFetchFailures.Load())

	recordTwitterFetchResult(nil)
	assert.Zero(t, twitterAuthFailures.Load())
	assert.Zero(t, twitterFetchFailures.Load())
	assert.Zero(t, twitterFetchFailingSince.Load())
}

func TestNotifyTwitterIncidentMessagesByKind(t *testing.T) {
	authMsg := newTwitterIncidentAlertMessage(twitterIncidentAuth, "登录态失效")
	assert.Contains(t, msgstringer.AdapterMsgToString(authMsg.Elements()), "Cookie失效预警")

	fetchMsg := newTwitterIncidentAlertMessage(twitterIncidentFetch, "响应无法解析")
	assert.Contains(t, msgstringer.AdapterMsgToString(fetchMsg.Elements()), "Twitter抓取异常")
	assert.NotContains(t, msgstringer.AdapterMsgToString(fetchMsg.Elements()), "Cookie失效")

	recovered := newTwitterRecoveredMessage(twitterIncidentFetch, time.Minute)
	assert.Contains(t, msgstringer.AdapterMsgToString(recovered.Elements()), "Twitter抓取已恢复")
}
