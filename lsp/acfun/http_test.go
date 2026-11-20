package acfun

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fixtureTransport struct {
	url, body string
}

func (f fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.String() != f.url {
		return nil, fmt.Errorf("unexpected request in AcFun test: %s %s", req.Method, req.URL)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    req,
	}, nil
}

func useHTTPFixture(t *testing.T, url, body string) {
	t.Helper()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = fixtureTransport{url: url, body: body}
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
}

const livePageFixture = `<html><script>window.__INITIAL_STATE__={"liveInfo":{"user":{"name":"admin","headUrl":"https://example.invalid/avatar.jpg"}}};(function(){}());</script></html>`
