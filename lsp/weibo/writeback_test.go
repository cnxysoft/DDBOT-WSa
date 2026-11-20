package weibo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 微博写回复用 lsp/cfg 的实现，段头同样必须顶格。
//
// 面板保存配置走 yaml.Marshal，顶层键按字母序输出，platform 排在 weibo 前面 ——
// 只按字符串前缀认段头的话，platform.weibo 会被当成段头，sub 写进 platform.weibo，
// 顶层 weibo.sub 永远写不上：登录成功、日志也打了「已写入配置」，重启却仍跑访客模式。
func TestWriteBackConfigIgnoresNestedSameNameSection(t *testing.T) {
	before := "platform:\n  bilibili:\n    enable: true\n  weibo:\n    enable: true\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "application.yaml")
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}

	if err := writeBackConfigToPath("NEWSUB", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	data, _ := os.ReadFile(path)
	after := string(data)

	if strings.Contains(after, "weibo:\n    enable: true\n    sub:") {
		t.Fatalf("sub 写进了 platform.weibo：\n%s", after)
	}
	if !strings.Contains(after, "\nweibo:\n  sub: \"NEWSUB\"") {
		t.Fatalf("应新开一个顶层 weibo 段：\n%s", after)
	}
}
