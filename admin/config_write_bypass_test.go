package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCollectConfigWriteViolationsParentReplacement 覆盖审查报告 M1：
// 旧校验只拦「命中受保护键的叶子标量」，因此 {"admin":null}、{"admin":"x"}、
// {"websocket":{"token":{...}}} 都能通过并落盘，把 admin.token/websocket.token 清掉，
// 导致重启后匿名 setup 窗口重开或 OneBot 鉴权 token 失效。
func TestCollectConfigWriteViolationsParentReplacement(t *testing.T) {
	existing := map[string]interface{}{
		"admin":     map[string]interface{}{"token": "real-secret", "enable": true},
		"websocket": map[string]interface{}{"token": "ob-secret", "mode": "ws-server"},
		"telegram":  map[string]interface{}{"token": "tg-secret"},
	}

	tests := []struct {
		name    string
		updated map[string]interface{}
		want    []string
	}{
		{
			name:    "admin 置 nil（整体替换父键）被拒绝",
			updated: map[string]interface{}{"admin": nil},
			want:    []string{"admin.token"},
		},
		{
			name:    "admin 换成标量被拒绝",
			updated: map[string]interface{}{"admin": "x"},
			want:    []string{"admin.token"},
		},
		{
			name:    "websocket 置 nil 被拒绝",
			updated: map[string]interface{}{"websocket": nil},
			want:    []string{"websocket.token"},
		},
		{
			name: "token 类型改成 map 被拒绝",
			updated: map[string]interface{}{
				"websocket": map[string]interface{}{"token": map[string]interface{}{"x": 1}},
			},
			want: []string{"websocket.token"},
		},
		{
			name: "token 类型改成数字被拒绝",
			updated: map[string]interface{}{
				"admin": map[string]interface{}{"token": 12345},
			},
			want: []string{"admin.token"},
		},
		{
			name: "token 类型改成列表被拒绝",
			updated: map[string]interface{}{
				"admin": map[string]interface{}{"token": []interface{}{"a"}},
			},
			want: []string{"admin.token"},
		},
		{
			name:    "无受保护键的父键整体替换允许（telegram 凭据可写不可读）",
			updated: map[string]interface{}{"telegram": nil},
			want:    nil,
		},
		{
			name:    "只改 admin 下的非受保护键允许",
			updated: map[string]interface{}{"admin": map[string]interface{}{"enable": false}},
			want:    nil,
		},
		{
			name:    "只改 websocket.mode 允许",
			updated: map[string]interface{}{"websocket": map[string]interface{}{"mode": "ws-reverse"}},
			want:    nil,
		},
		{
			name:    "受保护键原样回传允许",
			updated: map[string]interface{}{"admin": map[string]interface{}{"token": "real-secret"}},
			want:    nil,
		},
		{
			name:    "受保护键占位符回传允许",
			updated: map[string]interface{}{"admin": map[string]interface{}{"token": redactedPlaceholder}},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectConfigWriteViolations(existing, tt.updated, "")
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

// TestProtectedPathsInSubtree 父键被删除/替换时，需要报出被牵连的具体受保护键路径。
func TestProtectedPathsInSubtree(t *testing.T) {
	sub := map[string]interface{}{
		"token": "secret",
		"mode":  "ws-server",
		"nested": map[string]interface{}{
			"websocket": map[string]interface{}{"token": "deep-secret"},
		},
	}
	assert.ElementsMatch(t,
		[]string{"websocket.token", "websocket.nested.websocket.token"},
		protectedPathsInSubtree(sub, "websocket"))
}
