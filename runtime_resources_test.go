package DDBOT

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/spf13/viper"
)

func TestResourceDBCandidateReleasedOnLaterFailureAndDiskFailure(t *testing.T) {
	if template.GetTemplateDB() != nil {
		t.Fatal("test requires no active external template DB")
	}
	if err := template.InitTemplateDB(":memory:"); err != nil {
		t.Fatal(err)
	}
	defer template.CloseTemplateDB()
	if result := template.Set("independent-retained", "old-data"); result != "" {
		t.Fatal(result)
	}
	path := filepath.Join(t.TempDir(), "application.yaml")
	initial := []byte("extDb:\n  enable: true\n  path: ':memory:'\n")
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	for _, diskFailure := range []bool{false, true} {
		c := &config.Config{Viper: viper.New()}
		c.SetConfigFile(path)
		if err := c.ReadInConfig(); err != nil {
			t.Fatal(err)
		}
		c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
			participants := []reloadParticipant{{"actual external DB", template.PrepareRuntimeReload}}
			if !diskFailure {
				participants = append(participants, reloadParticipant{"later failure", func(_, _ *config.Snapshot) (func(), func(), error) { return nil, nil, errors.New("late rejection") }})
			}
			return prepareResources(previous, next, participants)
		})
		candidatePath := filepath.Join(t.TempDir(), "candidate.db")
		next := []byte(fmt.Sprintf("extDb:\n  enable: true\n  path: %q\n", candidatePath))
		persisted := false
		if err := c.Save(next, func() error { persisted = true; return errors.New("disk full") }); err == nil {
			t.Fatal("failed transaction succeeded")
		}
		if persisted != diskFailure {
			t.Fatal("preparation/persistence ordering wrong")
		}
		if c.Status().Revision != 1 || c.GetString("extDb.path") != ":memory:" {
			t.Fatal("rejected transaction published config")
		}
		if got := template.Get("independent-retained"); got != "old-data" {
			t.Fatalf("old active DB lost after rollback: %s", got)
		}
		reopened, err := localdb.NewDB(candidatePath)
		if err != nil {
			t.Fatalf("aborted candidate retained file lock: %v", err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
