package xhs

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestRuntimeCookieGenerationOwnsInputAndClears(t *testing.T) {
	input := map[string]string{"a1": "old-a1", "web_session": "old-session"}
	client := NewClient(input)
	input["a1"] = "mutated"
	if client.cookieSnapshot()["a1"] != "old-a1" {
		t.Fatal("caller mutated published cookie generation")
	}
	old := client.cookieSnapshot()
	client.SetCookies(map[string]string{"a1": "new-a1"})
	if old["a1"] != "old-a1" || client.cookieSnapshot()["a1"] != "new-a1" {
		t.Fatal("replacement modified old in-flight generation")
	}
	client.SetCookies(nil)
	if len(client.cookieSnapshot()) != 0 {
		t.Fatal("clear retained previous cookies")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				client.SetCookies(map[string]string{"a1": "concurrent"})
				_ = client.cookieSnapshot()["a1"]
			}
		}()
	}
	wg.Wait()
}

func TestRuntimeCookieMapRetainsCustomNamesAndValidatesTypes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(p, []byte("xhs:\n  cookies:\n    a1: test-a1\n    webId: id-value\n    CustomCase: custom-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Viper: viper.New()}
	cfg.SetConfigFile(p)
	if err := cfg.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	cookies := cookiesFromSnapshot(cfg.Snapshot())
	if cookies["webId"] != "id-value" || cookies["CustomCase"] != "custom-value" {
		t.Fatalf("cookie name case was lost: %#v", cookies)
	}
	cfg.Set("xhs.cookies", map[string]interface{}{"a1": true})
	if _, _, err := PrepareRuntimeReload((&config.Config{Viper: viper.New()}).Snapshot(), cfg.Snapshot()); err == nil {
		t.Fatal("non-string cookie silently accepted")
	}
}
