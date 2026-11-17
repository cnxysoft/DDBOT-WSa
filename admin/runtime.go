package admin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/sirupsen/logrus"
)

var activeServerMu sync.Mutex
var activeServer *Server

func configuredAdminAddr(s *config.Snapshot) string {
	if addr := s.GetString("admin.addr"); addr != "" {
		return addr
	}
	return defaultAdminAddr
}

// serve is called while the lifecycle owner has exclusive access.
func (s *Server) serve(ln net.Listener) {
	h := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
	s.listener, s.httpServer = ln, h
	go func() {
		if err := h.Serve(ln); err != nil && err != http.ErrServerClosed {
			logrus.Error("Web 管理面板服务异常退出")
		}
	}()
}

// Called with runtimeMu held, so Stop cannot begin waiting before registration.
func (s *Server) retireServer(h *http.Server) {
	if h == nil {
		return
	}
	s.retired.Add(1)
	go func() { defer s.retired.Done(); drainServer(h) }()
}

func drainServer(h *http.Server) {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if h.Shutdown(ctx) != nil {
		_ = h.Close()
	}
}

// PrepareRuntimeReload reserves a new listener before saving. Existing requests,
// including the save itself, finish on the old listener. Login sessions remain
// on the same Server; SSE clients reconnect at the new address when changed.
func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	activeServerMu.Lock()
	s := activeServer
	activeServerMu.Unlock()
	if s == nil {
		return nil, nil, nil
	}
	return s.prepareRuntimeReload(previous, next)
}

func (s *Server) prepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	enabled := next.GetBool("admin.enable")
	addr := configuredAdminAddr(next)
	token := next.GetString("admin.token")
	if enabled == previous.GetBool("admin.enable") && addr == configuredAdminAddr(previous) && token == previous.GetString("admin.token") {
		return nil, nil, nil
	}
	s.runtimeMu.Lock()
	var candidate net.Listener
	if enabled && (s.httpServer == nil || s.addr != addr) {
		var err error
		candidate, err = net.Listen("tcp", addr)
		if err != nil {
			s.runtimeMu.Unlock()
			return nil, nil, fmt.Errorf("Web 管理面板新地址不可用，保留原监听")
		}
	}
	abort := func() {
		if candidate != nil {
			_ = candidate.Close()
		}
		s.runtimeMu.Unlock()
	}
	commit := func() {
		s.setToken(token)
		old := s.httpServer
		if !enabled {
			s.listener, s.httpServer = nil, nil
		} else if candidate != nil {
			s.addr = addr
			s.serve(candidate)
		} else {
			old = nil
		}
		s.retireServer(old)
		s.runtimeMu.Unlock()
	}
	return commit, abort, nil
}

func Stop() {
	activeServerMu.Lock()
	s := activeServer
	activeServer = nil
	activeServerMu.Unlock()
	if s != nil {
		s.tokenMu.Lock()
		s.authStopped = true
		s.revokeStreamsLocked()
		s.loginManagerOf().revokeAll()
		s.tokenMu.Unlock()
		s.runtimeMu.Lock()
		h := s.httpServer
		s.listener, s.httpServer = nil, nil
		s.runtimeMu.Unlock()
		drainServer(h)
		s.retired.Wait()
	}
}
