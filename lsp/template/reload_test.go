package template

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/viper"
)

func reloadSnapshot(values map[string]interface{}) *config.Snapshot {
	c := &config.Config{Viper: viper.New()}
	for key, value := range values {
		c.Set(key, value)
	}
	return c.Snapshot()
}

func TestTemplateRuntimeEnableDisableAndInvalidRetains(t *testing.T) {
	t.Chdir(t.TempDir())
	previousLogger := logger
	localLogger := logrus.New()
	hook := logrustest.NewLocal(localLogger)
	logger = localLogger.WithField("module", "template")
	t.Cleanup(func() {
		Close()
		logger = previousLogger
	})
	if err := os.Mkdir("template", 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("template", "test.tmpl")
	if err := os.WriteFile(path, []byte(`{{define "reload-test"}}custom{{end}}`), 0600); err != nil {
		t.Fatal(err)
	}
	off := reloadSnapshot(map[string]interface{}{"template.enable": false})
	on := reloadSnapshot(map[string]interface{}{"template.enable": true})
	commit, _, err := PrepareRuntimeReload(off, on)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if LoadTemplate("reload-test") == nil {
		t.Fatal("external template missing")
	}
	if err = os.WriteFile(path, []byte(`{{define`), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		entry := hook.LastEntry()
		if entry != nil && entry.Message == "自定义模板更新失败，保留上次有效模板" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher did not process the invalid template")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if LoadTemplate("reload-test") == nil {
		t.Fatal("invalid file replaced working generation")
	}
	commit, _, err = PrepareRuntimeReload(on, off)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if LoadTemplate("reload-test") != nil {
		t.Fatal("disabled external template remained active")
	}
	if _, _, err = PrepareRuntimeReload(off, on); err == nil {
		t.Fatal("invalid template enabled")
	}
}

func TestExternalDBLeaseDrainAndFailureRetain(t *testing.T) {
	defer CloseTemplateDB()
	if err := InitTemplateDB(":memory:"); err != nil {
		t.Fatal(err)
	}
	if result := Set("probe", "value"); result != "" {
		t.Fatal(result)
	}
	old := GetTemplateDB()
	if _, _, err := prepareTemplateDB(true, filepath.Join(t.TempDir(), "missing", "db")); err == nil {
		t.Fatal("bad path accepted")
	}
	if Get("probe") != "value" {
		t.Fatal("failed prepare replaced database")
	}
	commit, _, err := prepareTemplateDB(true, filepath.Join(t.TempDir(), "next.db"))
	if err != nil {
		t.Fatal(err)
	}
	templateDBMu.RLock()
	done := make(chan struct{})
	go func() { commit(); close(done) }()
	select {
	case <-done:
		t.Fatal("DB closed before lease drained")
	case <-time.After(20 * time.Millisecond):
	}
	if old.DB == nil {
		t.Fatal("old database closed during lease")
	}
	templateDBMu.RUnlock()
	<-done
	if old.DB != nil {
		t.Fatal("old database not closed")
	}
	if Get("probe") == "value" {
		t.Fatal("database path switch unexpectedly migrated data")
	}
}
