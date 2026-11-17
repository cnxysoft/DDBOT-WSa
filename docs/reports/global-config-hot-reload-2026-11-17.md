# 全局配置热重载现状与可行性报告

调研日期：2026-11-17。调研分支：`next-dev`。基准提交：`f06149323ce2462f10eb424ed3780114b8360462`。

本报告基于当前仓库、实际使用的 Viper v1.20.1 / fsnotify v1.9.0 源码、相关单元测试和临时配置文件实验。实验环境为 Windows amd64、Go 1.26.2。未修改业务实现或真实运行配置，未连接在线 Bot。

## 1. 结论

**项目已有配置文件监听和部分配置热生效能力，但尚不支持可靠、统一的全局配置热重载。**

当前机制主要分为三类：每次使用时读取 Viper 的配置、通过回调更新的配置、初始化时复制到对象或后台任务中的配置。第三类不会因为文件重载而自动更新，前两类还存在事件遗漏、并发安全和状态不一致问题。

此外，本次在 Windows 上实际复现：**调用现有配置写回函数替换配置文件后，Viper 的监听循环可能因收到 REMOVE 事件直接退出；随后再直接编辑同一个文件，也不再触发回调。** 因而现状比“只有连接配置需要重启”更受限制。

可行性判断：

- 统一支持命令、日志、图片合并策略、平台暂停等业务配置：**可行，适合优先实施**。
- 轮询间隔、推送并发、代理和平台凭据：**可行，但要补充模块的重配置能力**。
- 适配器类型、机器人身份、数据库路径等全部无重启切换：需要独立的生命周期和数据迁移设计，**不建议纳入第一版承诺**。

建议的目标是：全局统一管理配置变更，逐字段声明“热生效 / 需重建模块 / 需重启”，并明确返回实际生效结果。

## 2. 当前实现

### 2.1 配置加载与监听链路

```text
bot.go: Run
  → 从当前目录、./config 搜索 application 配置
  → GlobalConfig.ReadInConfig()
  → GlobalConfig.WatchConfig()
  → bot.Init / 模块初始化
  → Lsp.Init 注册 OnConfigChange

Viper 收到文件事件
  → ReadInConfig() 替换内部配置 map
  → OnConfigChange()
      → 写入保护窗口内：直接返回
      → 否则：异步重载自定义前缀、更新日志等级、重建定时任务
```

主要依据：

| 代码位置 | 当前行为 |
| --- | --- |
| `bot/config/config.go:8` | `Config` 直接内嵌 `*viper.Viper`，全局实例没有统一的读写同步层 |
| `bot.go:133`、`bot.go:147` | 配置搜索、初次加载、启动文件监听 |
| `lsp/module.go:450` | 初始化自定义前缀并注册唯一的配置变更回调 |
| `lsp/cfg/config.go:67` | 配置写回跟随 `ConfigFileUsed()`，能够定位实际加载的文件 |
| `lsp/cfg/yamlutil.go:20` | 内部写入前后设置 1500ms 忽略窗口 |
| `admin/server.go:989`、`admin/server.go:1056` | WebUI 整体保存持写锁、设置忽略窗口，并用临时文件替换原文件 |

这里的“重载文件”与“重配运行中的对象”是两件事。调用了 `WatchConfig()`，只能说明存在文件监听，不能说明所有模块都已重新初始化。

### 2.2 配置生效范围

下表中“可热读取”均以 **监听仍有效、文件成功解析且没有更高优先级覆盖值** 为前提，不代表目前已经具备并发安全保证。

| 配置类别 / 示例 | 当前生效方式 | 判断与代码依据 |
| --- | --- | --- |
| `bot.commandPrefix` | 命令匹配时读取 | 可热读取；`lsp/cfg/config.go:41` |
| `customCommandPrefix` | 另行读磁盘，替换原子缓存 | 有回调支持，但内部保存可能跳过更新；`lsp/cfg/config.go:78`、`lsp/module.go:451` |
| `logLevel`、`ffmpeg.logLevel` | 回调执行 `configureLogLevels()` | 有回调支持，同样受忽略窗口影响；`lsp/module.go:86`、`:458` |
| `cronjob` | 回调先删除旧任务再添加新任务 | 有回调支持，缺少整组校验与失败回退；`lsp/cron.go:57` |
| `imageMerge.*`、站点图片合并配置 | 每次渲染读取 | 可热读取；`lsp/cfg/image_merge.go:31` |
| `platform.<site>.enable` | 轮询和发送路径检查 | 可暂停 / 恢复已经启动的模块；不是销毁或重新创建模块；`lsp/concern/stateManager.go:530`、`lsp/notify.go:55` |
| `autoreply.*.command`、`debug.*`、`dispatch.largeNotifyLimit` | 使用时读取 | 可热读取；`lsp/command.go:130`、`lsp/groupCommand.go:50`、`lsp/concern/stateManager.go:663` |
| `bot.offlineQueue.*`、部分通知策略 | 使用时读取 | 部分可热读取；离线队列见 `adapter/messenger.go:1947`。`onlyOnlineNotify` 还被部分轮询逻辑用来初始化计数，不能笼统认为全部行为都会更新 |
| 各站点 `interval`、`concern.emitInterval` | 保存为循环局部变量或 `EmitQueue.TimeInterval` | 已运行的轮询通常不会更新；见下面的具体说明 |
| `notify.parallel` | `Lsp.Init()` 创建信号量 | 不支持热调整；`lsp/module.go:284`、`lsp/notify.go:124` |
| `proxy.type`、`localProxyPool.*`、`pyProxyPool.host` | `Lsp.Init()` 创建代理池 | 不支持整体热切换；`lsp/module.go:381`。个别请求级配置如 `loliconPool.proxy` 有单独的实时读取，不应混同 |
| `imagePool.type`、图池路径与缓存大小 | `Lsp.Init()` 创建图池 | 不支持整体热切换；`lsp/module.go:349` |
| `template.enable` | 启动时决定是否创建模板监听器 | 不支持完整热切换；`lsp/module.go:446`。模板文件本身有独立监听，属于另一项能力，见 `lsp/template/loader.go:37` |
| B 站 / ACFun / 抖音 / 小红书 / Twitch / TwitCasting 凭据 | 初始化账号、Cookie 或客户端时复制 | 普通文件保存不能统一更新运行中的会话；例如 `lsp/bilibili/bilibili.go:92`、`lsp/douyin/init.go:22`、`lsp/xhs/concern.go:81`、`lsp/twitch/concern.go:92` |
| `twitter.mode`、镜像列表、UA、API 凭据 | `setCookies()` 初始化全局状态与客户端 | 无统一重载；`lsp/twitter/init.go:87`。虽有 `RefreshTwitterAPIFromConfig()`，当前仓库只有定义，没有调用点 |
| 微博模式、SUB、自动刷新开关 | 动态判断与运行时 Cookie、后台任务并存 | 部分变化可被读到，但不能保证会话和任务同步更新；`lsp/weibo/concern.go:65`、`lsp/weibo/sub_autorefresh.go:26` |
| `telegram.*` | `sync.Once` 初始化客户端、目标列表与接收循环 | 无配置变化触发的完整重载；错误恢复时重建客户端不能等同于配置热重载；`lsp/telegram/publisher.go:51`、`lsp/telegram_commands.go:21` |
| `adapter.mode`、`websocket.*`、QQ 官方连接配置 | `bot.Init()` 创建适配器 | 需重启或专门的连接重建；`bot/bot/bot.go:269`、`adapter/config.go:35` |
| `admin.enable`、`admin.addr`、普通文件修改的 `admin.token` | `admin.Start()` 创建服务并缓存 token | 需重启；初始化向导的 `s.setToken()` 是显式特例，不是通用重载；`admin/server.go:183`、`admin/setup.go:176` |
| `extDb.enable`、`extDb.path` | `Run()` 打开数据库并注册退出钩子 | 不支持热切换；`bot.go:149` |

轮询间隔的几个明确例子：

- B 站、ACFun、抖音、Twitter、Twitch：在进入轮询循环前读取一次，然后反复用这个局部变量重置定时器。对应 `lsp/bilibili/concern_fresher.go:23`、`lsp/acfun/concern.go:208`、`lsp/douyin/concern.go:341`、`lsp/twitter/concern.go:687`、`lsp/twitch/concern.go:130`。
- 斗鱼、虎牙、微博、小红书、小黑盒：启动时调用 `UseEmitQueueWithSiteInterval()`，间隔保存到队列对象。YouTube 和 TwitCasting 使用全局 `UseEmitQueue()`。见 `lsp/concern/stateManager.go:705`、`:757` 和 `utils/emitQueue.go:164`。

因此，配置 getter 存在、WebUI 能编辑某个字段，都不足以证明现有后台任务会使用新值。

## 3. 可靠性问题

### 3.1 高优先级：Windows 文件替换可能终止监听

`cfg.WriteConfigKeyValue()` 使用 `临时文件 → os.Rename` 写回；WebUI 的 `writeFileAtomic()` 也采用同类操作。

本次使用真实 `cfg.WriteConfigKeyValue()`、真实 Viper WatchConfig 和额外的原始 fsnotify 监听器，观察到以下事件顺序：

```text
CREATE application.yaml.tmp
WRITE  application.yaml.tmp
REMOVE application.yaml
RENAME application.yaml.tmp
CREATE application.yaml
```

Viper v1.20.1 的 `viper.go:365` 在配置路径收到 REMOVE 时直接结束事件循环。实验中，文件已成功写为新值，但 Viper 回调次数是 0；等待超过忽略窗口后再直接写原文件，回调次数仍是 0。

**影响：一次内部配置写回，就可能让之后的热读取能力也失效，直到重新建立监听或重启。** WebUI 保存采用相同的文件替换方式，应纳入同一修复和回归范围；本次没有通过 HTTP handler 对 WebUI 做端到端复现。

这是当前 Windows 环境下的实测结果，不推断所有平台和文件系统一定产生同样的事件顺序。文件真正被删除、再创建时，也需要处理监听恢复。

### 3.2 高优先级：忽略窗口丢弃应用层更新，且不会补载

`lsp/module.go:451` 在 `IsWritingInProgress()` 为真时直接返回。但是 Viper 的实际执行顺序是：

1. 先执行 `ReadInConfig()`。
2. 再调用 `OnConfigChange()`。

所以这个保护只能跳过后续业务更新，不能暂停 Viper 对文件的读取，也无法避免其并发写入内部配置 map。

隔离实验使用真实的写入保护和自定义前缀缓存 API，并按现有回调逻辑执行。普通文件写入后观察到：Viper 新值已经生效，缓存仍为旧值；等待超过 1500ms，缓存仍未更新。当前没有延迟补载逻辑。

**影响：同一份保存可能产生“部分字段是新值、部分模块还是旧状态”。** 日志等级、定时任务同样位于被跳过的回调内。修复时应把事件转成可合并、可重试的待处理变更，不能直接丢弃。

### 3.3 高优先级：共享 Viper 没有并发读写保护

全局配置直接暴露 `*viper.Viper`，业务协程同时调用 `Get*()`，文件监听会替换内部配置，扫码等流程还调用 `Set()`。

Viper v1.20.1 的 README 明确说明：并发读写需要调用方自行同步，否则可能 panic。`ReadInConfig()` 在 `viper.go:1507` 直接赋值 `v.config`；项目的配置文件写锁没有覆盖运行时读取。

这属于源码和依赖契约确认的并发风险；**本次未用 race detector 或并发压力测试复现具体竞态**。仅在回调中加锁不能解决问题，因为 Viper 在回调之前已经写入自身状态；继续保留可绕过封装的内嵌 Viper 也无法形成完整保护。

依赖说明：[Viper v1.20.1 并发读写说明](https://github.com/spf13/viper/blob/v1.20.1/README.md#is-it-safe-to-concurrently-read-and-write-to-a-viper)。

### 3.4 高优先级：缺少统一的校验与失败保留

- Viper 文件解析失败时会保留旧配置，但仍调用变更回调。
- `ReloadCustomCommandPrefix()` 独立再读一次磁盘，且通过 defer 无条件发布解析结果；读文件或解析失败时会把原缓存替换为空。此次已复现“Viper 保留旧值、自定义前缀消失”。
- `CronjobReload()` 先移除全部旧任务，再解析 / 添加新任务。合法 YAML 中的无效 cron 表达式，可能导致旧任务被删、新任务只成功一部分；当前没有整组准备成功后再提交的流程。
- 普通 WebUI 保存主要校验序列化和受保护字段，没有一个覆盖全局字段类型、范围和跨字段依赖的统一校验入口。现有启动时格式检查也不是完整配置 schema 校验。
- 自定义前缀的异步任务独立读文件，可能与其他模块应用的配置版本不同；快速连续修改时也缺少版本排序保证。

### 3.5 中优先级：Set 覆盖值可以长期遮蔽文件修改

Viper 的 `Set()` 写入 override 层，其优先级高于配置文件；`ReadInConfig()` 不会清空这一层。

项目在 B 站登录、微博 SUB 写回、QQ 官方扫码凭据保存等路径使用 `GlobalConfig.Set()`，例如 `admin/login.go:379`、`:548`、`lsp/weibo/qrlogin.go:541`。

实测在 `Set("value", "runtime")` 后把文件改为 `file-new` 并重新加载，getter 仍返回 `runtime`。因此必须明确配置文件、用户保存和自动刷新的运行时凭据之间的优先级与失效规则；不能靠重新读文件隐式解决。

### 3.6 中优先级：模块生命周期尚不适合通用重启

现有 `StateManager.Stop()` 会取消 context 并关闭 channel；`StopAll()` 还会关闭全局通知 channel。启动失败的 Concern 会被移出运行列表。见 `lsp/concern/stateManager.go:461`、`lsp/concern/registry.go:99`、`:127`。

因此不能简单在回调里遍历所有模块执行 `Stop(); Start()`。要支持重建，需保留工厂 / 模块描述，创建新实例、新 context 和新 channel，同时保持订阅、权限、去重和消息队列的正确归属。

QQ 官方机器人还有身份指纹校验：更换机器人可能让内部群号与旧订阅错误关联，当前会拒绝这种启动。相关逻辑在 `lsp/module.go:205` 起。热切换不能绕过该约束。

### 3.7 中优先级：用户界面把保存与生效混为一谈

`admin/web/js/app.js:969` 显示“配置已保存（自动热生效；连接类配置需重启 Bot）”。实际上代理、图池、并发数、轮询间隔、Telegram 等也没有完整热更新能力。

接口目前只返回保存成功，没有字段级生效状态，也没有配置版本、最后一次应用结果和待重启字段。读取接口返回的是磁盘配置，不能据此判断运行对象正在使用哪一份配置。

## 4. 推荐方案

### 4.1 方案选择

| 方案 | 能解决的问题 | 局限 | 建议 |
| --- | --- | --- | --- |
| 仅补几个 `OnConfigChange` 调用 | 个别缓存更新 | 无法解决监听退出、并发安全、验证失败和版本不一致 | 只适合作为小范围临时修复 |
| 同步封装共享 Viper | 可以减少既有 getter 改动 | 必须接管监听、覆盖所有读写入口并封住内嵌指针；资源对象仍需重配 | 可作迁移步骤 |
| 单一配置协调器 + 不可变快照 + 模块重配置 | 统一解析、验证、发布、追踪实际效果 | 初期改造量较大，需要逐步迁移全部入口 | **推荐目标架构** |

不要把运行中的 `GlobalConfig` 指针直接换成新 Viper 就认为完成了原子发布：这无法自动更新模块缓存、客户端和队列，也不能约束旧引用。

### 4.2 单一变更入口

建议把基础能力放入 `bot/config`，保持它不依赖 `lsp` 等上层业务包。业务模块通过注册校验 / 重配置处理器参与，避免引入循环依赖。

```text
文件变动 / WebUI 保存 / 登录凭据更新
  → 同一个配置协调器
  → 合并事件、短暂防抖、按内容去重
  → 读取候选配置并归一化键名
  → 类型、范围、跨字段及各模块校验
  → 计算差异和生效策略
  → 准备必要的新资源
  → 发布新的生效快照 / 资源版本
  → 通知工作协程并记录应用结果
```

关键要求：

1. **监听目录并持续运行**：配置文件出现 REMOVE / RENAME 时重新定位和等待 CREATE，不因一次替换永久退出。支持 Windows 和 Linux 的编辑器保存方式，处理真实删除、重建、监听错误和退出清理。
2. **独立解析候选配置**：使用新解析器构建候选值，Viper 只参与解析；运行中的 getter 读取不可变快照。任何解析 / 校验失败都保留上一份生效配置及派生缓存。
3. **统一发布完整快照**：使用 `atomic.Pointer` / `atomic.Value` 发布。map、slice 需要复制并保持只读；一次请求、渲染或轮询应持有同一份 View，避免多次 getter 跨越不同版本。
4. **接管所有更新来源**：迁移 `Set()` 和直接文件写回；显式维护运行时凭据覆盖层及来源。建议自动刷新的凭据在有效期内覆盖文件，用户显式修改同一凭据时清除旧覆盖并重新验证。最终策略应在实现时固化并测试。
5. **写入后显式申请应用**：WebUI / 内部写回在释放文件锁后提交变更，文件事件只负责兜底。用内容哈希或修订号去重自己的事件，替代 1500ms 丢弃窗口。保留写锁和临时文件替换，不能为规避事件问题改回直接覆盖文件。
6. **串行处理修订**：每次应用都有版本；准备资源耗时较长时，后来的修订不能被较早的异步结果覆盖。网络请求不应占用全局读取锁。
7. **区分期望值和实际值**：磁盘为期望配置，运行快照为生效配置。若允许同一次保存包含热更新字段与待重启字段，后者必须继续保持旧生效值；验证依赖关系时也要以真正能应用的组合为准。

### 4.3 各类配置如何应用

| 类型 | 推荐做法 |
| --- | --- |
| 纯业务标量 / 规则 | 发布快照，下一次操作读取新值；前缀直接从同一快照派生，避免再次读文件 |
| 日志等级 | 先校验全部等级，再应用；报告具体修订和结果 |
| 定时任务 | 先编译并校验整组 schedule 和模板引用，再提交；约定已经运行的旧任务是否允许结束，避免重复触发 |
| 轮询与 EmitQueue 间隔 | 将更新消息交给拥有 Timer 的工作协程处理，安全 Reset；明确“下一轮”或“立即调整”的生效语义，并保留站点最小间隔和抖动策略 |
| 推送并发 | 使用可调整容量的 limiter / worker pool，或排空后切换。不能直接替换 `l.msgLimit`：旧任务必须向原来 Acquire 的实例 Release |
| 代理池 / 图池 | 准备新池，验证成功后切换引用；旧请求持有旧池，退出后再关闭。当前 `proxy_pool.Init()` 是未同步的全局赋值，不能直接用于并发热切换 |
| 平台凭据与模式 | 原子替换完整的凭据 / 客户端状态，失效相关缓存；模式切换要显式启停任务。优先接入已有的 `SetVerify`、`UpdateFromConfig` 等安全更新能力 |
| Telegram | 用受管理的客户端和 context 替代不可重置的 Once 生命周期，旧接收循环停止后再启动新循环，避免双重收消息 |
| `template.enable` | 模板服务支持幂等启停；与模板文件自身监听分开管理，重新启用时加载最新模板 |
| 适配器、机器人身份、数据库 | 第一版标记为需重启；后续按独立需求设计连接排空、身份验证与数据迁移 |

资源更新需要“准备成功再切换、失败保留旧资源”。涉及多个资源时，应将同一修订的配置和资源句柄绑定发布，或明确暴露各模块应用进度；单独换一个配置指针并不能提供跨模块事务。工作协程确认配置通知前，也不能宣称所有副作用已在同一时刻完成。

## 5. 实施顺序和工作量

以下为单个熟悉项目的工程师的初步工程日估算，包含相应测试，不含外部平台等待和完整协议切换；需在实现前的字段清单梳理后复核。

| 阶段 | 交付内容 | 粗估 |
| --- | --- | --- |
| 第一阶段：统一可靠入口 | 修复替换文件 / 删除重建监听、替代忽略窗口、快照读取、覆盖值策略、保留旧配置、现有热更新字段迁移、WebUI 生效状态 | 5–8 日 |
| 第二阶段：调度参数 | Timer / EmitQueue 重配置、推送并发调整、定时任务整组准备和提交 | 3–5 日 |
| 第三阶段：资源和平台 | 代理 / 图池 / 模板服务重配、优先平台凭据更新与后台任务管理 | 8–15 日，按平台拆分 |
| 后续独立项目 | 适配器协议、机器人身份或数据库运行中迁移 | 单独评估，不计入上述范围 |

第一阶段即可形成有明确边界的、约 1–2 周的可交付版本。较广泛的业务配置覆盖按上述粗估合计约 16–28 工程日；不宜承诺所有配置无重启生效。

建议第一阶段同时修正 WebUI 文案，返回 `saved_revision`、`applied_revision`、`applied_keys`、`pending_restart_keys`、`failed_keys` 和脱敏错误信息。保存成功只表示落盘完成；应用状态另外展示。

## 6. 验收要求

1. 直接覆盖、临时文件替换、编辑器保存、删除再创建都能恢复读取；Windows / Linux 分别验证，不能只测试写回函数返回成功。
2. WebUI、外部编辑、扫码登录和自动刷新通过同一个变更机制；重复事件不会重复启动后台任务，连续变更最终应用最后一版。
3. YAML 错误、字段类型错误、非法间隔、无效 cron、跨字段冲突均不破坏旧生效状态；显式清空合法字段与解析失败要区分。
4. 保护窗口期间的变更最终得到应用；不出现 Viper 已更新而前缀 / 日志 / cron 仍停留旧状态。
5. 文件删除字段、缺省值、大小写等价键、运行时覆盖值失效规则有明确结果。真实配置路径继续跟随已加载文件。
6. 变更期间继续处理命令、渲染和推送；运行 race 检查，并验证一次操作使用同一修订。
7. 修改间隔确实影响下轮调度；动态调小并发时不超发、不错误 Release，旧任务能正常结束。
8. 平台从暂停恢复能继续运行；启动失败后补齐凭据的恢复行为单独验收，不能只依赖 `platform.enable`。
9. 资源准备失败保留旧客户端；旧 goroutine、连接和 Timer 能退出，无重复轮询 / 重复 Telegram 接收 / 订阅丢失。
10. 待重启字段不会改变当前身份或数据库句柄，界面正确展示期望值与生效值差异，凭据保持脱敏及原有 API 写权限约束。

后续代码改造的测试范围应包含主模块及受影响的嵌套模块：根目录的 `go test ./...` 不会自动覆盖 `bot`、`adapter` 等独立 `go.mod` 的全部测试。

## 7. 本次验证记录与限制

### 7.1 已执行的现有测试

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'ddbot-hot-reload-gocache'
$env:GOPROXY = 'off'
go test ./lsp/cfg ./admin -run 'Test(ConfigFilePath|ReloadCustomCommandPrefix|IsPlatformDisabled|ImageMerge|HandleConfigPost|WriteFileAtomic|CollectConfigWriteViolations)' -count=1
```

结果：两个包均通过。覆盖实际配置路径、自定义前缀解析、图片合并配置 getter、平台开关、WebUI 配置保存和文件替换等相关单元行为。缓存位置调整仅用于绕开当前环境的默认 Go 构建缓存访问问题。

这些测试通过不等于全局热重载通过：现有文件保存测试检查落盘内容，并未覆盖保存后整个监听与模块应用链路。图片合并测试也主要通过 `Set()` 验证 getter，不是完整 fsnotify 集成测试。

### 7.2 临时文件隔离实验

实验只使用临时目录。保护窗口实验使用真实 `cfg.MarkConfigWriteStart/End()`、`cfg.ReloadCustomCommandPrefix()` 和 Viper，回调按项目现有的跳过 / 异步重载分支搭建；文件替换实验直接调用真实 `cfg.WriteConfigKeyValue()`。没有调用 `Lsp.Init()` 或启动完整 Bot。

| 实验 | 观察结果 |
| --- | --- |
| 普通写入且处于保护窗口 | `viper="new" cached="old" ignored=true` |
| 等待 1800ms，超过保护窗口 | 缓存仍是 old，没有自动补载 |
| 窗口外普通写入 | Viper 和前缀缓存都更新为 external |
| 窗口外写入无效 YAML | Viper 保留 external，前缀缓存变空 |
| `Set()` 后更新文件并 ReadInConfig | getter 继续返回 runtime 覆盖值 |
| Windows 下调用真实写回函数 | 观察到 REMOVE → CREATE；文件成功更新，Viper 回调为 0 |
| 上一实验后等待超过窗口，再直接写入文件 | 原始 fsnotify 能看到 WRITE，Viper 回调仍为 0 |

本次未运行全量测试、race detector、Linux 文件事件实验或在线平台集成验证；上述限制不影响“当前仅部分支持、且存在已复现可靠性缺陷”的结论。实施方案中的资源无损切换仍需通过相应集成测试证明。
