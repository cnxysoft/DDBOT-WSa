package lsp

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/Sora233/sliceutil"
	"github.com/alecthomas/kong"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

type LspPrivateCommand struct {
	msg *adapter.PrivateMessage

	*Runtime
}

func NewLspPrivateCommand(l *Lsp, msg *adapter.PrivateMessage) *LspPrivateCommand {
	c := &LspPrivateCommand{
		msg:     msg,
		Runtime: NewRuntime(l),
	}
	c.Parse(c.msg.Elements)
	return c
}

func (c *LspPrivateCommand) Execute() {
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("stack", string(debug.Stack())).
				Errorf("panic recovered: %v", err)
			c.textSend("エラー発生：看到该信息表示BOT出了一些问题，该问题已记录")
		}
	}()

	if len(c.CommandName()) == 0 {
		return
	}

	log := c.DefaultLogger().WithField("cmd", c.GetCmdArgs())

	if c.l.PermissionStateManager.CheckBlockList(c.uin()) {
		log.Debug("blocked")
		return
	}

	if !c.DebugCheck() {
		log.Debugf("debug mode, skip execute.")
		return
	}

	log.Debug("execute command")

	// all permission will be checked later
	switch c.CommandName() {
	case PingCommand:
		c.PingCommand()
	case HelpCommand:
		c.HelpCommand()
	case BlockCommand:
		c.BlockCommand()
	case WatchCommand:
		c.WatchCommand(false)
	case UnwatchCommand:
		c.WatchCommand(true)
	case EnableCommand:
		c.EnableCommand(false)
	case DisableCommand:
		c.EnableCommand(true)
	case GrantCommand:
		c.GrantCommand()
	case LogCommand:
		c.LogCommand()
	case ListCommand:
		c.ListCommand()
	case SysinfoCommand:
		c.SysinfoCommand()
	case StatusCommand:
		c.StatusCommand()
	case ConfigCommand:
		c.ConfigCommand()
	case WhosyourdaddyCommand:
		c.WhosyourdaddyCommand()
	case QuitCommand:
		c.QuitCommand()
	case ModeCommand:
		c.ModeCommand()
	case GroupRequestCommand:
		c.GroupRequestCommand()
	case FriendRequestCommand:
		c.FriendRequestCommand()
	case AdminCommand:
		c.AdminCommand()
	case SilenceCommand:
		c.SilenceCommand()
	case NoUpdateCommand:
		c.NoUpdateCommand()
	case AbnormalConcernCheck:
		c.AbnormalConcernCheckCommand()
	case CleanConcern:
		c.CleanConcernCommand()
	case ResubscribeCommand:
		c.ResubscribeCommand()
	default:
		if CheckCustomPrivateCommand(c.CommandName()) {
			func() {
				log := c.DefaultLoggerWithCommand(c.CommandName()).WithField("CustomCommand", true)
				log.Infof("run %v command", c.CommandName())
				defer func() { log.Infof("%v command end", c.CommandName()) }()
				c.sendChain(
					c.templateMsg(
						fmt.Sprintf("custom.command.private.%s.tmpl", c.CommandName()),
						map[string]interface{}{
							"cmd":       c.CommandName(),
							"args":      c.GetArgs(),
							"full_args": strings.Join(c.GetArgs(), " "),
						},
					),
				)
			}()
		} else {
			func() {
				log := c.DefaultLoggerWithCommand("Unknown CMD Tips").WithField("CustomCommand", true)
				log.Info("run Unknown CMD Tips command")
				defer func() { log.Info("Unknown CMD Tips command end") }()
				c.sendChain(
					c.templateMsg(
						"command.private.unknown_cmd_tips.tmpl",
						map[string]interface{}{
							"cmd":       c.CommandName(),
							"args":      c.GetArgs(),
							"full_args": strings.Join(c.GetArgs(), " "),
							"help_cmd":  c.l.CommandShowName(HelpCommand),
						},
					),
				)
			}()
			log.Debug("no command matched")
		}
	}
}

func (c *LspPrivateCommand) NoUpdateCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var noUpdateCmd struct {
		Delete bool `optional:"" short:"d" help:"取消设置"`
	}

	_, output := c.parseCommandSyntax(&noUpdateCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	var err error
	key := localdb.DDBotNoUpdateKey(c.uin())
	if noUpdateCmd.Delete {
		_, err = localdb.Delete(key, localdb.IgnoreNotFoundOpt())
		if err == nil {
			c.textReply("成功 - 您将接收到更新消息")
		}
	} else {
		err = localdb.Set(key, "")
		if err == nil {
			c.textReply("成功 - 您将不再接受更新消息")
		}
	}
	if err != nil {
		c.textReplyF("失败 - %v", err)
	}
}

func (c *LspPrivateCommand) AbnormalConcernCheckCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	_, output := c.parseCommandSyntax(&struct{}{}, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	IAbnormalConcernCheck(c.NewMessageContext(log))
}

func (c *LspPrivateCommand) CleanConcernCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var cleanConcernCmd struct {
		Abnormal   bool    `optional:"" help:"清除异常订阅"`
		GroupCodes []int64 `optional:"" short:"g" help:"清除指定群的订阅，多个可用英文逗号隔开"`
		Site       string  `optional:"" short:"s" help:"清除指定的网站订阅，默认为全部"`
		Type       string  `optional:"" short:"t" help:"清除指定的订阅类型，默认为全部"`
	}

	_, output := c.parseCommandSyntax(&cleanConcernCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	ICleanConcern(c.NewMessageContext(log), cleanConcernCmd.Abnormal,
		cleanConcernCmd.GroupCodes, cleanConcernCmd.Site, cleanConcernCmd.Type)
}

func (c *LspPrivateCommand) ResubscribeCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	var resubscribeCmd struct {
		GroupCode int64 `required:"" short:"g" help:"要重新订阅的群号码"`
	}

	_, output := c.parseCommandSyntax(&resubscribeCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	// 成品 x64 0xcc8560 callees 含 (*LspPrivateCommand).checkGroupCode（+0x2c0），
	// 且没有 RequireAny/AdminRoleRequireOption；权限与群有效性统一由 checkGroupCode 负责。
	if err := c.checkGroupCode(resubscribeCmd.GroupCode); err != nil {
		c.textReply(err.Error())
		return
	}

	// 调用微博 Concern 的一键重新订阅功能
	weiboConcern, err := concern.GetConcernBySiteAndType("weibo", weibo.News)
	if err != nil {
		c.textReply(fmt.Sprintf("失败 - %v", err))
		return
	}

	wc, ok := weiboConcern.(*weibo.Concern)
	if !ok {
		c.textReply("失败 - 类型转换错误")
		return
	}

	count, err := wc.ResubscribeAll(c.NewMessageContext(log), resubscribeCmd.GroupCode)
	if err != nil {
		c.textReply(fmt.Sprintf("失败 - %v", err))
		return
	}

	c.textSend(fmt.Sprintf("成功 - 已重新订阅 %d 个微博用户", count))
}

func (c *LspPrivateCommand) WhosyourdaddyCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	_, output := c.parseCommandSyntax(&struct{}{}, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	if c.l.PermissionStateManager.CheckRole(c.uin(), permission.Admin) {
		c.textReply("您已经是管理员了，请不要重复使用此命令。")
		return
	}

	if c.l.PermissionStateManager.CheckNoAdmin() {
		if err := c.l.PermissionStateManager.GrantRole(c.uin(), permission.Admin); err != nil {
			log.WithField("permission", permission.Admin.String()).
				Errorf("GrantRole error %v", err)
			c.textReply("失败 - 内部错误")
		} else {
			log.Info("已配置bot初始管理员，现在可以开始使用bot了，祝你好运")
			c.textReply("成功 - 您已成为bot管理员")
		}
	} else {
		log.Debug("someone is trying WhosyourdaddyCommand")
		c.textReply("失败 - 该bot不属于你！")
	}
}

func (c *LspPrivateCommand) ListCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	var listCmd struct {
		Group int64  `optional:"" short:"g" help:"要操作的QQ群号码"`
		Site  string `optional:"" short:"s" help:"网站参数"`
	}
	_, output := c.parseCommandSyntax(&listCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	groupCode := listCmd.Group
	if err := c.checkGroupCode(groupCode); err != nil {
		c.textReply(err.Error())
		return
	}
	log = log.WithFields(localutils.GroupLogFields(groupCode))
	IList(c.NewMessageContext(log), groupCode, listCmd.Site)
}

func (c *LspPrivateCommand) ConfigCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	var configCmd struct {
		At struct {
			Site   string  `optional:"" short:"s" default:"bilibili" help:"网站参数"`
			Id     string  `arg:"" help:"配置的主播id"`
			Action string  `arg:"" enum:"add,remove,clear,show" help:"add / remove / clear / show"`
			QQ     []int64 `arg:"" optional:"" help:"需要@的成员QQ号码"`
		} `cmd:"" help:"配置推送时的@人员列表，默认为空" name:"at"`
		AtAll struct {
			Site   string `optional:"" short:"s" default:"bilibili" help:"网站参数"`
			Id     string `arg:"" help:"配置的主播id"`
			Switch string `arg:"" default:"on" enum:"on,off" help:"on / off"`
		} `cmd:"" help:"配置推送时@全体成员，默认关闭，需要管理员权限" name:"at_all"`
		TitleNotify struct {
			Site   string `optional:"" short:"s" default:"bilibili" help:"网站参数"`
			Id     string `arg:"" help:"配置的主播id"`
			Switch string `arg:"" default:"off" enum:"on,off" help:"on / off"`
		} `cmd:"" help:"配置直播间标题发生变化时是否进行推送，默认不推送" name:"title_notify"`
		OfflineNotify struct {
			Site   string `optional:"" short:"s" default:"bilibili" help:"网站参数"`
			Id     string `arg:"" help:"配置的主播id"`
			Switch string `arg:"" default:"off" enum:"on,off," help:"on / off"`
		} `cmd:"" help:"配置下播时是否进行推送，默认不推送" name:"offline_notify"`
		ExtendNotify struct {
			Site   string `optional:"" short:"s" default:"bilibili" help:"网站参数"`
			Id     string `arg:"" help:"配置的主播id"`
			Switch string `arg:"" default:"off" enum:"on,off," help:"on / off"`
		} `cmd:"" help:"配置是否启用增强推送，默认不启用" name:"extend_notify"`
		Filter struct {
			Site string `optional:"" short:"s" default:"bilibili" help:"网站参数"`
			Type struct {
				Id   string   `arg:"" help:"配置的主播id"`
				Type []string `arg:"" optional:"" help:"指定的种类"`
			} `cmd:"" help:"只推送指定种类的动态" name:"type" group:"filter"`
			NotType struct {
				Id   string   `arg:"" help:"配置的主播id"`
				Type []string `arg:"" optional:"" help:"指定不推送的种类"`
			} `cmd:"" help:"不推送指定种类的动态" name:"not_type" group:"filter"`
			Text struct {
				Id      string   `arg:"" help:"配置的主播id"`
				Keyword []string `arg:"" optional:"" help:"指定的关键字"`
			} `cmd:"" help:"当动态内容里出现关键字时进行推送" name:"text" group:"filter"`
			NotText struct {
				Id      string   `arg:"" help:"配置的主播id"`
				Keyword []string `arg:"" optional:"" help:"指定的关键字"`
			} `cmd:"" help:"当动态内容未出现关键字时进行推送" name:"not_text" group:"filter"`
			Clear struct {
				Id string `arg:"" help:"配置的主播id"`
			} `cmd:"" help:"清除过滤器" name:"clear" group:"filter"`
			Show struct {
				Id string `arg:"" help:"配置的主播id"`
			} `cmd:"" help:"查看当前过滤器" name:"show" group:"filter"`
		} `cmd:"" help:"配置动态过滤器" name:"filter"`
		WeiboAlert struct {
			Action string `arg:"" optional:"" default:"show" enum:"set,clear,show" help:"set=设置告警目标, clear=清除, show=查看当前设置"`
			Group  int64  `optional:"" short:"g" help:"告警发送到指定群"`
			QQ     int64  `optional:"" short:"q" help:"告警私聊发送给指定QQ"`
		} `cmd:"" help:"配置微博Cookie/SUB告警通知的发送对象" name:"weibo_alert"`
		Group int64 `optional:"" short:"g" help:"要操作的QQ群号码"`
	}

	kongCtx, output := c.parseCommandSyntax(&configCmd, c.CommandName(),
		kong.Description("管理BOT的配置，目前支持配置@成员、@全体成员、开启下播推送、开启标题推送、推送过滤"),
	)
	if output != "" {
		c.textReply(output)
	}
	if c.exit || len(kongCtx.Path) <= 1 {
		return
	}

	groupCode := configCmd.Group

	kongPath := strings.Split(kongCtx.Command(), " ")

	cmd := kongPath[0]

	// weibo_alert 不需要群号校验，其他子命令需要
	if cmd != "weibo_alert" {
		if err := c.checkGroupCode(groupCode); err != nil {
			c.textReply(err.Error())
			return
		}
	}
	log = log.WithFields(localutils.GroupLogFields(groupCode)).WithField("sub_command", cmd)

	switch cmd {
	case "at":
		site, ctype, err := c.ParseRawSiteAndType(configCmd.At.Site, "live")
		if err != nil {
			log.WithField("site", configCmd.At.Site).Errorf("ParseRawSiteAndType failed %v", err)
			c.textSend(fmt.Sprintf("失败 - %v", err.Error()))
			return
		}
		log = log.WithField("site", site).WithField("id", configCmd.At.Id).WithField("action", configCmd.At.Action).WithField("QQ", configCmd.At.QQ)
		IConfigAtCmd(c.NewMessageContext(log), groupCode, configCmd.At.Id, site, ctype, configCmd.At.Action, configCmd.At.QQ)
	case "at_all":
		site, ctype, err := c.ParseRawSiteAndType(configCmd.AtAll.Site, "live")
		if err != nil {
			log.WithField("site", configCmd.AtAll.Site).Errorf("ParseRawSiteAndType failed %v", err)
			c.textSend(fmt.Sprintf("失败 - %v", err.Error()))
			return
		}
		var on = localutils.Switch2Bool(configCmd.AtAll.Switch)
		log = log.WithField("site", site).WithField("id", configCmd.AtAll.Id).WithField("on", on)
		IConfigAtAllCmd(c.NewMessageContext(log), groupCode, configCmd.AtAll.Id, site, ctype, on)
	case "title_notify":
		site, ctype, err := c.ParseRawSiteAndType(configCmd.TitleNotify.Site, "live")
		if err != nil {
			log.WithField("site", configCmd.TitleNotify.Site).Errorf("ParseRawSiteAndType failed %v", err)
			c.textSend(fmt.Sprintf("失败 - %v", err.Error()))
			return
		}
		var on = localutils.Switch2Bool(configCmd.TitleNotify.Switch)
		log = log.WithField("site", site).WithField("id", configCmd.TitleNotify.Id).WithField("on", on)
		IConfigTitleNotifyCmd(c.NewMessageContext(log), groupCode, configCmd.TitleNotify.Id, site, ctype, on)
	case "offline_notify":
		site, ctype, err := c.ParseRawSiteAndType(configCmd.OfflineNotify.Site, "live")
		if err != nil {
			log.WithField("site", configCmd.OfflineNotify.Site).Errorf("ParseRawSiteAndType failed %v", err)
			c.textSend(fmt.Sprintf("失败 - %v", err.Error()))
			return
		}
		var on = localutils.Switch2Bool(configCmd.OfflineNotify.Switch)
		log = log.WithField("site", site).WithField("id", configCmd.OfflineNotify.Id).WithField("on", on)
		IConfigOfflineNotifyCmd(c.NewMessageContext(log), groupCode, configCmd.OfflineNotify.Id, site, ctype, on)
	case "extend_notify":
		site, ctype, err := c.ParseRawSiteAndType(configCmd.ExtendNotify.Site, "live")
		if err != nil {
			log.WithField("site", configCmd.ExtendNotify.Site).Errorf("ParseRawSiteAndType failed %v", err)
			c.textSend(fmt.Sprintf("失败 - %v", err.Error()))
			return
		}
		var on = localutils.Switch2Bool(configCmd.ExtendNotify.Switch)
		log = log.WithField("site", site).WithField("id", configCmd.ExtendNotify.Id).WithField("on", on)
		IConfigExtendNotifyCmd(c.NewMessageContext(log), groupCode, configCmd.ExtendNotify.Id, site, ctype, on)
	case "filter":
		filterCmd := kongPath[1]
		site, ctype, err := c.ParseRawSiteAndType(configCmd.Filter.Site, "news")
		if err != nil {
			log.WithField("site", configCmd.Filter.Site).Errorf("ParseRawSiteAndType failed %v", err)
			c.textSend(fmt.Sprintf("失败 - %v", err.Error()))
			return
		}
		switch filterCmd {
		case "type":
			IConfigFilterCmdType(c.NewMessageContext(log), groupCode, configCmd.Filter.Type.Id, site, ctype, configCmd.Filter.Type.Type)
		case "not_type":
			IConfigFilterCmdNotType(c.NewMessageContext(log), groupCode, configCmd.Filter.NotType.Id, site, ctype, configCmd.Filter.NotType.Type)
		case "text":
			IConfigFilterCmdText(c.NewMessageContext(log), groupCode, configCmd.Filter.Text.Id, site, ctype, configCmd.Filter.Text.Keyword)
		case "not_text":
			IConfigFilterCmdNotText(c.NewMessageContext(log), groupCode, configCmd.Filter.NotText.Id, site, ctype, configCmd.Filter.NotText.Keyword)
		case "clear":
			IConfigFilterCmdClear(c.NewMessageContext(log), groupCode, configCmd.Filter.Clear.Id, site, ctype)
		case "show":
			IConfigFilterCmdShow(c.NewMessageContext(log), groupCode, configCmd.Filter.Show.Id, site, ctype)
		default:
			log.WithField("filter_cmd", filterCmd).Errorf("unknown filter command")
			c.textSend("未知的filter子命令")
		}
	case "weibo_alert":
		handleWeiboAlertPrivate(func(s string) { c.textSend(s) }, configCmd.WeiboAlert.Action, configCmd.WeiboAlert.Group, configCmd.WeiboAlert.QQ)
	default:
		c.textSend("暂未支持，你可以催作者GKD")
	}

}

func (c *LspPrivateCommand) WatchCommand(remove bool) {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	var (
		site      string
		watchType concern_type.Type
		err       error
	)

	var watchCmd struct {
		Site  string `optional:"" short:"s" default:"bilibili" help:"网站参数"`
		Type  string `optional:"" short:"t" default:"" help:"类型参数"`
		Group int64  `optional:"" short:"g" help:"要操作的QQ群号码"`
		Id    string `arg:""`
	}

	_, output := c.parseCommandSyntax(&watchCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	site, watchType, err = c.ParseRawSiteAndType(watchCmd.Site, watchCmd.Type)
	if err != nil {
		log = log.WithField("args", c.GetArgs())
		log.Errorf("parse raw concern failed %v", err)
		c.textReply(fmt.Sprintf("参数错误 - %v", err))
		return
	}
	log = log.WithField("site", site).WithField("type", watchType)

	id := watchCmd.Id
	groupCode := watchCmd.Group

	if err := c.checkGroupCode(groupCode); err != nil {
		c.textReply(err.Error())
		return
	}

	log = log.WithFields(localutils.GroupLogFields(groupCode))

	IWatch(c.NewMessageContext(log), groupCode, id, site, watchType, remove)
}

func (c *LspPrivateCommand) EnableCommand(disable bool) {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	var enableCmd struct {
		Group   int64  `optional:"" short:"g" help:"要操作的QQ群号码"`
		Command string `arg:"" optional:"" help:"命令名"`
		Global  bool   `optional:"" help:"系统级操作，对所有群生效"`
	}

	_, output := c.parseCommandSyntax(&enableCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	if len(enableCmd.Command) == 0 {
		c.textReply("失败 - 没有指定要操作的命令名")
		log.Errorf("empty command")
		return
	}

	command := CombineCommand(enableCmd.Command)
	if !CheckOperateableCommand(command) {
		log.Errorf("unknown command")
		c.textReply("失败 - 命令名非法")
		return
	}

	if enableCmd.Global {
		if !c.l.PermissionStateManager.CheckRole(c.uin(), permission.Admin) {
			c.noPermission()
			return
		}
		if enableCmd.Group != 0 {
			c.textReply(fmt.Sprintf("注意：--global模式，忽略参数-g %v", enableCmd.Group))
		}

		var err error

		if disable {
			err = c.l.PermissionStateManager.GlobalDisableGroupCommand(command)
		} else {
			err = c.l.PermissionStateManager.GlobalEnableGroupCommand(command)
		}
		if err == nil {
			c.textReply("成功")
		} else if err == permission.ErrPermissionExist {
			if disable {
				c.textReply("失败 - 该命令已禁用")
			} else {
				c.textReply("失败 - 该命令已启用")
			}
		}
	} else {
		if c.l.PermissionStateManager.CheckGlobalCommandDisabled(command) {
			c.globalDisabledReply()
			return
		}

		groupCode := enableCmd.Group
		if err := c.checkGroupCode(groupCode); err != nil {
			c.textReply(err.Error())
			return
		}

		log = log.WithFields(localutils.GroupLogFields(groupCode)).
			WithField("global", enableCmd.Global).
			WithField("targetCommand", enableCmd.Command)

		IEnable(c.NewMessageContext(log), groupCode, command, disable)
	}
}

func (c *LspPrivateCommand) GrantCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	var grantCmd struct {
		Group   int64  `optional:"" short:"g" help:"要操作的QQ群号码"`
		Command string `required:"" short:"c" xor:"1" help:"命令名"`
		Role    string `required:"" short:"r" xor:"1" enum:"Admin,GroupAdmin" help:"Admin / GroupAdmin"`
		Delete  bool   `short:"d" help:"删除模式，执行删除权限操作"`
		Target  int64  `arg:"" help:"目标qq号"`
	}
	_, output := c.parseCommandSyntax(&grantCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	groupCode := grantCmd.Group

	grantFrom := c.uin()
	grantTo := grantCmd.Target
	if grantCmd.Command == "" && grantCmd.Role == "" {
		log.Errorf("command and role both empty")
		c.textReply("参数错误 - 必须指定-c / -r")
		return
	}

	del := grantCmd.Delete
	log = log.WithField("grantFrom", grantFrom).WithField("grantTo", grantTo).WithField("delete", del)

	if grantCmd.Command != "" {
		if err := c.checkGroupCode(groupCode); err != nil {
			c.textReply(err.Error())
			return
		}
		log = log.WithFields(localutils.GroupLogFields(groupCode))
		IGrantCmd(c.NewMessageContext(log), groupCode, grantCmd.Command, grantTo, del)
	} else if grantCmd.Role != "" {
		role := permission.NewRoleFromString(grantCmd.Role)
		if role != permission.Admin {
			if err := c.checkGroupCode(groupCode); err != nil {
				c.textReply(err.Error())
				return
			}
		}
		log = log.WithFields(localutils.GroupLogFields(groupCode))
		IGrantRole(c.NewMessageContext(log), groupCode, role, grantTo, del)
	}
}

func (c *LspPrivateCommand) BlockCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var blockCmd struct {
		Uin    int64 `arg:"" required:"" help:"要block的qq号或者qq群"`
		Days   int   `optional:"" help:"要block的天数，默认是永久"`
		Delete bool  `optional:"" short:"d" help:"取消block"`
		Force  bool  `optional:"" short:"f" help:"目标无法查询时仍强制block"`
	}

	_, output := c.parseCommandSyntax(&blockCmd, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	if blockCmd.Uin == c.uin() {
		log.Errorf("can not block yourself")
		c.textReply("失败 - 不能block自己")
		return
	}

	if blockCmd.Uin <= 0 {
		log.Errorf("invalid block target: %d", blockCmd.Uin)
		c.textReply("失败 - QQ号或QQ群号必须是正整数")
		return
	}

	if blockCmd.Days < 0 {
		log.Errorf("invalid block days: %d", blockCmd.Days)
		c.textReply("失败 - block天数不能小于0")
		return
	}

	log = log.WithField("TargetUin", blockCmd.Uin).
		WithField("Days", blockCmd.Days).
		WithField("Delete", blockCmd.Delete).
		WithField("Force", blockCmd.Force)

	name, targetType, err := c.resolveBlockTarget(blockCmd.Uin, !blockCmd.Delete)
	if err != nil && !blockCmd.Delete && !blockCmd.Force {
		log = log.WithField("TargetName", name).WithField("TargetType", targetType)
		log.Errorf("block目标实时查询失败，拒绝写入无效记录 - %v", err)
		c.textReplyF("失败 - 未找到QQ或QQ群<%v>；如需预先屏蔽尚不可查询的目标，请增加-f参数", blockCmd.Uin)
		return
	}
	if name == "" {
		name = fmt.Sprintf("目标 %d", blockCmd.Uin)
	}
	log = log.WithField("TargetName", name).WithField("TargetType", targetType)

	if !blockCmd.Delete {
		if targetType == "group" {
			err = c.l.PermissionStateManager.BlockGroup(blockCmd.Uin, time.Duration(blockCmd.Days)*time.Hour*24)
		} else {
			err = c.l.PermissionStateManager.AddBlockList(blockCmd.Uin, time.Duration(blockCmd.Days)*time.Hour*24)
		}
		if err == nil {
			log.Info("blocked")
			c.textReplyF("成功 - %v", name)
		} else if err == localdb.ErrKeyExist {
			log.Errorf("block failed - duplicate")
			c.textReply("失败 - 已经block过了")
		} else {
			log.Errorf("block failed err %v", err)
			c.textReply("失败 - 内部错误")
		}
	} else {
		if targetType == "group" {
			err = c.l.PermissionStateManager.UnblockGroup(blockCmd.Uin)
		} else {
			err = c.l.PermissionStateManager.DeleteBlockList(blockCmd.Uin)
		}
		if err == nil {
			log.Info("unblocked")
			c.textReplyF("成功 - %v", name)
		} else if localdb.IsNotFound(err) {
			log.Errorf("unblock failed - not exist")
			c.textReply("失败 - 该目标未被block")
		} else {
			log.Errorf("unblock failed err %v", err)
			c.textReply("失败 - 内部错误")
		}
	}
}

// resolveBlockTarget 查询 block 目标，返回展示名与目标类型（"group" / "user"）。
// reload 为 true 时先实时刷新群列表；查询失败时类型返回 "unknown"，
// reload 失败返回 error，reload 成功但查不到目标也返回 error。
func (c *LspPrivateCommand) resolveBlockTarget(code int64, reload bool) (string, string, error) {
	if reload {
		err := c.bot.ReloadGroupList()
		if err == nil {
			if gi := c.bot.FindGroup(code); gi != nil {
				return blockGroupDisplayName(gi, code), "group", nil
			}
		}
		if fi := c.bot.FindFriend(code); fi != nil {
			return blockFriendDisplayName(fi, code), "user", nil
		}
		if err != nil {
			return "", "unknown", fmt.Errorf("refresh group list: %w", err)
		}
		return "", "unknown", fmt.Errorf("target %d not found", code)
	}
	if gi := c.bot.FindGroup(code); gi != nil {
		return blockGroupDisplayName(gi, code), "group", nil
	}
	if fi := c.bot.FindFriend(code); fi != nil {
		return blockFriendDisplayName(fi, code), "user", nil
	}
	return "", "unknown", nil
}

// blockGroupDisplayName 返回群展示名：优先群名，其次群组名，都为空时退回群号。
func blockGroupDisplayName(gi *adapter.GroupInfo, code int64) string {
	if gi != nil {
		if name := strings.TrimSpace(gi.Name); name != "" {
			return name
		}
		if name := strings.TrimSpace(gi.GroupName); name != "" {
			return name
		}
	}
	return fmt.Sprintf("群 %d", code)
}

// blockFriendDisplayName 返回好友展示名：优先备注，其次昵称，都为空时退回QQ号。
func blockFriendDisplayName(fi *adapter.FriendInfo, code int64) string {
	if fi != nil {
		if name := strings.TrimSpace(fi.Remark); name != "" {
			return name
		}
		if name := strings.TrimSpace(fi.Nickname); name != "" {
			return name
		}
	}
	return fmt.Sprintf("QQ %d", code)
}

func (c *LspPrivateCommand) LogCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var logCmd struct {
		N       int       `arg:"" optional:"" help:"要查询的行数"`
		Date    time.Time `optional:"" short:"d" format:"2006-01-02" help:"要查询的日期，需要日志保留才能生效"`
		Keyword string    `optional:"" short:"k" help:"要包含的关键字"`
	}

	_, output := c.parseCommandSyntax(&logCmd, c.CommandName())
	if output != "" {
		c.textSend(output)
	}
	if c.exit {
		return
	}

	if logCmd.N == 0 {
		logCmd.N = 10
	}
	if logCmd.Date.IsZero() {
		logCmd.Date = time.Now()
	}
	logName := fmt.Sprintf("%v.log", logCmd.Date.Format("2006-01-02"))
	b, err := os.ReadFile("logs/" + logName)
	if err != nil {
		c.textSend(fmt.Sprintf("失败 - %v", err))
		return
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	if len(logCmd.Keyword) != 0 {
		var filteredLines []string
		for _, line := range lines {
			if strings.Contains(line, logCmd.Keyword) {
				filteredLines = append(filteredLines, line)
			}
		}
		lines = filteredLines
	}

	if logCmd.N > len(lines) {
		logCmd.N = len(lines)
	}

	lines = lines[len(lines)-logCmd.N:]

	if len(lines) == 0 {
		c.textSend("无结果")
	} else {
		c.textSend(strings.Join(lines, "\n"))
	}
}

func (c *LspPrivateCommand) QuitCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var quitCmd struct {
		GroupCode int64 `arg:"" optional:"" help:"要退出的群号"`
		Force     bool  `optional:"" short:"f" help:"强制清除"`
	}

	_, output := c.parseCommandSyntax(&quitCmd, c.CommandName())
	if output != "" {
		c.textSend(output)
	}
	if c.exit {
		return
	}

	if quitCmd.GroupCode == 0 {
		log.Errorf("GroupCode is 0")
		c.textSend("没有指定群号，请输入群号")
		return
	}

	log = log.WithField("TargetGroupCode", quitCmd.GroupCode).WithField("Force", quitCmd.Force)

	// 成品 x64 0xcd0160：FindGroup 前调 HackedBot.ReloadGroupList（+0x4f5）；
	// 失败且未指定 -f 时记录日志并提示后返回，不退出/清理任何数据。
	if err := c.bot.ReloadGroupList(); err != nil {
		if !quitCmd.Force {
			log.WithField("error", err).Error("退出群前实时刷新群列表失败")
			c.textSend("实时查询QQ群列表失败，本次不会退出或清理数据；如确认需要强制清理，请增加-f参数")
			return
		}
	}

	gi := c.bot.FindGroup(quitCmd.GroupCode)
	var displayName string
	if gi == nil {
		displayName = strconv.FormatInt(quitCmd.GroupCode, 10)
	} else {
		displayName = gi.Name
	}
	if gi == nil {
		if quitCmd.Force {
			log.Debugf("没有找到该QQ群，force已启用")
			c.textSend("没有找到该QQ群，请确认bot在群内，但由于指定了-f参数，将强制清除bot在该群的数据")
		} else {
			log.Errorf("没有找到该QQ群，force已禁用")
			c.textSend("没有找到该QQ群，请确认bot在群内，如果要强制清除bot在该群内的数据，请指定-f参数")
			return
		}
	} else {
		if err := c.bot.SetGroupLeave(quitCmd.GroupCode, false); err != nil {
			log.Errorf("退出群【%v】失败: %v", displayName, err)
			c.textSend(fmt.Sprintf("退出群【%v】失败: %v", displayName, err))
			return
		}
		log.Debugf("已退出群【%v】", displayName)
		c.textSend(fmt.Sprintf("已退出群【%v】", displayName))
	}
	c.l.RemoveAllByGroup(quitCmd.GroupCode)
	log.Debugf("已清除群【%v】的数据", displayName)
	c.textSend(fmt.Sprintf("已清除群【%v】的数据", displayName))
}

func (c *LspPrivateCommand) ModeCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var modeCmd struct {
		Mode string `arg:"" optional:"" help:"指定切换模式，支持<公开> <私人> <审核>，默认为公开"`
	}

	_, output := c.parseCommandSyntax(&modeCmd, c.CommandName(), kong.Description("切换BOT模式"), kong.UsageOnError())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	if modeCmd.Mode == "" {
		mode := c.l.LspStateManager.GetCurrentMode()
		log.Infof("当前Mode为%v", mode)
		switch c.l.LspStateManager.GetCurrentMode() {
		case PublicMode:
			c.textReply("当前模式为公开")
		case PrivateMode:
			c.textReply("当前模式为私人")
		case ProtectMode:
			c.textReply("当前模式为审核")
		}
		return
	}

	log = log.WithField("Mode", modeCmd.Mode)

	var err error

	switch modeCmd.Mode {
	case "公开":
		err = c.l.LspStateManager.SetMode(PublicMode)
	case "私人":
		err = c.l.LspStateManager.SetMode(PrivateMode)
	case "审核":
		err = c.l.LspStateManager.SetMode(ProtectMode)
	default:
		log.Errorf("未知的模式")
		c.textSend(fmt.Sprintf("未知的模式【%v】，仅支持<公开> <私人> <审核>，请查看命令文档", modeCmd.Mode))
		return
	}
	if err != nil {
		log.Errorf("切换模式失败 %v", err)
		c.textReply(fmt.Sprintf("切换模式失败 - %v", err))
	} else {
		log.Infof("切换到%v模式", modeCmd.Mode)
		c.textReply(fmt.Sprintf("成功 - 切换到%v模式", modeCmd.Mode))
	}
}

func (c *LspPrivateCommand) GroupRequestCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var groupRequestCmd struct {
		RequestId int64    `arg:"" optional:"" help:"要处理的请求的RequestId"`
		Reject    bool     `optional:"" short:"r" help:"拒绝请求"`
		All       bool     `optional:"" short:"a" help:"处理全部"`
		Message   []string `arg:"" optional:"" help:"拒绝理由"`
	}

	_, output := c.parseCommandSyntax(&groupRequestCmd, c.CommandName(), kong.Description("处理群邀请"), kong.UsageOnError())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	rmsg := strings.Join(groupRequestCmd.Message, " ")

	log = log.WithFields(logrus.Fields{
		"Requestid": groupRequestCmd.RequestId,
		"Reject":    groupRequestCmd.Reject,
		"All":       groupRequestCmd.All,
		"Message":   rmsg,
	})

	if groupRequestCmd.RequestId == 0 {
		requests, err := c.l.LspStateManager.ListGroupInvitedRequest()
		if err != nil {
			log.Errorf("ListGroupInvitedRequest error - %v", err)
			c.textReply(fmt.Sprintf("失败 - %v", err))
			return
		}

		if len(requests) == 0 {
			log.Infof("没有查询到加群邀请")
			c.textReply("没有查询到加群邀请")
			return
		}

		if groupRequestCmd.Reject {
			// 拒绝全部？
			if !groupRequestCmd.All {
				log.Info("拒绝全部加群邀请需要确认")
				c.textReply(fmt.Sprintf("您似乎想要拒绝全部%v个加群邀请，如果确定想这样做，请输入：\n%v --all %v", len(requests), c.GetCmd(), strings.Join(c.GetArgs(), " ")))
				return
			}
			// 拒绝全部！
			log.Info("确认拒绝全部加群邀请")
			for _, req := range requests {
				log.Debugf("正在拒绝%v(%v)的加群%v(%v)邀请", req.InvitorNick, req.InvitorUin, req.GroupName, req.GroupCode)
				c.bot.SolveGroupJoinRequest(req, false, false, rmsg)
				if err := c.l.LspStateManager.DeleteGroupInvitedRequest(req.RequestId); err != nil {
					log.Errorf("DeleteGroupInvitedRequest error %v", err)
				}
			}
			log.Infof("已拒绝%v个加群邀请", len(requests))
			c.textReply(fmt.Sprintf("成功 - 已拒绝全部%v个加群邀请", len(requests)))
			return
		}

		if groupRequestCmd.All {
			// 接受全部！
			for _, req := range requests {
				log.Infof("正在接受%v(%v)的加群%v(%v)邀请", req.InvitorNick, req.InvitorUin, req.GroupName, req.GroupCode)
				c.bot.SolveGroupJoinRequest(req, true, false, "")
				if err := c.l.LspStateManager.DeleteGroupInvitedRequest(req.RequestId); err != nil {
					log.Errorf("DeleteGroupInvitedRequest error %v", err)
				}
				if err := c.l.PermissionStateManager.GrantGroupRole(req.GroupCode, req.InvitorUin, permission.GroupAdmin); err != nil {
					log.Errorf("设置群管理员权限失败 - %v", err)
				}
				if err := c.l.PermissionStateManager.DeleteBlockList(req.GroupCode); err != nil {
					log.Errorf("DeleteBlockList error %v", err)
				}
			}
			log.Infof("已接受%v个加群邀请", len(requests))
			c.textReply(fmt.Sprintf("成功 - 已接受全部%v个加群邀请", len(requests)))
			return
		}

		// 展示加群邀请
		var sb strings.Builder
		for _, req := range requests {
			sb.WriteString(fmt.Sprintf("ID:%v %v(%v) 邀请加入群 %v(%v)\n", req.RequestId, req.InvitorNick, req.InvitorUin, req.GroupName, req.GroupCode))
		}
		log.Infof("查询到%v个加群邀请", len(requests))
		c.textReply(sb.String())
	} else {
		request, err := c.l.LspStateManager.GetGroupInvitedRequest(groupRequestCmd.RequestId)
		if localdb.IsNotFound(err) {
			log.Errorf("处理加群邀请失败 - 未找到该邀请")
			c.textReply(fmt.Sprintf("失败 - 未找到该邀请【%v】", groupRequestCmd.RequestId))
			return
		} else if err != nil {
			log.Errorf("GetGroupInvitedRequest error %v", err)
			c.textReply(fmt.Sprintf("失败 - 内部错误"))
			return
		}
		log := log.WithFields(logrus.Fields{
			"GroupName":   request.GroupName,
			"GroupCode":   request.GroupCode,
			"InvitorUin":  request.InvitorUin,
			"InvitorNick": request.InvitorNick,
		})
		if groupRequestCmd.Reject {
			c.bot.SolveGroupJoinRequest(request, false, false, rmsg)
			log.Info("拒绝加群邀请成功")
			c.textReply(fmt.Sprintf("成功- 已拒绝 %v(%v) 邀请加群 %v(%v)", request.InvitorNick, request.InvitorUin, request.GroupName, request.GroupCode))
		} else {
			c.bot.SolveGroupJoinRequest(request, true, false, "")
			if err := c.l.PermissionStateManager.GrantGroupRole(request.GroupCode, request.InvitorUin, permission.GroupAdmin); err != nil {
				log.Errorf("设置群管理员权限失败 - %v", err)
			}
			if err := c.l.PermissionStateManager.DeleteBlockList(request.GroupCode); err != nil {
				log.Errorf("DeleteBlockList error %v", err)
			}
			log.Info("接受加群请求成功")
			c.textReply(fmt.Sprintf("成功 - 已接受 %v(%v) 邀请加群 %v(%v)", request.InvitorNick, request.InvitorUin, request.GroupName, request.GroupCode))
		}
		if err := c.l.LspStateManager.DeleteGroupInvitedRequest(request.RequestId); err != nil {
			log.Errorf("DeleteGroupInvitedRequest error %v", err)
		}
	}
}

func (c *LspPrivateCommand) FriendRequestCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var friendRequestCmd struct {
		RequestId int64 `arg:"" optional:"" help:"要处理的请求的RequestId"`
		Reject    bool  `optional:"" short:"r" help:"拒绝请求"`
		All       bool  `optional:"" short:"a" help:"处理全部"`
	}

	_, output := c.parseCommandSyntax(&friendRequestCmd, c.CommandName(), kong.Description("处理好友请求"), kong.UsageOnError())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	log = log.WithFields(logrus.Fields{
		"Requestid": friendRequestCmd.RequestId,
		"Reject":    friendRequestCmd.Reject,
		"All":       friendRequestCmd.All,
	})

	if friendRequestCmd.RequestId == 0 {
		requests, err := c.l.LspStateManager.ListNewFriendRequest()
		if err != nil {
			log.Errorf("ListNewFriendRequest error - %v", err)
			c.textReply(fmt.Sprintf("失败 - %v", err))
			return
		}

		if len(requests) == 0 {
			log.Infof("没有查询到好友申请")
			c.textReply("没有查询到好友申请")
			return
		}

		if friendRequestCmd.Reject {
			if !friendRequestCmd.All {
				log.Info("拒绝全部好友申请需要确认")
				c.textReply(fmt.Sprintf("您似乎想要拒绝全部%v个好友申请，如果确定想这样做，请输入：\n%v --all %v", len(requests), c.GetCmd(), strings.Join(c.GetArgs(), " ")))
				return
			}
			log.Info("确认拒绝全部好友申请")
			for _, req := range requests {
				log.Debugf("正在拒绝%v(%v)的好友申请", req.RequesterNick, req.RequesterUin)
				c.bot.SolveFriendRequest(req.Flag, false)
				if err := c.l.LspStateManager.DeleteNewFriendRequest(req.RequestId); err != nil {
					log.Errorf("DeleteNewFriendRequest error %v", err)
				}
			}
			log.Infof("已拒绝%v个好友申请", len(requests))
			c.textReply(fmt.Sprintf("成功 - 已拒绝全部%v个好友申请", len(requests)))
			return
		}

		if friendRequestCmd.All {
			// 接受全部！
			for _, req := range requests {
				log.Debugf("正在接受%v(%v)的好友申请", req.RequesterNick, req.RequesterUin)
				c.bot.SolveFriendRequest(req.Flag, true)
				if err := c.l.LspStateManager.DeleteNewFriendRequest(req.RequestId); err != nil {
					log.Errorf("DeleteNewFriendRequest error %v", err)
				}
			}
			log.Infof("已接受%v个好友申请", len(requests))
			c.textReply(fmt.Sprintf("成功 - 已接受全部%v个好友申请", len(requests)))
			return
		}

		// 展示好友申请
		var sb strings.Builder
		for _, req := range requests {
			sb.WriteString(fmt.Sprintf("ID:%v %v(%v)申请好友\n", req.RequestId, req.RequesterNick, req.RequesterUin))
		}
		log.Infof("查询到%v个好友申请", len(requests))
		c.textReply(sb.String())
	} else {
		request, err := c.l.LspStateManager.GetNewFriendRequest(friendRequestCmd.RequestId)
		if localdb.IsNotFound(err) {
			log.Errorf("处理好友申请失败 - 未找到该好友申请")
			c.textReply(fmt.Sprintf("失败 - 未找到该好友申请【%v】", friendRequestCmd.RequestId))
			return
		} else if err != nil {
			log.Errorf("GetNewFriendRequest error %v", err)
			c.textReply(fmt.Sprintf("失败 - 内部错误"))
			return
		}

		log := log.WithFields(logrus.Fields{
			"RequesterNick": request.RequesterNick,
			"RequesterUin":  request.RequesterUin,
		})

		if friendRequestCmd.Reject {
			c.bot.SolveFriendRequest(request.Flag, false)
			log.Info("拒绝好友申请")
			c.textReply(fmt.Sprintf("成功 - 已拒绝 %v(%v) 的好友申请", request.RequesterNick, request.RequesterUin))
		} else {
			c.bot.SolveFriendRequest(request.Flag, true)
			log.Info("接受好友申请")
			c.textReply(fmt.Sprintf("成功 - 已接受 %v(%v) 的好友申请", request.RequesterNick, request.RequesterUin))
		}
		if err := c.l.LspStateManager.DeleteNewFriendRequest(request.RequestId); err != nil {
			log.Errorf("DeleteNewFriendRequest error %v", err)
		}
	}
}

func (c *LspPrivateCommand) AdminCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	var adminCmd struct {
		Group int64 `optional:"" short:"g" help:"要操作的QQ群号码"`
	}

	_, output := c.parseCommandSyntax(&adminCmd, c.CommandName(), kong.Description("查看当前Admin权限"), kong.UsageOnError())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	if adminCmd.Group == 0 {
		if !c.l.PermissionStateManager.RequireAny(
			permission.AdminRoleRequireOption(c.uin()),
		) {
			c.noPermission()
			return
		}
	} else {
		if !c.l.PermissionStateManager.RequireAny(
			permission.AdminRoleRequireOption(c.uin()),
			permission.GroupAdminRoleRequireOption(adminCmd.Group, c.uin()),
		) {
			c.noPermission()
			return
		}
	}

	var m = mmsg.NewMSG()

	if adminCmd.Group == 0 {
		ids := c.l.PermissionStateManager.ListAdmin()
		if len(ids) == 0 {
			c.textReply("未查询到Admin，如果bot刚刚启动，请稍后重试。")
			return
		}
		m.Textf("当前Admin：")
		var name string
		for _, id := range ids {
			fi := c.bot.FindFriend(id)
			if fi != nil {
				name = fi.Nickname
			} else {
				name = "未知"
			}
			m.Textf("\n%v %v", id, name)
		}
	} else {
		// 成品 AdminCommand x64 0xcd5ce1 call ReloadGroupList；失败走 0xcd5e4d textReply。
		if err := c.bot.ReloadGroupList(); err != nil {
			log.WithError(err).Error("查询GroupAdmin前实时刷新群列表失败")
			c.textReply("实时查询QQ群列表失败，请稍后重试")
			return
		}
		gi := c.bot.FindGroup(adminCmd.Group)
		if gi == nil {
			// 成品 x64 0xcd5fee textReplyF("没有找到QQ群<%v>，请确认bot是否在群内") 后 return。
			c.textReplyF("没有找到QQ群<%v>，请确认bot是否在群内", adminCmd.Group)
			return
		}
		ids := c.l.PermissionStateManager.ListGroupAdmin(adminCmd.Group)
		if len(ids) == 0 {
			m.Text("未查询到GroupAdmin，如果bot刚刚启动，请稍后重试。")
		} else {
			m.Textf("当前GroupAdmin：")
			var name string
			for _, id := range ids {
				// 成品 AdminCommand (x64 0xcd615a 先查群缓存、0xcd61b7 call EnsureGroupMember)：缓存缺失时实时拉取。
				fi := gi.FindMember(id)
				if fi == nil {
					fi, _ = c.bot.EnsureGroupMember(adminCmd.Group, id)
				}
				if fi != nil {
					// 成品 x64 0xcd61e6 取 0x28/0x20(CardName) 非空则 0x10/0x18(Nickname)，即 DisplayName()。
					name = fi.DisplayName()
				} else {
					name = "未知"
				}
				m.Textf("\n%v %v", id, name)
			}
		}
	}
	c.send(m)
}

func (c *LspPrivateCommand) SilenceCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	if !c.l.PermissionStateManager.RequireAny(
		permission.AdminRoleRequireOption(c.uin()),
	) {
		c.noPermission()
		return
	}

	var silenceCmd struct {
		Group  int64 `optional:"" short:"g" help:"要操作的QQ群号码"`
		Delete bool  `optional:"" short:"d" help:"取消设置"`
	}

	_, output := c.parseCommandSyntax(&silenceCmd, c.CommandName(), kong.Description("设置沉默模式"), kong.UsageOnError())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	ISilenceCmd(c.NewMessageContext(log), silenceCmd.Group, silenceCmd.Delete)
}

func (c *LspPrivateCommand) PingCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	_, output := c.parseCommandSyntax(&struct{}{}, c.CommandName(), kong.Description("返回pong"), kong.UsageOnError())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}
	c.sendChain(c.templateMsg("command.private.ping.tmpl", nil))
}

func (c *LspPrivateCommand) HelpCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	_, output := c.parseCommandSyntax(&struct{}{}, c.CommandName(), kong.Description("显示帮助信息"))
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}
	c.sendChain(c.templateMsg("command.private.help.tmpl", nil))
}

func (c *LspPrivateCommand) SysinfoCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	_, output := c.parseCommandSyntax(&struct{}{}, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	if !c.l.PermissionStateManager.RequireAny(permission.AdminRoleRequireOption(c.uin())) {
		c.noPermission()
		return
	}

	m := mmsg.NewMSG()
	m.Textf("当前好友数：%v\n", len(c.bot.GetFriendList()))
	m.Textf("当前群组数：%v\n", len(c.bot.GetGroupList()))
	for index, cm := range concern.ListConcern() {
		_, ids, ctypes, err := cm.GetStateManager().ListConcernState(
			func(groupCode int64, id interface{}, p concern_type.Type) bool {
				return true
			})
		if index > 0 {
			m.Text("\n")
		}
		ids, ctypes, err = cm.GetStateManager().GroupTypeById(ids, ctypes)
		if err != nil {
			m.Textf("当前%v订阅数：获取失败", cm.Site())
		} else {
			m.Textf("当前%v订阅数：%v", cm.Site(), len(ids))
		}
	}
	c.send(m)
}

func (c *LspPrivateCommand) StatusCommand() {
	log := c.DefaultLoggerWithCommand(c.CommandName())
	log.Infof("run %v command", c.CommandName())
	defer func() { log.Infof("%v command end", c.CommandName()) }()

	_, output := c.parseCommandSyntax(&struct{}{}, c.CommandName())
	if output != "" {
		c.textReply(output)
	}
	if c.exit {
		return
	}

	if !c.l.PermissionStateManager.RequireAny(permission.AdminRoleRequireOption(c.uin())) {
		c.noPermission()
		return
	}

	m := mmsg.NewMSG()
	m.Textf("当前好友数：%v\n", len(c.bot.GetFriendList()))
	m.Textf("当前群组数：%v\n", len(c.bot.GetGroupList()))
	for index, cm := range concern.ListConcern() {
		_, ids, ctypes, err := cm.GetStateManager().ListConcernState(
			func(groupCode int64, id interface{}, p concern_type.Type) bool {
				return true
			})
		if index > 0 {
			m.Text("\n")
		}
		ids, ctypes, err = cm.GetStateManager().GroupTypeById(ids, ctypes)
		if err != nil {
			m.Textf("当前%v订阅数：获取失败", cm.Site())
		} else {
			m.Textf("当前%v订阅数：%v", cm.Site(), len(ids))
		}
	}
	c.send(m)
}

func (c *LspPrivateCommand) DebugCheck() bool {
	var ok bool
	if c.debug {
		if sliceutil.Contains(config.GlobalConfig.GetStringSlice("debug.uin"), strconv.FormatInt(c.msg.Sender.UserID, 10)) {
			ok = true
		}
	} else {
		ok = true
	}
	return ok
}

func (c *LspPrivateCommand) DefaultLogger() *logrus.Entry {
	return logger.WithField("Uin", c.uin()).WithField("Name", c.name())
}

func (c *LspPrivateCommand) DefaultLoggerWithCommand(command string) *logrus.Entry {
	return c.DefaultLogger().WithField("Command", command)
}

func (c *LspPrivateCommand) noPermission() *adapter.PrivateMessage {
	return c.textReply("权限不够")
}

func (c *LspPrivateCommand) globalDisabledReply() *adapter.PrivateMessage {
	return c.textReply("无法操作该命令，该命令已被管理员禁用")
}

func (c *LspPrivateCommand) disabledReply() *adapter.PrivateMessage {
	return c.textSend("该命令已被设置为disable，请设置enable后重试")
}

func (c *LspPrivateCommand) notImplReply() *adapter.PrivateMessage {
	return c.textReply("暂未实现，你可以催作者GKD")
}

func (c *LspPrivateCommand) textSend(text string) *adapter.PrivateMessage {
	return c.send(mmsg.NewText(text))
}

func (c *LspPrivateCommand) textReply(text string) *adapter.PrivateMessage {
	// 私聊reply效果不好
	return c.send(mmsg.NewText(text))
}

func (c *LspPrivateCommand) textReplyF(format string, args ...interface{}) *adapter.PrivateMessage {
	return c.send(mmsg.NewTextf(format, args...))
}

func (c *LspPrivateCommand) send(msg *mmsg.MSG) *adapter.PrivateMessage {
	return c.l.APM(c.l.SendMsg(msg, mmsg.NewPrivateTarget(c.uin())))[0]
}

func (c *LspPrivateCommand) sendChain(msg *mmsg.MSG) []*adapter.PrivateMessage {
	return c.l.APM(c.l.SendMsg(msg, mmsg.NewPrivateTarget(c.uin())))
}

func (c *LspPrivateCommand) commonTemplateData() map[string]interface{} {
	return map[string]interface{}{
		"msg":         c.msg,
		"member_code": c.sender().UserID,
		"member_name": c.sender().DisplayName(),
		"command":     CommandMaps,
	}
}

func (c *LspPrivateCommand) templateMsg(name string, data map[string]interface{}) *mmsg.MSG {
	commonData := c.commonTemplateData()
	for k, v := range data {
		commonData[k] = v
	}
	commonData["template_name"] = name
	m, err := template.LoadAndExec(name, commonData)
	if err != nil {
		logger.Errorf("LoadAndExec error %v", err)
		c.textReply(fmt.Sprintf("错误 - %v", err))
		return nil
	}
	return m
}

func (c *LspPrivateCommand) sender() *adapter.SenderInfo {
	return &adapter.SenderInfo{
		UserID:   c.msg.Sender.UserID,
		Uin:      c.msg.Sender.UserID,
		Nickname: c.msg.Sender.Nickname,
	}
}
func (c *LspPrivateCommand) uin() int64 {
	return c.sender().UserID
}

func (c *LspPrivateCommand) name() string {
	return c.sender().DisplayName()
}

func (c *LspPrivateCommand) NewMessageContext(log *logrus.Entry) *MessageContext {
	ctx := NewMessageContext()
	ctx.Target = mmsg.NewPrivateTarget(c.uin())
	ctx.Lsp = c.l
	ctx.Log = log
	ctx.SendFunc = func(m *mmsg.MSG) interface{} {
		return c.send(m)
	}
	ctx.ReplyFunc = ctx.SendFunc
	ctx.NoPermissionReplyFunc = func() interface{} {
		return c.noPermission()
	}
	ctx.DisabledReply = func() interface{} {
		ctx.Log.Debugf("disabled")
		return c.disabledReply()
	}
	ctx.GlobalDisabledReply = func() interface{} {
		ctx.Log.Debugf("global disabled")
		return c.globalDisabledReply()
	}
	ctx.Sender = c.sender()
	return ctx
}

// checkGroupCode 校验群号并做操作前的群/成员前置检查。
//
// 成品 x64 0xcd9c40 callees：HackedBot.ReloadGroupList(+0x158)、FindGroup(+0x40e)、
// permission.(*StateManager).CheckRole(+0x440)、CheckGroupBlocked(+0x469)、
// HackedBot.RefreshGroupMember(+0x4cc)。重建原先缺 ReloadGroupList / RefreshGroupMember，
// 且在非管理员分支先取缓存 FindMember；按成品改为实时刷新后再取成员。
func (c *LspPrivateCommand) checkGroupCode(groupCode int64) error {
	if groupCode <= 0 {
		return fmt.Errorf("没有指定QQ群号码，请使用-g参数指定QQ群，例如对QQ群123456进行操作：%v %v %v", c.GetCmd(), "-g 123456", strings.Join(c.GetArgs(), " "))
	}
	// 成品：ReloadGroupList 失败时 Warn 一条带 error/GroupCode 字段的日志，
	// 并返回常量错误「实时查询QQ群列表失败，请稍后重试」。
	if err := c.bot.ReloadGroupList(); err != nil {
		logger.WithField("error", err).WithField("GroupCode", groupCode).Warn("校验群号前实时刷新群列表失败")
		return fmt.Errorf("实时查询QQ群列表失败，请稍后重试")
	}
	group := c.bot.FindGroup(groupCode)
	if group == nil {
		return fmt.Errorf("没有找到QQ群<%v>，请确认bot是否在群内", groupCode)
	}
	if !c.l.PermissionStateManager.CheckRole(c.uin(), permission.Admin) {
		if c.l.PermissionStateManager.CheckGroupBlocked(groupCode) {
			return fmt.Errorf("")
		}
		member, err := c.bot.RefreshGroupMember(groupCode, c.uin())
		if err != nil || member == nil {
			return fmt.Errorf("没有在QQ群<%v>内找到您，请确认您是否在群内", groupCode)
		}
	}
	return nil
}
