package twitch

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func twitchReloadSnapshot(id, secret string) *config.Snapshot {
	v := viper.New()
	v.Set("twitch.clientId", id)
	v.Set("twitch.clientSecret", secret)
	return (&config.Config{Viper: v}).Snapshot()
}

func TestTwitchRuntimeReloadDiscardsStaleTokenFetch(t *testing.T) {
	store := &appToken{clientId: "old-id", clientSecret: "old-secret"}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan apiAuth, 1)
	go func() {
		auth, err := store.auth(func(id, secret string) (tokenResponse, error) {
			require.Equal(t, "old-id", id)
			require.Equal(t, "old-secret", secret)
			close(entered)
			<-release
			return tokenResponse{AccessToken: "stale-token", ExpiresIn: 7200}, nil
		})
		if err == nil {
			result <- auth
		}
	}()
	<-entered
	store.install(&appToken{clientId: "new-id", clientSecret: "new-secret", accessToken: "new-token", expiresIn: 7200, fetchedAt: time.Now()})
	close(release)
	select {
	case auth := <-result:
		require.Equal(t, "new-token", auth.token)
		require.Equal(t, "new-id", auth.clientID)
	case <-time.After(time.Second):
		t.Fatal("old token request deadlocked or failed to retry new generation")
	}
	store.mu.Lock()
	require.Equal(t, "new-token", store.accessToken)
	store.mu.Unlock()
}

func TestTwitchRuntimeReloadCommitAbortAndOldUnauthorized(t *testing.T) {
	defer tokenStore.install(&appToken{})
	old := twitchReloadSnapshot("old-id", "old-secret")
	for _, abort := range []bool{false, true} {
		t.Run(fmt.Sprint(abort), func(t *testing.T) {
			tokenStore.install(&appToken{clientId: "old-id", clientSecret: "old-secret", accessToken: "old-token", expiresIn: 7200, fetchedAt: time.Now()})
			oldAuth, err := getAPIAuth()
			require.NoError(t, err)
			commit, cancel, err := prepareRuntimeReload(old, twitchReloadSnapshot("new-id", "new-secret"), func(id, secret string) (tokenResponse, error) {
				require.Equal(t, "new-id", id)
				require.Equal(t, "new-secret", secret)
				return tokenResponse{AccessToken: "new-token", ExpiresIn: 7200}, nil
			})
			require.NoError(t, err)
			before, err := getAPIAuth()
			require.NoError(t, err)
			require.Equal(t, "old-token", before.token)
			if abort {
				cancel()
				commit()
				after, err := getAPIAuth()
				require.NoError(t, err)
				require.Equal(t, "old-token", after.token)
			} else {
				commit()
				cancel()
				invalidateToken(oldAuth)
				after, err := getAPIAuth()
				require.NoError(t, err)
				require.Equal(t, "new-token", after.token)
				require.Equal(t, "new-id", after.clientID)
			}
		})
	}
	_, _, err := prepareRuntimeReload(old, twitchReloadSnapshot("partial", ""), requestAppToken)
	require.Error(t, err)
	_, _, err = prepareRuntimeReload(old, twitchReloadSnapshot("bad", "secret"), func(string, string) (tokenResponse, error) {
		return tokenResponse{}, errors.New("secret should stay private")
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret should")
	commit, _, err := prepareRuntimeReload(old, twitchReloadSnapshot("", ""), requestAppToken)
	require.NoError(t, err)
	commit()
	require.False(t, tokenStore.configured())
}

func TestTwitchRuntimeReloadConcurrentTokenReaders(t *testing.T) {
	store := &appToken{}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				auth, err := store.auth(func(id, secret string) (tokenResponse, error) {
					return tokenResponse{AccessToken: id + "-token", ExpiresIn: 7200}, nil
				})
				if err == nil && auth.token != auth.clientID+"-token" {
					t.Errorf("mixed token generation")
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		id := fmt.Sprint(i)
		store.install(&appToken{clientId: id, clientSecret: "secret", accessToken: id + "-token", expiresIn: 7200, fetchedAt: time.Now()})
	}
	wg.Wait()
}
