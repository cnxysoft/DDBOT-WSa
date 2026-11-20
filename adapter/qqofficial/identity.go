package qqofficial

import "strings"

// IdentityPrefix 是 identity 的 adapter 名前缀，最终形态形如 qqofficial/1900000000。
//
// 前缀和 app-id 一起进指纹：同一个 app-id 换个 adapter 落到同一个工作目录，同样是
// 「换了个机器人」，不能算一致。
const IdentityPrefix = "qqofficial/"

// Identity 返回「这个 adapter 当前是谁」，用于防止同一工作目录被不同机器人复用。
//
// 为什么需要它：.lsp.db 里订阅 / 权限 / @全体标记的 key 全由**内部小整数**（groupCode）
// 拼成，这些整数由本 adapter 的 IDMap 从 1 自增分配，存在 .qqofficial-state-<appid>.json；
// 两个文件之间没有任何关联。换 app-id 后新状态文件是空的、ID 从 1 重开，正好撞上旧机器人
// 用过的 1、2、3 —— 旧订阅会挂到错误的群上，而 .lsp.db 里既没有 openid 也没有群名，
// 事后没法校验。上层把这一串写进 .lsp.db 当指纹，不一致就拒绝启动。
//
// **这道闸有三个盲区，别指望它全覆盖**（写在这里免得日后误以为万无一失）：
//
//  1. ws → 官方通道互切：onebot / satori 不实现 Identity()，调用方断言不到就整段跳过，
//     旧库没有官方通道的指纹可比。切通道请换工作目录。
//  2. 只搬 .lsp.db、不搬 .qqofficial-state-<app-id>.json：指纹一致照样放行，
//     但内部 ID 会从 1 重开，旧订阅仍然错位。两个文件必须一起搬。
//  3. 先换机器人、后升级到带这道闸的版本：建档时事故已经发生，闸只能管以后。
//
// app-id 为空时返回空串，表示「没有身份可校验」，调用方必须整段跳过 ——
// 缺 app-id 是配置层自己的错（GetConfig.Validate 会报），不归这条闸管。
//
// 故意**不**并进 adapter.Adapter 公共接口：这是可选扩展能力，onebot / satori 没有它，
// 上层靠结构化断言（interface{ Identity() string }）取，断言不到就当没有这回事。
func (a *QQOfficialAdapter) Identity() string {
	if a == nil || a.cfg == nil {
		return ""
	}
	appID := strings.TrimSpace(a.cfg.AppID)
	if appID == "" {
		// 空串 = 没有身份可校验。全是空白也当空：指纹里带空白会让同一个机器人
		// 因为配置多敲一个空格被判成「换过人」。
		return ""
	}
	return IdentityPrefix + appID
}
