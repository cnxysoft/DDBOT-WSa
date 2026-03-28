package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var wsLogger = logrus.WithField("module", "wsclient")

const (
	WSModeServer   = "ws-server"
	WSModeReverse  = "ws-reverse"
	WriteWait      = 10 * time.Second
	PongWait       = 60 * time.Second
	PingPeriod     = 50 * time.Second
	MaxMessageSize = 512 * 1024
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
	mu           sync.RWMutex
	mode         string
	wsMode       string
	url          string
	token        string
	conn         *websocket.Conn
	stopChan     chan struct{}
	responseCh   map[string]chan *WSResponse
	messageChan  chan []byte
	readErrChan  chan error
	isConnected  bool
	reconnectCnt int
	maxReconnect int

	// ws-server 模式
	httpServer *http.Server
	handler    http.HandlerFunc

	// 回调
	messageHandler func([]byte)

	// 配置
	heartbeatInterval time.Duration
	connectTimeout    time.Duration
}

type WSClientOption func(*WSClient)

func WithWSToken(token string) WSClientOption {
	return func(c *WSClient) {
		c.token = token
	}
}

func WithWSHeartbeat(interval time.Duration) WSClientOption {
	return func(c *WSClient) {
		c.heartbeatInterval = interval
	}
}

func WithWSConnectTimeout(timeout time.Duration) WSClientOption {
	return func(c *WSClient) {
		c.connectTimeout = timeout
	}
}

func WithWSMaxReconnect(max int) WSClientOption {
	return func(c *WSClient) {
		c.maxReconnect = max
	}
}

func WithWSMessageHandler(handler func([]byte)) WSClientOption {
	return func(c *WSClient) {
		c.messageHandler = handler
	}
}

func NewWSClient(mode, wsMode, url string, opts ...WSClientOption) *WSClient {
	c := &WSClient{
		mode:              mode,
		url:               url,
		wsMode:            wsMode,
		stopChan:          make(chan struct{}),
		responseCh:        make(map[string]chan *WSResponse),
		messageChan:       make(chan []byte, 100),
		readErrChan:       make(chan error, 1),
		heartbeatInterval: 30 * time.Second,
		connectTimeout:    10 * time.Second,
		maxReconnect:      0, // 0 表示无限重连
		reconnectCnt:      0,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *WSClient) SetMessageHandler(handler func([]byte)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messageHandler = handler
}

func (c *WSClient) Start() error {
	switch c.wsMode {
	case WSModeServer:
		return c.startServer()
	case WSModeReverse:
		return c.startReverse()
	default:
		return fmt.Errorf("unknown ws mode: %s", c.mode)
	}
}

func (c *WSClient) Stop() error {
	close(c.stopChan)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	if c.httpServer != nil {
		c.httpServer.Shutdown(context.Background())
		c.httpServer = nil
	}

	wsLogger.Info("WebSocket client stopped")
	return nil
}

func (c *WSClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isConnected
}

func (c *WSClient) GetSelfID() int64 {
	return 0
}

func (c *WSClient) startServer() error {
	addr := c.url
	if addr == "" {
		addr = "0.0.0.0:15630"
	}

	c.handler = func(w http.ResponseWriter, r *http.Request) {
		if c.token != "" {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			auth = strings.TrimPrefix(auth, "Bearer ")
			if auth != c.token {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}
		}

		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			wsLogger.Errorf("WebSocket upgrade error: %v", err)
			return
		}

		c.mu.Lock()
		c.conn = conn
		c.isConnected = true
		c.reconnectCnt = 0
		c.mu.Unlock()

		wsLogger.Info("WebSocket client connected (ws-server mode)")
		c.handleConnection(conn)
	}

	c.httpServer = &http.Server{
		Addr:    addr,
		Handler: c.handler,
	}

	go func() {
		wsLogger.Infof("WebSocket server starting on ws://%s/ws", addr)
		if err := c.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			wsLogger.Errorf("WebSocket server error: %v", err)
		}
	}()

	return nil
}

func (c *WSClient) startReverse() error {
	go c.connectLoop()
	return nil
}

func (c *WSClient) connectLoop() {
	wsLogger.Debugf("connectLoop started, mode=%s", c.mode)
	for {
		select {
		case <-c.stopChan:
			wsLogger.Debugf("connectLoop stopped by stopChan")
			return
		default:
		}

		err := c.connect()
		if err == nil {
			// 连接成功，但可能后会断开
			wsLogger.Debugf("connectLoop: connect returned nil, connection may have been lost, continuing...")
			// 不要返回，继续尝试重连，但添加短暂延迟防止紧密循环
			select {
			case <-c.stopChan:
				wsLogger.Debugf("connectLoop stopped by stopChan")
				return
			case <-time.After(2 * time.Second):
			}
			continue
		} else {
			wsLogger.Errorf("WebSocket connection failed: %v", err)
		}

		if c.maxReconnect > 0 && c.reconnectCnt >= c.maxReconnect {
			wsLogger.Errorf("Max reconnect attempts reached")
			return
		}

		c.reconnectCnt++
		waitTime := time.Duration(c.reconnectCnt) * 5 * time.Second
		if waitTime > 60*time.Second {
			waitTime = 60 * time.Second
		}

		wsLogger.Infof("Reconnecting in %v (attempt %d)", waitTime, c.reconnectCnt)
		select {
		case <-c.stopChan:
			wsLogger.Debugf("connectLoop stopped by stopChan during wait")
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

	wsLogger.Infof("Connecting to WebSocket: %s", c.url)

	dialer := websocket.Dialer{
		HandshakeTimeout: c.connectTimeout,
	}

	conn, _, err := dialer.Dial(c.url, header)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.isConnected = true
	c.reconnectCnt = 0
	c.mu.Unlock()

	wsLogger.Info("WebSocket connected (ws-reverse mode)")
	c.handleConnection(conn)

	return nil
}

func (c *WSClient) handleConnection(conn *websocket.Conn) {
	defer func() {
		c.mu.Lock()
		c.isConnected = false
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()

		conn.Close()
		wsLogger.Info("WebSocket connection closed")

		// ws-reverse 模式断线重连
		if c.mode == WSModeReverse {
			wsLogger.Debugf("Connection closed, starting reconnect loop")
			go c.connectLoop()
		}
	}()

	// 启动读消息协程
	go c.readLoop(conn)

	// 心跳
	if c.heartbeatInterval > 0 {
		go c.heartbeatLoop(conn)
	}

	// 处理消息
	for {
		select {
		case <-c.stopChan:
			return
		case msg := <-c.messageChan:
			c.writeMessage(conn, msg)
		case err := <-c.readErrChan:
			if err != nil {
				wsLogger.Errorf("Read error: %v", err)
			}
			return
		}
	}
}

func (c *WSClient) readLoop(conn *websocket.Conn) {
	conn.SetReadLimit(MaxMessageSize)
	conn.SetReadDeadline(time.Now().Add(PongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(PongWait))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			select {
			case c.readErrChan <- err:
			default:
			}
			return
		}

		c.handleMessage(message)
	}
}

func (c *WSClient) handleMessage(message []byte) {
	// 尝试解析响应
	var resp WSResponse
	if err := json.Unmarshal(message, &resp); err == nil {
		// 如果有 echo，说明是 API 响应
		if resp.Echo != "" {
			c.mu.RLock()
			ch, exists := c.responseCh[resp.Echo]
			c.mu.RUnlock()

			if exists {
				select {
				case ch <- &resp:
				default:
					wsLogger.Warnf("Response channel full for echo: %s", resp.Echo)
				}
			} else {
				wsLogger.Tracef("Received response without handler: %s", resp.Echo)
			}
			return
		}
	}

	// 传递给消息处理器
	c.mu.RLock()
	handler := c.messageHandler
	c.mu.RUnlock()

	if handler != nil {
		handler(message)
	}
}

func (c *WSClient) heartbeatLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			c.mu.RLock()
			isConn := c.conn == conn
			c.mu.RUnlock()

			if isConn {
				err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(WriteWait))
				if err != nil {
					wsLogger.Warnf("Heartbeat error: %v", err)
				}
			}
		}
	}
}

func (c *WSClient) writeMessage(conn *websocket.Conn, message []byte) {
	err := conn.WriteMessage(websocket.TextMessage, message)
	if err != nil {
		wsLogger.Errorf("Write message error: %v", err)
	}
}

func (c *WSClient) Send(action string, params map[string]any) error {
	req := map[string]any{
		"action": action,
		"params": params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %v", err)
	}

	c.mu.RLock()
	conn := c.conn
	isConn := c.isConnected
	c.mu.RUnlock()

	if !isConn || conn == nil {
		return fmt.Errorf("not connected")
	}

	c.writeMessage(conn, data)
	return nil
}

func (c *WSClient) SendAndWait(action string, params map[string]any, timeout time.Duration) (*WSResponse, error) {
	echo := fmt.Sprintf("%s:%d", action, time.Now().UnixNano())

	req := map[string]any{
		"action": action,
		"params": params,
		"echo":   echo,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	ch := make(chan *WSResponse, 1)

	c.mu.Lock()
	c.responseCh[echo] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.responseCh, echo)
		c.mu.Unlock()
	}()

	c.mu.RLock()
	conn := c.conn
	isConn := c.isConnected
	c.mu.RUnlock()

	if !isConn || conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	c.writeMessage(conn, data)

	select {
	case resp := <-ch:
		if resp.Status == "ok" || resp.Retcode == 0 {
			return resp, nil
		}
		errMsg := resp.Wording
		if resp.Message != "" {
			errMsg = resp.Message
		} else if resp.Msg != "" {
			errMsg = resp.Msg
		}
		return resp, fmt.Errorf("%s", errMsg)
	case <-time.After(timeout):
		return nil, fmt.Errorf("%s timeout", action)
	}
}
