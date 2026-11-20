package lsp

// 「同一个工作目录不允许换机器人」那道闸的单测。
//
// 背景：.lsp.db 里订阅/权限/@全体标记的 key 全由**内部小整数**
// （groupCode）拼成，这些整数由 adapter/qqofficial 的 IDMap 自增分配，存在
// .qqofficial-state-<appid>.json。两个文件之间没有任何关联：换 app-id 后新状态文件是空的，
// ID 从 1 重开，正好撞上旧机器人用过的 1、2、3 —— 旧订阅会挂到错误的群上，
// 而 .lsp.db 里既没有 openid 也没有群名，事后无法校验。
//
// 所以：在 .lsp.db 里写一条机器指纹，不一致就拒绝启动。
// 断言不到身份（onebot / satori）或身份为空时**完全不介入**，行为必须和加这道闸之前一样。

import (
	"strings"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/buntdb"
)

// fakeIdentityAdapter 自报身份（QQ 官方 adapter 就是这样）。
type fakeIdentityAdapter struct {
	adapter.Adapter
	identity string
}

func (f *fakeIdentityAdapter) Identity() string { return f.identity }

// spyIdentityStore 记录闸到底有没有碰过库：「跳过」的用例必须一次都不碰。
type spyIdentityStore struct {
	stored  string
	loads   int
	saves   int
	loadErr error
	saveErr error
}

func (s *spyIdentityStore) load() (string, error) {
	s.loads++
	return s.stored, s.loadErr
}

func (s *spyIdentityStore) save(identity string) error {
	s.saves++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.stored = identity
	return nil
}

// countingIdentityStore 包一层真的库，只为了数「写了几次」。
type countingIdentityStore struct {
	inner botIdentityStore
	saves int
}

func (c *countingIdentityStore) load() (string, error) { return c.inner.load() }
func (c *countingIdentityStore) save(identity string) error {
	c.saves++
	return c.inner.save(identity)
}

// newTempIdentityStore 建一个**临时目录**里的 buntdb，绝不碰真实 .lsp.db。
func newTempIdentityStore(t *testing.T) (*buntdbIdentityStore, string) {
	t.Helper()
	path := t.TempDir() + "/lsp.db"
	db, err := buntdb.Open(path)
	if err != nil {
		t.Fatalf("打开临时 buntdb 失败：%v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &buntdbIdentityStore{db: db}, path
}

// 首次运行：指纹为空 → 写入当前身份 + 返回 nil + 打一条 Info。
func TestBotIdentityFirstRunRecordsFingerprint(t *testing.T) {
	logs := attachLogCapture(t)
	store, _ := newTempIdentityStore(t)
	const identity = "qqofficial/1900000000"

	if err := checkBotIdentity(store, identity); err != nil {
		t.Fatalf("首次运行（指纹为空）不该报错，实际：%v", err)
	}
	recorded, err := store.load()
	if err != nil {
		t.Fatalf("写入后读不回来：%v", err)
	}
	if recorded != identity {
		t.Fatalf("指纹没写对：库里是 %q，期望 %q", recorded, identity)
	}
	infos := logs.byLevel(logrus.InfoLevel)
	var found bool
	for _, e := range infos {
		if strings.Contains(e.Message, "已记录当前机器人身份") && strings.Contains(e.Message, identity) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("首次写入必须打一条 Info 说明「已记录当前机器人身份」，实际日志：%s", logs.dump())
	}
}

// 指纹与当前一致 → 返回 nil，且**不重复写**。
func TestBotIdentitySameFingerprintDoesNotRewrite(t *testing.T) {
	attachLogCapture(t)
	real, _ := newTempIdentityStore(t)
	const identity = "qqofficial/1900000000"
	if err := real.save(identity); err != nil {
		t.Fatalf("预置指纹失败：%v", err)
	}

	store := &countingIdentityStore{inner: real}
	if err := checkBotIdentity(store, identity); err != nil {
		t.Fatalf("指纹一致时不该报错，实际：%v", err)
	}
	if store.saves != 0 {
		t.Fatalf("指纹一致时不该重复写，实际写了 %d 次", store.saves)
	}
	if recorded, _ := real.load(); recorded != identity {
		t.Fatalf("指纹被改坏了：%q", recorded)
	}
}

// 指纹与当前不一致 → 返回 error，且信息里同时含旧身份、当前身份和处置办法；
// 库里那条旧指纹必须原样留着（拒绝启动不能顺手毁掉证据）。
func TestBotIdentityMismatchRejects(t *testing.T) {
	attachLogCapture(t)
	store, _ := newTempIdentityStore(t)
	const oldIdentity = "qqofficial/1111111111"
	const newIdentity = "qqofficial/1900000000"
	if err := store.save(oldIdentity); err != nil {
		t.Fatalf("预置指纹失败：%v", err)
	}

	err := checkBotIdentity(store, newIdentity)
	if err == nil {
		t.Fatal("数据库里的身份与当前机器人不一致时必须报错（否则旧订阅会挂到错误的群）")
	}
	for _, want := range []string{oldIdentity, newIdentity, ".lsp.db", "备份"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息应点出 %q，实际：%v", want, err)
		}
	}
	if recorded, _ := store.load(); recorded != oldIdentity {
		t.Fatalf("拒绝启动时不该改写旧指纹：库里成了 %q，期望 %q", recorded, oldIdentity)
	}
}

// adapter 没有 Identity 能力（onebot / satori）→ 完全跳过：一次都不读、不写。
func TestBotIdentitySkippedWithoutAdapterCapability(t *testing.T) {
	attachLogCapture(t)
	installTestBot(t, &fakePlainAdapter{})

	identity, ok := adapterIdentity()
	if ok {
		t.Fatalf("onebot / satori 这种 adapter 不该被断言出 Identity 能力，实际拿到 %q", identity)
	}
	if identity != "" {
		t.Fatalf("断言不到能力时身份必须是空串，实际 %q", identity)
	}

	// 反证：真有一份 spy 库摆在这里，闸也不许碰它。
	spy := &spyIdentityStore{stored: "qqofficial/1"}
	if err := checkBotIdentity(spy, identity); err != nil {
		t.Fatalf("没有身份时不该报错，实际：%v", err)
	}
	if spy.loads != 0 || spy.saves != 0 {
		t.Fatalf("没有身份时必须完全跳过，实际读了 %d 次、写了 %d 次", spy.loads, spy.saves)
	}
}

// 身份为空串 → 同样跳过。
func TestBotIdentitySkippedWhenIdentityEmpty(t *testing.T) {
	attachLogCapture(t)
	installTestBot(t, &fakeIdentityAdapter{identity: ""})

	identity, ok := adapterIdentity()
	if !ok || identity != "" {
		t.Fatalf("实现 Identity 但返回空串时应当是「有能力、身份为空」，实际 ok=%v identity=%q", ok, identity)
	}
	spy := &spyIdentityStore{}
	if err := checkBotIdentity(spy, identity); err != nil {
		t.Fatalf("身份为空时不该报错，实际：%v", err)
	}
	if spy.loads != 0 || spy.saves != 0 {
		t.Fatalf("身份为空时必须完全跳过，实际读了 %d 次、写了 %d 次", spy.loads, spy.saves)
	}
}

// 钉住取值路径：必须落到 Messenger.Adapter 上断言。
// GetBot().Bot 的动态类型是 *bot.Bot，只提升 *adapter.Messenger 的方法集，
// 而 Messenger.Adapter 是**具名接口字段**、不是嵌入 —— 在那层断言永远失败。
func TestBotIdentityMustComeFromMessengerAdapter(t *testing.T) {
	attachLogCapture(t)
	fake := &fakeIdentityAdapter{identity: "qqofficial/1900000000"}
	installTestBot(t, fake)

	identity, ok := adapterIdentity()
	if !ok || identity != fake.identity {
		t.Fatalf("从 Messenger.Adapter 必须能断言出身份，实际 ok=%v identity=%q", ok, identity)
	}
}

// 钉住「可选扩展」的两层含义：
//  1. Identity 不并进公共 adapter.Adapter 接口 —— 只嵌入 adapter.Adapter 占位的假 adapter
//     （onebot / satori 就是这样）照样能装进 Messenger，谁都**不**被逼着实现它；
//  2. GetBot().Bot 那一层断言不到身份能力（同 joinintro 踩过的坑），只能落 Messenger.Adapter。
func TestBotIdentityIsOptionalExtension(t *testing.T) {
	attachLogCapture(t)
	installTestBot(t, &fakeIdentityAdapter{identity: "qqofficial/1900000000"})

	if _, ok := localutils.GetBot().Bot.(botIdentityProvider); ok {
		t.Fatal("GetBot().Bot 的动态类型是 *bot.Bot，不该直接满足 botIdentityProvider" +
			"（Messenger.Adapter 是具名字段、不是嵌入，方法不会穿透）")
	}
	if _, ok := adapterIdentity(); !ok {
		t.Fatal("从 Messenger.Adapter 必须能断言出 botIdentityProvider")
	}

	// 没实现 Identity 的 adapter 依然能装进来，且不算有身份能力。
	installTestBot(t, &fakePlainAdapter{})
	if _, ok := adapterIdentity(); ok {
		t.Fatal("没实现 Identity 的 adapter 不该被认成有身份能力")
	}
}
