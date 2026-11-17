package qqofficial

// persist.go 的单测：钉住三条契约——写盘留上一版、坏了能从备份恢复、首次运行要吵醒人。
//
// 为什么值得单独写一块：内部 ID 的稳定性全押在这个文件上，而 lsp 的订阅与群权限是按
// 内部 ID 落进 .lsp.db 的。文件一丢，编号从 1 重排，旧权限与订阅就静默迁到别的群。
// 所以「丢了能恢复」和「丢了要吵醒人」这两件事必须有测试守着。

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
)

// captureLogs 把包内 logger 的输出接进内存，供断言日志内容与级别。
// 本包的单测不并行（没有 t.Parallel），临时换 output 是安全的。
type synchronizedLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *synchronizedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
func (b *synchronizedLogBuffer) Reset() { b.mu.Lock(); defer b.mu.Unlock(); b.buffer.Reset() }

func captureLogs(t *testing.T) *synchronizedLogBuffer {
	t.Helper()
	buf := &synchronizedLogBuffer{}
	oldOut := logger.Logger.Out
	oldLevel := logger.Logger.GetLevel()
	oldFormatter := logger.Logger.Formatter
	logger.Logger.SetOutput(buf)
	logger.Logger.SetLevel(logrus.DebugLevel)
	// Assert message text directly on every platform; the default formatter
	// escapes Windows path separators inside quoted log messages.
	logger.Logger.SetFormatter(&logrus.TextFormatter{DisableColors: true, DisableQuote: true})
	t.Cleanup(func() {
		logger.Logger.SetOutput(oldOut)
		logger.Logger.SetLevel(oldLevel)
		logger.Logger.SetFormatter(oldFormatter)
	})
	return buf
}

// newStateStore 建一个落在临时目录、带指定 appID 的状态存储。
func newStateStore(t *testing.T, path, appID string) *JSONStore {
	t.Helper()
	store, err := NewJSONStore(path, appID)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	return store
}

// saveIDs 写一版 idmap，失败即终止。
func saveIDs(t *testing.T, store *JSONStore, m map[string]int64) {
	t.Helper()
	if err := store.Save(m); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// 写盘必须把上一版留成 <path>.bak：备份是完整的整份状态（含群清单），不是半个 JSON。
func TestJSONStoreWriteKeepsPreviousVersionAsBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	bak := path + ".bak"
	store := newStateStore(t, path, testAppID)

	v1 := map[string]int64{composeIDKey(testAppID, testGroupOpen): 1}
	saveIDs(t, store, v1)
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatalf("第一次写盘没有上一版可留，不该有备份文件，实际 err=%v", err)
	}

	// 第二版：补上群清单，再换成两条 idmap
	if err := store.SaveGroups([]GroupRecord{{OpenID: testGroupOpen, Name: "测试群", MemberCount: 7, InfoKnown: true}}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}
	v2 := map[string]int64{
		composeIDKey(testAppID, testGroupOpen): 1,
		composeIDKey(testAppID, testOwnerOpen): 2,
	}
	saveIDs(t, store, v2)

	raw, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("写第二版之后必须留下上一版的备份 %s：%v", bak, err)
	}
	if !json.Valid(raw) {
		t.Fatalf("备份必须是一份完整 JSON，不是半个文件，实际 %q", raw)
	}
	var bakState stateFile
	if err := json.Unmarshal(raw, &bakState); err != nil {
		t.Fatalf("备份应能解析成状态文件：%v", err)
	}
	if len(bakState.IDMap) != 1 || bakState.IDMap[composeIDKey(testAppID, testGroupOpen)] != 1 {
		t.Errorf("备份应是上一版的 idmap（只该有群那一条），实际 %+v", bakState.IDMap)
	}
	if len(bakState.Groups) != 1 || bakState.Groups[0].Name != "测试群" {
		t.Errorf("备份要整份保留上一版（含群清单），实际 %+v", bakState.Groups)
	}

	mainRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读主文件失败：%v", err)
	}
	var mainState stateFile
	if err := json.Unmarshal(mainRaw, &mainState); err != nil {
		t.Fatalf("主文件应能解析：%v", err)
	}
	if len(mainState.IDMap) != 2 {
		t.Errorf("主文件应是新版（两条 idmap），实际 %+v", mainState.IDMap)
	}
}

// 主文件损坏但有备份：从备份恢复，打醒目告警，且恢复后 IDMap 不再是只读降级状态。
func TestJSONStoreRecoversFromBackupWhenMainCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := newStateStore(t, path, testAppID)

	// 第一版：一条 idmap + 一个群；第二版：两条 idmap（于是备份 = 第一版）
	saveIDs(t, store, map[string]int64{composeIDKey(testAppID, testGroupOpen): 1})
	if err := store.SaveGroups([]GroupRecord{{OpenID: testGroupOpen, Name: "测试群", InfoKnown: true}}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}
	saveIDs(t, store, map[string]int64{
		composeIDKey(testAppID, testGroupOpen): 1,
		composeIDKey(testAppID, testOwnerOpen): 2,
	})

	// 主文件被写坏：断电、磁盘满、被别的进程截断都长这样
	if err := os.WriteFile(path, []byte("{ 这不是 JSON"), 0o600); err != nil {
		t.Fatalf("写坏主文件失败：%v", err)
	}

	buf := captureLogs(t)
	fresh := newStateStore(t, path, testAppID)
	got, err := fresh.Load()
	if err != nil {
		t.Fatalf("主文件损坏但备份可用时必须从备份恢复，实际 err=%v", err)
	}
	if len(got) != 1 || got[composeIDKey(testAppID, testGroupOpen)] != 1 {
		t.Errorf("恢复出来的应是备份那一版（只有群那一条 id=1），实际 %+v", got)
	}
	groups, err := fresh.LoadGroups()
	if err != nil || len(groups) != 1 || groups[0].Name != "测试群" {
		t.Errorf("群清单也应从备份恢复，实际 %+v（err=%v）", groups, err)
	}
	logs := buf.String()
	if !strings.Contains(logs, path) || !strings.Contains(logs, path+".bak") {
		t.Errorf("恢复告警必须给出主文件与备份的路径，实际日志：%s", logs)
	}
	if !strings.Contains(logs, "备份") {
		t.Errorf("恢复告警要说明「已从备份恢复」，实际日志：%s", logs)
	}
	if !strings.Contains(logs, "level=error") {
		t.Errorf("主文件解析失败属真损坏，应是 Error 级，实际日志：%s", logs)
	}

	// 恢复之后不能还是「Load 失败 → 存储只读」，IDMap 必须能继续分配并落盘
	ids := NewIDMap(fresh)
	if id := ids.ToInt64(testAppID, testOtherOpen); id == 0 {
		t.Error("从备份恢复后应能正常分配内部 ID")
	}
	if err := ids.Flush(); err != nil {
		t.Errorf("从备份恢复后应能正常落盘，实际 %v", err)
	}
	if raw, err := os.ReadFile(path); err != nil || !json.Valid(raw) {
		t.Errorf("恢复后第一次写盘应把主文件修回合法 JSON，实际 err=%v", err)
	}
}

// 主文件与备份都不存在 = 真·首次运行：给空状态 + 一条点名后果的 Warn，但不能报错。
func TestJSONStoreFirstRunIsNotAnErrorButWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	buf := captureLogs(t)
	store := newStateStore(t, path, testAppID)

	got, err := store.Load()
	if err != nil {
		t.Fatalf("首次运行（主文件与备份都不存在）不能报错，实际 %v", err)
	}
	if len(got) != 0 {
		t.Errorf("首次运行应是空映射，实际 %+v", got)
	}
	groups, err := store.LoadGroups()
	if err != nil || len(groups) != 0 {
		t.Errorf("首次运行群清单应为空且不报错，实际 %+v（err=%v）", groups, err)
	}

	logs := buf.String()
	for _, want := range []string{path, "重新分配", "订阅", "不要删除"} {
		if !strings.Contains(logs, want) {
			t.Errorf("首次运行的告警应包含 %q，实际日志：%s", want, logs)
		}
	}
	if strings.Contains(logs, "level=error") {
		t.Errorf("首次运行是正常场景，不该打 Error 级，实际日志：%s", logs)
	}

	// 这条 Warn 只在第一次读到「空」时出现，不能每次读都刷
	if _, err := store.Load(); err != nil {
		t.Fatalf("再次 Load: %v", err)
	}
	if n := strings.Count(buf.String(), "重新分配"); n != 1 {
		t.Errorf("首次运行的告警应只出现一次，实际 %d 次：%s", n, buf.String())
	}
}

// 主文件损坏、备份也没有：必须报错（绝不能用空映射覆盖旧数据），并打 Error 级日志。
func TestJSONStoreCorruptMainWithoutBackupStaysFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := newStateStore(t, path, testAppID)
	saveIDs(t, store, map[string]int64{composeIDKey(testAppID, testGroupOpen): 1})

	const broken = "{ 坏掉的 JSON"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatalf("写坏主文件失败：%v", err)
	}
	buf := captureLogs(t)
	if _, err := store.Load(); err == nil {
		t.Error("主文件损坏且没有可用备份时必须报错，不能当作空的")
	}
	if logs := buf.String(); !strings.Contains(logs, "level=error") || !strings.Contains(logs, path) {
		t.Errorf("损坏且无备份应是 Error 级并带路径，实际日志：%s", logs)
	}

	// 报错之后绝不能有任何写入（写下去就等于用不完整的映射覆盖旧数据）
	ids := NewIDMap(store)
	if err := ids.Flush(); !errors.Is(err, ErrLoadFailed) {
		t.Errorf("Load 失败后 Flush 应报 ErrLoadFailed 且不写盘，实际 %v", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != broken {
		t.Errorf("Load 失败后主文件应原样不动，实际 %q（err=%v）", raw, err)
	}
}

// 备份轮转失败不能拖垮主写入路径（备份只是兜底，不是前置条件）。
func TestJSONStoreBackupFailureDoesNotBreakWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := newStateStore(t, path, testAppID)
	saveIDs(t, store, map[string]int64{composeIDKey(testAppID, testGroupOpen): 1})

	// 把备份路径占成目录：轮转必然失败
	if err := os.Mkdir(path+".bak", 0o700); err != nil {
		t.Fatalf("占用备份路径失败：%v", err)
	}
	buf := captureLogs(t)
	v2 := map[string]int64{
		composeIDKey(testAppID, testGroupOpen): 1,
		composeIDKey(testAppID, testOwnerOpen): 2,
	}
	if err := store.Save(v2); err != nil {
		t.Fatalf("备份失败不能拖垮主写入路径，实际 %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读主文件失败：%v", err)
	}
	var st stateFile
	if err := json.Unmarshal(raw, &st); err != nil || len(st.IDMap) != 2 {
		t.Errorf("备份失败时主文件仍应写成新版，实际 %+v（err=%v）", st.IDMap, err)
	}
	if logs := buf.String(); !strings.Contains(logs, "备份") || !strings.Contains(logs, "level=warning") {
		t.Errorf("备份失败应留下 Warn 日志，实际日志：%s", logs)
	}
}

// 文件里记的 app_id 与当前配置不一致要在读盘时就被点出来（含新旧 appID 与后果），且不刷屏。
func TestJSONStoreAppIDMismatchWarnsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	const oldAppID = "1900000009"
	old := newStateStore(t, path, oldAppID)
	if err := old.Save(map[string]int64{composeIDKey(oldAppID, testGroupOpen): 1}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	buf := captureLogs(t)
	cur := newStateStore(t, path, testAppID)
	if _, err := cur.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	logs := buf.String()
	for _, want := range []string{oldAppID, testAppID, "openid"} {
		if !strings.Contains(logs, want) {
			t.Errorf("app_id 不一致的告警应包含 %q，实际日志：%s", want, logs)
		}
	}
	if _, err := cur.Load(); err != nil {
		t.Fatalf("再次 Load: %v", err)
	}
	if n := strings.Count(buf.String(), "不一致"); n != 1 {
		t.Errorf("app_id 不一致的告警应只出现一次，实际 %d 次：%s", n, buf.String())
	}
}
