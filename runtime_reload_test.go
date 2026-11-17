package DDBOT

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestResourceTransactionAbortsInReverseOrder(t *testing.T) {
	var events []string
	participant := func(name string, fail bool) reloadParticipant {
		return reloadParticipant{name, func(_, _ *config.Snapshot) (func(), func(), error) {
			events = append(events, "prepare "+name)
			if fail {
				return nil, nil, errors.New("remote token=secret")
			}
			return func() { events = append(events, "commit "+name) }, func() { events = append(events, "abort "+name) }, nil
		}}
	}
	_, _, err := prepareResources(nil, nil, []reloadParticipant{participant("A", false), participant("B", false), participant("C", true)})
	if err == nil || err.Error() == "remote token=secret" {
		t.Fatal("preparation failure was not sanitized")
	}
	want := []string{"prepare A", "prepare B", "prepare C", "abort B", "abort A"}
	if !reflect.DeepEqual(events, want) {
		t.Fatal(events)
	}
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, []byte("value: old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		return prepareResources(previous, next, []reloadParticipant{participant("A", false), participant("B", false)})
	})
	events = nil
	if err := c.Save([]byte("value: new\n"), func() error { events = append(events, "persist"); return errors.New("disk full") }); err == nil {
		t.Fatal("failed disk write accepted")
	}
	if c.GetString("value") != "old" || c.Status().Revision != 1 {
		t.Fatal("failed save changed generation")
	}
	if !reflect.DeepEqual(events, []string{"prepare A", "prepare B", "persist", "abort B", "abort A"}) {
		t.Fatal(events)
	}
}
