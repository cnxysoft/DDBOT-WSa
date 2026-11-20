package lsp

import (
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestLsp_ConcernNotify(t *testing.T) {
	logrus.SetLevel(logrus.TraceLevel)
	initLsp(t)
	defer closeLsp(t)

	defer func() {
		Instance.concernNotify = concern.ReadNotifyChan()
	}()

	testEventChan := make(chan concern.Event, 16)
	testNotifyChan := make(chan concern.Notify, 2)

	Instance.concernNotify = testNotifyChan

	var result *mmsg.MSG
	msgChan := make(chan *mmsg.MSG, 10)
	target := mmsg.NewGroupTarget(test.G1)
	ctx := NewCtx(t, msgChan, test.Sender1, target)

	err := Instance.PermissionStateManager.GrantRole(test.Sender1.UserID, permission.Admin)
	assert.Nil(t, err)

	tc1 := newTestConcern(t, testEventChan, testNotifyChan, test.Site1, []concern_type.Type{test.T1})
	concern.RegisterConcern(tc1)
	defer tc1.Stop()

	IWatch(ctx, test.G1, test.NAME1, test.Site1, test.T1, false)
	result = <-msgChan
	assert.Contains(t, msgstringer.AdapterMsgToString(result.ToCombineMessage(target).Elements), success)

	IWatch(ctx, test.G2, test.NAME1, test.Site1, test.T1, false)
	result = <-msgChan
	assert.Contains(t, msgstringer.AdapterMsgToString(result.ToCombineMessage(target).Elements), success)

	testEventChan <- tc1.NewTestEvent(test.T1, 0, test.NAME1)

	// Receive both dispatched notifications before closing their input. Waiting
	// for the consumer before closing it deadlocks whenever its goroutine has
	// already registered with Instance.wg.
	notifications := make([]concern.Notify, 0, 2)
	for len(notifications) < 2 {
		select {
		case notification := <-testNotifyChan:
			notifications = append(notifications, notification)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for both subscribed groups' notifications")
		}
	}
	for _, notification := range notifications {
		testNotifyChan <- notification
	}
	close(testNotifyChan)
	Instance.ConcernNotify()
	Instance.notifyWg.Wait()
}

func TestNewAtAllMsg(t *testing.T) {
	var msg = mmsg.NewMSG()
	newAtAllMsg(msg)
	assert.NotNil(t, msg)
	var e adapter.IMessageElement
	for _, elem := range msg.Elements() {
		if _, ok := elem.(*mmsg.AtElement); ok {
			e = elem
			break
		}
	}
	assert.NotNil(t, e)
	assert.EqualValues(t, 0, e.(*mmsg.AtElement).Target)
}

func TestNewAtIdsMsg(t *testing.T) {
	var msg = mmsg.NewMSG()
	newAtIdsMsg(msg, []int64{test.UID1, test.UID2})
	assert.NotNil(t, msg)
	assert.Len(t, msg.Elements(), 2)
}
