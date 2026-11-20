package cfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
)

func readFileT(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFileT(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func parseT(t *testing.T, content string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &m); err != nil {
		t.Fatalf("不是合法 YAML: %v\n%s", err, content)
	}
	return m
}

func getPathT(t *testing.T, m map[string]interface{}, path ...string) interface{} {
	t.Helper()
	var cur interface{} = m
	for _, key := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			t.Fatalf("路径 %v 在 %q 处不是映射: %T", path, key, cur)
		}
		cur = mm[key]
	}
	return cur
}

// TestWriteConfigPatchesPreservesFormat 是这次改造的核心：只换目标值，
// 注释、空行、缩进、行内注释都不能动。
func TestWriteConfigPatchesPreservesFormat(t *testing.T) {
	before := "" +
		"# 顶部注释\n" +
		"bilibili:\n" +
		"    SESSDATA: \"old\"   # 你的 b 站 cookie\n" +
		"    interval: 25s\n" +
		"\n" +
		"# 下一个段\n" +
		"other:\n" +
		"    key: value\n"
	path := writeFileT(t, before)

	err := writeConfigPatchesForTest([]ConfigPatch{{Path: []string{"bilibili", "SESSDATA"}, Value: "new-cookie"}}, path)
	if err != nil {
		t.Fatal(err)
	}
	after := readFileT(t, path)

	want := strings.Replace(before, `"old"   # 你的 b 站 cookie`, `"new-cookie" # 你的 b 站 cookie`, 1)
	if after != want {
		t.Fatalf("应只替换值、保留其余格式\n--- want ---\n%q\n--- got ---\n%q", want, after)
	}
}

func TestWriteConfigPatchesNestedScalar(t *testing.T) {
	path := writeFileT(t, "platform:\n  bilibili:\n    enable: true\n  weibo:\n    enable: true\n")

	if err := writeConfigPatchesForTest([]ConfigPatch{{Path: []string{"platform", "bilibili", "enable"}, Value: false}}, path); err != nil {
		t.Fatal(err)
	}
	m := parseT(t, readFileT(t, path))
	if got := getPathT(t, m, "platform", "bilibili", "enable"); got != false {
		t.Fatalf("bilibili.enable 应为 false，got %v", got)
	}
	if got := getPathT(t, m, "platform", "weibo", "enable"); got != true {
		t.Fatalf("weibo.enable 不应被改动，got %v", got)
	}
}

// `app-id:` 后面本来没空格时，写入必须补一个空格；
// 否则 `app-id:"123"` 会被 YAML 当成普通标量，配置项静默失效。
func TestWriteConfigPatchesAddsSpaceAfterColon(t *testing.T) {
	path := writeFileT(t, "qqofficial:\n  app-id:\n  mode: websocket\n")

	if err := writeConfigPatchesForTest([]ConfigPatch{
		{Path: []string{"qqofficial", "app-id"}, Value: "10293045"},
	}, path); err != nil {
		t.Fatal(err)
	}
	m := parseT(t, readFileT(t, path))
	if got := getPathT(t, m, "qqofficial", "app-id"); got != "10293045" {
		t.Fatalf("qqofficial.app-id = %v", got)
	}
}

func TestWriteConfigPatchesCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.yaml")

	if err := writeConfigPatchesForTest([]ConfigPatch{
		{Path: []string{"admin", "token"}, Value: "secret123"},
	}, path); err != nil {
		t.Fatal(err)
	}
	m := parseT(t, readFileT(t, path))
	if got := getPathT(t, m, "admin", "token"); got != "secret123" {
		t.Fatalf("admin.token = %v", got)
	}
}

func TestWriteConfigPatchesInsertMissingSection(t *testing.T) {
	path := writeFileT(t, "bot:\n  commandPrefix: \"/\"\n")

	patches := []ConfigPatch{
		{Path: []string{"adapter", "mode"}, Value: "qqofficial"},
		{Path: []string{"qqofficial", "app-id"}, Value: "10293045"},
		{Path: []string{"qqofficial", "app-secret"}, Value: "secret"},
	}
	if err := writeConfigPatchesForTest(patches, path); err != nil {
		t.Fatal(err)
	}
	after := readFileT(t, path)
	m := parseT(t, after)
	if got := getPathT(t, m, "adapter", "mode"); got != "qqofficial" {
		t.Fatalf("adapter.mode = %v", got)
	}
	if got := getPathT(t, m, "qqofficial", "app-id"); got != "10293045" {
		t.Fatalf("qqofficial.app-id = %v", got)
	}
	if !strings.Contains(after, "bot:\n  commandPrefix: \"/\"\n") {
		t.Fatalf("原有内容不应被破坏:\n%s", after)
	}
}

// TestWriteConfigPatchesReplaceMap 覆盖 customCommandPrefix 这类「整表替换」。
func TestWriteConfigPatchesReplaceMap(t *testing.T) {
	before := "customCommandPrefix:\n  签到: \"\"\n  帮助: \"!\"\n# 段尾注释\nbot:\n  commandPrefix: \"/\"\n"
	path := writeFileT(t, before)

	if err := writeConfigPatchesForTest([]ConfigPatch{{
		Path:  []string{"customCommandPrefix"},
		Value: map[string]interface{}{"签到": ""},
	}}, path); err != nil {
		t.Fatal(err)
	}
	after := readFileT(t, path)
	m := parseT(t, after)
	prefixes, _ := getPathT(t, m, "customCommandPrefix").(map[string]interface{})
	if len(prefixes) != 1 {
		t.Fatalf("应整表替换为 1 项，got %v\n%s", prefixes, after)
	}
	if _, ok := prefixes["帮助"]; ok {
		t.Fatalf("删掉的命令不应残留: %v", prefixes)
	}
	if !strings.Contains(after, "# 段尾注释") {
		t.Fatalf("注释不应丢失:\n%s", after)
	}
}

func TestWriteConfigPatchesClearMap(t *testing.T) {
	path := writeFileT(t, "customCommandPrefix:\n  签到: \"\"\n  帮助: \"!\"\n")

	if err := writeConfigPatchesForTest([]ConfigPatch{{
		Path:  []string{"customCommandPrefix"},
		Value: map[string]interface{}{},
	}}, path); err != nil {
		t.Fatal(err)
	}
	after := readFileT(t, path)
	m := parseT(t, after)
	prefixes, _ := getPathT(t, m, "customCommandPrefix").(map[string]interface{})
	if len(prefixes) != 0 {
		t.Fatalf("应清空整表，got %v\n%s", prefixes, after)
	}
}

func TestWriteConfigPatchesReplaceList(t *testing.T) {
	path := writeFileT(t, "twitter:\n  baseUrl:\n    - \"https://a/\"\n    - \"https://b/\"\n  interval: 30s\n")

	if err := writeConfigPatchesForTest([]ConfigPatch{{
		Path:  []string{"twitter", "baseUrl"},
		Value: []string{"https://c/", "https://d/"},
	}}, path); err != nil {
		t.Fatal(err)
	}
	m := parseT(t, readFileT(t, path))
	list, _ := getPathT(t, m, "twitter", "baseUrl").([]interface{})
	if len(list) != 2 || list[0] != "https://c/" || list[1] != "https://d/" {
		t.Fatalf("列表应被替换，got %v", list)
	}
	if got := getPathT(t, m, "twitter", "interval"); got != "30s" {
		t.Fatalf("同级键不应被破坏，got %v", got)
	}
}

func TestWriteConfigPatchesPreservesCRLF(t *testing.T) {
	before := "bilibili:\r\n  SESSDATA: \"old\"\r\n  interval: 25s\r\n"
	path := writeFileT(t, before)

	if err := writeConfigPatchesForTest([]ConfigPatch{{Path: []string{"bilibili", "SESSDATA"}, Value: "new"}}, path); err != nil {
		t.Fatal(err)
	}
	after := readFileT(t, path)
	if !strings.Contains(after, "SESSDATA: \"new\"\r\n") {
		t.Fatalf("应保留 CRLF，got %q", after)
	}
	if strings.Contains(after, "\n\n") || strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\n") {
		t.Fatalf("不应混入裸 LF，got %q", after)
	}
}

func TestWriteConfigPatchesRejectsBrokenExisting(t *testing.T) {
	const broken = "bot:\n  commandPrefix: [unclosed\n"
	path := writeFileT(t, broken)

	err := writeConfigPatchesForTest([]ConfigPatch{{Path: []string{"bot", "commandPrefix"}, Value: "!"}}, path)
	if err == nil {
		t.Fatal("坏配置应拒绝写入")
	}
	if got := readFileT(t, path); got != broken {
		t.Fatalf("坏配置不应被覆盖:\n%s", got)
	}
}

func TestFlattenConfigPatch(t *testing.T) {
	patches := FlattenConfigPatch(map[string]interface{}{
		"adapter": map[string]interface{}{"mode": "qqofficial"},
		"qqofficial": map[string]interface{}{
			"app-id":     "1",
			"app-secret": "s",
		},
		"customCommandPrefix": map[string]interface{}{},
		"template":            map[string]interface{}{"enable": true},
	})
	got := map[string]interface{}{}
	for _, p := range patches {
		got[strings.Join(p.Path, ".")] = p.Value
	}
	if got["adapter.mode"] != "qqofficial" {
		t.Fatalf("adapter.mode 未展开: %v", got)
	}
	if got["qqofficial.app-id"] != "1" || got["qqofficial.app-secret"] != "s" {
		t.Fatalf("qqofficial 未展开: %v", got)
	}
	// 空 map 是叶子，不能展开成「什么都不写」
	if _, ok := got["customCommandPrefix"]; !ok {
		t.Fatalf("空 map 应作为叶子保留: %v", got)
	}
	if got["template.enable"] != true {
		t.Fatalf("template.enable 未展开: %v", got)
	}
}

// TestWriteConfigPatchesOnDefaultConfig 用仓库里真正的默认配置做回归：
// 改完 adapter/QQ 段后必须仍是合法 YAML，注释与空行不能被吃掉。
func TestWriteConfigPatchesOnDefaultConfig(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("..", "..", "dist", "application.yaml"))
	if err != nil {
		t.Skipf("默认配置不存在，跳过: %v", err)
	}
	path := writeFileT(t, string(original))

	patches := []ConfigPatch{
		{Path: []string{"adapter", "mode"}, Value: "qqofficial"},
		{Path: []string{"qqofficial", "app-id"}, Value: "10293045"},
		{Path: []string{"qqofficial", "app-secret"}, Value: "secret"},
		{Path: []string{"platform", "bilibili", "enable"}, Value: false},
	}
	if err := writeConfigPatchesForTest(patches, path); err != nil {
		t.Fatal(err)
	}
	after := readFileT(t, path)
	m := parseT(t, after)

	if got := getPathT(t, m, "adapter", "mode"); got != "qqofficial" {
		t.Fatalf("adapter.mode = %v", got)
	}
	if got := getPathT(t, m, "qqofficial", "app-id"); got != "10293045" {
		t.Fatalf("qqofficial.app-id = %v", got)
	}
	if got := getPathT(t, m, "platform", "bilibili", "enable"); got != false {
		t.Fatalf("platform.bilibili.enable = %v", got)
	}

	// 默认配置里大量说明性注释必须还在（抽查几条有代表性的）
	for _, needle := range []string{
		"# 命令前缀，默认 \"/\"",
		"# 是否启用离线缓存",
		"# 你的 b 站 cookie",
		"# 定时任务列表",
	} {
		if !strings.Contains(after, needle) {
			t.Fatalf("注释 %q 不应丢失", needle)
		}
	}
	// 空行数量不能大幅减少（整份重写会全部吃掉）
	beforeBlank := strings.Count(string(original), "\n\n")
	afterBlank := strings.Count(after, "\n\n")
	if afterBlank < beforeBlank-2 {
		t.Fatalf("空行被大量删除：before=%d after=%d", beforeBlank, afterBlank)
	}
}

func writeConfigPatchesForTest(patches []ConfigPatch, path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := RenderConfigPatches(data, patches)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}
