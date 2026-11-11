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
