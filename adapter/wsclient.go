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
	defer c.mu.Unlock()
	c.isConnected = false
	// Clean up response channels
	for echo, ch := range c.responseCh {
		close(ch)
		delete(c.responseCh, echo)
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	if c.httpServer != nil {
		c.httpServer.Shutdown(context.Background())
		c.httpServer = nil
	}
	return nil
}

func (c *WSClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isConnected
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
	c.conn = conn
	c.isConnected = true
	c.reconnectCnt = 0
	c.mu.Unlock()
	c.handleConnection(conn)
	return nil
}

func (c *WSClient) handleConnection(conn *websocket.Conn) {
	readErrChan := make(chan error, 1)
	defer func() {
		if r := recover(); r != nil {
			wsLogger.Errorf("handleConnection panic: %v", r)
		}
		conn.Close()
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
			c.isConnected = false
			for echo, ch := range c.responseCh {
				close(ch)
				delete(c.responseCh, echo)
			}
		}
		c.mu.Unlock()
	}()

	go c.readLoop(conn, readErrChan)
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
			return
		case err := <-readErrChan:
			if err != nil {
				wsLogger.Errorf("Read error: %v", err)
			}
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
	defer func() {
		if r := recover(); r != nil {
			wsLogger.Errorf("Read loop panic: %v", r)
			select {
			case errChan <- fmt.Errorf("panic: %v", r):
			case <-c.stopChan:
			}
		}
	}()

	conn.SetReadLimit(MaxMessageSize)
	conn.SetReadDeadline(time.Now().Add(PongWait))
	conn.SetPongHandler(func(string) error {
		defer func() {
			if r := recover(); r != nil {
				wsLogger.Errorf("Pong handler panic: %v", r)
			}
		}()
		conn.SetReadDeadline(time.Now().Add(PongWait))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			select {
			case errChan <- err:
			case <-c.stopChan:
				return
			default:
				wsLogger.Warnf("Read error (channel full): %v", err)
			}
			return
		}
		c.handleMessage(message)
	}
}

func (c *WSClient) handleMessage(message []byte) {
	var resp WSResponse
	if err := json.Unmarshal(message, &resp); err == nil && resp.Echo != "" {
		c.mu.Lock()
		if ch, exists := c.responseCh[resp.Echo]; exists {
			delete(c.responseCh, resp.Echo)
			select {
			case ch <- &resp:
			default:
			}
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
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
			if err := c.writeRaw(conn, websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func (c *WSClient) writeRaw(conn *websocket.Conn, messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
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
	c.responseCh[echo] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if _, ok := c.responseCh[echo]; ok {
			close(ch)
			delete(c.responseCh, echo)
		}
		c.mu.Unlock()
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
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	return c.writeRaw(conn, websocket.TextMessage, data)
}
