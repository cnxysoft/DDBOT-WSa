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
	setBilibiliLoginAlertSenderForTest(t, func() bool { return true })

	notifyBilibiliLoginExpired()
	notifyBilibiliLoginExpired()

	assert.True(t, bilibiliLoginAlertSent.Load())
	assert.Equal(t, 1, bilibiliLoginAlertSendCount)
}

func TestNotifyBilibiliLoginExpiredOnlyOnceWhenConcurrent(t *testing.T) {
	var calls atomic.Int32
	setBilibiliLoginAlertSenderForTest(t, func() bool {
		calls.Add(1)
		return true
	})

	var waitGroup sync.WaitGroup
	for range 20 {
		waitGroup.Go(notifyBilibiliLoginExpired)
	}
	waitGroup.Wait()

	assert.EqualValues(t, 1, calls.Load())
}

func TestNotifyBilibiliLoginExpiredRetriesAfterFailure(t *testing.T) {
	attempt := 0
	setBilibiliLoginAlertSenderForTest(t, func() bool {
		attempt++
		return attempt > 1
	})

	notifyBilibiliLoginExpired()
	assert.False(t, bilibiliLoginAlertSent.Load())
	notifyBilibiliLoginExpired()
	notifyBilibiliLoginExpired()

	assert.True(t, bilibiliLoginAlertSent.Load())
	assert.Equal(t, 2, bilibiliLoginAlertSendCount)
}

func TestBilibiliLoginExpiredAlertMessage(t *testing.T) {
	msg := newBilibiliLoginExpiredAlertMessage().ToCombineMessage(mmsg.NewPrivateTarget(1))
	text := msgstringer.AdapterMsgToString(msg.Elements)

	assert.Contains(t, text, "动态和直播订阅推送已停止")
	assert.Contains(t, text, "bilibili.SESSDATA")
	assert.Contains(t, text, "bilibili.bili_jct")
	assert.Contains(t, text, "bilibili.QRLogin")
}

var bilibiliLoginAlertSendCount int

func setBilibiliLoginAlertSenderForTest(t *testing.T, sender func() bool) {
	t.Helper()
	originalSender := sendBilibiliLoginAlert
	bilibiliLoginAlertSendCount = 0
	bilibiliLoginAlertSent.Store(false)
	sendBilibiliLoginAlert = func() bool {
		bilibiliLoginAlertSendCount++
		return sender()
	}
	t.Cleanup(func() {
		sendBilibiliLoginAlert = originalSender
		bilibiliLoginAlertSent.Store(false)
		bilibiliLoginAlertSendCount = 0
	})
}
