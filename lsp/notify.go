package lsp

import (
	"context"
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
	l.wg.Add(1)
	defer l.wg.Done()
	for {
		if !l.consumeConcernNotify() {
			return
		}
		// panic 后在同一 goroutine 内退避重启：
		// 1) 避免 recover 中 go spawn 导致的 WaitGroup Add/Done 竞态
		// 2) 持续性 panic 源不会变成无间隔热循环
		time.Sleep(time.Second)
	}
}

// consumeConcernNotify 执行一轮通知消费循环，返回 false 表示通道已关闭（正常退出），
// 返回 true 表示发生 panic（由调用方决定是否重启）。
func (l *Lsp) consumeConcernNotify() (panicked bool) {
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("stack", string(debug.Stack())).Errorf("concern notify recoverd %v", err)
			panicked = true
		}
	}()
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

			if m == nil {
				logger.Debug("the notification message is empty, skip this push.")
				continue
			}

			// 如果群id < 0, 则认为是TG聊群并忽略推送至QQ
			if inotify.GetGroupCode() < 0 {
				lsptelegram.SendToChat(inotify.GetGroupCode(), m)
				continue
			}

			// atConfig
			var atBeforeHook = cfg.AtBeforeHook(inotify)
			if !atBeforeHook.Pass {
				nLogger.WithField("Reason", atBeforeHook.Reason).Debug("notify @at filtered by hook AtBeforeHook")
			} else {
				// 有@全体成员 或者 @Someone
				var qqadmin = atBeforeHook.Pass &&
					l.PermissionStateManager.CheckGroupAdministrator(inotify.GetGroupCode(), utils.GetBot().GetUin())
				var checkAtAll = qqadmin &&
					cfg.GetGroupConcernAt().CheckAtAll(inotify.Type())
				var atAllMark = checkAtAll &&
					c.GetStateManager().CheckAndSetAtAllMark(inotify.GetGroupCode(), inotify.GetUid())
				nLogger.WithFields(logrus.Fields{
					"qqAdmin":    qqadmin,
					"checkAtAll": checkAtAll,
					"atMark":     atAllMark,
				}).Trace("at_all condition")
				if atBeforeHook.Pass && qqadmin && checkAtAll && atAllMark {
					nLogger = nLogger.WithField("at_all", true)
					newAtAllMsg(m)
				} else {
					ids := cfg.GetGroupConcernAt().GetAtSomeoneList(inotify.Type())
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
							if len(secondRes) != 1 {
								// 预期去掉@全员后应恰好一条结果；异常时记录并跳过，不以 panic 作控制流
								nLogger.WithField("len", len(secondRes)).
									Errorf("INTERNAL: unexpected len(secondRes), skip at-all retry")
								continue
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
							nLogger.Debug("notify atAll failed, try at someone")
							l.SendMsg(newAtIdsMsg(mmsg.NewMSG(), ids), target)
						} else {
							nLogger.Debug("notify atAll failed, at someone not config")
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
