package cfg

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestPublicCredentialWriterRejectsExternalEditDuringPrepare(t *testing.T) {
	const initial = "bilibili:\n  SESSDATA: old-session\n  bili_jct: old-csrf\nbot:\n  commandPrefix: /\n"
	const external = "bilibili:\n  SESSDATA: external-session\n  bili_jct: external-csrf\nbot:\n  commandPrefix: external\n"
	path := writeTempConfig(t, initial)
	previous := config.GlobalConfig
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig = c
	t.Cleanup(func() { config.GlobalConfig = previous })
	prepared, resume := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	t.Cleanup(release)
	committed, aborted := 0, 0
	c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		close(prepared)
		<-resume
		return func() { committed++ }, func() { aborted++ }, nil
	})
	done := make(chan error, 1)
	go func() {
		done <- WriteConfigKeyValues(map[string]string{"bilibili.SESSDATA": "new-secret-session", "bilibili.bili_jct": "new-secret-csrf"})
	}()
	select {
	case <-prepared:
	case <-time.After(3 * time.Second):
		t.Fatal("credential writer did not enter prepare")
	}
	if err := os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	err := <-done
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err == nil || !bytes.Equal(after, []byte(external)) || c.GetString("bilibili.sessdata") != "old-session" || c.GetString("bilibili.bili_jct") != "old-csrf" || c.Status().Revision != 1 || committed != 0 || aborted != 1 {
		t.Fatalf("credential writer lost external edit: err=%v revision=%d commit=%d abort=%d disk=%q", err, c.Status().Revision, committed, aborted, after)
	}
	if strings.Contains(err.Error(), "new-secret-session") || strings.Contains(err.Error(), "new-secret-csrf") || strings.Contains(err.Error(), "external-session") {
		t.Fatal("credential conflict error exposed secret values")
	}
}
