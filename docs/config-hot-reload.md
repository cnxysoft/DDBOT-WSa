# 配置热更新

配置管理器统一处理 WebUI 保存、内部单键回写和外部文件变更。支持热更新的字段在校验成功后发布；连接等需要重建资源的字段保存到文件，并显示为「需重启」。未列入支持范围的字段默认需要重启。

## 生效范围

| 配置 | 生效时机 |
| --- | --- |
| `logLevel`、`ffmpeg.logLevel` | 保存成功时更新日志等级 |
| `bot.commandPrefix`、`customCommandPrefix` | 保存成功时更新；自定义命令保留大小写，空表清空前缀 |
| `cronjob` | 任务实际改变时，全部校验通过后替换；无关保存保持间隔任务的执行时间，已开始执行的任务继续完成 |
| `platform.<site>.enable` | 后续轮询和推送读取新开关 |
| `concern.emitInterval` | EmitQueue 下一次安排计时器时读取 |
| B站、ACFun、抖音、Twitter、Twitch、微博、虎牙、斗鱼、小红书、小黑盒的检测间隔 | 下一次安排计时器时读取；保留平台默认值和随机抖动 |
| `imageMerge.*`、`<site>.imageMerge.*`、`bilibili.imageMergeMode` | 后续图片渲染使用新设置 |
| B站关注策略、专栏/动态解析开关、ACFun 关注策略、Twitter 取消关注与转发全文开关 | 后续对应操作读取新设置 |
| B站、ACFun、抖音、微博、YouTube、小红书、小黑盒的 `onlyOnlineNotify` | 后续通知过滤读取新设置 |
| Twitter 翻译开关、目标语言、最小间隔和批次等待时间 | 后续翻译与等待使用新设置；已进行的请求继续完成 |
| TwitCasting 名称和通知内容选项 | 后续通知读取新设置 |
| 进群改名、断线动作、离线缓存开关及有效期 | 后续对应事件读取新设置 |
| 自动回复命令列表、调试群/用户列表、大通知阈值、微博 Cookie 告警开关 | 后续对应操作读取新设置 |
| `notify.parallel`、`twitch.onlyOnlineNotify` | 需要重启；当前实现启动时捕获这些值 |
| Adapter/WebSocket、数据库、代理池、图片池、模板功能开关、Telegram、平台客户端凭据/运行模式、WebUI 监听参数 | 需要重启；本次未增加这些资源的在线重建 |

平台间隔键为 `bilibili.interval`、`acfun.interval`、`douyin.interval`、`twitter.interval`、`twitch.interval`、`weibo.interval`、`huya.interval`、`douyu.interval`、`xhs.interval`、`heybox.interval`。YouTube 通过全局 EmitQueue 间隔调度。

间隔调整不会打断当前请求或正在等待的计时器；当前一轮结束后，下一轮调度采用新间隔。零值使用已有默认/继承逻辑。Twitter 兼容原有的纯数字秒数，其余平台建议使用 `30s`、`2m` 等带单位格式。

## 保存与失败处理

- WebUI 候选配置通过 YAML、已接入热更新字段的类型、日志等级、间隔、图片合并枚举、全部 cron 表达式及目标 ID 校验后才写入。无效候选返回 HTTP 400，文件和运行配置保持原样。避免将数组前缀、任意字符串开关或布尔群号弱转换后投入使用。
- 外部文件变更无效或文件暂时不可读时，保留上次有效配置，并在状态中显示失败原因；文件修复后自动恢复。
- 文件监听监视父目录，兼容临时文件重命名、删除后重建，100ms 合并连续事件，另有每 2 秒的检查兜底。内容相同的事件不会重复应用。
- 内部写入不再使用忽略窗口。WebUI 返回成功前，配置发布及前缀、日志、cron 应用已完成；状态版本和时间在回调结束后一起更新，保存响应携带此次保存的状态。
- 需要重启的字段保留运行值。将文件改回已运行的值，会清除对应的重启提示。
- 专用扫码登录流程仍可更新其运行时状态。B站 WebUI 登录在验证当前客户端并持久化 Cookie 后确认对应凭据已应用；单纯调用运行时 `Set` 不代表底层连接已重建，不会据此隐藏重启提示。

这里的校验覆盖配置格式与已接入的热更新字段；需要重启的第三方资源仍由原有初始化代码校验连接、权限和凭据。尚未对实际平台连接进行运行中的端到端验收。

## WebUI / API

`GET /api/v1/config` 保持原有的脱敏配置对象结构，展示文件中保存的配置。

`POST /api/v1/config` 的请求格式保持不变，成功响应增加 `reload`：

```json
{
  "status": "success",
  "reload": {
    "revision": 2,
    "appliedAt": "2026-11-17T10:00:00Z",
    "restartRequired": ["websocket.ws-server"],
    "watching": true
  }
}
```

新增 `GET /api/v1/config/status` 使用相同鉴权，返回 `reload` 对象的内容；外部重载失败时另有 `lastError`。状态只包含版本、时间、字段路径和错误摘要，不返回凭据值。WebUI 配置页每 5 秒刷新状态，并在保存后立即反馈。

各配置表单和平台开关按用户操作顺序串行保存，失败后后续保存仍可继续。状态和配置读取有请求代次保护，迟到响应不能覆盖本页较新的保存结果；Bot 重启后版本从 1 重新开始仍可正常显示。

## 开发约定

应用代码通过 `Config` 方法访问配置，不能直接读写兼容构造用的 `Config.Viper` 字段。跨多个字段的一次业务操作可先取得 `Snapshot()`，确保读取同一版本。已发布的 Viper 实例不再修改；运行时 `Set` 使用复制后发布。

新增热更新项时，先确认实际消费者会动态读取，或为资源提供明确的更新机制，再加入 `lsp/cfg/reload.go` 的策略与校验。资源所有者确实完成更新后，才可调用 `MarkApplied` 清除对应的重启状态。

`OnConfigChange` 回调同步执行并串行化，不得在回调内再次调用 `Set`、`Save`、`Reload`、`MarkApplied` 或配置监听生命周期方法。读取快照是安全的。

主要回归覆盖目录监听连续替换/重建、失败保留、并发读写与快照一致性、运行时覆盖、重启差异撤回、WebUI 保存与紧随其后的外部变更、无效 cron 保留原任务，以及调度器重新读取间隔。

## 本次验证

- 根模块与 `bot` 子模块：`CGO_ENABLED=0 go test ./...` 通过。
- `adapter` 子模块及其 OneBot、QQ 官方、Satori 包回归通过。
- 本机原生构建：`CGO_ENABLED=1 go build ./cmd` 通过。
- `bot/config`、`lsp/cfg` 和新增 EmitQueue 场景的标准 `-race` 测试通过。
- admin、lsp 新增场景的标准 `-race` 在初始化阶段被已有 `modern-go/gls` 的 `checkptr` 检查阻断。仅对该依赖关闭指针检查后，保留竞态检测的回归通过：`go test -race -gcflags=github.com/modern-go/gls=-d=checkptr=0 ./admin ./lsp -run 'Test(Config|CronReload)'`。
- `node --check admin/web/js/app.js`、`node --test admin/web_tests/config_reload.test.cjs`（8 项真实函数行为回归）与 `git diff --check` 通过。未启动真实 Bot 或进行浏览器视觉验收。
