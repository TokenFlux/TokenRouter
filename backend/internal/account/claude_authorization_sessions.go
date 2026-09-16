// 本文件拥有 Claude 账号授权会话，平台协议不拥有登录会话状态。
package account

import (
	"sync"
	"time"
)

const ClaudeAuthorizationSessionTTL = 30 * time.Minute

type ClaudeAuthorizationSession struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"code_verifier"`
	Scope        string    `json:"scope"`
	ProxyURL     string    `json:"proxy_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ClaudeAuthorizationSessions manages OAuth sessions in memory
type ClaudeAuthorizationSessions struct {
	runtimeMu      sync.Mutex
	runtimeStarted bool
	runtimeStopped bool
	runtimeWG      sync.WaitGroup

	mu       sync.RWMutex
	sessions map[string]*ClaudeAuthorizationSession

	stopCh chan struct{}
}

// NewClaudeAuthorizationSessions creates a new session store
func NewClaudeAuthorizationSessions() *ClaudeAuthorizationSessions {
	store := &ClaudeAuthorizationSessions{
		sessions: make(map[string]*ClaudeAuthorizationSession),
		stopCh:   make(chan struct{}),
	}

	return store
}

// Stop stops the cleanup goroutine
// Start 显式启动当前会话实例的清理循环。
func (s *ClaudeAuthorizationSessions) Start() {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeStarted || s.runtimeStopped {
		return
	}
	s.runtimeStarted = true
	s.runtimeWG.Add(1)
	go func() { defer s.runtimeWG.Done(); s.cleanup() }()
}

// Stop 幂等停止并等待清理循环，未启动实例也可安全关闭。
func (s *ClaudeAuthorizationSessions) Stop() {
	s.runtimeMu.Lock()
	if !s.runtimeStopped {
		s.runtimeStopped = true
		close(s.stopCh)
	}
	s.runtimeMu.Unlock()
	s.runtimeWG.Wait()
}
func (s *ClaudeAuthorizationSessions) Set(sessionID string, session *ClaudeAuthorizationSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}

// Get retrieves a session
func (s *ClaudeAuthorizationSessions) Get(sessionID string) (*ClaudeAuthorizationSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if time.Since(session.CreatedAt) > ClaudeAuthorizationSessionTTL {
		return nil, false
	}
	return session, true
}

// Delete removes a session
func (s *ClaudeAuthorizationSessions) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// cleanup removes expired sessions periodically
func (s *ClaudeAuthorizationSessions) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > ClaudeAuthorizationSessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
