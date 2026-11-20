package cfg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestCanceledCredentialContextDoesNotPrepareOrPersist(t *testing.T) {
	for _, cancelDuringPrepare := range []bool{false, true} {
		name := "waiting_for_writer"
		if cancelDuringPrepare {
			name = "after_prepare"
		}
		t.Run(name, func(t *testing.T) {
			const before = "bilibili:\n  SESSDATA: old\n  bili_jct: old\n"
			path := writeTempConfig(t, before)
			previous := config.GlobalConfig
			c := &config.Config{Viper: viper.New()}
			c.SetConfigFile(path)
			if err := c.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			config.GlobalConfig = c
			t.Cleanup(func() { c.StopWatching(); config.GlobalConfig = previous })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, committed, aborted := 0, 0, 0
			c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
				prepared++
				if cancelDuringPrepare {
					cancel()
				}
				return func() { committed++ }, func() { aborted++ }, nil
			})
			values := map[string]string{"bilibili.SESSDATA": "new", "bilibili.bili_jct": "new"}
			var err error
			if cancelDuringPrepare {
				err = WriteConfigKeyValuesContext(ctx, values)
			} else {
				configWriteMu.Lock()
				result := make(chan error, 1)
				go func() { result <- WriteConfigKeyValuesContext(ctx, values) }()
				cancel()
				configWriteMu.Unlock()
				select {
				case err = <-result:
				case <-time.After(5 * time.Second):
					t.Fatal("canceled writer did not return after write lock released")
				}
			}
			wantPrepared := 0
			if cancelDuringPrepare {
				wantPrepared = 1
			}
			if !errors.Is(err, context.Canceled) || prepared != wantPrepared || committed != 0 || aborted != wantPrepared ||
				c.Status().Revision != 1 || c.GetString("bilibili.SESSDATA") != "old" || readTempConfig(t, path) != before {
				t.Fatalf("canceled credential save changed accepted state: err=%v prepare=%d commit=%d abort=%d revision=%d",
					err, prepared, committed, aborted, c.Status().Revision)
			}
		})
	}
}
