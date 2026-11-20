package DDBOT

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestResourceTransactionRetainsCommittedStateAcrossFailures(t *testing.T) {
	for _, failure := range []string{"first", "middle", "last", "persist", "none"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.yaml")
			old := []byte("resource: old\n")
			_ = os.WriteFile(path, old, 0600)
			c := &config.Config{Viper: viper.New()}
			c.SetConfigFile(path)
			if e := c.ReadInConfig(); e != nil {
				t.Fatal(e)
			}
			var trace []string
			live := []string{"old", "old", "old"}
			names := []string{"first", "middle", "last"}
			var owners []reloadParticipant
			for index, name := range names {
				i, n := index, name
				owners = append(owners, reloadParticipant{n, func(prev, next *config.Snapshot) (func(), func(), error) {
					trace = append(trace, "prepare:"+n)
					abort := func() { trace = append(trace, "abort:"+n) }
					if n == failure {
						return nil, abort, errors.New("https://private:secret@example.invalid/?token=SECRET")
					}
					return func() { live[i] = next.GetString("resource"); trace = append(trace, "commit:"+n) }, abort, nil
				}})
			}
			c.OnPrepareReload(func(p, n *config.Snapshot) (func(), func(), error) { return prepareResources(p, n, owners) })
			data := []byte("resource: new\n")
			err := c.Save(data, func() error {
				trace = append(trace, "persist")
				if failure == "persist" {
					return errors.New("write unavailable")
				}
				return os.WriteFile(path, data, 0600)
			})
			disk, _ := os.ReadFile(path)
			if failure == "none" {
				if err != nil || string(disk) != string(data) || c.GetString("resource") != "new" || c.Status().Revision != 2 || !reflect.DeepEqual(live, []string{"new", "new", "new"}) {
					t.Fatal(err, string(disk), live)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("failed resource accepted or secret exposed", err)
			}
			if string(disk) != string(old) || c.GetString("resource") != "old" || c.Status().Revision != 1 || !reflect.DeepEqual(live, []string{"old", "old", "old"}) {
				t.Fatal("failure changed committed state", string(disk), live)
			}
			var want []string
			limit := len(names) - 1
			for i, n := range names {
				want = append(want, "prepare:"+n)
				if n == failure {
					limit = i
					break
				}
			}
			if failure == "persist" {
				want = append(want, "persist")
			}
			for i := limit; i >= 0; i-- {
				want = append(want, "abort:"+names[i])
			}
			if !reflect.DeepEqual(trace, want) {
				t.Fatal(trace, want)
			}
		})
	}
}
