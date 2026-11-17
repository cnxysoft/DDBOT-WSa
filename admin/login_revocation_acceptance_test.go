package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter/qqofficial"
)

func TestAdminTokenRotationAbortsPreparedQRCredentials(t *testing.T) {
	for _, platform := range []string{loginPlatformBilibili, loginPlatformQQOfficial} {
		t.Run(platform, func(t *testing.T) {
			const initial = "bilibili:\n  SESSDATA: old-session\n  bili_jct: old-csrf\nqqofficial:\n  app-id: old-app\n  app-secret: old-secret\n"
			path := withConfigFile(t, initial)
			c := config.GlobalConfig
			if err := c.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			s := &Server{token: "old-admin"}
			sess := s.loginManagerOf().start(platform)
			prepared, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var resumeOnce sync.Once
			release := func() { resumeOnce.Do(func() { close(resume) }) }
			t.Cleanup(release)
			committed, aborted := 0, 0
			c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
				close(prepared)
				<-resume
				return func() { committed++ }, func() { aborted++ }, nil
			})
			go func() {
				defer close(finished)
				if platform == loginPlatformBilibili {
					s.finishBilibiliLogin(sess, nil, "https://example.invalid/?SESSDATA=new-session&bili_jct=new-csrf")
				} else {
					s.finishQQOfficialLogin(sess, &qqofficial.QRBindResult{AppID: "new-app", Secret: "new-secret"})
				}
			}()
			select {
			case <-prepared:
			case <-time.After(3 * time.Second):
				t.Fatal("QR credential transaction did not reach prepare")
			}
			s.setToken("new-admin")
			select {
			case <-sess.cancel:
			default:
				t.Error("token rotation left the old QR session active")
			}
			if s.loginManagerOf().get(sess.id) != nil {
				t.Error("revoked QR session remains exposed through status API")
			}
			release()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("revoked QR transaction did not drain")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			state, _, _ := sess.snapshot()
			if !bytes.Equal(after, []byte(initial)) || c.Status().Revision != 1 || committed != 0 || aborted != 1 || state == loginStateConfirmed {
				t.Fatalf("revoked QR credentials were published: revision=%d committed=%d aborted=%d state=%s disk=%q", c.Status().Revision, committed, aborted, state, after)
			}
		})
	}
}

func TestAuthenticatedQRCreationCannotOutliveItsTokenGeneration(t *testing.T) {
	s := &Server{token: "old-admin"}
	entered, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var created *loginSession
	handler := s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-resume
		created = s.startLoginForRequest(r, loginPlatformBilibili)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login/qrcode", nil)
	req.Header.Set("Authorization", "Bearer old-admin")
	go func() {
		defer close(finished)
		handler(httptest.NewRecorder(), req)
	}()
	<-entered
	s.setToken("new-admin")
	close(resume)
	<-finished
	if created != nil {
		t.Fatal("an authenticated request created a QR session after its token was revoked")
	}
}

func TestAdminStopRevokesQRWorkAndPreviouslyAuthorizedCreation(t *testing.T) {
	s := &Server{token: "old-admin"}
	sess := s.loginManagerOf().start(loginPlatformBilibili)
	activeServerMu.Lock()
	previous := activeServer
	activeServer = s
	activeServerMu.Unlock()
	t.Cleanup(func() {
		activeServerMu.Lock()
		activeServer = previous
		activeServerMu.Unlock()
	})
	entered, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var created *loginSession
	handler := s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-resume
		created = s.startLoginForRequest(r, loginPlatformBilibili)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login/qrcode", nil)
	req.Header.Set("Authorization", "Bearer old-admin")
	go func() {
		defer close(finished)
		handler(httptest.NewRecorder(), req)
	}()
	<-entered
	Stop()
	close(resume)
	<-finished
	select {
	case <-sess.cancel:
	default:
		t.Error("Stop left preexisting QR work active")
	}
	if s.loginManagerOf().get(sess.id) != nil || created != nil {
		t.Fatal("Stop retained a QR session or accepted a late authorized creation")
	}
}

func TestAdminStopRejectsNewAuthenticatedQRWorkWhileListenerDrains(t *testing.T) {
	s := &Server{token: "old-admin"}
	revoked := s.streamRevocation()
	activeServerMu.Lock()
	previous := activeServer
	activeServer = s
	activeServerMu.Unlock()
	t.Cleanup(func() {
		activeServerMu.Lock()
		activeServer = previous
		activeServerMu.Unlock()
	})
	// Freeze Stop between revoking existing sessions and draining the listener;
	// an HTTP handler already arriving at that listener must not spawn new work.
	s.runtimeMu.Lock()
	stopped := make(chan struct{})
	go func() { Stop(); close(stopped) }()
	select {
	case <-revoked:
	case <-time.After(3 * time.Second):
		s.runtimeMu.Unlock()
		t.Fatal("Stop did not revoke the existing authentication generation")
	}
	var created *loginSession
	handler := s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		created = s.startLoginForRequest(r, loginPlatformBilibili)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login/qrcode", nil)
	req.Header.Set("Authorization", "Bearer old-admin")
	handler(httptest.NewRecorder(), req)
	s.runtimeMu.Unlock()
	<-stopped
	if created != nil {
		defer created.stop()
		t.Fatal("Stop admitted new authenticated QR work after its revoke pass")
	}
}
