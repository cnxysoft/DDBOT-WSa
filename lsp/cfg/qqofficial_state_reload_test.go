package cfg

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestQQOfficialStateFileWaitsForRestart(t *testing.T) {
	previousPolicy := resourceHotReload.Swap(true)
	t.Cleanup(func() { resourceHotReload.Store(previousPolicy) })
	for _, key := range []string{"qqofficial.state-file", "QQOfficial.State-File"} {
		if IsHotKey(key) {
			t.Errorf("state-file must require restart even with resource reload enabled: %s", key)
		}
	}
	if !IsHotKey("qqofficial.app-secret") {
		t.Fatal("same-identity credential updates must remain hot")
	}

	content := func(path, secret string) []byte {
		data := fmt.Sprintf("qqofficial:\n  app-id: app\n  app-secret: %q\n", secret)
		if path != "" {
			data += fmt.Sprintf("  state-file: %q\n", path)
		}
		return []byte(data)
	}
	for _, tc := range []struct{ name, original, desired string }{
		{"change", "old.json", "new.json"},
		{"add", "", "new.json"},
		{"remove", "old.json", ""},
	} {
		for _, writer := range []string{"save", "external"} {
			t.Run(tc.name+"/"+writer, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "application.yaml")
				if err := os.WriteFile(path, content(tc.original, "old"), 0600); err != nil {
					t.Fatal(err)
				}
				c := &config.Config{Viper: viper.New()}
				c.SetConfigFile(path)
				c.ConfigureReload(ValidateReload, IsHotKey)
				if err := c.ReadInConfig(); err != nil {
					t.Fatal(err)
				}
				activeSecret, expectedSecret := "old", "old"
				prepares, commits := 0, 0
				c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
					prepares++
					if previous.GetString("qqofficial.app-secret") != activeSecret ||
						next.GetString("qqofficial.app-secret") != expectedSecret {
						t.Fatal("credential preparation did not use the current runtime and candidate")
					}
					for _, snapshot := range []*config.Snapshot{previous, next} {
						if snapshot.GetString("qqofficial.state-file") != tc.original ||
							snapshot.IsSet("qqofficial.state-file") != (tc.original != "") {
							t.Fatal("state migration reached resource preparation")
						}
					}
					return func() {
						commits++
						activeSecret = next.GetString("qqofficial.app-secret")
					}, nil, nil
				})
				apply := func(statePath, secret string, pending []string) {
					t.Helper()
					data := content(statePath, secret)
					expectedSecret = secret
					persist := func() error { return os.WriteFile(path, data, 0600) }
					if writer == "save" {
						if err := c.Save(data, persist); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := persist(); err != nil {
							t.Fatal(err)
						}
						if err := c.Reload(); err != nil {
							t.Fatal(err)
						}
					}
					disk, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(disk, data) || !bytes.Equal(c.Snapshot().RawYAML(), data) {
						t.Fatal("source was not accepted and persisted", err)
					}
					if c.GetString("qqofficial.state-file") != tc.original || activeSecret != secret ||
						c.GetString("qqofficial.app-secret") != secret || c.Status().LastError != "" {
						t.Fatal("runtime state path changed or hot credentials did not apply")
					}
					if !reflect.DeepEqual(c.Status().RestartRequired, pending) {
						t.Fatalf("restartRequired = %v, want %v", c.Status().RestartRequired, pending)
					}
				}
				pending := []string{"qqofficial.state-file"}
				apply(tc.desired, "old", pending)
				apply(tc.desired, "new", pending)
				// A fresh process reads the saved path, without migrating any existing state.
				restarted := &config.Config{Viper: viper.New()}
				restarted.SetConfigFile(path)
				restarted.ConfigureReload(ValidateReload, IsHotKey)
				if err := restarted.ReadInConfig(); err != nil ||
					restarted.GetString("qqofficial.state-file") != tc.desired ||
					len(restarted.Status().RestartRequired) != 0 {
					t.Fatal("restart did not accept the saved state path", err)
				}
				apply(tc.original, "newer", []string{})
				if prepares != 3 || commits != 3 || c.Status().Revision != 4 {
					t.Fatal("transactions were skipped or duplicated", prepares, commits, c.Status())
				}
			})
		}
	}
}
