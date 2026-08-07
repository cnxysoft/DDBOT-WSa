package bilibili

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/stretchr/testify/assert"
)

func TestIsBilibiliLoginInvalidResponse(t *testing.T) {
	tests := []struct {
		name    string
		code    int32
		message string
		want    bool
	}{
		{name: "直播账号未登录", code: -101, message: "账号未登录", want: true},
		{name: "动态用户未登录", code: 4100000, message: "用户未登录", want: true},
		{name: "其他接口错误", code: -400, message: "请求错误", want: false},
		{name: "相同错误码但不是登录错误", code: -101, message: "账号异常", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isBilibiliLoginInvalidResponse(tt.code, tt.message))
		})
	}
}

func TestNotifyBilibiliLoginExpiredOnlyOnceAfterSuccess(t *testing.T) {
	var calls int
	setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool {
		calls++
		return true
	})

	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)

	assert.Equal(t, 1, calls)
}

func TestNotifyBilibiliLoginExpiredOnlyOnceWhenConcurrent(t *testing.T) {
	var calls atomic.Int32
	setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool {
		calls.Add(1)
		return true
	})

	var waitGroup sync.WaitGroup
	for range 20 {
		waitGroup.Go(func() {
			notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
		})
	}
	waitGroup.Wait()

	assert.EqualValues(t, 1, calls.Load())
}

func TestNotifyBilibiliLoginExpiredRetriesOnlyFailedAdmins(t *testing.T) {
	calls := make(map[int64]int)
	setBilibiliLoginAlertDependenciesForTest(t, []int64{1, 2}, func(qq int64) bool {
		calls[qq]++
		return qq == 1 || calls[qq] > 1
	})

	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)

	assert.Equal(t, 1, calls[1])
	assert.Equal(t, 2, calls[2])
}

func TestNotifyBilibiliLoginExpiredRetriesAfterBotRecovery(t *testing.T) {
	var calls int
	ready := false
	setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool {
		calls++
		return true
	})
	bilibiliLoginAlertBotReady = func() bool { return ready }

	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	assert.Equal(t, 0, calls)

	ready = true
	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	assert.Equal(t, 1, calls)
}

func TestBilibiliLoginAlertResetsAfterSameInterfaceRecovery(t *testing.T) {
	for _, source := range []bilibiliLoginAlertSource{
		bilibiliLoginSourceDynamic,
		bilibiliLoginSourceLive,
	} {
		t.Run(sourceName(source), func(t *testing.T) {
			var calls int
			setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool {
				calls++
				return true
			})

			notifyBilibiliLoginExpired(source)
			notifyBilibiliLoginExpired(source)
			assert.Equal(t, 1, calls)

			markBilibiliLoginRecovered(source)
			notifyBilibiliLoginExpired(source)
			assert.Equal(t, 2, calls)
		})
	}
}

func TestBilibiliLoginAlertSelfCheckWaitsForRoutineInterfaces(t *testing.T) {
	var calls int
	setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool {
		calls++
		return true
	})

	notifyBilibiliLoginExpired(bilibiliLoginSourceSelf)
	markBilibiliLoginRecovered(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceLive)
	assert.Equal(t, 1, calls)

	markBilibiliLoginRecovered(bilibiliLoginSourceLive)
	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	assert.Equal(t, 2, calls)
}

func TestBilibiliLoginAlertSelfCheckRecoveryResetsAllInterfaces(t *testing.T) {
	var calls int
	setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool {
		calls++
		return true
	})

	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceLive)
	markBilibiliLoginRecovered(bilibiliLoginSourceSelf)
	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)

	assert.Equal(t, 2, calls)
}

func TestBilibiliLoginAlertWaitsForAllFailedInterfacesToRecover(t *testing.T) {
	var calls int
	setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool {
		calls++
		return true
	})

	notifyBilibiliLoginExpired(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceLive)
	assert.Equal(t, 1, calls)

	markBilibiliLoginRecovered(bilibiliLoginSourceDynamic)
	notifyBilibiliLoginExpired(bilibiliLoginSourceLive)
	assert.Equal(t, 1, calls)

	markBilibiliLoginRecovered(bilibiliLoginSourceLive)
	notifyBilibiliLoginExpired(bilibiliLoginSourceLive)
	assert.Equal(t, 2, calls)
}

func TestBilibiliLoginExpiredAlertMessage(t *testing.T) {
	msg := newBilibiliLoginExpiredAlertMessage().ToCombineMessage(mmsg.NewPrivateTarget(1))
	text := msgstringer.AdapterMsgToString(msg.Elements)

	assert.Contains(t, text, "动态和直播订阅推送已停止")
	assert.Contains(t, text, "bilibili.SESSDATA")
	assert.Contains(t, text, "bilibili.bili_jct")
	assert.Contains(t, text, "bilibili.QRLogin")
}

func setBilibiliLoginAlertDependenciesForTest(t *testing.T, admins []int64, sender func(int64) bool) {
	t.Helper()
	originalListAdmins := listBilibiliLoginAlertAdmins
	originalBotReady := bilibiliLoginAlertBotReady
	originalSender := sendBilibiliLoginAlertToAdmin

	bilibiliLoginAlertState.reset()
	bilibiliLoginAlertSending.Store(false)
	listBilibiliLoginAlertAdmins = func() []int64 {
		return append([]int64(nil), admins...)
	}
	bilibiliLoginAlertBotReady = func() bool { return true }
	sendBilibiliLoginAlertToAdmin = sender

	t.Cleanup(func() {
		listBilibiliLoginAlertAdmins = originalListAdmins
		bilibiliLoginAlertBotReady = originalBotReady
		sendBilibiliLoginAlertToAdmin = originalSender
		bilibiliLoginAlertState.reset()
		bilibiliLoginAlertSending.Store(false)
	})
}

func sourceName(source bilibiliLoginAlertSource) string {
	switch source {
	case bilibiliLoginSourceDynamic:
		return "dynamic"
	case bilibiliLoginSourceLive:
		return "live"
	default:
		return "self"
	}
}
