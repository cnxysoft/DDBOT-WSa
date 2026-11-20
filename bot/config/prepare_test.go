package config

import (
	"errors"
	"os"
	"testing"
)

func TestPrepareReloadTransaction(t *testing.T) {
	c, path := testConfig(t, "value: old\n")
	var events []string
	active := "old"
	c.OnPrepareReload(func(previous, next *Snapshot) (func(), func(), error) {
		if previous.GetString("value") != active {
			t.Fatal("prepare observed inconsistent live generation")
		}
		events = append(events, "prepare")
		if next.GetString("value") == "rejected" {
			return nil, func() { events = append(events, "abort") }, errors.New("resource rejected")
		}
		return func() {
			if c.GetString("value") != next.GetString("value") {
				t.Fatal("commit ran before generation publication")
			}
			active = next.GetString("value")
			events = append(events, "commit")
		}, func() { events = append(events, "abort") }, nil
	})
	initial := c.Status().Revision
	called := false
	if err := c.Save([]byte("value: rejected\n"), func() error { called = true; return nil }); err == nil || called {
		t.Fatal("resource preparation failure persisted candidate")
	}
	if err := c.Save([]byte("value: new\n"), func() error { return errors.New("disk full") }); err == nil {
		t.Fatal("persistence failure accepted")
	}
	if active != "old" || c.GetString("value") != "old" || c.Status().Revision != initial {
		t.Fatal("failed transaction changed live generation")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "value: old\n" {
		t.Fatal("failed transaction changed source")
	}
	if err := c.Save([]byte("value: new\n"), func() error {
		events = append(events, "persist")
		return os.WriteFile(path, []byte("value: new\n"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	if active != "new" || c.Status().Revision != initial+1 {
		t.Fatal("successful transaction did not publish")
	}
	count := len(events)
	if err := c.Reload(); err != nil || len(events) != count {
		t.Fatal("unchanged bytes repeated resource preparation")
	}
	want := []string{"prepare", "abort", "prepare", "abort", "prepare", "persist", "commit"}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v", events)
		}
	}
	writeConfig(t, path, "value: rejected\n")
	if c.Reload() == nil || active != "new" || c.GetString("value") != "new" || c.Status().LastError == "" {
		t.Fatal("external resource failure did not retain last good generation")
	}
}
