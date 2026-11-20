# 代理与登录凭据联动热更新验收报告

日期：2026-11-17。分支：`codex/config-hot-reload`。基于已验收提交 `d0ddacb` 继续完善。

## 问题与行为

此前配置事务按顺序准备代理池和登录客户端，但候选登录仍读取旧全局代理。旧代理失效时，同时保存新代理与新凭据可能无法完成授权，必须分两次保存。

本轮让 Telegram、Twitter API、Twitch 的候选凭据验证共享同一事务准备的新代理。准备期间不发布候选池，普通请求仍走旧池；全部准备和原子落盘成功后再提交。失败时旧文件、配置版本、代理和会话保持不变，并逆序清理候选资源。初始化向导的 OneBot 重启提示、资源准备失败提示和微博扫码未就绪时的成功提示也已修正。

候选路由有明确生命周期：

- 准备：仅使用候选池；明确关闭代理或本地池没有匹配路由时直连，不能落回旧池或环境代理。
- 提交：发布候选池，保留的 getter 自动改为读取全局池；后续只修改代理也能影响已提交的客户端。
- 取消：getter 失效，后续请求在发出前返回错误；包括指定 `PreferNone` 的调用。
- 并发：提交和取消互斥且幂等，停止池之前等待正在执行的取路由操作结束；已交给请求的代理地址允许继续使用。

普通请求保留原有代理回退规则。`requests.ProxyOption` 继续在构造时选择路由，避免改变 Twitter 镜像挑战的出口语义；新增 `ProxyOptionWithGetter` 按请求选择，只有显式候选 getter 才强制遵守候选路由。每个请求创建的 Transport 克隆在结束后关闭空闲连接，不修改调用方 Transport；TLS、CookieJar、HTTP/2 选项保留。

## 独立验收与回归证据

产品代码由主代理、`shared_resource_reload` 和 `platform_live_login` 分别实现。独立子代理 `independent_completion_audit` 只审查和编写验收探针，不修改产品实现；主代理执行集成读审和全量回归。这里的“独立验收”指独立子代理验收，并非人工使用真实账号验收。

| 范围 | 永久回归与结果 |
| --- | --- |
| 实际配置保存事务 | `proxy_resource_transaction_test.go`：旧代理返回 502，新代理与 Telegram token 一次保存；准备阻塞期间普通请求仍走旧池；后续仅换代理使用新池 |
| 完整生产参与者链 | 同文件 `TestFullRuntimeResourceTransactionSharesCandidateProxyWithTelegram` 调用生产 `prepareRuntimeResources`，验证候选路由确实接入，而非只测手工拼接的参与者 |
| 失败回滚 | 同文件验证错误凭据、后续资源失败、落盘失败均保留旧状态，取消后的 getter 拒绝请求；对外错误不包含测试凭据 |
| 并发配置保存与发送 | 同文件交错执行 24 次消息发送和 8 次仅改代理的保存；独立 race 重复运行通过 |
| 代理生命周期 | `proxy_pool/candidate_test.go`：隔离、提交后跟随全局、取消后不回退、等待 Get 排空、并发提交/取消和幂等 |
| Python 代理池 | `proxy_pool/py/pyPool_test.go` 与 `lsp/resource_proxy_reload_test.go` 验证根探测、`/get` 裸地址和候选请求路由；独立根事务探针使用 plain text 根响应、裸地址和失效旧池，验证新 py 配置与 Telegram 凭据同次保存可用 |
| 请求选项 | `requests/proxy_getter_test.go`：真实本地 HTTP 代理、显式直连覆盖旧代理、取消后无网络、TLS/CookieJar/HTTP2 组合、调用方 Transport 隔离及自定义默认 RoundTripper 兼容；`requests/proxy_compatibility_acceptance_test.go` 另验证 HTTPS CONNECT 隧道后保留调用方信任链、CookieJar 与真实 HTTP/2，以及空 getter 与旧接口的回退行为一致 |
| 平台调用链 | Telegram、Twitter、Twitch 各自 `proxy_reload_test.go`：候选授权走新代理、失败保留旧身份、取消、提交后路由更新、显式直连；Twitch 后续 token 刷新和无关保存也有验证 |
| 初始化向导 | `admin/setup_reload_test.go`：资源准备失败返回 HTTP 400，保持未初始化 token、原文件和配置版本；文案改为当前 OneBot 在线应用 |

实现与验收过程中发现并修复的边界包括：`PreferNone` 绕过取消状态检查、空 getter 意外覆盖原有回退行为、候选直连沿用调用方代理、TLS 选项被客户端覆盖、请求创建的 Transport 未主动关闭空闲连接，以及 SOCKS5 在继承 `DialContext` 后可能被绕过。原探针在修复后复验。

Python 代理池同时修复两个兼容问题：`/get` 返回的裸 `host:port` 统一补齐 `http://`，避免严格候选路由拒绝已有格式；根探测将 `HttpCodeOption` 放回选项参数，修复原来误当响应对象导致状态码始终为零的问题，并允许根响应为普通文本。

全量测试还暴露了既有 `TestEmitQueue` 的时序假设：启动后分三次 Add 不能保证 worker 原子观察到三个元素。测试改为先装入初始列表再启动，保留原本的轮询、删除、更新和类型合并断言，不修改队列产品行为。

## 验证命令

独立验收最终通过，无剩余阻断。主代理全量回归与构建通过：

- 根模块：`CGO_ENABLED=0 go test ./... -coverprofile=.coverage.out -timeout 120s` 全部通过。中途一次发现上述旧队列测试假设，修正后全量复跑通过。
- Bot 子模块：`CGO_ENABLED=1 go test ./... -count=1 -timeout 120s` 全部通过。
- 独立完整竞态检查：`CGO_ENABLED=1 go test -race -gcflags=github.com/modern-go/gls=-d=checkptr=0 ./ ./proxy_pool/... ./requests ./lsp ./lsp/telegram ./lsp/twitch ./lsp/twitter ./admin -count=1 -timeout 180s` 通过。仅关闭既有 `modern-go/gls` 依赖的指针检查，竞态检测保持开启。
- 最后 Python 代理兼容补丁另经 py、lsp、requests、Telegram 和完整生产保存链的独立 race 增量复验通过；公共实现者的 `proxy_pool/...` 与 `requests` 全包 race 也通过。
- `go test -race -gcflags=github.com/modern-go/gls=-d=checkptr=0 ./utils -run '^TestEmitQueue$' -count=15 -timeout 90s` 通过，保留原业务断言。
- 原生构建：`CGO_ENABLED=1 go build -buildvcs=false -o <TEMP>/ddbot-config-hot-reload.exe ./cmd` 通过。
- 前端：`node --check admin/web/js/app.js` 和 `node --test admin/web_tests/config_reload.test.cjs` 的 8 项行为回归通过。
- Go 格式与 `git diff --check` 检查通过；测试生成的 B站区域列表文件清理后不纳入提交。

## 保留边界

1. 本轮仅调整原本使用全局代理的 Telegram、Twitter API、Twitch 授权路径。B站等平台原有直连或专用路由策略不因此改变，Twitter 镜像挑战语义保持原样。
2. 候选代理配置有效不表示外部服务必然可达；相关候选凭据需要通过各平台已有授权校验。准备阶段不保证跨资源的瞬间同步切换，已开始的旧请求可以完成。
3. 跨协议、跨账号、QQ AppID 或状态路径迁移、extDb 数据迁移等边界维持 [支持说明](../config-hot-reload.md) 的约束。
4. 使用真实本地 HTTP/WS 服务和并发探针验收，未进行真实平台账号扫码、真实外部网关或浏览器视觉验收。
5. 按用户要求仅本地提交，不执行 Git 推送。

此前资源重建和首轮配置机制的证据分别见 [资源热更新验收报告](config-resource-reload-acceptance-2026-11-17.md) 与 [首轮报告](config-hot-reload-acceptance-2026-11-17.md)。
