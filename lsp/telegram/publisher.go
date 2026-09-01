package telegram

import (
	"encoding/base64"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	log         = logrus.WithField("module", "telegram")
	initOnce    sync.Once
	bot         *tgbotapi.BotAPI
	enabled     bool
	initErr     error
	globalChats []int64 // independent telegram chat ids
)

// recvOnce ensures we only start one receiving loop
var recvOnce sync.Once

// Publish sends MSG to globally configured Telegram chats (independent of QQ).
func Publish(m *mmsg.MSG) {
	if m == nil {
		return
	}
	if !ensureInit() {
		return
	}
	if len(globalChats) == 0 {
		return
	}
	// Convert DDBOT MSG to SendingMessage list
	sms := m.ToMessage(mmsg.NewGroupTarget(0))
	for _, chatID := range globalChats {
		for _, sm := range sms {
			go sendToTelegram(chatID, sm)
		}
	}
}

func ensureInit() bool {
	initOnce.Do(func() {
		enabled = config.GlobalConfig.GetBool("telegram.enable")
		if !enabled {
			return
		}
		token := config.GlobalConfig.GetString("telegram.token")
		if token == "" {
			initErr = Err("telegram.token is empty")
			return
		}
		// Parse global independent chats: telegram.chats: ["-1002003004005", "-1009998887777"]
		globalChats = nil
		for _, s := range config.GlobalConfig.GetStringSlice("telegram.chats") {
			id := parseInt64(strings.TrimSpace(s))
			if id != 0 {
				globalChats = append(globalChats, id)
			}
		}

		// Build tuned HTTP client (with or without proxy)
		httpClient := buildTelegramHTTPClient()
		// Determine API endpoint
		endpoint := config.GlobalConfig.GetString("telegram.endpoint")
		if endpoint == "" {
			endpoint = tgbotapi.APIEndpoint
		}
		// Create bot with explicit endpoint and client
		b, err := tgbotapi.NewBotAPIWithClient(token, endpoint, httpClient)
		if err != nil {
			initErr = err
			return
		}
		bot = b
		log.Infof("telegram bot authorized as %s", bot.Self.UserName)
	})
	if !enabled || initErr != nil || bot == nil {
		if initErr != nil {
			log.WithError(initErr).Error("telegram init failed")
		}
		return false
	}
	return true
}

// reinitTelegram re-creates the Telegram bot client using current config.
func reinitTelegram() error {
	if !config.GlobalConfig.GetBool("telegram.enable") {
		return Err("telegram disabled")
	}
	token := config.GlobalConfig.GetString("telegram.token")
	if token == "" {
		return Err("telegram.token is empty")
	}
	httpClient := buildTelegramHTTPClient()
	endpoint := config.GlobalConfig.GetString("telegram.endpoint")
	if endpoint == "" {
		endpoint = tgbotapi.APIEndpoint
	}
	b, err := tgbotapi.NewBotAPIWithClient(token, endpoint, httpClient)
	if err != nil {
		return err
	}
	bot = b
	log.Infof("telegram bot re-authorized as %s", bot.Self.UserName)
	return nil
}

// SendToChat sends the given MSG to a specific Telegram chat.
// It converts the MSG into one or more SendingMessage chunks and streams them out.
func SendToChat(chatID int64, m *mmsg.MSG) {
	if m == nil {
		return
	}
	if !ensureInit() {
		return
	}
	// Use group target 0 when building messages (no QQ routing semantics)
	sms := m.ToMessage(mmsg.NewGroupTarget(0))
	for _, sm := range sms {
		sendToTelegram(chatID, sm)
	}
}

// StartReceiving begins a long-polling loop delivering plain-text Telegram messages
// to the provided callback. It is safe to call multiple times; the loop will start once.
func StartReceiving(onText func(chatID int64, fromID int64, text string)) {
	if onText == nil {
		return
	}
	if !ensureInit() {
		return
	}
	recvOnce.Do(func() {
		go func() {
			log.Info("telegram receiving loop started")
			var offset int = 0
			var backoff time.Duration = 3 * time.Second
			const maxBackoff = 60 * time.Second
			consecutiveErrs := 0
			for {
				u := tgbotapi.NewUpdate(offset)
				u.Timeout = 60
				u.AllowedUpdates = []string{"message", "edited_message", "channel_post"}
				updates, err := bot.GetUpdates(u)
				if err != nil {
					consecutiveErrs++
					log.WithError(err).
						WithField("offset", offset).
						WithField("backoff", backoff.String()).
						WithField("consecutive", consecutiveErrs).
						Warn("telegram getUpdates failed; retrying")
					time.Sleep(backoff)
					if backoff < maxBackoff {
						backoff *= 2
						if backoff > maxBackoff {
							backoff = maxBackoff
						}
					}
					if consecutiveErrs%5 == 0 {
						if err := reinitTelegram(); err != nil {
							log.WithError(err).Warn("telegram reinit failed")
						} else {
							log.Info("telegram reinitialized after errors")
						}
					}
					continue
				}
				if len(updates) > 0 {
					log.WithField("count", len(updates)).Debug("telegram updates received")
				}
				consecutiveErrs = 0
				backoff = 3 * time.Second
				for _, update := range updates {
					if update.UpdateID >= offset {
						offset = update.UpdateID + 1
					}
					if update.Message == nil || update.Message.From == nil {
						continue
					}
					txt := strings.TrimSpace(update.Message.Text)
					if txt == "" {
						continue
					}
					log.WithField("chat", update.Message.Chat.ID).
						WithField("from", update.Message.From.ID).
						WithField("offset", offset).
						Debug("telegram incoming text")
					onText(update.Message.Chat.ID, update.Message.From.ID, txt)
				}
			}
		}()
	})
}

// buildTelegramHTTPClient constructs an *http.Client using the global proxy pool
// (PreferOversea，例如 systemProxy 模式检测到的系统代理)。
// 代理池未初始化或无可用代理时直连。
// 支持 http(s) 与 socks5 代理 URL（http.Transport.Proxy 原生支持 socks5 scheme）。
func buildTelegramHTTPClient() *http.Client {
	// Base transport with conservative timeouts suitable for long-polling
	tr := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 70 * time.Second, // > long-poll Timeout
		ForceAttemptHTTP2:     false,            // more stable with some proxies
	}
	if p, err := proxy_pool.Get(proxy_pool.PreferOversea); err == nil && p != nil {
		proxyURL := p.ProxyString()
		if u, err := url.Parse(proxyURL); err == nil && u.Scheme != "" {
			tr.Proxy = http.ProxyURL(u)
			log.WithField("proxy", proxyURL).Info("telegram 将经由全局代理池(oversea)连接")
		} else {
			log.Warnf("全局代理池返回的代理地址无法解析，telegram 直连: %s", proxyURL)
		}
	} else {
		log.Debug("全局代理池不可用或未配置代理，telegram 直连")
	}
	return &http.Client{Transport: tr}
}

func parseInt64(s string) int64 {
	var n int64
	var sign int64 = 1
	if s == "" {
		return 0
	}
	if s[0] == '-' {
		sign = -1
		s = s[1:]
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return sign * n
}

func sendToTelegram(chatID int64, sm *adapter.SendingMessage) {
	if sm == nil || bot == nil {
		return
	}
	var tb strings.Builder
	var images []*adapter.ImageSegment
	var videos []*adapter.VideoSegment
	for _, e := range sm.Elements {
		switch v := e.(type) {
		case *adapter.TextSegment:
			tb.WriteString(v.Content)
		case *adapter.ImageSegment:
			images = append(images, v)
		case *adapter.VideoSegment:
			if v.Url != "" {
				videos = append(videos, v)
			}
		case *adapter.AtSegment:
			if v.Target == 0 {
				tb.WriteString("@all ")
			} else {
				tb.WriteString("@")
			}
		default:
			// ignore unsupported elements
		}
	}
	caption := tb.String()
	switch {
	case len(videos) > 0:
		sendVideo(chatID, videos[0], caption)
		for i := 1; i < len(videos); i++ {
			sendVideo(chatID, videos[i], "")
		}
		for _, img := range images {
			sendPhoto(chatID, img, "")
		}
	case len(images) > 0:
		sendPhoto(chatID, images[0], caption)
		for i := 1; i < len(images); i++ {
			sendPhoto(chatID, images[i], "")
		}
	default:
		if len(caption) > 0 {
			msg := tgbotapi.NewMessage(chatID, caption)
			if _, err := bot.Send(msg); err != nil {
				log.WithError(err).WithField("chat", chatID).Warn("send text failed")
			}
		}
	}
}

func sendPhoto(chatID int64, img *adapter.ImageSegment, caption string) {
	if img == nil || bot == nil {
		return
	}
	file := strings.TrimSpace(img.File)
	var cfg tgbotapi.PhotoConfig
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		cfg = tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(file))
	} else if strings.HasPrefix(file, "base64://") {
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(file, "base64://"))
		if err != nil {
			log.WithError(err).Warn("decode base64 image failed")
			return
		}
		cfg = tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{Name: "image.jpg", Bytes: b})
	} else if strings.HasPrefix(file, "file://") {
		p := strings.TrimPrefix(file, "file://")
		b, err := httpReadFile(p)
		if err != nil {
			log.WithError(err).Warn("read local image failed")
			return
		}
		cfg = tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{Name: "image.jpg", Bytes: b})
	} else if len(img.Url) > 0 {
		cfg = tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(img.Url))
	} else {
		return
	}
	if len(caption) > 0 {
		cfg.Caption = caption
	}
	if _, err := bot.Send(cfg); err != nil {
		log.WithError(err).WithField("chat", chatID).Warn("send photo failed")
	}
}
func sendVideo(chatID int64, v *adapter.VideoSegment, caption string) {
	if v == nil || bot == nil {
		return
	}
	var cfg tgbotapi.VideoConfig
	file := strings.TrimSpace(v.Url)
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		cfg = tgbotapi.NewVideo(chatID, tgbotapi.FileURL(file))
	} else if strings.HasPrefix(file, "base64://") {
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(file, "base64://"))
		if err != nil {
			log.WithError(err).Warn("decode base64 video failed")
			return
		}
		cfg = tgbotapi.NewVideo(chatID, tgbotapi.FileBytes{Name: "video.mp4", Bytes: b})
	} else if strings.HasPrefix(file, "file://") {
		p := strings.TrimPrefix(file, "file://")
		b, err := httpReadFile(p)
		if err != nil {
			log.WithError(err).Warn("read local video failed")
			return
		}
		cfg = tgbotapi.NewVideo(chatID, tgbotapi.FileBytes{Name: "video.mp4", Bytes: b})
	} else {
		return
	}
	if len(caption) > 0 {
		cfg.Caption = caption
	}
	if _, err := bot.Send(cfg); err != nil {
		log.WithError(err).WithField("chat", chatID).Warn("send video failed")
	}
}

// httpReadFile isolates reading local files (can be extended for sandbox/allowlist)
func httpReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Err lightweight error
type Err string

func (e Err) Error() string { return string(e) }
