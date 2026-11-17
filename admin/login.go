package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/adapter/qqofficial"
	"github.com/cnxysoft/DDBOT-WSa/lsp/bilibili"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/weibo"
	"github.com/sirupsen/logrus"
	"github.com/skip2/go-qrcode"
)

// 扫码登录：把 B站 / 微博 的扫码登录搬到 Web 面板上。
//
// 两条链路都复用 lsp 里已有的实现，面板只负责「出码 -> 轮询状态 -> 落盘」：
//   - B站：lsp/bilibili 的 qrlogin 原语（建会话 / 出码 / 轮询 / 取 cookie），
//     二维码用项目已有的 skip2/go-qrcode 渲染成 PNG。
//   - 微博：lsp/weibo 的 RunQRLoginForQQ。它本来就是给「非聊天调用方」设计的
//     通道式接口（首帧是二维码图片，末帧带 SUB 与落盘结果），面板直接接上就行。
//   - QQ 官方机器人：adapter/qqofficial 的扫码绑定协议（create_bind_task -> 轮询 ->
//     AES-256-GCM 解密），拿到的是 AppID + AppSecret 这一对凭据。手填那两个键的路
//     同样保留——扫码是便利路径，不是唯一路径。
//
// 落盘走的是与扫码命令同一条配置事务路径（cfg.WriteConfigKeyValuesContext），
// 所以配置文件里其它内容与格式都会被保留。
//
// 关于权限：这三个接口挂在 withAuth 后面，和面板其它接口一样只认 admin.token，
// 没有额外限制（用户要能正常用）。扫码与配置 API 都允许写入平台凭据，
// 配置读取只返回脱敏状态；管理及 WebSocket token 仍禁止通过配置 API 修改。

const (
	loginPlatformBilibili   = "bilibili"
	loginPlatformWeibo      = "weibo"
	loginPlatformQQOfficial = "qqofficial"

	loginStateWaiting   = "waiting"   // 等待扫码
	loginStateScanned   = "scanned"   // 已扫码，等待手机确认
	loginStateConfirmed = "confirmed" // 登录成功且已写入配置
	loginStateFailed    = "failed"
	loginStateExpired   = "expired"

	// loginSessionTTL 单次扫码会话的总时长（微博侧的轮询超时也用它）
	loginSessionTTL = 5 * time.Minute
	// loginQRWait 出码最多等多久，网络慢时不让前端干等
	loginQRWait = 20 * time.Second
)

type loginSession struct {
	id        string
	platform  string
	createdAt time.Time
	cancel    chan struct{}

	cancelOnce sync.Once
	mu         sync.Mutex
	state      string
	message    string
	qrPNG      []byte
}

func (s *loginSession) setState(state, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.message = state, message
}

func (s *loginSession) setQR(png []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qrPNG = png
}

func (s *loginSession) snapshot() (state, message string, qrPNG []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.message, s.qrPNG
}

func (s *loginSession) stop() {
	s.cancelOnce.Do(func() { close(s.cancel) })
}

// A channel-backed context exposes cancellation synchronously without a bridge
// goroutine that could outlive a completed login or miss a queued transaction.
type loginSessionContext struct {
	context.Context
	done <-chan struct{}
}

func (c loginSessionContext) Done() <-chan struct{} { return c.done }

func (c loginSessionContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (s *loginSession) context() context.Context {
	return loginSessionContext{Context: context.Background(), done: s.cancel}
}

type loginManager struct {
	mu       sync.Mutex
	sessions map[string]*loginSession
	active   map[string]string // platform -> session id：同一平台只保留一个会话
}

// revokeAll cancels work without waiting: a canceled login may itself be waiting
// for the configuration transaction currently rotating the management token.
func (m *loginManager) revokeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sess := range m.sessions {
		sess.stop()
	}
	m.sessions = make(map[string]*loginSession)
	m.active = make(map[string]string)
}

func newLoginManager() *loginManager {
	return &loginManager{
		sessions: make(map[string]*loginSession),
		active:   make(map[string]string),
	}
}

// start 建会话。同一平台已有会话时先停掉它——用户再点一次「扫码登录」是要换一张码，
// 不是开第二个会话（两个会话同时扫会抢着写同一份配置）。
func (m *loginManager) start(platform string) *loginSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	if oldID, ok := m.active[platform]; ok {
		if old, ok := m.sessions[oldID]; ok {
			old.stop()
		}
		delete(m.sessions, oldID)
	}

	sess := &loginSession{
		id:        newLoginID(),
		platform:  platform,
		createdAt: time.Now(),
		cancel:    make(chan struct{}),
		state:     loginStateWaiting,
		message:   "正在获取二维码…",
	}
	m.sessions[sess.id] = sess
	m.active[platform] = sess.id

	m.pruneLocked()
	return sess
}

func (m *loginManager) get(id string) *loginSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// startLoginForRequest makes creation atomic with token revocation. An already
// authenticated request cannot start a new task after its token was rotated.
func (s *Server) startLoginForRequest(r *http.Request, platform string) *loginSession {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if s.authStopped {
		return nil
	}
	if revoked, ok := r.Context().Value(streamAuthKey{}).(<-chan struct{}); ok {
		if revoked != s.streamsRevoked {
			return nil
		}
		select {
		case <-revoked:
			return nil
		default:
		}
	}
	return s.loginManagerOf().start(platform)
}

// release 彻底移除会话（取消时用）。
func (m *loginManager) release(sess *loginSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sess.id)
	if m.active[sess.platform] == sess.id {
		delete(m.active, sess.platform)
	}
}

// finish 会话跑完时调用：腾出「同平台只留一个」的占位，但**把会话留在表里**。
// 立刻删除会让前端的下一次轮询拿到 404 —— 面板就永远停在倒数第二个状态
// （实测症状：手机上已经确认登录、配置也写好了，面板还显示「已扫码，请在手机上确认」）。
// 终态由 start/finish 里的过期清理负责回收。
func (m *loginManager) finish(sess *loginSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[sess.platform] == sess.id {
		delete(m.active, sess.platform)
	}
	m.pruneLocked()
}

// pruneLocked 清掉早就过期的会话，避免长跑的面板把 map 撑大。
func (m *loginManager) pruneLocked() {
	for id, s := range m.sessions {
		if time.Since(s.createdAt) > 2*loginSessionTTL {
			s.stop()
			delete(m.sessions, id)
		}
	}
}

func newLoginID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}

// loginManagerOf 懒初始化：Server 也可能是测试里直接构造的 &Server{}。
func (s *Server) loginManagerOf() *loginManager {
	s.loginOnce.Do(func() { s.login = newLoginManager() })
	return s.login
}

type loginQRCodeRequest struct {
	Platform string `json:"platform"`
	Session  string `json:"session"`
}

type loginQRCodeResponse struct {
	Session  string `json:"session"`
	Platform string `json:"platform"`
	State    string `json:"state"`
	Message  string `json:"message"`
	Image    string `json:"image"` // data:image/png;base64,...
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleLoginQRCode POST /api/v1/login/qrcode —— 开一个扫码会话并返回二维码。
func (s *Server) handleLoginQRCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req loginQRCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	switch req.Platform {
	case loginPlatformBilibili, loginPlatformWeibo, loginPlatformQQOfficial:
	default:
		writeJSONError(w, http.StatusBadRequest, "unsupported platform: "+req.Platform)
		return
	}

	mgr := s.loginManagerOf()
	sess := s.startLoginForRequest(r, req.Platform)
	if sess == nil {
		w.Header().Set("X-DDBOT-Auth", "invalid-token")
		writeJSONError(w, http.StatusForbidden, "登录已失效，请重新登录")
		return
	}
	switch req.Platform {
	case loginPlatformBilibili:
		go s.runBilibiliLogin(mgr, sess)
	case loginPlatformWeibo:
		go s.runWeiboLogin(mgr, sess)
	default:
		go s.runQQOfficialLogin(mgr, sess)
	}

	// 等出码。B站/微博都是秒级返回，网络差时最多等 loginQRWait。
	deadline := time.Now().Add(loginQRWait)
	for time.Now().Before(deadline) {
		state, message, png := sess.snapshot()
		if len(png) > 0 {
			_ = json.NewEncoder(w).Encode(loginQRCodeResponse{
				Session:  sess.id,
				Platform: sess.platform,
				State:    state,
				Message:  message,
				Image:    "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
			})
			return
		}
		if state == loginStateFailed || state == loginStateExpired {
			writeJSONError(w, http.StatusBadGateway, message)
			return
		}
		select {
		case <-sess.cancel:
			writeJSONError(w, http.StatusGone, "扫码会话已取消，请重新获取二维码")
			return
		case <-r.Context().Done():
			sess.stop()
			mgr.release(sess)
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	sess.stop()
	mgr.release(sess)
	writeJSONError(w, http.StatusGatewayTimeout, "获取二维码超时，请重试")
}

// handleLoginQRCodeStatus GET /api/v1/login/qrcode/status?session=xxx
func (s *Server) handleLoginQRCodeStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	sess := s.loginManagerOf().get(r.URL.Query().Get("session"))
	if sess == nil {
		writeJSONError(w, http.StatusNotFound, "会话不存在或已结束")
		return
	}
	state, message, _ := sess.snapshot()
	_ = json.NewEncoder(w).Encode(loginQRCodeResponse{
		Session:  sess.id,
		Platform: sess.platform,
		State:    state,
		Message:  message,
	})
}

// handleLoginQRCodeCancel POST /api/v1/login/qrcode/cancel
func (s *Server) handleLoginQRCodeCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req loginQRCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}

	mgr := s.loginManagerOf()
	if sess := mgr.get(req.Session); sess != nil {
		sess.stop()
		mgr.release(sess)
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// runBilibiliLogin 出码并轮询 B站扫码状态。
func (s *Server) runBilibiliLogin(mgr *loginManager, sess *loginSession) {
	defer mgr.finish(sess)

	qrSession := bilibili.NewQRLoginSession()
	resp, err := bilibili.GetQRCodeQuiet(qrSession)
	if sess.context().Err() != nil {
		return
	}
	if err != nil {
		sess.setState(loginStateFailed, "生成二维码失败："+err.Error())
		return
	}
	if resp.GetData().GetUrl() == "" {
		sess.setState(loginStateFailed, "B站没有返回二维码内容，请稍后重试")
		return
	}

	png, err := qrcode.Encode(resp.GetData().GetUrl(), qrcode.Low, 256)
	if err != nil {
		sess.setState(loginStateFailed, "渲染二维码失败："+err.Error())
		return
	}
	sess.setQR(png)
	sess.setState(loginStateWaiting, "请用 B站客户端扫码")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	expire := time.NewTimer(loginSessionTTL)
	defer expire.Stop()

	for {
		select {
		case <-sess.cancel:
			return
		case <-expire.C:
			sess.setState(loginStateExpired, "二维码已过期，请重新获取")
			return
		case <-ticker.C:
			check, err := bilibili.QRLoginCheck(qrSession, resp.GetData().GetQrcodeKey())
			if sess.context().Err() != nil {
				return
			}
			if err != nil {
				// 轮询偶发失败不结束会话，等下一次
				logrus.Debugf("B站扫码轮询失败（继续）: %v", err)
				continue
			}
			switch check.GetData().GetCode() {
			case bilibili.QRPollWaiting:
				// 还没扫，保持原状态
			case bilibili.QRPollScanned:
				sess.setState(loginStateScanned, "已扫码，请在手机上确认")
			case bilibili.QRPollExpired:
				sess.setState(loginStateExpired, "二维码已过期，请重新获取")
				return
			case bilibili.QRPollSuccess:
				s.finishBilibiliLogin(sess, qrSession, check.GetData().GetUrl())
				return
			}
		}
	}
}

// finishBilibiliLogin 校验扫码结果并落盘。
func (s *Server) finishBilibiliLogin(sess *loginSession, qrSession *bilibili.QRLoginSession, redirectURL string) {
	ctx := sess.context()
	if ctx.Err() != nil {
		return
	}
	cookies, err := bilibili.GetCookiesFromJar(qrSession)
	if err != nil || cookies.SESSDATA == "" || cookies.BILI_JCT == "" {
		// 与扫码命令同一条兜底：cookie jar 没拿到时，从轮询响应里的跳转地址解析
		if alt, altErr := bilibili.GetCookies(redirectURL); altErr == nil {
			cookies = alt
		} else {
			sess.setState(loginStateFailed, "扫码成功但没能取到 Cookie，请重试")
			return
		}
	}

	// The config transaction validates the candidate in an isolated client and
	// updates the live account only after both credentials are durably saved.
	if err := cfg.WriteConfigKeyValuesContext(ctx, map[string]string{
		"bilibili.SESSDATA": cookies.SESSDATA,
		"bilibili.bili_jct": cookies.BILI_JCT,
	}); err != nil {
		sess.setState(loginStateFailed, "登录凭据验证或保存失败，请检查网络后重试")
		return
	}
	invalidateAccountCache(loginPlatformBilibili)
	sess.setState(loginStateConfirmed, "登录成功，Cookie 已写入配置")
}

// runWeiboLogin 出码并等待微博扫码结果。SUB 的运行时加载与落盘都在
// lsp/weibo 的 RunQRLoginForQQ 里做完了，这里只把结果翻译成会话状态。
func (s *Server) runWeiboLogin(mgr *loginManager, sess *loginSession) {
	defer mgr.finish(sess)

	ch, err := weibo.RunQRLoginForQQContext(sess.context(), loginSessionTTL)
	if err != nil {
		sess.setState(loginStateFailed, "获取二维码失败："+err.Error())
		return
	}
	var first weibo.QRLoginResult
	select {
	case <-sess.cancel:
		return
	case first = <-ch:
	}
	if len(first.QRCodeImage) == 0 {
		if first.Error != nil {
			sess.setState(loginStateFailed, "获取二维码失败："+first.Error.Error())
		} else {
			sess.setState(loginStateFailed, "微博没有返回二维码图片，请稍后重试")
		}
		return
	}
	sess.setQR(first.QRCodeImage)
	sess.setState(loginStateWaiting, "请用微博客户端扫码")

	for {
		select {
		case <-sess.cancel:
			return
		case r, ok := <-ch:
			if !ok {
				sess.setState(loginStateExpired, "二维码已过期或扫码超时，请重新获取")
				return
			}
			if r.Error != nil {
				sess.setState(loginStateFailed, "扫码登录失败："+r.Error.Error())
				return
			}
			if r.Scanned {
				sess.setState(loginStateScanned, "已扫码，正在完成登录…")
				continue
			}
			if r.Sub == "" {
				continue
			}
			if !r.PersistSuccess {
				msg := "登录成功但写入配置失败"
				if r.PersistError != nil {
					msg += "：" + r.PersistError.Error()
				}
				sess.setState(loginStateFailed, msg)
				return
			}
			invalidateAccountCache(loginPlatformWeibo)
			if !r.RuntimeLoaded {
				sess.setState(loginStateFailed, "凭据已保存，但运行会话未就绪；请检查登录状态后重试")
				return
			}
			sess.setState(loginStateConfirmed, "登录成功，SUB 已写入配置并生效")
			return
		}
	}
}

// runQQOfficialLogin 出码并轮询 QQ 官方扫码绑定结果。
//
// 与 B站/微博 的差别：扫这一下拿到的是「AppID + AppSecret」一对凭据，等于顺手创建/
// 绑定一个 QQ 官方机器人；这个 AppSecret 是解密出来的明文，只进内存与配置，不进日志。
func (s *Server) runQQOfficialLogin(mgr *loginManager, sess *loginSession) {
	defer mgr.finish(sess)

	ctx := sess.context()

	qrSess, err := qqofficial.NewQRBindSession(ctx, qqofficial.DefaultQRBindOptions())
	if err != nil {
		sess.setState(loginStateFailed, "创建绑定任务失败："+err.Error())
		return
	}

	png, err := qrcode.Encode(qrSess.URL(), qrcode.Low, 256)
	if err != nil {
		sess.setState(loginStateFailed, "渲染二维码失败："+err.Error())
		return
	}
	sess.setQR(png)
	sess.setState(loginStateWaiting, "请用手机 QQ 扫码并授权机器人")

	ticker := time.NewTicker(qqofficial.QRBindPollInterval)
	defer ticker.Stop()
	expire := time.NewTimer(loginSessionTTL)
	defer expire.Stop()

	for {
		select {
		case <-sess.cancel:
			return
		case <-expire.C:
			sess.setState(loginStateExpired, "二维码已过期，请重新获取")
			return
		case <-ticker.C:
			// 轮询失败不结束会话：绑定接口偶发超时/抖动，等下一轮（与 B站 一致）
			res, err := qrSess.Poll(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logrus.Debugf("QQ 扫码轮询失败（继续）: %v", err)
				continue
			}
			// 只有 2 / 3 有判定意义：0 与 1 都是「等扫码」（见 qqofficial 里 status 常量的实测说明）
			switch res.Status {
			case qqofficial.QRBindStatusSuccess:
				s.finishQQOfficialLogin(sess, res)
				return
			case qqofficial.QRBindStatusFailed:
				sess.setState(loginStateExpired, "二维码已过期或授权被拒绝，请重新获取")
				return
			}
		}
	}
}

// finishQQOfficialLogin validates/reconnects the active same-identity adapter
// through the config transaction before reporting success. Credentials saved
// while another protocol is active remain pending for protocol migration.
func (s *Server) finishQQOfficialLogin(sess *loginSession, res *qqofficial.QRBindResult) {
	ctx := sess.context()
	if ctx.Err() != nil {
		return
	}
	if res.AppID == "" || res.Secret == "" {
		sess.setState(loginStateFailed, "扫码成功但凭据不完整，请重试")
		return
	}

	if err := cfg.WriteConfigKeyValuesContext(ctx, map[string]string{
		"qqofficial.app-id": res.AppID, "qqofficial.app-secret": res.Secret,
	}); err != nil {
		sess.setState(loginStateFailed, "绑定凭据应用失败，配置未应用；请检查连接和机器人身份后重试")
		return
	}
	invalidateAccountCache(loginPlatformQQOfficial)
	if adapter.GetAdapterType() == adapter.AdapterTypeQQOfficial {
		sess.setState(loginStateConfirmed, "绑定成功，凭据已保存并连接")
	} else {
		sess.setState(loginStateConfirmed, "绑定凭据已保存；当前使用其他协议，切换协议需要单独迁移运行数据")
	}
}
