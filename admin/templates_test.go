package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTemplateTestServer() *httptest.Server {
	s := &Server{token: "t"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/templates", s.handleTemplates)
	mux.HandleFunc("/api/v1/templates/detail", s.handleTemplateDetail)
	mux.HandleFunc("/api/v1/templates/save", s.handleTemplateSave)
	mux.HandleFunc("/api/v1/templates/reset", s.handleTemplateReset)
	mux.HandleFunc("/api/v1/templates/preview", s.handleTemplatePreview)
	return httptest.NewServer(mux)
}

// withTemplateDir 把自定义模板目录指到临时目录并在结束后清理。
func withTemplateDir(t *testing.T) string {
	dir := t.TempDir()
	old := templateDirPath
	// templateDirPath 是常量，不能改——测试改为 chdir 到临时目录
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "template"), 0766); err != nil {
		t.Fatal(err)
	}
	_ = old
	return dir
}

func TestTemplateNameSafety(t *testing.T) {
	for _, bad := range []string{"../../etc/passwd", "a/b", "..", "noDotName", "UPPER", "", "a b"} {
		if _, ok := safeTemplateName(bad); ok {
			t.Fatalf("非法名 %q 通过了校验", bad)
		}
	}
	for _, good := range []string{"notify.group.bilibili.live", "command.group.help", "trigger.private.poke"} {
		if _, ok := safeTemplateName(good); !ok {
			t.Fatalf("合法名 %q 被拒绝", good)
		}
	}
}

func TestTemplateListAndDetail(t *testing.T) {
	withTemplateDir(t)
	ts := newTemplateTestServer()
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/api/v1/templates")
	var body struct {
		Templates []TemplateItem `json:"templates"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(body.Templates) < 20 {
		t.Fatalf("模板列表异常 status=%d len=%d", resp.StatusCode, len(body.Templates))
	}
	found := false
	for _, it := range body.Templates {
		if it.Name == "notify.group.bilibili.live" && !it.Customized && it.Category == "notify" {
			found = true
		}
	}
	if !found {
		t.Fatal("默认模板 notify.group.bilibili.live 缺失")
	}

	// detail：默认内容非空
	resp, _ = http.Get(ts.URL + "/api/v1/templates/detail?name=notify.group.bilibili.live")
	var d struct {
		Content string `json:"content"`
		Default string `json:"default"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()
	if resp.StatusCode != 200 || d.Content == "" || d.Content != d.Default {
		t.Fatal("模板详情异常")
	}
	// 非法名拒绝
	resp, _ = http.Get(ts.URL + "/api/v1/templates/detail?name=../evil")
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("路径穿越应 400，got %d", resp.StatusCode)
	}
}

func TestTemplateSaveValidateAndReset(t *testing.T) {
	withTemplateDir(t)
	ts := newTemplateTestServer()
	defer ts.Close()

	save := func(body string) *http.Response {
		resp, _ := http.Post(ts.URL+"/api/v1/templates/save", "application/json", strings.NewReader(body))
		return resp
	}

	// 语法错误拒绝且不落盘
	resp := save(`{"name":"notify.group.bilibili.live","content":"{{ .broken"}`)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("坏语法应 400，got %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join("template", "notify.group.bilibili.live.tmpl")); !os.IsNotExist(err) {
		t.Fatal("坏语法不应落盘")
	}

	// 合法模板保存
	resp = save(`{"name":"notify.group.bilibili.live","content":"自定义 {{ .LiveInfo.Nickname }}"}`)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("保存失败 status=%d", resp.StatusCode)
	}

	// detail 显示自定义
	resp, _ = http.Get(ts.URL + "/api/v1/templates/detail?name=notify.group.bilibili.live")
	var d struct {
		Content    string `json:"content"`
		Customized bool   `json:"customized"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()
	if !d.Customized || d.Content != "自定义 {{ .LiveInfo.Nickname }}" {
		t.Fatal("自定义未生效")
	}

	// 列表标记 customized
	resp, _ = http.Get(ts.URL + "/api/v1/templates")
	var list struct {
		Templates []TemplateItem `json:"templates"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	marked := false
	for _, it := range list.Templates {
		if it.Name == "notify.group.bilibili.live" && it.Customized {
			marked = true
		}
	}
	if !marked {
		t.Fatal("列表未标记 customized")
	}

	// 重置
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/templates/reset?name=notify.group.bilibili.live", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("重置失败 status=%d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join("template", "notify.group.bilibili.live.tmpl")); !os.IsNotExist(err) {
		t.Fatal("重置后自定义文件应删除")
	}

	// 重复重置 → 404
	req, _ = http.NewRequest("DELETE", ts.URL+"/api/v1/templates/reset?name=notify.group.bilibili.live", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("重复重置应 404，got %d", resp.StatusCode)
	}
}

func TestTemplatePreview(t *testing.T) {
	withTemplateDir(t)
	ts := newTemplateTestServer()
	defer ts.Close()

	post := func(body string) (int, string) {
		resp, err := http.Post(ts.URL+"/api/v1/templates/preview", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// 1) 默认直播模板：应渲染出中文文案（getTime/pic 生效，图片折叠）
	st, body := post(`{"name":"notify.group.bilibili.live","content":"{{ if .living -}}{{ .name }}正在直播【{{ .title }}】{{ .parent_area_name }} - {{ .area_name }}{{ .url }}{{ pic .cover }}{{- else -}}下播{{- end }}"}`)
	if st != 200 {
		t.Fatalf("预览失败 status=%d body=%s", st, body)
	}
	for _, want := range []string{"星野源正在直播", "【歌回】深夜弹唱", "网络游戏", "[img]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("预览结果缺少 %q: %s", want, body)
		}
	}

	// 2) 坏语法 → 400
	st, body = post(`{"name":"notify.group.bilibili.live","content":"{{ .broken"}`)
	if st != 400 {
		t.Fatalf("坏语法应 400，got %d %s", st, body)
	}

	// 3) 非法名 → 400
	st, _ = post(`{"name":"../evil","content":"x"}`)
	if st != 400 {
		t.Fatalf("路径穿越应 400，got %d", st)
	}

	// 4) 变量路径构造：嵌套 .dynamic.Video.Action
	st, body = post(`{"name":"notify.group.bilibili.news","content":"{{ .dynamic.Video.Action }}"}`)
	if st != 200 || !strings.Contains(body, "发布了动态") {
		t.Fatalf("嵌套变量预览异常 status=%d body=%s", st, body)
	}
}
