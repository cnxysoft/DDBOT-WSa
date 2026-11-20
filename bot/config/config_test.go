package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

func testConfig(t *testing.T, content string) (*Config, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.StopWatching)
	return c, path
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("configuration change was not observed")
}

func TestWatchSurvivesAtomicReplacementAndRecreation(t *testing.T) {
	c, path := testConfig(t, "value: first\n")
	var callbacks atomic.Int32
	c.OnConfigChange(func(fsnotify.Event) { callbacks.Add(1) })
	c.WatchConfig()
	c.WatchConfig()
	writeConfig(t, path+".tmp", "value: second\n")
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return c.GetString("value") == "second" && callbacks.Load() == 1 })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, path, "value: third\n")
	await(t, func() bool { return c.GetString("value") == "third" && callbacks.Load() == 2 })
	writeConfig(t, path, "value: fourth\n")
	await(t, func() bool { return c.GetString("value") == "fourth" && callbacks.Load() == 3 })
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if callbacks.Load() != 3 {
		t.Fatal("duplicate event reapplied configuration")
	}
	c.StopWatching()
	c.StopWatching()
	if c.Status().Watching {
		t.Fatal("watcher did not stop")
	}
	c.WatchConfig()
	writeConfig(t, path, "value: fifth\n")
	await(t, func() bool { return c.GetString("value") == "fifth" })
}

func TestInvalidReloadAndSaveRetainLastGood(t *testing.T) {
	c, path := testConfig(t, "value: good\n")
	c.ConfigureReload(func(s *Snapshot) error {
		if s.GetString("value") == "invalid" {
			return fmt.Errorf("value rejected")
		}
		return nil
	}, nil)
	before := c.Status().Revision
	for _, data := range []string{"value: [broken", "value: invalid\n"} {
		writeConfig(t, path, data)
		if c.Reload() == nil {
			t.Fatal("invalid configuration accepted")
		}
		if c.GetString("value") != "good" || c.Status().Revision != before || c.Status().LastError == "" {
			t.Fatal("failed reload changed running configuration")
		}
		persisted := false
		if err := c.Save([]byte(data), func() error { persisted = true; return nil }); err == nil || persisted {
			t.Fatal("invalid save touched disk")
		}
	}
	writeConfig(t, path, "value: recovered\n")
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.Status().LastError != "" {
		t.Fatal("error did not clear after recovery")
	}
}

func TestHotPolicyTracksRestartAndReverts(t *testing.T) {
	c, path := testConfig(t, "hot: old\ncold:\n  token: old\n  removed: yes\n")
	c.ConfigureReload(nil, func(k string) bool { return k == "hot" })
	writeConfig(t, path, "hot: new\ncold:\n  token: new\nnewCold: value\n")
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("hot") != "new" || c.GetString("cold.token") != "old" || c.IsSet("newCold") {
		t.Fatal("policy did not preserve running cold values")
	}
	if got := c.Status().RestartRequired; !reflect.DeepEqual(got, []string{"cold.removed", "cold.token", "newcold"}) {
		t.Fatal(got)
	}
	writeConfig(t, path, "hot: new\ncold:\n  token: old\n  removed: yes\n")
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(c.Status().RestartRequired) != 0 {
		t.Fatal("reverted settings still pending restart")
	}
	// Changing an entire resource subtree to a scalar must not partially replace it.
	writeConfig(t, path, "hot: new\ncold: disabled\n")
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("cold.token") != "old" || !reflect.DeepEqual(c.Status().RestartRequired, []string{"cold"}) {
		t.Fatal("subtree replacement was not preserved")
	}
}

func TestRuntimeOverridesAndSnapshotIsolation(t *testing.T) {
	c, path := testConfig(t, "token: file\nother: old\nlist: [a, b]\n")
	previous := c.Snapshot()
	c.Set("token", "runtime")
	if previous.GetString("token") != "file" {
		t.Fatal("Set mutated an existing snapshot")
	}
	writeConfig(t, path, "token: file\nother: new\nlist: [a, b]\n")
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("token") != "runtime" {
		t.Fatal("unrelated file change erased runtime override")
	}
	writeConfig(t, path, "token: explicit\nother: new\nlist: [a, b]\n")
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("token") != "explicit" {
		t.Fatal("runtime override masked explicit file change")
	}
	list := c.Get("list").([]interface{})
	list[0] = "mutated"
	if c.GetStringSlice("list")[0] != "a" {
		t.Fatal("getter exposed snapshot memory")
	}
	if c.ConfigFileUsed() != path {
		t.Fatal("copy-on-write lost file path")
	}
}

func TestConcurrentSnapshotReadersAndWriters(t *testing.T) {
	c, path := testConfig(t, "pair:\n  a: 0\n  b: 0\n")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 150; j++ {
				s := c.Snapshot()
				if s.GetInt("pair.a") != s.GetInt("pair.b") {
					t.Error("mixed generations")
				}
				_ = c.GetString("runtime")
				_ = c.Status()
			}
		})
	}
	wg.Go(func() {
		for i := 0; i < 40; i++ {
			c.Set("runtime", i)
		}
	})
	for i := 1; i < 40; i++ {
		writeConfig(t, path, fmt.Sprintf("pair:\n  a: %d\n  b: %d\n", i, i))
		if err := c.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

func TestSavePublishesAfterPersistenceAndCallback(t *testing.T) {
	c, _ := testConfig(t, "value: old\n")
	called := false
	c.OnConfigChange(func(fsnotify.Event) { called = c.GetString("value") == "new" })
	if err := c.Save([]byte("value: new\n"), func() error {
		if c.GetString("value") != "old" {
			t.Fatal("published before persistence")
		}
		return fmt.Errorf("disk full")
	}); err == nil || c.GetString("value") != "old" || called {
		t.Fatal("failed persistence published data")
	}
	if err := c.Save([]byte("value: new\n"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !called || c.Status().AppliedAt.IsZero() {
		t.Fatal("Save returned before application completed")
	}
}

func TestRuntimeValuesDoNotAliasCallerOwnedCollections(t *testing.T) {
	c, _ := testConfig(t, "value: original\n")
	slice := []int64{1, 2}
	c.Set("ids", slice)
	slice[0] = 99
	got := c.Get("ids").([]int64)
	if got[0] != 1 {
		t.Fatal("setter retained caller slice")
	}
	got[0] = 77
	if c.Get("ids").([]int64)[0] != 1 {
		t.Fatal("getter exposed stored slice")
	}
	typed := map[int]string{1: "old"}
	c.Set("typed", typed)
	typed[1] = "changed"
	result := c.Get("typed").(map[int]string)
	if result[1] != "old" {
		t.Fatal("setter retained typed map")
	}
	result[1] = "changed"
	if c.Get("typed").(map[int]string)[1] != "old" {
		t.Fatal("getter exposed typed map")
	}
}

func TestRuntimeSetterDoesNotClaimResourceWasRebuilt(t *testing.T) {
	c, path := testConfig(t, "token: old\nhot: old\n")
	c.ConfigureReload(nil, func(key string) bool { return key == "hot" })
	c.Set("token", "new")
	writeConfig(t, path, "token: new\nhot: new\n")
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Status().RestartRequired, []string{"token"}) {
		t.Fatal("runtime setter concealed a required resource restart")
	}
	c.MarkApplied("token")
	if len(c.Status().RestartRequired) != 0 {
		t.Fatal("explicitly applied resource still pending")
	}
}

func TestReadInConfigAfterInitializationUsesSafeReload(t *testing.T) {
	c, path := testConfig(t, "value: old\n")
	previous := c.Snapshot()
	writeConfig(t, path, "value: [invalid\n")
	if c.ReadInConfig() == nil || c.GetString("value") != "old" {
		t.Fatal("re-reading damaged the last good configuration")
	}
	writeConfig(t, path, "value: new\n")
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	if previous.GetString("value") != "old" || c.GetString("value") != "new" {
		t.Fatal("re-read mutated a previous snapshot")
	}
}

func TestRuntimeSetPreservesLegacyFilePathBeforeFirstRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.yaml")
	writeConfig(t, path, "token: file\n")
	v := viper.New()
	v.SetConfigFile(path)
	c := &Config{Viper: v}
	c.Set("token", "runtime")
	if c.ConfigFileUsed() != path {
		t.Fatal("Set lost legacy file path")
	}
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("token") != "runtime" {
		t.Fatal("initial load lost runtime override")
	}
}

func TestRuntimeSetPreservesSearchPathsBeforeFirstRead(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, "application.yaml"), "value: file\n")
	c := &Config{Viper: viper.New()}
	c.SetConfigName("application")
	c.SetConfigType("yaml")
	c.AddConfigPath(dir)
	c.Set("token", "runtime")
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("value") != "file" || c.GetString("token") != "runtime" {
		t.Fatal("search path or override lost")
	}
}

func TestStatusOnlyAdvancesAfterApplicationCompletes(t *testing.T) {
	c, _ := testConfig(t, "value: old\n")
	before := c.Status()
	entered, release := make(chan struct{}), make(chan struct{})
	c.OnConfigChange(func(fsnotify.Event) { close(entered); <-release })
	type result struct {
		status ReloadStatus
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		s, err := c.SaveAndStatus([]byte("value: new\n"), func() error { return nil })
		completed <- result{s, err}
	}()
	<-entered
	whileApplying := c.Status()
	close(release)
	got := <-completed
	if whileApplying.Revision != before.Revision || !whileApplying.AppliedAt.Equal(before.AppliedAt) {
		t.Fatal("reported generation before application finished")
	}
	if got.err != nil || got.status.Revision != before.Revision+1 || got.status.AppliedAt.Before(before.AppliedAt) {
		t.Fatal("save did not return its completed application status", got.err)
	}
}
