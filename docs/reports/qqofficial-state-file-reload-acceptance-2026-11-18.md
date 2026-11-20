# QQ 官方状态路径重载策略验收

日期：2026-11-18。对应 PR #52 的 Copilot 审核意见。

## 问题与修复

`qqofficial.state-file` 原先被列入热更新清单，但 QQ 适配器接管运行状态时拒绝不同状态路径。配置准备会先停止原连接，再因路径不一致失败并回滚，导致路径修改无法按需重启字段保存。

将该键移出热更新清单，保留字符串类型校验和适配器底层路径、身份保护。新增、修改或移除配置路径时，源文件保存新值，运行快照及资源准备仍使用原值，`restartRequired` 报告 `qqofficial.state-file`。同次及后续 Secret 更新继续使用原状态路径；恢复原配置清除提示。重启采用保存的路径，不自动迁移状态数据。

## 回归与独立验收

- 实现者的 `lsp/cfg/qqofficial_state_reload_test.go` 覆盖显式路径变更、新增路径和移除路径，分别通过 `Config.Save` 与外部文件 `Reload` 验证。修复前六组场景均复现失败，修复后通过；同时验证混合热键、恢复清提示及新进程读取保存路径。
- 独立代理 `independent_completion_audit` 只读审查产品变更并编写根目录 `qqofficial_state_reload_acceptance_test.go`，通过真实配置策略、`bot.PrepareAdapterReload` 和本地 QQ HTTP/WebSocket 服务验证显式与默认运行路径各五个保存步骤。
- 独立场景验证路径单改不重连；路径与 Secret 混合保存可真实授权并重连；待重启期间后续 Secret 更新仍使用旧路径；恢复配置清除提示。目标路径预置另一 AppID 的哨兵文件，始终保持原字节；原 ID 映射保留。

验证结果全部通过：

- 根模块 `CGO_ENABLED=0 go test ./... -timeout 120s`。
- 根包、`lsp/cfg`、`admin` 的相关完整竞态检查；兼容既有 gls 依赖使用 `-gcflags=github.com/modern-go/gls=-d=checkptr=0`，保留竞态检测。
- bot 子模块 `./bot ./config` 完整竞态检查。
- adapter 子模块 `./qqofficial` 完整竞态检查。
- Go 格式检查与 `git diff --check`。

独立结论：本次修复可提交并推送现有 PR，不合并。测试生成的 `admin/res/bilibili_area_list.json` 不纳入提交。

## 仍待用户验收

本轮使用本地协议服务，没有使用真实 QQ 或其他平台账号，也未补做实际浏览器及部署验收。Bilibili/微博真实扫码、浏览器草稿/409/访问迁移、实际适配器/代理/Telegram 在线更新仍按 PR 清单等待用户验证。本次修复闭环一个代码问题，不代表上述验收已完成。
