package lsp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/Sora233/MiraiGo-Template/config"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	lsptelegram "github.com/cnxysoft/DDBOT-WSa/lsp/telegram"
	"github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
)

// StartTelegramCommands sets up a Telegram receiving loop that parses commands
// and routes them into existing I* handlers (watch/unwatch/list/enable/disable...).
// It respects configuration under `telegram.commands.*`.
func (l *Lsp) StartTelegramCommands() {
	if !config.GlobalConfig.GetBool("telegram.enable") {
		return
	}
	// Commands are enabled by default when telegram is enabled

	// Start receiving text messages from Telegram
	lsptelegram.StartReceiving(func(chatID int64, fromID int64, text string) {
		logger.WithField("tg_chat", chatID).WithField("tg_from", fromID).Info("telegram text received")
		// Build a TG-namespaced Lsp (no struct copy to avoid copying locks)
		tgL := &Lsp{
			PermissionStateManager: permission.NewTgStateManager(),
			LspStateManager:        l.LspStateManager,
		}
		// Parse command token and args
		cmd, args := parseTGLine(text)
		if cmd == "" {
			return
		}
		logger.WithField("tg_chat", chatID).WithField("tg_from", fromID).WithField("cmd", cmd).Info("telegram command parsed")
		// Build context: treat Telegram user ID as operator UIN
		senderUin := fromID
		// Default group: in Telegram 群聊/频道可省略 -g，默认使用当前聊天ID；私聊需显式 -g
		defaultGroup := tgDefaultGroup(chatID)

		switch strings.ToLower(cmd) {
		case "ping":
			lsptelegram.SendToChat(chatID, mmsg.NewText("pong"))
			return
		case "help":
			lsptelegram.SendToChat(chatID, mmsg.NewText("可用命令：/whosyourdaddy /list /watch /unwatch /enable /disable /grant /config /silence /abnormal /clean /noupdate /resubscribe /help /ping\n说明：在群聊中可省略 -g；站点默认 bilibili，仅在其他平台时使用 -s\n示例：/watch -s bilibili -t live 123456\n/resubscribe -g <群号> - 一键重新订阅该群的所有微博用户"))
			return
		case "whosyourdaddy":
			c := tgL.newTGContext(chatID, fromID, senderUin, 0)
			if tgL.PermissionStateManager.CheckRole(senderUin, permission.Admin) {
				c.TextReply("您已经是管理员了，请不要重复使用此命令。")
				return
			}
			if tgL.PermissionStateManager.CheckNoAdmin() {
				if err := tgL.PermissionStateManager.GrantRole(senderUin, permission.Admin); err != nil {
					c.TextReply("失败 - 内部错误")
				} else {
					c.TextReply("成功 - 您已成为bot管理员")
				}
			} else {
				c.TextReply("失败 - 该bot不属于你！")
			}
			return
		case "list":
			group, site, _ := parseFlags(args, defaultGroup)
			if site == "" {
				site = "bilibili"
			}
			if group == 0 {
				lsptelegram.SendToChat(chatID, mmsg.NewText("请先使用 -g <群号> 指定操作群；本聊天后续可省略 -g"))
				return
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			IList(c, group, site)
			return
		case "watch", "unwatch":
			group, site, typ := parseFlags(args, defaultGroup)
			id := firstNonFlag(args)
			if group == 0 || id == "" {
				lsptelegram.SendToChat(chatID, mmsg.NewText("用法: /watch -g <群号> -s <站点> -t <类型> <ID>"))
				return
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			if site == "" {
				site = "bilibili"
			}
			normSite, wt, err := NewRuntime(tgL).ParseRawSiteAndType(site, typ)
			if err != nil {
				c.TextReply("参数错误 - " + err.Error())
				return
			}
			IWatch(c, group, id, normSite, wt, strings.ToLower(cmd) == "unwatch")
			return
		case "enable", "disable":
			group, _, _ := parseFlags(args, defaultGroup)
			targetCmd := firstNonFlag(args)
			if group == 0 || targetCmd == "" {
				lsptelegram.SendToChat(chatID, mmsg.NewText("用法: /enable -g <群号> <命令名>"))
				return
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			IEnable(c, group, targetCmd, strings.ToLower(cmd) == "disable")
			return
		case "grant":
			// /grant -g <group> (-c <command> | -r <Admin|GroupAdmin>) [-d] <targetUin>
			group, _, _ := parseFlags(args, defaultGroup)
			var del bool
			var role, command string
			var target int64
			for i := 0; i < len(args); i++ {
				a := args[i]
				switch a {
				case "-d", "--delete":
					del = true
				case "-r":
					if i+1 < len(args) {
						role = args[i+1]
						i++
					}
				case "-c":
					if i+1 < len(args) {
						command = args[i+1]
						i++
					}
				default:
					if !strings.HasPrefix(a, "-") && target == 0 {
						if v, err := strconv.ParseInt(a, 10, 64); err == nil {
							target = v
						}
					}
				}
			}
			if group == 0 || target == 0 || (role == "" && command == "") {
				lsptelegram.SendToChat(chatID, mmsg.NewText("用法: /grant -g <群号> (-c <命令> | -r <Admin|GroupAdmin>) [-d] <目标QQ>"))
				return
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			if command != "" {
				IGrantCmd(c, group, command, target, del)
			} else if role != "" {
				r := permission.NewRoleFromString(role)
				IGrantRole(c, group, r, target, del)
			}
			return
		case "silence":
			// /silence -g <group> [-d]
			group, _, _ := parseFlags(args, defaultGroup)
			del := false
			for _, a := range args {
				if a == "-d" || a == "--delete" {
					del = true
					break
				}
			}
			if group == 0 {
				lsptelegram.SendToChat(chatID, mmsg.NewText("用法: /silence -g <群号> [-d]"))
				return
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			ISilenceCmd(c, group, del)
			return
		case "config":
			// /config -g <group> [-s <site>] <subcmd> ...
			group, site, _ := parseFlags(args, defaultGroup)
			if group == 0 {
				lsptelegram.SendToChat(chatID, mmsg.NewText("用法: /config -g <群号> [-s <站点>] <子命令> ..."))
				return
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			if len(args) == 0 {
				c.TextReply("参数错误")
				return
			}
			// find first non-flag index
			idx := 0
			for idx < len(args) && strings.HasPrefix(args[idx], "-") { // skip -g/-s/-t tokens
				if (args[idx] == "-g" || args[idx] == "-s" || args[idx] == "-t") && idx+1 < len(args) {
					idx += 2
				} else {
					idx++
				}
			}
			if idx >= len(args) {
				c.TextReply("参数错误")
				return
			}
			sub := strings.ToLower(args[idx])
			rest := args[idx+1:]
			if site == "" {
				site = "bilibili"
			}
			switch sub {
			case "at":
				// at <id> <add|remove|clear|show> [qq...]
				if len(rest) < 2 {
					c.TextReply("用法: /config -g <群号> at <id> <add|remove|clear|show> [qq]")
					return
				}
				id := rest[0]
				action := strings.ToLower(rest[1])
				normSite, ctype, err := NewRuntime(tgL).ParseRawSiteAndType(site, "live")
				if err != nil {
					c.TextReply("参数错误 - " + err.Error())
					return
				}
				var qqs []int64
				if action == "add" || action == "remove" {
					for _, s := range rest[2:] {
						if v, err := strconv.ParseInt(s, 10, 64); err == nil {
							qqs = append(qqs, v)
						}
					}
				}
				IConfigAtCmd(c, group, id, normSite, ctype, action, qqs)
			case "at_all":
				// at_all <id> <on|off>
				if len(rest) < 2 {
					c.TextReply("用法: /config -g <群号> at_all <id> <on|off>")
					return
				}
				id := rest[0]
				sw := strings.ToLower(rest[1])
				normSite, ctype, err := NewRuntime(tgL).ParseRawSiteAndType(site, "live")
				if err != nil {
					c.TextReply("参数错误 - " + err.Error())
					return
				}
				on := sw == "on"
				IConfigAtAllCmd(c, group, id, normSite, ctype, on)
			case "title_notify":
				if len(rest) < 2 {
					c.TextReply("用法: /config -g <群号> title_notify <id> <on|off>")
					return
				}
				id := rest[0]
				sw := strings.ToLower(rest[1])
				normSite, ctype, err := NewRuntime(tgL).ParseRawSiteAndType(site, "live")
				if err != nil {
					c.TextReply("参数错误 - " + err.Error())
					return
				}
				on := sw == "on"
				IConfigTitleNotifyCmd(c, group, id, normSite, ctype, on)
			case "offline_notify":
				if len(rest) < 2 {
					c.TextReply("用法: /config -g <群号> offline_notify <id> <on|off>")
					return
				}
				id := rest[0]
				sw := strings.ToLower(rest[1])
				normSite, ctype, err := NewRuntime(tgL).ParseRawSiteAndType(site, "live")
				if err != nil {
					c.TextReply("参数错误 - " + err.Error())
					return
				}
				on := sw == "on"
				IConfigOfflineNotifyCmd(c, group, id, normSite, ctype, on)
			case "filter":
				if len(rest) < 2 {
					c.TextReply("用法: /config -g <群号> filter <type|not_type|text|clear|show> ...")
					return
				}
				fsub := strings.ToLower(rest[0])
				normSite, ctype, err := NewRuntime(tgL).ParseRawSiteAndType(site, "news")
				if err != nil {
					c.TextReply("参数错误 - " + err.Error())
					return
				}
				switch fsub {
				case "type":
					if len(rest) < 2 {
						c.TextReply("用法: /config -g <群号> filter type <id> [types...]")
						return
					}
					id := rest[1]
					types := rest[2:]
					IConfigFilterCmdType(c, group, id, normSite, ctype, types)
				case "not_type":
					if len(rest) < 2 {
						c.TextReply("用法: /config -g <群号> filter not_type <id> [types...]")
						return
					}
					id := rest[1]
					types := rest[2:]
					IConfigFilterCmdNotType(c, group, id, normSite, ctype, types)
				case "text":
					if len(rest) < 2 {
						c.TextReply("用法: /config -g <群号> filter text <id> [keywords...]")
						return
					}
					id := rest[1]
					keywords := rest[2:]
					IConfigFilterCmdText(c, group, id, normSite, ctype, keywords)
				case "not_text":
					if len(rest) < 2 {
						c.TextReply("用法: /config -g <群号> filter text <id> [keywords...]")
						return
					}
					id := rest[1]
					keywords := rest[2:]
					IConfigFilterCmdNotText(c, group, id, normSite, ctype, keywords)
				case "clear":
					if len(rest) < 2 {
						c.TextReply("用法: /config -g <群号> filter clear <id>")
						return
					}
					id := rest[1]
					IConfigFilterCmdClear(c, group, id, normSite, ctype)
				case "show":
					if len(rest) < 2 {
						c.TextReply("用法: /config -g <群号> filter show <id>")
						return
					}
					id := rest[1]
					IConfigFilterCmdShow(c, group, id, normSite, ctype)
				default:
					c.TextReply("未知的filter子命令")
				}
			default:
				c.TextReply("未知的config子命令")
			}
			return
		case "abnormal":
			c := tgL.newTGContext(chatID, fromID, senderUin, 0)
			IAbnormalConcernCheck(c)
			return
		case "clean":
			// /clean [-a] [-g <group>] [-s <site>] [-t <type>]
			var abnormal bool
			group, site, typ := parseFlags(args, defaultGroup)
			for _, a := range args {
				if a == "-a" || a == "--abnormal" {
					abnormal = true
					break
				}
			}
			var groups []int64
			if group != 0 {
				groups = []int64{group}
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			ICleanConcern(c, abnormal, groups, site, typ)
			return
		case "noupdate", "no_update":
			// /noupdate [-d]
			del := false
			for _, a := range args {
				if a == "-d" || a == "--delete" {
					del = true
					break
				}
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, 0)
			key := localdb.DDBotNoUpdateKey(senderUin)
			var err error
			if del {
				_, err = localdb.Delete(key, localdb.IgnoreNotFoundOpt())
				if err == nil {
					c.TextReply("成功 - 您将接收到更新消息")
				}
			} else {
				err = localdb.Set(key, "")
				if err == nil {
					c.TextReply("成功 - 您不再接受更新消息")
				}
			}
			if err != nil {
				c.TextReply("失败 - " + err.Error())
			}
			return
		case "resubscribe", "重新订阅":
			// /resubscribe -g <群号>
			group, _, _ := parseFlags(args, defaultGroup)
			if group == 0 {
				lsptelegram.SendToChat(chatID, mmsg.NewText("用法：/resubscribe -g <群号>"))
				return
			}
			c := tgL.newTGContext(chatID, fromID, senderUin, group)
			// 检查权限：需要管理员权限
			if !tgL.PermissionStateManager.RequireAny(
				permission.AdminRoleRequireOption(senderUin),
			) {
				c.TextReply("权限不足")
				return
			}

			// 调用微博 Concern 的一键重新订阅功能
			weiboConcern, err := concern.GetConcernBySiteAndType("weibo", weibo.News)
			if err != nil {
				c.TextReply(fmt.Sprintf("失败 - %v", err))
				return
			}

			wc, ok := weiboConcern.(*weibo.Concern)
			if !ok {
				c.TextReply("失败 - 类型转换错误")
				return
			}

			count, err := wc.ResubscribeAll(c, group)
			if err != nil {
				c.TextReply(fmt.Sprintf("失败 - %v", err))
				return
			}

			c.TextSend(fmt.Sprintf("成功 - 已重新订阅 %d 个微博用户", count))
			return
		default:
			// 仅当用户使用了可识别的前缀（配置前缀或以'/'开头）时才回复未知命令，避免在群内刷屏
			first := ""
			fields := strings.Fields(strings.TrimSpace(text))
			if len(fields) > 0 {
				first = fields[0]
			}
			pref := strings.TrimSpace(config.GlobalConfig.GetString("bot.commandPrefix"))
			shouldNotify := false
			if pref != "" {
				shouldNotify = strings.HasPrefix(first, pref)
			} else {
				shouldNotify = strings.HasPrefix(first, "/")
			}
			if shouldNotify {
				lsptelegram.SendToChat(chatID, mmsg.NewText("未知命令: "+cmd))
			}
			return
		}
	})
}

// newTGContext builds a MessageContext that replies to Telegram chat.
func (l *Lsp) newTGContext(chatID, fromID, senderUin, groupCode int64) *MessageContext {
	c := &MessageContext{
		Lsp:    l,
		Log:    logger.WithField("tg_chat", chatID).WithField("tg_from", fromID).WithField("group", groupCode),
		Target: mmsg.NewGroupTarget(groupCode),
		Sender: &message.Sender{Uin: senderUin, Nickname: "tg:" + strconv.FormatInt(fromID, 10)},
	}
	c.ReplyFunc = func(m *mmsg.MSG) interface{} {
		lsptelegram.SendToChat(chatID, m)
		return nil
	}
	c.SendFunc = func(m *mmsg.MSG) interface{} {
		lsptelegram.SendToChat(chatID, m)
		return nil
	}
	c.NoPermissionReplyFunc = func() interface{} { lsptelegram.SendToChat(chatID, mmsg.NewText("失败 - 没有权限")); return nil }
	c.DisabledReply = func() interface{} {
		lsptelegram.SendToChat(chatID, mmsg.NewText("失败 - 该命令已禁用"))
		return nil
	}
	c.GlobalDisabledReply = func() interface{} {
		lsptelegram.SendToChat(chatID, mmsg.NewText("失败 - 管理员已禁用该命令"))
		return nil
	}
	return c
}

// parseTGLine extracts command and args from a Telegram text line.
func parseTGLine(text string) (string, []string) {
	s := strings.TrimSpace(text)
	if s == "" {
		return "", nil
	}
	// Remove prefix if present (default '/') and optional @botname suffix
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", nil
	}
	cmd := parts[0]
	pref := strings.TrimSpace(config.GlobalConfig.GetString("bot.commandPrefix"))
	if pref != "" {
		if !strings.HasPrefix(cmd, pref) {
			return "", nil
		}
		cmd = strings.TrimPrefix(cmd, pref)
	} else {
		// 前缀配置为空时，同时兼容“/cmd”和“cmd”两种形式
		if strings.HasPrefix(cmd, "/") {
			cmd = strings.TrimPrefix(cmd, "/")
		}
	}
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd, parts[1:]
}

// parseFlags parses -g, -s, -t from args
func parseFlags(args []string, defaultGroup int64) (group int64, site string, typ string) {
	group = defaultGroup
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-g" && i+1 < len(args) {
			if v, err := strconv.ParseInt(args[i+1], 10, 64); err == nil {
				group = v
			}
			i++
			continue
		}
		if a == "-s" && i+1 < len(args) {
			site = args[i+1]
			i++
			continue
		}
		if a == "-t" && i+1 < len(args) {
			typ = args[i+1]
			i++
			continue
		}
	}
	return
}

// firstNonFlag returns first arg not starting with '-'
func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// tgDefaultGroup: if Telegram chat is a group/supergroup/channel (ID usually negative),
// use chatID as default group code; for private chats return 0 (force -g).
func tgDefaultGroup(chatID int64) int64 {
	if chatID < 0 {
		return chatID
	}
	return 0
}
