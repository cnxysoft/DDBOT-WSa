package cfg

import (
	"strings"
	"testing"
)

// Windows 上编辑过的配置是 CRLF，写回后不能变成混合换行。
//
// 回归背景：群友反馈「自动更新配置会改动 yaml 的排版」。除了缩进和键名大小写，
// 行尾也被改过 —— 正则把行尾的 \r 一起吃进了「值」那一组，拼回时丢掉，
// 那两行就从 CRLF 变成 LF，整份文件成了混合换行。
// 用群友真机配置（319 行 CRLF）复现过：修之前写后是 317 CRLF + 2 LF。
func TestWriteConfigKeyValuePreservesCRLF(t *testing.T) {
	before := "bilibili:\r\n    sessdata: \"old\"\r\n    bili_jct: \"old\"\r\n"
	path := writeTempConfig(t, before)

	if err := writeConfigKeyValueToPath("bilibili.SESSDATA", "new-cookie", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	after := readTempConfig(t, path)

	want := strings.Replace(before, "\"old\"", "\"new-cookie\"", 1)
	if after != want {
		t.Fatalf("CRLF 配置应逐字节保留、只换值\n--- want ---\n%q\n--- got ---\n%q", want, after)
	}
	if lf := strings.Count(after, "\n") - strings.Count(after, "\r\n"); lf != 0 {
		t.Fatalf("不该出现纯 LF 行，got %d 行", lf)
	}
	mustParseYAML(t, after)
}

// 往 CRLF 配置里插新键，也要用 CRLF。
func TestWriteConfigKeyValueInsertsCRLF(t *testing.T) {
	before := "bilibili:\r\n    interval: 25s\r\n"
	path := writeTempConfig(t, before)

	if err := writeConfigKeyValueToPath("bilibili.SESSDATA", "cookie", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	after := readTempConfig(t, path)

	want := "bilibili:\r\n    interval: 25s\r\n    SESSDATA: \"cookie\"\r\n"
	if after != want {
		t.Fatalf("新键也应使用 CRLF\n--- want ---\n%q\n--- got ---\n%q", want, after)
	}
	mustParseYAML(t, after)
}
