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
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/acfun"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/douyu"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/huya"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/twitcasting"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/twitch"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/youtube"
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
	if fi, err := os.Stat("device.json"); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("警告：没有检测到 device.json，正在生成，如果是第一次运行，可忽略")
			bot.GenRandomDevice()
		} else {
			warn.Warn(fmt.Sprintf("检查 device.json 文件失败 - %v", err))
			os.Exit(1)
		}
	} else {
		if fi.IsDir() {
			warn.Warn("检测到 device.json，但目标是一个文件夹！请手动确认并删除该文件夹！")
			os.Exit(1)
		} else {
			fmt.Println("检测到 device.json，使用存在的 device.json")
		}
	}

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
	// 快速初始化
	bot.Init()

	// 初始化 Modules
	bot.StartService()

	_, _ = admin.Start(&bot.Instance.Online, nil)

	// 登录 跳过登录
	//bot.Login()

	// 刷新好友列表，群列表
	//以后刷新
	// bot.RefreshList()

	lsp.Instance.PostStart(bot.Instance)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	bot.Stop()
}

var exampleConfig = func() string {
	s := `
### 注意，填写时请把井号及后面的内容删除，并且冒号后需要加一个空格
bot:
  onJoinGroup: 
    rename: "【bot】"   # BOT 进群后自动改名，默认改名为"【bot】"，如果留空则不自动改名
  sendFailureReminder: # 失败提醒：发送失败达到一定次数后触发 notify.bot.send_failed.tmpl 模板
    enable: false      # 是否启用失败提醒
    times: 3           # 失败次数阈值
  offlineQueue:   # 离线缓存：BOT 离线时暂存要发送的消息，上线后重新发送（期间不能重启 DDBOT）
    enable: false # 是否启用离线缓存
    expire: 30m   # 离线消息有效期

# 初次运行时将不使用 b 站帐号方便进行测试
# 如果不使用 b 站帐号，则推荐订阅数不要超过 5 个，否则推送延迟将上升
# b 站相关的功能推荐配置一个 b 站账号，建议使用小号
# bot 将使用您 b 站帐号的以下功能：
# 关注用户 / 取消关注用户 / 查看关注列表
# 请注意，订阅一个账号后，此处使用的 b 站账号将自动关注该账号
bilibili:
  SESSDATA: # 你的 b 站 cookie
  bili_jct: # 你的 b 站 cookie
  qrlogin: true # 是否启用二维码登录（Cookies 失效时只需要清空 SESSDATA 和 bili_jct 重启即可再次登录）
  interval: 25s # 直播状态和动态检测间隔，过快可能导致 ip 被暂时封禁
  imageMergeMode: "auto" # 设置图片合并模式，支持 "auto" / "only9" / "off"
                          # auto 为默认策略，存在比较刷屏的图片时会合并
                          # only9 表示仅当恰好是 9 张图片的时候合并
                          # off 表示不合并
  hiddenSub: false    # 是否使用悄悄关注，默认不使用
  unsub: false        # 是否自动取消关注，默认不取消，如果您的 b 站账号有多个 bot 同时使用，取消可能导致推送丢失
  minFollowerCap: 0        # 设置订阅的 b 站用户需要满足至少有多少个粉丝，默认为 0，设为 -1 表示无限制
  disableSub: false        # 禁止 ddbot 去 b 站关注帐号，这意味着只能订阅帐号已关注的用户，或者在 b 站手动关注
  onlyOnlineNotify: false  # 是否不推送 Bot 离线期间的动态和直播，默认为 false 表示需要推送，设置为 true 表示不推送
  autoParsePosts: false    # 自动解析专栏，将发送专栏动态改为发送专栏内容
  secAnalysis: false        # 是否开启动态二次解析，默认关闭

# A 站相关的功能推荐配置一个 b 站账号，建议使用小号
# bot 将使用您 A 站帐号的以下功能（订阅动态时）：
# 关注用户 / 取消关注用户 / 查看关注列表
# 请注意，订阅一个账号后，此处使用的 A 站账号将自动关注该账号
# authKey 和 acPassToken 用于 ACFUN API 认证，与 account/password 不同
# 通常只需要 account + password 即可，authKey/acPassToken 为可选的高级配置
acfun:
  account:
  password:
  authKey:          # ACFUN authKey（可选）
  acPassToken:      # ACFUN acPassToken（可选）
  unsub: false
  interval: 25s
  onlyOnlineNotify: false

# Twitter 推送支持两种模式：
# 1. mirror 模式（默认）：使用 nitter 镜像获取推文，无需账号
# 2. api 模式：使用 Twitter API，需要配置 cookie 和 Bearer Token
# 注意：api 模式需要真实 Twitter 账号 cookie，第三方镜像可能有额外校验

# mirror 模式配置
# 支持使用多个 nitter 镜像，默认使用官方镜像（第三方镜像可能有额外校验）
# 使用 lightbrd 镜像请自行先访问 https://lightbrd.com/进行 cookies 的获取
# 填入你访问网站时提交的 user_agent，可在浏览器中查看
# 填入你访问网站后得到的 cf_clearance，可在浏览器中查看
twitter:
  mode: mirror  # 模式选择：mirror（默认）或 api
  baseUrl:     # mirror 模式下的 nitter 镜像列表
    - "https://nitter.net/"
    - "https://nitter.privacyredirect.com/"
    - "https://nitter.tiekoetter.com/"
    - "https://nitter.poast.org/"
    - "https://nitter.catsarch.com/"
  interval: 30s  # 查询间隔，过快可能导致 ip 被暂时封禁
  userAgent:      # 浏览器 User-Agent
  unsub: false    # 是否自动取消关注（当取消订阅时）

  # api 模式配置（mode: api 时生效）
  # 以下字段用于 Twitter API 认证，需要真实账号的 cookies
  # auth_token 和 ct0：登录 Twitter 后在 cookie 中获取
  # bearerToken：Twitter API Bearer Token（可从 dev.twitter.com 获取或通过 main.js 自动获取）
  # queryId：搜索 API 的 queryId（可通过 main.js 自动获取或使用默认配置）
  # screenName：Twitter 账号的用户名（可选，自动获取时会填充）
  # 注意：api 模式需要高信誉账号，否则可能被风控
  auth_token:   # Twitter auth_token cookie
  ct0:          # Twitter ct0 cookie
  bearerToken:  # Twitter Bearer Token
  queryId:      # Twitter 搜索 API queryId
  screenName:   # Twitter 账号 screen_name（可选） 

# 抖音直播推送（测试）
# 需要手动访问 www.douyin.com 并填入__ac_signature 和__ac_nonce、sessionId 共三个 cookies 和你的浏览器 UA
douyin:
  acSignature: 
  acNonce: 
  sessionId: 
  userAgent: 
  interval: 30s
  onlyOnlineNotify: false

# weibo 推送暂时需要设置 Cookie 才会启动。
weibo:
  onlyOnlineNotify: true  # 是否不推送 Bot 离线期间的动态和直播，默认为 false 表示需要推送，设置为 true 表示不推送
  mode: guest             # weibo 运行模式，可选 guest / login / api
                          # guest: 访客模式，自动生成临时 Cookie
                          # login: 登录模式，需要配置 sub 或启用 qrlogin 扫码登录
                          # api: API 模式，从外部 API 自动获取 Cookie（推荐）
  interval: 30s           # weibo 访客模式下 Cookie 刷新间隔
  sub: # 登录 weibo.com 后取得对应名称的 Cookie 填入此处（mode: login 时需要）。
  qrlogin: true           # 是否启用二维码登录（Cookies 失效时重启后可再次登录，仅 mode: login 时有效）
  autorefresh: false      # 是否启用 SUB 自动刷新（仅 mode: login 时有效）
                          # 启用后：1) 启动时若 sub 为空则从 API 获取（仅内存使用）
                          #      2) 每小时检查 SUB 是否变化，不同则自动替换（仅内存）
                          #      3) 需要配合 cookieRefreshAPI 使用
                          # 注意：不会修改配置文件中的 weibo.sub
  
  # API 模式配置（当 mode: api 时使用）
  # 从外部 API 自动获取 Cookie，无需手动配置 sub 或扫码登录
  cookieRefreshAPI: "http://127.0.0.1:5000/api/Weibo/GetWeiboCookie"  # Cookie 刷新 API 地址

youtube:
  onlyOnlineNotify: true  # 是否不推送 Bot 离线期间的动态和直播，默认为 false 表示需要推送，设置为 true 表示不推送

# Twitch 直播推送
# 需要在 https://dev.twitch.tv/console/apps 注册应用获取 clientId 和 clientSecret
twitch:
  clientId:               # Twitch 应用的 Client ID
  clientSecret:           # Twitch 应用的 Client Secret
  interval: 30s          # 轮询间隔，建议不要太短避免风控
  onlyOnlineNotify: false # 是否不推送Bot离线期间的直播，默认为false表示需要推送

# TwitCasting 直播推送
# 需要在 TwitCasting 开发者后台注册应用获取 clientId 和 clientSecret
twitcasting:
  clientId:               # TwitCasting 应用的 Client ID
  clientSecret:           # TwitCasting 应用的 Client Secret
  nameStrategy: name      # 发送消息时显示的名称策略：name（默认）/ userid / both
  broadcaster:            # 直播通知中是否显示主播信息
    title: false          # 是否显示直播间标题
    created: false       # 是否显示开播时间
    image: false         # 是否显示直播封面

concern:
  emitInterval: 5s

template:      # 是否启用模板功能，true 为启用，false 为禁用，默认为禁用
  enable: true # 需要了解模板请看模板文档
  
autoreply: # 自定义命令自动回复，自定义命令通过模板发送消息，且不支持任何参数，需要同时启用模板功能
  group:   # 需要了解该功能请看模板文档
    command: ["签到"]
  private:
    command: [ ]

# 重定义命令前缀，优先级高于 bot.commandPrefix
# 如果有多个，可填写多项，prefix 支持留空，可搭配自定义命令使用
# 例如下面的配置为：<Q 命令 1> <命令 2> </help>
customCommandPrefix:
  签到：""
  
# 日志等级，可选值：trace / debug / info / warn / error
logLevel: info

# QQ 消息日志，记录收发的 QQ 消息，输出到 qq-logs 文件夹
# enable: true 启用，false 禁用（默认启用）
qq-logs:
  enable: true

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
    enable: false         # 是否启用代理（http/https/socks5/socks5h）
    url: ""               # 代理地址，例如 http://127.0.0.1:7890 或 socks5h://127.0.0.1:1080
  endpoint: ""            # 可选：自定义 Telegram API Endpoint，留空使用默认

`
	// win 上用记事本打开不会正确换行
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}()
