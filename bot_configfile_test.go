package DDBOT

import (
	"os"
	"path/filepath"
	"testing"
)

// configFileInSearchPath 必须与 viper 的搜索规则逐条对齐：
// 先 "." 后 "./config"（CWD 优先）、SupportedExts 里的扩展名都算、目录不算、无扩展名的 application 也算。
// 少认一种形态，首次运行就会在 CWD 生成一份示例配置，而 viper 优先读 CWD ——
// 用户真正写的那份被静默顶掉。
func TestConfigFileInSearchPath(t *testing.T) {
	write := func(t *testing.T, dir string, rels ...string) {
		t.Helper()
		for _, rel := range rels {
			full := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("a: 1\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("两处都没有", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if p, ok := configFileInSearchPath(); ok {
			t.Fatalf("不应找到配置，got %q", p)
		}
	})

	t.Run("只有 ./config/application.yaml", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, filepath.Join("config", "application.yaml"))
		t.Chdir(dir)

		p, ok := configFileInSearchPath()
		if !ok || p != filepath.Join("config", "application.yaml") {
			t.Fatalf("应找到 ./config 下的配置，got %q ok=%v", p, ok)
		}
	})

	t.Run("只有 ./config/application.yml（viper 认 yml）", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, filepath.Join("config", "application.yml"))
		t.Chdir(dir)

		p, ok := configFileInSearchPath()
		if !ok || p != filepath.Join("config", "application.yml") {
			t.Fatalf("viper 认 .yml，这里也必须认，got %q ok=%v", p, ok)
		}
	})

	t.Run("只有 ./config/application.json（viper 认 json）", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, filepath.Join("config", "application.json"))
		t.Chdir(dir)

		if p, ok := configFileInSearchPath(); !ok || p != filepath.Join("config", "application.json") {
			t.Fatalf("viper 认 .json，这里也必须认，got %q ok=%v", p, ok)
		}
	})

	t.Run("两处都有时 CWD 优先", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "application.yaml", filepath.Join("config", "application.yaml"))
		t.Chdir(dir)

		p, ok := configFileInSearchPath()
		if !ok || p != "application.yaml" {
			t.Fatalf("CWD 应优先，got %q ok=%v", p, ok)
		}
	})

	t.Run("同名目录不算配置", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "config", "application.yaml"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(dir)

		// viper 的 exists 用 !IsDir 判定：目录不算配置。
		// 这里若误判成「找到了」，Run() 就不会生成示例，而 viper 也读不到目录，启动直接失败。
		if p, ok := configFileInSearchPath(); ok {
			t.Fatalf("目录不该被当成配置，got %q", p)
		}
	})
}
