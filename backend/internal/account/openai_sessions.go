// OpenAI 授权会话由账号模块持有，保留原进程内 TTL、清理和生命周期。
package account

import (
	"sync"
	"time"
)

const OpenAISessionTTL = 30 * time.Minute

// OpenAIOAuthSession stores OAuth flow state for OpenAI
type OpenAIOAuthSession struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"code_verifier"`
	ClientID     string    `json:"client_id,omitempty"`
	ProxyURL     string    `json:"proxy_url,omitempty"`
	RedirectURI  string    `json:"redirect_uri"`
	CreatedAt    time.Time `json:"created_at"`
}

// OpenAISessionStore manages OAuth sessions in memory
type OpenAISessionStore struct {
	runtimeMu      sync.Mutex
	runtimeStarted bool
	runtimeStopped bool
	runtimeWG      sync.WaitGroup

	mu       sync.RWMutex
	sessions map[string]*OpenAIOAuthSession

	stopCh chan struct{}
}

// NewOpenAISessionStore creates a new session store
func NewOpenAISessionStore() *OpenAISessionStore {
	store := &OpenAISessionStore{
		sessions: make(map[string]*OpenAIOAuthSession),
		stopCh:   make(chan struct{}),
	}
	// Start cleanup goroutine

	return store
}

// Set stores a session
func (s *OpenAISessionStore) Set(sessionID string, session *OpenAIOAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}

// Get retrieves a session
func (s *OpenAISessionStore) Get(sessionID string) (*OpenAIOAuthSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	// Check if expired
	if time.Since(session.CreatedAt) > OpenAISessionTTL {
		return nil, false
	}
	return session, true
}

// Delete removes a session
func (s *OpenAISessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// Stop stops the cleanup goroutine
// Start 显式启动当前会话实例的清理循环。
func (s *OpenAISessionStore) Start() {
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
func (s *OpenAISessionStore) Stop() {
	s.runtimeMu.Lock()
	if !s.runtimeStopped {
		s.runtimeStopped = true
		close(s.stopCh)
	}
	s.runtimeMu.Unlock()
	s.runtimeWG.Wait()
}
func (s *OpenAISessionStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > OpenAISessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
