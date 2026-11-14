package adapter

// 本文件钉住「adapter.mode 写错不再让程序起不来」这条产品决策。
//
// 依据：adapter.mode 是用户手写配置里最容易打错的一个字（onebot-v1、onebot_v11、
// onebto-v11 …），而它的历史行为是让 bot.Init 里 Fatalf 直接退出 —— 老用户升级
// 或换通道时打错一个字，整个程序连日志都来不及看就没了。现在统一在
// GetAdapterType 里收敛：不认识的打醒目 Warn，然后按既有的默认路径（onebot-v11）继续。
//
// 暂时性说明：这是「宁可跑起来再说」的取舍。将来如果 adapter.mode 有了必须严肃对待
// 的语义（比如 satori 与 onebot 行为不再兼容），或者配置改成由 Web 管理端写入
// （打错的可能性消失），就应该重新评估回「硬失败」，甚至配上显式校验 + 保存前拦截。

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/sirupsen/logrus"
)

// captureAdapterLogs 把包级日志接进内存。本包单测不并行，临时换输出是安全的。
func captureAdapterLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	std := logrus.StandardLogger()
	oldOut := std.Out
	oldLevel := std.Level
	std.SetOutput(buf)
	std.SetLevel(logrus.DebugLevel)
	t.Cleanup(func() {
		std.SetOutput(oldOut)
		std.SetLevel(oldLevel)
	})
	return buf
}

// setAdapterMode 改 adapter.mode，并在测试结束还原。
func setAdapterMode(t *testing.T, mode string) {
	t.Helper()
	old := config.GlobalConfig.GetString("adapter.mode")
	config.GlobalConfig.Set("adapter.mode", mode)
	t.Cleanup(func() { config.GlobalConfig.Set("adapter.mode", old) })
}

// 不认识的 adapter.mode：不许 panic / 不许走到 Fatal（工厂返回 nil），回到 onebot-v11 并告警。
func TestUnknownAdapterModeFallsBackToOneBot(t *testing.T) {
	const typo = "onebot-v1" // 少一个 1，最典型的那种手滑
	setAdapterMode(t, typo)

	buf := captureAdapterLogs(t)
	if got := GetAdapterType(); got != AdapterTypeOneBotV11 {
		t.Fatalf("不认识的 adapter.mode=%q 必须回落到 %s（否则工厂返回 nil，bot.Init 会 Fatalf 退出），实际 %q",
			typo, AdapterTypeOneBotV11, got)
	}
	if got := GetAdapterConfig().Mode; got != string(AdapterTypeOneBotV11) {
		t.Errorf("回落之后 AdapterConfig.Mode 也应当是一致的值（下游按它分支），实际 %q", got)
	}

	logs := buf.String()
	for _, want := range []string{typo, "回落", string(AdapterTypeOneBotV11)} {
		if !strings.Contains(logs, want) {
			t.Errorf("未知 adapter.mode 必须打一条醒目 Warn（含填写的值 %q 与「回落」），实际日志：%s", want, logs)
		}
	}
	if !strings.Contains(logs, "level=warning") {
		t.Errorf("这条告警必须是 Warn 级（否则用户看不见它为何跑成了 OneBot），实际日志：%s", logs)
	}
}

// 三个已知值必须原样返回，且不许刷告警。
func TestKnownAdapterModesUnchanged(t *testing.T) {
	for _, mode := range []string{string(AdapterTypeOneBotV11), string(AdapterTypeSatori), string(AdapterTypeQQOfficial)} {
		setAdapterMode(t, mode)
		buf := captureAdapterLogs(t)
		if got := GetAdapterType(); got != AdapterType(mode) {
			t.Errorf("已知 adapter.mode=%q 应原样返回，实际 %q", mode, got)
		}
		if logs := buf.String(); strings.Contains(logs, "回落") {
			t.Errorf("已知 adapter.mode=%q 不该打回落告警，实际日志：%s", mode, logs)
		}
	}
}

// 没配 adapter.mode：历史默认是 onebot-v11，且不该告警（这是正常配置）。
func TestEmptyAdapterModeIsOneBotWithoutWarning(t *testing.T) {
	setAdapterMode(t, "")
	buf := captureAdapterLogs(t)
	if got := GetAdapterType(); got != AdapterTypeOneBotV11 {
		t.Errorf("空 adapter.mode 应落到 %s，实际 %q", AdapterTypeOneBotV11, got)
	}
	if logs := buf.String(); strings.Contains(logs, "回落") {
		t.Errorf("空配置是正常情况，不该打回落告警，实际日志：%s", logs)
	}
}
