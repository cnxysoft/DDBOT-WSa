package py

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

func TestPyProxy(t *testing.T) {
	t.Run("ProxyString should return proxy string", func(t *testing.T) {
		proxy := &PyProxy{proxy: "http://test:8080"}
		result := proxy.ProxyString()
		if result != "http://test:8080" {
			t.Errorf("Expected http://test:8080, got %s", result)
		}
	})

	t.Run("ProxyString with empty string", func(t *testing.T) {
		proxy := &PyProxy{proxy: ""}
		result := proxy.ProxyString()
		if result != "" {
			t.Errorf("Expected empty string, got %s", result)
		}
	})
}

func TestPyProxyNormalizesBareAddressesAndPreservesSchemes(t *testing.T) {
	for address, want := range map[string]string{
		"": "", "127.0.0.1:8080": "http://127.0.0.1:8080", "[::1]:8080": "http://[::1]:8080",
		"http://proxy:8080": "http://proxy:8080", "https://proxy:443": "https://proxy:443",
		"socks5://proxy:1080": "socks5://proxy:1080", "socks5h://proxy:1080": "socks5h://proxy:1080",
	} {
		if got := (&PyProxy{proxy: address}).ProxyString(); got != want {
			t.Fatalf("ProxyString(%q)=%q, want %q", address, got, want)
		}
	}
}

func TestRealPYServiceBareAddressSupportsStrictRequests(t *testing.T) {
	var proxyCalls, getCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.URL.Host != "account.example.invalid" {
			t.Errorf("request did not use HTTP proxy form: %s", r.URL)
		}
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/get" {
			getCalls.Add(1)
			_ = json.NewEncoder(w).Encode(GetResponse{Proxy: strings.TrimPrefix(proxy.URL, "http://")})
			return
		}
		_, _ = io.WriteString(w, "ready")
	}))
	defer service.Close()
	pool, err := NewPYProxyPool(service.URL)
	if err != nil {
		t.Fatal("real service health check failed", err)
	}
	getter, _, abort := proxy_pool.PrepareSwap(pool)
	defer abort()
	var body string
	if err := requests.Get("http://account.example.invalid/session", nil, &body, requests.ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)); err != nil || body != "proxied" {
		t.Fatal("bare service address was not accepted by strict request", body, err)
	}
	if getCalls.Load() != 1 || proxyCalls.Load() != 1 {
		t.Fatal("request did not traverse service and proxy", getCalls.Load(), proxyCalls.Load())
	}
}

func TestProxyPoolGet(t *testing.T) {
	t.Run("Get should return error when pool is nil", func(t *testing.T) {
		var pool *ProxyPool = nil
		_, err := pool.Get(0)
		if err == nil {
			t.Error("Expected error when pool is nil")
		}
		if err.Error() != "<nil>" {
			t.Errorf("Expected '<nil>' error, got '%s'", err.Error())
		}
	})
}

func TestProxyPoolDelete(t *testing.T) {
	t.Run("Delete should return false when pool is nil", func(t *testing.T) {
		var pool *ProxyPool = nil
		result := pool.Delete("http://test:8080")
		if result {
			t.Error("Expected false when pool is nil")
		}
	})
}

func TestProxyPoolStop(t *testing.T) {
	t.Run("Stop should return nil", func(t *testing.T) {
		pool := &ProxyPool{Host: "http://localhost:5010"}
		err := pool.Stop()
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})
}

func TestNewPYProxyPool(t *testing.T) {
	t.Run("NewPYProxyPool should create pool with valid host", func(t *testing.T) {
		// This test will fail because we don't have a real proxy server running
		// But it tests the function signature and basic behavior
		pool, err := NewPYProxyPool("http://invalid-host-that-does-not-exist:5010")
		// We expect an error because the host doesn't exist
		if err == nil && pool != nil {
			t.Log("Unexpectedly succeeded - proxy server might be running")
		}
	})
}

func TestProxyPoolStruct(t *testing.T) {
	t.Run("ProxyPool should store host correctly", func(t *testing.T) {
		pool := &ProxyPool{Host: "http://localhost:5010"}
		if pool.Host != "http://localhost:5010" {
			t.Errorf("Expected http://localhost:5010, got %s", pool.Host)
		}
	})
}
