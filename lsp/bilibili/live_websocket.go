package bilibili

import (
	"bytes"
	"compress/zlib"
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// 登录账号直播弹幕 WebSocket（Lite 重写）：
// getDanmuInfo（wbi 签名 + 登录 Cookie）换取 Token 与 wss 主机列表，
// 连 wss://host:port/sub 完成鉴权后常驻；心跳 30s；收到 LIVE/PREPARING
// 状态命令时经批量 API 复核后走既有事件管线推送。
// -352 风控：暂停新建连接 3 分钟，in-flight（pending）列表转批量 API 兜底轮询（20s 间隔）；
// 无直播间 UID：30 分钟兜底重新解析（roomRetry）。
// TODO(lite): 监督状态机内部细节（重连退避常数、retry_uuid 策略）
// 未能从二进制完整复原，按可观察行为等价重建。
// auth 包体（scene="room"/type=2/protover=3/support_ack=true）已由 @0xd35140 与
// rodata 0x1142338 模板复原，见 makeAccountLiveAuthPayload。

const (
	liveWebsocketProtoVersion = 3

	liveWebsocketOpHeartbeat      = 2
	liveWebsocketOpHeartbeatReply = 3
	liveWebsocketOpMessage        = 5
	liveWebsocketOpAuth           = 7
	liveWebsocketOpAuthReply      = 8

	// liveWebsocketMaxDecompressedSize 是单帧解压后的体积上限。成品 decompressLiveProtocol
	// 的 io.LimitReader(reader, 0x2000000+1)（@0xd36687 movq $0x2000001）与
	// len>0x2000000 判错（@0xd36700 jg）都是 32MiB。
	liveWebsocketMaxDecompressedSize = 32 << 20
	// liveWebsocketMaxCompressionDepth 是压缩嵌套上限。成品 decodeLiveProtocolDepth 入口
	// @0xd35367 cmp $0x4,%rdi; jg → 深度 >4 才报「压缩嵌套过深」。
	liveWebsocketMaxCompressionDepth = 4
	// liveWebsocketMaxFrameSize 是单帧 packLen 上限。成品 decodeLiveProtocolDepth
	// @0xd35529 cmp $0x1000000,%r15; jg 与 serve SetReadLimit（@0xd33c39
	// movq $0x1000000,0x100(%rcx)）同为 16MiB。
	liveWebsocketMaxFrameSize     = 16 << 20
	liveWebsocketPacketHeaderSize = 16
	// liveWebsocketCompressionNone 是顶层压缩链初值。成品 serve 调 decodeLiveProtocolDepth
	// 时 rsi:r8=0x1099577 "none"（len 4，@0xd3406e/@0xd34075）。
	liveWebsocketCompressionNone = "none"

	// liveWebsocketPacketProtoVersion 是客户端发送包（auth/heartbeat）头部的 version 字段。
	// 成品 serveAccountLiveWebsocket auth 打包 @0xd33dd3 与心跳 func3.1 @0xd34f85 均写 0x100
	// （大端 version=1）；与 auth JSON 里的 protover=3 是两个不同的字段。
	liveWebsocketPacketProtoVersion = 1
	// liveWebsocketHeartbeatBody 是心跳包体。成品 func3.1 @0xd34fa7/@0xd34fb5 写入字面量
	// "[object Object]"，共 15B，与 16B 头相加 packLen=31。
	liveWebsocketHeartbeatBody = "[object Object]"

	PathGetDanmuInfo = "https://api.live.bilibili.com/xlive/web-room/v1/index/getDanmuInfo"
	PathFingerSPI    = "https://api.bilibili.com/x/frontend/finger/spi"

	liveWebsocketHeartbeatInterval = 30 * time.Second
	// liveWebsocketReadDeadline 是 WS 读超时。
	// 成品常量 0x14f46b0400（=90s）出现三处：serve 建连后 @0xd33c49、读取循环中每次
	// ReadMessage 成功后 @0xd33fd3，以及 pong handler func2 @0xd35057。
	liveWebsocketReadDeadline = 90 * time.Second
	// liveWebsocketHeartbeatWriteDeadline 是心跳写超时。
	// 成品 func3.1 @0xd34f25 常量 0x2540be400（=10s），写 conn.writeDeadline（*websocket.Conn 偏移
	// 0x60，见 gorilla SetWriteDeadline），随后 WriteMessage 心跳帧。
	liveWebsocketHeartbeatWriteDeadline = 10 * time.Second
	liveWebsocketRiskControlPause       = 3 * time.Minute
	liveWebsocketNoRoomRetryInterval    = 30 * time.Minute
	// liveWebsocketNoRoomRetryFastInterval 是批量直播状态加载失败时的快速重试间隔。
	// 成品 reconcile 0xd25eec 常量 0x45d964b800（=5min），成功时用 0x1a3185c5000（=30min）。
	liveWebsocketNoRoomRetryFastInterval = 5 * time.Minute
	// liveWebsocketFallbackPollInterval 是 in-flight 兜底轮询的最小间隔。
	// 成品 pollRiskFallbackIfDue @0xd2a1df/@0xd2a1e9 与 reconcile @0xd27ccf/@0xd27cd9
	// 均以 time.Time.Add(now, 0x4a817c800=20s) 写 nextFallbackPoll（m+0x68）。
	liveWebsocketFallbackPollInterval = 20 * time.Second
	liveWebsocketReconcileInterval    = time.Minute
	liveWebsocketConnectStageTimeout  = 45 * time.Second
	// liveWebsocketHandshakeTimeout 是 WS 拨号握手超时。成品 serve @0xd33482 复制的
	// websocket.Dialer 静态模板 0x11435a0（128B）仅 +0x28(HandshakeTimeout)=0x37e11d600
	// （=15s），其余字段（含 Proxy）全为 0；复制后无字段覆写。
	liveWebsocketHandshakeTimeout     = 15 * time.Second
	liveWebsocketSignalQueueSize      = 64
	liveWebsocketEventQueueSize       = 64
	liveWebsocketInitialRetryInterval = 5 * time.Second
	liveWebsocketMaxSignalRetry       = 5

	// liveWebsocketConnectInitialDelay / liveWebsocketConnectMaxDelay /
	// liveWebsocketConnectStableThreshold 是 runAccountLiveWebsocket 的断线重连退避参数。
	// 成品入口 @0xd2de2d 置 delay=0x77359400（=2s）；每段失败后 @0xd2e5f9/@0xd2ea3e/
	// @0xd2f4fb 均 delay*=2，再与 0xdf8475800（=60s）比较并 @0xd2e606/@0xd2ea4c/@0xd2f510
	// cmovl 封顶。仅 serve 失败路径额外在 @0xd2f4e5 与 0x6fc23ac00（=30s）比较
	// time.Since(建连时刻)：>=30s（连接稳定）→ @0xd2f4f4 重置为 2s。
	liveWebsocketConnectInitialDelay    = 2 * time.Second
	liveWebsocketConnectMaxDelay        = 60 * time.Second
	liveWebsocketConnectStableThreshold = 30 * time.Second
)

// liveDanmuAPIError 是 getDanmuInfo 业务错误。
type liveDanmuAPIError struct {
	Code    int
	Message string
}

// Error 实现 error 接口。
func (e *liveDanmuAPIError) Error() string {
	// 成品 (*liveDanmuAPIError).Error @0xd23b1d：fmt.Sprintf("getDanmuInfo失败: code=%d message=%s")（len 0x26）。
	return fmt.Sprintf("getDanmuInfo失败: code=%d message=%s", e.Code, e.Message)
}

// liveDanmuHost 是弹幕服务器主机。
type liveDanmuHost struct {
	Host    string `json:"host"`
	WSSPort int    `json:"wss_port"`
}

// liveDanmuInfoResponse 是 getDanmuInfo 响应。
type liveDanmuInfoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Token    string          `json:"token"`
		HostList []liveDanmuHost `json:"host_list"`
	} `json:"data"`
}

// bilibiliBuvidResponse 是 finger/spi 响应。
type bilibiliBuvidResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Buvid3 string `json:"b_3"`
	} `json:"data"`
}

// liveWebsocketAuthPayload 是 WS 鉴权包体。
// 成品类型闭包 recovered/types/types-closure-x64.tsv:10734：
//
//	struct 120 bilibili.liveWebsocketAuthPayload: UID int64 `json:"uid"`;
//	RoomID int64 `json:"roomid"`; ProtoVer int `json:"protover"`; Buvid string `json:"buvid"`;
//	SupportAck bool `json:"support_ack"`; QueueUUID string `json:"queue_uuid"`;
//	Scene string `json:"scene"`; Platform string `json:"platform"`; Type int `json:"type"`;
//	Key string `json:"key"`
//
// 字段偏移（120B）：UID 0x00 / RoomID 0x08 / ProtoVer 0x10 / Buvid 0x18 / SupportAck 0x28 /
// QueueUUID 0x30 / Scene 0x40 / Platform 0x50 / Type 0x60 / Key 0x68。
type liveWebsocketAuthPayload struct {
	UID        int64  `json:"uid"`
	RoomID     int64  `json:"roomid"`
	ProtoVer   int    `json:"protover"`
	Buvid      string `json:"buvid"`
	SupportAck bool   `json:"support_ack"`
	QueueUUID  string `json:"queue_uuid"`
	Scene      string `json:"scene"`
	Platform   string `json:"platform"`
	Type       int    `json:"type"`
	Key        string `json:"key"`
}

// liveWebsocketCredential 是 WS 建连所需凭据快照。
// 成品证据（9 字 = 72B，UID 在最前）：
//   - currentLiveWebsocketCredential(live_websocket.go:1085, @0xd2f9e0) 结果经 rax..r11 共 9 寄存器
//     返回，error 接口写调用者栈 0(rsp)/8(rsp)（callee 0x158/0x160）；返回构造 @0xd2feb4..0xd2ff0d：
//     word0=uid(@0x58(%rsp))，word1..8=SESSDATA/BiliJct(来自 VerifyInfo)/DedeUserID(=FormatInt(uid,10))/
//     Buvid3（来自 fetchBilibiliBuvid3）。
//   - cookieHeader(live_websocket.go:1231, @0xd319e0) 值接收者入口保存 rax..r11（9 字），只用
//     word1..8 调 applyBilibiliCookies——证明接收者 9 字且首字非 cookie 字段。
//   - 调用点 runAccountLiveWebsocket +0x4f9 的 9 字返回先暂存 0x3d8(%rsp)，+0x51d 起整块
//     拷到 0x200(%rsp)，+0x985 直接交给 getAccountLiveDanmuInfo；凭据段无日志
//     （"auth_uid"(0x10a4a4e) 是 +0x1041 新Token Debug 的字段键，不是此处日志）。
type liveWebsocketCredential struct {
	UID        int64
	SESSDATA   string
	BiliJct    string
	DedeUserID string
	Buvid3     string
}

// cookieHeader 取 WS 握手 Cookie 头。
// 成品 live_websocket.go:1231（352B, @0xd319e0，值接收者 9 字）反汇编：
// http.NewRequestWithContext(Bg, GET, "https://live.bilibili.com/", nil) +
// applyBilibiliCookies(req, c 的 cookie 部分——只装 rbx..r11 共 4 个 string，丢弃 word0=UID)，
// 再 req.Header.Get("Cookie")（0x109f70c,6="Cookie"）。
func (c liveWebsocketCredential) cookieHeader() string {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://live.bilibili.com/", nil)
	applyBilibiliCookies(req, c.SESSDATA, c.BiliJct, c.DedeUserID, c.Buvid3)
	return req.Header.Get("Cookie")
}

// liveWebsocketSignal 是跨协程的直播状态信号。
type liveWebsocketSignal struct {
	Mid    int64
	RoomID int64
	Cmd    string
	Source string
	Retry  int
}

// liveWebsocketListener 是单个 UID 的连接监听句柄。
type liveWebsocketListener struct {
	cancel   context.CancelFunc
	stopping bool
}

// liveWebsocketStartRequest 是一次建连请求。
type liveWebsocketStartRequest struct {
	Mid  int64
	User *UserInfo
}

// liveWebsocketInitialResult 是建连阶段的结果回报。
type liveWebsocketInitialResult struct {
	Mid     int64
	RoomID  int64
	Success bool
}

// liveProtocolCommand 是一帧解析后的协议命令。
// 成品布局（decodeLiveProtocolDepth op5 构造点 @0xd35749..@0xd35918，元素 72B）：
//
//	Raw string@0; Base string@16; Version uint16@32; Operation uint32@36;
//	Compression string@40; CompressionDepth int@56; PayloadBytes int@64
//
// 字段语义：
//   - Raw：op5 帧 JSON{cmd} 经 strings.TrimSpace 后的整串；
//   - Base：Raw 以 ":" strings.SplitN(_, ":", 2) 的前缀（无冒号时即 Raw）；
//   - Version/Operation：帧头大端 version(uint16)/operation(uint32)；
//   - Compression：压缩链，顶层 "none"，每层以 "zlib"/"brotli" 追加（"/" 连接）；
//   - CompressionDepth：解码深度（顶层 0）；
//   - PayloadBytes：帧体长度（不含 16B 头）。
type liveProtocolCommand struct {
	Raw              string
	Base             string
	Version          uint16
	Operation        uint32
	Compression      string
	CompressionDepth int
	PayloadBytes     int
}

// liveWebsocketManager 是登录账号直播 WS 的监督者。
type liveWebsocketManager struct {
	concern          *Concern
	mu               sync.Mutex
	listeners        map[int64]*liveWebsocketListener
	riskControlUntil time.Time
	signals          chan liveWebsocketSignal
	wg               sync.WaitGroup
	roomRetry        map[int64]time.Time
	noRoom           map[int64]struct{}
	// seeded 在成品类型里存在（types-closure-x64.tsv:10791），但本 build 反汇编只看到
	// reconcile 对它的裁剪（@0xd24edf）与 loop A 的读取（@0xd25389），未找到任何
	// mapassign 写入点；重建不再用它做「首轮」判定，保留字段以对齐类型布局。
	seeded           map[int64]struct{}
	pending          map[int64]*UserInfo
	nextFallbackPoll time.Time
}

var (
	// liveWebsocket 单例监督者，由 Init 装配。
	liveWebsocket     *liveWebsocketManager
	liveWebsocketOnce sync.Once
	liveWebsocketStop = make(chan struct{})
	liveWebsocketWg   sync.WaitGroup

	// liveWebsocketUIDs 是 UID 连接位（in-flight 去重）。
	// 成品 claimLiveWebsocketUID @0xd2f920 对全局 map(0x1bd0300) 调
	// sync.(*HashTrieMap[any,any]).LoadOrStore；releaseLiveWebsocketUID @0xd2f980 与
	// runAccountLiveWebsocket.deferwrap1 @0xd2f8c0 调 HashTrieMap.LoadAndDelete。
	// 重建用 sync.Map 等价承载（go1.24+ sync.Map 内部即 HashTrieMap）。
	liveWebsocketUIDs sync.Map

	// liveWebsocketCredentialMu 串行化凭据装配。
	// 成品 currentLiveWebsocketCredential(@0xd2f9e0) 入口 lock cmpxchg 0x1bf4500，
	// 该地址与 GetVerifyInfo 用的 mux(0x1bcc2a0) 不同，是凭据装配专用锁。
	liveWebsocketCredentialMu sync.Mutex

	// liveWebsocketEventChan 是 WS 信号经 API 复核后的事件桥。
	liveWebsocketEventChan = make(chan concern.Event, liveWebsocketEventQueueSize)

	// liveWebsocketStopOnce 保证停止信号只关闭一次。
	liveWebsocketStopOnce sync.Once
)

// newLiveWebsocketManager 创建监督者。
func newLiveWebsocketManager(c *Concern) *liveWebsocketManager {
	m := &liveWebsocketManager{
		concern:   c,
		listeners: make(map[int64]*liveWebsocketListener),
		signals:   make(chan liveWebsocketSignal, liveWebsocketSignalQueueSize),
		roomRetry: make(map[int64]time.Time),
		noRoom:    make(map[int64]struct{}),
		seeded:    make(map[int64]struct{}),
		pending:   make(map[int64]*UserInfo),
	}
	return m
}

// startLiveWebsocket 启动监督协程（幂等）。
// 成品证据：(*liveWebsocketManager).run(live_websocket.go:198) 是
// func(context.Context) 形式——由 (*Concern).Start.(*Concern).fresh.func4.3
// (concern_fresher.go:305, @0xd05800) 从闭包字段 [0x18]:[0x20] 取 ctx（rbx:rcx）后 go run(ctx)；
// run 再把同一 ctx 传 reconcile(ctx)（+0x767/+0x849 两处调用点均 rbx:rcx=ctx）。
func startLiveWebsocket(ctx context.Context, c *Concern) {
	liveWebsocketOnce.Do(func() {
		liveWebsocket = newLiveWebsocketManager(c)
		liveWebsocketWg.Add(1)
		go liveWebsocket.run(ctx)
	})
}

// stopLiveWebsocket 停止监督协程并断开全部连接。
func stopLiveWebsocket() {
	liveWebsocketStopOnce.Do(func() {
		close(liveWebsocketStop)
	})
	if liveWebsocket != nil {
		liveWebsocket.stopAll()
	}
}

// run 是监督者主循环：建连调度 + 风控兜底轮询。
// ctx 来自 startLiveWebsocket（成品 fresh.func4.3 闭包捕获的会话 ctx），下传 reconcile。
func (m *liveWebsocketManager) run(ctx context.Context) {
	defer m.runDeferred()
	// 成品 run(live_websocket.go:198, @0xd241bc) 启动日志：
	//   logrus.(*Entry).Logf(level=4=Info, "B站直播WebSocket当前模式：%s", mode)。
	// mode 由 VerifyInfo 的 SESSDATA/bili_jct 是否非空决定（@0xd23cf1/@0xd23d0f 两次
	// strings.TrimSpace）：真 → "带Cookie"(@0x10a5fba,len 9)，
	// 假 → "带Cookie（等待登录凭据，不回退匿名）"(@0x10de554,len 51)。
	credentialReady := false
	if verify := getVerify(); verify != nil &&
		strings.TrimSpace(verify.SESSDATA) != "" &&
		strings.TrimSpace(verify.BiliJct) != "" {
		credentialReady = true
	}
	mode := "带Cookie（等待登录凭据，不回退匿名）"
	if credentialReady {
		mode = "带Cookie"
	}
	logger.WithFields(logrus.Fields{
		"mode":             mode,
		"cookie_source":    cfg.GetBilibiliCookieMode(),
		"credential_ready": credentialReady,
	}).Infof("B站直播WebSocket当前模式：%s", mode)
	// 信号分发与停止监控独立成两个后台任务
	go m.dispatchSignals()
	go m.watchStop()
	ticker := time.NewTicker(liveWebsocketReconcileInterval)
	defer ticker.Stop()
	for {
		m.reconcile(ctx)
		m.pollRiskFallbackIfDue(ctx)
		select {
		case <-liveWebsocketStop:
			return
		case <-ticker.C:
		}
	}
}

// runDeferred 恢复 run 循环的恐慌并释放计数。
func (m *liveWebsocketManager) runDeferred() {
	m.wg.Done()
	if e := recover(); e != nil {
		logger.Errorf("liveWebsocket run panic: %v", e)
	}
}

// watchStop 在停止信号到达时回收全部监听协程。
func (m *liveWebsocketManager) watchStop() {
	<-liveWebsocketStop
	m.stopAll()
}

// reconcile 一轮建连调度：
// 全量列出直播订阅 → 每轮对 loop A 候选跑批量 API 状态 → 按风控/无房间决定是否发起
// WS 建连（pending 作为 in-flight 集合）→ 统计连接阶段结果。
// 成品 (*liveWebsocketManager).reconcile(live_websocket.go:247, @0xd24900) 签名实证：
// 接收者 rax，ctx context.Context = rbx:rcx（入口 0x1918/0x1920）；+0x3923 (@0xd28223)
// 调 context.WithCancel(parent=该 ctx)，派生 ctx（rax:rbx）+ cancel（rcx）；派生 ctx 被
// go 闭包 func4(@0xd28cc0) 捕获并以 rax:rbx 原样传给 runAccountLiveWebsocket(@0xd2dce0)。
func (m *liveWebsocketManager) reconcile(ctx context.Context) {
	defer func() {
		if e := recover(); e != nil {
			logger.Errorf("刷新B站直播WebSocket监听列表失败: %v", e)
		}
	}()
	mids, err := m.liveSubscriptionMids()
	if err != nil {
		logger.Errorf("刷新B站直播WebSocket监听列表失败: %v", err)
		return
	}
	// 成品 reconcile @0xd24d42：订阅集合里已消失的 UID 立即停 listener，
	//   logrus.(*Entry).Log(level=4=Info, WithField("mid", mid),
	//     "正在停止B站直播登录账号WebSocket：该主播已无直播订阅")（@0x1125690）。
	// 同时按 @0xd24f7f 起对 pending(m+0x60) 的 mapdelete 裁剪 in-flight 集合。
	subscribed := make(map[int64]struct{}, len(mids))
	for _, mid := range mids {
		subscribed[mid] = struct{}{}
	}
	m.mu.Lock()
	for mid, listener := range m.listeners {
		if _, ok := subscribed[mid]; ok {
			continue
		}
		logger.WithField("mid", mid).Info("正在停止B站直播登录账号WebSocket：该主播已无直播订阅")
		listener.stopping = true
		if listener.cancel != nil {
			listener.cancel()
		}
		delete(m.listeners, mid)
	}
	for mid := range m.pending {
		if _, ok := subscribed[mid]; ok {
			continue
		}
		delete(m.pending, mid)
	}
	m.mu.Unlock()
	// 批量直播状态加载：成品 reconcile @0xd85（@0xd25685）每轮都执行，不是仅首轮。
	// loop A（@0xd24f7f~@0xd253e8）收集候选：订阅中、无监听、不在 pending、roomRetry 未到期；
	// @0xd25641 取候选 len(0x1d0)，@0xd25649 为 0 则跳过，@0xd25665 把候选切片交给
	// GetLiveStatusByUIDs（ctx 来自 reconcile 入口 0x1918/0x1920）。
	now := time.Now()
	candidates := m.liveWebsocketBatchCandidates(mids, now)
	// batchStatusOK 对应成品 reconcile 的 flag@0x46(%rsp)（见 live_websocket.go:349 写入、
	// :388 读取）：批量直播状态加载“未失败”时为 true。成品 @0xd2568d 对 GetLiveStatusByUIDs 的
	// error 做 nil 判定后 sete 得到该标记，用于 noRoom 命中时选择 roomRetry 刷新时长。
	batchStatusOK := true
	if len(candidates) > 0 {
		results, batchErr := GetLiveStatusByUIDs(ctx, candidates)
		if batchErr == nil {
			m.applyFallbackLiveStatus(results, false)
			logger.Infof("B站直播批量API状态加载完成：候选%d", len(candidates))
		} else {
			batchStatusOK = false
			logger.WithField("error", batchErr).Warn("批量获取B站直播间状态失败，将按UID已有分类选择兜底或提前重试")
		}
	}
	// 成品 reconcile 的循环前状态：riskControl = now 是否在风控冷却期（@0xd298c0 reportRiskControl
	// 把 riskControlUntil 推到 now+180s）；两个计数器用于连接阶段日志。
	riskControl := now.Before(m.riskControlDeadline())
	riskWaiting, longPolling := 0, 0
	// 逐候选 UID 决定建连动作。成品第二循环遍历的是 loop A 产出的候选切片，不是全部 mids：
	// @0xd25cf1 取 0x1d0（候选 len）<<3，@0xd25d22 memmove 把 0x3c8（候选 ptr）拷到 0x420，
	// 循环上界读 0x208（= 候选 len）；listeners/pending/未到期 roomRetry 已在 loop A 过滤。
	for _, mid := range candidates {
		m.mu.Lock()
		if _, waiting := m.noRoom[mid]; waiting {
			m.mu.Unlock()
			// 成品 reconcile live_websocket.go:381-389（noRoom 命中）：
			//   @0xd25ecf mapaccess2_fast64(m+0x50=noRoom) 命中；
			//   @0xd25ee0 time.Now；@0xd25eec/@0xd25ef6 取 5min/30min（flag@0x46=batchStatusOK）；
			//   @0xd25f0c time.Time.Add；@0xd25f80 mapassign_fast64(m+0x48=roomRetry) ← 新时刻。
			// 该分支不读 roomRetry（无 mapaccess m+0x48）、不调 resolveLiveRoom：到期过滤已在
			// loop A 完成，候选里的 roomRetry 要么缺失要么已到期，故等价于“无条件刷新”。
			m.refreshLiveWebsocketNoRoomRetry(mid, batchStatusOK)
			longPolling++
			continue
		}
		m.mu.Unlock()
		// 风控与连接位不再在此拦截：
		//   - claimLiveWebsocketUID 的成品唯一调用点是 runAccountLiveWebsocket +0x7a
		//     （cg callers=1），reconcile 内不 claim/release（releaseLiveWebsocketUID callers=0）；
		//   - 风控期仍登记 pending：成品 pending 填充 @0xd27c17 在 riskControlDeadline @0xd27c61
		//     之前，命中风控只跳过 pendingStartRequests/建连（@0xd27c69 je，未命中才到 +0x383a）。
		userInfo, err := m.concern.GetUserInfo(mid)
		if err != nil || userInfo == nil {
			// 缓存里没有该 UID 的主播信息时，用无 Cookie 的 Master/info + 公开名片双源解析。
			// 成品调用图证据：reconcile(live_websocket.go:247) 直接调用 resolveLiveRoom(RoomResolve.go:65)。
			resolution := resolveLiveRoom(mid)
			if resolution != nil {
				if resolution.User != nil {
					if addErr := m.concern.AddUserInfo(resolution.User); addErr != nil {
						logger.WithField("mid", mid).Warnf("保存B站直播UID解析结果失败: %v", addErr)
					}
				}
				switch resolution.Status {
				case liveRoomStatusRoomFound:
					// 成品 @0xd26620/@0xd26640：房间有效 → 清 noRoom/roomRetry。
					logger.WithField("mid", mid).Debug("批量接口未返回UID，但单独查询取得直播间号")
					m.clearLiveWebsocketNoRoom(mid)
				case liveRoomStatusAccountExistsWithoutRoom:
					// 成品 @0xd276b8/@0xd27720：账号存在但无房间 → 记 30 分钟兜底重试并跳过建连。
					logger.WithField("mid", mid).Debug("B站账号存在但当前没有直播间，保留订阅并降低频率复查")
					m.markLiveWebsocketNoRoom(mid)
					continue
				case liveRoomStatusAccountDeleted:
					// 成品 reconcile live_websocket.go:435-439（account_deleted 分支）：
					//   @0xd269b1/@0xd26a13 内联 (*StateManager).RemoveAllById(mid)
					//     （reconcile.(*StateManager).RemoveAllById.func5, concern/stateManager.go:261，
					//      入参 mid 以 interface{} 形态捕获；调用点即 (*buntdb.ShortCut).RWCoverTx @0xade3e0）；
					//   @0xd26a36/@0xd26a6b 内联 (*StateManager).ClearByMid(mid)
					//     （reconcile.(*StateManager).ClearByMid.func6, bilibili/stateManager.go:112；
					//      调用点同样 RWCoverTx）；
					//   @0xd26a80/@0xd26ab7/@0xd26ad7 再删本地快照 / roomRetry / noRoom。
					m.purgeDeletedAccountConcern(mid)
					m.clearLiveWebsocketNoRoom(mid)
				default:
					logger.WithField("mid", mid).Warn("暂时无法确认B站账号直播间，保留订阅并稍后重试")
				}
				if resolution.User != nil {
					userInfo = resolution.User
				}
			}
		}
		if userInfo == nil {
			continue
		}
		m.mu.Lock()
		m.pending[mid] = userInfo
		m.mu.Unlock()
		if riskControl {
			riskWaiting++
		}
	}
	if riskControl {
		logger.WithField("risk_waiting", riskWaiting).Debug("B站直播WebSocket仍在-352风控冷却期，等待列表继续使用API轮询")
		logger.Info("B站直播WebSocket已暂停继续建连，等待列表切换为批量API轮询")
	}
	// 取出待建连请求（不清空：成品 pendingStartRequests @0xd29660 无 mapdelete，
	// pending 的清理由「注册 listener 时删除」+ reconcile 对订阅集合的裁剪负责）。
	// 成品调用图证据：reconcile(live_websocket.go:247) 直接调用 pendingStartRequests(live_websocket.go:587)。
	// 风控期不建连，pending 保留给 pollRiskFallbackIfDue 兜底轮询。
	var startRequests []liveWebsocketStartRequest
	if !riskControl {
		startRequests = m.pendingStartRequests()
	}
	if len(startRequests) == 0 {
		return
	}
	// 连接阶段：并发发起，限时收集结果
	resultChan := make(chan liveWebsocketInitialResult, len(startRequests))
	for i, request := range startRequests {
		// 成品 reconcile @0xd2855e..@0xd28837：每个建连请求先打一条 Info 日志——
		//   logrus.(*Entry).WithFields({
		//     "progress": fmt.Sprintf("%d/%d", i+1, total)（Sprintf @0x51eb00，格式串
		//       "%d/%d" @0x109a861,len 5；i+1 来自循环下标 0x1b0+1，total=pendingStartRequests
		//       的 len 0x120）,
		//     "mid": req.Mid, "room_id": req.User.RoomId(+0x28), "name": req.User.Name(+0x8/0x10)
		//   }).Infof("正在连接B站直播间 %d/%d" @0x10c54ea,len 0x1f, i+1, total)
		// level=4=Info，调用 logrus.(*Entry).Logf(@0x56f740)，fmt 参数与 progress 同源。
		logger.WithFields(logrus.Fields{
			"progress": fmt.Sprintf("%d/%d", i+1, len(startRequests)),
			"mid":      request.Mid,
			"room_id":  request.User.RoomId,
			"name":     request.User.Name,
		}).Infof("正在连接B站直播间 %d/%d", i+1, len(startRequests))
		// 成品 +0x3923（@0xd28223）：context.WithCancel(reconcile 的 ctx) 在遍历 startRequests
		// 的循环体内执行，派生 ctx 被 go 闭包 func4 捕获后以 rax:rbx 原样传入
		// runAccountLiveWebsocket。重建按请求派生并在连接协程退出时回收（父 ctx 仍可整体取消）。
		reqCtx, cancel := context.WithCancel(ctx)
		// 成品 live_websocket.go:541-542：listener 在 reconcile 侧构造并注册——
		//   @0xd282f9 runtime.newobject(liveWebsocketListener) → @0xd28329 (*listener)=cancel；
		//   @0xd28347 mapassign_fast64(m+0x10=listeners, key=mid) ← *listener。
		listener := &liveWebsocketListener{cancel: cancel}
		m.mu.Lock()
		m.listeners[request.Mid] = listener
		// 成品 @0xd283a6/@0xd283b9：listener 注册后 mapdelete(m+0x60=pending)，该 UID 转入
		// 已建连状态，不再由 pending 兜底轮询覆盖。
		delete(m.pending, request.Mid)
		m.mu.Unlock()
		// 成品 live_websocket.go:547-550：
		//   :547 m.wg.Add(1)（@0xd283cf sync.(*WaitGroup).Add）；
		//   :548 go func(ctx context.Context, request liveWebsocketStartRequest,
		//        listener *liveWebsocketListener) {...}（func4 @0xd28cc0，类型见
		//        recovered/types types-closure-x64.tsv:4613；launch @0xd284d6 runtime.newproc）；
		//   :549 defer m.wg.Done()（func4.deferwrap1 @0xd28e60，捕获 &m.wg）；
		//   :550 defer m.releaseListener(mid, listener)（func4.deferwrap2 @0xd28e20，
		//        捕获 m/mid/listener 后调 @0xd2b3c0 releaseListener）。
		// runAccountLiveWebsocket 自身不再构造/注册 listener，也不再自行释放。
		m.wg.Add(1)
		go func(reqCtx context.Context, req liveWebsocketStartRequest, listener *liveWebsocketListener) {
			defer m.wg.Done()
			defer m.releaseListener(req.Mid, listener)
			// 成品 reconcile.func4 @0xd28d78..@0xd28d91 的 runAccount 调用只传 7 个寄存器：
			// ctx(rbx:rcx) / mid(rcx←req.Mid) / roomID(rdi←User+0x28) / name(rsi:r8←User+0x8/0x10)
			// / signals(r9←m+0x30) / resultChan(r10←闭包捕获) / reportRiskControl(r11←方法值)。
			// listener 不进 runAccount（由 func4 的 defer releaseListener 持有）。
			runAccountLiveWebsocket(reqCtx, req.Mid, req.User.RoomId, req.User.Name, m.signals, resultChan, m.reportRiskControl)
		}(reqCtx, request, listener)
	}
	success, failed := 0, 0
	deadline := time.After(liveWebsocketConnectStageTimeout)
	collected := 0
	results := make([]liveWebsocketInitialResult, 0, len(startRequests))
	for collected < len(startRequests) {
		select {
		case result := <-resultChan:
			collected++
			results = append(results, result)
			if result.Success {
				success++
			} else {
				failed++
			}
		case <-deadline:
			collected = len(startRequests)
		}
	}
	// 成品调用图证据：reconcile(live_websocket.go:247) 直接调用 logInitialConnectionSummary(live_websocket.go:726)。
	m.logInitialConnectionSummary(len(startRequests), results)
	if success > 0 {
		logger.Infof("B站直播间连接阶段完成：成功%d，失败%d，风控API轮询等待%d，30分钟轮询%d",
			success, failed, riskWaiting, longPolling)
	} else if failed > 0 || riskWaiting > 0 || longPolling > 0 {
		logger.Infof("B站直播间连接阶段完成：成功0，失败%d，风控API轮询等待%d，30分钟轮询%d",
			failed, riskWaiting, longPolling)
	}
}

// liveSubscriptionMids 列出全部含直播订阅的 UID（去重）。
// 成品 reconcile 开头（@0xd24945..@0xd24994）两步：
//
//	@0xd24955 call concern.(*StateManager).ListConcernState(@0xaf32e0)，filter 为
//	  闭包 0x111d6c0（p.ContainAny(Live)）；
//	@0xd2498c call concern.(*StateManager).GroupTypeById(@0xaf38a0)，把上一步的
//	  (ids, types) 按 id 聚合（同 id 多群/多类型合并 concern_type.Type）后再用。
//
// GroupTypeById 语义见 lsp/concern/stateManager.go:342：以 map[interface{}]Type
// 对 id 去重并把各条 ctype Add 合并，返回聚合后的 (ids, types)。对「只取 mid 集合」
// 而言结果与直接去重一致，但成品确有此调用，重建补上以对齐聚合语义。
func (m *liveWebsocketManager) liveSubscriptionMids() ([]int64, error) {
	_, ids, types, err := m.concern.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool {
		return p.ContainAny(Live)
	})
	if err != nil {
		return nil, err
	}
	ids, _, err = m.concern.GroupTypeById(ids, types)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]struct{}, len(ids))
	var mids []int64
	for _, id := range ids {
		mid, ok := id.(int64)
		if !ok || mid <= 0 {
			continue
		}
		if _, exist := seen[mid]; exist {
			continue
		}
		seen[mid] = struct{}{}
		mids = append(mids, mid)
	}
	return mids, nil
}

// liveWebsocketBatchCandidates 返回本轮需要批量 API 校准的候选 UID。
// 成品 reconcile loop A（live_websocket.go:319-327, @0xd24f7f 起）过滤条件：
//   - m.listeners[mid] 命中（0xf670e0 mapaccess）→ 跳过（@0xd2521d）；
//   - m.pending[mid] 命中（0xf67060）→ 跳过（@0xd252ab）；
//   - m.roomRetry[mid] 命中且 now.Before(retry)（0xf672e0 + time.Time.Before @0xd2534b）
//     → 跳过（@0xd25306）。
//
// 通过的 UID 收进 []int64（候选切片 ptr/len/cap 分别在 0x3c8/0x1d0/0x1d8），
// 非空时由 reconcile 交给 GetLiveStatusByUIDs（@0xd25665，guard @0xd25649）。
func (m *liveWebsocketManager) liveWebsocketBatchCandidates(mids []int64, now time.Time) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidates := make([]int64, 0, len(mids))
	for _, mid := range mids {
		if listener := m.listeners[mid]; listener != nil && !listener.stopping {
			continue
		}
		if _, inFlight := m.pending[mid]; inFlight {
			continue
		}
		if retry, ok := m.roomRetry[mid]; ok && now.Before(retry) {
			continue
		}
		candidates = append(candidates, mid)
	}
	return candidates
}

// pendingStartRequests 取出当前待建连请求（不清空）。
// 成品 pendingStartRequests(live_websocket.go:587, @0xd29660) 反汇编：
//
//	+0x25 mov 0x60(%rax)=m.pending；+0x8b mapIterStart(0xf67060)；
//	循环体 @0xd2973e 取 elem(*UserInfo) 与 key(mid)：elem==nil(@0xd2974c) 或
//	elem+0x28(RoomId)<=0(@0xd29751) 跳过，否则 append{Mid,User}（元素 16B）；
//	全程无 mapdelete（callees 无 mapdelete_fast64）——pending 不在本函数消费；
//	+0x1e0 sort.Slice(func1@0xd29880)：less(i,j)=s[i].Mid<s[j].Mid → Mid 升序。
func (m *liveWebsocketManager) pendingStartRequests() []liveWebsocketStartRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	requests := make([]liveWebsocketStartRequest, 0, len(m.pending))
	for mid, user := range m.pending {
		if user == nil || user.RoomId <= 0 {
			continue
		}
		requests = append(requests, liveWebsocketStartRequest{Mid: mid, User: user})
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].Mid < requests[j].Mid })
	return requests
}

// reportRiskControl 收到 -352 风控：把 riskControlUntil 推到 now+3min 并记录一条 warn。
// 成品 reportRiskControl(live_websocket.go:599, @0xd298c0, 1504B) 反汇编（2026-10-06 复核）：
//
//	入参 rax=m、rbx=mid(int64)、rcx=roomID(int64)；
//	+0x35 time.Now；+0x45 锁 m.mu；+0xa0 time.Time.Before(now, m+0x18=riskControlUntil)，
//	为真（仍在冷却）→ +0xe8 解锁返回、不延长；否则 +0x104 time.Time.Add(now, 0x29e8d60800=180s)
//	写 m+0x18/0x20/0x28。全函数无 m+0x68(nextFallbackPoll) 写入。
//	唯一日志为 logrus.WithFields(...).Warn，字段（key 地址/长度）：
//	  mid←arg1、room_id←arg2、api_code←-352（int64 常量 @0x1124110）、
//	  cooldown←time.Duration(180s).String()、retry_at←(now+180s).Format("2006-01-02T15:04:05Z07:00")、
//	  fallback←"batch_live_status_api"（@0x10b6822,21B）、fallback_interval←time.Duration(20s).String()；
//	  message（81B, @0x10ed23d）=“B站直播WebSocket触发-352风控，暂停新建连接3分钟并启用API轮询”。
//	cg callers=1：仅 reportRiskControl-fm(@0xd61380)，即只作为 runAccountLiveWebsocket 的第 9 个
//	寄存器入参（方法值）在 -352 分支被调用；重建不再从 consumeSignals 直接调用。
func (m *liveWebsocketManager) reportRiskControl(mid int64, roomID int64) {
	now := time.Now()
	m.mu.Lock()
	if now.Before(m.riskControlUntil) {
		m.mu.Unlock()
		return
	}
	deadline := now.Add(liveWebsocketRiskControlPause)
	m.riskControlUntil = deadline
	m.mu.Unlock()
	logger.WithFields(logrus.Fields{
		"mid":               mid,
		"room_id":           roomID,
		"api_code":          -352,
		"cooldown":          liveWebsocketRiskControlPause.String(),
		"retry_at":          deadline.Format(time.RFC3339),
		"fallback":          "batch_live_status_api",
		"fallback_interval": liveWebsocketFallbackPollInterval.String(),
	}).Warn("B站直播WebSocket触发-352风控，暂停新建连接3分钟并启用API轮询")
}

// riskControlDeadline 返回风控冷却截止时间。
func (m *liveWebsocketManager) riskControlDeadline() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.riskControlUntil
}

// pollRiskFallbackIfDue 用批量 API 复核 in-flight（pending）UID 的直播状态。
// 成品 pollRiskFallbackIfDue(live_websocket.go:634, @0xd2a120) 反汇编：
//
//	+0x2d mov 0x60(%rax)=m.pending → +0x35 mov (%rdx)=hmap.count；count==0 →
//	  +0x188 清空 m+0x68..0x80（nextFallbackPoll 归零）后返回；
//	+0x69 time.Now → +0xa0 time.Time.Before(now, m.nextFallbackPoll)，为真返回（未到点）；
//	+0xc9 time.Time.Add(now, 0x4a817c800=20s) 写回 nextFallbackPoll；
//	+0x120 makeslice(int64, 0, len(pending)) → +0x165 mapIterStart(0xf67060=m.pending)
//	  收集全部 key；+0x24d convTslice + +0x2a0 sort.Slice(func1@0xd2a700，
//	  less(i,j)=key[i]<key[j] → 升序)；
//	+0x2c7 GetLiveStatusByUIDs(pending 的 key)，err!=nil 直接返回；
//	+0x300 applyFallbackLiveStatus + logrus 记录。
//
// 与 noRoom 的关系：pending 是「已解析出 UserInfo、尚未建立连接」的 in-flight 集合，
// reconcile 每轮写入（@0xd27c17）、注册 listener 时删除（@0xd283a6）、订阅消失时裁剪
// （@0xd24f7f 起对 m+0x60 的 mapdelete）；noRoom/roomRetry 只管「账号存在但无直播间」的
// 重新解析节流（@0xd276b8/@0xd27720 写、@0xd25306 读），不是同一集合，重建原先遍历
// noRoom 属结构错位。
// 成品该函数只从 reconcile 的风控分支（@0xd3430）调用；重建由 run 每轮调用，靠
// nextFallbackPoll 的 20s 间隔节流。
func (m *liveWebsocketManager) pollRiskFallbackIfDue(ctx context.Context) {
	now := time.Now()
	m.mu.Lock()
	if len(m.pending) == 0 {
		m.nextFallbackPoll = time.Time{}
		m.mu.Unlock()
		return
	}
	if now.Before(m.nextFallbackPoll) {
		m.mu.Unlock()
		return
	}
	m.nextFallbackPoll = now.Add(liveWebsocketFallbackPollInterval)
	uids := make([]int64, 0, len(m.pending))
	for mid := range m.pending {
		uids = append(uids, mid)
	}
	m.mu.Unlock()
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	results, err := GetLiveStatusByUIDs(ctx, uids)
	if err != nil {
		logger.WithField("error", err).Warn("B站直播WebSocket风控等待期间批量API轮询失败")
		return
	}
	m.applyFallbackLiveStatus(results, true)
	logger.WithField("uid_count", len(uids)).Debug("B站直播WebSocket风控等待列表批量API轮询完成")
}

// applyFallbackLiveStatus 将批量 API 结果落库；allowEvent 时允许推送事件。
// 返回是否有状态变化。
func (m *liveWebsocketManager) applyFallbackLiveStatus(results map[int64]*BatchLiveRoomInfo, allowEvent bool) bool {
	changed := false
	for mid, result := range results {
		if result == nil {
			continue
		}
		existing, _ := m.concern.FindUserLiving(mid, false)
		if existing != nil && existing.Living() != (result.LiveStatus == 1) {
			changed = true
		}
		liveInfo := m.concern.liveInfoFromBatch(result)
		if liveInfo == nil {
			// 成品 applyFallbackLiveStatus @0xd2b20e：
			// logrus.(*Entry).Log(level=3=Warn, "应用B站直播风控兜底API状态失败")（@0x11257d0）。
			// [推断] 该 Warn 在无法由批量结果构造 LiveInfo 时按条目打印；成品的
			// WithField 键来自运行期全局，静态未坐实，这里用 mid 定位。
			logger.WithField("mid", mid).Warn("应用B站直播风控兜底API状态失败")
			continue
		}
		if err := m.concern.AddLiveInfo(liveInfo); err != nil {
			logger.WithField("mid", mid).WithField("error", err).Error("保存B站直播风控兜底API状态失败")
			continue
		}
		if allowEvent {
			emitLiveWebsocketEvent(liveInfo)
		}
	}
	if allowEvent && changed {
		logger.Info("B站直播WebSocket风控等待期间由API轮询检测到状态变化")
	}
	return changed
}

// releaseListener 释放单个 UID 的监听位。
// 成品 live_websocket.go:717（@0xd2b3c0）签名实证：receiver=m(rax)、mid(rbx int64)、
// listener(rcx *liveWebsocketListener)。入口先锁 m.mu（m+0x8，@0xd2b40f cmpxchg），
// @0xd2b477 mapaccess2_fast64(m.listeners=m+0x10, key=mid)，命中后 @0xd2b493 cmp 表中指针
// 与传入 listener 是否相等，不等即直接返回（不做任何删除）；相等才 mapdelete（@0xd2b4b7）。
func (m *liveWebsocketManager) releaseListener(mid int64, listener *liveWebsocketListener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.listeners[mid]
	if current == nil || current != listener {
		return
	}
	current.stopping = true
	if current.cancel != nil {
		current.cancel()
	}
	delete(m.listeners, mid)
	logger.WithField("mid", mid).Debug("B站直播登录账号WebSocket监听协程已完全退出，UID连接位已释放")
}

// logInitialConnectionSummary 汇总建连情况（调试用途）。
func (m *liveWebsocketManager) logInitialConnectionSummary(total int, results []liveWebsocketInitialResult) {
	success := 0
	for _, result := range results {
		if result.Success {
			success++
		}
	}
	logger.WithFields(logrus.Fields{
		"total":   total,
		"success": success,
		"failed":  total - success,
	}).Debug("B站直播间连接阶段汇总")
}

// stopAll 停止全部监听连接。
func (m *liveWebsocketManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for mid, listener := range m.listeners {
		listener.stopping = true
		if listener.cancel != nil {
			listener.cancel()
		}
		delete(m.listeners, mid)
	}
}

// dispatchSignals 消费全部直播状态信号。
func (m *liveWebsocketManager) dispatchSignals() {
	for signal := range m.signals {
		m.consumeSignals(signal)
	}
}

// consumeSignals 处理单个信号：风控信号直接上报，状态信号交 Concern 复核。
func (m *liveWebsocketManager) consumeSignals(signal liveWebsocketSignal) {
	defer func() {
		if e := recover(); e != nil {
			logger.Errorf("处理B站直播WebSocket状态信号失败: %v", e)
		}
	}()
	if err := m.concern.handleLiveWebsocketSignal(signal); err != nil {
		logger.Warnf("处理B站直播WebSocket状态信号失败: %v", err)
		m.scheduleSignalRetry(signal)
	}
}

// scheduleSignalRetry 信号处理失败后按次数退避重试。
func (m *liveWebsocketManager) scheduleSignalRetry(signal liveWebsocketSignal) {
	if signal.Retry >= liveWebsocketMaxSignalRetry {
		logger.WithField("mid", signal.Mid).Warn("B站直播状态信号重试次数耗尽，等待下轮API校准")
		return
	}
	retry := signal
	retry.Retry++
	delay := nextLiveWebsocketBackoff(signal.Retry)
	time.AfterFunc(delay, func() {
		sendLiveWebsocketSignal(retry)
	})
}

// sendLiveWebsocketSignal 非阻塞投递信号。
func sendLiveWebsocketSignal(signal liveWebsocketSignal) {
	if liveWebsocket == nil {
		return
	}
	select {
	case liveWebsocket.signals <- signal:
	default:
		logger.WithField("mid", signal.Mid).Warn("B站直播状态信号队列已满，丢弃本次信号")
	}
}

// nextLiveWebsocketBackoff 计算第 retry 次重试的退避时长。
func nextLiveWebsocketBackoff(retry int) time.Duration {
	delay := liveWebsocketInitialRetryInterval
	for i := 0; i < retry && delay < time.Hour; i++ {
		delay *= 2
	}
	return delay
}

// nextLiveWebsocketConnectDelay 计算 runAccount 断线重连的下一段退避：翻倍并封顶 60s。
// 成品三处失败路径（凭据 @0xd2e5f9、getDanmuInfo @0xd2ea3e、serve @0xd2f4fb）均为
// delay*=2，再与 0xdf8475800（=60s）比较并 cmovl 封顶（@0xd2e606/@0xd2ea4c/@0xd2f510）；
// 连接存活 >=30s 时由调用方直接重置为 2s，不走本函数。
func nextLiveWebsocketConnectDelay(delay time.Duration) time.Duration {
	delay *= 2
	if delay > liveWebsocketConnectMaxDelay {
		delay = liveWebsocketConnectMaxDelay
	}
	return delay
}

// handleLiveWebsocketSignal 由 Concern 处理直播状态信号：
// 用单房间 GetRoomInfo 复核 WebSocket 信号，状态不一致时以 API 为准并推送事件。
//
// 成品 handleLiveWebsocketSignal(live_websocket.go:847, @0xd2cce0) 反汇编（2026-10-06 复核）：
//   - @0xd2ce07 call GetRoomInfo（RoomInfo.go:19，单房间 /room/v1/Room/get_info，入参 =
//     signal+0x8，即 signal.RoomID）；返回值 (rax=*LiveRoomData, rbx:rcx=error)。
//   - rbx!=nil → @0xd2d049 重试路径；成功时 @0xd2ce25 读 resp+0x8 = LiveRoomData.Code：
//     Code!=0 → fmt.Errorf("房间API响应异常: code=%d uid=%d", Code, Data.Uid)（@0xd2cfec，
//     arg1 convT32=Code、arg2 convT64=Data.Uid）；Data.Uid != signal.Mid（@0xd2ce4e
//     cmp %rdx,0x2f0(%rsp)）也落同一错误分支。
//   - @0xd2ce65 把期望状态指针（signal.Cmd: LIVE→true、PREPARING→false、SYNC→nil 由
//     @0xd2cd25..@0xd2cd78 决定）与 (Data.LiveStatus==1) 比较，不一致 →
//     fmt.Errorf("房间API状态尚未同步: websocket=%s api=%s", signal.Cmd,
//     LiveStatus(LiveStatus==1).String())（@0xd2cf06）。
//   - 成品在 @0xd2cdd1 起最多重试 3 次，间隔 1s（waitContext，@0xd2d07a）；重建不在
//     Concern 侧阻塞重试，改由 consumeSignals 的 scheduleSignalRetry 承担（语义等价）。
//
// 复核成功后把单房间结果折成 BatchLiveRoomInfo 交给既有 applyFallbackLiveStatus
// （成品后续走 (*StateManager).GetUserInfo/AddLiveInfo/ClearNotLiveCount 刷新并派发事件；
// 重建沿用已有事件管线，用户名/头像为空时由 userInfoFromBatch 回落 StateManager 缓存）。
func (c *Concern) handleLiveWebsocketSignal(signal liveWebsocketSignal) error {
	if liveWebsocket == nil {
		return nil
	}
	roomInfo, err := GetRoomInfo(signal.RoomID)
	if err != nil {
		return err
	}
	if roomInfo == nil || roomInfo.Data == nil {
		return errors.New("主播信息为空")
	}
	if roomInfo.Code != 0 || roomInfo.Data.Uid != signal.Mid {
		return fmt.Errorf("房间API响应异常: code=%d uid=%d", roomInfo.Code, roomInfo.Data.Uid)
	}
	apiStatus := roomInfo.GetLiveStatus()
	if (signal.Cmd == "LIVE" && apiStatus != LiveStatus_Living) ||
		(signal.Cmd == "PREPARING" && apiStatus == LiveStatus_Living) {
		return fmt.Errorf("房间API状态尚未同步: websocket=%s api=%s", signal.Cmd, apiStatus)
	}
	results := map[int64]*BatchLiveRoomInfo{
		signal.Mid: {
			RoomID:        roomInfo.Data.RoomId,
			UID:           signal.Mid,
			LiveTime:      ParseLiveTime(roomInfo.Data.LiveTime),
			LiveStatus:    roomInfo.Data.LiveStatus,
			Title:         roomInfo.Data.Title,
			CoverFromUser: roomInfo.Data.UserCover,
			Keyframe:      roomInfo.Data.Keyframe,
			AreaV2Name:    roomInfo.Data.AreaName,
		},
	}
	changed := liveWebsocket.applyFallbackLiveStatus(results, true)
	if changed {
		logger.Info("B站直播WebSocket信号已通过API刷新详情")
	}
	return nil
}

// emitLiveWebsocketEvent 把直播事件送入框架分发管线。
func emitLiveWebsocketEvent(event concern.Event) {
	if event == nil {
		return
	}
	select {
	case liveWebsocketEventChan <- event:
	default:
		logger.Warn("B站直播事件队列已满，丢弃本次事件")
	}
}

// runAccountLiveWebsocket 运行单个 UID 的 WS 连接生命周期：
// 凭据 → getDanmuInfo → 建连 → 鉴权 → serve；退出后释放连接位。
// 成品 runAccountLiveWebsocket(live_websocket.go:952, @0xd2dce0, 6816B) 完整 ABI（9 寄存器，
// 调用点 reconcile.func4 @0xd28cc0 / gowrap1 @0xd28c80 复核）：
//
//	rax:rbx = ctx context.Context                       （func4 捕获的派生 ctx）
//	rcx     = mid int64                                 （@0xd28d89 取 req.Mid）
//	rdi     = roomID int64                              （@0xd28d78 取 req.User+0x28）
//	rsi:r8  = name string                               （@0xd28d80/@0xd28d7c 取 req.User+0x8/+0x10）
//	r9      = m.signals chan liveWebsocketSignal        （@0xd28d84 取 m+0x30）
//	r10     = resultChan chan liveWebsocketInitialResult（func4 闭包捕获 @0xd28d08）
//	r11     = m.reportRiskControl 方法值 {reportRiskControl-fm@0xd61380, m}（@0xd28d67..@0xd28d8c）
//
// r10 类型坐实（本次新增证据）：gowrap1 把 5 个字（ctx 两字 + request 两字 + listener）从
// go 帧拷进 func4（@0xd28c8e..@0xd28ca9）；func4 自己的闭包 ctx 在 +0x8=m（@0xd28d00）、
// +0x10=r10（@0xd28d08）。m 同时用作 reportRiskControl 接收者（@0xd28d73）与 m+0x30=signals
// （@0xd28d84），故 +0x8 必为 *liveWebsocketManager，+0x10 即另一捕获 r10。r10 在被调方只出现于
// “完成回报”路径：@0xd2f57c 读 r10 槽 → @0xd2f584 置 nil → 组装 liveWebsocketInitialResult
// （@0xd2f599..@0xd2f617）→ @0xd2f638 runtime.selectgo 非阻塞发送；func1(@0xd2f7c0) 是同路径的
// 闭包副本，亦解引用该通道后 select-send（其 callees 只有 selectgo/gcWriteBarrier1）。
// 所以 r10 = 建连结果通道；用户记的“闭包捕获 0x360”是 func4 闭包/槽位地址，不是类型。
// name 在成品里被 5 处 WithFields(key "name" @0x1099517) 使用，重建据此把 name 并进日志字段。
// listener 仍由 reconcile 侧构造并 defer 释放；本函数不写 m.listeners、不调 releaseListener。
func runAccountLiveWebsocket(
	ctx context.Context,
	mid int64,
	roomID int64,
	name string,
	signals chan liveWebsocketSignal,
	resultChan chan<- liveWebsocketInitialResult,
	reportRiskControl func(mid, roomID int64),
) {
	// 成品 runAccountLiveWebsocket(live_websocket.go:952, @0xd2dce0) 入口 +0x7a（@0xd2dd5a）
	// 先 call claimLiveWebsocketUID(rax=rcx=mid)；al==0（已有连接链）→ @0xd2de39 以 logrus
	// 字段 "mid" 告警后直接返回。cg callers('claimLiveWebsocketUID') 只有这里一条，
	// 故重建把 claim 从 reconcile 挪到本函数入口。
	// 释放点：claim 成功后 +0x88（@0xd2dd68）注册 runAccountLiveWebsocket.deferwrap1
	// （live_websocket.go:966, @0xd2f8c0），内联 HashTrieMap.LoadAndDelete（全局 0x1bd0300）。
	//
	if !claimLiveWebsocketUID(mid) {
		logger.WithFields(logrus.Fields{"mid": mid, "name": name}).
			Warn("阻止重复B站直播WebSocket监督协程：该UID已被另一条连接链独占")
		return
	}
	defer releaseLiveWebsocketUID(mid)
	// 成品 runAccount 用 0x9e(%rsp) 单次标志 + 0xd60(%rsp) resultChan 槽承载建连结果回报：
	//   - 成功：serve 首次认证通过后回调 onAuthenticated（func1 @0xd2f7c0 被 func2 @0xd2f780
	//     以 al=1 调用）；@0xd2f57c 读取并 @0xd2f584 置 nil resultChan，再 select-send
	//     {Mid,RoomID,Success:true}（@0xd2f638 selectgo，另一 case 为 ctx.Done）；
	//   - 失败：仅 -352 风控分支：@0xd2f543 调 reportRiskControl 后落到 @0xd2f566，
	//     标志未置位则置位后发送 {Mid,RoomID,Success:false}（@0xd2f5c1 movb $0x0,0xf8(%rsp)）；
	//     其余失败出口（ctx 取消 / waitContext 中止 / signals 满）只返回、不回报。
	// 两者共用同一标志，保证每条连接链只回报一次。
	resultReported := false
	report := func(success bool) {
		if resultReported {
			return
		}
		resultReported = true
		sendResult(resultChan, mid, roomID, success)
	}
	onAuthenticated := func() {
		report(true)
	}
	// 成品 callees('runAccountLiveWebsocket') 无 runtime.gorecover：本函数不注册 panic
	// recover。因 HostList 由 getAccountLiveDanmuInfoFrom 过滤并保证非空，
	// attempt%len(HostList) 正常路径不会 panicdivide（@0xd2eece 的除零守卫是编译器生成）。
	//
	// 无限重连 + 退避（按成品 @0xd2dce0 反汇编重写）：
	//   入口 @0xd2de2d/@0xd2de32 置 delay=0x77359400（=2s）、attempt=0，跳 @0xd2e073 入循环；
	//   每轮循环顶部 @0xd2e05d（失败退避后 @0xd2f520 jmp 0xd2e05d 回到此处）：
	//     - ctx.Err()（itab+0x28，@0xd2e087）非空 → 直接返回（@0xd2f6d3，不回报）；
	//     - attempt>0（@0xd2e0a3）时向 signals 非阻塞发送
	//       {Cmd:"SYNC"(0x10995df), Source:"websocket_reconnecting"(0x10b7ec2)}（@0xd2e15c 起），
	//       default（@0xd2e1c3）→ 直接返回（@0xd2f6b5，不回报）；
	//     - 每轮重读凭据（@0xd2e1d9）与 getDanmuInfo（@0xd2e665），不缓存 Cookie/Token；
	//     - host 非随机轮转：host = HostList[attempt % len(HostList)]（@0xd2eec0 idiv +
	//       @0xd2eee7 bounds），元素 24B：Host.ptr@0/Host.len@8/WSSPort@0x10；
	//     - serve 失败（@0xd2f0b0）：log → waitContext(@0xd2f4b5) →
	//       time.Since(建连时刻 @0xd2f000) >= 30s（0x6fc23ac00，@0xd2f4e5）则 delay=2s
	//       （@0xd2f4f4），否则 delay=min(delay*2,60s)（0xdf8475800，@0xd2f506 cmovl）
	//       → attempt+1（@0xd2ebee inc，存 0xe0）→ @0xd2f520 回跳。
	//   凭据失败（@0xd2e26f）与 getDanmuInfo 失败（@0xd2e66d，非 -352）走另两个 waitContext
	//   （@0xd2e5e4、@0xd2ea29）：只翻倍封顶，attempt 不变。
	//   仅 -352（apiErr.Code==-352，@0xd2e6c5 cmpq $0xfffffffffffffea0）退出循环。
	delay := liveWebsocketConnectInitialDelay
	attempt := 0
	for {
		if ctx.Err() != nil {
			// 成品 @0xd2e087 ctx.Err()；非空即 @0xd2f6d3 直接返回，不回报。
			return
		}
		if attempt > 0 {
			select {
			case signals <- liveWebsocketSignal{Mid: mid, RoomID: roomID, Cmd: "SYNC", Source: "websocket_reconnecting"}:
			default:
				return
			}
		}
		// 成品调用点 +0x4f9：currentLiveWebsocketCredential 值返回 + error（error 走栈）。
		// 缺凭据不再一次性 return，而是等退避后重读（@0xd2e5e4 waitContext）。
		credential, credErr := currentLiveWebsocketCredential(ctx)
		if credErr != nil {
			logger.WithFields(logrus.Fields{"mid": mid, "name": name}).
				Warnf("B站直播登录账号WebSocket缺少完整凭据，本次不回退匿名连接: %v", credErr)
			if waitContext(ctx, delay) != nil {
				return
			}
			delay = nextLiveWebsocketConnectDelay(delay)
			continue
		}
		// 成品 runAccountLiveWebsocket(live_websocket.go:952) 在 +0x985 直接调
		// getAccountLiveDanmuInfo(ctx, roomID, credential)：roomID 由调用方给定，
		// 组装发生在函数内，不再经 concern.GetUserInfo 预检。
		// 成品 @0xd2e6cc（cmpq $-352）命中 → @0xd2f543：先调第 9 参方法值
		// reportRiskControl(mid, roomID)（@0xd2f563），再 report(false) 返回；
		// 非 -352 的错误（@0xd2e6d2）记录后等退避重读（@0xd2ea29 waitContext），不退出。
		danmu, err := getAccountLiveDanmuInfo(ctx, roomID, credential)
		if err != nil {
			var apiErr *liveDanmuAPIError
			if errors.As(err, &apiErr) && isLiveDanmuRiskControl(apiErr.Code) {
				reportRiskControl(mid, roomID)
				report(false)
				return
			}
			logger.WithFields(logrus.Fields{"mid": mid, "name": name}).
				Warnf("获取B站直播登录账号WebSocket参数失败: %v", err)
			if waitContext(ctx, delay) != nil {
				return
			}
			delay = nextLiveWebsocketConnectDelay(delay)
			continue
		}
		// 成品 runAccountLiveWebsocket @0xd2ee82..@0xd2ee96：getAccountLiveDanmuInfo 成功后
		// 以 Debug(level=5, logrus.(*Entry).Log @0x56f740 的 ebx) 记录本次连接 Token 状态。
		// 消息静态串头 @0x1125890 "B站直播登录账号WebSocket已获取本次连接的新Token"(len 63)；
		// makemap hint=0xa（10 字段），逐字段实证：
		//   mid=arg、room_id=arg、name=arg、
		//   connection_generation = 0xc8(%rsp)+1（循环代数；0xc8 即重建的 attempt，@0xd2ebee inc、
		//     存 0xe0 供 serve 失败后 @0xd2f517 取回，等价 attempt+1）、
		//   token_refreshed=true（@0x115cc48 布尔真）、auth_mode="current_cookie"（静态串 @0x1125880）、
		//   auth_uid=credential.UID（credential 结构存 0x200，UID@+0）、
		//   buvid_present = credential.Buvid3!=""（0x240=credential.Buvid3.len，setne）、
		//   get_danmu_cookie_sent=true、websocket_cookie_ready=true（同 0x115cc48）。
		logger.WithFields(logrus.Fields{
			"mid":                    mid,
			"room_id":                roomID,
			"name":                   name,
			"connection_generation":  attempt + 1,
			"token_refreshed":        true,
			"auth_mode":              "current_cookie",
			"auth_uid":               credential.UID,
			"buvid_present":          credential.Buvid3 != "",
			"get_danmu_cookie_sent":  true,
			"websocket_cookie_ready": true,
		}).Debug("B站直播登录账号WebSocket已获取本次连接的新Token")
		// 成品该层不复核 host（cg callees('runAccountLiveWebsocket') 无
		// isAllowedLiveWebsocketHost）；过滤在 getAccountLiveDanmuInfoFrom 内完成并保证非空。
		// 所以这里不再做 len(HostList)==0 的显式检查（成品该层无此分支），
		// host 按 attempt 轮转（见 @0xd2eec0 的 idiv）。
		hostList := danmu.Data.HostList
		host := hostList[attempt%len(hostList)]
		// 成品全函数无 context.WithCancel / context.Background 调用点：
		// getAccountLiveDanmuInfo(+0x985) 与 serveAccountLiveWebsocket(+0x13d0) 都直接使用
		// 入口 0xd28/0xd30 保存的入参 ctx（rax:rbx，reconcile +0x3923 的派生结果）。
		// 成品 runAccount @+0x1233 netJoinHostPort(host.Host, host.WSSPort) →
		// @+0x1345 url.URL{Scheme:"wss", Host, Path:"/sub"}.String()，再 @+0x13d0 调用 serve
		// （serve 收 url 字符串并自行拨号）。
		target := url.URL{Scheme: "wss", Host: netJoinHostPort(host.Host, host.WSSPort), Path: "/sub"}
		// 成品 @0xd2f000 time.Now 记录建连时刻，失败后据此判断连接是否存活 >=30s。
		connectedAt := time.Now()
		serveErr := serveAccountLiveWebsocket(ctx, target.String(), danmu.Data.Token, mid, roomID, signals, credential, onAuthenticated)
		// serve 返回后成品立即再判 ctx.Err()（@0xd2f0e0 test rax,rax → @0xd2f697 返回）：
		// ctx 已取消时不再回报、不再重连。
		if ctx.Err() != nil {
			return
		}
		// 成品该层不判 serveErr==nil（serve 的所有返回路径都是 error）；只记录字段
		// mid/room_id/name/wss_host/error 并 Warn「已断开，将重新读取最新Cookie和Token后重连」
		// （@0xd2f337/@0xd2f460 WithFields，@0xd2f498 Log(level=3)，消息 0x10ee6c1，
		// 字段键 0x109880f/0x10a30f7/0x1099517/0x10a4a56/0x1b91b60）。
		logger.WithFields(logrus.Fields{
			"mid":      mid,
			"room_id":  roomID,
			"name":     name,
			"wss_host": host.Host,
			"error":    serveErr,
		}).Warn("B站直播登录账号WebSocket已断开，将重新读取最新Cookie和Token后重连")
		// 成品 @0xd2f4b5 waitContext(ctx, delay)；ctx 结束（返回非 nil）即 @0xd2f679 返回。
		if waitContext(ctx, delay) != nil {
			return
		}
		// 成品 @0xd2f4e5（30s 比较）/@0xd2f4f4（重置 2s）/@0xd2f506（cmovl 封顶 60s）：
		// 连接存活 >=30s 视为稳定，退避重置为 2s；否则翻倍并封顶 60s。
		if time.Since(connectedAt) >= liveWebsocketConnectStableThreshold {
			delay = liveWebsocketConnectInitialDelay
		} else {
			delay = nextLiveWebsocketConnectDelay(delay)
		}
		// 成品 @0xd2ebee mov 0xc8(rsp)→inc→0xe0(rsp)，@0xd2f517 取回作下一轮 host 下标。
		attempt++
	}
}

// sendResult 非阻塞回报建连结果。
func sendResult(resultChan chan<- liveWebsocketInitialResult, mid int64, roomID int64, success bool) {
	select {
	case resultChan <- liveWebsocketInitialResult{Mid: mid, RoomID: roomID, Success: success}:
	default:
	}
}

// 注：拨号职责已并回 serveAccountLiveWebsocket（成品 serve @0xd33400 内含
// Dialer.DialContext @0xd33a05 + 6 个握手头构造，runAccount 只负责在 @+0x1233/+0x1345
// 拼出 wss://host:port/sub 并把 url 字符串传给它）。原 dialAccountLiveWebsocket
// （重建自创、成品无此符号）已删除，避免双重拨号与 ABI 漂移。

// claimLiveWebsocketUID 占用 UID 连接位；已有连接链时返回 false。
// 成品 live_websocket.go:1067（96B, @0xd2f920）反汇编：
//
//	@0xd2f92e call runtime.convT64(mid)；LiveWebsocketUIDs=0x1bd0300，
//	dict(0x1158700)，key=(type int64 0xee72c0, data boxed)，value=(type 0xf22780, data 0x1bf43a0)；
//	@0xd2f959 call sync.(*HashTrieMap[any,any]).LoadOrStore(@0x4a4820)；
//	返回后 @0xd2f95e xor $1,%ecx; mov %ecx,%eax → 返回 !loaded（本次是否新插入）。
func claimLiveWebsocketUID(mid int64) bool {
	_, loaded := liveWebsocketUIDs.LoadOrStore(mid, struct{}{})
	return !loaded
}

// releaseLiveWebsocketUID 释放 UID 连接位。
// 成品 live_websocket.go:1072（96B, @0xd2f980）：把 &mid 作为 key 交给
// sync.(*HashTrieMap[any,any]).LoadAndDelete（@0x4a3aa0）。该符号在成品里零调用
// （cg callers=0）——调用点已内联进 runAccountLiveWebsocket.deferwrap1（@0xd2f8c0）；
// 重建保留具名函数并由 runAccountLiveWebsocket 的 defer 调用。
func releaseLiveWebsocketUID(mid int64) {
	liveWebsocketUIDs.LoadAndDelete(mid)
}

// currentLiveWebsocketCredential 组装当前登录凭据，缺 buvid3 时自动补取。
// 成品 currentLiveWebsocketCredential(live_websocket.go:1085, 2848B, @0xd2f9e0) 完整反汇编：
//
//	入参 ctx context.Context = rax:rbx（入口只保存 rax/rbx，无其他寄存器入参）；
//	返回 (liveWebsocketCredential, error)：9 字结构体走 rax..r11，error 接口写调用者栈
//	  0(rsp)/8(rsp)（callee 0x158/0x160，成功返回点 0xd30425 / 0xd2ff30）。
//	入口先锁专用全局 sync.Mutex（0x1bf4500，+0x6c），defer 解锁（deferwrap1 @0xd30500）；
//	for retry := 0; retry < 3; retry++（+0xef cmp $0x3，+0xd7 inc）：
//	  1) verify := atomicVerifyInfo.Load()（0x1bcc298）；verify==nil 或
//	     TrimSpace(SESSDATA)/TrimSpace(BiliJct)（+0x123/+0x161）为空 →
//	     errors.New("缺少当前B站登录Cookie（SESSDATA/bili_jct）")（0x10de587,51B，+0x926）。
//	  2) uid := accountUid.Load()（0x1bf44e8）；uid<=0 先 strconv.ParseInt(
//	     TrimSpace(verify.DedeUserID),10,64)（+0x1c0）；仍<=0 则
//	     xWebInterfaceNavWithVerify(verify)（+0x1f1）：err →
//	     fmt.Errorf("实时确认B站登录UID失败: %w")（0x10cbac3,35B，返回点 0xd30201）；
//	     Code!=0 或 !Data.IsLogin 或 Data.Mid<=0 →
//	     fmt.Errorf("当前B站Cookie未登录: code=%d message=%s")（0x10d8a18,45B，+0x98 分支）；
//	     成功后 acceptBilibiliSelfInfo(resp)（+0x260）实时确认并写 accountUid，uid 取 Data.Mid。
//	  3) TrimSpace(verify.Buvid3)（+0x2b6）为空 → fetchBilibiliBuvid3(ctx,verify)（+0x2e0）：
//	     err → fmt.Errorf("自动补取B站buvid3失败: %w")（0x10c6fc1,32B，返回点 0xd2ff81）；
//	     成功 → newVerifyInfoWithCookies(SESSDATA,BiliJct,FormatInt(uid,10),buvid3)（+0x3c9）
//	     + atomic.CompareAndSwapPointer(&atomicVerifyInfo,verify,new)（+0x3e9）；
//	     CAS 失败（另一协程已改 Cookie）→ continue 进入下一轮；CAS 成功 →
//	     persistLiveWebsocketIdentity（+0x495）。
//	  4) 组装 credential{UID:uid,SESSDATA,BiliJct,DedeUserID:FormatInt(uid,10),Buvid3} 返回（@0x88）。
//	三轮都未成功 → errors.New("B站Cookie在建连时连续发生变化，请稍后重试")（0x10e3a72,58B，+0x9de）。
//	重试无 sleep（callees 无 time.Sleep），即 3 次立即重试。
func currentLiveWebsocketCredential(ctx context.Context) (liveWebsocketCredential, error) {
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", localutils.FuncName()).Tracef("cost %v", time.Since(st))
	}()
	liveWebsocketCredentialMu.Lock()
	defer liveWebsocketCredentialMu.Unlock()
	for retry := 0; retry < 3; retry++ {
		verify := atomicVerifyInfo.Load()
		if verify == nil || strings.TrimSpace(verify.SESSDATA) == "" || strings.TrimSpace(verify.BiliJct) == "" {
			return liveWebsocketCredential{}, errors.New("缺少当前B站登录Cookie（SESSDATA/bili_jct）")
		}
		uid := accountUid.Load()
		if uid <= 0 {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(verify.DedeUserID), 10, 64); err == nil {
				uid = parsed
			}
		}
		if uid <= 0 {
			navResp, err := xWebInterfaceNavWithVerify(verify)
			if err != nil {
				return liveWebsocketCredential{}, fmt.Errorf("实时确认B站登录UID失败: %w", err)
			}
			data := navResp.GetData()
			if navResp.GetCode() != 0 || data == nil || !data.GetIsLogin() || data.GetMid() <= 0 {
				return liveWebsocketCredential{}, fmt.Errorf("当前B站Cookie未登录: code=%d message=%s", navResp.GetCode(), navResp.GetMessage())
			}
			acceptBilibiliSelfInfo(navResp)
			uid = data.GetMid()
		}
		buvid3 := strings.TrimSpace(verify.Buvid3)
		credential := liveWebsocketCredential{
			UID:        uid,
			SESSDATA:   verify.SESSDATA,
			BiliJct:    verify.BiliJct,
			DedeUserID: strconv.FormatInt(uid, 10),
			Buvid3:     buvid3,
		}
		if buvid3 == "" {
			fetched, err := fetchBilibiliBuvid3(ctx, verify)
			if err != nil {
				return liveWebsocketCredential{}, fmt.Errorf("自动补取B站buvid3失败: %w", err)
			}
			credential.Buvid3 = fetched
			refreshed := newVerifyInfoWithCookies(verify.SESSDATA, verify.BiliJct, credential.DedeUserID, fetched)
			if !atomicVerifyInfo.CompareAndSwap(verify, refreshed) {
				// 成品 +0x3e9 CAS 失败：其它协程已刷新 Cookie，重试下一轮。
				continue
			}
			if err := persistLiveWebsocketIdentity(&credential); err != nil {
				logger.Warn("B站直播WebSocket已取得账号标识，但写入配置失败；本次仍使用内存值")
			} else {
				logger.Info("B站直播登录账号WebSocket已补全并保存身份字段")
			}
		}
		return credential, nil
	}
	return liveWebsocketCredential{}, errors.New("B站Cookie在建连时连续发生变化，请稍后重试")
}

// persistLiveWebsocketIdentity 持久化补取到的身份字段；
// CookieCloud 模式按约定仅内存保留，不回写配置。
func persistLiveWebsocketIdentity(credential *liveWebsocketCredential) error {
	if credential == nil {
		return errors.New("credential为空")
	}
	if isBilibiliCookieCloudMode() {
		if verify := getVerify(); verify != nil {
			refreshed := newVerifyInfoWithCookies(verify.SESSDATA, verify.BiliJct, credential.DedeUserID, credential.Buvid3)
			atomicVerifyInfo.Store(refreshed)
		}
		return nil
	}
	// 成品 persistLiveWebsocketIdentity @0xd3063c/@0xd306a9 写回两个键：
	// bilibili.DedeUserID(@0x10b37a8,len 19) 与 bilibili.buvid3。
	kv := make(map[string]string, 2)
	if credential.DedeUserID != "" {
		kv["bilibili.DedeUserID"] = credential.DedeUserID
	}
	if credential.Buvid3 != "" {
		kv["bilibili.buvid3"] = credential.Buvid3
	}
	if len(kv) == 0 {
		return nil
	}
	return cfg.WriteConfigKeyValues(kv)
}

// fetchBilibiliBuvid3 补取设备 buvid3。
// 成品 live_websocket.go:1172（128B, @0xd30ae0）反汇编：入口保存 rax/rbx（context）与
// rcx（credential 指针）；先 cloneDefaultHTTPTransport（+0x20）；再把固定 URL
// "https://api.bilibili.com/x/frontend/finger/spi"（0x10d9a4f，46B）与凭据交给
// fetchBilibiliBuvid3From（+0x43，rax:rbx=ctx、rcx=transport、rdi:rsi=url、r8=verify），自身不包装错误。
// 成品调用点 currentLiveWebsocketCredential @0xd2fcbd 传入 0x108(%rsp)（= atomicVerifyInfo.Load() 的
// *VerifyInfo），而非凭据指针。
func fetchBilibiliBuvid3(ctx context.Context, verify *VerifyInfo) (string, error) {
	return fetchBilibiliBuvid3From(ctx, cloneDefaultHTTPTransport(), PathFingerSPI, verify)
}

// fetchBilibiliBuvid3From 请求 finger/spi 获取 buvid3（net/http 自建请求，非 requests 库）。
// 成品 live_websocket.go:1176（2880B, @0xd30b60）完整签名（寄存器实证）：
//
//	ctx context.Context = rax:rbx（入口保存至 0x158/0x160，后作 NewRequestWithContext 的 ctx）
//	transport *http.Transport = rcx（+0x81 test rcx,rcx，为零调 cloneDefaultHTTPTransport）
//	url string = rdi:rsi（入口保存至 0x170/0x178，后作 NewRequestWithContext 的 url）
//	verify *VerifyInfo = r8（+0x50 test r8,r8 为零返回 ErrVerifyRequired；+0x551 前
//	  mov (%r8)/0x8/0x10/0x18/0x20/0x28/0x30/0x38… 取前 4 个 string（VerifyInfo 的
//	  SESSDATA/BiliJct/DedeUserID/Buvid3，偏移 0x00..0x3f）交 applyBilibiliCookies）
//
//	&http.Client{Transport: 传入, Timeout: 15s}（moveabs 0x37e11d600 写 0x28(%client)）
//	net/http.NewRequestWithContext(ctx, "GET", url, nil)              +0x169
//	5 个请求头 User-Agent/Accept/Accept-Language/Origin/Referer      +0x198..+0x4e0
//	applyBilibiliCookies(req, verify 的前 4 个 string)                +0x551
//	net/http.(*Client).do                                             +0x567
//	StatusCode-200 >= 100 判错；encoding/json.(*Decoder).Decode        +0x80d
//	strings.TrimSpace(b_3) 为空判错 → 返回 trim 后的 buvid3
func fetchBilibiliBuvid3From(ctx context.Context, transport *http.Transport, url string, verify *VerifyInfo) (string, error) {
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", localutils.FuncName()).Tracef("cost %v", time.Since(st))
	}()
	// 成品 +0x13 对凭据指针做 nil 检查，为空即返回 ErrVerifyRequired（+0x139 → 0xd31299）。
	if verify == nil {
		return "", ErrVerifyRequired
	}
	// 成品 +0x81 test rcx,rcx 未命中 → +0x86 调 cloneDefaultHTTPTransport 兜底。
	if transport == nil {
		transport = cloneDefaultHTTPTransport()
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", bilibiliWebUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Origin", "https://www.bilibili.com")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	applyBilibiliCookies(req, verify.SESSDATA, verify.BiliJct, verify.DedeUserID, verify.Buvid3)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("finger/spi HTTP状态异常: %s", resp.Status)
	}
	var result bilibiliBuvidResponse
	if err := stdjson.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析finger/spi响应失败: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("finger/spi失败: code=%d message=%s", result.Code, result.Message)
	}
	buvid3 := strings.TrimSpace(result.Data.Buvid3)
	if buvid3 == "" {
		return "", errors.New("finger/spi响应缺少b_3")
	}
	return buvid3, nil
}

// bilibiliWebsocketCookies 是 WS 握手固定顺序的 Cookie 模板。
// 成品 .rodata 0x1156ae0 为 4 个 0xb8B 的 http.Cookie，名称依次
// SESSDATA / bili_jct / DedeUserID / buvid3；函数入口先把模板整体拷到栈上再填值。
var bilibiliWebsocketCookies = [4]http.Cookie{
	{Name: "SESSDATA"},
	{Name: "bili_jct"},
	{Name: "DedeUserID"},
	{Name: "buvid3"},
}

// applyBilibiliCookies 把登录 Cookie 应用到请求。
// 成品 live_websocket.go:1218（768B, @0xd316e0）反汇编：入参只有 rax=req 与 rbx..r11 共 8 字
// （4 个 string：SESSDATA/bili_jct/DedeUserID/buvid3），即成品该函数并不吃整个 9 字 credential；
// 依次写入四个 cookie 值，对 strings.TrimSpace(value) != "" 的项调用
// net/http.(*Request).AddCookie。
// ABI 对齐（2026-10-05）：重建改为 8 字签名，与成品一致；调用点各自拆出四个 string 传入。
func applyBilibiliCookies(req *http.Request, sessdata, biliJct, dedeUserID, buvid3 string) {
	cookies := bilibiliWebsocketCookies
	cookies[0].Value = sessdata
	cookies[1].Value = biliJct
	cookies[2].Value = dedeUserID
	cookies[3].Value = buvid3
	for i := range cookies {
		if strings.TrimSpace(cookies[i].Value) != "" {
			req.AddCookie(&cookies[i])
		}
	}
}

// getAccountLiveDanmuInfo 获取弹幕服务器参数（wbi 预检 + 传输层 + 请求）。
// 成品 live_websocket.go:1237（224B, @0xd31b40）完整签名（寄存器/栈实证）：
//
//	ctx context.Context = rax:rbx（入口保存至 0xd0/0xd8，后原样下传 From）
//	roomID int64 = rcx（入口保存至 0xe0，下传 From 的 r8）
//	credential liveWebsocketCredential = 入参栈 0x40B（本函数 +0x88 起的调用者参数区，
//	  +0x87 处整块 0x40B 拷到出参区转交 From；因剩余寄存器 R9-R11 放不下 8 字结构体，
//	  Go ABI 对聚合体“全寄存器或全栈”，故整体走栈）
//
// callees 仅 ensureLiveWBI（+0x2a，返回 error，非空即 return nil,err）→
// cloneDefaultHTTPTransport（+0x42）→ getAccountLiveDanmuInfoFrom（+0xa9，固定 URL
// "…/getDanmuInfo" @0x10e84e4/66B、roomID、credential）。成品符号表无 markNoRoom、
// 亦无 concern.GetUserInfo 调用点，故删除重建多出的“无直播间”预检。
func getAccountLiveDanmuInfo(ctx context.Context, roomID int64, credential liveWebsocketCredential) (*liveDanmuInfoResponse, error) {
	if err := ensureLiveWBI(); err != nil {
		return nil, err
	}
	return getAccountLiveDanmuInfoFrom(ctx, cloneDefaultHTTPTransport(), PathGetDanmuInfo, roomID, credential)
}

// getAccountLiveDanmuInfoFrom 请求 getDanmuInfo（wbi 签名 + 登录 Cookie，net/http 自建请求）。
// 成品 live_websocket.go:1250（5408B, @0xd31c20）完整签名（寄存器/栈实证）：
//
//	ctx context.Context = rax:rbx（NewRequestWithContext 的 ctx）
//	transport *http.Transport = rcx（+0x69 test rcx,rcx 为零调 cloneDefaultHTTPTransport）
//	url string = rdi:rsi（入口保存至 0x3f8/0x400，+0x11e 交 net/url.Parse）
//	roomID int64 = r8（入口保存至 0x408；+0x245 作 strconv.FormatInt(roomID,10) 的入参，
//	  +0x7cb 作 Referer fmt "https://live.bilibili.com/%d" @0x10c0bf4 的入参）
//	credential liveWebsocketCredential = 入参栈 0x40B（+0x8a2… 8 字直接读 0x3a0-0x3d8
//	  后原值交 applyBilibiliCookies，无指针解引用/无 nil 判断）
//
//	&http.Client{Transport: 传入, Timeout: 15s}                        +0x6e/+0xceb
//	net/url.Parse → signWbi{id,type,web_location} → Values.Encode → URL.String
//	net/http.NewRequestWithContext(ctx, "GET", url, nil)              +0x4ad
//	11 个请求头                                                        +0x4e0..+0xd6b
//	  User-Agent / Accept / Accept-Language / Origin /
//	  Referer(fmt "https://live.bilibili.com/%d" 0x10c0bf4) /
//	  Sec-CH-UA / Sec-CH-UA-Mobile / Sec-CH-UA-Platform /
//	  Sec-Fetch-Dest / Sec-Fetch-Mode / Sec-Fetch-Site
//	applyBilibiliCookies(req, credential)                             +0xdca
//	net/http.(*Client).do                                             +0xde0
//	StatusCode-200 >= 100 判错 → json.(*Decoder).Decode → Code!=0 判错
//	token/host_list TrimSpace + isAllowedLiveWebsocketHost 过滤 → 空则报错
func getAccountLiveDanmuInfoFrom(ctx context.Context, transport *http.Transport, rawURL string, roomID int64, credential liveWebsocketCredential) (*liveDanmuInfoResponse, error) {
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", localutils.FuncName()).Tracef("cost %v", time.Since(st))
	}()
	// 成品 +0x69 test rcx,rcx 未命中 → 调 cloneDefaultHTTPTransport 兜底。
	if transport == nil {
		transport = cloneDefaultHTTPTransport()
	}
	// 成品参数表：{"id": FormatInt(roomID,10), "type":"0", "web_location":"444.8"}
	// （+0x1e1 "type"→"0"、+0x2c4 signWbi、"id" 键 0x1098263、web_location 值 0x109a866）。
	params := signWbi(map[string]string{
		"id":           strconv.FormatInt(roomID, 10),
		"type":         "0",
		"web_location": "444.8",
	})
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("解析getDanmuInfo响应失败: %w", err)
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", bilibiliWebUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Origin", "https://live.bilibili.com")
	req.Header.Set("Referer", fmt.Sprintf("https://live.bilibili.com/%d", roomID))
	req.Header.Set("Sec-CH-UA", `"Google Chrome";v="143", "Chromium";v="143", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	// 成品 +0xdca 无条件把 credential 的 4 个 cookie 字段交 applyBilibiliCookies（8 字签名）。
	applyBilibiliCookies(req, credential.SESSDATA, credential.BiliJct, credential.DedeUserID, credential.Buvid3)
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("解析getDanmuInfo响应失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("getDanmuInfo HTTP状态异常: %s", resp.Status)
	}
	result := new(liveDanmuInfoResponse)
	if err := stdjson.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, fmt.Errorf("解析getDanmuInfo响应失败: %w", err)
	}
	if result.Code != 0 {
		return nil, &liveDanmuAPIError{Code: result.Code, Message: result.Message}
	}
	result.Data.Token = strings.TrimSpace(result.Data.Token)
	hosts := make([]liveDanmuHost, 0, len(result.Data.HostList))
	for _, host := range result.Data.HostList {
		if isAllowedLiveWebsocketHost(strings.TrimSpace(host.Host)) {
			hosts = append(hosts, host)
		}
	}
	result.Data.HostList = hosts
	if result.Data.Token == "" || len(result.Data.HostList) == 0 {
		return nil, errors.New("getDanmuInfo响应缺少token或WSS主机")
	}
	// 成品 getAccountLiveDanmuInfo/getAccountLiveDanmuInfoFrom 内不含该文案：静态串头
	// @0x1125890 "B站直播登录账号WebSocket已获取本次连接的新Token" 在整个 .text 只有
	// runAccountLiveWebsocket @0xd2ee96 一个引用（level=5=Debug）。该日志已移回
	// runAccountLiveWebsocket 的 danmu 成功分支（见那里的 10 字段 WithFields）。
	return result, nil
}

// noRoom/roomRetry 写入点核对（2026-10-05 更新）。
// 成品 funcs-x64.tsv 全表无 (*liveWebsocketManager).markNoRoom 具名符号，但 reconcile @0xd24900
// 内联了全部写入：map 字段偏移来自 recovered/types types-closure-x64.tsv:10791
//   struct 128 bilibili.liveWebsocketManager = { concern; mu; listeners; riskControlUntil; signals;
//     wg; roomRetry map[int64]time.Time; noRoom map[int64]struct{}; seeded; pending; nextFallbackPoll }
// 即 roomRetry@0x48、noRoom@0x50。reconcile 内 mapassign/mapdelete 站点（对本 build 全量反汇编生成，
// 依据 callees('liveWebsocketManager).reconcile')）：
//   - @0xd25f80 mapassign @0x48（time.Now + 5min/30min 分支，noRoom 命中时的兜底时刻刷新）
//   - @0xd26620/@0xd26640 mapdelete @0x48/@0x50   ← Status=="room_found" 分支
//   - @0xd26ab7/@0xd26ad7 mapdelete @0x48/@0x50   ← Status=="account_deleted" 分支
//   - @0xd271ed mapassign @0x48（time.Now + 5min，retry/错误路径的短退避）
//   - @0xd276b8 mapassign @0x48（time.Now + 30min）+ @0xd27720 mapassign @0x50
//                                                 ← Status=="account_exists_without_room" 分支
// 触发判定：@0xd2706d runtime.memequal 拿 resolveLiveRoom 返回的 Status.ptr 与
// 0x10bf273（"account_exists_without_room",27B）比较，命中即落到 0xd276b8/0xd27720；
// 0x10a7511（"room_found",10B）与 0x10adedd（"account_deleted",15B）在 0xce7b40 侧构造。
// 30 分钟常量 0x1a3185c5000（=liveWebsocketNoRoomRetryInterval）。
// 结论：noRoom/roomRetry 是活字段，写入被内联进 reconcile；重建补上 markLiveWebsocketNoRoom /
// clearLiveWebsocketNoRoom 两个调用点后，reconcile 与 pollRiskFallbackIfDue 的 noRoom 分支可真正触发。

// refreshLiveWebsocketNoRoomRetry 刷新「账号存在但无直播间」的下次复查时刻。
// 成品 reconcile live_websocket.go:388-389 实证：
//
//	@0xd25ee0 call time.Now（0x4f91a0）；
//	@0xd25ee5 movzbl 0x46(%rsp) → flag（batchStatusOK）；
//	@0xd25eec movabs 0x45d964b800（=5min），@0xd25ef6 movabs 0x1a3185c5000（=30min），
//	@0xd25f00 cmovne（flag≠0 取 30min）；
//	@0xd25f0c call time.Time.Add，@0xd25f80 mapassign_fast64(m+0x48=roomRetry)。
func (m *liveWebsocketManager) refreshLiveWebsocketNoRoomRetry(mid int64, batchStatusOK bool) {
	interval := liveWebsocketNoRoomRetryFastInterval
	if batchStatusOK {
		interval = liveWebsocketNoRoomRetryInterval
	}
	m.mu.Lock()
	m.roomRetry[mid] = time.Now().Add(interval)
	m.mu.Unlock()
}

// purgeDeletedAccountConcern 清理已注销账号在全部群内的订阅与本地缓存。
// 成品 reconcile account_deleted 分支 live_websocket.go:435-436 内联调用（无具名符号）：
//   - (*StateManager).RemoveAllById(mid)：concern/stateManager.go:260（闭包 func5 在 :261），
//     形参 _id interface{}，入参为 mid；
//   - (*StateManager).ClearByMid(mid)：bilibili/stateManager.go:111（闭包 func6 在 :112），
//     形参 mid int64。
//
// 两个调用的目标均为 (*buntdb.ShortCut).RWCoverTx（@0xade3e0，shortcut.go:68）。
func (m *liveWebsocketManager) purgeDeletedAccountConcern(mid int64) {
	if m.concern == nil || m.concern.StateManager == nil {
		return
	}
	removeErr := m.concern.RemoveAllById(mid)
	if removeErr != nil {
		logger.WithField("mid", mid).WithField("error", removeErr).Error("确认B站账号不存在，但清理订阅失败")
	}
	clearErr := m.concern.ClearByMid(mid)
	if clearErr != nil {
		logger.WithField("mid", mid).WithField("error", clearErr).Warn("已清理销号UID订阅，但清理旧状态缓存失败")
	}
	if removeErr == nil && clearErr == nil {
		logger.WithField("mid", mid).Warn("确认B站账号已注销或不存在，已清理该UID的全部B站订阅")
	}
}

// markLiveWebsocketNoRoom 记录「账号存在但无直播间」，并设置 30 分钟兜底重试时刻。
// 成品 reconcile 写入：@0xd276b8 mapassign_fast64(map[int64]time.Time, m+0x48) ←
// time.Now().Add(30*time.Minute)（调用点 +0x2d70 time.Now / +0x2d80 time.Time.Add，
// 常量 0x1a3185c5000）；@0xd27720 mapassign_fast64(map[int64]struct{}, m+0x50) ← key=mid。
func (m *liveWebsocketManager) markLiveWebsocketNoRoom(mid int64) {
	m.mu.Lock()
	m.roomRetry[mid] = time.Now().Add(liveWebsocketNoRoomRetryInterval)
	m.noRoom[mid] = struct{}{}
	m.mu.Unlock()
}

// clearLiveWebsocketNoRoom 清除某 UID 的「无直播间」追踪。
// 成品 reconcile 写入：Status=="room_found" 时 @0xd26620/@0xd26640；
// Status=="account_deleted" 时 @0xd26ab7/@0xd26ad7；均为 mapdelete_fast64（m+0x48 / m+0x50）。
func (m *liveWebsocketManager) clearLiveWebsocketNoRoom(mid int64) {
	m.mu.Lock()
	delete(m.roomRetry, mid)
	delete(m.noRoom, mid)
	m.mu.Unlock()
}

// isAllowedLiveWebsocketHost 校验弹幕主机是否允许连接。
func isAllowedLiveWebsocketHost(host string) bool {
	if host == "" {
		return false
	}
	return isBilibiliHost(strings.ToLower(host))
}

// ensureLiveWBI 确保 wbi 密钥可用；缺失时立即刷新并返回错误。
// 成品 ensureLiveWBI(live_websocket.go:1322, @0xd33240) 反汇编：
//
//	call getWbi(@0xced580)；test rbx,rbx（imgKey len）/ test rdi,rdi（subKey len），
//	任一为 0 → call refreshLiveWBI(@0xd33280)；否则 xor eax,ebx 返回 nil error。
func ensureLiveWBI() error {
	imgKey, subKey := getWbi()
	if imgKey == "" || subKey == "" {
		return refreshLiveWBI()
	}
	return nil
}

// refreshLiveWBI 刷新 wbi 密钥（直接走 nav 接口，返回错误）。
// 成品 refreshLiveWBI(live_websocket.go:1330, @0xd33280) 反汇编：
//
//	XWebInterfaceNav(false)（+0x14）；err 非空 → fmt.Errorf("获取WBI密钥失败: %w"
//	  @0x10bc3cd,25B)（+0x25）；
//	否则取 resp.Data.WbiImg（+0x63/+0x87），wbiImg 为空或两个字段为空
//	  （+0xa6/+0xac cmpq $0,0x10/0x20）→ errors.New("获取WBI密钥失败: 响应缺少wbi_img"
//	  @0x10d54c8,42B)；
//	全部通过则 sync/atomic.StorePointer(&wbi, wbiImg) 并 xor eax,ebx 返回 nil。
func refreshLiveWBI() error {
	resp, err := XWebInterfaceNav(false)
	if err != nil {
		return fmt.Errorf("获取WBI密钥失败: %w", err)
	}
	wbiImg := resp.GetData().GetWbiImg()
	if wbiImg == nil || wbiImg.GetImgUrl() == "" || wbiImg.GetSubUrl() == "" {
		return errors.New("获取WBI密钥失败: 响应缺少wbi_img")
	}
	wbi.Store(wbiImg)
	return nil
}

// cloneDefaultHTTPTransport 复制默认传输层，避免共享连接池污染。
func cloneDefaultHTTPTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		cloned := base.Clone()
		cloned.ForceAttemptHTTP2 = true
		return cloned
	}
	return http.DefaultTransport.(*http.Transport).Clone()
}

// serveAccountLiveWebsocket 服务一条 WS 连接：拨号、鉴权、心跳、消息分发。
// 返回 nil 表示连接被停止，返回错误表示异常断线。
//
// 成品 serveAccountLiveWebsocket(live_websocket.go:1350, @0xd33400, 6368B) 完整 ABI
// （2026-10-08 反汇编实证，调用点 runAccount @0xd2f0b0）：
//
//	rax:rbx = ctx context.Context
//	rcx:rdi = url string        （runAccount 侧 netJoinHostPort@+0x1233 +
//	                              url.URL{Scheme:"wss",Host,Path:"/sub"}.String()@+0x1345）
//	rsi:r8  = token string
//	r9      = mid int64
//	r10     = roomID int64
//	r11     = signals chan liveWebsocketSignal
//	栈 0x848..0x890（9 字） = credential liveWebsocketCredential（值传参）
//	栈 0x890（第 10 字）    = onAuthenticated func()（funcval 指针；serve @0xd34173
//	                          mov (%rdx),%rax; call *%rax，首次 op8 code==0 时触发）
//	返回 error
//
// 拨号流程（@0xd33465..@0xd33a20）：
//   - websocket.Dialer 由静态模板 0x11435a0（128B）复制：仅 +0x28 HandshakeTimeout=15s，
//     Proxy 及其余字段为 0（@0xd33482..@0xd334d2 之后无字段覆写）；
//   - http.Header 依次 Set 6 个头（@0xd3354f..@0xd3398a）：
//     Origin=https://live.bilibili.com、User-Agent=bilibiliWebUserAgent(111B)、
//     Accept-Language=zh-CN,zh;q=0.9,en;q=0.8、Cache-Control=no-cache、Pragma=no-cache、
//     Cookie=credential.cookieHeader()（无条件写入，@0xd338f4 call @0xd319e0）；
//   - DialContext(ctx,url,header) @0xd33a05；失败且 resp!=nil →
//     fmt.Errorf("WebSocket握手失败: HTTP %d: %w", resp.StatusCode, err)（@0xd33aa1）。
//
// 建连后（@0xd33b4f 起）：defer conn.Close()（deferwrap1 @0xd35100）；
// connCtx,cancel := context.WithCancel(ctx)（@0xd33ba0）+ defer cancel()；
// go func(){ <-connCtx.Done(); conn.Close() }()（func1 @0xd350a0，newproc @0xd33c2b）；
// 心跳 func3(@0xd34ce0) 捕获 connCtx；前缀信号 select 用入口 ctx（@0xd34b65 读 0x898）。
func serveAccountLiveWebsocket(ctx context.Context, url string, token string, mid int64, roomID int64, signals chan liveWebsocketSignal, credential liveWebsocketCredential, onAuthenticated func()) error {
	// 成品 @0xd3346e newobject(websocket.Dialer) + @0xd33482 从 0x11435a0 复制 128B 模板，
	// 模板仅 HandshakeTimeout(0x28)=15s，Proxy(0x18)=nil。
	dialer := websocket.Dialer{HandshakeTimeout: liveWebsocketHandshakeTimeout}
	header := http.Header{}
	header.Set("Origin", "https://live.bilibili.com")
	header.Set("User-Agent", bilibiliWebUserAgent)
	header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Pragma", "no-cache")
	header.Set("Cookie", credential.cookieHeader())
	conn, resp, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		if resp != nil {
			err = fmt.Errorf("WebSocket握手失败: HTTP %d: %w", resp.StatusCode, err)
		}
		return err
	}
	defer conn.Close()
	// ctx 观察协程：成品 serveAccountLiveWebsocket.func1(live_websocket.go:1380, @0xd350a0)
	// 在 context.WithCancel(@0xd33ba0) 之后由 runtime.newproc(@0xd33c2b) 启动：先调
	// connCtx.Done()（itab+0x20），再 runtime.chanrecv1(@0xd350c7) 阻塞，随后调 conn 的 Close
	// (@0xd350dd)。读取主循环因此不自行判 ctx（成品循环 callees 无 selectgo/ctx.Done）。
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-connCtx.Done()
		_ = conn.Close()
	}()
	// 读取上限：成品 serve @0xd33c39 `movq $0x1000000,0x100(%rcx)`（rcx=*websocket.Conn，
	// 偏移 0x100=readLimit；符号 github.com/gorilla/websocket.(*Conn).SetReadLimit
	// @0x8894c0 正是 `mov %rbx,0x100(%rax)`）→ 16MiB，且在 time.Now/SetReadDeadline 之前。
	conn.SetReadLimit(16 << 20)
	// 建连后读超时：成品 serve @0xd33c44 起 time.Now + 常量 0x14f46b0400（90s），
	// @0xd33c77 经内联的 (*Conn).SetReadDeadline（gorilla conn.go:1105，转 *websocket.Conn
	// 内嵌 net.Conn 的 fun[5]）下发。
	_ = conn.SetReadDeadline(time.Now().Add(liveWebsocketReadDeadline))
	// pong handler：成品 serve @0xd33c89..@0xd33cf2 内联 (*Conn).SetPongHandler（conn.go:1185，
	// 写 conn.handlePong），符号表留痕 serveAccountLiveWebsocket.(*Conn).SetPongHandler.func4
	// @0xd4ef40；闭包 func2(@0xd35040, live_websocket.go:1386) 行为：time.Now + 90s 后调
	// conn.SetReadDeadline 续期（@0xd35057 常量 0x14f46b0400，@0xd35087 调用）。
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(liveWebsocketReadDeadline))
	})
	// 成品 serve @0xd33d55 的 ABI：rax=roomID、rbx:rcx=key string、credential 值接收者走栈
	// （0x98..0xd8 共 9 字，对应 credential.UID/Buvid3）；返回 []byte(RDI:RSI=error 接口)。
	authPayload, authErr := makeAccountLiveAuthPayload(roomID, token, credential)
	if authErr != nil {
		// 成品 @0xd33d6a test rdi,rdi → @0xd33f09 直接返回该 error（不包装）。
		return authErr
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, packLiveProtocolPacket(liveWebsocketOpAuth, authPayload)); err != nil {
		return fmt.Errorf("B站直播登录账号WebSocket认证失败: %w", err)
	}
	// 心跳：成品 serveAccountLiveWebsocket.func3(live_websocket.go:1399, @0xd34ce0) 是独立
	// goroutine，连接后立即发一帧（func3.1 @0xd34f00），之后 30s ticker（time.NewTicker(0x6fc23ac00)）；
	// 发送失败时把 error 送入 make(chan error,1)（@0xd33e3f），关闭连接后退出，
	// 由读取主循环判定并 return（@0xd33f72 selectnbrecv → @0xd34a01 "发送心跳失败: %w"）。
	heartbeatErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(liveWebsocketHeartbeatInterval)
		defer ticker.Stop()
		// 成品 func3.1 @0xd34f77..@0xd34fbf 心跳包：16B 头（packLen=31、headerLen=16、
		// version=1、op=2、seq=1）+ 15B 包体字面量 "[object Object]"。
		heartbeat := []byte(liveWebsocketHeartbeatBody)
		sendHeartbeat := func() error {
			// 成品 func3.1 @0xd34f25：写心跳前 time.Now + 常量 0x2540be400（10s），
			// @0xd34f39..@0xd34f5a 直接写 *websocket.Conn.writeDeadline（偏移 0x60，
			// 等价内联的 (*Conn).SetWriteDeadline），再 @0xd34fd8 WriteMessage 心跳帧。
			_ = conn.SetWriteDeadline(time.Now().Add(liveWebsocketHeartbeatWriteDeadline))
			return conn.WriteMessage(websocket.BinaryMessage, packLiveProtocolPacket(liveWebsocketOpHeartbeat, heartbeat))
		}
		if err := sendHeartbeat(); err != nil {
			heartbeatErr <- err
			_ = conn.Close()
			return
		}
		for {
			select {
			case <-connCtx.Done():
				return
			case <-ticker.C:
				if err := sendHeartbeat(); err != nil {
					heartbeatErr <- err
					_ = conn.Close()
					return
				}
			}
		}
	}()
	authenticated := false
	// 读取循环
	for {
		// 成品主循环开头（@0xd33f72）runtime.selectnbrecv(heartbeatErr)：
		// 收到心跳错误立即 return fmt.Errorf("发送心跳失败: %w", err)（@0xd34a01）。
		select {
		case err := <-heartbeatErr:
			return fmt.Errorf("发送心跳失败: %w", err)
		default:
		}
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		// 成品读取循环 @0xd33fce..@0xd34004：每次 ReadMessage 成功后把读超时续到 now+90s
		// （常量 0x14f46b0400），再判定消息类型：@0xd34020 cmp $0x2,%rax 只处理
		// BinaryMessage，其余消息直接 continue。
		_ = conn.SetReadDeadline(time.Now().Add(liveWebsocketReadDeadline))
		if messageType != websocket.BinaryMessage {
			continue
		}
		// 成品 @0xd34080：decodeLiveProtocolDepth(payload, 0, compression, flag)
		//   compression = 0x1099577 "none"（rsi:r8=len 4，@0xd3406e/@0xd34075）；
		//   flag = 0（@0xd3407b xor %r9d,%r9d）。
		// 返回 ([]string 前缀集, []liveProtocolCommand 命令集, *int op8 code, error)。
		// 顶层 flag=false：成品 serve 只消费前缀集与 op8 code，命令集不落地，故成品无
		// handleLiveCommand 符号（本函数已删除/内联）。压缩链由 decode 内部递归拼接。
		prefixes, _, authCode, err := decodeLiveProtocolDepth(payload, 0, liveWebsocketCompressionNone, false)
		if err != nil {
			logger.Warnf("解析B站直播WebSocket数据失败: %v", err)
			continue
		}
		// op8 认证回包：成品 decodeLiveProtocolDepth @0xd355d8..@0xd3562d 对 op==8 的帧
		// encoding/json.Unmarshal({"code":N}) 并返回 *int；serve @0xd3414a 判定：
		// code!=0 → fmt.Errorf("B站直播登录账号WebSocket认证失败: code=%d")（@0xd3491f）；
		// code==0 且首次 → @0xd34173 调 onAuthenticated() 回报建连成功 + Info 日志
		// （成品此处不向 signals 投递 SYNC；认证完成信号改由 runAccount 闭包走 resultChan）。
		if authCode != nil {
			if *authCode != 0 {
				return fmt.Errorf("B站直播登录账号WebSocket认证失败: code=%d", *authCode)
			}
			if !authenticated {
				authenticated = true
				// 成品 serve @0xd3416b 判定 authenticated(0x63(%rsp)) 为假 → @0xd34173
				// 调第 10 栈参 onAuthenticated()：先 mov (%rdx),%rax; call *%rax（无实参）。
				// 该闭包由 runAccount 构造，向 resultChan 单次回报 Success=true；
				// serve 自身不再向 signals 投递 websocket_authenticated（成品此处无 signal send）。
				if onAuthenticated != nil {
					onAuthenticated()
				}
				logger.Info("B站直播登录账号WebSocket认证成功，立即执行连接后API状态校准")
			}
		}
		// 前缀集遍历：成品 @0xd34a88 对 LIVE(4)/PREPARING(9) 命中后投递
		// liveWebsocketSignal{Cmd: 前缀, Source: "websocket_cmd"}（常量 0x10ab5a3,13B，
		// 非旧重建的 "websocket"）。decode 已只收这两类，循环体仍保留命中判定以对齐成品。
		for _, prefix := range prefixes {
			if prefix != "LIVE" && prefix != "PREPARING" {
				continue
			}
			// 成品 serve @0xd34ae0..@0xd34bd1：构造 signal{Mid, RoomID, Cmd: 前缀,
			// Source: "websocket_cmd"} 后对 signals(0x8d8) 与 ctx.Done()(0x898) 做
			// 阻塞 select；ctx 被取消时 @0xd34bf5 调 ctx.Err() 并作为返回值退出。
			sig := liveWebsocketSignal{
				Mid:    mid,
				RoomID: roomID,
				Cmd:    prefix,
				Source: "websocket_cmd",
			}
			select {
			case signals <- sig:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// makeAccountLiveAuthPayload 构造 WS 鉴权包体。
// 成品 makeAccountLiveAuthPayload(live_websocket.go:1483, @0xd35140, 448B) 反汇编：
//
//	ABI：rax=roomID int64、rbx:rcx=key string、credential liveWebsocketCredential 值接收者
//	整体走栈（0x98..0xd8 共 9 字）；返回 []byte(rax:rbx:rcx) + error(rdi:rsi)。
//	入口 @0xd35162 cmpq $0,0x98(%rsp)（credential.UID）→ jle @0xd3519f 错误路径；
//	@0xd35195 strings.TrimSpace(credential.Buvid3 @0xd0/0xd8)，test rbx,rbx（len==0）→ 同一错误路径
//	errors.New("B站直播WebSocket账号鉴权缺少UID或buvid3")（@0x10dcb98, 49B；@0xd351ab len=0x31）。
//	@0xd351e1..@0xd35231 从 0x1142338 拷 120B 静态模板到栈，随后仅覆写：
//	  0x00=credential.UID(@0xd3523e)、0x08=roomID(@0xd3524b)、
//	  0x18/0x20=TrimSpace(buvid3)(@0xd35260/@0xd35265)、
//	  0x30/0x38=randomLiveQueueUUID(@0xd3526a)、0x68/0x70=key(@0xd3527c/@0xd35289)。
//	@0xd352a0 json.Marshal（0x103e0c0 = liveWebsocketAuthPayload 类型），原样返回其 ([]byte, error)。
//	模板 0x1142338 默认值：0x10 ProtoVer=3、0x28 SupportAck=1/true、0x40 Scene="room"、
//	0x50 Platform="web"、0x60 Type=2，其余为零。
func makeAccountLiveAuthPayload(roomID int64, key string, credential liveWebsocketCredential) ([]byte, error) {
	buvid := strings.TrimSpace(credential.Buvid3)
	if credential.UID <= 0 || buvid == "" {
		return nil, errors.New("B站直播WebSocket账号鉴权缺少UID或buvid3")
	}
	payload := liveWebsocketAuthPayload{
		UID:        credential.UID,
		RoomID:     roomID,
		ProtoVer:   liveWebsocketProtoVersion,
		Buvid:      buvid,
		SupportAck: true,
		QueueUUID:  randomLiveQueueUUID(),
		Scene:      "room",
		Platform:   "web",
		Type:       2,
		Key:        key,
	}
	return json.Marshal(payload)
}

// decodeLiveProtocolDepth 按深度解析协议帧。
//
// 成品 decodeLiveProtocolDepth(live_websocket.go:1520, @0xd35300, 4224B) 完整契约（本 build 反汇编）：
//
//	入参：payload []byte = rax:rbx:rcx；depth int = rdi；compression string = rsi:r8；
//	      flag bool = r9b（serve 传 0；递归原样下传）。
//	返回（9 寄存器）：[]string(rax:rbx:rcx) + []liveProtocolCommand(rdi:rsi:r8) + *int(r9) +
//	      error(r10:r11)。栈上结果区：0xc8=[]string（只收 LIVE/PREPARING 前缀）、
//	      0xe0=[]liveProtocolCommand、0xf8=*int（最后一个 op8 的 code）。
//	深度：入口 @0xd35367 cmp $0x4,%rdi; jg → depth>4 返回 errors.New(
//	      "B站直播协议压缩嵌套过深")（0x10ca332,34B）。
//	循环：offset<len(payload)；剩余<16 → errors.New("B站直播协议帧头不完整")（0x10c5528,31B，@0xd361c8）。
//	  逐帧读大端 packLen(u32@0)、headerLen(u16@4)、version(u16@6)、operation(u32@8)：
//	    packLen<16 / packLen>remaining / packLen>0x1000000(16MiB)
//	      → fmt.Errorf("B站直播协议包长度无效: %d")（0x10cbae6,35B，@0xd360c8）；
//	    headerLen<16 / packLen<headerLen
//	      → fmt.Errorf("B站直播协议头长度无效: %d")（0x10cbb09,35B，@0xd35fca）；
//	  version>1（@0xd355a0 cmp $1,%si; ja）：
//	    version>3 → fmt.Errorf("不支持的B站直播协议压缩版本: %d")（0x10d01f1,38B，@0xd35ecd）；
//	    否则 decompressLiveProtocol(body, int(version))（@0xd35a77，body 先行）
//	      → 以 compression 链递归 decode(depth+1)。压缩链：父为 "none"/空时取
//	      "zlib"(v2)/"brotli"(v3)，否则 runtime.concatstring3(父, "/", 新)（@0xd35b12）；
//	      递归结果的前缀集/命令集追加回本层（@0xd35bc9..@0xd35d47），authCode 非 nil 覆盖（@0xd35d79）。
//	  version<=1 时按 operation 分派：
//	    op5（@0xd35672）：encoding/json.Unmarshal(body, &struct{Cmd string json:"cmd"})；
//	      失败跳过；成功 cmd → strings.TrimSpace（@0xd356e0）→ strings.SplitN(cmd, ":", 2)
//	      （sep 0x1121448，n=2，@0xd35706）。flag&&cmd!="" 时 append liveProtocolCommand
//	      {Raw: cmd, Base: parts[0], Version: version, Operation: 5,
//	       Compression: 传入链, CompressionDepth: depth, PayloadBytes: len(body)}
//	      （构造点 @0xd35749..@0xd35876，元素 72B）。parts[0]=="LIVE"(len4) 或
//	      "PREPARING"(len9) 时 append 到前缀集（@0xd35935..@0xd35a1a）。
//	    op8（@0xd355d8）：encoding/json.Unmarshal(body, &struct{Code int json:"code"})；
//	      成功 runtime.newobject(int) 写入 code 并置结果区 0xf8（@0xd35613..@0xd3562d）。
//	    其余 op（含 op3 心跳回包）跳过（@0xd35452）。
//	  offset += packLen。
func decodeLiveProtocolDepth(payload []byte, depth int, compression string, flag bool) ([]string, []liveProtocolCommand, *int, error) {
	if depth > liveWebsocketMaxCompressionDepth {
		return nil, nil, nil, errors.New("B站直播协议压缩嵌套过深")
	}
	var (
		prefixes []string
		commands []liveProtocolCommand
		authCode *int
	)
	offset := 0
	for offset < len(payload) {
		if len(payload)-offset < liveWebsocketPacketHeaderSize {
			return nil, nil, nil, errors.New("B站直播协议帧头不完整")
		}
		packLen := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
		if packLen < liveWebsocketPacketHeaderSize || packLen > len(payload)-offset || packLen > liveWebsocketMaxFrameSize {
			return nil, nil, nil, fmt.Errorf("B站直播协议包长度无效: %d", packLen)
		}
		headerLen := int(binary.BigEndian.Uint16(payload[offset+4 : offset+6]))
		if headerLen < liveWebsocketPacketHeaderSize || packLen < headerLen {
			return nil, nil, nil, fmt.Errorf("B站直播协议头长度无效: %d", headerLen)
		}
		version := binary.BigEndian.Uint16(payload[offset+6 : offset+8])
		operation := binary.BigEndian.Uint32(payload[offset+8 : offset+12])
		body := payload[offset+headerLen : offset+packLen]
		if version > 1 {
			if version > 3 {
				return nil, nil, nil, fmt.Errorf("不支持的B站直播协议版本: %d", version)
			}
			nestedCompression := "zlib"
			if version == 3 {
				nestedCompression = "brotli"
			}
			if compression != "" && compression != liveWebsocketCompressionNone {
				nestedCompression = compression + "/" + nestedCompression
			}
			decompressed, err := decompressLiveProtocol(body, int(version))
			if err != nil {
				return nil, nil, nil, err
			}
			nestedPrefixes, nestedCommands, nestedAuthCode, err := decodeLiveProtocolDepth(decompressed, depth+1, nestedCompression, flag)
			if err != nil {
				return nil, nil, nil, err
			}
			prefixes = append(prefixes, nestedPrefixes...)
			commands = append(commands, nestedCommands...)
			if nestedAuthCode != nil {
				authCode = nestedAuthCode
			}
			offset += packLen
			continue
		}
		switch operation {
		case liveWebsocketOpMessage:
			var message struct {
				Cmd string `json:"cmd"`
			}
			if err := stdjson.Unmarshal(body, &message); err != nil {
				offset += packLen
				continue
			}
			trimmed := strings.TrimSpace(message.Cmd)
			parts := strings.SplitN(trimmed, ":", 2)
			if flag && trimmed != "" {
				commands = append(commands, liveProtocolCommand{
					Raw:              trimmed,
					Base:             parts[0],
					Version:          version,
					Operation:        operation,
					Compression:      compression,
					CompressionDepth: depth,
					PayloadBytes:     len(body),
				})
			}
			if parts[0] == "LIVE" || parts[0] == "PREPARING" {
				prefixes = append(prefixes, parts[0])
			}
		case liveWebsocketOpAuthReply:
			var reply struct {
				Code int `json:"code"`
			}
			if err := stdjson.Unmarshal(body, &reply); err == nil {
				code := reply.Code
				authCode = &code
			}
		}
		offset += packLen
	}
	return prefixes, commands, authCode, nil
}

// decompressLiveProtocol 按协议版本解压一帧压缩载荷（限大小）。
// 成品 decompressLiveProtocol(live_websocket.go:1611, @0xd36380) 以 version 分派：
//   - cmp $2 → compress/zlib.NewReaderDict（@0xd365a5）；
//   - cmp $3 → github.com/andybalholm/brotli.(*Reader).Reset（@0xd36476，即 brotli.NewReader）；
//   - 其余 → fmt.Errorf("未知压缩版本: %d", version)（@0xd364af,len 0x16）。
//
// 两分支共用 io.ReadAll(io.LimitReader(reader, 0x2000000+1))（@0xd36687/@0xd36699）与
// len>0x2000000 判错（@0xd36700 jg → "B站直播协议解压后超过大小限制"）。
// ABI：body []byte 先行（rax:rbx:rcx），version int 在后（rdi）；调用点
// decodeLiveProtocolDepth @0xd35a6a..@0xd35a77。
func decompressLiveProtocol(body []byte, version int) ([]byte, error) {
	var reader io.Reader
	switch version {
	case 2:
		zr, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("解析B站直播WebSocket数据失败: %w", err)
		}
		defer zr.Close()
		reader = zr
	case 3:
		reader = brotli.NewReader(bytes.NewReader(body))
	default:
		return nil, fmt.Errorf("未知压缩版本: %d", version)
	}
	decompressed, err := io.ReadAll(io.LimitReader(reader, liveWebsocketMaxDecompressedSize+1))
	if err != nil {
		return nil, fmt.Errorf("解析B站直播WebSocket数据失败: %w", err)
	}
	if len(decompressed) > liveWebsocketMaxDecompressedSize {
		return nil, errors.New("B站直播协议解压后超过大小限制")
	}
	return decompressed, nil
}

// packLiveProtocolPacket 组装一帧协议包（16 字节头 + 载荷）。
func packLiveProtocolPacket(operation int, body []byte) []byte {
	headerLen := liveWebsocketPacketHeaderSize
	packLen := headerLen + len(body)
	packet := make([]byte, packLen)
	binary.BigEndian.PutUint32(packet[0:4], uint32(packLen))
	binary.BigEndian.PutUint16(packet[4:6], uint16(headerLen))
	binary.BigEndian.PutUint16(packet[6:8], uint16(liveWebsocketPacketProtoVersion))
	binary.BigEndian.PutUint32(packet[8:12], uint32(operation))
	binary.BigEndian.PutUint32(packet[12:16], 1)
	copy(packet[headerLen:], body)
	return packet
}

// isLiveDanmuRiskControl 判断是否 -352 风控码。
func isLiveDanmuRiskControl(code int) bool {
	return code == -352
}

// randomLiveQueueUUID 生成随机 queue_uuid（v4 格式）。
func randomLiveQueueUUID() string {
	var buf [16]byte
	_, _ = cryptorand.Read(buf[:])
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// netJoinHostPort 主机与端口拼接（端口为 int）。
func netJoinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// waitContext 等待 d 或 ctx 结束；ctx 结束返回其错误。
func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
