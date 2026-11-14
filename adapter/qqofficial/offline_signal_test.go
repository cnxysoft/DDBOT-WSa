package qqofficial

import (
	"errors"
	"sync"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/sirupsen/logrus"
)

// offlineSignalHook 抓 Error 级别日志，用来验证「永久离线」有没有真的喊出来。
type offlineSignalHook struct {
	mu   sync.Mutex
	msgs []string
}

func (h *offlineSignalHook) Levels() []logrus.Level { return []logrus.Level{logrus.ErrorLevel} }

func (h *offlineSignalHook) Fire(e *logrus.Entry) error {
	h.mu.Lock()
	h.msgs = append(h.msgs, e.Message)
	h.mu.Unlock()
	return nil
}

func (h *offlineSignalHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.msgs)
}

// TestReportOfflineDistinguishesFatalFromRetrying 复现 P0：网关致命关闭（4914/4915）之后
// run() 直接收工、不再重连，而 Connected() 与普通断线一模一样 —— 上层必须能看出
// 「永远回不来」，而不是一直以为「正在重连」。
func TestReportOfflineDistinguishesFatalFromRetrying(t *testing.T) {
	var online []interface{}
	a := &QQOfficialAdapter{gateway: &Gateway{}}
	a.OnMetaEvent(func(ev *adapter.MetaEvent) {
		online = append(online, ev.Status["online"])
	})

	hook := &offlineSignalHook{}
	logrus.AddHook(hook)
	defer logrus.StandardLogger().ReplaceHooks(dropHook(logrus.StandardLogger().Hooks, hook))

	// 普通断线：报离线事件，但不许喊「永久离线」。
	a.reportOffline()
	if len(online) != 1 || online[0] != false {
		t.Fatalf("普通断线必须报一次离线心跳，实际 %v", online)
	}
	if hook.count() != 0 {
		t.Fatalf("普通断线不该打 Error（后台还在重连），实际 %d 条", hook.count())
	}

	// 致命关闭：同样报离线事件，另外必须打一条 Error 说清「不再重连」。
	a.gateway.markFatal(errors.New("close 4914: bot offline"))
	a.reportOffline()
	if len(online) != 2 || online[1] != false {
		t.Fatalf("永久离线也必须报离线心跳，实际 %v", online)
	}
	if hook.count() != 1 {
		t.Fatalf("永久离线必须打一条 Error 让上层/用户看见，实际 %d 条", hook.count())
	}
}

// dropHook 把测试挂上去的 hook 摘掉，别污染同包其它用例。
func dropHook(hooks logrus.LevelHooks, drop *offlineSignalHook) logrus.LevelHooks {
	out := logrus.LevelHooks{}
	for lvl, list := range hooks {
		for _, h := range list {
			if h == drop {
				continue
			}
			out[lvl] = append(out[lvl], h)
		}
	}
	return out
}
