package qqofficial

// 本文件钉住两条**暂时性**产品决策：
//
//  1. 默认状态文件名带 appId，并从旧的无 appId 文件做一次性迁移；
//  2. 状态刷盘间隔 2 秒。
//
// 依据（为什么这么定）：openid 是 per-bot 的 —— 同一个群，个人机器人看到
// 0F6E696A…，企业机器人看到 B97B93A3…，拿旧 openid 去问新机器人会得到 11255
// 「请求的资源不存在」。所以换机器人之后，旧状态文件里的映射与群清单对新机器人
// 全部失效，却仍会被当成本机器人的群继续使用。文件名带上 appId 之后，换机器人
// 天然是一个干净文件，旧文件原地不动、可回退。
//
// 迁移的唯一意义是「老用户升级上来内部 ID 一个都不变」：lsp 的群权限与订阅是按
// 内部 ID 存在 .lsp.db 里的，编号从 1 重排会把旧权限与订阅静默迁到别的群。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// legacyStateFileName 是旧版默认状态文件名（不含 appId）。
//
// 这里故意写字面量而不是引用实现里的常量：这条名字属于**历史文件格式**，
// 实现里的常量哪天被重命名，测试也不该跟着漂 —— 它守的就是「磁盘上那个老名字」。
const legacyStateFileName = ".qqofficial-state.json"

// setQQOfficialConfig 改 qqofficial.* 字符串配置，并在测试结束还原。
func setQQOfficialConfig(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		key := ConfigKeyPrefix + "." + k
		old := config.GlobalConfig.GetString(key)
		config.GlobalConfig.Set(key, v)
		t.Cleanup(func() { config.GlobalConfig.Set(key, old) })
	}
}

// writeStateFileAt 按老版本落盘的样子直接写一份状态文件。
func writeStateFileAt(t *testing.T, path, appID string, ids map[string]int64, groups []GroupRecord) {
	t.Helper()
	st := stateFile{Version: stateFileVersion, AppID: appID, IDMap: ids, Groups: groups}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("序列化状态文件失败：%v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("写状态文件 %s 失败：%v", path, err)
	}
}

// newDefaultPathAdapter 在 dir 下按**默认路径**（不配 qqofficial.state-file）建 adapter。
func newDefaultPathAdapter(t *testing.T) *QQOfficialAdapter {
	t.Helper()
	setQQOfficialConfig(t, map[string]string{
		"state-file": "",
		"app-id":     testAppID,
		"app-secret": "test-secret",
	})
	a, ok := NewQQOfficialAdapter(&adapter.AdapterConfig{Mode: string(adapter.AdapterTypeQQOfficial)}).(*QQOfficialAdapter)
	if !ok || a == nil {
		t.Fatal("NewQQOfficialAdapter 必须返回 *QQOfficialAdapter")
	}
	return a
}

// 老用户升级：默认文件名带上 appId，旧文件里的一切原样搬过去，内部 ID 一个都不变。
func TestDefaultStateFileMigratesLegacyFileKeepsIDs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	legacy := filepath.Join(dir, legacyStateFileName)
	writeStateFileAt(t, legacy, testAppID,
		map[string]int64{
			composeIDKey(testAppID, testGroupOpen): 7,
			composeIDKey(testAppID, testOwnerOpen): 8,
		},
		[]GroupRecord{{OpenID: testGroupOpen, Name: "测试群", MemberCount: 3, InfoKnown: true}},
	)

	buf := captureLogs(t)
	a := newDefaultPathAdapter(t)
	if a.store == nil {
		t.Fatal("默认状态文件不可用，adapter 应能建起存储")
	}

	wantName := ".qqofficial-state-" + testAppID + ".json"
	if got := filepath.Base(a.store.Path()); got != wantName {
		t.Fatalf("默认状态文件名应带 appId：期望 %q，实际 %q（换机器人时会继续吃旧映射）", wantName, got)
	}

	// 这条迁移的全部意义：内部 ID 一个都不能变
	if got := a.ids.ToInt64(testAppID, testGroupOpen); got != 7 {
		t.Errorf("迁移后群的内部 ID 应为旧文件里的 7，实际 %d（编号重排 = 旧权限与订阅静默迁到别的群）", got)
	}
	if got := a.ids.ToInt64(testAppID, testOwnerOpen); got != 8 {
		t.Errorf("迁移后用户的内部 ID 应为旧文件里的 8，实际 %d", got)
	}

	// 群清单也必须跟着搬：官方没有「列出我加入的群」接口，丢了这份就得等群里有动静
	groups, err := a.store.LoadGroups()
	if err != nil || len(groups) != 1 || groups[0].OpenID != testGroupOpen || groups[0].Name != "测试群" {
		t.Errorf("群清单应随迁移一起保留，实际 %+v（err=%v）", groups, err)
	}

	if _, err := os.Stat(wantName); err != nil {
		t.Errorf("迁移后应出现新状态文件 %s：%v", wantName, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("旧的无 appId 文件应被处理掉（改名成 .migrated），实际仍留在 %s（err=%v）", legacy, err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "level=info") || !strings.Contains(logs, "迁移") {
		t.Errorf("迁移必须留一条 Info 说明发生了什么（否则用户不知道自己的文件被搬过），实际日志：%s", logs)
	}
	// 日志里给的是配置/默认值原样的路径（默认就是工作目录下的相对名字）
	if !strings.Contains(logs, wantName) || !strings.Contains(logs, legacyStateFileName) {
		t.Errorf("迁移日志应给出旧、新两个名字，实际日志：%s", logs)
	}
}

// 用户显式配了 qqofficial.state-file：完全按用户给的路径走，不改名、不迁移。
func TestExplicitStateFileSkipsMigration(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	legacy := filepath.Join(dir, legacyStateFileName)
	writeStateFileAt(t, legacy, testAppID,
		map[string]int64{composeIDKey(testAppID, testGroupOpen): 7}, nil)

	explicit := filepath.Join(dir, "my-state.json")
	setQQOfficialConfig(t, map[string]string{
		"state-file": explicit,
		"app-id":     testAppID,
		"app-secret": "test-secret",
	})

	buf := captureLogs(t)
	a, ok := NewQQOfficialAdapter(&adapter.AdapterConfig{Mode: string(adapter.AdapterTypeQQOfficial)}).(*QQOfficialAdapter)
	if !ok || a == nil || a.store == nil {
		t.Fatal("adapter 应能建起存储")
	}
	if got := a.store.Path(); got != explicit {
		t.Fatalf("显式配置的状态文件路径必须原样使用：期望 %q，实际 %q", explicit, got)
	}
	// 显式路径下不许偷看旧文件：ID 必须从 1 开始重新分配
	if got := a.ids.ToInt64(testAppID, testGroupOpen); got != 1 {
		t.Errorf("显式配置时不该读取旧状态文件，内部 ID 应从 1 开始，实际 %d", got)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("显式配置时绝不许动旧文件：%v", err)
	}
	if _, err := os.Stat(".qqofficial-state-" + testAppID + ".json"); !os.IsNotExist(err) {
		t.Errorf("显式配置时不该生成带 appId 的默认文件（err=%v）", err)
	}
	if strings.Contains(buf.String(), "迁移") {
		t.Errorf("显式配置时不该提迁移，实际日志：%s", buf.String())
	}
}

// 旧文件不是合法状态文件：不搬，记 Warn 让用户自己处理。
func TestLegacyStateFileNotMigratedWhenCorrupt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	legacy := filepath.Join(dir, legacyStateFileName)
	const broken = "{ 这不是一份 JSON"
	if err := os.WriteFile(legacy, []byte(broken), 0o600); err != nil {
		t.Fatalf("写坏旧文件失败：%v", err)
	}

	buf := captureLogs(t)
	a := newDefaultPathAdapter(t)

	if _, err := os.Stat(".qqofficial-state-" + testAppID + ".json"); !os.IsNotExist(err) {
		t.Errorf("旧文件解析不了就不许搬，实际生成新文件（err=%v）", err)
	}
	if raw, err := os.ReadFile(legacy); err != nil || string(raw) != broken {
		t.Errorf("旧文件必须原封不动留给用户处理，实际 %q（err=%v）", raw, err)
	}
	logs := buf.String()
	if !strings.Contains(logs, "level=warning") || !strings.Contains(logs, legacyStateFileName) || !strings.Contains(logs, "迁移") {
		t.Errorf("不迁移时必须打一条带路径的 Warn 说明让用户自己处理，实际日志：%s", logs)
	}
	if a == nil {
		t.Fatal("新文件不存在时应按首次运行继续，不该 panic")
	}
}

// 旧文件里的 app_id 与当前配置不符（换过机器人）：同样不搬，记 Warn。
func TestLegacyStateFileNotMigratedWhenAppIDDiffers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	const oldAppID = "1900000009"
	legacy := filepath.Join(dir, legacyStateFileName)
	writeStateFileAt(t, legacy, oldAppID,
		map[string]int64{composeIDKey(oldAppID, testGroupOpen): 7}, nil)

	buf := captureLogs(t)
	newDefaultPathAdapter(t)

	if _, err := os.Stat(".qqofficial-state-" + testAppID + ".json"); !os.IsNotExist(err) {
		t.Errorf("旧文件属于别的机器人（app_id=%s）时不许搬进当前 appID 的文件（err=%v）", oldAppID, err)
	}
	logs := buf.String()
	if !strings.Contains(logs, "level=warning") || !strings.Contains(logs, oldAppID) || !strings.Contains(logs, testAppID) {
		t.Errorf("app_id 不符时的不迁移告警应点出两个 appID，实际日志：%s", logs)
	}
}

// 状态刷盘间隔：60s → 2s。丢了这段就能丢掉这段时间内新分配的内部 ID。
func TestStateFlushIntervalIsTwoSeconds(t *testing.T) {
	if stateFlushInterval != 2*time.Second {
		t.Fatalf("状态刷盘间隔应为 2 秒，实际 %v：进程崩溃会丢掉这段时间内新分配的内部 ID（群权限与订阅的唯一凭据）", stateFlushInterval)
	}
}
