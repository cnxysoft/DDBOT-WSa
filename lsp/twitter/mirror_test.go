package twitter

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/stretchr/testify/require"
)

const mirrorTestHTML = `<html><head><title>Test User (@testuser)</title>
<meta property="og:title" content="Test User (@testuser)"></head><body>
<div class="timeline-item"><a class="tweet-link" href="/testuser/status/2119846862286930036#m"></a>
<div class="tweet-body"><div class="tweet-content">A complete post</div>
<span class="tweet-date"><a title="Nov 9, 2026 · 5:37 PM UTC"></a></span></div></div>
<div class="timeline-item show-more"><a href="?cursor=next">Load more</a></div></body></html>`

func useTestMirrors(t *testing.T, urls ...string) {
	t.Helper()
	oldURLs, oldCookie := BaseURL, Cookie
	BaseURL = urls
	Cookie, _ = cookiejar.New(nil)
	proxy_pool.Init(nil)
	t.Cleanup(func() { BaseURL, Cookie = oldURLs, oldCookie })
}

func TestMirrorFailoverRemembersSuccessfulInstance(t *testing.T) {
	var failures, successes atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failures.Add(1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<title>Gandalf - You shall not pass!</title>`)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successes.Add(1)
		fmt.Fprint(w, mirrorTestHTML)
	}))
	defer good.Close()
	useTestMirrors(t, bad.URL, good.URL)
	tc := &twitterConcern{}
	for i := 0; i < 2; i++ {
		posts, err := tc.GetTweets("testuser")
		require.NoError(t, err)
		require.Len(t, posts, 1, "pagination controls must not become empty tweets")
		require.Equal(t, "2119846862286930036", posts[0].ID)
		require.False(t, posts[0].CreatedAt.IsZero())
	}
	require.EqualValues(t, 1, failures.Load())
	require.EqualValues(t, 2, successes.Load())
}

func TestMirrorContinueLinkKeepsSession(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("__aragorn") == "accepted" {
			cookie, err := r.Cookie("verification")
			require.NoError(t, err)
			require.Equal(t, "started", cookie.Value)
			fmt.Fprint(w, mirrorTestHTML)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "verification", Value: "started", Path: "/"})
		fmt.Fprint(w, `<title>Checking your browser</title><a class="timeline-item" href="/testuser?__aragorn=accepted">continue</a>`)
	}))
	defer server.Close()
	useTestMirrors(t, server.URL)
	posts, err := (&twitterConcern{}).GetTweets("testuser")
	require.NoError(t, err)
	require.Len(t, posts, 1)
	require.EqualValues(t, 2, calls.Load())
}

func TestMirrorContinueLinkIsBoundedAndSameOrigin(t *testing.T) {
	for _, link := range []string{"/testuser?__aragorn=loop", "https://example.org/testuser?__aragorn=escape", "/otheruser?__aragorn=escape"} {
		t.Run(link, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprintf(w, `<title>Checking your browser</title><a href="%s">continue</a>`, link)
			}))
			defer server.Close()
			useTestMirrors(t, server.URL)
			_, err := (&twitterConcern{}).GetTweets("testuser")
			require.Error(t, err)
			if strings.Contains(link, "loop") {
				require.EqualValues(t, mirrorChallengeAttempts, calls.Load())
			} else {
				require.EqualValues(t, 1, calls.Load())
			}
		})
	}
}

func TestMirrorRejectsFalseSuccessPages(t *testing.T) {
	for _, body := range []string{
		`<title>Gandalf</title><p>Checking your connection</p>`,
		`<title>Prove You're Human</title><p>Click the dog's nose</p>`,
		`<meta property="og:title" content="Unexpected title without a username">`,
		`<meta property="og:title" content="Another User (@someoneelse)">`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			useTestMirrors(t, server.URL)
			_, err := (&twitterConcern{}).GetTweets("testuser")
			require.ErrorContains(t, err, "目标用户资料")
		})
	}
}

func TestMirrorRejectsUnsupportedAnubis(t *testing.T) {
	for _, rules := range []string{
		`{"algorithm":"hashx","difficulty":1,"extensions":["css-load"]}`,
		`{"algorithm":"fast","difficulty":1,"extensions":["css-load"]}`,
	} {
		t.Run(rules, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprintf(w, `<title>Making sure you're not a bot!</title><script id="anubis_challenge" type="application/json">{"rules":%s,"challenge":"sample"}</script>`, rules)
			}))
			defer server.Close()
			useTestMirrors(t, server.URL)
			_, err := (&twitterConcern{}).GetTweets("testuser")
			require.ErrorContains(t, err, "不支持的 Anubis")
		})
	}
}

func TestMirrorAnubisDetectionDoesNotDependOnLanguage(t *testing.T) {
	for _, title := range []string{"Making sure you're not a bot!", "正在确认你是不是机器人！"} {
		body := fmt.Sprintf(`<title>%s</title><script id="anubis_challenge" type="application/json">{"rules":{"algorithm":"fast","difficulty":1},"challenge":"sample"}</script>`, title)
		_, _, challenge, err := ParseResp([]byte(body), "https://example.org/testuser")
		require.NoError(t, err)
		require.NotNil(t, challenge)
		require.Equal(t, "anubis", challenge.Type)
	}
}

func TestDefaultMirrorWatchPremarksExistingTweets(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, mirrorTestHTML) }))
	defer server.Close()
	useTestMirrors(t, server.URL)
	oldMode, oldAPI := TwitterMode, twitterAPI
	TwitterMode, twitterAPI = resolveTwitterMode(""), nil
	t.Cleanup(func() { TwitterMode, twitterAPI = oldMode, oldAPI })
	tc := newConcern(nil)
	tc.StateManager.FreshIndex(123)
	identity, err := tc.Add(nil, 123, "testuser", Tweets)
	require.NoError(t, err)
	require.Equal(t, "Test User", identity.GetName())
	posts, err := tc.GetTweets("testuser")
	require.NoError(t, err)
	require.False(t, tc.filterTweet(posts[0]), "watch must not push the existing feed on its first poll")
	require.True(t, tc.filterTweet(&Tweet{ID: "2119846862286930037"}))
	state, err := tc.GetStateManager().GetConcern("testuser")
	require.NoError(t, err)
	require.True(t, state.ContainAll(Tweets))

}
