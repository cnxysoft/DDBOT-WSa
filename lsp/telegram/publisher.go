package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

var log = logrus.WithField("module", "telegram")

var managerMu sync.RWMutex
var prepareMu sync.Mutex
var current *generation
var globalChats []int64
var receiver func(chatID, fromID int64, text string)
var initialized bool
var offsets = make(map[string]int)
var dispatchOnce sync.Once
var dispatchQueue = make(chan incomingText, 100)
var activeCommands sync.WaitGroup

type incomingText struct {
	g              *generation
	handler        func(int64, int64, string)
	chatID, fromID int64
	text           string
}

type generation struct {
	bot      *tgbotapi.BotAPI
	token    string
	endpoint string
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	polling  bool // protected by managerMu
	client   *http.Client
}

type pollingClient struct {
	client *http.Client
	ctx    context.Context
}

func (c pollingClient) Do(req *http.Request) (*http.Response, error) {
	// Cancel long polls on retirement; sends already in progress may finish.
	if strings.HasSuffix(req.URL.Path, "/getUpdates") {
		req = req.Clone(c.ctx)
	}
	return c.client.Do(req)
}

func snapshotBot() *tgbotapi.BotAPI {
	if !ensureInit() {
		return nil
	}
	managerMu.RLock()
	defer managerMu.RUnlock()
	if current == nil {
		return nil
	}
	return current.bot
}

func Publish(m *mmsg.MSG) {
	if m == nil || !ensureInit() {
		return
	}
	managerMu.RLock()
	g := current
	chats := append([]int64(nil), globalChats...)
	managerMu.RUnlock()
	if g == nil {
		return
	}
	sms := m.ToMessage(mmsg.NewGroupTarget(0))
	for _, chatID := range chats {
		for _, sm := range sms {
			go sendToTelegramWithBot(g.bot, chatID, sm)
		}
	}
}

func ensureInit() bool {
	managerMu.RLock()
	ready := initialized
	managerMu.RUnlock()
	if !ready {
		commit, abort, err := PrepareRuntimeReload(nil, config.GlobalConfig.Snapshot())
		if err != nil {
			if abort != nil {
				abort()
			}
			log.Warn("Telegram 初始化失败，请检查凭据与连接")
			return false
		}
		if commit != nil {
			commit()
		}
	}
	managerMu.RLock()
	defer managerMu.RUnlock()
	return current != nil
}

// PrepareRuntimeReload authorizes a new client before persistence. A rejected
// candidate cannot overwrite the live client, chats, or receiving offset.
func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	if previous != nil && reflect.DeepEqual(previous.Get("telegram"), next.Get("telegram")) {
		return nil, nil, nil
	}
	prepareMu.Lock()
	managerMu.RLock()
	old := current
	alreadyInitialized := initialized
	managerMu.RUnlock()
	// A concurrent lazy initializer may have finished while we waited.
	if previous == nil && alreadyInitialized {
		prepareMu.Unlock()
		return nil, nil, nil
	}
	var chats []int64
	for _, value := range next.GetStringSlice("telegram.chats") {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id == 0 {
			prepareMu.Unlock()
			return nil, nil, fmt.Errorf("telegram.chats 包含无效聊天 ID")
		}
		chats = append(chats, id)
	}
	var candidate *generation
	if next.GetBool("telegram.enable") {
		token := strings.TrimSpace(next.GetString("telegram.token"))
		endpoint := next.GetString("telegram.endpoint")
		if endpoint == "" {
			endpoint = tgbotapi.APIEndpoint
		}
		if token == "" {
			prepareMu.Unlock()
			return nil, nil, fmt.Errorf("telegram.token 不能为空")
		}
		if old != nil && old.token == token && old.endpoint == endpoint {
			candidate = old
		} else {
			ctx, cancel := context.WithCancel(context.Background())
			client := buildTelegramHTTPClient()
			client.Timeout = 75 * time.Second
			b, err := tgbotapi.NewBotAPIWithClient(token, endpoint, pollingClient{client, ctx})
			if err != nil {
				cancel()
				client.CloseIdleConnections()
				prepareMu.Unlock()
				return nil, nil, fmt.Errorf("Telegram 授权失败，请检查 token、endpoint 与网络连接")
			}
			candidate = &generation{bot: b, token: token, endpoint: endpoint, ctx: ctx, cancel: cancel, done: make(chan struct{}), client: client}
		}
	}
	var once sync.Once
	abort := func() {
		once.Do(func() {
			if candidate != nil && candidate != old {
				candidate.cancel()
				candidate.client.CloseIdleConnections()
			}
			prepareMu.Unlock()
		})
	}
	commit := func() {
		once.Do(func() {
			// Stop old polls before publishing the next receiver. No user callback runs
			// in the polling goroutine, so saving config from a command cannot deadlock.
			if old != nil && old != candidate {
				old.cancel()
				managerMu.RLock()
				polling := old.polling
				managerMu.RUnlock()
				if polling {
					<-old.done
				}
				old.client.CloseIdleConnections()
			}
			managerMu.Lock()
			current = candidate
			globalChats = chats
			initialized = true
			startReceiverLocked(candidate)
			managerMu.Unlock()
			prepareMu.Unlock()
		})
	}
	return commit, abort, nil
}

func SendToChat(chatID int64, m *mmsg.MSG) {
	if m == nil {
		return
	}
	b := snapshotBot()
	if b == nil {
		return
	}
	for _, sm := range m.ToMessage(mmsg.NewGroupTarget(0)) {
		sendToTelegramWithBot(b, chatID, sm)
	}
}

// StartReceiving registers the handler even while disabled, enabling off->on.
func StartReceiving(onText func(chatID, fromID int64, text string)) {
	if onText == nil {
		return
	}
	dispatchOnce.Do(func() {
		go func() {
			for message := range dispatchQueue {
				dispatch(message)
			}
		}()
	})
	managerMu.Lock()
	receiver = onText
	managerMu.Unlock()
	if !ensureInit() {
		return
	}
	managerMu.Lock()
	startReceiverLocked(current)
	managerMu.Unlock()
}

func dispatch(message incomingText) {
	managerMu.Lock()
	if message.g == nil || current != message.g || message.g.ctx.Err() != nil {
		managerMu.Unlock()
		return
	}
	activeCommands.Add(1)
	managerMu.Unlock()
	defer activeCommands.Done()
	defer func() {
		if recover() != nil {
			log.Error("Telegram 命令处理异常")
		}
	}()
	message.handler(message.chatID, message.fromID, message.text)
}

func startReceiverLocked(g *generation) {
	if g == nil || g.polling || receiver == nil {
		return
	}
	g.polling = true
	go g.receive()
}

func (g *generation) receive() {
	defer close(g.done)
	backoff := time.Second
	for {
		select {
		case <-g.ctx.Done():
			return
		default:
		}
		managerMu.RLock()
		offset := offsets[g.token]
		managerMu.RUnlock()
		u := tgbotapi.NewUpdate(offset)
		u.Timeout = 60
		u.AllowedUpdates = []string{"message", "edited_message", "channel_post"}
		updates, err := g.bot.GetUpdates(u)
		if g.ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn("Telegram 接收失败，将重试")
			select {
			case <-g.ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, update := range updates {
			managerMu.Lock()
			if g.ctx.Err() != nil || current != g {
				managerMu.Unlock()
				return
			}
			if update.UpdateID >= offsets[g.token] {
				offsets[g.token] = update.UpdateID + 1
			}
			handler := receiver
			managerMu.Unlock()
			message := update.Message
			if message == nil {
				message = update.EditedMessage
			}
			if message == nil {
				message = update.ChannelPost
			}
			if message == nil || message.From == nil || message.Chat == nil || handler == nil {
				continue
			}
			text := strings.TrimSpace(message.Text)
			if text != "" {
				// A separate serial worker preserves command order and allows a
				// command to save config while the old long poll is cancelled.
				select {
				case dispatchQueue <- incomingText{g, handler, message.Chat.ID, message.From.ID, text}:
				case <-g.ctx.Done():
					return
				}
			}
		}
	}
}

// Close retires the current poller; existing sends are allowed to complete.
func Close() {
	prepareMu.Lock()
	managerMu.Lock()
	old := current
	current = nil
	initialized = true
	managerMu.Unlock()
	if old != nil {
		old.cancel()
		if old.polling {
			<-old.done
		}
		old.client.CloseIdleConnections()
	}
	// Release prepareMu before waiting: a running command may itself save config.
	// The application closes its config manager first, so no new generation starts.
	prepareMu.Unlock()
	activeCommands.Wait()
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
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		if p, err := proxy_pool.Get(proxy_pool.PreferOversea); err == nil && p != nil {
			u, err := url.Parse(p.ProxyString())
			if err == nil && u.Scheme != "" {
				return u, nil
			}
		}
		return nil, nil
	}

	return &http.Client{Transport: tr, Timeout: 75 * time.Second}
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
	if b := snapshotBot(); b != nil {
		sendToTelegramWithBot(b, chatID, sm)
	}
}

func sendToTelegramWithBot(bot *tgbotapi.BotAPI, chatID int64, sm *adapter.SendingMessage) {
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
		sendVideo(bot, chatID, videos[0], caption)
		for i := 1; i < len(videos); i++ {
			sendVideo(bot, chatID, videos[i], "")
		}
		for _, img := range images {
			sendPhoto(bot, chatID, img, "")
		}
	case len(images) > 0:
		sendPhoto(bot, chatID, images[0], caption)
		for i := 1; i < len(images); i++ {
			sendPhoto(bot, chatID, images[i], "")
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

func sendPhoto(bot *tgbotapi.BotAPI, chatID int64, img *adapter.ImageSegment, caption string) {
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
func sendVideo(bot *tgbotapi.BotAPI, chatID int64, v *adapter.VideoSegment, caption string) {
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
