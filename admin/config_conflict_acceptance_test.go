package admin

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
)

func TestAdminWritersRejectExternalEditsDuringResourcePrepare(t *testing.T) {
	for _, route := range []string{"config", "setup"} {
		for _, externalAction := range []string{"replace", "delete"} {
			t.Run(route+"/"+externalAction, func(t *testing.T) {
				const initial = "bot:\n  commandPrefix: /\nadmin:\n  token: ''\n"
				const external = "bot:\n  commandPrefix: external\nbilibili:\n  SESSDATA: external-secret\n"
				path := withConfigFile(t, initial)
				c := config.GlobalConfig
				if err := c.ReadInConfig(); err != nil {
					t.Fatal(err)
				}
				prepared, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(resume) }) }
				t.Cleanup(release)
				committed, aborted := 0, 0
				c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
					close(prepared)
					<-resume
					return func() { committed++ }, func() { aborted++ }, nil
				})
				s := &Server{}
				body := `{"config":{"bot":{"commandPrefix":"web"}}}`
				handler := s.handleConfig
				if route == "setup" {
					body = `{"token":"new-admin-secret"}`
					handler = s.handleSetup
				}
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+route, strings.NewReader(body))
				go func() { defer close(finished); handler(rec, req) }()
				select {
				case <-prepared:
				case <-time.After(3 * time.Second):
					t.Fatal("writer did not enter resource prepare")
				}
				if externalAction == "delete" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(external), 0600); err != nil {
					t.Fatal(err)
				}
				release()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Fatal("conflicting writer did not return")
				}
				after, readErr := os.ReadFile(path)
				filePreserved := externalAction == "delete" && os.IsNotExist(readErr) || externalAction == "replace" && readErr == nil && bytes.Equal(after, []byte(external))
				if rec.Code != http.StatusConflict || !filePreserved || c.Status().Revision != 1 || c.GetString("bot.commandPrefix") != "/" || c.GetString("admin.token") != "" || s.getToken() != "" || committed != 0 || aborted != 1 {
					t.Fatalf("external edit was overwritten or published: status=%d revision=%d commit=%d abort=%d readErr=%v disk=%q body=%s", rec.Code, c.Status().Revision, committed, aborted, readErr, after, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "external-secret") || strings.Contains(rec.Body.String(), "new-admin-secret") {
					t.Fatal("conflict response disclosed credentials")
				}
				// A conflict must not permanently poison later external reconciliation.
				c.OnPrepareReload(nil)
				if err := os.WriteFile(path, []byte(external), 0600); err != nil {
					t.Fatal(err)
				}
				if err := c.Reload(); err != nil || c.GetString("bot.commandPrefix") != "external" || c.Status().Revision != 2 {
					t.Fatalf("external configuration could not be accepted after conflict: %v", err)
				}
			})
		}
	}
}

func TestConfigETagRejectsAStalePageAndAdvancesAfterAcceptedSave(t *testing.T) {
	const initial = "bot:\n  commandPrefix: /\nbilibili:\n  SESSDATA: private-source-secret\n"
	path := withConfigFile(t, initial)
	c := config.GlobalConfig
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	s := &Server{token: "admin-token"}
	get := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	s.withAuth(s.handleConfig)(get, req)
	wantETag := fmt.Sprintf("\"%x\"", sha256.Sum256([]byte(initial)))
	if get.Code != http.StatusOK || get.Header().Get("ETag") != wantETag || strings.Contains(get.Body.String(), "private-source-secret") {
		t.Fatalf("GET did not return an exact source version with masked credentials: status=%d headers=%v body=%s", get.Code, get.Header(), get.Body.String())
	}
	post := func(etag, prefix string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{"config":{"bot":{"commandPrefix":"`+prefix+`"}}}`))
		req.Header.Set("Authorization", "Bearer admin-token")
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		rec := httptest.NewRecorder()
		s.withAuth(s.handleConfig)(rec, req)
		return rec
	}
	first := post(wantETag, "!")
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	firstETag := fmt.Sprintf("\"%x\"", sha256.Sum256(afterFirst))
	if first.Code != http.StatusOK || first.Header().Get("ETag") != firstETag || firstETag == wantETag || c.GetString("bot.commandPrefix") != "!" {
		t.Fatalf("accepted save failed to advance source version: status=%d headers=%v body=%s", first.Code, first.Header(), first.Body.String())
	}
	for _, stale := range []string{wantETag, "W/" + firstETag} {
		rec := post(stale, "stale-page")
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusConflict || !bytes.Equal(after, afterFirst) || c.GetString("bot.commandPrefix") != "!" || c.Status().Revision != 2 {
			t.Fatalf("stale or weak version changed accepted configuration: status=%d revision=%d body=%s", rec.Code, c.Status().Revision, rec.Body.String())
		}
	}
	second := post(firstETag, "#")
	if second.Code != http.StatusOK || c.GetString("bot.commandPrefix") != "#" || c.Status().Revision != 3 {
		t.Fatal("a refreshed page could not save after a conflict", second.Code, second.Body.String())
	}
	legacy := post("", "legacy")
	if legacy.Code != http.StatusOK || c.GetString("bot.commandPrefix") != "legacy" {
		t.Fatal("legacy clients without If-Match stopped working", legacy.Code, legacy.Body.String())
	}
}

func TestConfigHandlerPreservesIfMatchHeaderSemantics(t *testing.T) {
	for _, name := range []string{"absent", "empty", "weak", "strong-list", "multiple-lines", "wildcard"} {
		t.Run(name, func(t *testing.T) {
			const initial = "bot:\n  commandPrefix: /\n"
			path := withConfigFile(t, initial)
			c := config.GlobalConfig
			if err := c.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			etag := fmt.Sprintf("\"%x\"", sha256.Sum256([]byte(initial)))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{"config":{"bot":{"commandPrefix":"!"}}}`))
			req.Header.Set("Authorization", "Bearer admin-token")
			wantStatus := http.StatusOK
			switch name {
			case "empty":
				req.Header["If-Match"] = []string{""}
				wantStatus = http.StatusConflict
			case "weak":
				req.Header.Set("If-Match", "W/"+etag)
				wantStatus = http.StatusConflict
			case "strong-list":
				req.Header.Set("If-Match", `"obsolete", `+etag)
			case "multiple-lines":
				req.Header.Add("If-Match", `"obsolete"`)
				req.Header.Add("If-Match", etag)
			case "wildcard":
				req.Header.Set("If-Match", "*")
			}
			rec := httptest.NewRecorder()
			s := &Server{token: "admin-token"}
			s.withAuth(s.handleConfig)(rec, req)
			if rec.Code != wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, wantStatus, rec.Body.String())
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if wantStatus == http.StatusConflict {
				if string(after) != initial || c.GetString("bot.commandPrefix") != "/" || c.Status().Revision != 1 {
					t.Fatalf("rejected version changed file or runtime: data=%q revision=%d", after, c.Status().Revision)
				}
			} else if c.GetString("bot.commandPrefix") != "!" || c.Status().Revision != 2 || rec.Header().Get("ETag") != fmt.Sprintf("\"%x\"", sha256.Sum256(after)) {
				t.Fatalf("accepted version failed to publish matching file/runtime/ETag: headers=%v revision=%d", rec.Header(), c.Status().Revision)
			}
		})
	}
}
