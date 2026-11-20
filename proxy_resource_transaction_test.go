package DDBOT

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/telegram"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/py"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/spf13/viper"
)

type transactionProxyServer struct {
	server       *httptest.Server
	mu           sync.Mutex
	paths        []string
	failAuth     atomic.Bool
	failAll      atomic.Bool
	getMeEntered chan struct{}
	getMeRelease chan struct{}
	enteredOnce  sync.Once
}

func newTransactionProxyServer(t *testing.T) *transactionProxyServer {
	t.Helper()
	p := &transactionProxyServer{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		p.paths = append(p.paths, r.URL.Path)
		p.mu.Unlock()
		if !r.URL.IsAbs() {
			t.Errorf("request did not use HTTP proxy form: %s", r.RequestURI)
		}
		if p.failAll.Load() {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "old proxy is unavailable")
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			if p.getMeEntered != nil {
				p.enteredOnce.Do(func() { close(p.getMeEntered) })
				<-p.getMeRelease
			}
			if p.failAuth.Load() {
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprint(w, "remote-secret-token=do-not-leak")
				return
			}
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"transaction","username":"transaction_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":-100,"type":"supergroup"},"text":"ok"}}`)
		default:
			fmt.Fprint(w, "background-ok")
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *transactionProxyServer) count(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, value := range p.paths {
		if value == path {
			count++
		}
	}
	return count
}

type transactionTrackedPool struct {
	proxy_pool.IProxyPool
	stops atomic.Int32
}

func (p *transactionTrackedPool) Stop() error { p.stops.Add(1); return p.IProxyPool.Stop() }

func proxyTransactionYAML(proxyURL, token, endpoint string) []byte {
	return []byte(fmt.Sprintf("proxy:\n  type: localProxyPool\nlocalProxyPool:\n  oversea: [%q]\ntelegram:\n  enable: true\n  token: %q\n  endpoint: %q\n", proxyURL, token, endpoint))
}

func newProxyTransactionConfig(t *testing.T, oldProxy *transactionProxyServer) (*config.Config, *transactionTrackedPool) {
	t.Helper()
	telegram.Close()
	pool := &transactionTrackedPool{IProxyPool: local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{{Proxy: oldProxy.server.URL, Type: proxy_pool.PreferOversea}})}
	proxy_pool.Swap(pool)
	path := filepath.Join(t.TempDir(), "application.yaml")
	if err := os.WriteFile(path, proxyTransactionYAML(oldProxy.server.URL, "old", "http://telegram.invalid/bot%s/%s"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	oldCfg := config.GlobalConfig
	config.GlobalConfig = c
	t.Cleanup(func() { c.BeginShutdown(); telegram.Close(); proxy_pool.Swap(nil); config.GlobalConfig = oldCfg })
	disabled := &config.Config{Viper: viper.New()}
	commit, _, err := telegram.PrepareRuntimeReload(disabled.Snapshot(), c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if commit == nil {
		t.Fatal("initial Telegram generation was not prepared")
	}
	commit()
	return c, pool
}

func installProxyTransaction(c *config.Config, getter *proxy_pool.Getter, laterFailure bool) {
	c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		participants := []reloadParticipant{
			{"proxy", func(a, b *config.Snapshot) (func(), func(), error) {
				var commit, abort func()
				var err error
				*getter, commit, abort, err = lsp.PrepareProxyReloadWithGetter(a, b)
				return commit, abort, err
			}},
			{"Telegram", func(a, b *config.Snapshot) (func(), func(), error) {
				return telegram.PrepareRuntimeReloadWithProxy(a, b, *getter)
			}},
		}
		if laterFailure {
			participants = append(participants, reloadParticipant{"later-owner", func(_, _ *config.Snapshot) (func(), func(), error) {
				return nil, nil, errors.New("remote-secret-token=do-not-leak")
			}})
		}
		return prepareResources(previous, next, participants)
	})
}

func TestProxyAndTelegramCredentialsSaveUsesCandidateAndPromotesGetter(t *testing.T) {
	oldProxy, candidateProxy := newTransactionProxyServer(t), newTransactionProxyServer(t)
	c, oldPool := newProxyTransactionConfig(t, oldProxy)
	oldProxy.failAll.Store(true)
	candidateProxy.getMeEntered, candidateProxy.getMeRelease = make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(candidateProxy.getMeRelease) }) }
	defer release()
	var getter proxy_pool.Getter
	installProxyTransaction(c, &getter, false)
	next := proxyTransactionYAML(candidateProxy.server.URL, "new", "http://telegram.invalid/bot%s/%s")
	saved := make(chan error, 1)
	go func() { saved <- c.Save(next, func() error { return os.WriteFile(c.ConfigFileUsed(), next, 0600) }) }()
	select {
	case <-candidateProxy.getMeEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate credential validation did not reach new proxy")
	}
	var background string
	if err := requests.Get("http://background.invalid/during-prepare", nil, &background, requests.ProxyOption(proxy_pool.PreferOversea)); err == nil {
		t.Fatal("background request unexpectedly bypassed the unavailable active proxy")
	}
	if oldProxy.count("/during-prepare") != 1 || candidateProxy.count("/during-prepare") != 0 {
		t.Fatal("background traffic switched before persistence")
	}
	if c.GetString("telegram.token") != "old" || oldPool.stops.Load() != 0 {
		t.Fatal("preparation published candidate state")
	}
	release()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("single save did not complete")
	}
	if c.GetString("telegram.token") != "new" || oldPool.stops.Load() != 1 {
		t.Fatal("single save did not commit both owners")
	}
	telegram.SendToChat(-100, mmsg.NewText("candidate committed"))
	if candidateProxy.count("/botnew/sendMessage") != 1 {
		t.Fatal("new live client did not use committed proxy")
	}

	thirdProxy := newTransactionProxyServer(t)
	third := proxyTransactionYAML(thirdProxy.server.URL, "new", "http://telegram.invalid/bot%s/%s")
	if err := c.Save(third, func() error { return os.WriteFile(c.ConfigFileUsed(), third, 0600) }); err != nil {
		t.Fatal(err)
	}
	telegram.SendToChat(-100, mmsg.NewText("follow global proxy"))
	if thirdProxy.count("/botnew/sendMessage") != 1 || candidateProxy.count("/botnew/sendMessage") != 1 {
		t.Fatal("client remained pinned to previous candidate after proxy-only save")
	}

	var traffic sync.WaitGroup
	traffic.Add(1)
	go func() {
		defer traffic.Done()
		for i := 0; i < 24; i++ {
			telegram.SendToChat(-100, mmsg.NewText("concurrent proxy reload"))
		}
	}()
	for i := 0; i < 8; i++ {
		address := candidateProxy.server.URL
		if i%2 == 1 {
			address = thirdProxy.server.URL
		}
		update := proxyTransactionYAML(address, "new", "http://telegram.invalid/bot%s/%s")
		if err := c.Save(update, func() error { return os.WriteFile(c.ConfigFileUsed(), update, 0600) }); err != nil {
			t.Fatal(err)
		}
	}
	traffic.Wait()
	if got := candidateProxy.count("/botnew/sendMessage") + thirdProxy.count("/botnew/sendMessage"); got != 26 {
		t.Fatalf("concurrent live traffic was lost or duplicated across proxy saves: %d", got)
	}
}

func TestProxyCredentialTransactionFailuresPreserveOldAndInvalidateCandidate(t *testing.T) {
	for _, failure := range []string{"credentials", "later-owner", "persistence"} {
		t.Run(failure, func(t *testing.T) {
			oldProxy, candidateProxy := newTransactionProxyServer(t), newTransactionProxyServer(t)
			c, oldPool := newProxyTransactionConfig(t, oldProxy)
			candidateProxy.failAuth.Store(failure == "credentials")
			var getter proxy_pool.Getter
			installProxyTransaction(c, &getter, failure == "later-owner")
			next := proxyTransactionYAML(candidateProxy.server.URL, "sensitive-new-token", "http://telegram.invalid/bot%s/%s")
			persisted := false
			err := c.Save(next, func() error { persisted = true; return errors.New("disk full") })
			if err == nil {
				t.Fatal("failed transaction was accepted")
			}
			if strings.Contains(err.Error(), "sensitive-new-token") || strings.Contains(err.Error(), "remote-secret") {
				t.Fatalf("failure exposed secrets: %s", err)
			}
			if persisted != (failure == "persistence") {
				t.Fatal("validation and persistence ran out of order")
			}
			if c.Status().Revision != 1 || c.GetString("telegram.token") != "old" || oldPool.stops.Load() != 0 {
				t.Fatal("failed transaction modified old configuration or pool")
			}
			if _, err := getter(proxy_pool.PreferOversea); !errors.Is(err, proxy_pool.ErrCandidateAborted) {
				t.Fatalf("aborted getter remains usable: %v", err)
			}
			data, err := os.ReadFile(c.ConfigFileUsed())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "sensitive-new-token") {
				t.Fatal("failed transaction modified config file")
			}
			telegram.SendToChat(-100, mmsg.NewText("retain old client"))
			if oldProxy.count("/botold/sendMessage") != 1 {
				t.Fatal("failure replaced live Telegram client")
			}
		})
	}
}

func TestProxyOffCandidateUsesDirectConnectionInsteadOfOldPool(t *testing.T) {
	oldProxy := newTransactionProxyServer(t)
	c, _ := newProxyTransactionConfig(t, oldProxy)
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.IsAbs() {
			t.Error("off candidate used a proxy request")
		}
		fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"direct","username":"direct_bot"}}`)
	}))
	defer direct.Close()
	var getter proxy_pool.Getter
	installProxyTransaction(c, &getter, false)
	next := []byte(fmt.Sprintf("proxy:\n  type: off\ntelegram:\n  enable: true\n  token: direct\n  endpoint: %q\n", direct.URL+"/bot%s/%s"))
	if err := c.Save(next, func() error { return os.WriteFile(c.ConfigFileUsed(), next, 0600) }); err != nil {
		t.Fatal(err)
	}
	if oldProxy.count("/botdirect/getMe") != 0 {
		t.Fatal("off candidate fell back to old proxy")
	}
	if proxy, err := getter(proxy_pool.PreferOversea); proxy != nil || !errors.Is(err, proxy_pool.ErrNil) {
		t.Fatalf("off getter did not remain direct after commit: %v %v", proxy, err)
	}
}

func TestFullRuntimeResourceTransactionSharesCandidateProxyWithTelegram(t *testing.T) {
	oldProxy, candidateProxy := newTransactionProxyServer(t), newTransactionProxyServer(t)
	c, oldPool := newProxyTransactionConfig(t, oldProxy)
	oldProxy.failAll.Store(true)
	c.OnPrepareReload(prepareRuntimeResources)
	next := proxyTransactionYAML(candidateProxy.server.URL, "full-chain", "http://telegram.invalid/bot%s/%s")
	if err := c.Save(next, func() error { return os.WriteFile(c.ConfigFileUsed(), next, 0600) }); err != nil {
		t.Fatal(err)
	}
	if oldPool.stops.Load() != 1 || candidateProxy.count("/botfull-chain/getMe") != 1 || oldProxy.count("/botfull-chain/getMe") != 0 {
		t.Fatal("full production participant chain did not share candidate proxy")
	}
	telegram.SendToChat(-100, mmsg.NewText("production participant chain"))
	if candidateProxy.count("/botfull-chain/sendMessage") != 1 {
		t.Fatal("full production chain did not publish Telegram client")
	}
}

func TestFullRuntimeProxyCredentialSaveAcceptsPythonPoolBareAddress(t *testing.T) {
	oldProxy, candidateProxy := newTransactionProxyServer(t), newTransactionProxyServer(t)
	c, oldPool := newProxyTransactionConfig(t, oldProxy)
	oldProxy.failAll.Store(true)
	var selections atomic.Int32
	pythonPool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/get" {
			selections.Add(1)
			fmt.Fprintf(w, `{"proxy":%q}`, strings.TrimPrefix(candidateProxy.server.URL, "http://"))
			return
		}
		fmt.Fprint(w, "python proxy pool ready")
	}))
	defer pythonPool.Close()
	if pool, err := py.NewPYProxyPool(pythonPool.URL); err != nil {
		t.Fatalf("local Python proxy API preparation failed: %v", err)
	} else {
		_ = pool.Stop()
	}
	c.OnPrepareReload(prepareRuntimeResources)
	next := []byte(fmt.Sprintf("proxy:\n  type: pyProxyPool\npyProxyPool:\n  host: %q\ntelegram:\n  enable: true\n  token: python\n  endpoint: 'http://telegram.invalid/bot%%s/%%s'\n", pythonPool.URL))
	if err := c.Save(next, func() error { return os.WriteFile(c.ConfigFileUsed(), next, 0600) }); err != nil {
		t.Fatal(err)
	}
	telegram.SendToChat(-100, mmsg.NewText("python proxy pool"))
	if oldPool.stops.Load() != 1 || candidateProxy.count("/botpython/getMe") != 1 || candidateProxy.count("/botpython/sendMessage") != 1 || selections.Load() < 2 {
		t.Fatal("bare Python pool routes did not reach candidate or committed client")
	}
}
