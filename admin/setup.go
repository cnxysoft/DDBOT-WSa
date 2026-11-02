package admin

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
)

// 初始化向导：首次部署（admin.token 未设置）时，前端登录层之前先展示向导，
// 引导设置管理密码与 OneBot 连接。接口本身无鉴权，但只要 token 已设置就全部关闭
// （404），不构成常驻后门。

// SetupStatus GET /api/v1/setup/status 的响应
type SetupStatus struct {
	Needed bool `json:"needed"` // true = admin.token 未设置，需要初始化
}

// SetupRequest POST /api/v1/setup 的请求体
type SetupRequest struct {
	Token   string `json:"token"`   // 管理密码（admin.token），必填
	ObMode  string `json:"obMode"`  // OneBot 连接模式：ws-server | ws-reverse；空 = 跳过
	ObAddr  string `json:"obAddr"`  // 对应模式的地址
	ObToken string `json:"obToken"` // OneBot Access Token，可空
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

func (s *Server) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(SetupStatus{Needed: s.getToken() == ""})
}

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
	if len(req.Token) < 6 {
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
	cfg.MarkConfigWriteStart()
	defer cfg.MarkConfigWriteEnd()

	var fileMode os.FileMode = 0600 // 文件不存在时沿用旧行为
	if info, err := os.Stat(cfgPath); err == nil {
		fileMode = info.Mode() // 保留原文件权限
	}

	existing := make(map[string]interface{})
	var oldData []byte
	if data, err := os.ReadFile(cfgPath); err == nil {
		oldData = data
		if uerr := yaml.Unmarshal(data, &existing); uerr != nil {
			// 解析失败时 existing 停在空 map，向导照常写白名单键 = 把用户原有配置清空。
			// 宁可拒绝写入，让用户先修好 YAML。
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "现有配置解析失败，已拒绝写入以免覆盖: " + uerr.Error(),
			})
			return
		}
	} else if !os.IsNotExist(err) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "读取现有配置失败: " + err.Error()})
		return
	}
	if existing == nil {
		// 同 handleConfig：空内容 / 纯注释 / 字面 null 会被解析成 nil map，
		// setNested 往 nil map 写会 panic。
		existing = make(map[string]interface{})
	}

	setNested := func(top, key string, val interface{}) {
		sub, ok := existing[top].(map[string]interface{})
		if !ok {
			sub = make(map[string]interface{})
			existing[top] = sub
		}
		sub[key] = val
	}

	setNested("admin", "token", req.Token)
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

	data, err := yaml.Marshal(existing)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to marshal config"})
		return
	}
	if len(oldData) > 0 {
		if err := os.WriteFile(cfgPath+".bak", oldData, fileMode); err != nil {
			logrus.Warnf("配置备份失败: %v", err)
		}
	}
	if err := writeFileAtomic(cfgPath, data, fileMode); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to write config file"})
		return
	}

	// 热更新内存中的 token：设置完成后立即可用新密码登录，无需重启
	s.setToken(req.Token)
	logrus.Info("初始化向导完成：admin.token 已设置" + obNote(req.ObMode, req.ObAddr))

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func obNote(mode, addr string) string {
	switch mode {
	case "ws-server", "ws-reverse":
		if addr != "" {
			return "；OneBot 连接已写入配置（重启 Bot 后生效）"
		}
	}
	return ""
}
