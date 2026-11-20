package cfg

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestMultiKeyWriteValidatesAndPersistsOneCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.yaml")
	before := []byte("# account\r\nbilibili:\r\n  sessdata: old # session\r\n  bili_jct: old # csrf\r\nweibo:\r\n  mode: guest\r\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	saves := 0
	err := updateConfigKeyValues(map[string]string{"bilibili.SESSDATA": "new-session", "bilibili.bili_jct": "new-csrf"}, path, func(data []byte, persist func() error) error {
		saves++
		v := viper.New()
		v.SetConfigType("yaml")
		if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
			return err
		}
		if v.GetString("bilibili.SESSDATA") != "new-session" || v.GetString("bilibili.bili_jct") != "new-csrf" {
			t.Fatal("partial credential candidate")
		}
		return persist()
	})
	if err != nil || saves != 1 {
		t.Fatal(err, saves)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Contains(after, []byte("sessdata: \"new-session\" # session\r\n")) || !bytes.Contains(after, []byte("weibo:\r\n  mode: guest\r\n")) {
		t.Fatal("format or unrelated setting changed", string(after))
	}
	if err := updateConfigKeyValues(map[string]string{"weibo.mode": "login", "invalid": "value"}, path, nil); err == nil {
		t.Fatal("invalid key accepted")
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(after, unchanged) {
		t.Fatal("invalid multikey write partially persisted")
	}
}
