# FFmpeg 媒体处理与日志

CLI 和内置 FFmpeg 共用 DD 的日志输出：文件、控制台和管理页面使用相同的格式与过滤规则。
原生日志携带 `module=ffmpeg`、`backend=cli|cgo`、`ffmpeg_level`，有上下文时还包含
`component`（例如 `hls` 或 `https`），不再直接向 stderr 打印另一套格式。

```yaml
logLevel: info
ffmpeg:
  logLevel: warning
```

`ffmpeg.logLevel` 默认为 `warning`，支持 `quiet`、`panic`、`fatal`、`error`、`warning`
（或 `warn`）、`info`、`verbose`、`debug`、`trace`；等级越靠后，信息越详细。
`quiet` 关闭 FFmpeg 原生日志，转换失败仍会由上层推送流程报告。无效值保留当前设置。
修改配置会热更新；内置 FFmpeg 立即采用新阈值，CLI 的原生阈值在下次启动转换时采用。

| FFmpeg 原始等级 | DD 等级 |
| --- | --- |
| panic / fatal / error | error |
| warning | warn |
| info | info |
| verbose / debug | debug |
| trace | trace |

全局 `logLevel` 继续过滤映射后的日志。排查 HLS 时可将全局设为 `debug`、
`ffmpeg.logLevel` 设为 `debug`；排查完恢复 `info` / `warning`。
原生 panic/fatal 表示媒体处理错误，只记录为 error，不触发 DD 退出或 panic。
GIF 转换内部的阶段日志同样受此配置控制，逐帧和逐包信息仅在 trace 下输出。

网络读写超时为 30 秒；CLI 通过进程上下文将整次转换限时 3 分钟，内置 FFmpeg 则通过
中断回调为探测及输入读取设置 3 分钟期限。多码率 HLS 的 MP4 输出选择一套视频和关联音频；
首次打开仍可能探测其他码率，不能据此保证上游每个初始化分片都可用。

Windows 内置 FFmpeg 显式使用 `-static`，将 MinGW 运行库一同静态链接，避免
新版工具链的时间函数引入 `libwinpthread-1.dll` 分发依赖。候选静态库与配套头文件
必须同时替换；本地在原路径替换 `.a` 后，应强制重建，Go 缓存不会自动跟踪外部静态库内容。

发布 CI 当前固定下载 `v7.1-131`，并使用 `.github/ffmpeg-static-v7.1-131.sha256`
逐个平台验证 ZIP；更新依赖时须同时更新 release 和校验清单。精简候选尚未替换发布依赖。

验证不包含 x264 的精简候选库时，使用 `CGO_ENABLED=1 go test -tags ffmpeg_minimal ./ffmpeg`
及 `go build -tags ffmpeg_minimal ./cmd`。该标签仅省去 x264 链接，须与精简候选的头文件和库配套；
默认构建继续使用包含 x264 的现有发布库。
