package admin

import (
	"bytes"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

// Config saves return navigation only after the listener change actually commits.
// Addresses are data, not URLs: the browser must account for wildcard listeners
// and reverse proxies without transferring its token to another origin.
type configNavigation struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr,omitempty"`
}

func (s *Server) prepareConfigNavigation(data []byte) func() *configNavigation {
	unchanged := func() *configNavigation { return nil }
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return unchanged // Validation owns the error response.
	}
	previous := config.GlobalConfig.Snapshot()
	enabled := v.GetBool("admin.enable")
	addr := v.GetString("admin.addr")
	if addr == "" {
		addr = defaultAdminAddr
	}
	if enabled == previous.GetBool("admin.enable") && addr == configuredAdminAddr(previous) {
		return unchanged
	}
	s.runtimeMu.Lock()
	wasListening := s.httpServer != nil && s.listener != nil
	s.runtimeMu.Unlock()
	return func() *configNavigation {
		s.runtimeMu.Lock()
		defer s.runtimeMu.Unlock()
		if !enabled {
			if wasListening && s.httpServer == nil {
				return &configNavigation{Enabled: false}
			}
			return nil
		}
		if s.httpServer == nil || s.listener == nil || s.addr != addr {
			return nil
		}
		// A :0 bind must advertise the assigned port, never the requested zero.
		return &configNavigation{Enabled: true, Addr: s.listener.Addr().String()}
	}
}
