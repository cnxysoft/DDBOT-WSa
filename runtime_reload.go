package DDBOT

import (
	"fmt"
	"strings"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter/qqofficial"
	"github.com/cnxysoft/DDBOT-WSa/admin"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/acfun"
	"github.com/cnxysoft/DDBOT-WSa/lsp/bilibili"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/douyin"
	"github.com/cnxysoft/DDBOT-WSa/lsp/telegram"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/cnxysoft/DDBOT-WSa/lsp/twitcasting"
	"github.com/cnxysoft/DDBOT-WSa/lsp/twitch"
	"github.com/cnxysoft/DDBOT-WSa/lsp/twitter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
	"github.com/cnxysoft/DDBOT-WSa/lsp/xhs"
)

type reloadParticipant struct {
	name    string
	prepare func(*config.Snapshot, *config.Snapshot) (func(), func(), error)
}

// prepareResources is all-or-nothing before file persistence. Reverse-order
// cleanup releases candidate listeners, locks and workers when any owner fails.
func prepareResources(previous, next *config.Snapshot, participants []reloadParticipant) (func(), func(), error) {
	var commits, aborts []func()
	abort := func() {
		for i := len(aborts) - 1; i >= 0; i-- {
			if aborts[i] != nil {
				aborts[i]()
			}
		}
	}
	for _, participant := range participants {
		commit, cleanup, err := participant.prepare(previous, next)
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			abort()
			// HTTP/network errors can contain credential-bearing URLs. Keep the
			// public failure tied to the owner without returning remote content.
			return nil, nil, fmt.Errorf("%s更新失败，配置未应用；请检查配置、凭据和连接后重试", participant.name)
		}
		commits, aborts = append(commits, commit), append(aborts, cleanup)
	}
	return func() {
		for _, commit := range commits {
			if commit != nil {
				commit()
			}
		}
	}, abort, nil
}

func installResourceReload() {
	config.GlobalConfig.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		return prepareResources(previous, next, []reloadParticipant{
			{"代理池", lsp.PrepareProxyReload},
			{"图片池", lsp.Instance.PrepareImageReload},
			{"模板或外部数据库", template.PrepareRuntimeReload},
			{"Telegram", telegram.PrepareRuntimeReload},
			{"B站凭据", bilibili.PrepareRuntimeReload},
			{"ACFun凭据", acfun.PrepareRuntimeReload},
			{"抖音凭据", douyin.PrepareRuntimeReload},
			{"小红书凭据", xhs.PrepareRuntimeReload},
			{"Twitch凭据", twitch.PrepareRuntimeReload},
			{"TwitCasting凭据", twitcasting.PrepareRuntimeReload},
			{"Twitter会话", twitter.PrepareRuntimeReload},
			{"微博会话", weibo.PrepareRuntimeReload},
			{"WebUI监听", admin.PrepareRuntimeReload},
			// Connections are last: all earlier fallible preparation has finished
			// before the brief disconnection of the active adapter.
			{"Bot连接（协议、QQ AppID及状态路径须保持一致）", prepareBotConnection},
		})
	})
	cfg.EnableResourceHotReload()
	bilibili.SetTransactionalReloadEnabled()
	weibo.SetTransactionalReloadEnabled()
}

func prepareBotConnection(previous, next *config.Snapshot) (func(), func(), error) {
	if next.GetString("adapter.mode") != "qqofficial" {
		return bot.PrepareAdapterReload(previous, next)
	}
	identity := strings.TrimSpace(qqofficial.GetConfigFrom(next).AppID)
	if identity == "" {
		return bot.PrepareAdapterReload(previous, next)
	}
	commitIdentity, abortIdentity, err := lsp.PrepareBotIdentity(qqofficial.IdentityPrefix + identity)
	if err != nil {
		return nil, nil, err
	}
	commit, abort, err := bot.PrepareAdapterReload(previous, next)
	if err != nil {
		if abortIdentity != nil {
			abortIdentity()
		}
		return nil, nil, err
	}
	return func() {
			if commitIdentity != nil {
				commitIdentity()
			}
			if commit != nil {
				commit()
			}
		}, func() {
			if abort != nil {
				abort()
			}
			if abortIdentity != nil {
				abortIdentity()
			}
		}, nil
}
