package requests

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
)

func requestProxyPool(address string) proxy_pool.IProxyPool {
	return local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{{Proxy: address, Type: proxy_pool.PreferOversea}})
}

func responseServer(t *testing.T, body string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, body) }))
	t.Cleanup(s.Close)
	return s
}

func requireHTTPBody(t *testing.T, address, want string, options ...Option) {
	t.Helper()
	var body string
	if err := Get(address, nil, &body, options...); err != nil || body != want {
		t.Fatalf("HTTP body=%q err=%v, want %q", body, err, want)
	}
}

func TestProxyGetterUsesCandidateThenFollowsGlobalPool(t *testing.T) {
	defer proxy_pool.Swap(nil)
	var oldCalls, newCalls, futureCalls atomic.Int32
	old := responseServer(t, "old", &oldCalls)
	next := responseServer(t, "candidate", &newCalls)
	future := responseServer(t, "future", &futureCalls)
	proxy_pool.Swap(requestProxyPool(old.URL))
	getter, commit, abort := proxy_pool.PrepareSwap(requestProxyPool(next.URL))
	option := ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)
	const address = "http://account.example.invalid/check"
	requireHTTPBody(t, address, "candidate", option)
	requireHTTPBody(t, address, "old", ProxyOption(proxy_pool.PreferOversea))
	commit()
	abort()
	requireHTTPBody(t, address, "candidate", option)
	proxy_pool.Swap(requestProxyPool(future.URL))
	requireHTTPBody(t, address, "future", option)
	if oldCalls.Load() != 1 || newCalls.Load() != 2 || futureCalls.Load() != 1 {
		t.Fatal("wrong proxy route counts", oldCalls.Load(), newCalls.Load(), futureCalls.Load())
	}
}

func TestProxyGetterOffOverridesTransportAndAbortFailsBeforeNetwork(t *testing.T) {
	defer proxy_pool.Swap(nil)
	var targetCalls, oldCalls atomic.Int32
	target := responseServer(t, "direct", &targetCalls)
	old := responseServer(t, "old", &oldCalls)
	proxy_pool.Swap(requestProxyPool(old.URL))
	getter, commit, abort := proxy_pool.PrepareSwap(nil)
	option := ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)
	oldURL, _ := url.Parse(old.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(oldURL)
	defer transport.CloseIdleConnections()
	requireHTTPBody(t, target.URL, "direct", WithTransport(transport), RawProxyOption(old.URL), option)
	if transport.Proxy == nil {
		t.Fatal("direct request mutated caller transport")
	}
	abort()
	commit()
	for _, prefer := range []proxy_pool.Prefer{proxy_pool.PreferOversea, proxy_pool.PreferNone} {
		var body string
		err := Get(target.URL, nil, &body, ProxyOptionWithGetter(prefer, getter))
		if !errors.Is(err, proxy_pool.ErrCandidateAborted) {
			t.Fatal("aborted getter did not fail closed", err)
		}
	}
	if targetCalls.Load() != 1 || oldCalls.Load() != 0 {
		t.Fatal("aborted/direct candidate leaked to another route", targetCalls.Load(), oldCalls.Load())
	}
	requireHTTPBody(t, target.URL, "old", ProxyOption(proxy_pool.PreferOversea))
}

func TestNilProxyGetterReadsCurrentGlobalForEveryRequest(t *testing.T) {
	defer proxy_pool.Swap(nil)
	var firstCalls, secondCalls atomic.Int32
	first := responseServer(t, "first", &firstCalls)
	second := responseServer(t, "second", &secondCalls)
	option := ProxyOptionWithGetter(proxy_pool.PreferOversea, nil)
	proxy_pool.Swap(requestProxyPool(first.URL))
	requireHTTPBody(t, "http://account.example.invalid", "first", option)
	proxy_pool.Swap(requestProxyPool(second.URL))
	requireHTTPBody(t, "http://account.example.invalid", "second", option)
}

func TestProxyGetterPreservesTLSCookieAndTransportOptions(t *testing.T) {
	for _, withHTTP2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", withHTTP2), func(t *testing.T) {
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cookie, err := r.Cookie("session")
				if err != nil || cookie.Value != "candidate" {
					http.Error(w, "cookie missing", 400)
					return
				}
				_, _ = io.WriteString(w, "verified")
			}))
			target.EnableHTTP2 = withHTTP2
			target.StartTLS()
			defer target.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(target.URL)
			jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "candidate"}})
			transport := http.DefaultTransport.(*http.Transport).Clone()
			defer transport.CloseIdleConnections()
			getter, _, abort := proxy_pool.PrepareSwap(nil)
			defer abort()
			opts := []Option{WithTransport(transport), WithCookieJar(jar), DisableTlsOption(), ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)}
			if withHTTP2 {
				opts = append(opts, HTTP2Option())
			}
			requireHTTPBody(t, target.URL, "verified", opts...)
			if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
				t.Fatal("request changed shared TLS verification")
			}
		})
	}
}

type replacementRoundTripper struct{}

func (replacementRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("default transport must not be used")
}

func TestProxyGetterDirectHandlesCustomDefaultRoundTripper(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = replacementRoundTripper{}
	defer func() { http.DefaultTransport = previous }()
	var calls atomic.Int32
	target := responseServer(t, "direct", &calls)
	getter, _, abort := proxy_pool.PrepareSwap(nil)
	defer abort()
	requireHTTPBody(t, target.URL, "direct", ProxyOptionWithGetter(proxy_pool.PreferOversea, getter))
}

type emptySelectedProxy struct{}

func (emptySelectedProxy) ProxyString() string { return "" }

func TestInvalidProxyGetterFailsWithoutSendingRequest(t *testing.T) {
	var calls atomic.Int32
	target := responseServer(t, "unexpected", &calls)
	for _, getter := range []proxy_pool.Getter{
		func(proxy_pool.Prefer) (proxy_pool.IProxy, error) { return nil, nil },
		func(proxy_pool.Prefer) (proxy_pool.IProxy, error) { return emptySelectedProxy{}, nil },
		func(proxy_pool.Prefer) (proxy_pool.IProxy, error) { return nil, errors.New("candidate unavailable") },
	} {
		var body string
		if err := Get(target.URL, nil, &body, ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)); err == nil {
			t.Fatal("invalid proxy getter accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid candidate request reached the network", calls.Load())
	}
}

type unavailableRequestProxyPool struct{}

func (unavailableRequestProxyPool) Get(proxy_pool.Prefer) (proxy_pool.IProxy, error) {
	return nil, errors.New("pool unavailable")
}
func (unavailableRequestProxyPool) Delete(string) bool { return false }
func (unavailableRequestProxyPool) Stop() error        { return nil }

func TestNilProxyGetterRetainsLegacyRawAndTransportFallback(t *testing.T) {
	defer proxy_pool.Swap(nil)
	var calls atomic.Int32
	proxy := responseServer(t, "fallback", &calls)
	option := ProxyOptionWithGetter(proxy_pool.PreferOversea, nil)
	for _, pool := range []proxy_pool.IProxyPool{nil, unavailableRequestProxyPool{}} {
		proxy_pool.Swap(pool)
		requireHTTPBody(t, "http://account.example.invalid", "fallback", RawProxyOption(proxy.URL), option)
		proxyURL, _ := url.Parse(proxy.URL)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(proxyURL)
		requireHTTPBody(t, "http://account.example.invalid", "fallback", WithTransport(transport), option)
		transport.CloseIdleConnections()
	}
	if calls.Load() != 4 {
		t.Fatal("legacy fallback routes were changed", calls.Load())
	}
}

func TestProxyGetterUsesSOCKSWithDefaultDialContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		serve := func() error {
			header := make([]byte, 2)
			if _, err := io.ReadFull(conn, header); err != nil {
				return err
			}
			methods := make([]byte, int(header[1]))
			if _, err := io.ReadFull(conn, methods); err != nil {
				return err
			}
			if _, err := conn.Write([]byte{5, 0}); err != nil {
				return err
			}
			header = make([]byte, 4)
			if _, err := io.ReadFull(conn, header); err != nil {
				return err
			}
			if header[0] != 5 || header[1] != 1 || header[3] != 3 {
				return fmt.Errorf("unexpected SOCKS request %v", header)
			}
			length := make([]byte, 1)
			if _, err := io.ReadFull(conn, length); err != nil {
				return err
			}
			target := make([]byte, int(length[0])+2)
			if _, err := io.ReadFull(conn, target); err != nil {
				return err
			}
			if string(target[:len(target)-2]) != "socks-target.invalid" {
				return fmt.Errorf("unexpected SOCKS target %q", target)
			}
			if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
				return err
			}
			req, err := http.ReadRequest(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			req.Body.Close()
			_, err = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nsocks")
			return err
		}
		done <- serve()
	}()
	getter, _, abort := proxy_pool.PrepareSwap(requestProxyPool("socks5://" + ln.Addr().String()))
	defer abort()
	requireHTTPBody(t, "http://socks-target.invalid/status", "socks", ProxyOptionWithGetter(proxy_pool.PreferOversea, getter))
	if err := <-done; err != nil {
		t.Fatal("SOCKS routing failed", err)
	}
}
