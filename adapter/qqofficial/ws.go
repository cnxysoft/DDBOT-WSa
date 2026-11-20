package qqofficial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// EventHandler 是事件分发的出口：网关收到 op=0 且 t 是业务事件时回调。
// data 是信封里 d 字段的原始 JSON，原样透传，由上层按 eventType 反序列化。
//
// eventID 是信封**顶层**的 id 字段（不是 d 里的），原样透传。
// 为什么必须传下去：实测（2026-11-12）GROUP_ADD_ROBOT 的帧顶层带
// "id":"GROUP_ADD_ROBOT:<uuid>"，把它当 event_id 发消息，在
// allow_proactive_msg=false 的群里也能发出去（http=200）—— 也就是「没有主动消息
// 权限时唯一还能发话的通道」。事件体 d 里**没有**这个 id，丢了就再也拿不回来。
// 其余事件类型目前没有观察到这个字段，为空串。
//
// 注意：OnDispatch 在读循环里**同步**调用（保持事件顺序），实现不能长时间阻塞。
type EventHandler interface {
	OnDispatch(eventType string, id string, data json.RawMessage)
}

// EventHandlerFunc 让普通函数满足 EventHandler。
type EventHandlerFunc func(eventType string, id string, data json.RawMessage)

// OnDispatch 实现 EventHandler。
func (f EventHandlerFunc) OnDispatch(eventType string, id string, data json.RawMessage) {
	if f != nil {
		f(eventType, id, data)
	}
}

// wsPayload 是网关消息的统一信封。
//
// ID 是事件 id（帧**顶层**，事件体 d 里没有）。实测样本只有 GROUP_ADD_ROBOT 一条：
//
//	{"op":0,"s":3,"t":"GROUP_ADD_ROBOT","id":"GROUP_ADD_ROBOT:1fbfcd00-…","d":{…}}
//
// 它可以直接当发消息的 event_id 用（见 message.go 的 SendOptions.EventID），所以
// 读循环里必须把它一路传到事件处理，别在信封这一层丢掉。
type wsPayload struct {
	Op   int             `json:"op"`
	Data json.RawMessage `json:"d,omitempty"`
	Seq  *int64          `json:"s,omitempty"`
	Type string          `json:"t,omitempty"`
	ID   string          `json:"id,omitempty"`
}

type helloData struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type readyData struct {
	SessionID string `json:"session_id"`
	Shard     []int  `json:"shard"`
	User      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"user"`
}

type gatewayResp struct {
	URL string `json:"url"`
}

type identifyData struct {
	Token      string         `json:"token"`
	Intents    int            `json:"intents"`
	Shard      []int          `json:"shard"`
	Properties map[string]any `json:"properties"`
}

type resumeData struct {
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
}

// fatalWSError 表示不该重连的网关错误（机器人被下架 / 被封禁）。
type fatalWSError struct {
	Code int
	Err  error
}

func (e *fatalWSError) Error() string {
	return fmt.Sprintf("网关不可恢复（关闭码 %d）：%v", e.Code, e.Err)
}

func (e *fatalWSError) Unwrap() error { return e.Err }

func isFatalWSError(err error) bool {
	var fe *fatalWSError
	return errors.As(err, &fe)
}

// Gateway 是 QQ 官方机器人网关（WebSocket）连接。
//
// 生命周期：Start 取 /gateway -> 连 wss -> 收 Hello -> 发 Identify（或 Resume）->
// 等首个 READY 后**返回**；之后在后台维持心跳与断线重连，直到 Stop 或 ctx 取消。
//
// 一条约定：网关地址不硬编。实测 /gateway 返回的域名与文档示例不一致，
// 所以每次重连都重新取一次。
type Gateway struct {
	tm      *TokenManager
	handler EventHandler
	cfg     *Config

	httpClient *http.Client

	mu        sync.Mutex
	conn      *websocket.Conn
	writeMu   sync.Mutex
	sessionID string
	seq       int64
	// fatalErr 是**启动完成之后**发生的不可恢复关闭（4914 下架 / 4915 封禁），见 FatalError。
	fatalErr  error
	started   bool
	cancelRun context.CancelFunc

	stop      chan struct{}
	done      chan struct{}
	ready     chan struct{}
	initErr   chan error
	readyOnce sync.Once
	stopOnce  sync.Once
	errOnce   sync.Once

	lastAck   atomic.Int64 // 最近一次心跳 ACK 的纳秒时间戳
	connected atomic.Bool
}

// NewGateway 建一个网关。h 为 nil 时事件会被丢掉（只剩连接能力）。
func NewGateway(tm *TokenManager, h EventHandler) *Gateway {
	return newGatewayFromConfig(tm, h, GetConfig())
}

func newGatewayFromConfig(tm *TokenManager, h EventHandler, cfg *Config) *Gateway {
	copyConfig := *cfg
	return &Gateway{
		tm:         tm,
		handler:    h,
		cfg:        &copyConfig,
		httpClient: &http.Client{Timeout: GatewayFetchTimeout},
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		ready:      make(chan struct{}),
		initErr:    make(chan error, 1),
	}
}

// Start 启动网关并**阻塞到首个 READY**（或失败）。
// 返回 nil 表示已经就绪；之后连接由后台协程维持，直到 Stop 或 ctx 取消。
func (g *Gateway) Start(ctx context.Context) error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return fmt.Errorf("%s: gateway 已启动过，不能重复 Start", ConfigKeyPrefix)
	}
	if g.tm == nil {
		g.mu.Unlock()
		return fmt.Errorf("%s: TokenManager 为空", ConfigKeyPrefix)
	}
	g.started = true
	runCtx, cancel := context.WithCancel(ctx)
	g.cancelRun = cancel
	g.mu.Unlock()

	go g.run(runCtx, cancel)

	timeout := g.cfg.StartTimeout
	if timeout <= 0 {
		timeout = DefaultStartTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-g.ready:
		return nil
	case err := <-g.initErr:
		_ = g.Stop()
		return err
	case <-timer.C:
		_ = g.Stop()
		return fmt.Errorf("%s: 等待网关 READY 超时（%s）", ConfigKeyPrefix, timeout)
	case <-ctx.Done():
		_ = g.Stop()
		return ctx.Err()
	}
}

// Stop 关闭连接并等后台协程退出。可重复调用。
func (g *Gateway) Stop() error {
	g.mu.Lock()
	if !g.started {
		g.mu.Unlock()
		return nil
	}
	cancel := g.cancelRun
	conn := g.conn
	done := g.done
	g.mu.Unlock()

	g.stopOnce.Do(func() { close(g.stop) })
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}

	select {
	case <-done:
		return nil
	case <-time.After(StopTimeout):
		return fmt.Errorf("%s: 等待网关退出超时", ConfigKeyPrefix)
	}
}

// SessionID 返回当前会话 id（未就绪时为空）。
func (g *Gateway) SessionID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessionID
}

// Seq 返回最近一次收到的事件序号。
func (g *Gateway) Seq() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seq
}

// Connected 表示当前是否有一条已就绪的连接。
func (g *Gateway) Connected() bool { return g.connected.Load() }

// FatalError 返回**启动完成之后**发生的、不可恢复的网关关闭（4914 机器人下架 / 4915 封禁）。
// 没有发生过就是 nil。
//
// 为什么必须有这个查询点：致命关闭之后 run() 直接收工（不重连、不再有心跳），而 Start
// 早就返回了，initErr 通道没人读——只留一条 Warn 等于没上报。此时 Connected() 会变 false，
// 但它和「普通断线、后台正在重连」长得一模一样，上层分不出「等一会儿就好」和「永远回不来」。
//
// 上层怎么消费（adapter 侧心跳巡检，即 adapter.go 的 heartbeatLoop；这里不改它的接口）：
//
//	cur := a.IsConnected()          // = started && gateway.Connected()
//	if !cur && a.gateway.FatalError() != nil {
//	    // 永久离线：报离线事件 + Error 日志（带 FatalError 的文案），等人处理后重启 adapter
//	}
//
// 也就是说：FatalError() 返回非 nil 表示**不会再自动恢复**，重连循环已经退出；
// 返回 nil 表示只是普通断线，后台仍在按退避重连，不必惊动上层。
func (g *Gateway) FatalError() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fatalErr
}

// markFatal 记下不可恢复的关闭。只记第一条（后面的都是它的余波）。
func (g *Gateway) markFatal(err error) {
	g.mu.Lock()
	if g.fatalErr == nil {
		g.fatalErr = err
	}
	g.mu.Unlock()
}

// isReady 表示是否已经收到过首个 READY / RESUMED（Start 已经返回 true 的那种状态）。
// 只用来决定日志措辞：READY 之前的致命关闭由 Start 的错误返回兜住，之后只能靠 FatalError()。
func (g *Gateway) isReady() bool {
	select {
	case <-g.ready:
		return true
	default:
		return false
	}
}

func (g *Gateway) stopping() bool {
	select {
	case <-g.stop:
		return true
	default:
		return false
	}
}

func (g *Gateway) run(ctx context.Context, cancel context.CancelFunc) {
	defer close(g.done)
	defer cancel()

	backoff := MinReconnectBackoff
	for {
		if ctx.Err() != nil || g.stopping() {
			return
		}

		established, err := g.connectAndServe(ctx)
		if err != nil && !g.stopping() && ctx.Err() == nil {
			logger.Warnf("%s: 网关连接中断：%v", ConfigKeyPrefix, err)
			if isFatalWSError(err) {
				// 致命关闭码（4914 下架 / 4915 封禁）之后 run() 直接收工，走不到下面
				// 那次 g.connected.Store(false)；而 READY(444) / RESUMED(389) 会把它置 true。
				// 这里必须自己清掉，否则连接已经没了、上层却永久认为在线（心跳循环看不出状态变化，
				// 连离线事件都不发）。
				//
				// 光清标志不够：Start 早已返回、initErr 没人读、重连循环也退出了。所以致命关闭
				// 必须落在 FatalError() 这个可查询状态上，并且 READY 之后那次用 **Error** 级别
				// 明说「不再重连、要人工处理」——上层（adapter 心跳巡检）据此发离线事件，
				// 而不是把「永久离线」当成「马上重连」。
				g.connected.Store(false)
				if g.isReady() {
					logger.Errorf("%s: 网关致命关闭，后台不再重连，机器人已永久离线；"+
						"需要人工处理（确认机器人没被下架/封禁）后重启 adapter。上层应当调 FatalError() "+
						"区分永久离线与「正在重连」：%v", ConfigKeyPrefix, err)
				} else {
					logger.Warnf("%s: 网关致命关闭，且尚未 READY（本次启动直接失败）：%v", ConfigKeyPrefix, err)
				}
				g.failInit(err)
				return
			}
		}
		g.connected.Store(false)

		if ctx.Err() != nil || g.stopping() {
			return
		}

		if established {
			backoff = MinReconnectBackoff
		}
		// 抖动一下，别让多个实例同时回来
		wait := backoff + time.Duration(rand.Int63n(int64(backoff/2)+1))
		logger.Infof("%s: %s 后重连网关", ConfigKeyPrefix, wait)
		select {
		case <-time.After(wait):
		case <-g.stop:
			return
		case <-ctx.Done():
			return
		}
		if !established && backoff < MaxReconnectBackoff {
			backoff *= 2
			if backoff > MaxReconnectBackoff {
				backoff = MaxReconnectBackoff
			}
		}
	}
}

// connectAndServe 连一次网关并一直读到断开。
// 返回的 established 表示这次连接是否成功建立了会话（收到 READY / RESUMED）。
func (g *Gateway) connectAndServe(ctx context.Context) (bool, error) {
	wsURL, err := g.fetchGatewayURL(ctx)
	if err != nil {
		return false, err
	}

	token, err := g.tm.Token(ctx)
	if err != nil {
		return false, err
	}

	dialer := websocket.Dialer{HandshakeTimeout: HandshakeTimeout}
	conn, resp, err := dialer.DialContext(ctx, wsURL, http.Header{"User-Agent": []string{UserAgent}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return false, fmt.Errorf("连接网关 %s 失败（HTTP %d）：%w", wsURL, status, err)
	}
	g.setConn(conn)
	defer func() {
		g.clearConn(conn)
		_ = conn.Close()
	}()

	// 1) 等 Hello，拿服务端下发的心跳间隔
	hello, err := g.waitHello(conn)
	if err != nil {
		return false, err
	}
	interval := time.Duration(hello.HeartbeatInterval) * time.Millisecond
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}

	// 2) Identify 或 Resume
	bearer := AuthHeader(token)
	sessionID, lastSeq := g.session()
	if sessionID != "" {
		if err := g.writeJSON(conn, map[string]any{
			"op": OpResume,
			"d":  resumeData{Token: bearer, SessionID: sessionID, Seq: lastSeq},
		}); err != nil {
			return false, fmt.Errorf("发送 Resume 失败：%w", err)
		}
		logger.Infof("%s: 已发送 Resume（session_id=%s, seq=%d）", ConfigKeyPrefix, sessionID, lastSeq)
	} else {
		intents := g.cfg.Intents
		if intents <= 0 {
			intents = DefaultIntents
		}
		if err := g.writeJSON(conn, map[string]any{
			"op": OpIdentify,
			"d": identifyData{
				Token:      bearer,
				Intents:    intents,
				Shard:      []int{g.cfg.ShardID, g.cfg.ShardCount},
				Properties: map[string]any{},
			},
		}); err != nil {
			return false, fmt.Errorf("发送 Identify 失败：%w", err)
		}
		logger.Infof("%s: 已发送 Identify（intents=%d, shard=[%d,%d]）",
			ConfigKeyPrefix, intents, g.cfg.ShardID, g.cfg.ShardCount)
	}

	// 3) 心跳
	hbCtx, cancelHB := context.WithCancel(ctx)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		g.heartbeatLoop(hbCtx, conn, interval)
	}()
	defer func() {
		cancelHB()
		<-hbDone
	}()

	// 4) 读循环
	established := false
	readTimeout := interval*3 + HeartbeatAckSlack
	for {
		p, err := g.readFrame(conn, readTimeout)
		if err != nil {
			return established, g.handleReadError(err)
		}
		if p.Seq != nil {
			g.setSeq(*p.Seq)
		}

		switch p.Op {
		case OpHeartbeatACK:
			g.markAck()
			logger.Debugf("%s: 收到心跳 ACK", ConfigKeyPrefix)
		case OpDispatch:
			switch p.Type {
			case EventReady:
				if err := g.onReady(p.Data); err != nil {
					return established, err
				}
				established = true
			case EventResumed:
				g.connected.Store(true)
				g.markReady()
				established = true
				logger.Infof("%s: RESUMED（session_id=%s, seq=%d）", ConfigKeyPrefix, g.SessionID(), g.Seq())
			default:
				// p.ID 是帧顶层的**事件 id**（实测 GROUP_ADD_ROBOT 有），
				// 它能在没有主动消息权限的群里当 event_id 用，所以必须一起传下去。
				g.dispatch(p.Type, p.ID, p.Data)
			}
		case OpReconnect:
			// op=7 只是「服务端要求重连」，**不是**会话作废：session_id / seq 必须留着，
			// 下次连上照常走 Resume，断线期间的事件（含发给机器人的命令）才会补发。
			// 原先这里 clearSession() 会把可恢复的中断当成不可恢复，直接丢掉重放。
			//
			// 保留 session 的平台侧依据（三家独立实现口径一致）：
			//   - koishi adapter-qq：src/ws.ts:80-84 收到 RECONNECT 只清心跳定时器，
			//     不动 _sessionId；它用 code 4000 关连接，落在 ws.ts:114 的
			//     「>4000 且非 4008/4009」判定之外，所以 session 留着，下次 HELLO 走 RESUME；
			//   - QQ 官方 SDK：src/protocol/gateway/gateway-connection.ts:251-254，
			//     RECONNECT 只 cleanup + scheduleReconnect，不清 sessionId（只有
			//     INVALID_SESSION 且 canResume=false 才清），下次 handleHello 带 sessionId 发 RESUME；
			//   - MaiBot-QQ-Adapter：plugin.py:569-575，op=7 只关连接，
			//     仅 op=9 且 d 不为 true 才 _reset_gateway_session()。
			// 真不可恢复时平台会用 4006/4007 明说，那时 handleReadError 再清也不迟。
			return established, errors.New("网关下发 op=7，要求重新连接")
		case OpInvalidSession:
			// d 为 true 表示还能 Resume，false 必须重新 Identify（三家的口径见上面 op=7 那段）。
			//
			// **解析失败 ≠ 平台说「不可 resume」**：原来这里是一句 `_ = json.Unmarshal(p.Data, &resumable)`，
			// 错误被吞掉、resumable 保持 false，于是「我们读不懂」被当成「平台说不恢复」→ clearSession()
			// 把一段其实还能恢复的会话直接作废：下次连上只能 Identify，断线期间的事件（含发给机器人的
			// 命令）全部丢掉。d 的类型并不保证永远是 bool（换成对象/字符串，或干脆省略，都是平台上
			// 一次改协议就能发生的事）。
			//
			// 所以读不懂时走保守方向：**保留 session**，下次连上照常 Resume。真不可 resume 时平台会
			// 用 4006/4007 明说，那时 handleReadError 再清也不迟 —— 与 op=7 分支同源：能 resume 就别丢。
			//
			// 用 *bool 而不是 bool 是有意的：JSON 的 null 解进 bool 不报错、静静留成 false，
			// 那会把「平台没给值」当成「不可 resume」，还是同一个坑换个形状。null 与缺失都归到「不知道」。
			var resumable *bool
			var unknownWhy string
			switch {
			case len(p.Data) == 0:
				unknownWhy = "d 缺失"
			default:
				if err := json.Unmarshal(p.Data, &resumable); err != nil {
					unknownWhy = fmt.Sprintf("d 解析失败（%v）", err)
				} else if resumable == nil {
					unknownWhy = "d 为 null"
				}
			}
			if unknownWhy != "" {
				logger.Warnf("%s: op=9 的 %s，按保守方向保留 session（下次连上照常 Resume；"+
					"真不可 resume 平台会用 4006/4007 明说）", ConfigKeyPrefix, unknownWhy)
				return established, fmt.Errorf("网关下发 op=9 无效会话（%s，无法判定能否 resume；按保守方向保留 session）", unknownWhy)
			}
			if !*resumable {
				g.clearSession()
			}
			return established, fmt.Errorf("网关下发 op=9 无效会话（可 resume=%v）", *resumable)
		default:
			// 不认识的 op 必须喊出来：平台新增 op 是常态（op=7、op=9 都是这么加进来的），
			// Debug 级等于「新 op 没处理」在故障现场一片沉默，没人会知道。
			//
			// 但**只打 op 号与载荷长度**：d 里可能带 openid、消息内容这类敏感信息，
			// 日志是会被贴进 issue 的。长度足够判断「有没有载荷、大概多大」。
			logger.Warnf("%s: 收到未处理的网关 op=%d（载荷 %d 字节），本实现不认识它，已忽略；"+
				"若平台刚升级，这条就是排查线索", ConfigKeyPrefix, p.Op, len(p.Data))
		}
	}
}

// waitHello 等建连后的 op=10 Hello。
func (g *Gateway) waitHello(conn *websocket.Conn) (*helloData, error) {
	p, err := g.readFrame(conn, HelloTimeout)
	if err != nil {
		return nil, fmt.Errorf("等待 Hello 失败：%w", err)
	}
	if p.Op != OpHello {
		return nil, fmt.Errorf("网关首个消息不是 op=10 Hello，而是 op=%d", p.Op)
	}
	var hello helloData
	if len(p.Data) > 0 {
		if err := json.Unmarshal(p.Data, &hello); err != nil {
			return nil, fmt.Errorf("解析 Hello 失败：%w", err)
		}
	}
	return &hello, nil
}

func (g *Gateway) onReady(data json.RawMessage) error {
	var rd readyData
	if len(data) > 0 {
		if err := json.Unmarshal(data, &rd); err != nil {
			return fmt.Errorf("解析 READY 失败：%w", err)
		}
	}
	if rd.SessionID == "" {
		return errors.New("READY 里没有 session_id")
	}
	g.mu.Lock()
	g.sessionID = rd.SessionID
	g.mu.Unlock()
	g.connected.Store(true)
	logger.Infof("%s: READY（session_id=%s, shard=%v, bot=%s/%s）",
		ConfigKeyPrefix, rd.SessionID, rd.Shard, rd.User.ID, rd.User.Username)
	g.markReady()
	return nil
}

func (g *Gateway) markReady() {
	g.readyOnce.Do(func() { close(g.ready) })
}

func (g *Gateway) failInit(err error) {
	g.errOnce.Do(func() {
		select {
		case g.initErr <- err:
		default:
		}
	})
}

// dispatch 把业务事件交给上层。带 panic 兜底：上层实现崩了不能把连接带走。
//
// id 是帧顶层的事件 id（可能为空串），原样透传，不在这里解释语义。
func (g *Gateway) dispatch(eventType, id string, data json.RawMessage) {
	if eventType == "" || g.handler == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("%s: 处理事件 %s 时 panic：%v", ConfigKeyPrefix, eventType, r)
		}
	}()
	g.handler.OnDispatch(eventType, id, data)
}

// heartbeatLoop 按服务端下发的间隔发 op=1 心跳（d 为最新 seq），
// 连续收不到 ACK 就主动断开让外层重连。
func (g *Gateway) heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	g.markAck() // 连上就是基线，第一次判定在一个 interval 之后
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-g.stop:
			return
		case <-ticker.C:
			last := g.lastAck.Load()
			if last != 0 && time.Since(time.Unix(0, last)) > interval*2+HeartbeatAckSlack {
				logger.Warnf("%s: 连续 %s 没收到心跳 ACK，主动断开重连", ConfigKeyPrefix, interval*2)
				_ = conn.Close()
				return
			}
			seq := g.Seq()
			var d any
			if seq > 0 {
				d = seq
			}
			if err := g.writeJSON(conn, map[string]any{"op": OpHeartbeat, "d": d}); err != nil {
				logger.Warnf("%s: 发送心跳失败：%v", ConfigKeyPrefix, err)
				_ = conn.Close()
				return
			}
			logger.Debugf("%s: 已发送心跳（d=%v）", ConfigKeyPrefix, d)
		}
	}
}

func (g *Gateway) markAck() { g.lastAck.Store(time.Now().UnixNano()) }

// readFrame 读一帧并解析信封。
func (g *Gateway) readFrame(conn *websocket.Conn, timeout time.Duration) (*wsPayload, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var p wsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("解析网关消息失败：%w", err)
	}
	return &p, nil
}

// writeJSON 串行化写。gorilla 只允许一个并发写者，握手与心跳共用这条通道。
func (g *Gateway) writeJSON(conn *websocket.Conn, v any) error {
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return conn.WriteJSON(v)
}

// handleReadError 处理读错误：按关闭码决定保留还是清掉会话，并标出致命码。
func (g *Gateway) handleReadError(err error) error {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return err
	}
	logger.Warnf("%s: 网关关闭（code=%d, reason=%q）", ConfigKeyPrefix, closeErr.Code, closeErr.Text)

	switch closeErr.Code {
	case WSCloseBotOffline, WSCloseBotBanned:
		g.clearSession()
		fe := &fatalWSError{Code: closeErr.Code, Err: err}
		// 记进可查询状态：READY 之后发生的话，Start 已经返回，上层只能靠 FatalError() 感知。
		g.markFatal(fe)
		return fe
	case WSCloseAuthFailed:
		// token 无效：作废缓存，下次重新取
		if g.tm != nil {
			g.tm.Invalidate()
		}
		g.clearSession()
	case WSCloseSessionInvalid, WSCloseIdentifySeqInvalid,
		WSCloseInvalidShard, WSCloseShardRequired, WSCloseInvalidVersion, WSCloseInvalidIntents,
		WSCloseAlreadyIdentified, WSCloseNotIdentified, WSCloseInvalidOpcode, WSCloseInvalidPayload:
		g.clearSession()
	case WSCloseRateLimited:
		// 4008 是「发得太快」（限速），**不是**会话作废：session_id / seq 还是好的，
		// 下次连上照常 Resume，断线期间的事件会被补发。原先它跟 4006/4007 一起塞进
		// 上面那批 clearSession 里，一次限速就白白丢掉整段补发窗口——这正是 op=7 分支
		// （「能 resume 就别丢 session」）自己论证过的坑。
		//
		// 这里只做一件事：把退避重连交给 run()（它本来就会按 MinReconnectBackoff 起步、
		// 失败翻倍）。不清 session、不标致命、不进 FatalError。
		logger.Warnf("%s: 网关限速关闭（4008），session 与 seq 一律保留，退避后走 Resume 重连", ConfigKeyPrefix)
	}
	return err
}

// fetchGatewayURL 每次连之前重新取网关地址。
// 实测返回的域名与文档示例不同，所以不许缓存硬编。
func (g *Gateway) fetchGatewayURL(ctx context.Context) (string, error) {
	token, err := g.tm.Token(ctx)
	if err != nil {
		return "", err
	}

	base := g.cfg.APIBaseURL
	if base == "" {
		base = APIBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+PathGateway, nil)
	if err != nil {
		return "", fmt.Errorf("构造 /gateway 请求失败：%w", err)
	}
	req.Header.Set(AuthHeaderName, AuthHeader(token))
	req.Header.Set("User-Agent", UserAgent)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 /gateway 失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
	if err != nil {
		return "", fmt.Errorf("读 /gateway 响应失败：%w", err)
	}

	var out gatewayResp
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	if out.URL == "" {
		if apiErr := ParseAPIError(resp.StatusCode, body); apiErr != nil {
			return "", fmt.Errorf("获取网关地址失败：%w", apiErr)
		}
		return "", fmt.Errorf("获取网关地址失败：HTTP %d 的响应里没有 url", resp.StatusCode)
	}
	// 只认 scheme，不校验域名——实测域名就和文档不一样
	if !strings.HasPrefix(out.URL, "wss://") && !strings.HasPrefix(out.URL, "ws://") {
		return "", fmt.Errorf("网关地址 scheme 不是 ws/wss：%q", out.URL)
	}
	logger.Infof("%s: 网关地址 %s", ConfigKeyPrefix, out.URL)
	return out.URL, nil
}

func (g *Gateway) setConn(conn *websocket.Conn) {
	g.mu.Lock()
	g.conn = conn
	g.mu.Unlock()
}

func (g *Gateway) clearConn(conn *websocket.Conn) {
	g.mu.Lock()
	if g.conn == conn {
		g.conn = nil
	}
	g.mu.Unlock()
}

func (g *Gateway) session() (string, int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessionID, g.seq
}

func (g *Gateway) setSeq(seq int64) {
	g.mu.Lock()
	g.seq = seq
	g.mu.Unlock()
}

func (g *Gateway) clearSession() {
	g.mu.Lock()
	g.sessionID = ""
	g.seq = 0
	g.mu.Unlock()
}
