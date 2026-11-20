package requests

import (
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

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
)

type unavailableCompatibilityPool struct{}

func (unavailableCompatibilityPool) Get(proxy_pool.Prefer) (proxy_pool.IProxy, error) {
	return nil, errors.New("pool is unavailable")
}

func TestCandidateHTTPSProxyRetainsCallerTrustCookieJarAndHTTP2(t *testing.T) {
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("candidate-session")
		if err != nil || cookie.Value != "same-request" || r.TLS == nil || r.ProtoMajor != 2 {
			http.Error(w, "candidate lost TLS, HTTP2 or cookie options", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "https-candidate")
	}))
	target.EnableHTTP2 = true
	target.StartTLS()
	defer target.Close()
	var tunnels atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target.Listener.Addr().String() {
			http.Error(w, "unexpected CONNECT target", http.StatusBadRequest)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		tunnels.Add(1)
		fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { defer client.Close(); defer upstream.Close(); _, _ = io.Copy(upstream, client) }()
		_, _ = io.Copy(client, upstream)
		client.Close()
		upstream.Close()
	}))
	defer proxy.Close()
	getter, _, abort := proxy_pool.PrepareSwap(requestProxyPool(proxy.URL))
	defer abort()
	transport := target.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(target.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate-session", Value: "same-request"}})
	var body string
	if err := Get(target.URL, nil, &body, WithTransport(transport), WithCookieJar(jar), HTTP2Option(), ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)); err != nil {
		t.Fatal(err)
	}
	if body != "https-candidate" || tunnels.Load() != 1 {
		t.Fatalf("candidate HTTPS route=%q tunnels=%d", body, tunnels.Load())
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("candidate changed caller TLS trust configuration")
	}
}
func (unavailableCompatibilityPool) Delete(string) bool { return false }
func (unavailableCompatibilityPool) Stop() error        { return nil }

func TestNilGetterPreservesLegacyFallbackTransportAndRawProxy(t *testing.T) {
	defer proxy_pool.Swap(nil)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "caller-proxy") }))
	defer proxy.Close()
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "direct") }))
	defer direct.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, unavailable := range []bool{false, true} {
		for _, prefer := range []proxy_pool.Prefer{proxy_pool.PreferOversea, proxy_pool.PreferNone} {
			for _, transportFallback := range []bool{false, true} {
				t.Run(fmt.Sprintf("unavailable=%v/prefer=%d/transport=%v", unavailable, prefer, transportFallback), func(t *testing.T) {
					if unavailable {
						proxy_pool.Swap(unavailableCompatibilityPool{})
					} else {
						proxy_pool.Swap(nil)
					}
					var options []Option
					if transportFallback {
						transport := http.DefaultTransport.(*http.Transport).Clone()
						transport.Proxy = http.ProxyURL(proxyURL)
						defer transport.CloseIdleConnections()
						options = append(options, WithTransport(transport))
					} else {
						options = append(options, RawProxyOption(proxy.URL))
					}
					for _, selector := range []Option{ProxyOption(prefer), ProxyOptionWithGetter(prefer, nil)} {
						var body string
						if err := Get(direct.URL, nil, &body, append(options, selector)...); err != nil {
							t.Fatal(err)
						}
						if body != "caller-proxy" {
							t.Fatalf("nil getter changed legacy fallback route to %q", body)
						}
					}
				})
			}
		}
	}
}
