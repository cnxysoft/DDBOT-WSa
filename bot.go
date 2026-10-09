package DDBOT

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/admin"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/cnxysoft/DDBOT-WSa/utils"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/warn"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"

	_ "github.com/cnxysoft/DDBOT-WSa/logging"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
	_ "github.com/cnxysoft/DDBOT-WSa/msg-marker"
)

// SetUpLog 使用默认的日志格式配置，会写入到 logs 文件夹内，日志会保留七天
func SetUpLog() {
	writer, err := rotatelogs.New(
		path.Join("logs", "%Y-%m-%d.log"),
		rotatelogs.WithMaxAge(7*24*time.Hour),
		rotatelogs.WithRotationTime(24*time.Hour),
	)
	if err != nil {
		logrus.WithError(err).Error("unable to write logs")
		return
	}
	formatter := &logrus.TextFormatter{
		FullTimestamp:    true,
		PadLevelText:     true,
		QuoteEmptyFields: true,
		ForceQuote:       true,
	}
	logrus.SetOutput(writer)
	logrus.SetFormatter(formatter)
	logrus.AddHook(lfshook.NewHook(
		lfshook.WriterMap{
			logrus.DebugLevel: os.Stdout,
			logrus.InfoLevel:  os.Stdout,
			logrus.WarnLevel:  os.Stderr,
			logrus.ErrorLevel: os.Stderr,
			logrus.FatalLevel: os.Stderr,
			logrus.PanicLevel: os.Stderr,
		},
		formatter,
	))
}

// Run 启动 bot，这个函数会阻塞直到收到退出信号
func Run() {
	if fi, err := os.Stat("application.yaml"); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("警告：没有检测到配置文件 application.yaml，正在生成，如果是第一次运行，可忽略")
			if err := ioutil.WriteFile("application.yaml", []byte(exampleConfig), 0755); err != nil {
				warn.Warn(fmt.Sprintf("application.yaml 生成失败 - %v", err))
				os.Exit(1)
			} else {
				fmt.Println("最小配置 application.yaml 已生成，请按需修改，如需高级配置请查看帮助文档")
			}
		} else {
			warn.Warn(fmt.Sprintf("检查 application.yaml 文件失败 - %v", err))
			os.Exit(1)
		}
	} else {
		if fi.IsDir() {
			warn.Warn("检测到 application.yaml，但目标是一个文件夹！请手动确认并删除该文件夹！")
			os.Exit(1)
		} else {
			fmt.Println("检测到 application.yaml，使用存在的 application.yaml")
		}
	}

	config.GlobalConfig.SetConfigName("application")
	config.GlobalConfig.SetConfigType("yaml")
	config.GlobalConfig.AddConfigPath(".")
	config.GlobalConfig.AddConfigPath("./config")

	err := config.GlobalConfig.ReadInConfig()
	if err != nil {
		warn.Warn(fmt.Sprintf("读取配置文件失败！请检查配置文件格式是否正确 - %v", err))
		os.Exit(1)
	}
	config.GlobalConfig.WatchConfig()

	// 根据配置启用 EXT 数据库
	if cfg.GetExtDbEnable() {
		err = template.InitTemplateDB(cfg.GetExtDbPath())
		if err != nil {
			if err == localdb.ErrLockNotHold {
				warn.Warn("tryLock 数据库失败：您可能重复启动了这个 BOT！\n如果您确认没有重复启动，请删除.lsp_ext.db.lock 文件并重新运行。")
			} else {
				warn.Warn("无法正常初始化数据库！请检查.ext.db 文件权限是否正确，如无问题则为数据库文件损坏，请阅读文档获得帮助。")
			}
			return
		}
		db := template.GetTemplateDB()
		// 添加数据库关闭钩子
		utils.AddExitHook(func() {
			db.Close()
		})
	}
	// 输出本次站点模块启动计划
	logSiteStartupPlan()

	// 快速初始化
	bot.Init()

	// 初始化 Modules
	bot.StartService()

	_, _ = admin.Start(&bot.Instance.Online, nil)

	// 刷新好友列表，群列表
	//以后刷新
	// bot.RefreshList()

	lsp.Instance.PostStart(bot.Instance)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	bot.Stop()
}

// logSiteStartupPlan 输出本次站点模块启动计划，便于确认 sites.enabled 配置是否生效。
//
// 逐条对齐成品 main.logSiteStartupPlan（x64 0xdaed80, 1312B）：
//   - 头部/尾部横幅恒输出；
//   - "计划启动的站点模块"（enabled_sites）在成品内联汇编中无 len 判断，恒输出；
//   - disabled_sites / unknown_sites 仅 len>0 时输出；
//   - "未配置 sites.enabled..." 使用 Info 级别（成品汇编 level=4）。
func logSiteStartupPlan() {
	enabled, disabled, unknown, configured := concern.StartupSiteSelection()
	logrus.Info("========== 站点模块启动配置 ==========")
	logrus.WithField("enabled_sites", enabled).Info("计划启动的站点模块")
	if len(disabled) > 0 {
		logrus.WithField("disabled_sites", disabled).Info("已停用的站点模块")
	}
	if len(unknown) > 0 {
		logrus.WithField("unknown_sites", unknown).Warn("sites.enabled 中存在不支持的站点名称")
	}
	if !configured {
		logrus.Info("未配置 sites.enabled，按兼容模式启动全部站点模块")
	}
	if cfg.GetDefaultBlockAllGroups() {
		logrus.Info("群交互策略：默认全部block；管理员私聊 /block 群号 -d 后放行")
	} else {
		logrus.Info("群交互策略：默认允许；仅手动block的群不响应")
	}
	if cfg.IsAtAllRemainingLimiterEnabled() {
		logrus.WithFields(logrus.Fields{
			"remaining_threshold":    cfg.GetAtAllRemainingThreshold(),
			"bilibili_uid_whitelist": cfg.GetAtAllBilibiliUIDWhitelist(),
		}).Info("atAll账号剩余次数限制已启用")
	} else {
		logrus.Info("未配置 atAll，账号剩余次数限制未启用")
	}
	logrus.Info("======================================")
}

var exampleConfig = func() string {
	s := `
### 注意，填写时请把井号及后面的内容删除，并且冒号后需要加一个空格
bot:
  # true（默认）：所有群默认处于block状态，不响应群指令、链接解析和群事件模板；
  # 主动的直播/动态推送不受影响。管理员私聊 /block 群号 -d 可放行指定群。
  # false：恢复旧版行为，未手动block的群默认可响应。
  defaultBlockAllGroups: true
  onJoinGroup: 
    rename: "【bot】"   # BOT 进群后自动改名，默认改名为"【bot】"，如果留空则不自动改名
  offlineQueue:   # 离线缓存：BOT 离线时暂存要发送的消息，上线后重新发送（期间不能重启 DDBOT）
    enable: false # 是否启用离线缓存
    expire: 30m   # 离线消息有效期
  imageSend:      # OneBot图片发送方式；修改后重启DDBOT
    mode: auto    # auto保持原方式；可切换 url（网络地址）/ base64 / file（本地临时文件）
    timeout: 30s  # base64/file模式下载网络图片的超时
    tempDir: ".image-send" # file模式临时目录；DDBOT与OneBot需在同一台机器或共享此路径

sites:
  # 本版本支持 B站、抖音、微博、猫耳FM。默认只启动B站；需要时加入 douyin / weibo / missevan，修改后重启。
  enabled:
    - bilibili

# B站直播和动态必须登录后使用，不再提供无Cookie慢速轮询。
# 登录方式二选一：qrcode 扫码/手填Cookie，或 cookiecloud。
# b 站相关的功能建议使用小号
# bot 将使用您 b 站帐号的以下功能：
# 关注用户 / 取消关注用户 / 查看关注列表
# 请注意，订阅一个账号后，此处使用的 b 站账号将自动关注该账号
bilibili:
  cookieMode: qrcode # Cookie来源：qrcode（默认扫码/兼容手填Cookie）或 cookiecloud
  cookieCloud:       # 仅 cookieMode: cookiecloud 时生效；客户端只下载并在本地解密，不会上传Cookie
    endpoint:        # CookieCloud服务端地址，例如 https://cookie.example.com；部署在子目录时需包含子目录
    uuid:            # CookieCloud浏览器插件中配置的UUID
    password:        # CookieCloud浏览器插件中配置的端到端加密密码
    interval: 10m    # 启动时立即获取，此后定时获取；最小1m，未配置默认10m
    timeout: 15s     # 单次请求超时，未配置默认15s
    cryptoType: auto # auto（推荐，读取服务端标记）、legacy 或 aes-128-cbc-fixed
  SESSDATA: # 你的 b 站 cookie；扫码后可能显示为逗号连接的三段，这是一个完整值，请勿拆分
  bili_jct: # 你的 b 站 cookie
  DedeUserID: # 扫码登录时自动保存；与 SESSDATA、bili_jct 同时完整才会覆盖旧登录信息
  ac_time_value: # 扫码接口返回的网页 refresh token，自动保存
  qrlogin: true # qrcode模式下允许管理员确认后生成二维码；程序不会主动生成
  interval: 25s # 未单独配置动态探测间隔时沿用；也用于部分首次直播状态加载
  dynamic:
    probeInterval: 3    # 网页新版增量探测秒数；1就是1秒，0/未配置时沿用上面的interval
    jitter: 0s          # 每轮随机抖动；追求固定3秒探测时保持0s
    maxBackoff: 15m     # 网络错误、5xx或风控后的最大退避时间
    reconcileInterval: 1m # 即使增量计数为0，也定期拉一次列表兜底；最小30s，默认1m
    seenLimit: 5000     # 内存中保留的最近动态ID数量，用于本轮幂等去重
  imageMergeMode: "auto" # 设置图片合并模式，支持 "auto" / "only9" / "off"
                          # auto 为默认策略，存在比较刷屏的图片时会合并
                          # only9 表示仅当恰好是 9 张图片的时候合并
                          # off 表示不合并
  hiddenSub: false    # 是否使用悄悄关注，默认不使用
  unsub: false        # 是否自动取消关注，默认不取消，如果您的 b 站账号有多个 bot 同时使用，取消可能导致推送丢失
  minFollowerCap: 0        # 设置订阅的 b 站用户需要满足至少有多少个粉丝，默认为 0，设为 -1 表示无限制
  disableSub: false        # 禁止 ddbot 去 b 站关注帐号，这意味着只能订阅帐号已关注的用户，或者在 b 站手动关注
  onlyOnlineNotify: false  # 是否不推送 Bot 离线期间的动态和直播，默认为 false 表示需要推送，设置为 true 表示不推送
  disableCookieAlert: false # 设为 true 可关闭 B站 Cookie 异常/恢复告警；接收目标复用 /config weibo_alert 配置
  autoParsePosts: false    # 自动解析专栏，将发送专栏动态改为发送专栏内容
  secAnalysis: false        # 是否开启动态二次解析，默认关闭

# 抖音直播与作品推送（仅在 sites.enabled 中加入 douyin 后启动）
douyin:
  cookieMode: qrcode # Cookie来源：qrcode（默认扫码/兼容手填及状态文件）或 cookiecloud
  cookieCloud:       # 仅 cookieMode: cookiecloud 时生效，可填写与B站相同的CookieCloud服务配置
    endpoint:        # CookieCloud服务端地址，例如 https://cookie.example.com；部署在子目录时需包含子目录
    uuid:            # CookieCloud浏览器插件中配置的UUID
    password:        # CookieCloud浏览器插件中配置的端到端加密密码
    interval: 10m    # 启动时立即获取，此后定时获取；最小1m，未配置默认10m
    timeout: 15s     # 单次请求超时，未配置默认15s
    cryptoType: auto # auto（推荐，读取服务端标记）、legacy 或 aes-128-cbc-fixed
  cookie:             # 推荐：浏览器开发者工具复制的完整 Cookie；留空时默认尝试纯HTTP扫码
  userAgent:          # Cookie对应浏览器的User-Agent；留空使用内置稳定UA
  qrlogin: true       # 没有可用Cookie时允许管理员私聊回复“生成 抖音”后生成二维码；不会主动生成
  qrloginTimeout: 3m  # 扫码等待时间
  stateFile: ".douyin-state/cookies.json" # 扫码取得的完整Cookie Jar，权限限制为仅当前用户读取
  requestTimeout: 15s # 单次抖音HTTP请求超时
  liveInterval: 30s   # 直播状态轮询间隔
  newsInterval: 2m    # 作品列表轮询间隔；作品接口更易风控，建议不要低于1m
  requestSpacing: 2s  # 所有抖音请求严格串行，且相邻请求至少间隔此时长；同一主播跨群每轮只请求一次
  # interval: 30s     # 旧版兼容项；仅当 liveInterval/newsInterval 未配置时作为共同间隔
  notLiveConfirmTimes: 3 # 连续确认多少次下播才推送，避免单次接口抖动误报
  onlyOnlineNotify: false
  disableCookieAlert: false # true时关闭抖音Cookie异常/恢复告警
  linkParse: false      # 默认关闭；需要时改为true，自动解析群聊/私聊中的抖音链接
  linkParsePrivate: true # 是否同时解析私聊中的抖音链接
  linkParseGroupWhitelist: [] # 可解析的QQ群号；与用户白名单均为空时不限制
  linkParseUserWhitelist: []  # 可触发解析的QQ号；群聊满足“群或用户任一白名单”即可，私聊只看此项
  linkParseSendVideo: true # 视频作品除文字和封面外，再单独发送视频；失败不影响前一条解析结果
  linkParseMaxImages: 9 # 图文最多取多少张；1张普通发送，大于1张强制使用合并转发，最高20
  linkParseDedup: 1m   # 同一群/私聊重复链接的去重时间，避免重复下载和发送
  # 以下三项仅兼容旧配置，不建议新部署继续使用：
  # acSignature:
  # acNonce:
  # sessionId:

# 猫耳FM直播与新声音推送（在 sites.enabled 中加入 missevan 后启动；无需登录或Cookie）
missevan:
  liveInterval: 15s     # 直播状态检查间隔，最低5秒
  newsInterval: 30s     # 新声音检查间隔，最低10秒
  profileInterval: 30m  # 个人资料和直播间号复查间隔，最低1分钟
  requestSpacing: 500ms # 猫耳FM请求严格串行，相邻请求最小间隔
  requestTimeout: 15s   # 单次请求超时

# 微博推送
weibo:
  onlyOnlineNotify: true  # 是否不推送 Bot 离线期间的动态和直播，默认为 false 表示需要推送，设置为 true 表示不推送
  mode: guest             # weibo 运行模式，可选 guest / login / api
                          # guest: 纯 HTTP 访客流程，动态解析 ver/request_id 并生成临时 Cookie
                          # login: 读取完整登录 Cookie Jar；失效或不存在时使用官方 App 扫码
                          # api: 从外部兼容 API 获取微博数据，不使用微博 Cookie
  cookieMode: qrcode      # 仅mode: login生效；qrcode（本地状态/管理员确认扫码）或cookiecloud
  cookieCloud:            # 仅mode: login且cookieMode: cookiecloud时生效
    endpoint:             # CookieCloud服务端地址，可与B站/抖音相同
    uuid:
    password:
    interval: 10m         # 启动立即获取，之后定时刷新；最小1m
    timeout: 15s
    cryptoType: auto      # auto（推荐）、legacy 或 aes-128-cbc-fixed
  interval: 60s           # 微博订阅轮询间隔；guest 模式最低 60s
  stateDir: ".weibo-state" # Cookie Jar 与访客 TID 保存目录；请勿上传或分享
  requestTimeout: 15s     # 单次 HTTP 请求超时
  protocolTimeout: 2m     # SSO/跨域同步等完整协议步骤总超时
  guestRefreshInterval: 1h # guest 临时 Cookie 定时刷新间隔，最低 10m
  sub:                    # 兼容旧配置；推荐扫码后使用 stateDir 中的完整 Cookie Jar
  qrlogin: true           # login Cookie失效时允许管理员回复“生成 微博”后生成二维码；不会主动生成
  qrloginTimeout: 3m      # 二维码轮询总时限
  qrAutoOpen: false       # 是否自动打开二维码图片；默认 false，避免弹出外部窗口
  autorefresh: true       # 是否定时调用微博心跳并捕获服务端真实 Set-Cookie 轮换
  loginRefreshInterval: 30m # 登录 Cookie 心跳间隔，最低 5m
  # userAgent:            # 可选；留空使用内置稳定 UA，Cookie Jar 会记录并复用

  # API 模式配置（mode: api 时使用）
  # API 模式直接从外部 API 获取微博数据，无需 Cookie
  # API 返回的数据格式需与微博移动端 API 兼容
  apiModeBaseURL: "http://127.0.0.1:5000"  # API 模式基础地址，完整 URL 为 {baseURL}/api/Weibo/GetMobileCards?uid=uid

  # Cookie 告警通知配置（接收群通过 /config weibo_alert 配置）
  disableCookieAlert: false  # 设为 true 可关闭 Cookie/SUB 失效告警通知

concern:
  emitInterval: 5s

template:      # 是否启用模板功能，true 为启用，false 为禁用，默认为禁用
  enable: false # 需要自定义直播/动态模板时再开启
  
autoreply: # 自定义命令自动回复，自定义命令通过模板发送消息，且不支持任何参数，需要同时启用模板功能
  group:   # 需要了解该功能请看模板文档
    command: []
  private:
    command: []
  
# 日志等级，可选值：trace / debug / info / warn / error
logLevel: info

# QQ 消息日志，记录收发的 QQ 消息，输出到 qq-logs 文件夹
# enable: true 启用，false 禁用（默认启用）
qq-logs:
  enable: true

# 不使用图片池和代理池时保持 off，避免全新配置产生无意义的未知类型错误。
imagePool:
  type: "off"
proxy:
  type: "off"

# 适配器选择：onebot-v11（默认）
adapter:
  mode: onebot-v11

# ws 模式支持 ws-server（正向）和 ws-reverse（反向）
# token 是服务端设置的 Access Token
# ws-server 默认监听全部请求，如需限制请修改为指定 ip:端口
# ws-reverse 需要配合反向 ws 服务器使用，默认为 LLOneBot 地址
websocket:
  mode: ws-server
  token:
  ws-server: 0.0.0.0:15630
  ws-reverse: ws://localhost:3001
admin:
  enable: false
  addr: "127.0.0.1:15631"
  token: ""

# 自定义数据库设置
# 启用后才会生成自定义数据库文件，并持久化保存
# 不启用时如果使用了相关函数，则会写入.lsp.db（慎重！）
extDb:
  enable: false
  path: ".ext.db"

# Telegram 推送设置
# 启用后，可在 Telegram 中进行所有操作（命令与 QQ 一致）
telegram:
  enable: false            # 是否启用 Telegram
  token: ""                # Telegram Bot Token
  proxy:
    url: ""               # 留空不使用代理；支持 http/https/socks5/socks5h
  endpoint: ""            # 可选：自定义 Telegram API Endpoint，留空使用默认

`
	// win 上用记事本打开不会正确换行
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}()
