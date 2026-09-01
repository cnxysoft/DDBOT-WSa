package lsp

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/sliceutil"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/image_pool"
	"github.com/cnxysoft/DDBOT-WSa/image_pool/local_pool"
	"github.com/cnxysoft/DDBOT-WSa/image_pool/lolicon_pool"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/cnxysoft/DDBOT-WSa/lsp/version"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/py"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/system_proxy"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/fsnotify/fsnotify"
	jsoniter "github.com/json-iterator/go"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/buntdb"
	"go.uber.org/atomic"
	"golang.org/x/sync/semaphore"
)

const ModuleName = "me.sora233.Lsp"

const (
	TargetTypeGroup = iota
	TargetTypeFriend
)

var logger = logrus.WithField("module", ModuleName)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

var Debug = false

// SkipOnlineCheck 为 true 时跳过等待 bot 上线，直接启动订阅系统
// 通过启动参数 --online 设置，用于调试或无 WS 连接的场景
var SkipOnlineCheck = false

type Lsp struct {
	pool          image_pool.Pool
	concernNotify <-chan concern.Notify
	stop          chan interface{}
	wg            sync.WaitGroup
	status        *Status
	notifyWg      sync.WaitGroup
	msgLimit      *semaphore.Weighted
	cron          *cron.Cron

	PermissionStateManager *permission.StateManager
	LspStateManager        *StateManager
	started                atomic.Bool
}

func (l *Lsp) CommandShowName(command string) string {
	return cfg.GetCommandPrefix(command) + command
}

func (l *Lsp) MiraiGoModule() bot.ModuleInfo {
	return bot.ModuleInfo{
		ID:       ModuleName,
		Instance: Instance,
	}
}

func (l *Lsp) Init() {
	log := logger.WithField("log_level", config.GlobalConfig.GetString("logLevel"))
	lev, err := logrus.ParseLevel(config.GlobalConfig.GetString("logLevel"))
	if err != nil {
		logrus.SetLevel(logrus.DebugLevel)
		log.Warn("无法识别logLevel，将使用Debug级别")
	} else {
		logrus.SetLevel(lev)
		log.Infof("设置logLevel为%v", lev.String())
	}

	l.msgLimit = semaphore.NewWeighted(int64(cfg.GetNotifyParallel()))

	if Tags != "UNKNOWN" {
		logger.Infof("DDBOT版本：Release版本【%v】", Tags)
	} else {
		if CommitId == "UNKNOWN" {
			logger.Infof("DDBOT版本：编译版本未知")
		} else {
			logger.Infof("DDBOT版本：编译版本【%v-%v】", BuildTime, CommitId)
		}
	}

	db := localdb.MustGetClient()
	var count int
	err = db.View(func(tx *buntdb.Tx) error {
		return tx.Ascend("", func(key, value string) bool {
			count++
			return true
		})
	})
	if err == nil && count == 0 {
		if _, err := version.SetVersion(LspVersionName, LspSupportVersion); err != nil {
			log.Fatalf("警告：初始化LspVersion失败！")
		}
	} else {
		curVersion := version.GetCurrentVersion(LspVersionName)
		if curVersion < 0 {
			log.Errorf("警告：无法检查数据库兼容性，程序可能无法正常工作")
		} else if curVersion > LspSupportVersion {
			log.Fatalf("警告：检查数据库兼容性失败！最高支持版本：%v，当前版本：%v", LspSupportVersion, curVersion)
		} else if curVersion < LspSupportVersion {
			// 应该更新下
			backupFileName := fmt.Sprintf("%v-%v", localdb.LSPDB, time.Now().Unix())
			log.Warnf(
				`警告：数据库兼容性检查完毕，当前需要从<%v>更新至<%v>，将备份当前数据库文件到"%v"`,
				curVersion, LspSupportVersion, backupFileName)
			f, err := os.Create(backupFileName)
			if err != nil {
				log.Fatalf(`无法创建备份文件<%v>：%v`, backupFileName, err)
			}
			err = db.Save(f)
			if err != nil {
				log.Fatalf(`无法备份数据库到<%v>：%v`, backupFileName, err)
			}
			log.Infof(`备份完成，已备份数据库到<%v>"`, backupFileName)
			log.Info("五秒后将开始更新数据库，如需取消请按Ctrl+C")
			time.Sleep(time.Second * 5)
			err = version.DoMigration(LspVersionName, lspMigrationMap)
			if err != nil {
				log.Fatalf("更新数据库失败：%v", err)
			}
		} else {
			log.Debugf("数据库兼容性检查完毕，当前已为最新模式：%v", curVersion)
		}
	}

	imagePoolType := config.GlobalConfig.GetString("imagePool.type")
	log = logger.WithField("image_pool_type", imagePoolType)

	switch imagePoolType {
	case "loliconPool":
		pool, err := lolicon_pool.NewLoliconPool(&lolicon_pool.Config{
			ApiKey:   config.GlobalConfig.GetString("loliconPool.apikey"),
			CacheMin: config.GlobalConfig.GetInt("loliconPool.cacheMin"),
			CacheMax: config.GlobalConfig.GetInt("loliconPool.cacheMax"),
		})
		if err != nil {
			log.Errorf("can not init pool %v", err)
		} else {
			l.pool = pool
			log.Infof("初始化%v图片池", imagePoolType)
			l.status.ImagePoolEnable = true
		}
	case "localPool":
		pool, err := local_pool.NewLocalPool(config.GlobalConfig.GetString("localPool.imageDir"))
		if err != nil {
			log.Errorf("初始化%v图片池失败 %v", imagePoolType, err)
		} else {
			l.pool = pool
			log.Infof("初始化%v图片池", imagePoolType)
			l.status.ImagePoolEnable = true
		}
	case "off":
		log.Debug("关闭图片池")
	default:
		log.Errorf("未知的图片池")
	}

	proxyType := config.GlobalConfig.GetString("proxy.type")
	// 未配置 proxy.type 时默认使用 systemProxy：自动检测系统代理（环境变量/GNOME/Windows 注册表）
	if proxyType == "" {
		proxyType = "systemProxy"
		log.Info("未配置 proxy.type，默认使用 systemProxy（自动检测系统代理）")
	}
	log = logger.WithField("proxy_type", proxyType)
	switch proxyType {
	case "pyProxyPool":
		host := config.GlobalConfig.GetString("pyProxyPool.host")
		log := log.WithField("host", host)
		pyPool, err := py.NewPYProxyPool(host)
		if err != nil {
			log.Errorf("init py pool err %v", err)
		} else {
			proxy_pool.Init(pyPool)
			l.status.ProxyPoolEnable = true
		}
	case "localProxyPool":
		overseaProxies := config.GlobalConfig.GetStringSlice("localProxyPool.oversea")
		mainlandProxies := config.GlobalConfig.GetStringSlice("localProxyPool.mainland")
		var proxies []*local_proxy_pool.Proxy
		for _, proxy := range overseaProxies {
			proxies = append(proxies, &local_proxy_pool.Proxy{
				Type:  proxy_pool.PreferOversea,
				Proxy: proxy,
			})
		}
		for _, proxy := range mainlandProxies {
			proxies = append(proxies, &local_proxy_pool.Proxy{
				Type:  proxy_pool.PreferMainland,
				Proxy: proxy,
			})
		}
		pool := local_proxy_pool.NewLocalPool(proxies)
		proxy_pool.Init(pool)
		log.WithField("local_proxy_num", len(proxies)).Debug("debug")
		l.status.ProxyPoolEnable = true
	case "systemProxy":
		// 自动检测系统代理，仅用于海外请求（X、YouTube 等）
		sysProxy, enabled := system_proxy.DetectSystemProxy()
		if enabled && sysProxy != "" {
			log.Infof("检测到系统代理: %s（仅用于海外请求）", sysProxy)
			proxies := []*local_proxy_pool.Proxy{
				{
					Type:  proxy_pool.PreferOversea,
					Proxy: sysProxy,
				},
			}
			pool := local_proxy_pool.NewLocalPool(proxies)
			proxy_pool.Init(pool)
			l.status.ProxyPoolEnable = true
		} else {
			log.Warn("未检测到系统代理或系统代理未启用。" +
				"检测范围：环境变量(http_proxy/https_proxy/all_proxy)、Linux GNOME 系统代理(gsettings)、Windows 注册表。" +
				"注意：代理客户端仅监听端口不代表系统代理已开启；" +
				"终端直接运行可先 export https_proxy=http://127.0.0.1:端口 再启动，" +
				"或在配置中改用静态代理（proxy.oversea 等条目）")
		}
	case "off":
		log.Debug("proxy pool turn off")
	default:
		log.Errorf("unknown proxy type")
	}
	if cfg.GetTemplateEnabled() {
		log.Infof("已启用模板")
		template.InitTemplateLoader()
	}
	cfg.ReloadCustomCommandPrefix()
	config.GlobalConfig.OnConfigChange(func(in fsnotify.Event) {
		// 如果正在写入配置，跳过这次热重载
		if cfg.IsWritingInProgress() {
			logrus.WithField("config", "GlobalConfig").Debug("配置文件变更被忽略（正在写入中）")
			return
		}
		go cfg.ReloadCustomCommandPrefix()
		l.CronjobReload()
	})
}

func (l *Lsp) PostInit() {
}

func (l *Lsp) DebugCheck(groupCode int64, uin int64, isGroupMessage bool) bool {
	var ok bool
	if Debug {
		if isGroupMessage {
			if sliceutil.Contains(config.GlobalConfig.GetStringSlice("debug.group"), strconv.FormatInt(groupCode, 10)) {
				ok = true
			}
		}
		if sliceutil.Contains(config.GlobalConfig.GetStringSlice("debug.uin"), strconv.FormatInt(uin, 10)) {
			ok = true
		}
	} else {
		ok = true
	}
	return ok
}

func (l *Lsp) Serve(bot *bot.Bot) {
	logger.Debugf("Lsp.Serve called: bot=%p, GroupMessageEvent=%p", bot, bot.GroupMessageEvent)
	bot.GroupMemberJoinEvent.Subscribe(func(event *adapter.MemberJoinGroupEvent) {
		if err := localdb.Set(localdb.Key("OnGroupMemberJoined", event.Group.Code, event.Member.Uin, event.Member.JoinTime), "",
			localdb.SetExpireOpt(time.Minute*2), localdb.SetNoOverWriteOpt()); err != nil {
			return
		}
		groupName := event.Group.Name
		memberName := event.Member.DisplayName()
		if gi := localutils.GetBot().FindGroup(event.Group.Code); gi != nil {
			groupName = gi.Name
			if fi := gi.FindMember(event.Member.Uin); fi != nil {
				memberName = fi.DisplayName()
			}
		}
		m, _ := template.LoadAndExec("trigger.group.member_in.tmpl", map[string]interface{}{
			"group_code":  event.Group.Code,
			"group_name":  groupName,
			"member_code": event.Member.Uin,
			"member_name": memberName,
		})
		if m != nil && l.DebugCheck(event.Group.Code, event.Member.Uin, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.Group.Code))
		}
	})
	bot.GroupMemberLeaveEvent.Subscribe(func(event *adapter.MemberLeaveGroupEvent) {
		if err := localdb.Set(localdb.Key("OnGroupMemberLeaved", event.Group.Code, event.Member.Uin, event.Member.JoinTime), "",
			localdb.SetExpireOpt(time.Minute*2), localdb.SetNoOverWriteOpt()); err != nil {
			return
		}
		groupName := event.Group.Name
		memberName := event.Member.DisplayName()
		if gi := localutils.GetBot().FindGroup(event.Group.Code); gi != nil {
			groupName = gi.Name
			if fi := gi.FindMember(event.Member.Uin); fi != nil {
				memberName = fi.DisplayName()
			}
		}
		m, _ := template.LoadAndExec("trigger.group.member_out.tmpl", map[string]interface{}{
			"group_code":  event.Group.Code,
			"group_name":  groupName,
			"member_code": event.Member.Uin,
			"member_name": memberName,
		})
		if m != nil && l.DebugCheck(event.Group.Code, event.Member.Uin, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.Group.Code))
		}
	})

	bot.GroupMessageRecalledEvent.Subscribe(func(event *adapter.GroupMessageRecalledEvent) {
		data := map[string]interface{}{
			"group_code":   event.GroupCode,
			"author_uin":   event.AuthorUin,
			"operator_uin": event.OperatorUin,
			"message_id":   event.MessageId,
		}
		if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
			data["group_name"] = gi.Name
			if fi := gi.FindMember(event.AuthorUin); fi != nil {
				data["author_name"] = fi.DisplayName()
			}
			if fi := gi.FindMember(event.OperatorUin); fi != nil {
				data["operator_name"] = fi.DisplayName()
			}
		}
		m, _ := template.LoadAndExec("trigger.group.recall.tmpl", data)
		if m != nil && l.DebugCheck(event.GroupCode, event.AuthorUin, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.GroupCode))
		}
	})

	bot.GroupInvitedEvent.Subscribe(func(request *adapter.GroupInvitedRequest) {
		log := logger.WithFields(logrus.Fields{
			"GroupCode":   request.GroupCode,
			"GroupName":   request.GroupName,
			"InvitorUin":  request.InvitorUin,
			"InvitorNick": request.InvitorNick,
		})

		if l.PermissionStateManager.CheckBlockList(request.InvitorUin) {
			log.Debug("收到加群邀请，该用户在block列表中，将拒绝加群邀请")
			l.PermissionStateManager.AddBlockList(request.GroupCode, 0)
			localutils.GetBot().SolveGroupInvitedRequest(request.Flag, false, "")
			return
		}

		requests, err := l.LspStateManager.ListGroupInvitedRequest()
		if err != nil {
			log.Errorf("ListGroupInvitedRequest error - %v", err)
			return
		}
		for _, r := range requests {
			if r.GroupCode == request.GroupCode {
				l.LspStateManager.DeleteGroupInvitedRequest(request.RequestId)
				log.Info("收到加群邀请，该群聊已在申请列表中，将忽略该申请")
				return
			}
		}

		fi := bot.FindFriend(request.InvitorUin)
		if fi == nil {
			log.Error("收到加群邀请，无法找到好友信息，将拒绝加群邀请")
			l.PermissionStateManager.AddBlockList(request.GroupCode, 0)
			localutils.GetBot().SolveGroupInvitedRequest(request.Flag, false, "未找到阁下的好友信息，请添加好友进行操作")
			return
		}

		if l.PermissionStateManager.CheckAdmin(request.InvitorUin) {
			log.Info("收到管理员的加群邀请，将同意加群邀请")
			l.PermissionStateManager.DeleteBlockList(request.GroupCode)
			localutils.GetBot().SolveGroupInvitedRequest(request.Flag, true, "")
			return
		}

		switch l.LspStateManager.GetCurrentMode() {
		case PrivateMode:
			log.Info("收到加群邀请，当前BOT处于私有模式，将拒绝加群邀请")
			l.PermissionStateManager.AddBlockList(request.GroupCode, 0)
			localutils.GetBot().SolveGroupInvitedRequest(request.Flag, false, "当前BOT处于私有模式")
		case ProtectMode:
			if err := l.LspStateManager.SaveGroupInvitedRequest(request); err != nil {
				log.Errorf("收到加群邀请，但记录申请失败，将拒绝该申请，请将该问题反馈给开发者 - error %v", err)
				localutils.GetBot().SolveGroupInvitedRequest(request.Flag, false, "内部错误")
			} else {
				log.Info("收到加群邀请，当前BOT处于审核模式，将保留加群邀请")
			}
		case PublicMode:
			localutils.GetBot().SolveGroupInvitedRequest(request.Flag, true, "")
			l.PermissionStateManager.DeleteBlockList(request.GroupCode)
			log.Info("收到加群邀请，当前BOT处于公开模式，将接受加群邀请")
			m, _ := template.LoadAndExec("trigger.private.group_invited.tmpl", map[string]interface{}{
				"member_code": request.InvitorUin,
				"member_name": request.InvitorNick,
				"group_code":  request.GroupCode,
				"group_name":  request.GroupName,
				"command":     CommandMaps,
			})
			if m != nil {
				l.SendMsg(m, mmsg.NewPrivateTarget(request.InvitorUin))
			}
			if err := l.PermissionStateManager.GrantGroupRole(request.GroupCode, request.InvitorUin, permission.GroupAdmin); err != nil {
				if err != permission.ErrPermissionExist {
					log.Errorf("设置群管理员权限失败 - %v", err)
				}
			}
		default:
			// impossible
			log.Errorf("收到加群邀请，当前BOT处于未知模式，将拒绝加群邀请，请将该问题反馈给开发者")
			localutils.GetBot().SolveGroupInvitedRequest(request.Flag, false, "内部错误")
		}
	})

	bot.NewFriendRequestEvent.Subscribe(func(request *adapter.NewFriendRequest) {
		log := logger.WithFields(logrus.Fields{
			"RequesterUin":  request.RequesterUin,
			"RequesterNick": request.RequesterNick,
			"Message":       request.Message,
		})
		if l.PermissionStateManager.CheckBlockList(request.RequesterUin) {
			log.Info("收到好友申请，该用户在block列表中，将拒绝好友申请")
			localutils.GetBot().SolveFriendRequest(request.Flag, false)
			return
		}
		req, err := l.LspStateManager.ListNewFriendRequest()
		if err != nil {
			log.Errorf("ListNewFriendRequest error %v", err)
			return
		}
		for _, r := range req {
			if r.RequesterUin == request.RequesterUin {
				l.LspStateManager.DeleteNewFriendRequest(request.RequestId)
				log.Info("收到好友申请，该用户已在申请列表中，将忽略该申请")
				return
			}
		}
		var botMode string
		switch l.LspStateManager.GetCurrentMode() {
		case PrivateMode:
			botMode = string(PrivateMode)
			log.Info("收到好友申请，当前BOT处于私有模式，将拒绝好友申请")
			localutils.GetBot().SolveFriendRequest(request.Flag, false)
		case ProtectMode:
			botMode = string(ProtectMode)
			if err := l.LspStateManager.SaveNewFriendRequest(request); err != nil {
				log.Errorf("收到好友申请，但记录申请失败，将拒绝该申请，请将该问题反馈给开发者 - error %v", err)
				localutils.GetBot().SolveFriendRequest(request.Flag, false)
			} else {
				log.Info("收到好友申请，当前BOT处于审核模式，将保留好友申请")
			}
		case PublicMode:
			botMode = string(PublicMode)
			log.Info("收到好友申请，当前BOT处于公开模式，将通过好友申请")
			localutils.GetBot().SolveFriendRequest(request.Flag, true)
		default:
			botMode = "unknown"
			// impossible
			log.Errorf("收到好友申请，当前BOT处于未知模式，将拒绝好友申请，请将该问题反馈给开发者")
			localutils.GetBot().SolveFriendRequest(request.Flag, false)
		}
		data := map[string]interface{}{
			"request_id":  request.RequestId,
			"member_name": request.RequesterNick,
			"member_code": request.RequesterUin,
			"bot_mode":    botMode,
		}
		m, _ := template.LoadAndExec("trigger.bot.new_friend_request.tmpl", data)
		if m != nil {
			l.SendMsgToAdmin(m)
		}
	})

	bot.NewFriendEvent.Subscribe(func(event *adapter.NewFriendEvent) {
		log := logger.WithFields(logrus.Fields{
			"Uin":      event.Friend.Uin,
			"Nickname": event.Friend.Nickname,
		})
		log.Info("添加新好友")

		l.LspStateManager.RWCover(func() error {
			requests, err := l.LspStateManager.ListNewFriendRequest()
			if err != nil {
				log.Errorf("ListNewFriendRequest error %v", err)
				return err
			}
			for _, req := range requests {
				if req.RequesterUin == event.Friend.Uin {
					l.LspStateManager.DeleteNewFriendRequest(req.RequestId)
				}
			}
			return nil
		})

		m, _ := template.LoadAndExec("trigger.private.new_friend_added.tmpl", map[string]interface{}{
			"member_code": event.Friend.Uin,
			"member_name": event.Friend.Nickname,
			"command":     CommandMaps,
		})
		if m != nil {
			l.SendMsg(m, mmsg.NewPrivateTarget(event.Friend.Uin))
		}
	})

	bot.GroupJoinEvent.Subscribe(func(info *adapter.GroupInfo) {
		l.FreshIndex()
		log := logger.WithFields(logrus.Fields{
			"GroupCode":   info.Code,
			"MemberCount": info.MemberCount,
			"GroupName":   info.Name,
			"OwnerUin":    info.OwnerUin,
		})
		log.Info("进入新群聊")

		rename := config.GlobalConfig.GetString("bot.onJoinGroup.rename")
		if len(rename) > 0 {
			if len(rename) > 60 {
				rename = rename[:60]
			}
			minfo := info.FindMember(bot.Uin.Load())
			if minfo != nil {
				localutils.GetBot().EditGroupCard(info.Code, bot.Uin.Load(), rename)
			}
		}

		l.LspStateManager.RWCover(func() error {
			requests, err := l.LspStateManager.ListGroupInvitedRequest()
			if err != nil {
				log.Errorf("ListGroupInvitedRequest error %v", err)
				return err
			}
			for _, req := range requests {
				if req.GroupCode == info.Code {
					if err = l.LspStateManager.DeleteGroupInvitedRequest(req.RequestId); err != nil {
						log.WithField("RequestId", req.RequestId).Errorf("DeleteGroupInvitedRequest error %v", err)
					}
					if err = l.PermissionStateManager.GrantGroupRole(info.Code, req.InvitorUin, permission.GroupAdmin); err != nil {
						if err != permission.ErrPermissionExist {
							log.WithField("target", req.InvitorUin).Errorf("设置群管理员权限失败 - %v", err)
						}
					}
				}
			}
			return nil
		})
	})

	bot.GroupLeaveEvent.Subscribe(func(event *adapter.GroupLeaveEvent) {
		log := logger.WithField("GroupCode", event.Group.Code).
			WithField("GroupName", event.Group.Name).
			WithField("MemberCount", event.Group.MemberCount)
		for _, c := range concern.ListConcern() {
			_, ids, _, err := c.GetStateManager().ListConcernState(
				func(groupCode int64, id interface{}, p concern_type.Type) bool {
					return groupCode == event.Group.Code
				})
			if err != nil {
				log = log.WithField(fmt.Sprintf("%v订阅", c.Site()), "查询失败")
			} else {
				log = log.WithField(fmt.Sprintf("%v订阅", c.Site()), len(ids))
			}
		}
		if event.Operator == nil || event.Operator.Uin == bot.Uin.Load() {
			log.Info("退出群聊")
		} else {
			log.Infof("被 %v 踢出群聊", event.Operator.DisplayName())
		}
		l.RemoveAllByGroup(event.Group.Code)
	})

	bot.GroupNotifyEvent.Subscribe(func(ievent adapter.NotifyEvent) {
		switch event := ievent.(type) {
		case *adapter.GroupPokeNotifyEvent:
			data := map[string]interface{}{
				"member_code":   event.Sender,
				"receiver_code": event.Receiver,
				"group_code":    event.GroupCode,
			}
			if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
				data["group_name"] = gi.Name
				if fi := gi.FindMember(event.Sender); fi != nil {
					data["member_name"] = fi.DisplayName()
				}
				if fi := gi.FindMember(event.Receiver); fi != nil {
					data["receiver_name"] = fi.DisplayName()
				}
			}
			m, _ := template.LoadAndExec("trigger.group.poke.tmpl", data)
			if m != nil && l.DebugCheck(event.GroupCode, event.Sender, true) {
				l.SendMsg(m, mmsg.NewGroupTarget(event.GroupCode))
			}
		}
	})

	bot.FriendNotifyEvent.Subscribe(func(ievent adapter.NotifyEvent) {
		switch event := ievent.(type) {
		case *adapter.FriendPokeNotifyEvent:
			if event.Receiver == localutils.GetBot().GetUin() {
				data := map[string]interface{}{
					"member_code": event.Sender,
				}
				if fi := localutils.GetBot().FindFriend(event.Sender); fi != nil {
					data["member_name"] = fi.Nickname
				}
				m, _ := template.LoadAndExec("trigger.private.poke.tmpl", data)
				if m != nil && l.DebugCheck(0, event.Sender, false) {
					l.SendMsg(m, mmsg.NewPrivateTarget(event.Sender))
				}
			}
		}
	})

	bot.GroupMessageEvent.Subscribe(func(msg *adapter.GroupMessage) {
		// Parse 在本回调内同步执行（NewLspGroupCommand 构造函数里调用），
		// 用户消息内容触发解析 panic 时必须就地 recover，
		// 否则会中断同一消息后续所有订阅处理器（logging 等）
		defer func() {
			if e := recover(); e != nil {
				logger.WithField("stack", string(debug.Stack())).
					Errorf("group message dispatch panic recovered: %v", e)
			}
		}()
		if len(msg.Elements) <= 0 {
			return
		}
		if err := l.LspStateManager.SaveMessageImageUrl(msg.GroupCode, int32(msg.ID), msg.Elements); err != nil {
			logger.Errorf("SaveMessageImageUrl failed %v", err)
		}
		if !l.started.Load() {
			return
		}
		logger.Debugf("%+v\n", msg)
		cmd := NewLspGroupCommand(l, msg)
		if Debug {
			cmd.Debug()
		}
		if !l.LspStateManager.IsMuted(msg.GroupCode, bot.Uin.Load()) ||
			l.PermissionStateManager.CheckGroupAdministrator(msg.GroupCode, bot.Uin.Load()) {
			go cmd.Execute()
		} else {
			logger.Debug("BOT被禁言无法响应群指令")
		}
	})

	bot.SelfGroupMessageEvent.Subscribe(func(msg *adapter.GroupMessage) {
		if len(msg.Elements) <= 0 {
			return
		}
		if err := l.LspStateManager.SaveMessageImageUrl(msg.GroupCode, int32(msg.ID), msg.Elements); err != nil {
			logger.Errorf("SaveMessageImageUrl failed %v", err)
		}
	})

	bot.GroupMuteEvent.Subscribe(func(event *adapter.GroupMuteEvent) {
		if err := l.LspStateManager.Muted(event.GroupCode, event.TargetUin, event.Time); err != nil {
			logger.Errorf("Muted failed %v", err)
		}
		if event.TargetUin == localutils.GetBot().GetUin() {
			data := map[string]interface{}{
				"group_code":    event.GroupCode,
				"member_code":   event.TargetUin,
				"operator_code": event.OperatorUin,
				"mute_duration": event.Time,
			}
			if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
				data["group_name"] = gi.Name
				if fi := gi.FindMember(event.TargetUin); fi != nil {
					data["member_name"] = fi.DisplayName()
				}
				if fi := gi.FindMember(event.OperatorUin); fi != nil {
					data["operator_name"] = fi.DisplayName()
				}
			}
			m, _ := template.LoadAndExec("trigger.group.bot_mute.tmpl", data)
			if m != nil {
				l.SendMsgToAdmin(m)
			}
		}
	})

	bot.PrivateMessageEvent.Subscribe(func(msg *adapter.PrivateMessage) {
		// 同 GroupMessageEvent：Parse 在回调内同步执行，需就地 recover
		defer func() {
			if e := recover(); e != nil {
				logger.WithField("stack", string(debug.Stack())).
					Errorf("private message dispatch panic recovered: %v", e)
			}
		}()
		if !l.started.Load() {
			return
		}
		if len(msg.Elements) == 0 {
			return
		}
		cmd := NewLspPrivateCommand(l, msg)
		if Debug {
			cmd.Debug()
		}
		go cmd.Execute()
	})
	bot.FriendMessageRecalledEvent.Subscribe(func(event *adapter.FriendMessageRecalledEvent) {
		friendName := "未知好友"
		if fi := localutils.GetBot().FindFriend(event.FriendUin); fi != nil {
			friendName = fi.Nickname
		}
		m, _ := template.LoadAndExec("trigger.private.friend_recall.tmpl", map[string]interface{}{
			"friend_code": event.FriendUin,
			"friend_name": friendName,
			"message_id":  event.MessageId,
		})
		if m != nil {
			l.SendMsg(m, mmsg.NewPrivateTarget(event.FriendUin))
		}
	})
	bot.DisconnectedEvent.Subscribe(func(event *adapter.ClientDisconnectedEvent) {
		logger.Errorf("收到OnDisconnected事件 %v", event.Message)
		if config.GlobalConfig.GetString("bot.onDisconnected") == "exit" {
			logger.Fatalf("onDisconnected设置为exit，bot将自动退出")
		}
	})

	bot.MemberCardUpdatedEvent.Subscribe(func(event *adapter.MemberCardUpdatedEvent) {
		groupName := event.Group.Name
		memberName := event.Member.DisplayName()
		if gi := localutils.GetBot().FindGroup(event.Group.Code); gi != nil {
			groupName = gi.Name
			if fi := gi.FindMember(event.Member.Uin); fi != nil {
				memberName = fi.DisplayName()
			}
		}
		data := map[string]interface{}{
			"group_code":      event.Group.Code,
			"group_name":      groupName,
			"member_code":     event.Member.Uin,
			"old_member_name": event.OldCard,
			"member_name":     memberName,
		}
		if event.OldCard == "" {
			data["old_member_name"] = event.Member.Nickname
		}
		m, _ := template.LoadAndExec("trigger.group.card_updated.tmpl", data)
		if m != nil && l.DebugCheck(event.Group.Code, event.Member.Uin, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.Group.Code))
		}
	})

	bot.GroupUploadNotifyEvent.Subscribe(func(event *adapter.GroupUploadNotifyEvent) {
		data := map[string]interface{}{
			"member_code": event.Sender,
			"group_code":  event.GroupCode,
			"file_name":   event.File.FileName,
			"file_size":   event.File.FileSize,
			"file_id":     event.File.FileId,
			"file_url":    event.File.FileUrl,
			"file_busId":  event.File.BusId,
		}
		if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
			data["group_name"] = gi.Name
			if fi := gi.FindMember(event.Sender); fi != nil {
				data["member_name"] = fi.DisplayName()
			}
		}
		m, _ := template.LoadAndExec("trigger.group.upload.tmpl", data)
		if m != nil && l.DebugCheck(event.GroupCode, event.Sender, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.GroupCode))
		}
	})

	bot.GroupMemberPermissionChangedEvent.Subscribe(func(event *adapter.MemberPermissionChangedEvent) {
		groupName := event.Group.Name
		memberName := event.Member.DisplayName()
		if gi := localutils.GetBot().FindGroup(event.Group.Code); gi != nil {
			groupName = gi.Name
			if fi := gi.FindMember(event.Member.Uin); fi != nil {
				memberName = fi.DisplayName()
			}
		}
		data := map[string]interface{}{
			"group_code":  event.Group.Code,
			"group_name":  groupName,
			"member_code": event.Member.Uin,
			"member_name": memberName,
		}
		permission := func(permission adapter.MemberPermission) string {
			switch permission {
			case adapter.Member:
				return "群员"
			case adapter.Administrator:
				return "管理员"
			case adapter.Owner:
				return "群主"
			}
			return "未知权限"
		}
		data["old_permission"] = permission(event.OldPermission)
		data["permission"] = permission(event.NewPermission)
		m, _ := template.LoadAndExec("trigger.group.admin_changed.tmpl", data)
		if m != nil && l.DebugCheck(event.Group.Code, event.Member.Uin, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.Group.Code))
		}
	})
	bot.MemberSpecialTitleUpdatedEvent.Subscribe(func(event *adapter.MemberSpecialTitleUpdatedEvent) {
		groupName := ""
		memberName := ""
		if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
			groupName = gi.Name
			if fi := gi.FindMember(event.Uin); fi != nil {
				memberName = fi.DisplayName()
			}
		}
		data := map[string]interface{}{
			"group_code":  event.GroupCode,
			"group_name":  groupName,
			"member_code": event.Uin,
			"member_name": memberName,
			"new_title":   event.NewTitle,
		}
		m, _ := template.LoadAndExec("trigger.group.special_title_updated.tmpl", data)
		if m != nil && l.DebugCheck(event.GroupCode, event.Uin, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.GroupCode))
		}
	})

	bot.GroupEssenceChangedEvent.Subscribe(func(event *adapter.GroupDigestEvent) {
		data := map[string]interface{}{
			"group_code": event.GroupCode,
		}
		if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
			data["group_name"] = gi.Name
		}
		m, _ := template.LoadAndExec("trigger.group.essence_changed.tmpl", data)
		if m != nil && l.DebugCheck(event.GroupCode, 0, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.GroupCode))
		}
	})

	bot.GroupDisbandEvent.Subscribe(func(event *adapter.GroupDisbandEvent) {
		data := map[string]interface{}{
			"group_code": event.Group.Code,
		}
		if gi := localutils.GetBot().FindGroup(event.Group.Code); gi != nil {
			data["group_name"] = gi.Name
		}
		if event.Operator != nil {
			data["operator_code"] = event.Operator.Uin
			if gi := localutils.GetBot().FindGroup(event.Group.Code); gi != nil {
				if fi := gi.FindMember(event.Operator.Uin); fi != nil {
					data["operator_name"] = fi.DisplayName()
				}
			}
		}
		m, _ := template.LoadAndExec("trigger.group.disband.tmpl", data)
		if m != nil && event.Group != nil {
			// 群解散事件发送到管理员
			l.SendMsgToAdmin(m)
		}
	})

	bot.GroupMsgEmojiLikeEvent.Subscribe(func(event *adapter.GroupMsgEmojiLikeEvent) {
		memberName := ""
		if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
			if fi := gi.FindMember(event.UserId); fi != nil {
				memberName = fi.DisplayName()
			}
		}
		data := map[string]interface{}{
			"group_code":  event.GroupCode,
			"member_code": event.UserId,
			"member_name": memberName,
			"message_id":  event.MessageId,
			"emoji_id":    event.EmojiId,
			"emoji_count": event.EmojiCount,
			"is_add":      event.IsAdd,
		}
		m, _ := template.LoadAndExec("trigger.group.emoji_like.tmpl", data)
		if m != nil && l.DebugCheck(event.GroupCode, event.UserId, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.GroupCode))
		}
	})

	bot.ProfileLikeEvent.Subscribe(func(event *adapter.ProfileLikeEvent) {
		data := map[string]interface{}{
			"operator_id":   event.OperatorId,
			"operator_nick": event.OperatorNick,
			"times":         event.Times,
		}
		m, _ := template.LoadAndExec("trigger.private.profile_like.tmpl", data)
		if m != nil {
			l.SendMsgToAdmin(m)
		}
	})

	bot.PokeRecallEvent.Subscribe(func(event *adapter.PokeRecallEvent) {
		data := map[string]interface{}{
			"member_code":   event.Sender,
			"receiver_code": event.Receiver,
			"group_code":    event.GroupCode,
		}
		if gi := localutils.GetBot().FindGroup(event.GroupCode); gi != nil {
			data["group_name"] = gi.Name
			if fi := gi.FindMember(event.Sender); fi != nil {
				data["member_name"] = fi.DisplayName()
			}
			if fi := gi.FindMember(event.Receiver); fi != nil {
				data["receiver_name"] = fi.DisplayName()
			}
		}
		m, _ := template.LoadAndExec("trigger.group.poke_recall.tmpl", data)
		if m != nil && l.DebugCheck(event.GroupCode, event.Sender, true) {
			l.SendMsg(m, mmsg.NewGroupTarget(event.GroupCode))
		}
	})

	bot.BotOnlineEvent.Subscribe(func(event *adapter.BotOnlineEvent) {
		templateName := "notify.bot.online.tmpl"
		data := map[string]interface{}{
			"template_name": templateName,
		}
		logger.Debug("BOT已上线，尝试触发上线提醒模板")
		_, _ = template.LoadAndExec(templateName, data)
	})

	bot.BotOfflineEvent.Subscribe(func(event *adapter.BotOfflineEvent) {
		templateName := "notify.bot.offline.tmpl"
		data := map[string]interface{}{
			"template_name": templateName,
		}
		logger.Debug("BOT已离线，尝试触发离线提醒模板")
		_, _ = template.LoadAndExec(templateName, data)
	})

	bot.BotSendFailedEvent.Subscribe(func(event *adapter.BotSendFailedEvent) {
		logger.Debugf("消息已 %d 次发送失败，尝试触发提醒模板", event.Times)
		templateName := "notify.bot.send_failed.tmpl"
		data := map[string]interface{}{
			"message":     event.Message,
			"target_id":   event.TargetUin,
			"target_type": event.TargetType,
			"times":       event.Times,
		}
		switch event.TargetType {
		case TargetTypeGroup:
			if gi := localutils.GetBot().FindGroup(event.TargetUin); gi != nil {
				data["target_name"] = gi.Name
			}
		case TargetTypeFriend:
			if fi := localutils.GetBot().FindFriend(event.TargetUin); fi != nil {
				data["target_name"] = fi.Nickname
			}
		}
		logger.Debug("消息多次发送失败，尝试触发提醒模板")
		_, _ = template.LoadAndExec(templateName, data)
	})
}

func (l *Lsp) SendMsgToAdmin(m *mmsg.MSG) {
	if admin := l.PermissionStateManager.ListAdmin(); len(admin) > 0 {
		l.SendMsg(m, mmsg.NewPrivateTarget(admin[0]))
	} else {
		logger.Warn("未设置管理员，取消提示")
	}
}

func (l *Lsp) PostStart(bot *bot.Bot) {
	l.FreshIndex()
	go func() {
		for range time.Tick(time.Second * 30) {
			l.FreshIndex()
		}
	}()
	l.CronjobReload()
	l.CronStart()

	// 等待 bot 上线后再启动订阅系统，避免 not connected 和 nil group info 错误
	go func() {
		if SkipOnlineCheck {
			logger.Warn("本次启动已跳过 bot 上线等待流程（--online）")
			// --online 模式仍需启动订阅系统，否则微博/B站等订阅任务不会运行
			concern.StartAll()
			l.started.Store(true)
			logger.Infof("DDBOT启动完成（--online 调试模式）")
		} else {
			// 首次等待：5 分钟超时
			if !waitForBotOnline(l, bot, 5*time.Minute) {
				logger.Errorf("首次等待 bot 上线超时，将在后台继续重试...")
			}
		}

		// 如果仍需要等待，在后台持续重试（每30秒检查一次）
		if !SkipOnlineCheck && !l.started.Load() {
			go func() {
				retryTicker := time.NewTicker(30 * time.Second)
				defer retryTicker.Stop()
				for range retryTicker.C {
					if l.started.Load() {
						return
					}
					logger.Info("后台重试：等待 bot 上线...")
					if waitForBotOnline(l, bot, 30*time.Second) {
						return
					}
					logger.Debug("后台重试：bot 仍未上线，继续等待...")
				}
			}()
		}
	}()

	// 启动TG适配器
	l.StartTelegramCommands()

	var newVersionChan = make(chan string, 1)
	go func() {
		newVersionChan <- CheckUpdate()
		for range time.Tick(time.Hour * 24) {
			newVersionChan <- CheckUpdate()
		}
	}()
	go l.NewVersionNotify(newVersionChan)

}

// waitForBotOnline 等待 bot 上线并完成群列表加载，超时返回 false
func waitForBotOnline(l *Lsp, b *bot.Bot, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	logger.Infof("等待 bot 上线（超时 %v）...", timeout)
	// 基于实际 WS 连接状态等待，避免心跳缓存 Online 滞后导致误判
	for b.Messenger == nil || !b.Messenger.IsConnected() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
	// 等待群/好友/群成员列表加载完成，不依赖固定 Sleep
	logger.Info("bot 已上线，等待群列表加载完成...")
	for b.Messenger == nil || !b.Messenger.IsListLoaded() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
	logger.Info("群列表加载完成，启动订阅系统")

	concern.StartAll()
	l.started.Store(true)
	logger.Infof("DDBOT启动完成")
	logger.Infof("D宝，一款真正人性化的单推BOT")
	if len(l.PermissionStateManager.ListAdmin()) == 0 {
		logger.Infof("您似乎正在部署全新的BOT，请通过qq对bot私聊发送<%v>(不含括号)获取管理员权限，然后私聊发送<%v>(不含括号)开始使用您的bot",
			l.CommandShowName(WhosyourdaddyCommand), l.CommandShowName(HelpCommand))
	}
	return true
}

func (l *Lsp) Start(bot *bot.Bot) {
	go l.ConcernNotify()
}

func (l *Lsp) Stop(bot *bot.Bot, wg *sync.WaitGroup) {
	defer wg.Done()
	if l.stop != nil {
		close(l.stop)
	}
	l.CronStop()
	concern.StopAll()

	l.wg.Wait()
	logger.Debug("等待所有推送发送完毕")
	l.notifyWg.Wait()
	logger.Debug("推送发送完毕")

	proxy_pool.Stop()
}

func (l *Lsp) NewVersionNotify(newVersionChan <-chan string) {
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("stack", string(debug.Stack())).
				Errorf("new version notify recoverd %v", err)
			go l.NewVersionNotify(newVersionChan)
		}
	}()
	for newVersion := range newVersionChan {
		if newVersion == "" {
			continue
		}
		var newVersionNotify bool
		err := localdb.RWCover(func() error {
			key := localdb.DDBotReleaseKey()
			releaseVersion, err := localdb.Get(key, localdb.IgnoreNotFoundOpt())
			if err != nil {
				return err
			}
			if releaseVersion != newVersion {
				newVersionNotify = true
			}
			return localdb.Set(key, newVersion)
		})
		if err != nil {
			logger.Errorf("NewVersionNotify error %v", err)
			continue
		}
		if !newVersionNotify {
			continue
		}
		m := mmsg.NewMSG()
		m.Textf("DDBOT管理员您好，DDBOT有可用更新版本【%v】，请前往 https://github.com/cnxysoft/DDBOT-WSa/releases 查看详细信息\n\n", newVersion)
		m.Textf("如果您不想接收更新消息，请输入<%v>(不含括号)", l.CommandShowName(NoUpdateCommand))
		for _, admin := range l.PermissionStateManager.ListAdmin() {
			if localdb.Exist(localdb.DDBotNoUpdateKey(admin)) {
				continue
			}
			if localutils.GetBot().FindFriend(admin) == nil {
				continue
			}
			logger.WithField("Target", admin).Infof("new ddbot version notify")
			l.SendMsg(m, mmsg.NewPrivateTarget(admin))
		}
	}
}

func (l *Lsp) FreshIndex() {
	for _, c := range concern.ListConcern() {
		c.FreshIndex()
	}
	l.PermissionStateManager.FreshIndex()
	l.LspStateManager.FreshIndex()
}

func (l *Lsp) RemoveAllByGroup(groupCode int64) {
	for _, c := range concern.ListConcern() {
		c.GetStateManager().RemoveAllByGroupCode(groupCode)
	}
	l.PermissionStateManager.RemoveAllByGroupCode(groupCode)
}

func (l *Lsp) GetImageFromPool(options ...image_pool.OptionFunc) ([]image_pool.Image, error) {
	if l.pool == nil {
		return nil, image_pool.ErrNotInit
	}
	return l.pool.Get(options...)
}

func (l *Lsp) send(msg *adapter.SendingMessage, target mmsg.Target) interface{} {
	switch target.TargetType() {
	case mmsg.TargetGroup:
		return l.sendGroupMessage(target.TargetCode(), msg)
	case mmsg.TargetPrivate:
		return l.sendPrivateMessage(target.TargetCode(), msg)
	}
	panic("unknown target type")
}

// SendMsg 总是返回至少一个
func (l *Lsp) SendMsg(m *mmsg.MSG, target mmsg.Target) (res []interface{}) {
	failure := func() interface{} {
		switch target.TargetType() {
		case mmsg.TargetPrivate:
			return &adapter.PrivateMessage{ID: -1}
		case mmsg.TargetGroup:
			return &adapter.GroupMessage{ID: -1}
		}
		return &adapter.GroupMessage{ID: -1}
	}

	if m == nil {
		res = append(res, failure())
		return
	}

	// 检查 elements 中是否有 ForwardElement
	var forwardNodes []map[string]interface{}
	var forwardOptions *adapter.ForwardOptions
	for _, elem := range m.Elements() {
		if fe, ok := elem.(*mmsg.ForwardElement); ok {
			forwardNodes = append(forwardNodes, fe.Nodes...)
			if fe.Options != nil {
				// 转换 mmsg.ForwardOptions 为 adapter.ForwardOptions
				forwardOptions = &adapter.ForwardOptions{
					Prompt:  fe.Options.Prompt,
					Source:  fe.Options.Source,
					Summary: fe.Options.Summary,
					News:    fe.Options.News,
				}
			}
		}
	}

	if len(forwardNodes) > 0 {
		switch target.TargetType() {
		case mmsg.TargetPrivate:
			msgID, _, err := l.sendPrivateForwardMessage(target.TargetCode(), forwardNodes, forwardOptions)
			if err != nil {
				res = append(res, &adapter.PrivateMessage{ID: -1})
			} else {
				res = append(res, &adapter.PrivateMessage{ID: int64(msgID), UserID: target.TargetCode()})
			}
		case mmsg.TargetGroup:
			msgID, _, err := l.sendGroupForwardMessage(target.TargetCode(), forwardNodes, forwardOptions)
			if err != nil {
				res = append(res, &adapter.GroupMessage{ID: -1})
			} else {
				res = append(res, &adapter.GroupMessage{ID: int64(msgID), GroupCode: target.TargetCode()})
			}
		}
		return
	}

	msgs := m.ToMessage(target)
	if len(msgs) == 0 {
		res = append(res, failure())
		return
	}
	for idx, msg := range msgs {
		r := l.send(msg, target)
		res = append(res, r)
		switch v := r.(type) {
		case *adapter.GroupMessage:
			if v.ID == -1 {
				return
			}
		case *adapter.PrivateMessage:
			if v.ID == -1 {
				return
			}
		}
		if idx > 1 {
			time.Sleep(time.Millisecond * 300)
		}
	}
	return res
}

func (l *Lsp) AGM(res []interface{}) []*adapter.GroupMessage {
	var result []*adapter.GroupMessage
	for _, r := range res {
		result = append(result, r.(*adapter.GroupMessage))
	}
	return result
}

func (l *Lsp) APM(res []interface{}) []*adapter.PrivateMessage {
	var result []*adapter.PrivateMessage
	for _, r := range res {
		result = append(result, r.(*adapter.PrivateMessage))
	}
	return result
}

func (l *Lsp) sendPrivateMessage(uin int64, msg *adapter.SendingMessage) (res *adapter.PrivateMessage) {
	if msg == nil {
		logger.WithFields(localutils.FriendLogFields(uin)).Debug("send with nil private message")
		return &adapter.PrivateMessage{ID: -1}
	}
	// 不在此处依据连接状态短路：由 Messenger.SendPrivateMessage 统一处理
	// 在线直接发送、离线且开启离线队列时入队，避免依赖可能滞后的 Online 心跳缓存误判
	if bot.Instance == nil || bot.Instance.Messenger == nil {
		return &adapter.PrivateMessage{ID: -1, UserID: uin, Elements: msg.Elements}
	}
	msg.Elements = localutils.AdapterMessageFilter(msg.Elements, func(element adapter.IMessageElement) bool {
		return element != nil
	})
	if len(msg.Elements) == 0 {
		logger.WithFields(localutils.FriendLogFields(uin)).Debug("send with empty private message")
		return &adapter.PrivateMessage{ID: -1}
	}
	var newstring = msgstringer.AdapterMsgToString(msg.Elements)
	resp := bot.Instance.SendPrivateMessage(uin, msg, newstring)
	res = resp.RetMSG
	if res == nil || res.ID == -1 {
		logger.WithField("content", msgstringer.AdapterMsgToString(msg.Elements)).
			WithFields(localutils.GroupLogFields(uin)).
			Errorf("发送私聊消息失败")
	}
	if res == nil {
		res = &adapter.PrivateMessage{ID: -1, UserID: uin, Elements: msg.Elements}
	}
	return res
}

// sendGroupMessage 发送一条消息，返回值总是非nil，ID为-1表示发送失败
func (l *Lsp) sendGroupMessage(groupCode int64, msg *adapter.SendingMessage, recovered ...bool) (res *adapter.GroupMessage) {
	defer func() {
		if e := recover(); e != nil {
			content := ""
			if msg != nil {
				content = msgstringer.AdapterMsgToString(msg.Elements)
			}
			if len(recovered) == 0 {
				logger.WithField("content", content).
					WithField("stack", string(debug.Stack())).
					Errorf("sendGroupMessage panic recovered")
				res = l.sendGroupMessage(groupCode, msg, true)
			} else {
				logger.WithField("content", content).
					WithField("stack", string(debug.Stack())).
					Errorf("sendGroupMessage panic recovered but panic again %v", e)
				elements := []adapter.IMessageElement(nil)
				if msg != nil {
					elements = msg.Elements
				}
				res = &adapter.GroupMessage{ID: -1, GroupCode: groupCode, Elements: elements}
			}
		}
	}()
	if msg == nil {
		logger.Debug("消息为空，返回")
		logger.WithFields(localutils.GroupLogFields(groupCode)).Debug("send with nil group message")
		return &adapter.GroupMessage{ID: -1, GroupCode: groupCode}
	}
	if bot.Instance == nil {
		return &adapter.GroupMessage{ID: -1, GroupCode: groupCode, Elements: msg.Elements}
	}
	if l.LspStateManager.IsMuted(groupCode, bot.Instance.Uin.Load()) &&
		!l.PermissionStateManager.CheckGroupAdministrator(groupCode, bot.Instance.Uin.Load()) {
		logger.WithField("content", msgstringer.AdapterMsgToString(msg.Elements)).
			WithFields(localutils.GroupLogFields(groupCode)).
			Debug("BOT被禁言无法发送群消息")
		return &adapter.GroupMessage{ID: -1, GroupCode: groupCode, Elements: msg.Elements}
	}
	msg.Elements = localutils.AdapterMessageFilter(msg.Elements, func(element adapter.IMessageElement) bool {
		return element != nil
	})
	if len(msg.Elements) == 0 {
		logger.WithFields(localutils.GroupLogFields(groupCode)).Debug("send with empty group message")
		return &adapter.GroupMessage{ID: -1, GroupCode: groupCode}
	}
	var newstring = msgstringer.AdapterMsgToString(msg.Elements)
	ret := bot.Instance.SendGroupMessage(groupCode, msg, newstring)
	res = ret.RetMSG
	err := ret.Error
	if err != nil {
		msgStr := msgstringer.AdapterMsgToString(msg.Elements)
		if len(msgStr) > 150 {
			msgStr = msgStr[:150] + "..."
		}
		logger.WithField("content", msgStr).
			WithFields(localutils.GroupLogFields(groupCode)).
			Error(err)
	}
	if res == nil {
		logger.WithFields(localutils.GroupLogFields(groupCode)).Debug("failed to send message")
		res = &adapter.GroupMessage{ID: -1, GroupCode: groupCode, Elements: msg.Elements}
	}
	return res
}

// sendGroupForwardMessage 发送群合并转发消息
func (l *Lsp) sendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	if bot.Instance == nil {
		return -1, "", fmt.Errorf("bot not initialized")
	}
	return bot.Instance.SendGroupForwardMessage(groupCode, nodes, options)
}

// sendPrivateForwardMessage 发送私聊合并转发消息
func (l *Lsp) sendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	if bot.Instance == nil {
		return -1, "", fmt.Errorf("bot not initialized")
	}
	return bot.Instance.SendPrivateForwardMessage(userID, nodes, options)
}

var Instance = &Lsp{
	concernNotify:          concern.ReadNotifyChan(),
	stop:                   make(chan interface{}),
	status:                 NewStatus(),
	msgLimit:               semaphore.NewWeighted(3),
	PermissionStateManager: permission.NewStateManager(),
	LspStateManager:        NewStateManager(),
	cron:                   cron.New(cron.WithLogger(cron.VerbosePrintfLogger(cronLog)), cron.WithChain(cron.Recover(cron.PrintfLogger(cronLog)))),
}

func init() {
	bot.RegisterModule(Instance)

	template.RegisterExtFunc("currentMode", func() string {
		return string(Instance.LspStateManager.GetCurrentMode())
	})

	// 注意：sorted set 索引在 ZAdd 时惰性创建，无需在 init 中预创建
}
