package config

import (
	"os"
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

func TestSnapshotSectionIncludesEveryRuntimeOverride(t *testing.T) {
	c := &Config{Viper: viper.New()}
	c.Set("telegram.enable", true)
	c.Set("telegram.token", "first")
	c.Set("telegram.endpoint", "http://example.test/bot%s/%s")
	first := c.Snapshot()
	want := map[string]interface{}{
		"enable": true, "token": "first", "endpoint": "http://example.test/bot%s/%s",
	}
	if got := first.Get("telegram"); !reflect.DeepEqual(got, want) {
		t.Fatalf("section getter lost runtime settings: got %#v want %#v", got, want)
	}
	c.Set("telegram.token", "second")
	second := c.Snapshot()
	if first.GetString("telegram.token") != "first" || second.GetString("telegram.token") != "second" {
		t.Fatal("runtime setter mutated an existing snapshot")
	}
	if reflect.DeepEqual(first.Get("telegram"), second.Get("telegram")) {
		t.Fatal("section comparison missed a changed runtime token")
	}
}

func TestReloadKeepsCompleteSectionsWithRuntimeOverrides(t *testing.T) {
	c, path := testConfig(t, "telegram:\n  enable: true\n  token: first\n  endpoint: original\n")
	c.Set("telegram.endpoint", "runtime")
	called := false
	c.OnPrepareReload(func(previous, next *Snapshot) (func(), func(), error) {
		called = true
		section, ok := next.Get("telegram").(map[string]interface{})
		if !ok || section["enable"] != true || section["token"] != "second" || section["endpoint"] != "runtime" {
			t.Fatalf("candidate section lost siblings when applying overrides: %#v", section)
		}
		return nil, nil, nil
	})
	updated := []byte("telegram:\n  enable: true\n  token: second\n  endpoint: original\n")
	if err := c.Save(updated, func() error { return os.WriteFile(path, updated, 0600) }); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("changed candidate did not prepare")
	}
	section := c.Snapshot().Get("telegram").(map[string]interface{})
	if section["token"] != "second" || section["endpoint"] != "runtime" || section["enable"] != true {
		t.Fatal("published section disagrees with prepared section", section)
	}
}
