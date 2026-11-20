package weibo

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/stretchr/testify/assert"
)

// TestRuntimeSUB_UpdateAndRead 验证运行时 SUB 可被更新和读取
// 回归场景：运行中扫码更换 SUB 后执行周期刷新不应使用过期值
func TestRuntimeSUB_UpdateAndRead(t *testing.T) {
	original := getRuntimeSUB()
	defer setRuntimeSUB(original)

	setRuntimeSUB("")
	assert.Equal(t, "", getRuntimeSUB())

	newSub := "new_sub_from_qrlogin_1234567890"
	setRuntimeSUB(newSub)
	assert.Equal(t, newSub, getRuntimeSUB())

	// 周期刷新读取当前运行时值，应拿到新值而非启动时的旧值
	assert.Equal(t, newSub, getRuntimeSUB())

	newSub2 := "updated_sub_9876543210"
	setRuntimeSUB(newSub2)
	assert.Equal(t, newSub2, getRuntimeSUB())
	assert.NotEqual(t, newSub, getRuntimeSUB())
}

// TestQRLoginResult_Fields 验证 QRLoginResult 正确区分运行时加载与持久化状态
// 回归场景：配置文件不可写时命令回复应提示"运行时生效但未持久化"
func TestQRLoginResult_Fields(t *testing.T) {
	// 完全成功
	r := QRLoginResult{
		Sub:            "test_sub",
		RuntimeLoaded:  true,
		PersistSuccess: true,
	}
	assert.True(t, r.RuntimeLoaded)
	assert.True(t, r.PersistSuccess)
	assert.Nil(t, r.PersistError)

	// 运行时成功但持久化失败
	r2 := QRLoginResult{
		Sub:            "test_sub",
		RuntimeLoaded:  true,
		PersistSuccess: false,
		PersistError:   os.ErrPermission,
	}
	assert.True(t, r2.RuntimeLoaded)
	assert.False(t, r2.PersistSuccess)
	assert.NotNil(t, r2.PersistError)
}

// TestWriteBackConfig_PersistenceError 验证配置写入不可写路径时返回错误
// 回归场景：配置文件不可写时的命令回复
func TestWriteBackConfig_PersistenceError(t *testing.T) {
	nonExistentPath := filepath.Join(os.TempDir(), "nonexistent_dir_xyz", "application.yaml")
	err := writeBackConfigToPath("test_sub_value", nonExistentPath)
	assert.Error(t, err, "写入不存在的路径应返回错误")
}

// TestWriteBackConfig_AtomicWrite 验证配置写回使用原子替换并保留权限
func TestWriteBackConfig_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "application.yaml")

	initialContent := "weibo:\n  mode: login\n  sub: \"old_sub\"\n"
	err := os.WriteFile(cfgPath, []byte(initialContent), 0o600)
	assert.NoError(t, err)

	err = writeBackConfigToPath("new_sub_12345", cfgPath)
	assert.NoError(t, err)

	data, err := os.ReadFile(cfgPath)
	assert.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "new_sub_12345")
	assert.NotContains(t, content, "old_sub")

	// 权限保留（Unix only，Windows 无 Unix 权限位）
	if runtime.GOOS != "windows" {
		info, err := os.Stat(cfgPath)
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	// 无残留临时文件
	_, err = os.Stat(cfgPath + ".tmp")
	assert.True(t, os.IsNotExist(err), "临时文件应已被清理")
}

// TestWriteBackConfig_PreservesOtherFields 验证写回时保留其他 weibo 配置字段
func TestWriteBackConfig_PreservesOtherFields(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "application.yaml")

	initialContent := `weibo:
  mode: login
  sub: "old_sub"
  qrlogin: true
  interval: 60s
other:
  key: value
`
	err := os.WriteFile(cfgPath, []byte(initialContent), 0o644)
	assert.NoError(t, err)

	err = writeBackConfigToPath("new_sub_value", cfgPath)
	assert.NoError(t, err)

	data, err := os.ReadFile(cfgPath)
	assert.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "new_sub_value")
	assert.Contains(t, content, "qrlogin: true")
	assert.Contains(t, content, "interval: 60s")
	assert.Contains(t, content, "other:")
	assert.Contains(t, content, "key: value")
}

// TestWriteBackConfig_NoWeiboBlock 验证配置中没有 weibo 块时追加新块
func TestWriteBackConfig_NoWeiboBlock(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "application.yaml")

	initialContent := "bot:\n  qq: 123456\n"
	err := os.WriteFile(cfgPath, []byte(initialContent), 0o644)
	assert.NoError(t, err)

	err = writeBackConfigToPath("appended_sub", cfgPath)
	assert.NoError(t, err)

	data, err := os.ReadFile(cfgPath)
	assert.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "weibo:")
	assert.Contains(t, content, "appended_sub")
	assert.Contains(t, content, "bot:")
	assert.Contains(t, content, "123456")
}

// TestCanDeliverMessage_NilBot 验证 bot 未初始化时 canDeliverMessage 返回 false
// 回归场景：WS 断开期间告警失败及重连重试
func TestCanDeliverMessage_NilBot(t *testing.T) {
	// 测试环境中 bot.Instance 通常为 nil，应返回 false
	assert.False(t, canDeliverMessage(), "bot 未初始化时 canDeliverMessage 应返回 false")
}

// TestEventBusPublishBotOnline 验证 bot_online 事件可被发布和订阅
// 回归场景：bot_online 事件没有发布者导致协程不被唤醒
func TestEventBusPublishBotOnline(t *testing.T) {
	bus := eventbus.New()

	ch := bus.Subscribe("bot_online")
	bus.Publish("bot_online", true)

	select {
	case msg := <-ch:
		m, ok := msg.(bool)
		assert.True(t, ok, "事件类型应为 bool")
		assert.True(t, m, "应收到 true 表示 bot 已上线")
	default:
		t.Fatal("应收到 bot_online 事件")
	}
}

// TestEventBusBotOnlineMultipleSubscribers 验证多个订阅者都能收到事件
func TestEventBusBotOnlineMultipleSubscribers(t *testing.T) {
	bus := eventbus.New()

	ch1 := bus.Subscribe("bot_online")
	ch2 := bus.Subscribe("bot_online")

	bus.Publish("bot_online", true)

	select {
	case <-ch1:
	default:
		t.Fatal("订阅者1应收到事件")
	}

	select {
	case <-ch2:
	default:
		t.Fatal("订阅者2应收到事件")
	}
}

// TestIsAuthFailure_Classification 验证错误分类：
// 只把登录页响应（HTML JSON 解析失败）和 HTTP 401/403 归为凭据失效，
// 429 限流、414 请求错误、空响应/截断 JSON 等按临时错误或状态未知处理，
// 避免非鉴权错误错误触发凭据失效告警。
func TestIsAuthFailure_Classification(t *testing.T) {
	// 明确的凭据失效：API 返回 HTML 登录页
	assert.True(t, isAuthFailure(errors.New("invalid character '<' looking for beginning of value")))

	// 明确的鉴权拒绝：HTTP 401/403
	assert.True(t, isAuthFailure(errors.New("http code error 401")))
	assert.True(t, isAuthFailure(errors.New("http code error 403")))

	// 空响应/截断 JSON：可能是临时上游故障，不应视为凭据失效
	assert.False(t, isAuthFailure(errors.New("unexpected end of JSON input")))
	assert.False(t, isAuthFailure(errors.New("cannot unmarshal number into Go value of type string")))

	// 非鉴权类 HTTP 4xx：429 限流、414 请求错误，不应触发凭据失效告警
	assert.False(t, isAuthFailure(errors.New("http code error 429")))
	assert.False(t, isAuthFailure(errors.New("http code error 414")))

	// 网络层错误：DNS、超时、连接拒绝，以及 HTTP 5xx 服务端错误
	assert.False(t, isAuthFailure(errors.New("dial tcp 127.0.0.1:80: connect: connection refused")))
	assert.False(t, isAuthFailure(errors.New("http code error 502")))
	assert.False(t, isAuthFailure(nil))
}

// TestSanitizeLoginURL_MasksSensitiveParams 验证登录跳转 URL 日志脱敏，
// 避免完整 ALT/ticket 凭据写入日志。
func TestSanitizeLoginURL_MasksSensitiveParams(t *testing.T) {
	u, err := url.Parse("https://passport.weibo.com/sso/v2/login?alt=ALT-very-secret-token&entry=miniblog&ticket=ticket-abc-123")
	assert.NoError(t, err)

	sanitized := sanitizeLoginURL(u)
	assert.NotContains(t, sanitized, "ALT-very-secret-token", "不应泄露完整 ALT")
	assert.NotContains(t, sanitized, "ticket-abc-123", "不应泄露完整 ticket")
	// url.Values.Encode() 会将 *** 编码为 %2A%2A%2A
	assert.Contains(t, sanitized, "%2A%2A%2A", "敏感参数值应被脱敏")
	assert.Contains(t, sanitized, "entry=miniblog", "非敏感参数应保留")

	assert.Equal(t, "", sanitizeLoginURL(nil))
}

// TestWriteBackConfig_InsertKeepsSiblingIndent 覆盖「weibo 块存在但没有 sub 键」这条路径：
// 插入的 sub 必须与块内兄弟键同缩进。写死两空格会让 4 空格缩进的块变成混合缩进，
// YAML 直接不合法 —— 和 lsp/cfg 的 yamlutil 是同一个坑。
func TestWriteBackConfig_InsertKeepsSiblingIndent(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "application.yaml")

	initialContent := "weibo:\n    mode: login\n    qrlogin: true\n"
	assert.NoError(t, os.WriteFile(cfgPath, []byte(initialContent), 0o644))

	assert.NoError(t, writeBackConfigToPath("sub_value", cfgPath))

	data, err := os.ReadFile(cfgPath)
	assert.NoError(t, err)
	want := "weibo:\n    mode: login\n    qrlogin: true\n    sub: \"sub_value\"\n"
	assert.Equal(t, want, string(data))
}

// 键名大小写不敏感（viper 如此）：文件里写的是大写 SUB 时要原地替换，
// 不能在块尾另起一行小写 sub 造成两个等价键。
func TestWriteBackConfig_ReplacesKeyCaseInsensitively(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "application.yaml")

	initialContent := "weibo:\n  SUB: \"old\"\n  qrlogin: true\n"
	assert.NoError(t, os.WriteFile(cfgPath, []byte(initialContent), 0o644))

	assert.NoError(t, writeBackConfigToPath("new_value", cfgPath))

	data, err := os.ReadFile(cfgPath)
	assert.NoError(t, err)
	want := "weibo:\n  SUB: \"new_value\"\n  qrlogin: true\n"
	assert.Equal(t, want, string(data))
}
