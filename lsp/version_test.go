package lsp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestCompareVersion(t *testing.T) {
	assert.True(t, compareVersion("v0.0.1", "v0.0.2"))
	assert.False(t, compareVersion("v0.0.2", "v0.0.2"))
	assert.False(t, compareVersion("v0.0.3", "v0.0.2"))
	assert.True(t, compareVersion("v0.0.1", "v0.0.18"))
	assert.True(t, compareVersion("v0.0.1", "v1.1.8"))
	assert.False(t, compareVersion("v1.1.9", "v1.1.8"))
	assert.False(t, compareVersion("v1.0.0", "v0.0.8"))
	assert.True(t, compareVersion("v0.0.1", "v0.0.10"))
	assert.False(t, compareVersion("v10.0.10", "v1.0.10"))
	assert.True(t, compareVersion("v1", "v2"))
	assert.True(t, compareVersion("v1", "v2.0"))
	assert.True(t, compareVersion("v1", "v2.0.0"))
	assert.True(t, compareVersion("v1.0", "v2.0.0"))
	assert.False(t, compareVersion("v1.0.0", "v1.0"))
	assert.False(t, compareVersion("v1.0.0", "v1"))
	assert.True(t, compareVersion("v1.0.0", "v2"))
	assert.True(t, compareVersion("v1.0.0", "v2.0"))
	assert.True(t, compareVersion("v1.0.0", "v2.0.0"))
	assert.True(t, compareVersion("v0.0.19", "v0.0.19.1"))
	assert.True(t, compareVersion("v0.0.19.1", "v0.0.20"))
}

func TestCheckUpdate(t *testing.T) {
	oldTags, oldOutput, oldLevel := Tags, logrus.StandardLogger().Out, logrus.GetLevel()
	t.Cleanup(func() {
		Tags = oldTags
		logrus.SetOutput(oldOutput)
		logrus.SetLevel(oldLevel)
	})
	for _, tc := range []struct {
		name, current, response, wantTag, wantLog string
	}{
		{"update", "v0.0.1", `{"tag_name":"v0.0.2"}`, "v0.0.2", "更新检测完成"},
		{"current", "v0.0.2", `{"tag_name":"v0.0.2"}`, "", "当前为DDBOT最新版本"},
		{"api error", "v0.0.1", `{"message":"rate limit exceeded"}`, "", "更新检测失败"},
		{"invalid response", "v0.0.1", `{}`, "", "更新检测失败"},
		{"development", "UNKNOWN", `{}`, "", "跳过更新检测"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			var logs bytes.Buffer
			logrus.SetOutput(&logs)
			logrus.SetLevel(logrus.DebugLevel)
			Tags = tc.current
			assert.Equal(t, tc.wantTag, checkUpdate(server.URL))
			assert.Contains(t, logs.String(), tc.wantLog)
		})
	}
}
