package cfg

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// withViperConfigFile 指向一个临时配置文件，并保证测试结束后还原全局 viper。
// 用 SetConfigFile 而不是 chdir：被修的正是「硬编码路径」这一类写法。
func withViperConfigFile(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	old := config.GlobalConfig
	t.Cleanup(func() { config.GlobalConfig = old })
	v := viper.New()
	v.SetConfigFile(path)
	config.GlobalConfig = &config.Config{Viper: v}
}

// captureLogrus 把标准 logger 的输出接到缓冲区，测试结束还原。
func captureLogrus(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := logrus.StandardLogger().Out
	logrus.SetOutput(&buf)
	t.Cleanup(func() { logrus.SetOutput(old) })
	return &buf
}

// 正常写法：半角冒号 → 读到映射，且读的是 viper 实际加载的那个文件。
func TestReloadCustomCommandPrefixReadsViperFile(t *testing.T) {
	withViperConfigFile(t, "customCommandPrefix:\n  签到: \"\"\n  帮助: \"!\"\n")

	ReloadCustomCommandPrefix()

	got := GetCustomCommandPrefix()
	if len(got) != 2 {
		t.Fatalf("应读到 2 条自定义前缀，got %v", got)
	}
	if got["签到"] != "" || got["帮助"] != "!" {
		t.Fatalf("前缀值不对，got %v", got)
	}
}

// 事故形态：全角冒号「：」不是 YAML 键分隔符，整个块退化成一段多行字符串，
// cast.ToStringMapString 拿到空 map 且不报错——用户只看到「自定义前缀不生效」。
//
// ReloadCustomCommandPrefix 没有错误返回值，所以这里钉住两件事：
// 全角冒号就是读不到；而且必须落一条点明原因的 error 日志。
func TestReloadCustomCommandPrefixFullwidthColonYieldsNothing(t *testing.T) {
	withViperConfigFile(t, "customCommandPrefix:\n  签到：\"\"\n")
	buf := captureLogrus(t)

	ReloadCustomCommandPrefix()

	if got := GetCustomCommandPrefix(); len(got) != 0 {
		t.Fatalf("全角冒号不该解析出任何前缀，got %v", got)
	}
	if !strings.Contains(buf.String(), "全角") {
		t.Fatalf("应报出全角冒号这个原因，实际日志：%s", buf.String())
	}
}

// 显式空表（customCommandPrefix: {}）是合法配置——WebUI 里清空条目写出来的就是这个，
// 必须静默通过，不能刷「请检查全角冒号」这种内容错误的告警。
func TestReloadCustomCommandPrefixEmptyMapIsNotAnError(t *testing.T) {
	withViperConfigFile(t, "customCommandPrefix: {}\n")
	buf := captureLogrus(t)

	ReloadCustomCommandPrefix()

	if got := GetCustomCommandPrefix(); len(got) != 0 {
		t.Fatalf("空表应解析出 0 条，got %v", got)
	}
	if strings.Contains(buf.String(), "解析结果为空") {
		t.Fatalf("空表是合法配置，不该报错，实际日志：%s", buf.String())
	}
}

// 空字符串是写错了（不是映射），要报——但不能赖到全角冒号头上。
func TestReloadCustomCommandPrefixEmptyStringWarnsWithoutBlamingColon(t *testing.T) {
	withViperConfigFile(t, "customCommandPrefix: \"\"\n")
	buf := captureLogrus(t)

	ReloadCustomCommandPrefix()

	logged := buf.String()
	if !strings.Contains(logged, "解析结果为空") {
		t.Fatalf("空字符串应报警，实际日志：%s", logged)
	}
	if strings.Contains(logged, "全角") {
		t.Fatalf("空字符串不该赖全角冒号，实际日志：%s", logged)
	}
}
