// Gemini 授权会话保留三十分钟 TTL 和显式启停，状态归账号拥有者。
package account

import (
	"sync"
	"time"
)

const GeminiAuthorizationSessionTTL = 30 * time.Minute

type GeminiOAuthSession struct {
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier"`
	ProxyURL     string `json:"proxy_url,omitempty"`
	RedirectURI  string `json:"redirect_uri"`
	ProjectID    string `json:"project_id,omitempty"`
	// TierID is a user-selected fallback tier.
	// For oauth types that support auto detection (google_one/code_assist), the server will prefer
	// the detected tier and fall back to TierID when detection fails.
	TierID    string    `json:"tier_id,omitempty"`
	OAuthType string    `json:"oauth_type"` // "code_assist" 或 "ai_studio"
	CreatedAt time.Time `json:"created_at"`
}
type GeminiAuthorizationSessions struct {
	runtimeMu      sync.Mutex
	runtimeStarted bool
	runtimeStopped bool
	runtimeWG      sync.WaitGroup

	mu       sync.RWMutex
	sessions map[string]*GeminiOAuthSession
	stopCh   chan struct{}
}

func NewGeminiAuthorizationSessions() *GeminiAuthorizationSessions {
	store := &GeminiAuthorizationSessions{
		sessions: make(map[string]*GeminiOAuthSession),
		stopCh:   make(chan struct{}),
	}

	return store
}
func (s *GeminiAuthorizationSessions) Set(sessionID string, session *GeminiOAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}
func (s *GeminiAuthorizationSessions) Get(sessionID string) (*GeminiOAuthSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if time.Since(session.CreatedAt) > GeminiAuthorizationSessionTTL {
		return nil, false
	}
	return session, true
}
func (s *GeminiAuthorizationSessions) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// Start 显式启动当前会话实例的清理循环。
func (s *GeminiAuthorizationSessions) Start() {
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
func (s *GeminiAuthorizationSessions) Stop() {
	s.runtimeMu.Lock()
	if !s.runtimeStopped {
		s.runtimeStopped = true
		close(s.stopCh)
	}
	s.runtimeMu.Unlock()
	s.runtimeWG.Wait()
}
func (s *GeminiAuthorizationSessions) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > GeminiAuthorizationSessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
