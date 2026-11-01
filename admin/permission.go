package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
)

// 权限与命令管理 API。
//
// 数据层全部复用 lsp/permission 的 StateManager（buntdb 持久化）：
//   - 管理员（GrantRole/UngrantRole/ListAdmin）
//   - 群管理员（GrantGroupRole/UngrantGroupRole/ListGroupAdmin）
//   - 命令开关（EnableGroupCommand/DisableGroupCommand + GlobalEnable/GlobalDisable）
//   - 静默（GlobalSilence/GroupSilence）
//   - 黑名单（AddBlockList/DeleteBlockList/ListBlockList，带 TTL）
//
// admin 包自身不做权限数据存储。

func (s *Server) permissionSM() *permission.StateManager {
	return lsp.Instance.PermissionStateManager
}

// ---- 请求结构 ----

type AdminActionRequest struct {
	QQ     int64  `json:"qq"`
	Action string `json:"action"` // grant | ungrant
}

type GroupAdminActionRequest struct {
	GroupCode int64  `json:"groupCode"`
	QQ        int64  `json:"qq"`
	Action    string `json:"action"`
}

type SilenceRequest struct {
	Scope     string `json:"scope"` // global | group
	GroupCode int64  `json:"groupCode"`
	Action    string `json:"action"` // on | off
}

type BlockListRequest struct {
	QQ          int64  `json:"qq"`
	Action      string `json:"action"` // add | remove
	DurationSec int64  `json:"durationSec"`
}

type CommandGlobalRequest struct {
	Command string `json:"command"`
	Action  string `json:"action"` // enable | disable
}

type CommandGroupRequest struct {
	GroupCode int64  `json:"groupCode"`
	Command   string `json:"command"`
	Action    string `json:"action"` // enable | disable
}

// ---- 响应 ----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---- 权限 ----

// handlePermissionsOverview GET /api/v1/permissions/overview
func (s *Server) handlePermissionsOverview(w http.ResponseWriter, r *http.Request) {
	sm := s.permissionSM()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"admins":        sm.ListAdmin(),
		"blockList":     sm.ListBlockList(),
		"globalSilence": sm.CheckGlobalSilence(),
	})
}

// handleAdminAction POST /api/v1/permissions/admin
func (s *Server) handleAdminAction(w http.ResponseWriter, r *http.Request) {
	var req AdminActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QQ == 0 {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	sm := s.permissionSM()
	var err error
	switch req.Action {
	case "grant":
		err = sm.GrantRole(req.QQ, permission.Admin)
	case "ungrant":
		err = sm.UngrantRole(req.QQ, permission.Admin)
	default:
		writeErr(w, http.StatusBadRequest, "action must be grant or ungrant")
		return
	}
	if err != nil && !errors.Is(err, permission.ErrPermissionExist) && !errors.Is(err, permission.ErrPermissionNotExist) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 已授权/未授权视为幂等成功
	writeJSON(w, http.StatusOK, map[string]interface{}{"admins": sm.ListAdmin()})
}

// handleGroupAdmins GET /api/v1/permissions/group-admins?groupCode=
func (s *Server) handleGroupAdmins(w http.ResponseWriter, r *http.Request) {
	groupCode, err := strconv.ParseInt(r.URL.Query().Get("groupCode"), 10, 64)
	if err != nil || groupCode == 0 {
		writeErr(w, http.StatusBadRequest, "invalid groupCode")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"groupAdmins":  s.permissionSM().ListGroupAdmin(groupCode),
		"groupSilence": s.permissionSM().CheckGroupSilence(groupCode),
	})
}

// handleGroupAdminAction POST /api/v1/permissions/group-admin
func (s *Server) handleGroupAdminAction(w http.ResponseWriter, r *http.Request) {
	var req GroupAdminActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GroupCode == 0 || req.QQ == 0 {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	sm := s.permissionSM()
	var err error
	switch req.Action {
	case "grant":
		err = sm.GrantGroupRole(req.GroupCode, req.QQ, permission.GroupAdmin)
	case "ungrant":
		err = sm.UngrantGroupRole(req.GroupCode, req.QQ, permission.GroupAdmin)
	default:
		writeErr(w, http.StatusBadRequest, "action must be grant or ungrant")
		return
	}
	if err != nil && !errors.Is(err, permission.ErrPermissionExist) && !errors.Is(err, permission.ErrPermissionNotExist) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"groupAdmins": sm.ListGroupAdmin(req.GroupCode)})
}

// handleSilence POST /api/v1/permissions/silence
func (s *Server) handleSilence(w http.ResponseWriter, r *http.Request) {
	var req SilenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	sm := s.permissionSM()
	var err error
	switch req.Scope {
	case "global":
		if req.Action == "on" {
			err = sm.GlobalSilence()
		} else if req.Action == "off" {
			err = sm.UndoGlobalSilence()
		} else {
			writeErr(w, http.StatusBadRequest, "action must be on or off")
			return
		}
	case "group":
		if req.GroupCode == 0 {
			writeErr(w, http.StatusBadRequest, "groupCode required")
			return
		}
		if req.Action == "on" {
			err = sm.GroupSilence(req.GroupCode)
		} else if req.Action == "off" {
			err = sm.UndoGroupSilence(req.GroupCode)
		} else {
			writeErr(w, http.StatusBadRequest, "action must be on or off")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "scope must be global or group")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleBlockList POST /api/v1/permissions/blocklist
func (s *Server) handleBlockList(w http.ResponseWriter, r *http.Request) {
	var req BlockListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QQ == 0 {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	sm := s.permissionSM()
	switch req.Action {
	case "add":
		d := time.Duration(req.DurationSec) * time.Second
		if d <= 0 {
			d = 24 * time.Hour
		}
		if err := sm.AddBlockList(req.QQ, d); err != nil && !localdbErrKeyExist(err) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "remove":
		_ = sm.DeleteBlockList(req.QQ)
	default:
		writeErr(w, http.StatusBadRequest, "action must be add or remove")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"blockList": sm.ListBlockList()})
}

func localdbErrKeyExist(err error) bool {
	return errors.Is(err, buntdb.ErrKeyExist)
}

// ---- 命令开关 ----

// CommandStatus 单个命令的开关状态
type CommandStatus struct {
	Command        string `json:"command"`
	GlobalDisabled bool   `json:"globalDisabled"`
	GroupEnabled   bool   `json:"groupEnabled,omitempty"`
	GroupDisabled  bool   `json:"groupDisabled,omitempty"`
}

// handleCommands GET /api/v1/commands?groupCode=（可选，带上则包含群级状态）
func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	sm := s.permissionSM()
	groupCode, _ := strconv.ParseInt(r.URL.Query().Get("groupCode"), 10, 64)

	group := make([]CommandStatus, 0, len(lsp.AllGroupCommands()))
	for _, cmd := range lsp.AllGroupCommands() {
		cs := CommandStatus{Command: cmd, GlobalDisabled: sm.CheckGlobalCommandDisabled(cmd)}
		if groupCode != 0 {
			cs.GroupEnabled = sm.CheckGroupCommandEnabled(groupCode, cmd)
			cs.GroupDisabled = sm.CheckGroupCommandDisabled(groupCode, cmd)
		}
		group = append(group, cs)
	}
	private := make([]CommandStatus, 0, len(lsp.AllPrivateCommands()))
	for _, cmd := range lsp.AllPrivateCommands() {
		private = append(private, CommandStatus{Command: cmd, GlobalDisabled: sm.CheckGlobalCommandDisabled(cmd)})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"group":   group,
		"private": private,
	})
}

// handleCommandGlobal POST /api/v1/commands/global
// 全局开关：disable 后所有群不再响应该命令；enable 恢复默认。
func (s *Server) handleCommandGlobal(w http.ResponseWriter, r *http.Request) {
	var req CommandGlobalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	sm := s.permissionSM()
	var err error
	switch req.Action {
	case "enable":
		err = sm.GlobalEnableGroupCommand(req.Command)
	case "disable":
		err = sm.GlobalDisableGroupCommand(req.Command)
	default:
		writeErr(w, http.StatusBadRequest, "action must be enable or disable")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, CommandStatus{Command: req.Command, GlobalDisabled: sm.CheckGlobalCommandDisabled(req.Command)})
}

// handleCommandGroup POST /api/v1/commands/group
// 群级开关：enable/disable 为该群的显式覆盖（全局禁用的命令无法在群内启用）。
func (s *Server) handleCommandGroup(w http.ResponseWriter, r *http.Request) {
	var req CommandGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GroupCode == 0 || req.Command == "" {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	sm := s.permissionSM()
	var err error
	switch req.Action {
	case "enable":
		err = sm.EnableGroupCommand(req.GroupCode, req.Command)
	case "disable":
		err = sm.DisableGroupCommand(req.GroupCode, req.Command)
	default:
		writeErr(w, http.StatusBadRequest, "action must be enable or disable")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, CommandStatus{
		Command:        req.Command,
		GlobalDisabled: sm.CheckGlobalCommandDisabled(req.Command),
		GroupEnabled:   sm.CheckGroupCommandEnabled(req.GroupCode, req.Command),
		GroupDisabled:  sm.CheckGroupCommandDisabled(req.GroupCode, req.Command),
	})
}
