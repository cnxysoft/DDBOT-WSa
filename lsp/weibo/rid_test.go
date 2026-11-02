package weibo

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/spf13/viper"
)

// rid 必须本地生成，不许打外部服务。
//
// 回归背景：原来这里会去调 https://sc.znin.net/render 拿 rid，那个服务已经不可用，
// 访客模式整条链路（FreshCookieGuest）直接失败。
// 实测微博的 genvisitor2 接受任意字符串当 rid，本地随机即可。
func TestGetRidFallsBackToLocal(t *testing.T) {
	old := config.GlobalConfig
	defer func() { config.GlobalConfig = old }()
	config.GlobalConfig = &config.Config{Viper: viper.New()}

	info, err := getRid("UA-test")
	if err != nil {
		t.Fatalf("本地生成 rid 不该报错、也不该打外部服务：%v", err)
	}
	if len(info.Rid) != 32 {
		t.Fatalf("本地 rid 应为 32 位 hex，got %q", info.Rid)
	}
	if _, err := hex.DecodeString(info.Rid); err != nil {
		t.Fatalf("rid 应为 hex：%v", err)
	}
	if info.UA != "UA-test" {
		t.Fatalf("UA 应原样带回，got %q", info.UA)
	}
}

// 两次生成的 rid 不能一样。
func TestRandomRidIsUnique(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		r := randomRid()
		if len(r) != 32 {
			t.Fatalf("长度应为 32，got %q", r)
		}
		if seen[r] {
			t.Fatalf("生成了重复的 rid: %q", r)
		}
		seen[r] = true
	}
}

// 写完配置必须同步到内存里的 viper。
//
// 回归背景：GetWeiboMode() 靠「配置里有没有 weibo.sub」判断 guest/login。
// WriteBackConfig 只写文件、不同步 viper 的话，扫码登录成功后进程里仍然是
// guest 模式（热重载这会儿正被 MarkConfigWrite 挡着），要等文件被重新读入才切过去
// —— 表现就是「扫码登录成功了，还在跑访客 Cookie」。
func TestWriteBackConfigSyncsViper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "application.yaml")
	if err := os.WriteFile(path, []byte("weibo:\n  qrlogin: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := config.GlobalConfig
	defer func() { config.GlobalConfig = old }()
	v := viper.New()
	v.SetConfigFile(path)
	config.GlobalConfig = &config.Config{Viper: v}

	if err := WriteBackConfig("NEW-SUB-VALUE"); err != nil {
		t.Fatalf("写回配置失败：%v", err)
	}
	if got := config.GlobalConfig.GetString("weibo.sub"); got != "NEW-SUB-VALUE" {
		t.Fatalf("viper 里的 weibo.sub 应为 NEW-SUB-VALUE，got %q（只写了文件、没同步内存）", got)
	}
	if got := cfg.GetWeiboMode(); got != "login" {
		t.Fatalf("写入 SUB 后模式应为 login，got %q", got)
	}

	// 文件本身也得真的写进去
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "NEW-SUB-VALUE") {
		t.Fatalf("文件里没有写入 SUB：%s", data)
	}
}

// 扫码登录的顺序是 setRuntimeSUB → 刷新 Cookie → 落盘。
// 刷新 Cookie 那一刻如果只认配置，就会被判成 guest，白跑一次访客流程
// （而访客流程要打外部接口，实测能拖出 30 多秒）。
func TestIsGuestModeHonoursRuntimeSUB(t *testing.T) {
	old := config.GlobalConfig
	defer func() {
		config.GlobalConfig = old
		setRuntimeSUB("")
	}()
	config.GlobalConfig = &config.Config{Viper: viper.New()}

	if !isGuestMode() {
		t.Fatal("既没 SUB 也没配 mode 时应该是 guest")
	}

	setRuntimeSUB("runtime-sub")
	if isGuestMode() {
		t.Fatal("有运行时 SUB 时不该判成 guest")
	}

	// 显式配了 mode 就听配置的
	config.GlobalConfig.Set("weibo.mode", "guest")
	if !isGuestMode() {
		t.Fatal("显式配 weibo.mode=guest 时应为 guest")
	}
}
