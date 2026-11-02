package weibo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 与 lsp/cfg 的 yamlutil 同一个坑：CRLF 配置写回后不能变成混合换行。
// 群友在 Windows 上编辑的配置是 CRLF，扫码登录写回 sub 时曾经只把那两行改成 LF。
func TestWriteBackConfigPreservesCRLF(t *testing.T) {
	before := "weibo:\r\n  qrlogin: true\r\n  sub: \"old\"\r\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "application.yaml")
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}

	if err := writeBackConfigToPath("new-sub", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after := string(data)

	want := strings.Replace(before, "\"old\"", "\"new-sub\"", 1)
	if after != want {
		t.Fatalf("CRLF 配置应逐字节保留、只换值\n--- want ---\n%q\n--- got ---\n%q", want, after)
	}
	if lf := strings.Count(after, "\n") - strings.Count(after, "\r\n"); lf != 0 {
		t.Fatalf("不该出现纯 LF 行，got %d 行", lf)
	}
}

// 往 CRLF 配置里插新键也要用 CRLF。
func TestWriteBackConfigInsertsCRLF(t *testing.T) {
	before := "weibo:\r\n  qrlogin: true\r\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "application.yaml")
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}

	if err := writeBackConfigToPath("new-sub", path); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	data, _ := os.ReadFile(path)
	after := string(data)

	if lf := strings.Count(after, "\n") - strings.Count(after, "\r\n"); lf != 0 {
		t.Fatalf("新键也应使用 CRLF，纯 LF 行 = %d\n%q", lf, after)
	}
	if !strings.Contains(after, "sub: \"new-sub\"") {
		t.Fatalf("应写入 sub\n%q", after)
	}
}
