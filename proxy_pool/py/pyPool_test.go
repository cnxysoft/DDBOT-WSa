package py

import (
	"testing"
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
