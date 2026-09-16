// Antigravity 授权会话唯一归账号拥有，构造不启动原五分钟清理循环。
package account

import (
	"sync"
	"time"
)

const AntigravitySessionTTL = 30 * time.Minute

// AntigravityAuthorizationSession 保存 OAuth 授权流程的临时状态
type AntigravityAuthorizationSession struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"code_verifier"`
	ProxyURL     string    `json:"proxy_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// AntigravityAuthorizationSessions OAuth session 存储
type AntigravityAuthorizationSessions struct {
	runtimeMu      sync.Mutex
	runtimeStarted bool
	runtimeStopped bool
	runtimeWG      sync.WaitGroup

	mu       sync.RWMutex
	sessions map[string]*AntigravityAuthorizationSession
	stopCh   chan struct{}
}

func NewAntigravityAuthorizationSessions() *AntigravityAuthorizationSessions {
	store := &AntigravityAuthorizationSessions{
		sessions: make(map[string]*AntigravityAuthorizationSession),
		stopCh:   make(chan struct{}),
	}

	return store
}
func (s *AntigravityAuthorizationSessions) Set(sessionID string, session *AntigravityAuthorizationSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}
func (s *AntigravityAuthorizationSessions) Get(sessionID string) (*AntigravityAuthorizationSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if time.Since(session.CreatedAt) > AntigravitySessionTTL {
		return nil, false
	}
	return session, true
}
func (s *AntigravityAuthorizationSessions) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// Start 显式启动当前会话实例的清理循环。
func (s *AntigravityAuthorizationSessions) Start() {
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
func (s *AntigravityAuthorizationSessions) Stop() {
	s.runtimeMu.Lock()
	if !s.runtimeStopped {
		s.runtimeStopped = true
		close(s.stopCh)
	}
	s.runtimeMu.Unlock()
	s.runtimeWG.Wait()
}
func (s *AntigravityAuthorizationSessions) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > AntigravitySessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
