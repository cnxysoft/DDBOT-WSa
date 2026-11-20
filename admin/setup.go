package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
)

// 初始化向导：首次部署（admin.token 未设置）时，前端登录层之前先展示向导，
// 引导设置管理密码与接入方式（OneBot 11 / QQ 开放平台）。接口本身无鉴权，
// 但只要 token 已设置就全部关闭（404），不构成常驻后门。

const (
	// setupAdapterOneBot 是 OneBot 11 兼容实现端的 adapter.mode 取值。
	setupAdapterOneBot = "onebot-v11"
	// setupAdapterQQOfficial 是 QQ 官方开放平台的 adapter.mode 取值。
	setupAdapterQQOfficial = "qqofficial"

	// setupQQModeWebSocket 是目前 qqofficial 唯一实现的连接方式。
	setupQQModeWebSocket = "websocket"

	minAdminTokenLen = 6
)

// SetupStatus GET /api/v1/setup/status 的响应。
//
// 除了「要不要初始化」，还把现有的接入方式与 QQ 官方绑定状态带回去：
// 向导在「扫码绑定」后刷新页面 / 重新打开时不用从零猜，能直接落在正确的分支上。
// 这里**不返回 AppSecret**，只返回「是否已配置」的布尔值。
type SetupStatus struct {
	Needed      bool   `json:"needed"`                // true = admin.token 未设置，需要初始化
	Initialized bool   `json:"initialized"`           // true = admin.token 已设置；非回环来源据此提示去本机初始化
	AdapterMode string `json:"adapterMode,omitempty"` // 当前 adapter.mode；空 = 未配置（按 onebot-v11）
	QQAppID     string `json:"qqAppId,omitempty"`     // 已绑定的 QQ 官方机器人 AppID（非密钥）
	QQSecretSet bool   `json:"qqSecretSet,omitempty"` // QQ AppSecret 是否已配置
}

// SetupRequest POST /api/v1/setup 的请求体
type SetupRequest struct {
	Token       string `json:"token"`       // 管理密码（admin.token），必填
	AdapterMode string `json:"adapterMode"` // 接入方式：onebot-v11 | qqofficial；空 = 保持现状
	ObMode      string `json:"obMode"`      // OneBot 连接模式：ws-server | ws-reverse；空 = 跳过
	ObAddr      string `json:"obAddr"`      // 对应模式的地址
	ObToken     string `json:"obToken"`     // OneBot Access Token，可空
	QQAppID     string `json:"qqAppId"`     // QQ 官方机器人 AppID（adapterMode=qqofficial 时必填，扫码后可为空）
	QQAppSecret string `json:"qqAppSecret"` // QQ 官方机器人 AppSecret（adapterMode=qqofficial 时必填，扫码后可为空）
}

// validateSetupOb 在写入前校验向导的 OneBot 连接配置，规则与 adapter/wsclient.go
// 的启动约束保持一致：ws-server 监听非回环地址时必须提供 Access Token，
// 否则 adapter 会拒绝启动——不能写出一个「向导说成功、重启起不来」的配置。
// 判定与 adapter.isLoopbackAddr 等价：只有能解析为回环 IP 的 host 才算回环，
// 因此 "localhost:15630" 与 ":15630" 同样要求 token。
func validateSetupOb(mode, addr, token string) error {
	if mode != "ws-server" || token != "" || isLoopbackListenAddr(addr) {
		return nil
	}
	return fmt.Errorf("监听 %s 时 Access Token 不能为空：Bot 会拒绝在非回环地址上无鉴权启动。"+
		"请把监听地址改为 127.0.0.1:15630，或填写 Access Token", addr)
}

// isLoopbackListenAddr 判断监听地址是否只绑定回环地址。
// 空 host（":15630"）会绑定全部网卡；解析失败或 host 不是 IP 一律视为非回环。
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validateSetupAdapterMode 只放行向导里真正能配好的接入方式。
// satori 虽然 adapter 支持，但向导没有它的连接表单，写进去只会得到一份
// 「选了 satori、却没地方填地址」的配置，所以向导不提供。
func validateSetupAdapterMode(mode string) error {
	switch mode {
	case "", setupAdapterOneBot, setupAdapterQQOfficial:
		return nil
	default:
		return fmt.Errorf("未知的接入方式 %q（支持 %s / %s）", mode, setupAdapterOneBot, setupAdapterQQOfficial)
	}
}

// isNumericAppID 判断 AppID 是否全是数字。
// 与 adapter/qqofficial 的绑定协议一致：AppID 一定是纯数字，别的都是脏数据。
func isNumericAppID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// configValueString 把 YAML/JSON 里解出来的标量转成字符串。
// ghodss/yaml 会把 `app-id: 10293045` 解成 float64，直接 fmt.Sprint 会得到科学计数法，
// 这里显式处理常见的数值类型。
func configValueString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case bool:
		return strconv.FormatBool(x)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", x))
	}
}

// nestedConfigString 读 existing[top][key...] 中第一个非空值，兼容 app-secret 的几种别名。
func nestedConfigString(existing map[string]interface{}, top string, keys ...string) string {
	actualTop, _ := configMapKey(existing, top)
	sub, ok := existing[actualTop].(map[string]interface{})
	if !ok {
		return ""
	}
	for _, key := range keys {
		actual, _ := configMapKey(sub, key)
		if s := configValueString(sub[actual]); s != "" {
			return s
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 使用与 guardSetup 相同的来源限制，避免向远端响应机器人信息。
	// Initialized 只报告「有没有设置管理密码」这一位，不含任何凭据；
	// 非回环来源据此提示去本机完成初始化，而不是对着登录框猜密码。
	token := s.getToken()
	status := SetupStatus{
		Needed:      token == "" && isLoopbackAddr(r.RemoteAddr) && isLoopbackHost(r.Host) && sameOriginRequest(r),
		Initialized: token != "",
	}
	// 额外的接入方式 / AppID 只回给本机：面板暴露在局域网时，匿名请求
	// 仍然只需要知道「要不要初始化」，不该顺带读到机器人的 AppID。
	if status.Needed {
		// 只报「接入方式 / AppID / 有没有 Secret」，凭据本身不出现在任何响应里。
		status.AdapterMode = strings.TrimSpace(config.GlobalConfig.GetString("adapter.mode"))
		status.QQAppID = strings.TrimSpace(config.GlobalConfig.GetString("qqofficial.app-id"))
		status.QQSecretSet = strings.TrimSpace(config.GlobalConfig.GetString("qqofficial.app-secret")) != ""
	}
	_ = json.NewEncoder(w).Encode(status)
}

var errSetupCompleted = errors.New("setup already completed")

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 已初始化的实例：向导接口整体关闭，避免任何形式的越权写配置
	if s.getToken() != "" {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "setup already completed"})
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	var req SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}
	if len(req.Token) < minAdminTokenLen {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "管理密码至少 6 位"})
		return
	}

	// 读现有配置（向导只写受控白名单键，不做任意 merge）。
	// 与 lsp/cfg 的写入约定一致：用 viper 实际路径（否则配置在 ./config/ 下时
	// 会读写错文件），并持有全局配置写锁，避免与二维码登录的 cookie 回写并发覆盖。
	cfgPath := cfg.ConfigFilePath()
	cfgMu := cfg.GetConfigWriteMutex()
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if s.getToken() != "" {
		writeErr(w, http.StatusNotFound, "setup already completed")
		return
	}

	existing := make(map[string]interface{})
	version, err := cfg.ReadConfigFileVersion(cfgPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取现有配置失败")
		return
	}
	var fileMode os.FileMode = 0600
	if version.Exists() {
		fileMode = version.Mode()
		data := version.Bytes()
		if uerr := yaml.Unmarshal(data, &existing); uerr != nil {
			// 解析失败时 existing 停在空 map，向导照常写白名单键 = 把用户原有配置清空。
			// 宁可拒绝写入，让用户先修好 YAML。
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "现有配置解析失败，已拒绝写入以免覆盖；请检查 YAML 格式",
			})
			return
		}
		if err := validateConfigDocument(data); err != nil {
			writeErr(w, http.StatusBadRequest, "现有配置字段重复或格式无效，已拒绝写入；请检查 YAML 格式")
			return
		}
	}
	if existing == nil {
		// 同 handleConfig：空内容 / 纯注释 / 字面 null 会被解析成 nil map，
		// setNested 往 nil map 写会 panic。
		existing = make(map[string]interface{})
	}
	var identityErr error
	existing, identityErr = normalizeConfigPaths(existing)
	if identityErr != nil {
		writeErr(w, http.StatusBadRequest, "现有配置字段存在歧义，已拒绝写入："+identityErr.Error())
		return
	}

	setNested := func(top, key string, val interface{}) {
		actualTop, _ := configMapKey(existing, top)
		sub, ok := existing[actualTop].(map[string]interface{})
		if !ok {
			sub = make(map[string]interface{})
			existing[actualTop] = sub
		}
		actualKey, _ := configMapKey(sub, key)
		sub[actualKey] = val
	}

	setNested("admin", "token", req.Token)

	// 接入方式：显式选择优先；只填了 QQ 凭据时按 QQ 开放平台推断，
	// 免得老前端 / 手写请求漏传 adapterMode 导致凭据写了却不生效。
	adapterMode := strings.TrimSpace(req.AdapterMode)
	if adapterMode == "" && (strings.TrimSpace(req.QQAppID) != "" || strings.TrimSpace(req.QQAppSecret) != "") {
		adapterMode = setupAdapterQQOfficial
	}
	if err := validateSetupAdapterMode(adapterMode); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	var effectiveQQAppID string
	switch adapterMode {
	case setupAdapterQQOfficial:
		appID := strings.TrimSpace(req.QQAppID)
		appSecret := strings.TrimSpace(req.QQAppSecret)

		// 换 AppID 却不给新 Secret 时，合并会拿旧 Secret 去配新 AppID——必然连不上。
		// 扫码绑定只会写入同一对凭据，所以这条只在「手填改了 AppID」时触发。
		existingAppID := nestedConfigString(existing, "qqofficial", "app-id")
		if appID != "" && appSecret == "" && existingAppID != "" && appID != existingAppID {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "更换 AppID 时请同时填写对应的 AppSecret，或重新扫码绑定",
			})
			return
		}

		if appID != "" {
			setNested("qqofficial", "app-id", appID)
		}
		if appSecret != "" {
			setNested("qqofficial", "app-secret", appSecret)
		}

		// 扫码绑定会先一步把凭据写进配置，此时前端提交的 AppSecret 是空的。
		// 校验必须落在「合并后的有效值」上，而不是只看请求体。
		effectiveQQAppID = firstNonEmpty(appID, existingAppID)
		effectiveSecret := firstNonEmpty(appSecret, nestedConfigString(existing, "qqofficial", "app-secret", "appSecret", "secret"))
		if effectiveQQAppID == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "QQ 开放平台需要 AppID：请手动填写，或先点「扫码绑定」",
			})
			return
		}
		if effectiveSecret == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "QQ 开放平台需要 AppSecret：请手动填写，或先点「扫码绑定」",
			})
			return
		}
		if !isNumericAppID(effectiveQQAppID) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "AppID 必须是纯数字，请到 QQ 开放平台机器人管理端核对",
			})
			return
		}
		setNested("adapter", "mode", setupAdapterQQOfficial)
		if nestedConfigString(existing, "qqofficial", "mode") == "" {
			setNested("qqofficial", "mode", setupQQModeWebSocket)
		}

	case setupAdapterOneBot, "":
		// 显式选择 OneBot 时写入 adapter.mode；空值保持旧行为，不改 adapter.mode。
		if adapterMode == setupAdapterOneBot {
			setNested("adapter", "mode", setupAdapterOneBot)
		}
		switch req.ObMode {
		case "ws-server", "ws-reverse":
			if req.ObAddr != "" {
				if err := validateSetupOb(req.ObMode, req.ObAddr, req.ObToken); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
					return
				}
				setNested("websocket", "mode", req.ObMode)
				setNested("websocket", req.ObMode, req.ObAddr)
				if req.ObToken != "" {
					setNested("websocket", "token", req.ObToken)
				}
			}
		case "":
			// 跳过 OneBot 配置，稍后可在配置页设置
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "未知的连接模式"})
			return
		}
	}
	if err := validateQQOfficialConfig(existing); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := renderConfigCandidate(version.Bytes(), existing)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to marshal config")
		return
	}
	reloadStatus, err := config.GlobalConfig.SaveAndStatus(data, func() error {
		if s.getToken() != "" {
			return errSetupCompleted
		}
		return version.Persist(data, fileMode, true)
	})
	if err != nil {
		var prepareErr *config.PrepareError
		if errors.Is(err, cfg.ErrConfigConflict) {
			writeErr(w, http.StatusConflict, cfg.ErrConfigConflict.Error())
		} else if errors.Is(err, errSetupCompleted) {
			writeErr(w, http.StatusNotFound, errSetupCompleted.Error())
		} else if errors.As(err, &prepareErr) {
			writeErr(w, http.StatusBadRequest, prepareErr.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "Failed to write config file")
		}
		return
	}
	// 热更新内存中的 token：设置完成后立即可用新密码登录，无需重启
	// Normally the admin runtime participant already rotated the token. The
	// fallback is for a server initialized without runtime participants.
	if s.getToken() == "" && config.GlobalConfig.Status().Revision == reloadStatus.Revision {
		s.setToken(req.Token)
		if config.GlobalConfig.GetString("admin.token") != req.Token {
			config.GlobalConfig.Set("admin.token", req.Token)
			config.GlobalConfig.MarkApplied("admin.token")
			reloadStatus = config.GlobalConfig.Status()
		}
	}
	logrus.Info("初始化向导完成：admin.token 已设置")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "reload": reloadStatus})
}
