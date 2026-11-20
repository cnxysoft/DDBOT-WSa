package proxy_pool

import (
	"errors"
	"sync"
)

var ErrNil = errors.New("<nil>")

// ErrCandidateAborted prevents a discarded candidate from silently using the
// running pool or a direct connection.
var ErrCandidateAborted = errors.New("proxy candidate was aborted")

type Prefer int64

const (
	PreferAny Prefer = 1 << iota
	PreferMainland
	PreferOversea
	PreferNone
)

type IProxyPool interface {
	Get(Prefer) (IProxy, error)
	Delete(string) bool
	Stop() error
}

type IProxy interface {
	ProxyString() string
}

// Getter selects a proxy for one request. ErrNil explicitly selects a direct
// connection; other errors must not fall back to a different pool.
type Getter func(Prefer) (IProxy, error)

var proxyPool IProxyPool
var poolMu sync.RWMutex

func Init(proxy IProxyPool) {
	Swap(proxy)
}

// Swap waits for pool operations to finish before retiring the previous pool.
// A proxy string already handed to an HTTP request remains valid.
func Swap(proxy IProxyPool) {
	poolMu.Lock()
	defer poolMu.Unlock()
	old := proxyPool
	proxyPool = proxy
	if old != nil && old != proxy {
		_ = old.Stop()
	}
}

func Get(prefer Prefer) (IProxy, error) {
	poolMu.RLock()
	defer poolMu.RUnlock()
	if proxyPool == nil {
		return nil, ErrNil
	}
	return proxyPool.Get(prefer)
}
func Delete(proxy string) bool {
	poolMu.Lock()
	defer poolMu.Unlock()
	if proxyPool == nil {
		return false
	}
	return proxyPool.Delete(proxy)
}
func Stop() error {
	poolMu.Lock()
	defer poolMu.Unlock()
	if proxyPool == nil {
		return ErrNil
	}
	old := proxyPool
	proxyPool = nil
	return old.Stop()
}
