package weibo

import (
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// TestFreshCookie tests the FreshCookieGuest function - requires external network access
// and is disabled in CI. To run locally:
// 2. Run: go test -v -run TestFreshCookie ./lsp/weibo/...
// func TestFreshCookie(t *testing.T) {
// 	var cookies []*http.Cookie
// 	var err error
// 	localutils.Retry(5, time.Second, func() bool {
// 		cookies, err = FreshCookieGuest()
// 		return err == nil
// 	})
// 	assert.Nil(t, err)
// 	assert.NotEmpty(t, cookies)
// }

func TestExtractCookieValue(t *testing.T) {
	opts := []requests.Option{
		requests.CookieOption("SUB", "sub-value"),
		requests.CookieOption("SUBP", "subp-value"),
		requests.CookieOption("XSRF-TOKEN", "token=value"),
	}

	if got := extractCookieValue(opts, "SUB"); got != "sub-value" {
		t.Fatalf("extractCookieValue(SUB) = %q, want sub-value", got)
	}
	if got := extractCookieValue(opts, "XSRF-TOKEN"); got != "token=value" {
		t.Fatalf("extractCookieValue(XSRF-TOKEN) = %q, want token=value", got)
	}
	if got := extractCookieValue(opts, "missing"); got != "" {
		t.Fatalf("extractCookieValue(missing) = %q, want empty", got)
	}
}
