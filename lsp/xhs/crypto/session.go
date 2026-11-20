package crypto

import (
	"math/rand"
	"sync"
	"time"
)

// SignState represents the immutable state for a single signing operation
type SignState struct {
	PageLoadTimestamp int64
	SequenceValue     int
	WindowPropsLength int
	URILength         int
}

// SessionManager manages state for a simulated user session
type SessionManager struct {
	mu                sync.Mutex
	config            *CryptoConfig
	pageLoadTimestamp int64
	sequenceValue     int
	windowPropsLength int
}

// NewSessionManager creates a new SessionManager
func NewSessionManager(config *CryptoConfig) *SessionManager {
	return &SessionManager{
		config:            config,
		pageLoadTimestamp: time.Now().UnixMilli(),
		sequenceValue:     config.SessionSequenceInitMin + rand.Intn(config.SessionSequenceInitMax-config.SessionSequenceInitMin),
		windowPropsLength: config.SessionWindowPropsInitMin + rand.Intn(config.SessionWindowPropsInitMax-config.SessionWindowPropsInitMin),
	}
}

// UpdateState updates the session state to simulate user activity between requests
func (sm *SessionManager) UpdateState() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.updateState()
}

func (sm *SessionManager) updateState() {
	sm.sequenceValue += sm.config.SessionSequenceStepMin + rand.Intn(sm.config.SessionSequenceStepMax-sm.config.SessionSequenceStepMin)
	sm.windowPropsLength += sm.config.SessionWindowPropsStepMin + rand.Intn(sm.config.SessionWindowPropsStepMax-sm.config.SessionWindowPropsStepMin)
}

// GetCurrentState returns the current signing state for a request
func (sm *SessionManager) GetCurrentState(uri string) *SignState {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.updateState()
	return &SignState{
		PageLoadTimestamp: sm.pageLoadTimestamp,
		SequenceValue:     sm.sequenceValue,
		WindowPropsLength: sm.windowPropsLength,
		URILength:         len(uri),
	}
}
