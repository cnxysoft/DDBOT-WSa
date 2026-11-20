package logging

import (
	"io"
	"path"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/cnxysoft/DDBOT-WSa/utils/qqlog"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
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

func (m *logging) MiraiGoModule() bot.ModuleInfo {
	return bot.ModuleInfo{
		ID:       moduleId,
		Instance: instance,
	}
}

func (m *logging) Init() {
	qqLogger := logrus.New()

	if !config.GlobalConfig.GetBool("qq-logs.enabled") && !config.GlobalConfig.GetBool("qq-logs.enable") {
		// 未启用时丢弃所有输出
		qqLogger.Out = io.Discard
		qqlog.Enabled = false
	} else {
		qqlog.Enabled = true
		// 创建文件 writer
		writer, err := rotatelogs.New(
			path.Join("qq-logs", "%Y-%m-%d.log"),
			rotatelogs.WithMaxAge(7*24*time.Hour),
			rotatelogs.WithRotationTime(24*time.Hour),
		)
		if err != nil {
			logrus.WithError(err).Error("无法写入日志")
			return
		}
		// 文件：沿用 logrus 默认的 TextFormatter，内容与改造前一致
		qqLogger.SetOutput(writer)
		// 控制台：人读格式，warn 及以上走 stderr
		// （原先用嗅探行首 level=" 的 levelWriter 做分流，但日志行永远以 time=" 开头，从未生效）
		InstallConsole(qqLogger)
	}
	qqLogger.SetLevel(logrus.DebugLevel)
	logger = qqLogger.WithField("module", moduleId)
	qqlog.Init(logger)
}

func (m *logging) PostInit() {
	// 第二次初始化
	// 再次过程中可以进行跨Module的动作
	// 如通用数据库等等
}

func (m *logging) Serve(b *bot.Bot) {
	// 注册服务函数部分
	registerLog(b)
}

func (m *logging) Start(b *bot.Bot) {
	// 此函数会新开携程进行调用
	// ```go
	// 		go exampleModule.Start()
	// ```

	// 可以利用此部分进行后台操作
	// 如http服务器等等
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
