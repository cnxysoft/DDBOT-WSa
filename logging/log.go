package logging

import (
	"io"
	"path"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/logging/logstyle"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/cnxysoft/DDBOT-WSa/utils/qqlog"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"

	"github.com/Sora233/MiraiGo-Template/bot"
)

const moduleId = "ddbot.logging"

func init() {
	instance = &logging{}
	bot.RegisterModule(instance)
}

type logging struct {
}

var instance *logging

var logger *logrus.Entry

// MiraiGoModule 返回模块信息，供 MiraiGo-Template 框架在 init 时注册。
// [推断] 本处注释与单行 return 形式按行号缺口推断复原，
// 成品二进制不保留注释文本，仅函数起始行有 pclntab 证据。
func (m *logging) MiraiGoModule() bot.ModuleInfo {
	return bot.ModuleInfo{ID: moduleId, Instance: instance}
}

func (m *logging) Init() {
	qqLogger := logrus.New()

	// qq-logs.enabled 为新配置项，qq-logs.enable 为旧配置项，二者任一开启即视为启用
	if !config.GlobalConfig.GetBool("qq-logs.enabled") && !config.GlobalConfig.GetBool("qq-logs.enable") {
		// 未启用时丢弃所有输出，并标记 qqlog 不可用
		qqLogger.Out = io.Discard
		qqlog.Enabled = false
	} else {
		qqlog.Enabled = true
		// 创建文件 writer：日志按天切割写入 qq-logs/，保留 7 天
		writer, err := rotatelogs.New(
			path.Join("qq-logs", "%Y-%m-%d.log"),
			rotatelogs.WithMaxAge(7*24*time.Hour),
			rotatelogs.WithRotationTime(24*time.Hour),
		)
		if err != nil {
			logrus.WithError(err).Error("unable to write logs")
			return
		}
		// 文件输出：rotatelogs 直接作为 Out，保证每条日志都落盘
		qqLogger.SetOutput(writer)
		// 文件格式器：无颜色、强制引号、完整时间戳
		qqLogger.SetFormatter(logstyle.NewFileFormatter())
		// 控制台输出：通过 lfshook 按 level 分流——info 及以下写 stdout，
		// warn 及以上写 stderr；控制台与文件各走一路，不会重复写文件。
		// 写出前由 consoleFormatter 把 level 字段替换为带 ANSI 颜色的版本
		qqLogger.AddHook(lfshook.NewHook(lfshook.WriterMap{
			logrus.TraceLevel: logstyle.NewConsoleStdout(),
			logrus.DebugLevel: logstyle.NewConsoleStdout(),
			logrus.InfoLevel:  logstyle.NewConsoleStdout(),
			logrus.WarnLevel:  logstyle.NewConsoleStderr(),
			logrus.ErrorLevel: logstyle.NewConsoleStderr(),
			logrus.FatalLevel: logstyle.NewConsoleStderr(),
			logrus.PanicLevel: logstyle.NewConsoleStderr(),
		}, logstyle.NewConsoleFormatter()))
	}

	// 日志级别固定为 Debug，不在此处过滤；
	// 由各输出端（文件/控制台）自行决定显示与过滤策略
	qqLogger.SetLevel(logrus.DebugLevel)
	logger = qqLogger.WithField("module", moduleId)
	qqlog.Init(logger)
}

func (m *logging) PostInit() {
}
func (m *logging) Serve(b *bot.Bot) {
	// 注册服务函数部分
	registerLog(b)
}

// Start 会新开协程进行调用：
//
//	go exampleModule.Start()
//
// 可以利用此部分进行后台操作，例如启动 http 服务器等常驻任务；
// 当前模块没有后台任务，实现为空。
//
// [推断] 本注释按基底文件复原，位置有行号证据、文本为推断。
func (m *logging) Start(b *bot.Bot) {
}
func (m *logging) Stop(b *bot.Bot, wg *sync.WaitGroup) {
	// 别忘了解锁
	defer wg.Done()
	// 结束部分
	// 一般调用此函数时，程序接收到 os.Interrupt 信号
	// 即将退出
	// 在此处应该释放相应的资源或者对状态进行保存
}

func logGroupMessage(msg *adapter.GroupMessage) {
	name := msg.Sender.Card
	if name == "" {
		name = msg.Sender.Nickname
	}
	logger.Infof("收到群 %s(%d) 内 %s(%d) 的消息: %s (%d)", msg.GroupName, msg.GroupCode, name, msg.Sender.UserID, msgstringer.AdapterMsgToString(msg.Elements), msg.ID)
}

func logPrivateMessage(msg *adapter.PrivateMessage) {
	logger.Infof("收到 %s(%d) 的私聊消息: %s (%d)", msg.Sender.Nickname, msg.Sender.UserID, msgstringer.AdapterMsgToString(msg.Elements), msg.ID)
}

func logFriendMessageRecallEvent(event *adapter.FriendMessageRecalledEvent) {
	logger.WithFields(logrus.Fields{
		"From":      "FriendsMessageRecall",
		"MessageID": event.MessageId,
		"SenderID":  event.FriendUin,
	}).Info("好友消息撤回")
}

func logGroupMessageRecallEvent(event *adapter.GroupMessageRecalledEvent) {
	logger.WithFields(localutils.GroupLogFields(event.GroupCode)).
		WithFields(logrus.Fields{
			"From":       "GroupMessageRecall",
			"MessageID":  event.MessageId,
			"SenderID":   event.AuthorUin,
			"OperatorID": event.OperatorUin,
		}).Info("群消息撤回")
}

func logGroupMuteEvent(event *adapter.GroupMuteEvent) {
	muteLogger := logger.WithFields(localutils.GroupLogFields(event.GroupCode)).
		WithFields(logrus.Fields{
			"From":        "GroupMute",
			"TargetUin":   event.TargetUin,
			"OperatorUin": event.OperatorUin,
		})
	if event.TargetUin == 0 {
		if event.Time != 0 {
			muteLogger.Debug("开启了全体禁言")
		} else {
			muteLogger.Debug("关闭了全体禁言")
		}
	} else {
		gi := bot.Instance.FindGroup(event.GroupCode)
		var mi *adapter.GroupMemberInfo
		if gi != nil {
			mi = gi.FindMember(event.TargetUin)
			if mi != nil {
				muteLogger = muteLogger.WithField("TargetName", mi.DisplayName())
			}
			mi = gi.FindMember(event.OperatorUin)
			if mi != nil {
				muteLogger = muteLogger.WithField("OperatorName", mi.DisplayName())
			}
		}
		if event.Time > 0 {
			muteLogger.Debug("用户被禁言")
		} else {
			muteLogger.Debug("用户被取消禁言")
		}
	}
}

func logDisconnect(event *adapter.ClientDisconnectedEvent) {
	logger.WithFields(logrus.Fields{
		"From":   "Disconnected",
		"Reason": event.Message,
	}).Warn("bot断开链接")
}

func registerLog(b *bot.Bot) {
	b.GroupMessageRecalledEvent.Subscribe(func(event *adapter.GroupMessageRecalledEvent) {
		logGroupMessageRecallEvent(event)
	})

	b.GroupMessageEvent.Subscribe(func(msg *adapter.GroupMessage) {
		logGroupMessage(msg)
	})

	b.GroupMuteEvent.Subscribe(func(event *adapter.GroupMuteEvent) {
		logGroupMuteEvent(event)
	})

	b.PrivateMessageEvent.Subscribe(func(privateMessage *adapter.PrivateMessage) {
		logPrivateMessage(privateMessage)
	})

	b.FriendMessageRecalledEvent.Subscribe(func(event *adapter.FriendMessageRecalledEvent) {
		logFriendMessageRecallEvent(event)
	})

	b.DisconnectedEvent.Subscribe(func(event *adapter.ClientDisconnectedEvent) {
		logDisconnect(event)
	})

	// Note: SelfGroupMessageEvent and SelfPrivateMessageEvent are not logged separately
	// to avoid duplicate logs when bot sends messages
}
