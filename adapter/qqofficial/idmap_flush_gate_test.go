package qqofficial

// 本文件钉住「Flush 无事可写时不碰磁盘」这条前提。
//
// 为什么必须钉：状态刷盘间隔被压到 2 秒（见 adapter.go 的 stateFlushInterval 注释），
// 一天有 43200 个 tick。如果每个 tick 都真写一次盘（读主文件 → 序列化 → 临时文件 →
// fsync → rename → 备份轮转），在双核小机上就是白白磨损磁盘。所以「gen 没推进就
// 直接返回」必须是硬契约，不是优化建议。

import (
	"testing"
)

// 没有新分配时 Flush 必须直接返回，不调用 Save。
func TestFlushSkipsSaveWhenNothingNewAllocated(t *testing.T) {
	p := newFakePersister()
	m := NewIDMap(p)

	m.ToInt64("app", "x")
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, saves := p.counters(); saves != 1 {
		t.Fatalf("有新分配时 Flush 应写一次，实际 %d", saves)
	}

	// 第二个、第三个 tick：没有任何新分配 —— 不许再写盘
	for i := 0; i < 3; i++ {
		if err := m.Flush(); err != nil {
			t.Fatalf("第 %d 次空 Flush: %v", i+1, err)
		}
	}
	if _, saves := p.counters(); saves != 1 {
		t.Fatalf("没有新分配时 Flush 不该写盘，实际写了 %d 次（2 秒一次 = 一天 43200 次 fsync）", saves)
	}

	// 一旦有新分配，下一次 Flush 必须补上
	m.ToInt64("app", "y")
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, saves := p.counters(); saves != 2 {
		t.Fatalf("有新分配后 Flush 应再写一次，实际 %d", saves)
	}
	if snap := p.snapshot(); len(snap) != 2 {
		t.Fatalf("落盘快照应含 2 条，实际 %v", snap)
	}
}
