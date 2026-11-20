package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/ghodss/yaml"
)

// withConfigFile 把 viper 的配置文件路径指到临时文件，结束后还原。
// 配置读写一律走 cfg.ConfigFilePath()，所以这里必须走 SetConfigFile，
// 而不是 chdir——chdir 只能骗过硬编码路径，正是被修掉的那种写法。
func withConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.yaml")
	// 总是落盘：空串 = 0 字节文件（这条路径也要测），不存在的情况另行构造
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	prev := config.GlobalConfig.ConfigFileUsed()
	config.GlobalConfig.SetConfigFile(path)
	t.Cleanup(func() { config.GlobalConfig.SetConfigFile(prev) })
	return path
}

func postConfig(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleConfig(rec, req)
	return rec
}

func readConfigMap(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[string]interface{})
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("写出的配置不是合法 YAML: %v\n%s", err, data)
	}
	return m
}

// TestHandleConfigPostReplacesCustomCommandPrefix 覆盖 customCommandPrefix 的整表替换语义：
// mergeConfig 是递归合并，不做特判的话用户在面板里删掉的命令会一直留在配置里。
func TestHandleConfigPostReplacesCustomCommandPrefix(t *testing.T) {
	path := withConfigFile(t, "bot:\n  commandPrefix: \"/\"\ncustomCommandPrefix:\n  签到: \"\"\n  帮助: \"!\"\n")

	rec := postConfig(t, "{\"config\":{\"customCommandPrefix\":{\"签到\":\"\"}}}")
	if rec.Code != http.StatusOK {
		t.Fatalf("保存应成功，got %d: %s", rec.Code, rec.Body.String())
	}

	got := readConfigMap(t, path)
	prefixes, ok := got["customCommandPrefix"].(map[string]interface{})
	if !ok {
		t.Fatalf("customCommandPrefix 应为映射，got %T: %v", got["customCommandPrefix"], got["customCommandPrefix"])
	}
	if len(prefixes) != 1 {
		t.Fatalf("应整表替换为 1 项，got %d: %v", len(prefixes), prefixes)
	}
	if _, exists := prefixes["帮助"]; exists {
		t.Fatalf("删掉的命令不应残留: %v", prefixes)
	}
	// 其他键仍走合并，不能被这次替换带走
	bot, _ := got["bot"].(map[string]interface{})
	if bot == nil || bot["commandPrefix"] != "/" {
		t.Fatalf("其他配置键不应丢失: %v", got)
	}
}

// TestHandleConfigPostRejectsBrokenYAML 覆盖「现有配置语法坏掉」这条路径：
// 解析失败时若继续合并写回，existing 停在空 map，整份配置会被清空。
func TestHandleConfigPostRejectsBrokenYAML(t *testing.T) {
	const broken = "bot:\n  commandPrefix: [unclosed\n"
	path := withConfigFile(t, broken)

	rec := postConfig(t, "{\"config\":{\"bot\":{\"commandPrefix\":\"!\"}}}")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("坏配置应拒绝写入并返回 500，got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已拒绝写入") {
		t.Fatalf("错误信息应说明原因，got %s", rec.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != broken {
		t.Fatalf("坏配置不应被覆盖：\n--- want ---\n%s\n--- got ---\n%s", broken, after)
	}
}

// TestHandleSetupRejectsBrokenYAML 同一条规则接到初始化向导上：向导只写白名单键，
// 但解析失败时 existing 为空，会把用户原有配置整份抹掉。
func TestHandleSetupRejectsBrokenYAML(t *testing.T) {
	const broken = "bot:\n  commandPrefix: [unclosed\n"
	path := withConfigFile(t, broken)

	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader("{\"token\":\"secret123\"}"))
	rec := httptest.NewRecorder()
	s.handleSetup(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("坏配置应拒绝写入并返回 500，got %d: %s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != broken {
		t.Fatalf("坏配置不应被覆盖：\n--- want ---\n%s\n--- got ---\n%s", broken, after)
	}
}

// TestHandleConfigPostKeepsUntouchedCustomCommandPrefix 覆盖「配置本来就是坏的」这条现实路径：
// 全角冒号让 customCommandPrefix 退化成字符串，而整份 YAML 本身是合法的，
// 所以解析不会报错。用户只想改别的设置，保存后这个坏值必须原样留着，不能被顺手改写。
func TestHandleConfigPostKeepsUntouchedCustomCommandPrefix(t *testing.T) {
	const raw = "bot:\n  commandPrefix: \"/\"\ncustomCommandPrefix:\n  签到：\"\"\n"
	path := withConfigFile(t, raw)

	rec := postConfig(t, "{\"config\":{\"bot\":{\"commandPrefix\":\"!\"}}}")
	if rec.Code != http.StatusOK {
		t.Fatalf("保存应成功，got %d: %s", rec.Code, rec.Body.String())
	}

	got := readConfigMap(t, path)
	if _, ok := got["customCommandPrefix"].(string); !ok {
		t.Fatalf("坏值应原样保留为字符串，got %T: %v", got["customCommandPrefix"], got["customCommandPrefix"])
	}
	bot, _ := got["bot"].(map[string]interface{})
	if bot == nil || bot["commandPrefix"] != "!" {
		t.Fatalf("本次改动应生效: %v", got)
	}
}

// TestHandleConfigPostToleratesEmptyConfigFile 覆盖「文件存在但内容为空」这条路径：
// ghodss/yaml 对空内容/纯注释/字面 null 不报错，却会把目标 map 置成 nil，
// 之后 mergeConfig 写 nil map 会 panic——HTTP handler 里就是连接被直接掐断。
func TestHandleConfigPostToleratesEmptyConfigFile(t *testing.T) {
	for _, content := range []string{"", "# 只有注释\n", "null\n", "{}\n"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			path := withConfigFile(t, content)
			rec := postConfig(t, "{\"config\":{\"bot\":{\"commandPrefix\":\"!\"}}}")
			if rec.Code != http.StatusOK {
				t.Fatalf("应正常写入，got %d: %s", rec.Code, rec.Body.String())
			}
			got := readConfigMap(t, path)
			bot, _ := got["bot"].(map[string]interface{})
			if bot == nil || bot["commandPrefix"] != "!" {
				t.Fatalf("改动应落盘: %v", got)
			}
		})
	}
}

// TestHandleSetupToleratesEmptyConfigFile 与 handleConfig 同一条兜底：
// 空内容 / 纯注释 / 字面 null 会被解析成 nil map，setNested 往 nil map 写会 panic。
func TestHandleSetupToleratesEmptyConfigFile(t *testing.T) {
	for _, content := range []string{"", "# 只有注释\n", "null\n"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			path := withConfigFile(t, content)

			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader("{\"token\":\"secret123\"}"))
			rec := httptest.NewRecorder()
			s.handleSetup(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("应正常写入，got %d: %s", rec.Code, rec.Body.String())
			}
			got := readConfigMap(t, path)
			adm, _ := got["admin"].(map[string]interface{})
			if adm == nil || adm["token"] != "secret123" {
				t.Fatalf("token 应落盘: %v", got)
			}
		})
	}
}

// TestHandleConfigGetReturnsObjectForEmptyFile 空配置的 GET 必须返回 {} 而不是 null：
// 返回 null 时前端 configCache 变成 null，高级页的整份 JSON 编辑框拿不到值，
// 保存必报「配置格式有误」。
func TestHandleConfigGetReturnsObjectForEmptyFile(t *testing.T) {
	withConfigFile(t, "")

	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，got %d: %s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "{}" {
		t.Fatalf("空配置应返回 {}，got %s", body)
	}
}
