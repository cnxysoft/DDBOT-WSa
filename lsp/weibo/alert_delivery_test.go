package weibo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	testutil "github.com/cnxysoft/DDBOT-WSa/internal/test"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type alertDeliveryAdapter struct {
	adapter.Adapter
	groupErr error
}

func (a *alertDeliveryAdapter) IsConnected() bool                                   { return true }
func (a *alertDeliveryAdapter) Stop() error                                         { return nil }
func (a *alertDeliveryAdapter) OnGroupMessage(func(*adapter.GroupMessageEvent))     {}
func (a *alertDeliveryAdapter) OnPrivateMessage(func(*adapter.PrivateMessageEvent)) {}
func (a *alertDeliveryAdapter) OnMetaEvent(func(*adapter.MetaEvent))                {}
func (a *alertDeliveryAdapter) OnNoticeEvent(func(*adapter.NoticeEvent))            {}
func (a *alertDeliveryAdapter) OnRequestEvent(func(*adapter.RequestEvent))          {}
func (a *alertDeliveryAdapter) SendGroupMessage(_ int64, _ interface{}) (int32, error) {
	if a.groupErr != nil {
		return 0, a.groupErr
	}
	return 1, nil
}

func TestGroupAlertsKeepDedupWhenMessageIsQueued(t *testing.T) {
	testutil.InitBuntdb(t)
	defer testutil.CloseBuntdb(t)

	oldBot := bot.Instance
	oldConcern := c
	oldQueueEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldAlertDisabled := config.GlobalConfig.GetBool("weibo.disableCookieAlert")
	defer func() {
		bot.Instance = oldBot
		c = oldConcern
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldQueueEnable)
		config.GlobalConfig.Set("weibo.disableCookieAlert", oldAlertDisabled)
	}()

	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	config.GlobalConfig.Set("weibo.disableCookieAlert", false)
	c = NewConcern(nil)

	tests := []struct {
		name string
		key  func(int64) string
		send func(int64) bool
	}{
		{name: "cookie alert", key: func(groupCode int64) string {
			return c.StateManager.CookieAlertKey(groupCode)
		}, send: func(groupCode int64) bool {
			return sendGroupAlert(groupCode, false)
		}},
		{name: "SUB alert", key: func(groupCode int64) string {
			return c.StateManager.SUBExpiredAlertKey(groupCode)
		}, send: sendSUBExpiredGroupAlert},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groupCode := int64(987650 + i)
			mock := &alertDeliveryAdapter{groupErr: fmt.Errorf("%w: not connected", adapter.ErrRequestNotSent)}
			messenger := adapter.NewMessenger(mock)
			messenger.Online.Store(true)
			bot.Instance = &bot.Bot{Messenger: messenger}
			defer messenger.Stop()

			assert.True(t, tc.send(groupCode), "queued group alert must count as accepted delivery")
			_, err := localdb.Get(tc.key(groupCode))
			require.NoError(t, err, "queued group alert must retain its dedup key")
			_, _ = localdb.Delete(tc.key(groupCode))
		})
	}
}

func TestGroupAlertsClearDedupOnUnclassifiedError(t *testing.T) {
	testutil.InitBuntdb(t)
	defer testutil.CloseBuntdb(t)

	oldBot := bot.Instance
	oldConcern := c
	oldQueueEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldAlertDisabled := config.GlobalConfig.GetBool("weibo.disableCookieAlert")
	defer func() {
		bot.Instance = oldBot
		c = oldConcern
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldQueueEnable)
		config.GlobalConfig.Set("weibo.disableCookieAlert", oldAlertDisabled)
	}()

	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	config.GlobalConfig.Set("weibo.disableCookieAlert", false)
	c = NewConcern(nil)

	tests := []struct {
		name string
		key  func(int64) string
		send func(int64) bool
	}{
		{name: "cookie alert", key: func(groupCode int64) string {
			return c.StateManager.CookieAlertKey(groupCode)
		}, send: func(groupCode int64) bool {
			return sendGroupAlert(groupCode, false)
		}},
		{name: "SUB alert", key: func(groupCode int64) string {
			return c.StateManager.SUBExpiredAlertKey(groupCode)
		}, send: sendSUBExpiredGroupAlert},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groupCode := int64(987660 + i)
			mock := &alertDeliveryAdapter{groupErr: errors.New("satori request failed")}
			messenger := adapter.NewMessenger(mock)
			messenger.Online.Store(true)
			bot.Instance = &bot.Bot{Messenger: messenger}
			defer messenger.Stop()

			assert.False(t, tc.send(groupCode), "unclassified errors must not count as accepted delivery")
			_, err := localdb.Get(tc.key(groupCode))
			assert.Error(t, err, "failed group alert must not retain its dedup key")
		})
	}
}
