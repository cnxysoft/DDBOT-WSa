package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCollectConfigWriteViolations 覆盖「整体写」的鉴权：
// 必须挡掉受保护键/敏感键的真实变更，同时不能误伤
// GET 脱敏后原样回传的占位符，以及未发生的变更。
func TestCollectConfigWriteViolations(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]interface{}
		updated  map[string]interface{}
		want     []string
	}{
		{
			name:     "脱敏占位符回传不算修改",
			existing: map[string]interface{}{"admin": map[string]interface{}{"token": "real-secret"}},
			updated:  map[string]interface{}{"admin": map[string]interface{}{"token": redactedPlaceholder}},
			want:     nil,
		},
		{
			name:     "改成真实 token 被拒绝",
			existing: map[string]interface{}{"admin": map[string]interface{}{"token": "old-secret"}},
			updated:  map[string]interface{}{"admin": map[string]interface{}{"token": "new-secret"}},
			want:     []string{"admin.token"},
		},
		{
			name:     "清空已有 token 被拒绝",
			existing: map[string]interface{}{"admin": map[string]interface{}{"token": "old-secret"}},
			updated:  map[string]interface{}{"admin": map[string]interface{}{"token": ""}},
			want:     []string{"admin.token"},
		},
		{
			name:     "原值相同不算修改",
			existing: map[string]interface{}{"admin": map[string]interface{}{"token": "same"}},
			updated:  map[string]interface{}{"admin": map[string]interface{}{"token": "same"}},
			want:     nil,
		},
		{
			name:     "新增 websocket token 被拒绝",
			existing: map[string]interface{}{"websocket": map[string]interface{}{"mode": "ws-server"}},
			updated: map[string]interface{}{
				"websocket": map[string]interface{}{"mode": "ws-server", "token": "abc"},
			},
			want: []string{"websocket.token"},
		},
		{
			name:     "双方都未设置不算修改",
			existing: map[string]interface{}{"websocket": map[string]interface{}{"mode": "ws-server"}},
			updated:  map[string]interface{}{"websocket": map[string]interface{}{"token": ""}},
			want:     nil,
		},
		{
			name:     "telegram token 变更被拒绝",
			existing: map[string]interface{}{"telegram": map[string]interface{}{"token": "t1"}},
			updated:  map[string]interface{}{"telegram": map[string]interface{}{"token": "t2"}},
			want:     []string{"telegram.token"},
		},
		{
			name:     "普通配置变更允许",
			existing: map[string]interface{}{"proxy": map[string]interface{}{"type": "systemProxy"}},
			updated:  map[string]interface{}{"proxy": map[string]interface{}{"type": "off"}},
			want:     nil,
		},
		{
			name:     "嵌套多层仍能定位",
			existing: map[string]interface{}{},
			updated: map[string]interface{}{
				"a": map[string]interface{}{
					"b": map[string]interface{}{"websocket": map[string]interface{}{"token": "x"}},
				},
			},
			want: []string{"a.b.websocket.token"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectConfigWriteViolations(tt.existing, tt.updated, "")
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

// TestWriteFileAtomic 验证原子写：内容正确落盘、权限收紧、不残留临时文件。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "application.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old: value\n"), 0600))

	require.NoError(t, writeFileAtomic(path, []byte("new: value\n"), 0600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new: value\n", string(data))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "原子写不应残留临时文件，实际: %v", entries)
}
