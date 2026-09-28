package twitter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/stretchr/testify/assert"
)

func setTwitterLoginAlertDependenciesForTest(t *testing.T, admins []int64, sender func(int64, *mmsg.MSG) bool) {
	t.Helper()
	originalListAdmins := listTwitterLoginAlertAdmins
	originalBotReady := twitterLoginAlertBotReady
	originalSender := sendTwitterLoginAlertToAdmin

	twitterLoginAlertState.reset()
	twitterLoginAlertSending.Store(false)
	listTwitterLoginAlertAdmins = func() []int64 {
		return append([]int64(nil), admins...)
	}
	twitterLoginAlertBotReady = func() bool { return true }
	sendTwitterLoginAlertToAdmin = sender

	t.Cleanup(func() {
		listTwitterLoginAlertAdmins = originalListAdmins
		twitterLoginAlertBotReady = originalBotReady
		sendTwitterLoginAlertToAdmin = originalSender
		twitterLoginAlertState.reset()
		twitterLoginAlertSending.Store(false)
	})
}

func TestNotifyTwitterLoginExpiredOnlyOnceAfterSuccess(t *testing.T) {
	var calls int
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool {
		calls++
		return true
	})

	notifyTwitterLoginExpired("测试失效")
	notifyTwitterLoginExpired("测试失效")

	assert.Equal(t, 1, calls)
}

func TestNotifyTwitterLoginExpiredOnlyOnceWhenConcurrent(t *testing.T) {
	var calls atomic.Int32
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool {
		calls.Add(1)
		return true
	})

	var waitGroup sync.WaitGroup
	for range 20 {
		waitGroup.Go(func() {
			notifyTwitterLoginExpired("测试失效")
		})
	}
	waitGroup.Wait()

	assert.EqualValues(t, 1, calls.Load())
}

func TestNotifyTwitterLoginExpiredRetriesOnlyFailedAdmins(t *testing.T) {
	calls := make(map[int64]int)
	setTwitterLoginAlertDependenciesForTest(t, []int64{1, 2}, func(qq int64, _ *mmsg.MSG) bool {
		calls[qq]++
		return qq == 1 || calls[qq] > 1
	})

	notifyTwitterLoginExpired("测试失效")
	notifyTwitterLoginExpired("测试失效")
	notifyTwitterLoginExpired("测试失效")

	assert.Equal(t, 1, calls[1])
	assert.Equal(t, 2, calls[2])
}

func TestNotifyTwitterLoginExpiredRetriesAfterBotRecovery(t *testing.T) {
	var calls int
	ready := false
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool {
		calls++
		return true
	})
	twitterLoginAlertBotReady = func() bool { return ready }

	notifyTwitterLoginExpired("测试失效")
	assert.Equal(t, 0, calls)

	ready = true
	notifyTwitterLoginExpired("测试失效")
	notifyTwitterLoginExpired("测试失效")
	assert.Equal(t, 1, calls)
}

func TestTwitterLoginAlertResetsAfterRecovery(t *testing.T) {
	var calls int
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool {
		calls++
		return true
	})

	notifyTwitterLoginExpired("测试失效")
	notifyTwitterLoginExpired("测试失效")
	assert.Equal(t, 1, calls)

	// 恢复时也会向管理员发一条恢复通知
	notifyTwitterLoginRecovered(time.Minute)
	assert.Equal(t, 2, calls)

	notifyTwitterLoginExpired("再次失效")
	assert.Equal(t, 3, calls)
}

func TestNotifyTwitterLoginRecoveredWithoutAlert(t *testing.T) {
	var calls int
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool {
		calls++
		return true
	})

	notifyTwitterLoginRecovered(time.Minute)
	assert.Equal(t, 0, calls)
}

func TestNotifyTwitterLoginRecoveredWithoutBot(t *testing.T) {
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool {
		t.Fatal("Bot未在线时不应发送恢复通知")
		return true
	})
	twitterLoginAlertBotReady = func() bool { return false }

	notifyTwitterLoginExpired("测试失效")
	notifyTwitterLoginRecovered(time.Minute)
}

func TestEnterTwitterRecoveringRunsOnceAndRecovers(t *testing.T) {
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool { return true })

	originalVerify := twitterVerifyFunc
	originalBackoff := twitterRecoveryBackoff
	originalRecovered := twitterRecovering.Load()
	twitterRecoveryBackoff = []time.Duration{time.Millisecond}
	twitterVerifyFunc = func() (string, string, error) {
		return "tester", "main.js", nil
	}
	t.Cleanup(func() {
		twitterVerifyFunc = originalVerify
		twitterRecoveryBackoff = originalBackoff
		twitterRecovering.Store(originalRecovered)
	})

	enterTwitterRecovering("测试失效")
	// 重复调用不应启动第二个恢复循环
	enterTwitterRecovering("测试失效")

	assert.Eventually(t, func() bool {
		return !twitterRecovering.Load()
	}, time.Second, 10*time.Millisecond)
}

func TestRecordTwitterFetchResultTriggersRecovering(t *testing.T) {
	setTwitterLoginAlertDependenciesForTest(t, []int64{1}, func(_ int64, _ *mmsg.MSG) bool { return true })

	originalThreshold := twitterFetchFailThreshold
	originalVerify := twitterVerifyFunc
	originalBackoff := twitterRecoveryBackoff
	twitterFetchFailThreshold = 3
	twitterRecoveryBackoff = []time.Duration{time.Millisecond}
	var verifyCalls atomic.Int32
	twitterVerifyFunc = func() (string, string, error) {
		verifyCalls.Add(1)
		return "", "", assert.AnError
	}
	t.Cleanup(func() {
		twitterFetchFailThreshold = originalThreshold
		twitterVerifyFunc = originalVerify
		twitterRecoveryBackoff = originalBackoff
		twitterFetchFailures.Store(0)
		twitterRecovering.Store(false)
	})

	recordTwitterFetchResult(true)
	for range 3 {
		recordTwitterFetchResult(false)
	}

	assert.True(t, twitterRecovering.Load())
	// 恢复循环应使用mock验证并不断重试
	assert.Eventually(t, func() bool {
		return verifyCalls.Load() > 0
	}, time.Second, 10*time.Millisecond)

	// 成功应清零计数
	recordTwitterFetchResult(true)
	assert.EqualValues(t, 0, twitterFetchFailures.Load())
	assert.Positive(t, verifyCalls.Load())
}
