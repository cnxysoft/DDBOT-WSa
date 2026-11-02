package admin

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

// 请求和响应结构体

type mockMsgCtx struct {
	groupCode int64
}

func (m *mockMsgCtx) TextSend(text string) interface{}  { return nil }
func (m *mockMsgCtx) TextReply(text string) interface{} { return nil }
func (m *mockMsgCtx) Reply(msg *mmsg.MSG) interface{}   { return nil }
func (m *mockMsgCtx) Send(msg *mmsg.MSG) interface{}    { return nil }
func (m *mockMsgCtx) NoPermissionReply() interface{}    { return nil }
func (m *mockMsgCtx) GetLog() *logrus.Entry             { return logrus.NewEntry(logrus.StandardLogger()) }
func (m *mockMsgCtx) GetTarget() mmsg.Target            { return mmsg.NewGroupTarget(m.groupCode) }
func (m *mockMsgCtx) GetSender() *adapter.SenderInfo {
	return &adapter.SenderInfo{UserID: 10000, Uin: 10000}
}

type AddSubRequest struct {
	Site      string      `json:"site"`
	ID        interface{} `json:"id"`
	Type      string      `json:"type"`
	GroupCode int64       `json:"groupCode"`
}

func parseIdToString(id interface{}) string {
	switch v := id.(type) {
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int, int32, int64:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

type RemoveSubRequest struct {
	Site      string      `json:"site"`
	ID        interface{} `json:"id"`
	Type      string      `json:"type"`
	GroupCode int64       `json:"groupCode"`
}

type GetSubConfigRequest struct {
	Site      string      `json:"site"`
	ID        interface{} `json:"id"`
	Type      string      `json:"type"`
	GroupCode int64       `json:"groupCode"`
}

type SetSubConfigRequest struct {
	Site      string                      `json:"site"`
	ID        interface{}                 `json:"id"`
	Type      string                      `json:"type"`
	GroupCode int64                       `json:"groupCode"`
	Config    *concern.GroupConcernConfig `json:"config"`
}

type ConfigUpdateRequest struct {
	Config map[string]interface{} `json:"config"`
}

type SubInfo struct {
	ID        interface{} `json:"id"`
	Type      string      `json:"type"`
	Site      string      `json:"site"`
	GroupCode int64       `json:"groupCode"`
	Name      string      `json:"name"`
}

// LogInfo SSE 日志流与内存环形缓冲的条目结构
type LogInfo struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type Server struct {
	addr string

	// token 支持运行期热更新（初始化向导设置密码后无需重启即可登录），读写加锁
	tokenMu sync.RWMutex
	token   string

	startedAt time.Time
	online    *atomic.Bool
}

func (s *Server) getToken() string {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.token
}

func (s *Server) setToken(t string) {
	s.tokenMu.Lock()
	s.token = t
	s.tokenMu.Unlock()
}

type SubSummary struct {
	Total  int            `json:"total"`
	BySite map[string]int `json:"bySite"`
}

type OneBotStatus struct {
	Online bool `json:"online"`
	Good   bool `json:"good"`
}

type Health struct {
	Ok        bool   `json:"ok"`
	UptimeSec int64  `json:"uptimeSec"`
	Version   string `json:"version"`
}

func Start(online *atomic.Bool, alive *atomic.Bool) (*Server, error) {
	enable := config.GlobalConfig.GetBool("admin.enable")
	if !enable {
		return nil, nil
	}
	addr := config.GlobalConfig.GetString("admin.addr")
	if addr == "" {
		addr = "127.0.0.1:15631"
	}
	token := config.GlobalConfig.GetString("admin.token")
	if token == "" {
		logrus.Warn("admin.token 未配置：为防止未授权访问，管理 API 将拒绝所有请求（仅 /api/v1/health 可用性另议）。请配置 admin.token 后重启")
	}

	s := &Server{addr: addr, token: token, startedAt: time.Now(), online: online}

	// 推送记录收集器（eventbus → 内存环形缓冲，供 REST/SSE 读取）
	installPushCollector()

	mux := http.NewServeMux()
	// 初始化向导（无鉴权，但仅在 admin.token 未设置时可用，见 setup.go）。
	// guardSetup 把可达来源限制为本机回环 + 同源，堵住跨站页面/局域网主机在首次部署窗口夺权（见审查报告 H2）。
	mux.HandleFunc("/api/v1/setup", s.guardSetup(s.handleSetup))
	mux.HandleFunc("/api/v1/setup/status", s.handleSetupStatus)
	// 基础 API
	mux.HandleFunc("/api/v1/health", s.withAuth(s.handleHealth))
	mux.HandleFunc("/api/v1/onebot/status", s.withAuth(s.handleOneBotStatus))
	mux.HandleFunc("/api/v1/subs/summary", s.withAuth(s.handleSubsSummary))

	// 订阅管理 API
	mux.HandleFunc("/api/v1/subs/list", s.withAuth(s.handleSubsList))
	mux.HandleFunc("/api/v1/subs/add", s.withAuth(s.handleAddSub))
	mux.HandleFunc("/api/v1/subs/remove", s.withAuth(s.handleRemoveSub))
	mux.HandleFunc("/api/admin/sub/config", s.withAuth(s.handleSubConfig))

	// 配置管理 API
	mux.HandleFunc("/api/v1/config", s.withAuth(s.handleConfig))

	// 日志管理 API
	mux.HandleFunc("/api/v1/logs/stream", s.withAuth(s.handleLogsStream))

	// 状态管理 API
	mux.HandleFunc("/api/v1/status", s.withAuth(s.handleStatus))

	// 权限与命令管理 API（数据层复用 lsp/permission StateManager）
	mux.HandleFunc("/api/v1/permissions/overview", s.withAuth(s.handlePermissionsOverview))
	// guardAdminUngrant 保护最后一个管理员：lsp 在无管理员时会把管理权授予第一个私聊命令者（见审查报告 M6）
	mux.HandleFunc("/api/v1/permissions/admin", s.withAuth(s.guardAdminUngrant(s.handleAdminAction)))
	mux.HandleFunc("/api/v1/permissions/group-admins", s.withAuth(s.handleGroupAdmins))
	mux.HandleFunc("/api/v1/permissions/group-admin", s.withAuth(s.handleGroupAdminAction))
	mux.HandleFunc("/api/v1/permissions/silence", s.withAuth(s.handleSilence))
	mux.HandleFunc("/api/v1/permissions/blocklist", s.withAuth(s.handleBlockList))
	mux.HandleFunc("/api/v1/commands", s.withAuth(s.handleCommands))
	mux.HandleFunc("/api/v1/commands/global", s.withAuth(s.handleCommandGlobal))
	mux.HandleFunc("/api/v1/commands/group", s.withAuth(s.handleCommandGroup))

	// 推送记录
	mux.HandleFunc("/api/v1/pushes", s.withAuth(s.handlePushes))
	mux.HandleFunc("/api/v1/pushes/stream", s.withAuth(s.handlePushesStream))

	// 模板编辑
	mux.HandleFunc("/api/v1/templates", s.withAuth(s.handleTemplates))
	mux.HandleFunc("/api/v1/templates/detail", s.withAuth(s.handleTemplateDetail))
	mux.HandleFunc("/api/v1/templates/save", s.withAuth(s.handleTemplateSave))
	mux.HandleFunc("/api/v1/templates/preview", s.withAuth(s.handleTemplatePreview))
	mux.HandleFunc("/api/v1/templates/reset", s.withAuth(s.handleTemplateReset))

	// 通知管理 API

	// API 调试界面（同样需要鉴权，避免成为唯一无鉴权路由）

	// Web 管理面板静态资源（catch-all 兜底；数据接口均已在上方精确注册并鉴权，
	// ServeMux 最长匹配保证 /api/* 不会被这里吞掉。静态资源见 static.go 的说明）
	mux.Handle("/", http.HandlerFunc(serveWebFile))

	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Errorf("admin server 启动失败(addr=%s): %v", addr, err)
		}
	}()
	return s, nil
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 安全：不返回 Access-Control-Allow-Origin（旧实现是通配符 *，见审查报告 H2）。
		// 面板与 API 由同一个 server 同源提供，同源请求不需要 CORS；返回 * 会让任意网页
		// 在受害者浏览器里携带 token 读取 /api/v1/config 等数据接口的响应。
		// 若确有跨源前端，应按白名单回显 Origin，而不是放开 *。
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// 安全：token 为空时一律拒绝，不允许无鉴权访问（对应配置 admin.token 为空的默认情况）
		if s.getToken() == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"admin token not configured"}`))
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// 常量时间比较，防时序攻击
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))), []byte(s.getToken())) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		next(w, r)
	}
}

// ---- 初始化向导来源限制（H2）----
//
// admin.token 未设置时 /api/v1/setup 匿名可写。仅监听 127.0.0.1 挡不住用户浏览器里的
// 跨站页面（它们可以向 127.0.0.1 发 CORS 简单请求）；监听 0.0.0.0 时局域网任意主机都能
// 抢先设置管理密码接管面板。因此 guardSetup 要求：
//   - RemoteAddr 是回环地址（挡局域网/公网直连）；
//   - Host 是回环地址（挡 DNS rebinding：攻击者域名解析到 127.0.0.1 时 Host 仍是攻击者域名）；
//   - 带 Origin/Referer 时必须与 Host 完全同源（挡跨站页面伪造的请求）。
//
// 影响：首次向导只能在运行 bot 的机器上通过 http://127.0.0.1:15631（或 localhost）完成；
// 已初始化后远程/反代访问面板不受影响（走 token 鉴权）。guardSetup 包裹 setup.go 的
// handleSetup，是因为本次改动范围限定在 server.go。
func (s *Server) guardSetup(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if !isLoopbackAddr(r.RemoteAddr) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "setup is only allowed from localhost"})
			return
		}
		if !isLoopbackHost(r.Host) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "setup is only allowed via a loopback host"})
			return
		}
		// Origin / Referer 任一存在时都必须是同源；两者都缺失（如本机 curl）时靠上面的回环校验兜底
		if origin := r.Header.Get("Origin"); origin != "" {
			if !sameOriginHost(origin, r.Host) {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "cross-origin setup request rejected"})
				return
			}
		} else if referer := r.Header.Get("Referer"); referer != "" && !sameOriginHost(referer, r.Host) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "cross-origin setup request rejected"})
			return
		}
		next(w, r)
	}
}

// isLoopbackAddr 判断连接来源地址（r.RemoteAddr，host:port）是否为回环地址。
func isLoopbackAddr(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// isLoopbackHost 判断请求 Host 头是否指向本机（回环 IP 或 localhost）。
func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sameOriginHost 判断 Origin 头的 host:port 是否与请求 Host 完全一致（同源）。
func sameOriginHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// ---- 最后一个管理员保护（M6）----
//
// lsp 在 CheckNoAdmin() 为真时会把 Admin 授予第一个私聊 Whosyourdaddy 的用户
// （lsp/permission/stateManager.go、lsp/privateCommand.go）。面板上无提示地 ungrant 掉
// 最后一个管理员，等于把 bot 管理权交给任意能私聊 bot 的 QQ 用户。
// guardAdminUngrant 包裹 permission.go 的 handleAdminAction，是因为本次改动范围限定在 server.go。
func (s *Server) guardAdminUngrant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			body, err := io.ReadAll(r.Body)
			// 还原 body，真正的 handler 还要解析一次
			r.Body = io.NopCloser(bytes.NewReader(body))
			var req AdminActionRequest
			if err == nil && json.Unmarshal(body, &req) == nil && req.Action == "ungrant" && req.QQ != 0 {
				admins := s.permissionSM().ListAdmin()
				if len(admins) == 1 && admins[0] == req.QQ {
					writeErr(w, http.StatusBadRequest, "不能移除最后一个管理员：移除后 bot 会把管理权授予第一个发送私聊命令的用户")
					return
				}
			}
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	uptime := time.Since(s.startedAt).Seconds()
	_ = json.NewEncoder(w).Encode(Health{Ok: true, UptimeSec: int64(uptime), Version: ""})
}

func (s *Server) handleOneBotStatus(w http.ResponseWriter, _ *http.Request) {
	online := false
	if s.online != nil {
		online = s.online.Load()
	}
	_ = json.NewEncoder(w).Encode(OneBotStatus{Online: online, Good: online})
}

func (s *Server) handleSubsSummary(w http.ResponseWriter, _ *http.Request) {
	bySite := map[string]int{}
	total := 0
	for _, c := range concern.ListConcern() {
		sm := c.GetStateManager()
		_, ids, ctypes, err := sm.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool { return true })
		if err != nil {
			continue
		}
		ids, ctypes, _ = sm.GroupTypeById(ids, ctypes)
		cnt := len(ids)
		bySite[c.Site()] = cnt
		total += cnt
	}
	_ = json.NewEncoder(w).Encode(SubSummary{Total: total, BySite: bySite})
}

// 订阅管理 API 处理函数

func (s *Server) handleSubsList(w http.ResponseWriter, r *http.Request) {
	subs := make([]SubInfo, 0) // 空列表返回 [] 而非 null
	for _, c := range concern.ListConcern() {
		sm := c.GetStateManager()
		groupCodes, ids, ctypes, err := sm.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool { return true })
		if err != nil {
			continue
		}
		for i := range ids {
			if i < len(groupCodes) && i < len(ctypes) {
				name := ""
				if info, err := c.Get(ids[i]); err == nil && info != nil {
					name = info.GetName()
				}
				subs = append(subs, SubInfo{
					ID:        ids[i],
					Type:      ctypes[i].String(),
					Site:      c.Site(),
					GroupCode: groupCodes[i],
					Name:      name,
				})
			}
		}
	}
	_ = json.NewEncoder(w).Encode(subs)
}

func (s *Server) handleAddSub(w http.ResponseWriter, r *http.Request) {
	var req AddSubRequest
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
		return
	}

	// 查找对应的 concern
	var targetConcern concern.Concern
	for _, c := range concern.ListConcern() {
		if c.Site() == req.Site {
			targetConcern = c
			break
		}
	}

	if targetConcern == nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid site"})
		return
	}

	// 转换类型
	var concernType concern_type.Type
	for _, t := range targetConcern.Types() {
		if t.String() == req.Type {
			concernType = t
			break
		}
	}

	if concernType == concern_type.Empty {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid concern type"})
		return
	}

	// 添加订阅
	parsedId, err := targetConcern.ParseId(parseIdToString(req.ID))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid ID format: " + err.Error()})
		return
	}

	ctx := &mockMsgCtx{groupCode: req.GroupCode}
	_, err = targetConcern.Add(ctx, req.GroupCode, parsedId, concernType)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleRemoveSub(w http.ResponseWriter, r *http.Request) {
	var req RemoveSubRequest
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
		return
	}

	// 查找对应的 concern
	var targetConcern concern.Concern
	for _, c := range concern.ListConcern() {
		if c.Site() == req.Site {
			targetConcern = c
			break
		}
	}

	if targetConcern == nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid site"})
		return
	}

	// 转换类型
	var concernType concern_type.Type
	for _, t := range targetConcern.Types() {
		if t.String() == req.Type {
			concernType = t
			break
		}
	}

	if concernType == concern_type.Empty {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid concern type"})
		return
	}

	// 删除订阅
	parsedId, err := targetConcern.ParseId(parseIdToString(req.ID))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid ID format: " + err.Error()})
		return
	}

	ctx := &mockMsgCtx{groupCode: req.GroupCode}
	_, err = targetConcern.Remove(ctx, req.GroupCode, parsedId, concernType)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleSubConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		site := r.URL.Query().Get("site")
		id := r.URL.Query().Get("id")
		groupCodeStr := r.URL.Query().Get("groupCode")

		var targetConcern concern.Concern
		for _, c := range concern.ListConcern() {
			if c.Site() == site {
				targetConcern = c
				break
			}
		}

		if targetConcern == nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid site"})
			return
		}

		parsedId, err := targetConcern.ParseId(id)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid ID format: " + err.Error()})
			return
		}

		var groupCode int64
		if _, err := fmt.Sscanf(groupCodeStr, "%d", &groupCode); err != nil || groupCode <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid groupCode"})
			return
		}

		sm := targetConcern.GetStateManager()
		cfg := sm.GetGroupConcernConfig(groupCode, parsedId)

		_ = json.NewEncoder(w).Encode(cfg)
		return
	}

	if r.Method == "POST" {
		var req SetSubConfigRequest
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request: " + err.Error()})
			return
		}
		// 安全/健壮性：省略 config 字段时 req.Config 是 nil 指针（接口非 nil），
		// 直接传给 OperateGroupConcernConfig 会在 cfg.Validate() 里对 nil 取字段地址 panic（见审查报告 M2）。
		if req.Config == nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "config is required"})
			return
		}

		var targetConcern concern.Concern
		for _, c := range concern.ListConcern() {
			if c.Site() == req.Site {
				targetConcern = c
				break
			}
		}

		if targetConcern == nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid site"})
			return
		}

		parsedId, err := targetConcern.ParseId(parseIdToString(req.ID))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid ID format: " + err.Error()})
			return
		}

		sm := targetConcern.GetStateManager()
		err = sm.OperateGroupConcernConfig(req.GroupCode, parsedId, req.Config, func(IConfig concern.IConfig) bool {
			return true
		})

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

// 配置管理 API 处理函数

// redactedPlaceholder 是敏感配置项在 GET /api/v1/config 响应中的占位值；
// POST 写入时遇到该占位值会被跳过，避免把脱敏值写回真实配置。
const redactedPlaceholder = "***REDACTED***"

// 凭据脱敏白名单：按「从根开始的完整路径」判断，不再用叶子键名子串匹配（见审查报告 H1）。
// 旧实现只看叶子键名是否包含 password/token/cookie 等子串，导致 bilibili.bili_jct、
// weibo.sub、twitter.ct0、douyin.sessionId/acSignature/acNonce、xhs.cookies.* 等高价值
// 凭据在 GET /api/v1/config 中明文返回，与「凭据明文不可读取」的安全模型矛盾。
//
// sensitiveConfigLeafPaths：这些路径的整个值（标量/异常 map/切片）都替换为占位符。
// 覆盖仓库自带 application.yaml（bot.go exampleConfig）中的全部凭据字段；
// 不含账号名、clientId、API 地址一类非密钥字段，避免过度脱敏。
var sensitiveConfigLeafPaths = map[string]bool{
	"admin.token":              true,
	"websocket.token":          true,
	"telegram.token":           true,
	"bilibili.SESSDATA":        true,
	"bilibili.bili_jct":        true,
	"acfun.password":           true,
	"acfun.authKey":            true,
	"acfun.acPassToken":        true,
	"twitter.auth_token":       true,
	"twitter.ct0":              true,
	"twitter.bearerToken":      true,
	"douyin.acSignature":       true,
	"douyin.acNonce":           true,
	"douyin.sessionId":         true,
	"weibo.sub":                true,
	"twitch.clientSecret":      true,
	"twitcasting.clientSecret": true,
	"heybox.x_xhh_tokenid":     true,
	"loliconPool.apikey":       true,
}

// sensitiveConfigSubtreePaths：这些路径下的所有叶子都视为凭据（保留嵌套结构，逐叶替换）。
// 用于 xhs.cookies 这类「凭据命名空间」——子键名（a1/web_session/webId）不含敏感子串，
// 且未来可能新增 cookie 字段，整棵树脱敏才安全。
var sensitiveConfigSubtreePaths = []string{
	"xhs.cookies",
}

func joinConfigPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func isSensitiveSubtreePath(path string) bool {
	for _, p := range sensitiveConfigSubtreePaths {
		if path == p || strings.HasPrefix(path, p+".") {
			return true
		}
	}
	return false
}

// maskSensitiveConfig 递归地将白名单路径的非空值替换为占位符。
// 非 string 标量（如数值型 token）与切片同样脱敏，避免「写成不带引号的数字就漏」的绕过。
func maskSensitiveConfig(cfg map[string]interface{}) {
	maskConfigMap(cfg, "")
}

func maskConfigMap(cfg map[string]interface{}, prefix string) {
	for k, v := range cfg {
		path := joinConfigPath(prefix, k)
		if sensitiveConfigLeafPaths[path] {
			cfg[k] = maskedConfigValue(v)
			continue
		}
		sensitive := isSensitiveSubtreePath(path)
		switch val := v.(type) {
		case map[string]interface{}:
			maskConfigMap(val, path)
		case []interface{}:
			if sensitive {
				// 敏感路径下的整个列表无法按元素定位，整体替换
				cfg[k] = maskedConfigValue(v)
				continue
			}
			maskConfigSlice(val, path)
		default:
			if sensitive && !configValueUnset(v) {
				cfg[k] = redactedPlaceholder
			}
		}
	}
}

// maskConfigSlice 递归处理列表中的 map/slice 元素，使嵌套白名单路径也能命中。
func maskConfigSlice(items []interface{}, prefix string) {
	for _, item := range items {
		switch val := item.(type) {
		case map[string]interface{}:
			maskConfigMap(val, prefix)
		case []interface{}:
			maskConfigSlice(val, prefix)
		}
	}
}

// maskedConfigValue 返回脱敏后的值：未设置（nil/空串）保持原样，其余一律占位符。
func maskedConfigValue(v interface{}) interface{} {
	if configValueUnset(v) {
		return v
	}
	return redactedPlaceholder
}

// mergeConfig 将 newCfg 合并到 baseCfg（new 优先）；值为脱敏占位符的键跳过，保留 base 原值
func mergeConfig(baseCfg, newCfg map[string]interface{}) {
	for k, v := range newCfg {
		if s, ok := v.(string); ok && s == redactedPlaceholder {
			continue
		}
		if nm, ok := v.(map[string]interface{}); ok {
			if bm, ok2 := baseCfg[k].(map[string]interface{}); ok2 {
				mergeConfig(bm, nm)
				continue
			}
		}
		baseCfg[k] = v
	}
}

// writeFileAtomic 以「临时文件 + rename」方式写入配置文件。
// 配置热重载由 fsnotify 监听，直接 os.WriteFile 会被读到只写了一半的内容；
// 原子替换同时也保证写入失败时磁盘上仍是完整的旧配置。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName) // 失败路径清理临时文件；成功 rename 后 tmpName 会被置空
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

// configValueUnset 判断配置值是否等价于「未设置」（nil / 空字符串）
func configValueUnset(v interface{}) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return s == ""
	}
	return false
}

// collectConfigWriteViolations 递归比较「请求写入的配置」与「现有配置」，
// 返回会被拒绝的受保护键路径（protectedConfigKeys），只要 mergeConfig 会改变它的值。
//
// 凭据安全模型（可写不可读）：
//   - GET 永远脱敏（maskSensitiveConfig），凭据明文不可读取
//   - 受保护键（admin.token / websocket.token）禁止修改——这两个键可提权或留后门
//   - 其余敏感键（平台 cookie/token/密码）允许覆盖写入，但读不回明文；
//     拿到 admin token 的攻击者最多覆盖凭据（影响与删订阅同级），无法窃取既有凭据
//
// 不能只检查「命中受保护键的叶子标量」，那会漏掉两类绕过（见审查报告 M1）：
//  1. 整体替换父键：{"admin": null} / {"admin": "x"} 会把 admin.token 连同父键一起删掉；
//  2. 改变受保护键类型：{"websocket": {"token": {"x": 1}}} 会把字符串 token 换成 map。
//
// 因此这里：
//   - 受保护键被写成 map（类型/结构改变）一律违规；
//   - 待写入的 map 替换掉现有 map 时，检查新子树里的受保护键；
//   - 非受保护路径若用标量/nil 整体替换掉含受保护键的现有子树，按被删掉的受保护键记违规；
//   - 受保护键的叶子值按「占位符代表未修改」「新旧语义相等代表未修改」判断。
func collectConfigWriteViolations(existing, updated map[string]interface{}, prefix string) []string {
	var violations []string
	for k, newVal := range updated {
		key := joinConfigPath(prefix, k)
		newMap, newIsMap := newVal.(map[string]interface{})
		oldVal, oldPresent := existing[k]
		oldMap, oldIsMap := oldVal.(map[string]interface{})

		if newIsMap {
			// 受保护键被写成 map：类型/结构改变，直接违规
			if isProtectedConfigKeyPath(key) {
				violations = append(violations, key)
				continue
			}
			// existing 不是 map 时传入 nil，递归仍能定位新子树里的受保护键
			if !oldIsMap {
				violations = append(violations, collectConfigWriteViolations(nil, newMap, key)...)
				continue
			}
			violations = append(violations, collectConfigWriteViolations(oldMap, newMap, key)...)
			continue
		}

		if isProtectedConfigKeyPath(key) {
			// 与 mergeConfig 保持一致：脱敏占位符代表「未修改」，merge 时会被跳过
			if s, ok := newVal.(string); ok && s == redactedPlaceholder {
				continue
			}
			oldUnset := !oldPresent || configValueUnset(oldVal)
			newUnset := configValueUnset(newVal)
			if oldUnset && newUnset {
				continue // 新旧都是未设置状态，视为未修改
			}
			if !oldUnset && !newUnset && !oldIsMap && fmt.Sprintf("%v", oldVal) == fmt.Sprintf("%v", newVal) {
				continue // 值未变化（类型差异如 yaml int 与 json float64 也不误报）
			}
			violations = append(violations, key)
			continue
		}

		// 普通路径：若用标量/nil 整体替换掉了一个 map，且该 map 里含受保护键，
		// 说明受保护键会被一起删除（如 {"admin": null}）。
		if oldIsMap {
			violations = append(violations, protectedPathsInSubtree(oldMap, key)...)
		}
	}
	return violations
}

// protectedPathsInSubtree 返回 cfg 子树中所有受保护键的完整路径（用于检测父键被整体替换/删除）。
func protectedPathsInSubtree(cfg map[string]interface{}, prefix string) []string {
	var paths []string
	for k, v := range cfg {
		path := joinConfigPath(prefix, k)
		if sub, ok := v.(map[string]interface{}); ok {
			paths = append(paths, protectedPathsInSubtree(sub, path)...)
			continue
		}
		if isProtectedConfigKeyPath(path) {
			paths = append(paths, path)
		}
	}
	return paths
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 获取完整配置（敏感键脱敏，防止密钥泄漏）
		data, err := os.ReadFile(cfg.ConfigFilePath())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read config file"})
			return
		}
		var config map[string]interface{}
		if err := yaml.Unmarshal(data, &config); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse config file"})
			return
		}
		if config == nil {
			// 空内容 / 纯注释 / 字面 null 会被解析成 nil map，直接 Encode 出去是 JSON null，
			// 前端 configCache 变成 null，高级页整份 JSON 编辑框拿不到值。
			config = make(map[string]interface{})
		}
		maskSensitiveConfig(config)
		_ = json.NewEncoder(w).Encode(config)

	case http.MethodPost:
		// 更新配置：与现有配置合并（请求优先），避免整体覆盖丢失未提交的键
		var req ConfigUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
			return
		}

		// 与其他写配置的地方（初始化向导 / 微博 cookie 回写）共用同一把写锁：
		// 这里是「读整份 -> 改 -> 写整份」，不持锁与后台回写并发时会互相覆盖。
		cfgMu := cfg.GetConfigWriteMutex()
		cfgMu.Lock()
		defer cfgMu.Unlock()
		cfg.MarkConfigWriteStart()
		defer cfg.MarkConfigWriteEnd()

		existing := make(map[string]interface{})
		var oldData []byte
		cfgPath := cfg.ConfigFilePath()
		if data, err := os.ReadFile(cfgPath); err == nil {
			oldData = data
			if uerr := yaml.Unmarshal(data, &existing); uerr != nil {
				// 解析失败时 existing 停在空 map，merge 后整份写回 = 把用户配置清空。
				// 宁可拒绝写入，让用户先修好 YAML。
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "现有配置解析失败，已拒绝写入以免覆盖: " + uerr.Error(),
				})
				return
			}
		} else if !os.IsNotExist(err) {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read config file"})
			return
		}
		if existing == nil {
			// ghodss/yaml 对空内容 / 纯注释 / 字面 null 不报错，却会把目标 map 置成 nil，
			// 后面 mergeConfig 往 nil map 写会 panic（handler 里就是连接被直接掐断）。
			existing = make(map[string]interface{})
		}

		// 安全：整体写挡掉受保护键（admin.token、websocket.token），
		// 否则可以绕过 handleConfigKey 的单键校验，靠整体 merge 落盘 token 提权或留下后门。
		// 平台凭据（cookie/token/密码）允许覆盖写入但不可读取明文（见 collectConfigWriteViolations 注释）。
		if violations := collectConfigWriteViolations(existing, req.Config, ""); len(violations) > 0 {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "protected config keys cannot be modified via API: " + strings.Join(violations, ", "),
			})
			return
		}

		// customCommandPrefix 是「命令 -> 前缀」的整表，语义是整体替换：
		// mergeConfig 是递归合并，用户删掉的命令会继续留在配置里，改不动。
		if v, ok := req.Config["customCommandPrefix"]; ok {
			existing["customCommandPrefix"] = v
			delete(req.Config, "customCommandPrefix")
		}

		mergeConfig(existing, req.Config)

		data, err := yaml.Marshal(existing)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to marshal config"})
			return
		}

		// 写入前备份原配置
		if len(oldData) > 0 {
			if err := os.WriteFile(cfgPath+".bak", oldData, 0600); err != nil {
				logrus.Warnf("配置备份失败: %v", err)
			}
		}

		if err := writeFileAtomic(cfgPath, data, 0600); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to write config file"})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
	}
}

// protectedConfigKeys 禁止通过 API 修改的配置键（防止改 token 提权或持久化后门）
var protectedConfigKeys = map[string]bool{
	"admin.token":     true,
	"websocket.token": true,
}

// isProtectedConfigKeyPath 判断完整键路径是否命中受保护键：
// 精确匹配顶层路径（websocket.token），或任意嵌套层级的同名末段（a.b.websocket.token），
// 防止把受保护键藏进嵌套结构绕过校验。
func isProtectedConfigKeyPath(path string) bool {
	if protectedConfigKeys[path] {
		return true
	}
	for pk := range protectedConfigKeys {
		if strings.HasSuffix(path, "."+pk) {
			return true
		}
	}
	return false
}

// 日志管理 API 处理函数

// 状态管理 API 处理函数

type DetailedStatus struct {
	Online              bool            `json:"online"`
	Uptime              time.Duration   `json:"uptime"`
	Version             string          `json:"version"`
	Subscriptions       int             `json:"subscriptions"`
	SubscriptionsBySite map[string]int  `json:"subscriptionsBySite"`
	ConcernsStatus      map[string]bool `json:"concernsStatus"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// 计算运行时间
	uptime := time.Since(s.startedAt)

	// 获取订阅信息
	subscriptions := 0
	subscriptionsBySite := make(map[string]int)
	for _, c := range concern.ListConcern() {
		sm := c.GetStateManager()
		_, ids, ctypes, err := sm.ListConcernState(func(groupCode int64, id interface{}, p concern_type.Type) bool { return true })
		if err != nil {
			continue
		}
		ids, ctypes, _ = sm.GroupTypeById(ids, ctypes)
		cnt := len(ids)
		subscriptionsBySite[c.Site()] = cnt
		subscriptions += cnt
	}

	// 获取各站点关注状态
	concernsStatus := make(map[string]bool)
	for _, c := range concern.ListConcern() {
		concernsStatus[c.Site()] = true // 简单起见，假设所有站点都正常
	}

	// 构建详细状态
	online := false
	if s.online != nil {
		online = s.online.Load()
	}
	status := DetailedStatus{
		Online:              online,
		Uptime:              uptime,
		Version:             "", // 版本信息需要从其他地方获取
		Subscriptions:       subscriptions,
		SubscriptionsBySite: subscriptionsBySite,
		ConcernsStatus:      concernsStatus,
	}

	_ = json.NewEncoder(w).Encode(status)
}

// API 调试界面处理函数
