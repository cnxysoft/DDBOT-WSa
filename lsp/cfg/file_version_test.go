package cfg

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigVersionRejectsChangedDeletedAndNewFiles(t *testing.T) {
	for _, action := range []string{"change", "delete", "create", "create_empty"} {
		t.Run(action, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "application.yaml")
			if action == "change" || action == "delete" {
				if err := os.WriteFile(path, []byte("old: secret\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			version, err := ReadConfigFileVersion(path)
			if err != nil {
				t.Fatal(err)
			}
			var external []byte
			if action == "delete" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				external = []byte("external: credential\n")
				if action == "create_empty" {
					external = []byte{}
				}
				if err := os.WriteFile(path, external, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := version.Persist([]byte("candidate: secret\n"), 0600, true); !errors.Is(err, ErrConfigConflict) ||
				strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "credential") || strings.Contains(err.Error(), path) {
				t.Fatalf("conflict not safely reported: %v", err)
			}
			after, err := os.ReadFile(path)
			if action == "delete" {
				if !os.IsNotExist(err) {
					t.Fatal("deleted source was recreated", err)
				}
			} else if err != nil || !bytes.Equal(after, external) {
				t.Fatal("external source overwritten", err)
			}
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Fatal("conflict created a backup of a stale source", err)
			}
		})
	}
}

func TestConfigVersionBacksUpOnlyMatchedSourceAndPreservesExistingBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.yaml")
	const initial, next, externalBackup = "# exact original\r\nvalue: old\r\n", "value: new\n", "external backup\x00"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	version, err := ReadConfigFileVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	copyOfSource := version.Bytes()
	copyOfSource[0] = '!'
	if err := version.Persist([]byte(next), 0600, true); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || string(backup) != initial {
		t.Fatal("backup differs from matched original", err)
	}
	if err := os.WriteFile(path+".bak", []byte(externalBackup), 0600); err != nil {
		t.Fatal(err)
	}
	version, err = ReadConfigFileVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := version.Persist([]byte("value: final\n"), 0600, true); err != nil {
		t.Fatal(err)
	}
	backup, err = os.ReadFile(path + ".bak")
	if err != nil || string(backup) != externalBackup {
		t.Fatal("existing backup overwritten", err)
	}
	leftovers, err := filepath.Glob(path + ".tmp-*")
	if err != nil || len(leftovers) != 0 {
		t.Fatal("unique staging file left behind", leftovers, err)
	}
}

func TestConfigVersionConditionalTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.yaml")
	absent, err := ReadConfigFileVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	if absent.Exists() || absent.ETag() != "" || absent.MatchesETag("*") {
		t.Fatal("absent source matched an existence condition")
	}
	if err := absent.Persist([]byte{}, 0600, true); err != nil {
		t.Fatal(err)
	}
	present, err := ReadConfigFileVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	tag := present.ETag()
	for _, condition := range []string{tag, "*", `"different", ` + tag, " \t" + tag + "\t "} {
		if !present.MatchesETag(condition) {
			t.Fatalf("matching strong condition rejected: %q", condition)
		}
	}
	for _, condition := range []string{"", " \t ", "W/" + tag, `"different"`, `W/"different", W/` + tag} {
		if present.MatchesETag(condition) {
			t.Fatalf("nonmatching condition accepted: %q", condition)
		}
	}
}
