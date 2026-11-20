package cfg

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestMultiKeyCredentialSaveIsAtomicAndPreservesSource(t *testing.T) {
	old := config.GlobalConfig
	defer func() { config.GlobalConfig = old }()
	path := filepath.Join(t.TempDir(), "app.yaml")
	initial := []byte("\ufeff# header\r\nbilibili:\r\n    sessdata: old # keep\r\n    bili_jct: old\r\nother: untouched\r\n")
	if e := os.WriteFile(path, initial, 0600); e != nil {
		t.Fatal(e)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if e := c.ReadInConfig(); e != nil {
		t.Fatal(e)
	}
	config.GlobalConfig = c
	calls := 0
	c.OnPrepareReload(func(p, n *config.Snapshot) (func(), func(), error) {
		calls++
		if n.GetString("bilibili.sessdata") != "new" || n.GetString("bilibili.bili_jct") != "new" {
			t.Fatal("partial candidate")
		}
		return nil, nil, errors.New("reject")
	})
	if e := WriteConfigKeyValues(map[string]string{"bilibili.SESSDATA": "new", "bilibili.bili_jct": "new"}); e == nil {
		t.Fatal("rejected candidate persisted")
	}
	disk, _ := os.ReadFile(path)
	if !bytes.Equal(disk, initial) || calls != 1 || c.Status().Revision != 1 {
		t.Fatal("failed pair changed source/state", calls)
	}
	c.OnPrepareReload(func(p, n *config.Snapshot) (func(), func(), error) {
		calls++
		if n.GetString("bilibili.sessdata") != "new" || n.GetString("bilibili.bili_jct") != "new" {
			t.Fatal("partial pair")
		}
		return nil, nil, nil
	})
	if e := WriteConfigKeyValues(map[string]string{"bilibili.SESSDATA": "new", "bilibili.bili_jct": "new"}); e != nil {
		t.Fatal(e)
	}
	disk, _ = os.ReadFile(path)
	if calls != 2 || c.Status().Revision != 2 || !bytes.Contains(disk, []byte("# keep\r\n")) || !bytes.Contains(disk, []byte("other: untouched\r\n")) {
		t.Fatal("multi-key save did not preserve other text", calls, string(disk))
	}
}
