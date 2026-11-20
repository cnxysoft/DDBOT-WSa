package cfg

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

// Exercise the public credential writer, Config transaction and runtime snapshot
// together: successful QR completion must never mean only the text was changed.
func TestCredentialWriterAppliesExactlyOrRejectsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name, before, peerPath, peerValue string
	}{
		{"flat_shadow", "bilibili.SESSDATA: old-session\nbilibili:\n  bili_jct: old-csrf\n  interval: 30s\n", "bilibili.interval", "30s"},
		{"uppercase_flow", "BILIBILI: {SESSDATA: old-session, bili_jct: old-csrf, interval: 30s}\n", "bilibili.interval", "30s"},
		{"nested_same_leaf", "bilibili:\n  profile:\n    SESSDATA: child-session\n  SESSDATA: old-session\n  bili_jct: old-csrf\n", "bilibili.profile.sessdata", "child-session"},
		{"block_scalar_body", "bilibili:\n  note: |\n    SESSDATA: literal-prose\n  SESSDATA: old-session\n  bili_jct: old-csrf\n", "bilibili.note", "SESSDATA: literal-prose\n"},
		{"single_quoted_keys", "'bilibili':\n  'SESSDATA': old-session\n  'bili_jct': old-csrf\n  interval: 30s\n", "bilibili.interval", "30s"},
		{"crlf_quoted_flow", "# keep this comment\r\n'BILIBILI': {SESSDATA: old-session, bili_jct: old-csrf, interval: 30s} # credentials\r\n", "bilibili.interval", "30s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempConfig(t, tc.before)
			previous := config.GlobalConfig
			c := &config.Config{Viper: viper.New()}
			c.SetConfigFile(path)
			c.ConfigureReload(nil, nil)
			if err := c.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			config.GlobalConfig = c
			t.Cleanup(func() { c.StopWatching(); config.GlobalConfig = previous })
			if c.GetString("bilibili.sessdata") != "old-session" || c.GetString("bilibili.bili_jct") != "old-csrf" || c.GetString(tc.peerPath) != tc.peerValue {
				t.Fatal("fixture is not accepted as intended by the real Config parser")
			}
			const nextSession, nextCSRF = "new-session-secret", "new-csrf-secret"
			prepared, committed := 0, 0
			c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
				prepared++
				if next.GetString("bilibili.sessdata") != nextSession || next.GetString("bilibili.bili_jct") != nextCSRF {
					t.Error("prepare saw a partial or shadowed credential pair")
				}
				return func() { committed++ }, nil, nil
			})
			err := WriteConfigKeyValues(map[string]string{"bilibili.SESSDATA": nextSession, "bilibili.bili_jct": nextCSRF})
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err != nil {
				if !bytes.Equal(after, []byte(tc.before)) || c.Status().Revision != 1 || c.GetString("bilibili.sessdata") != "old-session" || c.GetString("bilibili.bili_jct") != "old-csrf" || prepared != 0 || committed != 0 {
					t.Fatalf("rejected writer mutated accepted state: revision=%d prepared=%d committed=%d disk=%q", c.Status().Revision, prepared, committed, after)
				}
				if strings.Contains(err.Error(), nextSession) || strings.Contains(err.Error(), nextCSRF) {
					t.Fatal("credential value leaked through writer error")
				}
				return // Conservative refusal is valid for syntax the writer cannot preserve safely.
			}
			if c.GetString("bilibili.sessdata") != nextSession || c.GetString("bilibili.bili_jct") != nextCSRF || c.GetString(tc.peerPath) != tc.peerValue || c.Status().Revision != 2 || prepared != 1 || committed != 1 {
				t.Fatalf("writer returned success without exact effective update: session=%q csrf=%q peer=%q revision=%d prepared=%d committed=%d disk=%q", c.GetString("bilibili.sessdata"), c.GetString("bilibili.bili_jct"), c.GetString(tc.peerPath), c.Status().Revision, prepared, committed, after)
			}
			if err := c.ReadInConfig(); err != nil || c.GetString("bilibili.sessdata") != nextSession || c.GetString("bilibili.bili_jct") != nextCSRF || c.GetString(tc.peerPath) != tc.peerValue {
				t.Fatalf("persisted credentials disagree with accepted runtime after reload: %v", err)
			}
			if strings.Contains(tc.before, "\r\n") {
				if strings.Contains(strings.ReplaceAll(string(after), "\r\n", ""), "\n") || !bytes.Contains(after, []byte("# keep this comment")) || !bytes.Contains(after, []byte("# credentials")) {
					t.Fatalf("writer lost comments or changed CRLF: %q", after)
				}
			}
		})
	}
}

func TestCredentialWriterCanceledOwnerCannotPersistPreparedPair(t *testing.T) {
	const initial = "bilibili:\n  SESSDATA: old-session\n  bili_jct: old-csrf\n"
	path := writeTempConfig(t, initial)
	previous := config.GlobalConfig
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig = c
	t.Cleanup(func() { c.StopWatching(); config.GlobalConfig = previous })
	prepared, resume := make(chan struct{}), make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	t.Cleanup(release)
	committed, aborted := 0, 0
	c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		if next.GetString("bilibili.sessdata") != "new-session" || next.GetString("bilibili.bili_jct") != "new-csrf" {
			t.Error("prepare did not receive a complete credential pair")
		}
		close(prepared)
		<-resume
		return func() { committed++ }, func() { aborted++ }, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- WriteConfigKeyValuesContext(ctx, map[string]string{"bilibili.SESSDATA": "new-session", "bilibili.bili_jct": "new-csrf"})
	}()
	select {
	case <-prepared:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not enter prepare")
	}
	cancel()
	release()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled credential owner was accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, []byte(initial)) || c.Status().Revision != 1 || c.GetString("bilibili.sessdata") != "old-session" || c.GetString("bilibili.bili_jct") != "old-csrf" || committed != 0 || aborted != 1 {
		t.Fatalf("canceled prepared credentials escaped: revision=%d committed=%d aborted=%d disk=%q", c.Status().Revision, committed, aborted, after)
	}
}

func TestCredentialWriterCanceledWhileWaitingForWriteLockDoesNothing(t *testing.T) {
	const initial = "bilibili:\n  SESSDATA: old-session\n"
	path := writeTempConfig(t, initial)
	previous := config.GlobalConfig
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig = c
	t.Cleanup(func() { config.GlobalConfig = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	configWriteMu.Lock()
	started, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		result <- WriteConfigKeyValuesContext(ctx, map[string]string{"bilibili.SESSDATA": "new-session"})
	}()
	<-started
	cancel()
	configWriteMu.Unlock()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued canceled credential owner was accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, []byte(initial)) || c.Status().Revision != 1 || c.GetString("bilibili.sessdata") != "old-session" {
		t.Fatal("canceled queued writer mutated accepted configuration")
	}
}
