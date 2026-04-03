package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var wsLogger = logrus.WithField("module", "wsclient")

// isDebugLoggingEnabled 检查当前是否启用 debug 级别日志，用于热路径中避免调用 runtime.Caller
func isDebugLoggingEnabled() bool {
	return wsLogger.Logger.IsLevelEnabled(logrus.DebugLevel)
}

// 调试：用原子计数器追踪消息处理
var handleMessageTotal atomic.Int64

// 调试：锁操作计数器，用于追踪锁的配对
var lockSeq atomic.Int64

func nextLockSeq() int64 {
	return lockSeq.Add(1)
}

// 获取调用者的函数名
func getCallerFuncName(skip int) string {
	pc, _, _, _ := runtime.Caller(skip)
	f := runtime.FuncForPC(pc)
	if f != nil {
		return f.Name()
	}
	return "unknown"
}

const (
	WSModeServer   = "ws-server"
	WSModeReverse  = "ws-reverse"
	WriteWait      = 10 * time.Second
	PongWait       = 60 * time.Second
	MaxMessageSize = 150 * 1024 * 1024 // 150MB
)

type WSResponse struct {
	Status  string      `json:"status"`
	Retcode int         `json:"retcode"`
	Data    interface{} `json:"data"`
	Message string      `json:"message"`
	Msg     string      `json:"msg"`
	Wording string      `json:"wording"`
	Echo    string      `json:"echo,omitempty"`
}

type WSClient struct {
	mu                sync.RWMutex
	writeMu           sync.Mutex // 保护 websocket 写入操作
	mode              string
	wsMode            string
	url               string
	token             string
	conn              *websocket.Conn
	stopChan          chan struct{}
	stopOnce          sync.Once // 确保 stopChan 只被关闭一次
	responseCh        map[string]chan *WSResponse
	isConnected       bool
	reconnectCnt      int
	maxReconnect      int
	httpServer        *http.Server
	messageHandler    func([]byte)
	heartbeatInterval time.Duration
	connectTimeout    time.Duration

	// 调试：用原子计数器追踪活跃的 goroutine
	activeReadLoops      atomic.Int32
	activeHeartbeatLoops atomic.Int32
	activeHandleConns    atomic.Int32
}

type WSClientOption func(*WSClient)

func WithWSToken(token string) WSClientOption {
	return func(c *WSClient) { c.token = token }
}

func WithWSHeartbeat(interval time.Duration) WSClientOption {
	return func(c *WSClient) { c.heartbeatInterval = interval }
}

func WithWSConnectTimeout(timeout time.Duration) WSClientOption {
	return func(c *WSClient) { c.connectTimeout = timeout }
}

func WithWSMaxReconnect(max int) WSClientOption {
	return func(c *WSClient) { c.maxReconnect = max }
}

func WithWSMessageHandler(handler func([]byte)) WSClientOption {
	return func(c *WSClient) { c.messageHandler = handler }
}

func NewWSClient(mode, wsMode, url string, opts ...WSClientOption) *WSClient {
	c := &WSClient{
		mode:              mode,
		url:               url,
		wsMode:            wsMode,
		stopChan:          make(chan struct{}),
		responseCh:        make(map[string]chan *WSResponse),
		heartbeatInterval: 30 * time.Second,
		connectTimeout:    10 * time.Second,
		maxReconnect:      0,
		reconnectCnt:      0,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *WSClient) Start() error {
	switch c.wsMode {
	case WSModeServer:
		return c.startServer()
	case WSModeReverse:
		go c.connectLoop()
		return nil
	default:
		return fmt.Errorf("unknown ws mode: %s", c.wsMode)
	}
}

func (c *WSClient) Stop() error {
	c.stopOnce.Do(func() {
		close(c.stopChan)
	})
	c.mu.Lock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf(">>> mu.Lock() #%d  caller=%s  [Stop]", nextLockSeq(), getCallerFuncName(2))
	}
	c.isConnected = false
	// Clean up response channels
	for echo, ch := range c.responseCh {
		close(ch)
		delete(c.responseCh, echo)
	}
	if c.conn != nil {
		// 设置短 deadline 唤醒阻塞的 HTTP/2 读循环，防止 goroutine 泄漏
		c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		c.conn.Close()
		c.conn = nil
	}
	if c.httpServer != nil {
		c.httpServer.Shutdown(context.Background())
		c.httpServer = nil
	}
	c.mu.Unlock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf("<<< mu.Unlock() #%d  caller=%s  [Stop]", nextLockSeq(), getCallerFuncName(2))
	}
	return nil
}

func (c *WSClient) IsConnected() bool {
	c.mu.RLock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf(">>> mu.RLock() #%d  caller=%s  [IsConnected]", nextLockSeq(), getCallerFuncName(2))
	}
	ret := c.isConnected
	c.mu.RUnlock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf("<<< mu.RUnlock() #%d  caller=%s  [IsConnected]", nextLockSeq(), getCallerFuncName(2))
	}
	return ret
}

func (c *WSClient) startServer() error {
	addr := c.url
	if addr == "" {
		addr = "0.0.0.0:15630"
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if c.token != "" {
			auth := r.Header.Get("Authorization")
			if auth == "" || strings.TrimPrefix(auth, "Bearer ") != c.token {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			wsLogger.Errorf("WebSocket upgrade error: %v", err)
			return
		}
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.conn = conn
		c.isConnected = true
		c.reconnectCnt = 0
		c.mu.Unlock()
		wsLogger.Info("WebSocket client connected (ws-server mode)")
		c.handleConnection(conn)
	}
	c.httpServer = &http.Server{Addr: addr, Handler: http.HandlerFunc(handler)}
	go func() {
		wsLogger.Infof("WebSocket server starting on ws://%s/", addr)
		if err := c.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			wsLogger.Errorf("WebSocket server error: %v", err)
		}
	}()
	return nil
}

func (c *WSClient) connectLoop() {
	for {
		select {
		case <-c.stopChan:
			return
		default:
		}
		if err := c.connect(); err != nil {
			wsLogger.Errorf("WebSocket connection failed: %v", err)
		}
		if c.maxReconnect > 0 && c.reconnectCnt >= c.maxReconnect {
			return
		}
		c.reconnectCnt++
		waitTime := time.Duration(c.reconnectCnt) * 2 * time.Second
		if waitTime > 30*time.Second {
			waitTime = 30 * time.Second
		}
		select {
		case <-c.stopChan:
			return
		case <-time.After(waitTime):
		}
	}
}

func (c *WSClient) connect() error {
	header := http.Header{}
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}
	dialer := websocket.Dialer{HandshakeTimeout: c.connectTimeout}
	conn, _, err := dialer.Dial(c.url, header)
	if err != nil {
		return err
	}
	c.mu.Lock()
	oldConn := c.conn
	// 先关闭旧连接，唤醒其 goroutines，防止泄漏
	if oldConn != nil {
		wsLogger.Debugf("connect: closing old conn=%p before replacing", oldConn)
		oldConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		oldConn.Close()
	}
	c.conn = conn
	c.isConnected = true
	c.reconnectCnt = 0
	c.mu.Unlock()
	c.handleConnection(conn)
	return nil
}

func (c *WSClient) handleConnection(conn *websocket.Conn) {
	wsLogger.Debugf("handleConnection: starting, conn=%p", conn)
	readErrChan := make(chan error, 1)
	wsLogger.Debugf("handleConnection: readErrChan created, goroutine about to start")
	defer func() {
		if r := recover(); r != nil {
			wsLogger.Errorf("handleConnection panic: %v", r)
		}
		// 设置短 deadline 唤醒阻塞的 HTTP/2 读循环，防止 goroutine 泄漏
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		conn.Close()
		c.mu.Lock()
		if isDebugLoggingEnabled() {
			wsLogger.Debugf(">>> mu.Lock() #%d  caller=%s  [handleConnection defer]", nextLockSeq(), getCallerFuncName(2))
		}
		if c.conn == conn {
			cleanedCount := len(c.responseCh)
			wsLogger.Debugf("handleConnection defer: conn=%p, cleaning %d pending response channels, isConnected will be false", conn, cleanedCount)
			c.conn = nil
			c.isConnected = false
			for echo, ch := range c.responseCh {
				close(ch)
				delete(c.responseCh, echo)
			}
		} else {
			wsLogger.Warnf("handleConnection defer: conn=%p, but c.conn != conn (c.conn=%p), skipping cleanup!", conn, c.conn)
		}
		c.mu.Unlock()
		if isDebugLoggingEnabled() {
			wsLogger.Debugf("<<< mu.Unlock() #%d  caller=%s  [handleConnection defer]", nextLockSeq(), getCallerFuncName(2))
		}
		c.activeHandleConns.Add(-1)
		wsLogger.Debugf("handleConnection: returning, conn=%p, activeHandleConns now=%d", conn, c.activeHandleConns.Load())
	}()

	go c.readLoop(conn, readErrChan)
	c.activeHandleConns.Add(1)
	if c.heartbeatInterval > 0 {
		go c.heartbeatLoop(conn)
	}

	// 定期检查连接状态，防止死锁
	heartbeatTimeout := c.heartbeatInterval
	if heartbeatTimeout <= 0 {
		heartbeatTimeout = 30 * time.Second
	}
	checkTimer := time.NewTicker(heartbeatTimeout)
	defer checkTimer.Stop()

	for {
		select {
		case <-c.stopChan:
			wsLogger.Debugf("handleConnection select: stopChan fired, returning")
			return
		case err := <-readErrChan:
			if err != nil {
				wsLogger.Errorf("Read error: %v", err)
			} else {
				wsLogger.Warnf("Read error: got nil from readErrChan (channel may be closed)")
			}
			wsLogger.Debugf("handleConnection: readErrChan case, returning (isConnected=%v)", c.isConnected)
			return
		case <-checkTimer.C:
			// 定期检查连接是否已被新的连接替换，防止死锁
			c.mu.RLock()
			isCurrentConnection := c.conn == conn
			c.mu.RUnlock()
			if !isCurrentConnection {
				wsLogger.Debugf("Connection replaced, handler exiting")
				return
			}
		}
	}
}

func (c *WSClient) readLoop(conn *websocket.Conn, errChan chan error) {
	c.activeReadLoops.Add(1)
	wsLogger.Debugf("readLoop started: conn=%p, errChan=%p, activeReadLoops=%d", conn, errChan, c.activeReadLoops.Load())
	defer func() {
		if r := recover(); r != nil {
			wsLogger.Errorf("Read loop panic: %v", r)
			select {
			case errChan <- fmt.Errorf("panic: %v", r):
			case <-c.stopChan:
				wsLogger.Debugf("readLoop defer: panic recovered, but stopChan closed, not sending to errChan")
				return
			default:
				wsLogger.Debugf("readLoop defer: panic recovered, errChan full, not sending")
			}
		}
		c.activeReadLoops.Add(-1)
		wsLogger.Debugf("readLoop exiting: conn=%p, activeReadLoops now=%d", conn, c.activeReadLoops.Load())
	}()

	conn.SetReadLimit(MaxMessageSize)
	conn.SetReadDeadline(time.Now().Add(PongWait))
	conn.SetPongHandler(func(string) error {
		defer func() {
			if r := recover(); r != nil {
				wsLogger.Errorf("Pong handler panic: %v", r)
			}
		}()
		// 不在 pongHandler 中设置 deadline，而是在主循环中每次刷新
		// 因为 pongHandler 中的 deadline 设置失败不会被 ReadMessage 正确传播
		return nil
	})

	for {
		// 每次循环都刷新读取截止时间，确保即使在等待数据时也能超时
		conn.SetReadDeadline(time.Now().Add(PongWait))
		_, message, err := conn.ReadMessage()
		if err != nil {
			wsLogger.Debugf("readLoop ReadMessage error: %v (type=%T)", err, err)
			select {
			case errChan <- err:
				wsLogger.Debugf("readLoop: sent error to errChan: %v", err)
			case <-c.stopChan:
				wsLogger.Debugf("readLoop: stopChan closed, not sending error to errChan")
				return
			default:
				// errChan 满时，丢弃旧值直接返回，不在此阻塞
				select {
				case <-errChan:
				default:
				}
				wsLogger.Debugf("readLoop: errChan full, dropped stale error, returning")
				return
			}
		}
		c.handleMessage(message)
	}
}

func (c *WSClient) handleMessage(message []byte) {
	// 处理 echo 响应
	var resp WSResponse
	if err := json.Unmarshal(message, &resp); err == nil && resp.Echo != "" {
		c.mu.Lock()
		if isDebugLoggingEnabled() {
			wsLogger.Debugf(">>> mu.Lock() #%d  caller=%s  [handleMessage echo]", nextLockSeq(), getCallerFuncName(2))
		}
		if ch, exists := c.responseCh[resp.Echo]; exists {
			delete(c.responseCh, resp.Echo)
			c.mu.Unlock()
			if isDebugLoggingEnabled() {
				wsLogger.Debugf("<<< mu.Unlock() #%d  caller=%s  [handleMessage echo]", nextLockSeq(), getCallerFuncName(2))
			}
			select {
			case ch <- &resp:
			default:
				wsLogger.Warnf("handleMessage: response channel full, dropping echo=%s", resp.Echo)
			}
			return
		}
		c.mu.Unlock()
		if isDebugLoggingEnabled() {
			wsLogger.Debugf("<<< mu.Unlock() #%d  caller=%s  [handleMessage echo]", nextLockSeq(), getCallerFuncName(2))
		}
	}

	c.mu.RLock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf(">>> mu.RLock() #%d  caller=%s  [handleMessage handler]", nextLockSeq(), getCallerFuncName(2))
	}
	handler := c.messageHandler
	responseChLen := len(c.responseCh)
	c.mu.RUnlock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf("<<< mu.RUnlock() #%d  caller=%s  [handleMessage handler]", nextLockSeq(), getCallerFuncName(2))
	}

	// 每处理100条消息打印一次 responseCh 状态
	handleMessageTotal.Add(1)
	if handleMessageTotal.Load()%100 == 0 {
		wsLogger.Debugf("handleMessage: processed %d messages, responseCh pending=%d", handleMessageTotal.Load(), responseChLen)
	}

	if handler != nil {
		handler(message)
	}
}

func (c *WSClient) heartbeatLoop(conn *websocket.Conn) {
	c.activeHeartbeatLoops.Add(1)
	wsLogger.Debugf("heartbeatLoop started: conn=%p, activeHeartbeatLoops=%d", conn, c.activeHeartbeatLoops.Load())
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()
	defer func() {
		c.activeHeartbeatLoops.Add(-1)
		wsLogger.Debugf("heartbeatLoop exited: conn=%p, activeHeartbeatLoops now=%d", conn, c.activeHeartbeatLoops.Load())
	}()
	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			if err := c.writeRaw(conn, websocket.PingMessage, []byte{}); err != nil {
				wsLogger.Debugf("heartbeatLoop write error: conn=%p, err=%v", conn, err)
				return
			}
			// 每10次心跳打印一次活跃协程数和responseCh状态
			c.mu.RLock()
			if isDebugLoggingEnabled() {
				wsLogger.Debugf(">>> mu.RLock() #%d  caller=%s  [heartbeatLoop]", nextLockSeq(), getCallerFuncName(2))
			}
			respChLen := len(c.responseCh)
			c.mu.RUnlock()
			if isDebugLoggingEnabled() {
				wsLogger.Debugf("<<< mu.RUnlock() #%d  caller=%s  [heartbeatLoop]", nextLockSeq(), getCallerFuncName(2))
			}
			wsLogger.Debugf("heartbeat [this goroutine]: totalGoroutines=%d, activeReadLoops=%d, activeHeartbeatLoops=%d, activeHandleConns=%d, responseCh=%d",
				runtime.NumGoroutine(), c.activeReadLoops.Load(), c.activeHeartbeatLoops.Load(), c.activeHandleConns.Load(), respChLen)
		}
	}
}

func (c *WSClient) writeRaw(conn *websocket.Conn, messageType int, data []byte) error {
	seq := nextLockSeq()
	c.writeMu.Lock()
	wsLogger.Debugf(">>> writeMu.Lock() #%d  caller=%s  [writeRaw]", seq, getCallerFuncName(2))
	defer func() {
		c.writeMu.Unlock()
		wsLogger.Debugf("<<< writeMu.Unlock() #%d  caller=%s  [writeRaw]", seq, getCallerFuncName(2))
	}()
	if conn == nil {
		return fmt.Errorf("nil connection")
	}
	conn.SetWriteDeadline(time.Now().Add(WriteWait))
	if messageType == websocket.PingMessage {
		return conn.WriteControl(websocket.PingMessage, data, time.Now().Add(WriteWait))
	}
	return conn.WriteMessage(messageType, data)
}

func (c *WSClient) SendAndWait(action string, params map[string]any, timeout time.Duration) (*WSResponse, error) {
	echo := fmt.Sprintf("%s:%d", action, time.Now().UnixNano())
	ch := make(chan *WSResponse, 1)
	c.mu.Lock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf(">>> mu.Lock() #%d  caller=%s  [SendAndWait reg]", nextLockSeq(), getCallerFuncName(2))
	}
	c.responseCh[echo] = ch
	responseChLen := len(c.responseCh)
	c.mu.Unlock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf("<<< mu.Unlock() #%d  caller=%s  [SendAndWait reg]", nextLockSeq(), getCallerFuncName(2))
	}

	// 调试日志：当 responseCh 堆积时发出警告
	if responseChLen >= 5 {
		wsLogger.Warnf("SendAndWait: action=%s, responseCh pending=%d", action, responseChLen)
	}

	defer func() {
		c.mu.Lock()
		if isDebugLoggingEnabled() {
			wsLogger.Debugf(">>> mu.Lock() #%d  caller=%s  [SendAndWait cleanup]", nextLockSeq(), getCallerFuncName(2))
		}
		if _, ok := c.responseCh[echo]; ok {
			close(ch)
			delete(c.responseCh, echo)
		}
		c.mu.Unlock()
		if isDebugLoggingEnabled() {
			wsLogger.Debugf("<<< mu.Unlock() #%d  caller=%s  [SendAndWait cleanup]", nextLockSeq(), getCallerFuncName(2))
		}
	}()
	req := map[string]any{"action": action, "params": params, "echo": echo}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := c.SendRawData(data); err != nil {
		return nil, err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connection closed")
		}
		if resp.Status == "ok" || resp.Retcode == 0 {
			return resp, nil
		}
		return resp, fmt.Errorf("api error: %s", resp.Message)
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout")
	}
}

func (c *WSClient) SendRawData(data []byte) error {
	c.mu.RLock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf(">>> mu.RLock() #%d  caller=%s  [SendRawData]", nextLockSeq(), getCallerFuncName(2))
	}
	conn := c.conn
	c.mu.RUnlock()
	if isDebugLoggingEnabled() {
		wsLogger.Debugf("<<< mu.RUnlock() #%d  caller=%s  [SendRawData]", nextLockSeq(), getCallerFuncName(2))
	}
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	return c.writeRaw(conn, websocket.TextMessage, data)
}
