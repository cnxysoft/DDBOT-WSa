package cfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTempConfig(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustParseYAML(t *testing.T, content string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &m); err != nil {
		t.Fatalf("不再是合法 YAML: %v\n%s", err, content)
	}
	return m
}

// 写回单个键必须原样保留缩进与键名写法，只换值。
//
// 回归背景：B 站扫码登录成功后回写 cookie，原实现把缩进写死成两空格，
// 4 空格缩进的配置被改成「SESSDATA 两空格、bili_jct 四空格」的混合缩进，
// YAML 直接不合法 —— 提示「请重启」之后重启就报错。
// 原实现还是大小写敏感匹配，文件里写小写 sessdata 时匹配不上，
// 会在段尾另起一行 SESSDATA，同一份配置出现两个等价键。
func TestWriteConfigKeyValuePreservesFormat(t *testing.T) {
	cases := []struct {
		name   string
		before string
		old    string
		key    string
		value  string
	}{
		{
			name:   "四空格缩进 + 小写键名",
			before: "bilibili:\n    sessdata: \"old\"\n    bili_jct: \"old\"\n    interval: 25s\n",
			old:    "sessdata: \"old\"",
			key:    "bilibili.SESSDATA",
			value:  "new-cookie",
		},
		{
			name:   "两空格缩进",
			before: "bilibili:\n  SESSDATA: \"old\"\n  interval: 25s\n",
			old:    "SESSDATA: \"old\"",
			key:    "bilibili.SESSDATA",
			value:  "new-cookie",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeTempConfig(t, c.before)

			if err := writeConfigKeyValueToPath(c.key, c.value, path); err != nil {
				t.Fatalf("写入失败: %v", err)
			}
			after := readTempConfig(t, path)

			mustParseYAML(t, after)

			newPair := strings.Replace(c.old, "\"old\"", "\""+c.value+"\"", 1)
			want := strings.Replace(c.before, c.old, newPair, 1)
			if after != want {
				t.Fatalf("应只替换值、保留其余格式\n--- want ---\n%q\n--- got ---\n%q", want, after)
			}
		})
	}
}

// 键不存在时要按段内已有子键的缩进插入，不能写死两空格。
func TestWriteConfigKeyValueInsertUsesSiblingIndent(t *testing.T) {
	path := writeTempConfig(t, "bilibili:\n    bili_jct: \"jct\"\n    interval: 25s\n")

	if err := writeConfigKeyValueToPath("bilibili.SESSDATA", "cookie", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	after := readTempConfig(t, path)

	want := "bilibili:\n    bili_jct: \"jct\"\n    interval: 25s\n    SESSDATA: \"cookie\"\n"
	if after != want {
		t.Fatalf("新键应与兄弟键同缩进、且不落在尾部空行之后\n--- want ---\n%q\n--- got ---\n%q", want, after)
	}
	mustParseYAML(t, after)
}

// 值必须加引号：cookie / token 里出现 ": " 或 " #" 时裸写会被 YAML 解析错。
func TestWriteConfigKeyValueQuotesValue(t *testing.T) {
	path := writeTempConfig(t, "bilibili:\n  sessdata: \"old\"\n")

	if err := writeConfigKeyValueToPath("bilibili.SESSDATA", "a: b #c", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	after := readTempConfig(t, path)

	m := mustParseYAML(t, after)
	got, _ := m["bilibili"].(map[string]interface{})
	if got == nil || got["sessdata"] != "a: b #c" {
		t.Fatalf("值应被完整保留，got %v\n%s", got, after)
	}
}

// 段不存在时追加新段，原有内容不能丢。
func TestWriteConfigKeyValueAppendsSection(t *testing.T) {
	path := writeTempConfig(t, "bot:\n  qq: 123456\n")

	if err := writeConfigKeyValueToPath("bilibili.SESSDATA", "cookie", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	after := readTempConfig(t, path)

	m := mustParseYAML(t, after)
	bot, _ := m["bot"].(map[string]interface{})
	bili, _ := m["bilibili"].(map[string]interface{})
	if bot == nil || bili == nil || bili["SESSDATA"] != "cookie" {
		t.Fatalf("原有内容应保留、新段应写入，got %v\n%s", m, after)
	}
	// 原文件以换行结尾，写完后也得是——不然 git diff 会显示 "\ No newline at end of file"
	if !strings.HasSuffix(after, "\n") {
		t.Fatalf("应保留结尾换行，got %q", after)
	}
}

// 段后面还有别的段时，新键要插在段内，不能跑到别的段里。
func TestWriteConfigKeyValueStopsAtNextSection(t *testing.T) {
	path := writeTempConfig(t, "bilibili:\n    interval: 25s\nother:\n    key: value\n")

	if err := writeConfigKeyValueToPath("bilibili.SESSDATA", "cookie", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	after := readTempConfig(t, path)

	m := mustParseYAML(t, after)
	other, _ := m["other"].(map[string]interface{})
	if other == nil || other["key"] != "value" || len(other) != 1 {
		t.Fatalf("other 段不能被破坏，got %v\n%s", m, after)
	}
	if strings.Count(after, "SESSDATA") != 1 {
		t.Fatalf("应只出现一次 SESSDATA\n%s", after)
	}
}
