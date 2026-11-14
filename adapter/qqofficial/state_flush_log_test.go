package qqofficial

// 定期落盘失败的日志限流（P2）。
//
// 缺陷形态：stateFlushInterval 是 2 秒，stateLoop 每次失败各打一条 Warn。
// 而 IDMap 一旦因为状态文件读不出来进入只读降级（ErrLoadFailed），Flush 就是
// **永久**失败 —— 每 2 秒一条，一天四万多条，把真正有用的日志淹掉。
//
// 要求：同类失败做限流（同一原因首条 Warn，之后每分钟最多一条），
// 但不许因此把失败吞掉：被压掉的条数要在下一条里体现出来。

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// failingPersister 复现「状态文件读不出来」：Load 失败 -> IDMap 只读降级 -> Flush 永远报错。
type failingPersister struct{}

func (failingPersister) Load() (map[string]int64, error) {
	return nil, errUnsupported("测试用 Persister")
}

func (failingPersister) Save(map[string]int64) error { return nil }

// warnCapture 抓 Warn 级日志；off 置真之后不再记录（测试结束时用它断开，避免影响别的用例）。
type warnCapture struct {
	mu   sync.Mutex
	off  bool
	msgs []string
}

func (h *warnCapture) Levels() []logrus.Level { return logrus.AllLevels }

func (h *warnCapture) Fire(e *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.off || e.Level != logrus.WarnLevel {
		return nil
	}
	h.msgs = append(h.msgs, e.Message)
	return nil
}

func (h *warnCapture) stop() {
	h.mu.Lock()
	h.off = true
	h.mu.Unlock()
}

// matching 数「消息里含 want 的 Warn」有几条，顺便把原文拼出来给断言失败时看。
func (h *warnCapture) matching(want string) (int, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	out := ""
	for _, m := range h.msgs {
		if strings.Contains(m, want) {
			n++
			out += "| " + m + " "
		}
	}
	return n, out
}

// TestStateLoopFlushFailureLogIsRateLimited：勾 3 次 tick，只许打 1 条 Warn。
func TestStateLoopFlushFailureLogIsRateLimited(t *testing.T) {
	a := newTestAdapter(t)
	// 只读降级的 IDMap：Flush 每次都失败，和真机「状态文件坏了」一模一样。
	a.ids = NewIDMap(failingPersister{})

	h := &warnCapture{}
	logger.Logger.AddHook(h)
	t.Cleanup(h.stop)

	ctx, cancel := context.WithCancel(context.Background())
	a.wg.Add(1)
	go a.stateLoop(ctx)

	// stateFlushInterval 是 2 秒：睡 4.5 秒 = 至少两次 tick。
	time.Sleep(stateFlushInterval*2 + 500*time.Millisecond)
	cancel()
	a.wg.Wait()

	n, dump := h.matching("定期落盘 ID 映射失败")
	if n != 1 {
		t.Fatalf("同类落盘失败要限流：%v 内出现 %d 次 tick 就该只有 1 条 Warn"+
			"（首条立即打，之后每分钟最多一条），实际 %d 条：%s",
			stateFlushInterval*2+500*time.Millisecond, 2, n, dump)
	}
	if dump == "" {
		t.Fatal("限流不许把失败吞掉：一次都没报")
	}
}

// TestFailureLogLimiterRateLimitsPerKey：限流器本身的语义（受控时钟，不依赖真实时间）。
//
//   - 每个 key 的首条立即放行；
//   - 间隔内重复被压掉，过了间隔才再放行，并且把「压掉几条」带出来；
//   - 不同 key 互不影响；
//   - reset 之后重新按首条算。
func TestFailureLogLimiterRateLimitsPerKey(t *testing.T) {
	clock := newTestClock()
	l := newFailureLogLimiter(time.Minute)
	l.now = clock.now

	if ok, dropped := l.allow(stateFlushLogKeyIDMap); !ok || dropped != 0 {
		t.Fatalf("首条失败必须立即打出来：ok=%v dropped=%d", ok, dropped)
	}
	clock.advance(30 * time.Second)
	if ok, _ := l.allow(stateFlushLogKeyIDMap); ok {
		t.Fatal("同一类失败在间隔内不该再打第二条（否则 2 秒一条就刷屏了）")
	}
	if ok, _ := l.allow(stateFlushLogKeyGroups); !ok {
		t.Fatal("不同 key（群清单）不该被 ID 映射的失败挡住")
	}
	clock.advance(31 * time.Second)
	ok, dropped := l.allow(stateFlushLogKeyIDMap)
	if !ok {
		t.Fatal("过了间隔必须再放行一条，否则等于把失败吞了")
	}
	if dropped != 1 {
		t.Fatalf("再放行的那条要把期间被压掉的条数带出来，期望 1，实际 %d", dropped)
	}
	if had, _ := l.reset(stateFlushLogKeyIDMap); !had {
		t.Fatal("reset 应报告「之前处于失败状态」，调用方据此打恢复日志")
	}
	// reset **不重置间隔**：紧接着又失败（抖动）必须继续被压住，否则 2 秒一对会刷爆日志。
	if ok, _ := l.allow(stateFlushLogKeyIDMap); ok {
		t.Fatal("reset 之后仍在间隔内，抖动不该立刻再打一条")
	}
	if had, _ := l.reset("从来没失败过的 key"); had {
		t.Fatal("从没失败过的 key 调 reset 应返回 false（没有要报的恢复）")
	}
	// 过了间隔再失败，必须重新打得出来（不许把失败吞了）。
	clock.advance(2 * time.Minute)
	if ok, _ := l.allow(stateFlushLogKeyIDMap); !ok {
		t.Fatal("距上次打日志已超过间隔，新失败必须立刻可见")
	}
}
