package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
)

// Configuration access is case-insensitive in Viper. The authenticated API must
// apply its secret policy to every spelling accepted by the actual runtime.
func TestConfigAPIMasksRuntimeEquivalentCredentialPaths(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, runtimePath string
	}{
		{"lowercase_bilibili", "bilibili:\n  sessdata: identity-secret\n", "bilibili.SESSDATA"},
		{"uppercase_parent", "BILIBILI:\n  SESSDATA: identity-secret\n", "bilibili.sessdata"},
		{"lowercase_camel_alias", "qqofficial:\n  appsecret: identity-secret\n", "qqofficial.appSecret"},
		{"uppercase_camel_field", "DOUYIN:\n  SESSIONID: identity-secret\n", "douyin.sessionId"},
		{"uppercase_cookie_subtree", "XHS:\n  COOKIES:\n    a1: identity-secret\n", "xhs.cookies.a1"},
		{"dotted_uppercase", "TELEGRAM.TOKEN: identity-secret\n", "telegram.token"},
		{"bilibili_password", "bilibili:\n  password: identity-secret\n", "bilibili.password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withConfigFile(t, tc.yaml)
			if err := config.GlobalConfig.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			if got := config.GlobalConfig.GetString(tc.runtimePath); got != "identity-secret" {
				t.Fatalf("fixture is not accepted by the runtime: %q", got)
			}
			s := &Server{token: "old-admin"}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
			req.Header.Set("Authorization", "Bearer old-admin")
			rec := httptest.NewRecorder()
			s.withAuth(s.handleConfig)(rec, req)
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "identity-secret") || !strings.Contains(rec.Body.String(), redactedPlaceholder) {
				t.Fatalf("runtime credential was not masked: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestConfigAPIRejectsAmbiguousStructuralIdentities(t *testing.T) {
	for _, tc := range []struct{ name, initial, patch string }{
		{"request_case", "bot:\n  commandPrefix: /\n", `{"bilibili":{"SESSDATA":"secret-one","sessdata":"secret-two"}}`},
		{"request_flat_nested", "bot:\n  commandPrefix: /\n", `{"bilibili.SESSDATA":"secret-one","bilibili":{"sessdata":"secret-two"}}`},
		{"request_scalar_map", "bot:\n  commandPrefix: /\n", `{"bilibili":"secret-one","BILIBILI.interval":"1m"}`},
		{"source_case", "bilibili:\n  SESSDATA: secret-one\nBILIBILI:\n  sessdata: secret-two\n", `{"bot":{"commandPrefix":"!"}}`},
		{"source_flat_nested", "bilibili.SESSDATA: secret-one\nbilibili:\n  sessdata: secret-two\n", `{"bot":{"commandPrefix":"!"}}`},
		{"source_multiple_documents", "bot:\n  commandPrefix: /\n---\nbilibili:\n  SESSDATA: secret-two\n", `{"bot":{"commandPrefix":"!"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := withConfigFile(t, tc.initial)
			if err := config.GlobalConfig.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			rec := postConfig(t, `{"config":`+tc.patch+`}`)
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if (rec.Code != http.StatusBadRequest && rec.Code != http.StatusInternalServerError) || !bytes.Equal(after, []byte(tc.initial)) || config.GlobalConfig.Status().Revision != 1 {
				t.Fatalf("ambiguous identity was saved: status=%d revision=%d body=%s disk=%q", rec.Code, config.GlobalConfig.Status().Revision, rec.Body.String(), after)
			}
			if strings.Contains(rec.Body.String(), "secret-one") || strings.Contains(rec.Body.String(), "secret-two") {
				t.Fatal("ambiguity error exposed a credential")
			}
		})
	}
}

func TestSetupRejectsMultipleDocumentsWithoutDroppingTrailingCredentials(t *testing.T) {
	const initial = "bot:\n  commandPrefix: /\n---\nbilibili:\n  SESSDATA: trailing-secret\n"
	path := withConfigFile(t, initial)
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(`{"token":"new-admin"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSetup(rec, req)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusBadRequest || !bytes.Equal(after, []byte(initial)) || s.getToken() != "" || config.GlobalConfig.Status().Revision != 0 || strings.Contains(rec.Body.String(), "trailing-secret") {
		t.Fatalf("setup dropped a trailing YAML document: status=%d revision=%d body=%s disk=%q", rec.Code, config.GlobalConfig.Status().Revision, rec.Body.String(), after)
	}
}

func TestConfigAPIPreservesLiteralKeysInDataNamespaces(t *testing.T) {
	path := withConfigFile(t, "customCommandPrefix:\n  Old: /\ntelegram:\n  commands:\n    Existing.Name: old\nxhs:\n  cookies:\n    a1: old-cookie\n")
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	rec := postConfig(t, `{"config":{"CUSTOMCOMMANDPREFIX":{"Help.Topic":"!","Help":"?","help":"#"},"TELEGRAM":{"COMMANDS":{"Status.Info":"new","A":"upper","a":"lower"}},"XHS":{"COOKIES":{"Web.ID":"new-cookie","Aa":"upper-cookie","aa":"lower-cookie"}}}}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	data := readConfigMap(t, path)
	for _, tc := range []struct {
		path string
		want map[string]interface{}
	}{
		{"customCommandPrefix", map[string]interface{}{"Help.Topic": "!", "Help": "?", "help": "#"}},
		{"telegram.commands", map[string]interface{}{"Existing.Name": "old", "Status.Info": "new", "A": "upper", "a": "lower"}},
		{"xhs.cookies", map[string]interface{}{"a1": "old-cookie", "Web.ID": "new-cookie", "Aa": "upper-cookie", "aa": "lower-cookie"}},
	} {
		if got := getConfigPath(t, data, tc.path); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("literal data namespace %s changed identity: got=%v want=%v", tc.path, got, tc.want)
		}
	}
}

func TestAdminAuthMarkerDistinguishesRejectedTokenFromProtectedWrite(t *testing.T) {
	withConfigFile(t, "admin:\n  token: old-admin\n")
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	s := &Server{token: "old-admin"}
	for _, tc := range []struct {
		name, token, body, wantMarker string
	}{
		{"invalid_credentials", "wrong", `{"config":{"bot":{"commandPrefix":"!"}}}`, "invalid-token"},
		{"business_forbidden", "old-admin", `{"config":{"admin":{"token":"new-admin"}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			s.withAuth(s.handleConfig)(rec, req)
			if rec.Code != http.StatusForbidden || rec.Header().Get("X-DDBOT-Auth") != tc.wantMarker {
				t.Fatalf("business policy and rejected authentication are indistinguishable: status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
			}
		})
	}
}

func TestConfigAPIMergesEquivalentCredentialPathsWithoutLosingPeers(t *testing.T) {
	for _, credential := range []string{redactedPlaceholder, "new-session"} {
		t.Run(credential, func(t *testing.T) {
			path := withConfigFile(t, "BILIBILI:\n  SESSDATA: old-session\n  bili_jct: old-csrf\n  interval: 30s\n")
			config.GlobalConfig.ConfigureReload(nil, nil)
			if err := config.GlobalConfig.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			rec := postConfig(t, `{"config":{"bilibili":{"sessdata":"`+credential+`","interval":"2m"}}}`)
			wantSession := credential
			if credential == redactedPlaceholder {
				wantSession = "old-session"
			}
			if rec.Code != http.StatusOK || config.GlobalConfig.GetString("bilibili.sessdata") != wantSession || config.GlobalConfig.GetString("bilibili.bili_jct") != "old-csrf" || config.GlobalConfig.GetString("bilibili.interval") != "2m" {
				t.Fatalf("case variant merge lost a sibling or credential: status=%d session=%q csrf=%q interval=%q body=%s", rec.Code, config.GlobalConfig.GetString("bilibili.sessdata"), config.GlobalConfig.GetString("bilibili.bili_jct"), config.GlobalConfig.GetString("bilibili.interval"), rec.Body.String())
			}
			roots := 0
			for key := range readConfigMap(t, path) {
				if strings.EqualFold(key, "bilibili") {
					roots++
				}
			}
			if roots != 1 {
				t.Fatalf("save created %d equivalent bilibili roots, so a future reload depends on map iteration", roots)
			}
		})
	}
}

func TestConfigAPIRejectsRuntimeEquivalentProtectedPaths(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, patch, runtimePath string
	}{
		{"mixedcase_admin", "Admin:\n  Token: old-admin\n", `{"Admin":{"Token":"new-admin"}}`, "admin.token"},
		{"uppercase_admin", "ADMIN:\n  TOKEN: old-admin\n", `{"ADMIN":{"TOKEN":"new-admin"}}`, "admin.token"},
		{"uppercase_websocket", "WEBSOCKET:\n  TOKEN: old-admin\n", `{"WEBSOCKET":{"TOKEN":"new-admin"}}`, "websocket.token"},
		{"dotted_admin", "ADMIN.TOKEN: old-admin\n", `{"ADMIN.TOKEN":"new-admin"}`, "admin.token"},
		{"mixedcase_parent_replacement", "Admin:\n  Token: old-admin\n", `{"Admin":null}`, "admin.token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := withConfigFile(t, tc.yaml)
			// This is the Config manager used by production; enabling every key here
			// isolates policy enforcement from the resource participant allowlist.
			config.GlobalConfig.ConfigureReload(nil, nil)
			if err := config.GlobalConfig.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			if got := config.GlobalConfig.GetString(tc.runtimePath); got != "old-admin" {
				t.Fatalf("fixture is not accepted by the runtime: %q", got)
			}
			s := &Server{token: "old-admin"}
			config.GlobalConfig.OnPrepareReload(s.prepareRuntimeReload)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/config", strings.NewReader(`{"config":`+tc.patch+`}`))
			req.Header.Set("Authorization", "Bearer old-admin")
			rec := httptest.NewRecorder()
			s.withAuth(s.handleConfig)(rec, req)
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusForbidden || !bytes.Equal(after, []byte(tc.yaml)) || config.GlobalConfig.Status().Revision != 1 || config.GlobalConfig.GetString(tc.runtimePath) != "old-admin" || s.getToken() != "old-admin" {
				t.Fatalf("protected alias accepted: status=%d revision=%d effective=%q live-admin-token=%q disk=%q body=%s", rec.Code, config.GlobalConfig.Status().Revision, config.GlobalConfig.GetString(tc.runtimePath), s.getToken(), after, rec.Body.String())
			}
		})
	}
}
