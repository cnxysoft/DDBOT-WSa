package proxy_pool

import (
	"errors"
	"testing"
)

// mockProxyPool is a mock implementation of IProxyPool for testing
type mockProxyPool struct {
	proxies []string
}

func (m *mockProxyPool) Get(prefer Prefer) (IProxy, error) {
	if len(m.proxies) == 0 {
		return nil, errors.New("no proxies available")
	}
	return &mockProxy{proxy: m.proxies[0]}, nil
}

func (m *mockProxyPool) Delete(proxy string) bool {
	for i, p := range m.proxies {
		if p == proxy {
			m.proxies = append(m.proxies[:i], m.proxies[i+1:]...)
			return true
		}
	}
	return false
}

func (m *mockProxyPool) Stop() error {
	m.proxies = nil
	return nil
}

// mockProxy is a mock implementation of IProxy
type mockProxy struct {
	proxy string
}

func (m *mockProxy) ProxyString() string {
	return m.proxy
}

func TestInitAndGet(t *testing.T) {
	// Reset global state
	proxyPool = nil

	t.Run("Get should return ErrNil when pool not initialized", func(t *testing.T) {
		proxy, err := Get(PreferAny)
		if err != ErrNil {
			t.Errorf("Expected ErrNil, got %v", err)
		}
		if proxy != nil {
			t.Error("Expected nil proxy")
		}
	})

	t.Run("Get should work after Init", func(t *testing.T) {
		mockPool := &mockProxyPool{proxies: []string{"http://proxy1:8080"}}
		Init(mockPool)

		proxy, err := Get(PreferAny)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if proxy == nil {
			t.Fatal("Expected non-nil proxy")
		}
		if proxy.ProxyString() != "http://proxy1:8080" {
			t.Errorf("Expected http://proxy1:8080, got %s", proxy.ProxyString())
		}
	})
}

func TestDelete(t *testing.T) {
	// Reset global state
	proxyPool = nil

	t.Run("Delete should return false when pool not initialized", func(t *testing.T) {
		result := Delete("http://proxy1:8080")
		if result {
			t.Error("Expected false when pool not initialized")
		}
	})

	t.Run("Delete should work after Init", func(t *testing.T) {
		mockPool := &mockProxyPool{proxies: []string{"http://proxy1:8080", "http://proxy2:8080"}}
		Init(mockPool)

		result := Delete("http://proxy1:8080")
		if !result {
			t.Error("Expected true for existing proxy")
		}

		result = Delete("http://nonexistent:8080")
		if result {
			t.Error("Expected false for non-existent proxy")
		}
	})
}

func TestStop(t *testing.T) {
	// Reset global state
	proxyPool = nil

	t.Run("Stop should return ErrNil when pool not initialized", func(t *testing.T) {
		err := Stop()
		if err != ErrNil {
			t.Errorf("Expected ErrNil, got %v", err)
		}
	})

	t.Run("Stop should work after Init", func(t *testing.T) {
		mockPool := &mockProxyPool{proxies: []string{"http://proxy1:8080"}}
		Init(mockPool)

		err := Stop()
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})
}

func TestPreferConstants(t *testing.T) {
	t.Run("Prefer constants should have correct values", func(t *testing.T) {
		if PreferAny != 1 {
			t.Errorf("Expected PreferAny to be 1, got %d", PreferAny)
		}
		if PreferMainland != 2 {
			t.Errorf("Expected PreferMainland to be 2, got %d", PreferMainland)
		}
		if PreferOversea != 4 {
			t.Errorf("Expected PreferOversea to be 4, got %d", PreferOversea)
		}
		if PreferNone != 8 {
			t.Errorf("Expected PreferNone to be 8, got %d", PreferNone)
		}
	})
}

func TestErrNil(t *testing.T) {
	if ErrNil == nil {
		t.Error("ErrNil should not be nil")
	}
	if ErrNil.Error() != "<nil>" {
		t.Errorf("Expected error message '<nil>', got '%s'", ErrNil.Error())
	}
}
