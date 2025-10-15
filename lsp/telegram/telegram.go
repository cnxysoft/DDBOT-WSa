//go:build ignore
package telegram

import (
    "context"
    "encoding/base64"
    "io/ioutil"
    "net"
    "net/http"
    "strings"
    "sync"

    "github.com/Mrs4s/MiraiGo/message"
    "github.com/Sora233/MiraiGo-Template/config"
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    "github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
    xproxy "golang.org/x/net/proxy"
    "github.com/sirupsen/logrus"
)

var (
	botOnce  sync.Once
{{ ... }}
			sendPhoto(chatID, v)
		case *message.VideoElement:
			flushText(&tb)
			sendVideo(chatID, v)
		case *message.AtElement:
			// best-effort textual mention
			if v.Target == 0 {
				tb.WriteString("@all ")
			} else {
				tb.WriteString("@")
			}
		default:
			// ignore unsupported elements, but keep textual flow
	}
}
func flushText(tb *strings.Builder) {
	if tb.Len() > 0 {
		tb := tb.String()
		tb = strings.TrimSpace(tb.String())
		if len(tb.String()) > 0 {
			if _, err := bot.Send(tgbotapi.NewMessage(chatID, tb)); err != nil {
				logger.WithError(err).WithField("chat", chatID).Warn("send text failed")
			}
		}
		tb.Reset()
	}
}

// buildTelegramHTTPClient constructs an *http.Client honoring telegram.proxy.url
// Supports http(s) proxies and socks5/socks5h proxies.
func buildTelegramHTTPClient() *http.Client {
    proxyURL := config.GlobalConfig.GetString("telegram.proxy.url")
    if proxyURL == "" {
        return nil
    }
    tr := &http.Transport{}
    lower := strings.ToLower(proxyURL)
    if strings.HasPrefix(lower, "socks5://") || strings.HasPrefix(lower, "socks5h://") {
        u, err := url.Parse(proxyURL)
        if err != nil {
            logger.WithError(err).Warn("invalid telegram.proxy.url for socks5")
            return nil
        }
        var auth *xproxy.Auth
        if u.User != nil {
            pass, _ := u.User.Password()
            auth = &xproxy.Auth{User: u.User.Username(), Password: pass}
        }
        d, err := xproxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{})
        if err != nil {
            logger.WithError(err).Warn("failed to init socks5 dialer")
            return nil
        }
        tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
            return d.Dial(network, addr)
        }
    } else {
        u, err := url.Parse(proxyURL)
        if err != nil {
            logger.WithError(err).Warn("invalid telegram.proxy.url")
            return nil
        }
        tr.Proxy = http.ProxyURL(u)
    }
    return &http.Client{Transport: tr}
}

func ensureInit() bool {
    botOnce.Do(func() {
        enabled = config.GlobalConfig.GetBool("telegram.enable")
        if !enabled {
            return
        }
        token := config.GlobalConfig.GetString("telegram.token")
        if token == "" {
            initErr = Err("telegram.token is empty")
            return
        }
        var httpClient *http.Client
        if config.GlobalConfig.GetBool("telegram.proxy.enable") {
            httpClient = buildTelegramHTTPClient()
        }
        var b *tgbotapi.BotAPI
        var err error
        if httpClient != nil {
            b, err = tgbotapi.NewBotAPIWithClient(token, httpClient)
        } else {
            b, err = tgbotapi.NewBotAPI(token)
        }
        if err != nil {
            initErr = err
            return
        }
        if ep := config.GlobalConfig.GetString("telegram.endpoint"); ep != "" {
            b.APIEndpoint = ep
        }
        bot = b
        }
    }
    if _, err := bot.Send(cfg); err != nil {
        logger.WithError(err).WithField("chat", chatID).Warn("send video failed")
    }
}

// Err is a lightweight error helper to avoid importing fmt/errors for a single string
type Err string
func (e Err) Error() string { return string(e) }
