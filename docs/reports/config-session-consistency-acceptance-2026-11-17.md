# 配置路径与登录会话一致性验收报告

日期：2026-11-17。分支：`codex/config-hot-reload`。基于已验收提交 `d44469a` 继续完善。

## 修复的问题与行为

本轮修复三组会影响热更新可信度的问题：配置 API 对字段路径的识别与运行时不一致、特殊 YAML 下凭据回写可能假成功，以及 WebUI 登录切换后旧请求继续操作。

### 配置路径与保护规则

Viper 忽略配置结构键的大小写，并识别点号路径。旧 API 对路径按原始拼写保护、脱敏和合并；独立探针复现了 `Admin.Token` 等写法可以绕过管理令牌保护并改变运行值，GET 的凭据脱敏也遗漏了大小写变体和 B站密码。

现在源文件与请求先使用相同路径规则去歧义，再做保护比较和合并。结构键保留已有拼写；点号路径转为嵌套结构，保留同级未修改项；大小写或平铺/嵌套的重复声明、值与父路径冲突、多文档 YAML 拒绝保存。初始化向导也使用相同规则，避免写出重复的 `Admin` / `admin` 段。解析错误不回显可能含凭据的 YAML 诊断正文。

`customCommandPrefix`、`telegram.commands`、`xhs.cookies` 中的名称是数据键，保持大小写和点号原样。自定义命令表仍按整表替换；其原文解析器只对外层配置段忽略大小写，命令名称不变。管理及 WebSocket token 的修改限制维持原有规则，平台凭据仍允许覆盖或清空。

### 凭据回写与取消

旧正则写入可能误命中嵌套同名键或块标量正文，也可能新增被 dotted key 遮蔽的无效字段。现在解析 YAML 节点定位真实路径，再局部编辑源码；保留其他字节、注释、BOM、大小写和已有换行。支持可安全定位的普通嵌套、引号键、flow 映射和纯 dotted key；有歧义或不能安全局部修改的目标明确拒绝。

每个目标值和整组凭据都在持久化前用 Viper 的实际读取结果核对，失败时不进入资源准备、不改变原文件。目标或祖先映射上的锚点/别名、目标多行或显式标签值、冲突路径、多文档等情况安全拒绝；并非声称支持所有合法 YAML 写法。

新增 `WriteConfigKeyValuesContext` 和 `Config.SaveContext`。会话在等候写锁、事务锁或准备资源时被取消，候选资源会被清理，凭据不再发布或落盘。持久化开始并成功后仍完成提交，保持文件和运行状态一致。撤销只发出取消，不等待扫码工作退出，避免令牌轮换事务与排队的凭据事务互相等待。

管理令牌变化或 WebUI 服务停止会撤销已有扫码任务；已通过旧鉴权但尚未建会话的请求也不能在撤销后新建任务。仅修改监听地址保留扫码会话。B站、QQ 官方的最终凭据对使用可取消事务；微博的出码、轮询、换取登录凭据 HTTP 请求和最终写入均贯通取消。

独立最终验收还复现了停止服务时的一个窗口：撤销旧会话后，监听器排空期间新请求仍能使用有效令牌创建任务。修复在同一把鉴权锁下设置最终停止状态，鉴权入口和建会话入口均拒绝新工作；原红测已转绿，地址迁移和在线开关监听器的语义保持不变。

### WebUI 登录代次与请求生命周期

无效 token 的 403 增加 `X-DDBOT-Auth: invalid-token`。WebUI 对鉴权失败返回登录页，对受保护写入的普通 403 保留当前登录。

退出或重新登录（包括再次使用相同 token）都会更新登录代次，终止旧日志/推送 SSE、重连计时器和扫码轮询。迟到登录和状态响应不能覆盖新会话；关闭或替换二维码期间迟到的创建响应会使用原鉴权取消对应服务端任务。旧代次排队的配置 patch 不会改用新 token 发出，迟到 JSON body 和保存回调也不能覆盖新表单。

## 独立验收

主代理实现配置 API 路径、向导、撤销入口及底层保存事务；`shared_resource_reload` 实现凭据 YAML 定位和语义核对；`platform_live_login` 实现前端会话及扫码 Context 链。独立子代理 `independent_completion_audit` 只审查和编写验收测试，不修改产品实现。这里的独立验收是独立子代理验收，未使用真实平台账号。

| 范围 | 永久测试与验证内容 |
| --- | --- |
| 实际配置 API | `admin/config_identity_acceptance_test.go`：真实 withAuth → handler → 保存 → 运行值链，覆盖大小写/dotted 保护与脱敏、同级项保留、数据键保真、歧义和多文档拒绝、403 标记区分 |
| 初始化与命令映射 | `admin/config_paths_test.go`、`lsp/cfg/custom_prefix_identity_test.go`：uppercase 已有段合并，保留连接 token；外层大小写一致而命令名大小写不变 |
| YAML 局部写入 | `lsp/cfg/yamlutil_nodes_test.go`：14 种格式精确对比、11 种安全拒绝、整组有效值核对；`yamlutil_acceptance_test.go` 独立调用公开写入 API 验证有效配置和失败无变化 |
| 取消凭据事务 | cfg 的 context/acceptance 测试：写锁等候取消、慢 prepare 后取消，验证不落盘、不增加版本、候选 abort；`admin/login_revocation_acceptance_test.go` 通过真实 B站/QQ finish 链验证令牌轮换取消 |
| 服务撤销与旧鉴权 | 同一 admin 独立测试：停止撤销工作、旧鉴权代次不能创建新任务；已有监听迁移回归验证地址变更仍保留扫码会话 |
| 微博真实本地 HTTP | `qrlogin_context_test.go`、`qr_cancel_acceptance_test.go`：出码、poll、成功扫码后 ticket exchange 期间取消，HTTP 断开、结果生产者退出；mode/SUB 慢 prepare 时取消，不落盘、不发布、不应用运行 SUB |
| 前端行为 | 全部 36 项 Node 回归：鉴权失败、旧/新同 token 登录、乱序二维码创建与关闭、迟到响应、两类 SSE 撤销/重连、配置队列和 JSON body 跨会话隔离 |

## 验证命令

- 根模块：`CGO_ENABLED=0 go test ./... -coverprofile=.coverage.out -timeout 120s`，全部通过。
- Bot 子模块：`CGO_ENABLED=1 go test -race ./... -count=1 -timeout 120s`，全部通过。
- 主代理根集成及 cfg：`CGO_ENABLED=1 go test -race -gcflags=github.com/modern-go/gls=-d=checkptr=0 ./ ./lsp/cfg -count=1 -timeout 120s`，通过。
- 前端：两个修改的 JS 文件通过 `node --check`；`node --test admin/web_tests/*.test.cjs` 全部 36 项通过（PowerShell 中先枚举文件再传入 Node）。
- 构建：`CGO_ENABLED=0 go build -buildvcs=false -o <TEMP>/ddbot-config-hot-reload.exe ./cmd`，通过。

独立最终验收通过，无剩余阻塞项；新增红测均在修复后复验：

- 根集成、管理服务、cfg 和微博：`CGO_ENABLED=1 go test -race -gcflags=github.com/modern-go/gls=-d=checkptr=0 . ./admin ./lsp/cfg ./lsp/weibo -count=1 -timeout 180s`，通过。该兼容参数仅关闭既有 `modern-go/gls` 的指针检查，竞态检测保持开启。
- Bot 子模块 `go test -race ./config`、Adapter 子模块 `go test -race ./qqofficial`，通过。
- 独立前端 7 项与实现者、既有回归合计 36 项全部通过，两处 JS 语法检查通过。
- Go 格式与 `git diff --check` 通过；测试生成的区域列表不纳入提交。

## 尚待完善与保留边界

1. 保存或扫码成功后重新读取整份配置，仍可能丢失其他未保存表单草稿；需进一步引入字段级草稿与合并机制。
2. 修改管理监听地址或关闭 WebUI 后，前端尚无完整迁移导航；监听器本身已支持在线变更。
3. 慢资源准备期间用外部编辑器修改文件，尚无基于原文版本的条件写入保护，可能被在途保存覆盖。面板与内部凭据写入已共用写锁，外部编辑尚不参与该锁。
4. 跨协议、跨账号、QQ AppID/状态文件迁移与 extDb 数据迁移仍遵循 [热更新支持说明](../config-hot-reload.md)；本轮未扩大这些迁移范围。
5. 使用临时配置和真实本地 HTTP/WS 模拟，不依赖真实平台账号；未进行真实账号扫码或浏览器视觉验收。
6. 按用户要求仅本地提交，不执行 Git 推送。
