# 来源与授权（Provenance & Authorization）

## 这是什么

本分支是 [阿宇](https://github.com/why0209) 的 **DDBOT-Lite** 的**证据驱动重实现（re-implementation）**，不是原始源代码。

DDBOT-Lite 由 阿宇 在 [cnxysoft/DDBOT-WSa](https://github.com/cnxysoft/DDBOT-WSa) 的 `next-dev` 线上开发
（基底提交 `bad19a4c`，2026-06-22），是一套从未公开提交的 DynamicLite 改动（约 810 个函数 / 560 KB 机器码）。
原成品为 2026-09-11 的 go1.26.2 本地构建；公开渠道（GitHub 各分支/tag/Release/PR refs/fork、gitee 镜像、代码搜索）
均无源码副本。

本分支的代码是在**没有原始源码**的前提下，以成品二进制做**证据驱动重实现**：

- **符号表**：从 `pclntab` 全量还原函数表（名称 / 文件:行号 / 大小 / 入口地址）
- **调用图**：`.text` 段 `E8 rel32` 调用点扫描 + `objdump` 二次确认
- **类型层**：`typelinks` 与类型闭包（结构体字段 / 偏移 / json tag / 接口方法集）
- **字节级恢复**：`rodata` 中的内嵌模板、随包文档、字符串表

## 授权

- 已获得原作者 [阿宇](https://github.com/why0209) 授权发布本重实现。
- 原作者因工作原因已放弃维护，并**自行删除了原始源代码**；本分支不是对其源码的复制，而是独立重写。
- 无法与原成品 1:1 一致：约 810 个函数的实现逻辑无法从机器码还原。

## 许可

沿用上游 **AGPL-3.0**（见 LICENSE）。任何分发、修改与网络服务部署都必须继续遵守 AGPL-3.0。

## 已知差异（诚实清单）

- **符号覆盖**：关闭内联构建（`-gcflags=all=-l`）下 702/702 = 100.0%，真缺口 0；
  默认构建 659/702 = 93.9%（缺的 43 个全部是 go1.27 内联产物，功能均在）。
- **未做端到端生产验证**（需要真实 OneBot 11 实现端与各平台账号 / Cookie）。
- 少量二进制不可复原的常数标有 `TODO(lite)`；无法从二进制坐实的推断标有 `[推断]`。
- 以下为**已知的、未能 100% 坐实**的项（均不影响编译与主要行为）：
  - **并发/时序**：B 站 CookieCloud refresh 的 CAS 守卫与控制流差异；微博 `ForceFreshCookie` 的锁序
    与 `cookieHealthy` 提前返回；微博刷新器为 `AfterFunc` 自续而非 `NewTicker+authStop`。
  - **协议栈**：B 站 `serveAccountLiveWebsocket` 的结果回报与认证回调承载方式与成品 ABI 不同（行为等价）。
  - **纯 API/命名**：`cookiecloudclient` 保留若干未使用的 sentinel/uuidPattern；个别内部类型名不同。
  - **`[推断]`**：少量日志字段语义（如抖音 `new_posts` 的取值）。

## 上游

- [cnxysoft/DDBOT-WSa](https://github.com/cnxysoft/DDBOT-WSa) （AGPL-3.0）
- [Sora233/DDBOT](https://github.com/Sora233/DDBOT)
- [Hoshinonyaruko/DDBOT-ws](https://github.com/Hoshinonyaruko/DDBOT-ws)
