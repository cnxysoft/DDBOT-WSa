package lsp

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	lsptelegram "github.com/cnxysoft/DDBOT-WSa/lsp/telegram"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/sirupsen/logrus"
)

func (l *Lsp) ConcernNotify() {
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("stack", string(debug.Stack())).Errorf("concern notify recoverd %v", err)
			go l.ConcernNotify()
		}
	}()
	l.wg.Add(1)
	defer l.wg.Done()
	for {
		select {
		case _inotify, ok := <-l.concernNotify:
			if !ok {
				return
			}
			if _inotify == nil {
				continue
			}
			var inotify = _inotify
			target := mmsg.NewGroupTarget(inotify.GetGroupCode())
			nLogger := inotify.Logger()

			if l.LspStateManager.IsMuted(inotify.GetGroupCode(), utils.GetBot().GetUin()) &&
				!l.PermissionStateManager.CheckGroupAdministrator(inotify.GetGroupCode(), utils.GetBot().GetUin()) {
				nLogger.Info("BOT群内被禁言，跳过本次推送")
				continue
			}

			c, err := concern.GetConcernBySiteAndType(inotify.Site(), inotify.Type())
			if err != nil {
				nLogger.Errorf("GetConcernBySiteAndType error %v", err)
				continue
			}
			cfg := c.GetStateManager().GetGroupConcernConfig(inotify.GetGroupCode(), inotify.GetUid())
			cfg.NotifyBeforeCallback(inotify)

			// 注意notify可能会缓存MSG
			var m = l.NotifyMessage(inotify).Clone()

			if m == nil || len(m.Elements()) == 0 {
				// 成品 notify.go:63（Info）——模板中止/输出为空，连 @ 一起跳过本次推送。
				nLogger.WithFields(logrus.Fields{
					"notify_stage":     "template_filter",
					"template_empty":   true,
					"at_check_skipped": true,
					"selected":         "none",
				}).Info("模板已中止或输出为空，跳过本次推送（包括所有 @）")
				continue
			}

			// 如果群id < 0, 则认为是TG聊群并忽略推送至QQ
			if inotify.GetGroupCode() < 0 {
				lsptelegram.SendToChat(inotify.GetGroupCode(), m)
				continue
			}

			// atConfig
			//
			// 成品 ConcernNotify @0xcc23c0 的 @全体 判定分层与日志（逐条取自反汇编）：
			//
			//	+0x851  checkAtAllGroupAdministrator(groupCode, uin)   -> (admin, role, err)
			//	+0x10f8 checkAtAllRemaining(inotify)                   -> atAllRemainingDecision
			//	+0x1c5a mmsg.(*MSG).AtAll     (newAtAllMsg 内联)
			//	+0x2ac9 newAtIdsMsg
			//
			// 阶段字段 at_all_stage：before_hook / admin_check / eligibility /
			// remaining_check / selection；模板空跳过用 notify_stage=template_filter。
			var atBeforeHook = cfg.AtBeforeHook(inotify)
			if !atBeforeHook.Pass {
				// 成品 notify.go:82（Info）——AtBeforeHook 未通过，本次不添加任何 @。
				nLogger.WithFields(logrus.Fields{
					"at_all_stage": "before_hook",
					"pass":         atBeforeHook.Pass,
					"reason":       atBeforeHook.Reason,
					"selected":     "none",
				}).Info("@全体逻辑：前置条件未通过，本次不添加任何 @")
			} else {
				var (
					admin     bool
					role      string
					adminErr  error
					decision  atAllRemainingDecision
					ranCheck  bool
					atAllMark bool
				)
				checkAtAll := cfg.GetGroupConcernAt().CheckAtAll(inotify.Type())
				if checkAtAll {
					// 成品 notify.go:88——实时查询 BOT 群角色；失败/完成分别记 Warn/Info。
					admin, role, adminErr = checkAtAllGroupAdministrator(inotify.GetGroupCode(), utils.GetBot().GetUin())
					adminFields := logrus.Fields{
						"at_all_stage":          "admin_check",
						"admin_api":             "get_group_member_info",
						"admin_api_called":      true,
						"admin_no_cache":        true,
						"admin_check_source":    "onebot_realtime",
						"bot_group_admin":       admin,
						"bot_group_member_role": role,
					}
					if adminErr != nil {
						// 成品 notify.go:100（Warn，entry.go:328）
						nLogger.WithFields(adminFields).Warn("@全体逻辑：实时查询机器人群角色失败，禁止使用 @全体")
					} else {
						// 成品 notify.go:103（Info）
						nLogger.WithFields(adminFields).Info("@全体逻辑：实时查询机器人群角色完成")
					}
				}

				// 成品 notify.go:116（Info）——权限与订阅配置检查完成。
				nLogger.WithFields(logrus.Fields{
					"at_all_stage":          "eligibility",
					"bot_group_admin":       admin,
					"bot_group_member_role": role,
					"at_all_configured":     checkAtAll,
					"admin_api_called":      checkAtAll,
					"admin_no_cache":        checkAtAll,
					"will_run_at_all_check": checkAtAll && admin,
				}).Info("@全体逻辑：检查机器人权限和订阅配置")

				if checkAtAll && admin {
					// 成品 notify.go:119——白名单与账号剩余次数判定。
					decision = checkAtAllRemaining(inotify)
					ranCheck = true
					remainingFields := logrus.Fields{
						"at_all_stage":              "remaining_check",
						"remaining_limiter_enabled": decision.RemainingLimiterEnabled,
						"remaining_api":             "get_group_at_all_remain",
						"remaining_api_called":      decision.RemainingLimiterEnabled && !decision.Whitelisted,
						"remaining_scope":           "account",
						"allowed":                   decision.Allowed,
						"whitelisted":               decision.Whitelisted,
						"threshold":                 decision.Threshold,
						"reason":                    decision.Reason,
					}
					if decision.Remaining != nil {
						remainingFields["remaining"] = *decision.Remaining
					}
					switch {
					case !decision.RemainingLimiterEnabled:
						// 成品 notify.go:136（Info）——未配置 atAll，跳过账号剩余次数限制。
						nLogger.WithFields(remainingFields).Info("@全体逻辑：未配置 atAll，跳过账号剩余次数限制")
					case decision.Err == nil:
						// 成品 notify.go:142（Info）——白名单及账号剩余次数判断完成。
						nLogger.WithFields(remainingFields).Info("@全体逻辑：白名单及账号剩余次数判断完成")
					default:
						// 成品 notify.go:139（Warn）——查询失败，禁止 @全体。
						remainingFields["error"] = decision.Err
						nLogger.WithFields(remainingFields).Warn("@全体逻辑：腾讯剩余次数接口查询失败，禁止使用 @全体")
					}
					if decision.Allowed {
						atAllMark = c.GetStateManager().CheckAndSetAtAllMark(inotify.GetGroupCode(), inotify.GetUid())
					}
				}

				// 成品 notify.go:174-181——selection 阶段：给出 reason，选择 @全体 或 @指定成员。
				// reason 字面量：at_all_not_configured @0x10b66fc / bot_admin_query_failed @0x10b7c86 /
				// bot_not_group_admin @0x10b375c / two_hour_at_all_interval @0x10bac25。
				var reason string
				switch {
				case !checkAtAll:
					reason = "at_all_not_configured"
				case adminErr != nil:
					reason = "bot_admin_query_failed"
				case !admin:
					reason = "bot_not_group_admin"
				case ranCheck && decision.Allowed && !atAllMark:
					reason = "two_hour_at_all_interval"
				case ranCheck:
					reason = decision.Reason
				}
				if ranCheck && decision.Allowed && atAllMark {
					// 成品 notify.go:154（Info）——最终选择 @全体成员。
					nLogger.WithFields(logrus.Fields{
						"at_all_stage":    "selection",
						"at_all_mark":     atAllMark,
						"at_all_cooldown": "2h",
						"selected":        "at_all",
						"reason":          reason,
					}).Info("@全体逻辑：最终选择 @全体成员")
					nLogger = nLogger.WithField("at_all", true)
					newAtAllMsg(m)
				} else {
					ids := cfg.GetGroupConcernAt().GetAtSomeoneList(inotify.Type())
					// 成品 notify.go:181（Info）——未使用 @全体，回退 @指定成员。
					nLogger.WithFields(logrus.Fields{
						"at_all_stage":    "selection",
						"at_all_mark":     atAllMark,
						"at_all_cooldown": "2h",
						"selected":        "at_QQ",
						"reason":          reason,
						"at_QQ":           ids,
					}).Info("@全体逻辑：未使用 @全体，执行回退方案")
					nLogger = nLogger.WithField("at_QQ", ids)
					newAtIdsMsg(m, ids)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			if err := l.msgLimit.Acquire(ctx, 1); err != nil {
				cancel()
				nLogger.WithField("Content", msgstringer.AdapterMsgToString(m.Elements())).
					Errorf("BOT负载过高，推送已积压超过一分钟，将舍弃本次推送。")
				continue
			}
			cancel()
			l.notifyWg.Add(1)
			nLogger.Info("notify")
			go func() {
				defer l.notifyWg.Done()
				defer func() {
					l.msgLimit.Release(1)
					if e := recover(); e != nil {
						nLogger.WithField("stack", string(debug.Stack())).
							Errorf("notify panic recovered: %v", e)
					}
				}()
				// 如果 groupCode < 0，则将其视为 Telegram 聊天 ID，并仅发送到 Telegram
				if inotify.GetGroupCode() < 0 {
					lsptelegram.SendToChat(inotify.GetGroupCode(), m)
					cfg.NotifyAfterCallback(inotify, nil)
					return
				}

				msgs := l.AGM(l.SendMsg(m, target))
				if len(msgs) > 0 {
					cfg.NotifyAfterCallback(inotify, msgs[0])
				}

				if atBeforeHook.Pass {
					var atIdsOnce bool
					for _, msg := range msgs {
						if msg.ID == -1 {
							// 检查有没有@全体成员
							e := utils.AdapterMessageFilter(msg.Elements, isAtAllElement)
							if len(e) == 0 {
								continue
							}
							// 2022/09/24 现在@全员不会再作为单独一条消息
							// 有@全体成员的消息应该去掉之后重试
							secondM := mmsg.NewMSGFromGroupMessage(&adapter.GroupMessage{Elements: msg.Elements})
							secondM.Drop(func(e adapter.IMessageElement, _ int) bool {
								return isAtAllElement(e)
							})

							secondRes := l.AGM(l.SendMsg(secondM, target))
							// secondRes一定是一条
							if len(secondRes) != 1 {
								panic(fmt.Sprintf("INTERNAL: len(secondRes) is %v", len(secondRes)))
							}
							if secondRes[0].ID == -1 {
								// 去掉@全员还是发送失败
								continue
							}
							if !atIdsOnce {
								// 去掉@全员之后发送成功，可能是次数到了，尝试@列表
								atIdsOnce = true
							}
						}
					}
					if atIdsOnce {
						ids := cfg.GetGroupConcernAt().GetAtSomeoneList(inotify.Type())
						if len(ids) != 0 {
							nLogger = nLogger.WithField("at_QQ", ids)
							nLogger.Info("@全体逻辑：@全体发送失败，消息已去除 @全体后重发，并尝试 @指定成员")
							l.SendMsg(newAtIdsMsg(mmsg.NewMSG(), ids), target)
						} else {
							nLogger.Info("@全体逻辑：@全体发送失败，消息已去除 @全体后重发，但没有配置 @指定成员")
						}
					}
				}
			}()
		}
	}
}

func (l *Lsp) NotifyMessage(inotify concern.Notify) *mmsg.MSG {
	return inotify.ToMessage()
}

func isAtAllElement(element adapter.IMessageElement) bool {
	at, ok := element.(*adapter.AtSegment)
	return ok && at.Target == 0
}

func newAtAllMsg(m *mmsg.MSG) *mmsg.MSG {
	return m.AtAll(true)
}

func newAtIdsMsg(m *mmsg.MSG, ids []int64) *mmsg.MSG {
	if len(ids) > 0 {
		m.Cut()
		for _, id := range ids {
			m.At(id)
		}
	}
	return m
}
