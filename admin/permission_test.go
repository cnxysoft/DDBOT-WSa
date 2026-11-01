package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
)

// newPermTestServer 构造带鉴权的测试服务器，绕过 token 中间件直接测 handler。
func newPermTestServer() (*Server, *httptest.Server) {
	s := &Server{token: "t"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/permissions/overview", s.handlePermissionsOverview)
	mux.HandleFunc("/api/v1/permissions/admin", s.handleAdminAction)
	mux.HandleFunc("/api/v1/permissions/group-admins", s.handleGroupAdmins)
	mux.HandleFunc("/api/v1/permissions/group-admin", s.handleGroupAdminAction)
	mux.HandleFunc("/api/v1/permissions/silence", s.handleSilence)
	mux.HandleFunc("/api/v1/permissions/blocklist", s.handleBlockList)
	mux.HandleFunc("/api/v1/commands", s.handleCommands)
	mux.HandleFunc("/api/v1/commands/global", s.handleCommandGlobal)
	mux.HandleFunc("/api/v1/commands/group", s.handleCommandGroup)
	ts := httptest.NewServer(mux)
	return s, ts
}

func TestPermissionAPI(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	lsp.Instance.PermissionStateManager.FreshIndex() // ListXxx 依赖 buntdb 索引
	_, ts := newPermTestServer()
	defer ts.Close()
	sm := lsp.Instance.PermissionStateManager

	t.Run("管理员授予与撤销", func(t *testing.T) {
		// 授予
		resp, err := http.Post(ts.URL+"/api/v1/permissions/admin", "application/json",
			strings.NewReader(`{"qq":10001,"action":"grant"}`))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("grant status %d", resp.StatusCode)
		}
		if !sm.CheckAdmin(10001) {
			t.Fatal("CheckAdmin 应为 true")
		}
		// 重复授予幂等
		resp, _ = http.Post(ts.URL+"/api/v1/permissions/admin", "application/json",
			strings.NewReader(`{"qq":10001,"action":"grant"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("重复授予应幂等，got %d", resp.StatusCode)
		}
		// 撤销
		resp, _ = http.Post(ts.URL+"/api/v1/permissions/admin", "application/json",
			strings.NewReader(`{"qq":10001,"action":"ungrant"}`))
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 || sm.CheckAdmin(10001) {
			t.Fatal("撤销后 CheckAdmin 应为 false")
		}
	})

	t.Run("群管理员", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/api/v1/permissions/group-admin", "application/json",
			strings.NewReader(`{"groupCode":20001,"qq":10002,"action":"grant"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !sm.CheckGroupAdmin(20001, 10002) {
			t.Fatalf("群管理员授予失败 status=%d", resp.StatusCode)
		}
		resp, _ = http.Get(ts.URL + "/api/v1/permissions/group-admins?groupCode=20001")
		var body map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("群管理员列表 status=%d", resp.StatusCode)
		}
		resp, _ = http.Post(ts.URL+"/api/v1/permissions/group-admin", "application/json",
			strings.NewReader(`{"groupCode":20001,"qq":10002,"action":"ungrant"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 || sm.CheckGroupAdmin(20001, 10002) {
			t.Fatal("群管理员撤销失败")
		}
	})

	t.Run("全局与群级静默", func(t *testing.T) {
		resp, _ := http.Post(ts.URL+"/api/v1/permissions/silence", "application/json",
			strings.NewReader(`{"scope":"group","groupCode":20002,"action":"on"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 || !sm.CheckGroupSilence(20002) {
			t.Fatal("群静默开启失败")
		}
		resp, _ = http.Post(ts.URL+"/api/v1/permissions/silence", "application/json",
			strings.NewReader(`{"scope":"group","groupCode":20002,"action":"off"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 || sm.CheckGroupSilence(20002) {
			t.Fatal("群静默关闭失败")
		}
	})

	t.Run("黑名单增删与列表", func(t *testing.T) {
		resp, _ := http.Post(ts.URL+"/api/v1/permissions/blocklist", "application/json",
			strings.NewReader(`{"qq":10003,"action":"add","durationSec":60}`))
		var body map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !sm.CheckBlockList(10003) {
			t.Fatal("黑名单添加失败")
		}
		found := false
		for _, v := range body["blockList"].([]interface{}) {
			if int64(v.(float64)) == 10003 {
				found = true
			}
		}
		if !found {
			t.Fatal("黑名单列表应包含 10003")
		}
		resp, _ = http.Post(ts.URL+"/api/v1/permissions/blocklist", "application/json",
			strings.NewReader(`{"qq":10003,"action":"remove"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 || sm.CheckBlockList(10003) {
			t.Fatal("黑名单移除失败")
		}
	})

	t.Run("命令开关全局", func(t *testing.T) {
		resp, _ := http.Post(ts.URL+"/api/v1/commands/global", "application/json",
			strings.NewReader(`{"command":"roll","action":"disable"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 || !sm.CheckGlobalCommandDisabled("roll") {
			t.Fatal("全局禁用 roll 失败")
		}
		resp, _ = http.Post(ts.URL+"/api/v1/commands/global", "application/json",
			strings.NewReader(`{"command":"roll","action":"enable"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 || sm.CheckGlobalCommandDisabled("roll") {
			t.Fatal("全局恢复 roll 失败")
		}
	})

	t.Run("命令开关群级", func(t *testing.T) {
		resp, _ := http.Post(ts.URL+"/api/v1/commands/group", "application/json",
			strings.NewReader(`{"groupCode":20003,"command":"list","action":"disable"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 || !sm.CheckGroupCommandDisabled(20003, "list") {
			t.Fatal("群级禁用 list 失败")
		}
		resp, _ = http.Post(ts.URL+"/api/v1/commands/group", "application/json",
			strings.NewReader(`{"groupCode":20003,"command":"list","action":"enable"}`))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("群级恢复失败 status=%d", resp.StatusCode)
		}
		_ = sm.DisableGroupCommand(20003, "list") // 清理
	})

	t.Run("命令列表", func(t *testing.T) {
		resp, _ := http.Get(ts.URL + "/api/v1/commands?groupCode=20003")
		var body struct {
			Group   []CommandStatus `json:"group"`
			Private []CommandStatus `json:"private"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(body.Group) == 0 || len(body.Private) == 0 {
			t.Fatalf("命令列表异常 group=%d private=%d", len(body.Group), len(body.Private))
		}
	})

	t.Run("总览", func(t *testing.T) {
		resp, _ := http.Get(ts.URL + "/api/v1/permissions/overview")
		var body map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal("overview 失败")
		}
		if _, ok := body["admins"]; !ok {
			t.Fatal("overview 缺 admins")
		}
	})

	t.Run("非法请求拒绝", func(t *testing.T) {
		resp, _ := http.Post(ts.URL+"/api/v1/permissions/admin", "application/json",
			strings.NewReader(`{"qq":0,"action":"grant"}`))
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("qq=0 应 400，got %d", resp.StatusCode)
		}
		resp, _ = http.Post(ts.URL+"/api/v1/permissions/admin", "application/json",
			strings.NewReader(`{"qq":1,"action":"hack"}`))
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("未知 action 应 400，got %d", resp.StatusCode)
		}
	})
}

// 权限数据清理：测试直接操作全局 lsp.Instance 的 StateManager（buntdb 内存/文件），
// 用例自身保证 grant 后 ungrant 还原。
var _ = permission.Admin
