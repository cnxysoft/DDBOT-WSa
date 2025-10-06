package telegram

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
	xproxy "golang.org/x/net/proxy"
)

var (
	log         = logrus.WithField("module", "telegram")
	initOnce    sync.Once
	bot         *tgbotapi.BotAPI
	enabled     bool
	initErr     error
	chatMap     map[int64][]int64 // qq group code -> telegram chat ids (legacy)
	globalChats []int64           // independent telegram chat ids
)

// recvOnce ensures we only start one receiving loop
var recvOnce sync.Once

// BindGroupToChat dynamically binds a QQ group to a Telegram chat at runtime.
// Subsequent calls to PublishGroup for this group will fan out to the chat.
func BindGroupToChat(groupCode int64, chatID int64) {
	if !ensureInit() {
		return
	}
	if groupCode == 0 || chatID == 0 {
		return
	}
	if chatMap == nil {
		chatMap = make(map[int64][]int64)
	}
	// prevent duplicates
	for _, id := range chatMap[groupCode] {
		if id == chatID {
			return
		}
	}
	chatMap[groupCode] = append(chatMap[groupCode], chatID)
}

// PublishGroup fans out an MSG to mapped Telegram chats for the given QQ groupCode.
// It is safe to call concurrently and will no-op when telegram is disabled or not configured.
func PublishGroup(groupCode int64, m *mmsg.MSG) {
	if m == nil {
		return
	}
	if !ensureInit() {
		return
	}
	chats := chatMap[groupCode]
	if len(chats) == 0 {
		return
	}
	// Convert DDBOT MSG to SendingMessage list and fan out to mapped chats
	sms := m.ToMessage(mmsg.NewGroupTarget(groupCode))
	for _, chatID := range chats {
		for _, sm := range sms {
			go sendToTelegram(chatID, sm)
		}
	}
}

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
		// Parse mapping: telegram.groups: ["<qqGroupCode>:<tgChatID1>,<tgChatID2>"] (legacy)
		chatMap = make(map[int64][]int64)
		for _, entry := range config.GlobalConfig.GetStringSlice("telegram.groups") {
			parts := strings.Split(entry, ":")
			if len(parts) != 2 {
				continue
			}
			qq := parseInt64(parts[0])
			if qq == 0 {
				continue
			}
			var chats []int64
			for _, s := range strings.Split(parts[1], ",") {
				id := parseInt64(strings.TrimSpace(s))
				if id != 0 {
					chats = append(chats, id)
				}
			}
			if len(chats) > 0 {
				chatMap[qq] = chats
			}
		}

		// Parse global independent chats: telegram.chats: ["-1002003004005", "-1009998887777"]
		globalChats = nil
		for _, s := range config.GlobalConfig.GetStringSlice("telegram.chats") {
			id := parseInt64(strings.TrimSpace(s))
			if id != 0 {
				globalChats = append(globalChats, id)
			}
		}

		// Build HTTP client (with or without proxy)
		var httpClient *http.Client
		if config.GlobalConfig.GetBool("telegram.proxy.enable") {
			httpClient = buildTelegramHTTPClient()
		}
		if httpClient == nil {
			httpClient = http.DefaultClient
		}
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
		u := tgbotapi.NewUpdate(0)
		u.Timeout = 60
		updates := bot.GetUpdatesChan(u)
		go func() {
			for update := range updates {
				if update.Message == nil || update.Message.From == nil {
					continue
				}
				txt := strings.TrimSpace(update.Message.Text)
				if txt == "" {
					continue
				}
				onText(update.Message.Chat.ID, update.Message.From.ID, txt)
			}
		}()
	})
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
			log.WithError(err).Warnf("invalid telegram.proxy.url for socks5: %s", proxyURL)
			return nil
		}
		var auth *xproxy.Auth
		if u.User != nil {
			pass, _ := u.User.Password()
			auth = &xproxy.Auth{User: u.User.Username(), Password: pass}
		}
		d, err := xproxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{})
		if err != nil {
			log.WithError(err).Warnf("failed to init socks5 dialer for %s", proxyURL)
			return nil
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.Dial(network, addr)
		}
	} else {
		u, err := url.Parse(proxyURL)
		if err != nil {
			log.WithError(err).Warnf("invalid telegram.proxy.url: %s", proxyURL)
			return nil
		}
		tr.Proxy = http.ProxyURL(u)
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

func sendToTelegram(chatID int64, sm *message.SendingMessage) {
	if sm == nil || bot == nil {
		return
	}
	var tb strings.Builder
	var images []*message.ImageElement
	var videos []*message.VideoElement
	for _, e := range sm.Elements {
		switch v := e.(type) {
		case *message.TextElement:
			tb.WriteString(v.Content)
		case *message.ImageElement:
			images = append(images, v)
		case *message.VideoElement:
			videos = append(videos, v)
		case *message.AtElement:
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

func sendPhoto(chatID int64, img *message.ImageElement, caption string) {
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
func sendVideo(chatID int64, v *message.VideoElement, caption string) {
	if v == nil || bot == nil {
		return
	}
	var cfg tgbotapi.VideoConfig
	switch f := v.File.(type) {
	case string:
		file := strings.TrimSpace(f)
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
		} else if len(v.Url) > 0 {
			cfg = tgbotapi.NewVideo(chatID, tgbotapi.FileURL(v.Url))
		} else {
			return
		}
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
