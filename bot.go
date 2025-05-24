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
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/youtube"
	_ "github.com/cnxysoft/DDBOT-WSa/msg-marker"
)

// SetUpLog 使用默认的日志格式配置，会写入到logs文件夹内，日志会保留七天
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
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:    true,
		PadLevelText:     true,
		QuoteEmptyFields: true,
	})
	logrus.AddHook(lfshook.NewHook(writer, &logrus.TextFormatter{
		FullTimestamp:    true,
		PadLevelText:     true,
		QuoteEmptyFields: true,
		ForceQuote:       true,
	}))
}

// Run 启动bot，这个函数会阻塞直到收到退出信号
func Run() {
	if fi, err := os.Stat("device.json"); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("警告：没有检测到device.json，正在生成，如果是第一次运行，可忽略")
			bot.GenRandomDevice()
		} else {
			warn.Warn(fmt.Sprintf("检查device.json文件失败 - %v", err))
			os.Exit(1)
		}
	} else {
		if fi.IsDir() {
			warn.Warn("检测到device.json，但目标是一个文件夹！请手动确认并删除该文件夹！")
			os.Exit(1)
		} else {
			fmt.Println("检测到device.json，使用存在的device.json")
		}
	}

	if fi, err := os.Stat("application.yaml"); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("警告：没有检测到配置文件application.yaml，正在生成，如果是第一次运行，可忽略")
			if err := ioutil.WriteFile("application.yaml", []byte(exampleConfig), 0755); err != nil {
				warn.Warn(fmt.Sprintf("application.yaml生成失败 - %v", err))
				os.Exit(1)
			} else {
				fmt.Println("最小配置application.yaml已生成，请按需修改，如需高级配置请查看帮助文档")
			}
		} else {
			warn.Warn(fmt.Sprintf("检查application.yaml文件失败 - %v", err))
			os.Exit(1)
		}
	} else {
		if fi.IsDir() {
			warn.Warn("检测到application.yaml，但目标是一个文件夹！请手动确认并删除该文件夹！")
			os.Exit(1)
		} else {
			fmt.Println("检测到application.yaml，使用存在的application.yaml")
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

	// 快速初始化
	bot.Init()

	// 初始化 Modules
	bot.StartService()
	fmt.Println("运行完了bot.StartService()")
	// 登录 跳过登录
	//bot.Login()

	// 刷新好友列表，群列表
	//以后刷新
	// bot.RefreshList()

	lsp.Instance.PostStart(bot.Instance)
	fmt.Println("运行完了lsp.Instance.PostStart(bot.Instance)")

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
    rename: "【bot】"  # BOT进群后自动改名，默认改名为“【bot】”，如果留空则不自动改名

# 初次运行时将不使用b站帐号方便进行测试
# 如果不使用b站帐号，则推荐订阅数不要超过5个，否则推送延迟将上升
# b站相关的功能推荐配置一个b站账号，建议使用小号
# bot将使用您b站帐号的以下功能：
# 关注用户 / 取消关注用户 / 查看关注列表
# 请注意，订阅一个账号后，此处使用的b站账号将自动关注该账号
bilibili:
  SESSDATA: # 你的b站cookie
  bili_jct: # 你的b站cookie
  qrlogin: true # 是否启用二维码登录（Cookies失效时只需要清空SESSDATA和bili_jct重启即可再次登录）
  interval: 25s # 直播状态和动态检测间隔，过快可能导致ip被暂时封禁
  imageMergeMode: "auto" # 设置图片合并模式，支持 "auto" / "only9" / "off"
                         # auto 为默认策略，存在比较刷屏的图片时会合并
                         # only9 表示仅当恰好是9张图片的时候合并
                         # off 表示不合并
  hiddenSub: false    # 是否使用悄悄关注，默认不使用
  unsub: false        # 是否自动取消关注，默认不取消，如果您的b站账号有多个bot同时使用，取消可能导致推送丢失
  minFollowerCap: 0        # 设置订阅的b站用户需要满足至少有多少个粉丝，默认为0，设为-1表示无限制
  disableSub: false        # 禁止ddbot去b站关注帐号，这意味着只能订阅帐号已关注的用户，或者在b站手动关注
  onlyOnlineNotify: false  # 是否不推送Bot离线期间的动态和直播，默认为false表示需要推送，设置为true表示不推送

# 支持使用多个nitter镜像，默认使用官方镜像（第三方镜像可能有额外校验）
# 使用lightbrd镜像请自行先访问https://lightbrd.com/进行cookies的获取
# 填入你访问网站时提交的user_agent，可在浏览器中查看
# 填入你访问网站后得到的cf_clearance，可在浏览器中查看
twitter:
  baseurl:
    - "https://nitter.net/"
  interval: 300s # 查询间隔，过快可能导致ip被暂时封禁
  useragent:
  cfclearance:


concern:
  emitInterval: 5s

template:      # 是否启用模板功能，true为启用，false为禁用，默认为禁用
  enable: true # 需要了解模板请看模板文档
  
autoreply: # 自定义命令自动回复，自定义命令通过模板发送消息，且不支持任何参数，需要同时启用模板功能
  group:   # 需要了解该功能请看模板文档
    command: ["签到"]
  private:
    command: [ ]

# 重定义命令前缀，优先级高于bot.commandPrefix
# 如果有多个，可填写多项，prefix支持留空，可搭配自定义命令使用
# 例如下面的配置为：<Q命令1> <命令2> </help>
customCommandPrefix:
  签到: ""
  
# 日志等级，可选值：trace / debug / info / warn / error
logLevel: info

# ws模式支持ws-server（正向）和ws-reverse（反向）
# token 是服务端设置的 Access Token
# ws-server 默认监听全部请求，如需限制请修改为指定ip:端口
# ws-reverse 需要配合反向ws服务器使用，默认为LLOneBot地址
websocket:
  mode: ws-server
  token:
  ws-server: 0.0.0.0:15630
  ws-reverse: ws://localhost:3001

# 延迟加载好友、群组、群员信息
reloadDelay:
  enable: true # 是否启用数据延迟加载
  time: 3s # 延迟时间，默认为3秒
`
	// win上用记事本打开不会正确换行
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}()
