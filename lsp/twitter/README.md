# Twitter 镜像模式

默认使用 `twitter.mode: mirror`，无需 Twitter 账号 Cookie。显式配置 `api` 时仍使用账号 API。
`twitter.baseUrl` 非空时会覆盖内置列表；已有配置要使用新增实例，需同步修改列表。

内置实例依次为：

- `https://nitter.tiekoetter.com/`
- `https://nitter.mizuki.st/`
- `https://nitter.ip6arpa.uk/`
- `https://nitter.catsarch.com/`
- `https://nitter.jaydenha.uk/`
- `https://nitter.click/`

查找订阅用户与后台刷新共用同一抓取流程，优先复用上次成功的实例。实例失败后依次尝试其他
实例，并将失败实例冷却 30 秒；连续失败时按倍数延长，最长 15 分钟。冷却到期后允许重试，
请求成功即清除失败记录。全部实例都在冷却中时会返回错误，等待后续轮询，不会持续重试。
用户不存在、封禁或受保护等账号错误不会触发整个实例的冷却。

同一用户页的验证最多请求三轮，使用相同的代理出口与 Cookie 会话。已支持：

- Anubis `fast` / `slow`：SHA-256 工作量证明，计算限时 5 秒。
- Anubis `hashx`：获取当前实例的 baseline WebAssembly worker，通过纯 Go 的 wazero 运行。
  模块最多 256 KiB，运行内存最多 32 MiB，执行限时 5 秒，不开放 WASI、文件或网络接口。
- Anubis `css-load` 扩展。
- Poast 验证及 click 的同源、同用户继续链接。

解析器同时兼容 Nitter 与 NitterTS 的图片、MP4、HLS、GIF、转发与引用媒体，并保留正文换行。
缺少时间标题时由推文 ID 恢复发布时间。

`https://nitter.xitter.cc/` 与 `https://nitterts.pages.dev/` 可自行加入 `baseUrl` 作为可选备用。
2026-11-11 复测时，xitter 请求返回上游 502；NitterTS 曾成功返回 NASA 和 OpenAI 页面，
随后同样返回 502。因此保留协议兼容，不加入默认列表。catsarch 的 HashX 与 CSS 扩展已通过
NASA、OpenAI 两个账号的抓取验证。上述为当日实测，并不保证公共实例长期可用。

RSS 暂不作为完整推送后端：已有同推文对照样本中，视频只提供封面，缺少模板发送视频所需的
播放地址。新增实例需同时验证资料、正文、时间、转发作者、引用和媒体字段。

## 推送链路验收

2026-11-11 使用本地镜像服务串联订阅、轮询、媒体下载转换、默认模板和消息适配器，
覆盖纯文字、图片、MP4、HLS、GIF、转推、引用图片、引用视频、视频后接引用及多个视频。
CLI 与内置 FFmpeg 构建均通过；首次订阅不推送存量，再次轮询及切换镜像不重复生成事件。
视频独立分段发送，保留其后的引用和其他媒体；推文链接仅输出一次。

真实媒体验证中，catsarch 的图片、CLI HLS 与 tiekoetter 的图片、CLI/内置 FFmpeg MP4
下载及消息组装成功。内置 FFmpeg 请求 catsarch HLS 的初始化分片时曾出现 I/O 错误，
该路径仍有网络可用性限制。

同日通过本机 Satori/LLOneBot 向专用测试群发送并回读 6 类样本、12 条消息，覆盖真实图片、
GIF、视频后接引用、引用视频、多个视频和真实转推视频。服务端返回的 GIF 保留 3 帧动画。
验收同时修复了 Satori 发送后缺少原始消息 ID 映射的问题，回读和引用无需依赖消息事件到达。
已有自定义 Twitter 模板会覆盖默认模板，需自行同步视频分段改动。

## 故障诊断与媒体超时

镜像失败日志包含 `Mirror`、`userId`、`attempt`、`stage`、`http_status`、`duration_ms`
和适用时的 `cooldown_seconds`。`stage` 区分请求、解压、解析、验证、账号状态及资料校验；
实例切换成功会记录信息日志。打开 debug 日志可查看每次请求、解析耗时和冷却跳过情况。
媒体日志另含 `media_host`、`media_type`、`via_proxy`、耗时及成功文件大小，便于区分
镜像抓取失败和媒体下载转换失败。

CLI 与内置 FFmpeg 均设置 30 秒网络读写超时，直连同样生效；CLI 转换限时 3 分钟，
内置 FFmpeg 的探测及输入读取通过中断回调限时 3 分钟。
内置 FFmpeg 将多码率输入选为一条视频及其关联音频，避免把所有码率写入同一个 MP4。
读取失败或写入文件尾失败会返回错误；推送流程清除失败的临时文件并沿用 URL 回退。
HLS 的首次打开仍可能访问其他码率的初始化分片；选流不保证绕过这一阶段的上游故障。

2026-11-13 在系统 FFmpeg CLI 与 `v7.1.5-1` 精简 CGO 后端下复验，Twitter、模板和 FFmpeg
包测试及 vet 均通过。上述 10 类本地完整推送场景在两种后端下全部通过，
包括首次订阅、重复轮询及镜像切换后的去重；本轮消息由本地适配器截获。
