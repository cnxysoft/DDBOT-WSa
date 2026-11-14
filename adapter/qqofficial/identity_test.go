package qqofficial

// Identity 是「同一工作目录不允许换机器人」那道闸的身份来源。
//
// 它必须**只反映配置里的 app-id**：.lsp.db 里的订阅/权限/@全体标记全部按内部群号
// （groupCode）存储，而这些整数是 IDMap 按 app-id 自增分配的。换机器人 = 换一套分配，
// 从 1 重开就会撞上旧机器人的 1、2、3。上层据此判定「这个工作目录以前是谁在用」。
//
// 约定：app-id 为空时返回空串 = 「没有身份可校验」，调用方必须整段跳过
// （缺 app-id 的配置由 config 层自己报错，不归这条闸管）。

import "testing"

func TestQQOfficialAdapterIdentity(t *testing.T) {
	t.Run("正常形态", func(t *testing.T) {
		a := &QQOfficialAdapter{cfg: &Config{AppID: testAppID}}
		if got, want := a.Identity(), "qqofficial/"+testAppID; got != want {
			t.Fatalf("Identity() = %q，期望 %q", got, want)
		}
	})

	t.Run("app-id 为空返回空串", func(t *testing.T) {
		a := &QQOfficialAdapter{cfg: &Config{}}
		if got := a.Identity(); got != "" {
			t.Fatalf("app-id 为空时 Identity() 必须返回空串（= 没有身份可校验），实际 %q", got)
		}
	})

	t.Run("app-id 只有空白也算空", func(t *testing.T) {
		a := &QQOfficialAdapter{cfg: &Config{AppID: "   "}}
		if got := a.Identity(); got != "" {
			t.Fatalf("app-id 全是空白时 Identity() 必须返回空串，实际 %q", got)
		}
	})

	t.Run("app-id 前后空白会被裁掉", func(t *testing.T) {
		a := &QQOfficialAdapter{cfg: &Config{AppID: " " + testAppID + "\n"}}
		if got, want := a.Identity(), "qqofficial/"+testAppID; got != want {
			t.Fatalf("Identity() = %q，期望 %q（前后空白不该进指纹，否则同一机器人会判成两个）", got, want)
		}
	})

	t.Run("nil adapter 不 panic", func(t *testing.T) {
		var a *QQOfficialAdapter
		if got := a.Identity(); got != "" {
			t.Fatalf("nil adapter 的 Identity() 必须返回空串，实际 %q", got)
		}
	})

	t.Run("nil 配置不 panic", func(t *testing.T) {
		a := &QQOfficialAdapter{}
		if got := a.Identity(); got != "" {
			t.Fatalf("没有配置的 adapter 的 Identity() 必须返回空串，实际 %q", got)
		}
	})
}
