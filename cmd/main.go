package main

import (
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/alecthomas/kong"
	"github.com/cnxysoft/DDBOT-WSa"
	"github.com/cnxysoft/DDBOT-WSa/logging"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/acfun"
	"github.com/cnxysoft/DDBOT-WSa/lsp/bilibili"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/douyin"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/douyu"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/huya"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/twitch"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/twitter"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
	_ "github.com/cnxysoft/DDBOT-WSa/lsp/youtube"
	_ "github.com/cnxysoft/DDBOT-WSa/msg-marker"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/cnxysoft/DDBOT-WSa/warn"
	"github.com/sirupsen/logrus"
	"net/http"
	_ "net/http/pprof"
	"os"
)

func main() {
	var cli struct {
		Play         bool  `optional:"" help:"运行play函数，适用于测试和开发"`
		Debug        bool  `optional:"" help:"启动debug模式"`
		Online       bool  `optional:"" help:"跳过等待bot上线，直接启动订阅系统（调试用）"`
		SetAdmin     int64 `optional:"" xor:"c" help:"设置admin权限"`
		Version      bool  `optional:"" xor:"c" short:"v" help:"打印版本信息"`
		SyncBilibili bool  `optional:"" xor:"c" help:"同步b站帐号的关注，适用于更换或迁移b站帐号的时候"`
	}
	kong.Parse(&cli)

	banner := logging.BannerInfo{Version: lsp.Tags, Commit: lsp.CommitId, BuildTime: lsp.BuildTime}
	if cli.Version {
		logging.PrintVersion(banner)
		os.Exit(0)
	}

	if err := localdb.InitBuntDB(""); err != nil {
		if err == localdb.ErrLockNotHold {
			warn.Warn("tryLock数据库失败：您可能重复启动了这个BOT！\n如果您确认没有重复启动，请删除.lsp.db.lock文件并重新运行。")
		} else {
			warn.Warn("无法正常初始化数据库！请检查.lsp.db文件权限是否正确，如无问题则为数据库文件损坏，请阅读文档获得帮助。")
		}
		return
	}

	// 添加数据库关闭钩子
	utils.AddExitHook(func() {
		localdb.Close()
	})

	// 设置退出钩子处理器
	utils.SetupExitHook()

	if cli.SetAdmin != 0 {
		sm := permission.NewStateManager()
		if err := sm.GrantRole(cli.SetAdmin, permission.Admin); err != nil {
			logrus.WithError(err).Error("设置Admin权限失败")
		}
		return
	}

	if cli.SyncBilibili {
		config.Init()
		c := bilibili.NewConcern(nil)
		c.StateManager.FreshIndex()
		bilibili.Init()
		c.SyncSub()
		return
	}

	if cli.Debug {
		lsp.Debug = true
		go http.ListenAndServe("localhost:6060", nil)
	}

	if cli.Online {
		lsp.SkipOnlineCheck = true
	}

	if cli.Play {
		play()
		return
	}

	// 日志在这里才就位：--version / --set-admin / --sync-bilibili / --play 这些
	// 早退路径不该凭空建出 logs 目录；重复启动时也不会和另一个实例同写一份日志文件。
	DDBOT.SetUpLog()
	logging.PrintBanner()
	logStartupInfo(banner)
	if cli.Debug {
		logrus.WithField("module", "startup").Info("已开启 debug 模式，pprof 监听 localhost:6060")
	}

	DDBOT.Run()
}

// logStartupInfo 把启动时要说的话全部打成常规日志行，不再裸打印。
func logStartupInfo(info logging.BannerInfo) {
	log := logrus.WithField("module", "startup")
	log.Info("DDBOT-WSa " + logging.VersionLine(info))
	log.Info("基于 OneBot 协议的消息推送框架")
	log.Info("文档站 https://kizunerwe.github.io/DDBOT-WSa-docs/")
	log.Info("交流群 一群 755612788（已满）· 二群 980848391")
	log.Info("二次修改 https://github.com/Hoshinonyaruko/DDBOT-ws")
	log.Info("三次修改 https://github.com/cnxysoft/DDBOT-WSa")
	log.Info("支持的连接方式 onebot-v11（默认）/ qqofficial / satori")
}
