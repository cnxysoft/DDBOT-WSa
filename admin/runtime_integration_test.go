package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter/qqofficial"
	"github.com/spf13/viper"
)

func TestRealHTTPSaveDisableAndReenable(t *testing.T) {
	old := config.GlobalConfig
	defer func() { config.GlobalConfig = old }()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := ln.Addr().String()
	path := filepath.Join(t.TempDir(), "app.yaml")
	initial := []byte("admin:\n  enable: true\n  token: old\n  addr: " + addr + "\n")
	if e = os.WriteFile(path, initial, 0600); e != nil {
		t.Fatal(e)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if e = c.ReadInConfig(); e != nil {
		t.Fatal(e)
	}
	config.GlobalConfig = c
	s := newServer(nil)
	s.addr = addr
	s.serve(ln)
	defer func() {
		s.runtimeMu.Lock()
		h := s.httpServer
		s.runtimeMu.Unlock()
		if h != nil {
			_ = h.Close()
		}
	}()
	c.OnPrepareReload(s.prepareRuntimeReload)
	req, e := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/config", bytes.NewBufferString(`{"config":{"admin":{"enable":false}}}`))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer old")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Second}
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal("save response lost on disable", e)
	}
	body, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil {
		t.Fatal("truncated save response", e)
	}
	var result struct {
		Status string
		Reload struct{ Revision int }
	}
	if e = json.Unmarshal(body, &result); e != nil || resp.StatusCode != 200 || result.Status != "success" || result.Reload.Revision != 2 {
		t.Fatal(resp.StatusCode, string(body), e)
	}
	limit := time.Now().Add(time.Second)
	for {
		conn, e := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if e != nil {
			break
		}
		conn.Close()
		if time.Now().After(limit) {
			t.Fatal("disabled listener alive")
		}
		time.Sleep(time.Millisecond)
	}
	enabled := []byte("admin:\n  enable: true\n  token: new\n  addr: " + addr + "\n")
	if e = c.Save(enabled, func() error { return os.WriteFile(path, enabled, 0600) }); e != nil {
		t.Fatal("off->on failed", e)
	}
	req, _ = http.NewRequest(http.MethodGet, "http://"+addr+"/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer old")
	resp, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("old token authorized", resp.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer new")
	resp, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("new token rejected", resp.StatusCode)
	}
}

func TestLoginFinishUsesOneAcceptedCredentialPair(t *testing.T) {
	previousGlobal := config.GlobalConfig
	defer func() { config.GlobalConfig = previousGlobal }()
	for _, platform := range []string{loginPlatformBilibili, loginPlatformQQOfficial} {
		for _, rejected := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/rejected=%v", platform, rejected), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "app.yaml")
				initial := []byte("adapter:\n  mode: onebot-v11\nbilibili:\n  SESSDATA: old-session\n  bili_jct: old-csrf\nqqofficial:\n  app-id: old-app\n  app-secret: old-secret\n")
				if err := os.WriteFile(path, initial, 0600); err != nil {
					t.Fatal(err)
				}
				c := &config.Config{Viper: viper.New()}
				c.SetConfigFile(path)
				if err := c.ReadInConfig(); err != nil {
					t.Fatal(err)
				}
				config.GlobalConfig = c
				key1, key2 := "bilibili.SESSDATA", "bilibili.bili_jct"
				value1, value2 := "new-session", "new-csrf"
				if platform == loginPlatformQQOfficial {
					key1, key2 = "qqofficial.app-id", "qqofficial.app-secret"
					value1, value2 = "new-app", "new-secret"
				}
				prepared, committed, aborted := 0, 0, 0
				c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
					prepared++
					if next.GetString(key1) != value1 || next.GetString(key2) != value2 {
						t.Fatalf("partial pair at prepare: %q %q", next.GetString(key1), next.GetString(key2))
					}
					commit := func() { committed++ }
					abort := func() { aborted++ }
					if rejected {
						return commit, abort, fmt.Errorf("remote https://secret-user:secret-password@example.invalid failed")
					}
					return commit, abort, nil
				})
				s := newServer(nil)
				session := s.loginManagerOf().start(platform)
				if platform == loginPlatformBilibili {
					s.finishBilibiliLogin(session, nil, "https://example.invalid/?SESSDATA=new-session&bili_jct=new-csrf")
				} else {
					s.finishQQOfficialLogin(session, &qqofficial.QRBindResult{AppID: value1, Secret: value2})
				}
				state, message, _ := session.snapshot()
				if strings.Contains(message, "secret-password") || strings.Contains(message, "secret-user") {
					t.Fatal("login error leaked remote details", message)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if prepared != 1 {
					t.Fatal("credential pair not prepared exactly once", prepared)
				}
				if rejected {
					if state != loginStateFailed || committed != 0 || aborted != 1 || !bytes.Equal(data, initial) || c.Status().Revision != 1 || c.GetString(key1) == value1 || c.GetString(key2) == value2 {
						t.Fatalf("rejected pair changed state: state=%s committed=%d aborted=%d revision=%d", state, committed, aborted, c.Status().Revision)
					}
				} else if state != loginStateConfirmed || committed != 1 || aborted != 0 || c.Status().Revision != 2 || c.GetString(key1) != value1 || c.GetString(key2) != value2 {
					t.Fatalf("accepted pair not fully applied: state=%s committed=%d aborted=%d revision=%d", state, committed, aborted, c.Status().Revision)
				}
			})
		}
	}
}

func TestRealSSETokenRotationClosesAuthorizedStreams(t *testing.T) {
	previousGlobal := config.GlobalConfig
	defer func() { config.GlobalConfig = previousGlobal }()
	for _, route := range []string{"/api/v1/logs/stream", "/api/v1/pushes/stream"} {
		t.Run(route, func(t *testing.T) {
			v := viper.New()
			v.Set("admin.token", "old")
			config.GlobalConfig = &config.Config{Viper: v}
			s := newServer(nil)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			s.addr = ln.Addr().String()
			s.serve(ln)
			defer s.httpServer.Close()
			client := &http.Client{Timeout: 2 * time.Second}
			req, _ := http.NewRequest(http.MethodGet, "http://"+s.addr+route, nil)
			req.Header.Set("Authorization", "Bearer old")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatal("stream not established", resp.StatusCode)
			}
			defer resp.Body.Close()
			done := make(chan error, 1)
			go func() { _, err := io.Copy(io.Discard, resp.Body); done <- err }()
			commit, _, err := s.prepareRuntimeReload(adminSnapshot(true, s.addr, "old"), adminSnapshot(true, s.addr, "new"))
			if err != nil {
				t.Fatal(err)
			}
			commit()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("stream did not end cleanly", err)
				}
			case <-time.After(time.Second):
				t.Fatal("old authorized SSE continued after token rotation")
			}
			req, _ = http.NewRequest(http.MethodGet, "http://"+s.addr+"/api/v1/health", nil)
			req.Header.Set("Authorization", "Bearer old")
			resp, err = client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatal("old token accepted after rotation", resp.StatusCode)
			}
			req.Header.Set("Authorization", "Bearer new")
			resp, err = client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatal("new token rejected", resp.StatusCode)
			}
		})
	}
}

func TestRealHTTPListenerMigrationRetainsLoginSession(t *testing.T) {
	previousGlobal := config.GlobalConfig
	defer func() { config.GlobalConfig = previousGlobal }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	oldAddr := ln.Addr().String()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	newAddr := reservation.Addr().String()
	reservation.Close()
	path := filepath.Join(t.TempDir(), "app.yaml")
	initial := []byte("admin:\n  enable: true\n  token: old\n  addr: " + oldAddr + "\n")
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig = c
	s := newServer(nil)
	s.addr = oldAddr
	s.serve(ln)
	defer func() {
		s.runtimeMu.Lock()
		h := s.httpServer
		s.runtimeMu.Unlock()
		if h != nil {
			h.Close()
		}
	}()
	c.OnPrepareReload(s.prepareRuntimeReload)
	sess := s.loginManagerOf().start(loginPlatformQQOfficial)
	sess.setState(loginStateScanned, "pending confirmation")
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodPost, "http://"+oldAddr+"/api/v1/config", strings.NewReader(`{"config":{"admin":{"addr":"`+newAddr+`"}}}`))
	req.Header.Set("Authorization", "Bearer old")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("migration save response lost", err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"status":"success"`)) {
		t.Fatal("migration save failed", resp.StatusCode, string(data), err)
	}
	req, _ = http.NewRequest(http.MethodGet, "http://"+newAddr+"/api/v1/login/qrcode/status?session="+sess.id, nil)
	req.Header.Set("Authorization", "Bearer old")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal("new listener inaccessible", err)
	}
	data, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"state":"scanned"`)) || s.loginManagerOf().get(sess.id) != sess {
		t.Fatal("pending QR session lost across listener migration", resp.StatusCode, string(data), err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.DialTimeout("tcp", oldAddr, 50*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("retired listener still accepts requests")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStopDrainsRequestsOnRetiredListeners(t *testing.T) {
	activeServerMu.Lock()
	previousActive := activeServer
	activeServerMu.Unlock()
	defer func() { activeServerMu.Lock(); activeServer = previousActive; activeServerMu.Unlock() }()
	entered, release := make(chan struct{}), make(chan struct{})
	s := &Server{token: "token", handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "finished")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.addr = ln.Addr().String()
	s.serve(ln)
	activeServerMu.Lock()
	activeServer = s
	activeServerMu.Unlock()
	client := &http.Client{Timeout: 2 * time.Second}
	requestDone := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + ln.Addr().String() + "/blocked")
		if err != nil {
			requestDone <- err
			return
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err == nil && string(body) != "finished" {
			err = fmt.Errorf("retired response truncated: %q", body)
		}
		requestDone <- err
	}()
	<-entered
	commit, _, err := s.prepareRuntimeReload(adminSnapshot(true, s.addr, "token"), adminSnapshot(true, "127.0.0.1:0", "token"))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	stopped := make(chan struct{})
	go func() { Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a retired listener still owned an active request")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatal("retired request did not finish", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after retired requests drained")
	}
}
