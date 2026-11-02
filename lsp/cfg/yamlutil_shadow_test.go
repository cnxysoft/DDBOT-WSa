package cfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAndRead(t *testing.T, before, key, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigKeyValueToPath(key, value, path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	data, _ := os.ReadFile(path)
	return string(data)
}

// 段头必须是顶格的。面板保存配置走 yaml.Marshal，顶层键按字母序输出，
// platform 一定排在 weibo 前面 —— 只看字符串前缀的话，platform.weibo
// 会被当成段头，sub 写进 platform.weibo，顶层 weibo 段永远是空的。
func TestWriteConfigKeyValueIgnoresNestedSameNameSection(t *testing.T) {
	before := "platform:\n  bilibili:\n    enable: true\n  weibo:\n    enable: true\n"
	after := writeAndRead(t, before, "bilibili.SESSDATA", "NEWCOOKIE")

	if strings.Contains(after, "weibo:\n    enable: true\n    SESSDATA") {
		t.Fatalf("写进了 platform.bilibili：\n%s", after)
	}
	if !strings.Contains(after, "\nbilibili:\n  SESSDATA: \"NEWCOOKIE\"") {
		t.Fatalf("应新开一个顶层 bilibili 段：\n%s", after)
	}
	if !strings.Contains(after, "platform:\n  bilibili:\n    enable: true\n  weibo:\n    enable: true\n") {
		t.Fatalf("platform 段不能被动：\n%s", after)
	}
}

// 段头大小写不敏感（viper 就是），引号写法也要认 ——
// 认不出来就会在文件末尾追加第二个同名段，重复顶层键让 yaml.v3 直接报错、进程起不来。
func TestWriteConfigKeyValueRecognizesSectionHeaderVariants(t *testing.T) {
	for _, before := range []string{
		"Bilibili:\n  SESSDATA: \"OLD\"\n",
		"\"bilibili\":\n  SESSDATA: \"OLD\"\n",
		"\ufeffbilibili:\n  SESSDATA: \"OLD\"\n", // 记事本存的 UTF-8 带 BOM
	} {
		after := writeAndRead(t, before, "bilibili.SESSDATA", "NEWSESS")
		if strings.Count(after, "ilibili") != 1 {
			t.Fatalf("不该追加重复段（输入 %q）：\n%s", before, after)
		}
		if !strings.Contains(after, "\"NEWSESS\"") {
			t.Fatalf("值没写进去（输入 %q）：\n%s", before, after)
		}
	}
}

// 段里的顶格注释不算段结束。当成结束会把新键插到注释前面，
// 和后面的真键撞成重复键，整份配置直接读不了。
func TestWriteConfigKeyValueCommentDoesNotEndSection(t *testing.T) {
	after := writeAndRead(t, "bilibili:\n# 说明\n  SESSDATA: \"OLD\"\n", "bilibili.SESSDATA", "NEWSESS")
	if strings.Count(after, "SESSDATA") != 1 {
		t.Fatalf("注释不该导致重复键：\n%s", after)
	}
}

// 行内注释要留着 —— 官方文档的示例就写着 SESSDATA: # 你的b站cookie。
func TestWriteConfigKeyValueKeepsInlineComment(t *testing.T) {
	after := writeAndRead(t, "bilibili:\n  SESSDATA: # 你的b站cookie\n  bili_jct: # 另一个\n", "bilibili.SESSDATA", "NEWSESS")
	if !strings.Contains(after, "SESSDATA: \"NEWSESS\" # 你的b站cookie") {
		t.Fatalf("行内注释被吃掉了：\n%s", after)
	}
	if !strings.Contains(after, "bili_jct: # 另一个") {
		t.Fatalf("别的键不该被动：\n%s", after)
	}
}

// 带引号的键名要能原地替换，不能另起一行。
func TestWriteConfigKeyValueMatchesQuotedKey(t *testing.T) {
	after := writeAndRead(t, "bilibili:\n  \"SESSDATA\": \"OLD\"\n  bili_jct: x\n", "bilibili.SESSDATA", "NEWSESS")
	if strings.Count(after, "SESSDATA") != 1 {
		t.Fatalf("引号键应原地替换：\n%s", after)
	}
}

// 混合换行的文件里，不能给本来就是 LF 的行补 CR —— 那等于把文件改成另一种混合换行。
func TestWriteConfigKeyValueKeepsPerLineEOL(t *testing.T) {
	after := writeAndRead(t, "a: 1\r\nb: 2\nbilibili:\n  SESSDATA: OLD\n", "bilibili.SESSDATA", "NEWSESS")
	if strings.Contains(after, "NEWSESS\"\r") {
		t.Fatalf("LF 行被补了 CR：\n%q", after)
	}
	if !strings.Contains(after, "a: 1\r\n") {
		t.Fatalf("原本的 CRLF 行不该被动：\n%q", after)
	}
}
