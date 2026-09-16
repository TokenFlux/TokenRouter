// Grok 授权会话与一次性消费归账号；供应商交换不持有 Redis 或本地回退状态。
package account

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const GrokSessionTTL = 30 * time.Minute

type GrokSessionBackend interface {
	Set(context.Context, string, any) error
	Get(context.Context, string, any) (bool, error)
	Delete(context.Context, string) error
	TryConsume(context.Context, string) (bool, error)
}

// GrokOAuthSession 保存一次 PKCE OAuth 授权流程状态。
type GrokOAuthSession struct {
	State         string    `json:"state"`
	CodeVerifier  string    `json:"code_verifier"`
	CodeChallenge string    `json:"code_challenge"`
	ClientID      string    `json:"client_id,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	ProxyURL      string    `json:"proxy_url,omitempty"`
	RedirectURI   string    `json:"redirect_uri"`
	CreatedAt     time.Time `json:"created_at"`

	mu       sync.Mutex
	consumed bool
}

// TryConsume 保证进程内回退会话也只能被消费一次。
func (s *GrokOAuthSession) TryConsume() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumed {
		return false
	}
	s.consumed = true
	return true
}

// GrokSessionStore 以 Redis 共享 xAI OAuth 会话，并在 Redis 写入失败时使用进程内回退。
type GrokSessionStore struct {
	runtimeMu      sync.Mutex
	runtimeStarted bool
	runtimeStopped bool
	runtimeWG      sync.WaitGroup

	mu        sync.RWMutex
	sessions  map[string]*GrokOAuthSession
	localOnly map[string]struct{}

	stopCh chan struct{}
	remote GrokSessionBackend
}
type grokSessionDTO struct {
	State         string    `json:"state"`
	CodeVerifier  string    `json:"code_verifier"`
	CodeChallenge string    `json:"code_challenge"`
	ClientID      string    `json:"client_id,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	ProxyURL      string    `json:"proxy_url,omitempty"`
	RedirectURI   string    `json:"redirect_uri"`
	CreatedAt     time.Time `json:"created_at"`
}

func NewGrokSessionStore(remote GrokSessionBackend) *GrokSessionStore {
	store := &GrokSessionStore{
		remote: remote,

		sessions: make(map[string]*GrokOAuthSession),

		localOnly: make(map[string]struct{}),

		stopCh: make(chan struct{}),
	}

	return store
}
func (s *GrokSessionStore) Set(sessionID string, session *GrokOAuthSession) {
	if session == nil {
		return
	}
	var remoteErr error
	if s != nil && s.remote != nil {
		remoteErr = s.remote.Set(context.Background(), sessionID, grokSessionDTO{

			State:         session.State,
			CodeVerifier:  session.CodeVerifier,
			CodeChallenge: session.CodeChallenge,

			ClientID: session.ClientID,
			Scope:    session.Scope,
			ProxyURL: session.ProxyURL,

			RedirectURI: session.RedirectURI,
			CreatedAt:   session.CreatedAt,
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
	if remoteErr != nil {
		s.localOnly[sessionID] = struct{}{}
		slog.Warn("xai oauth session Redis write failed; using process-local fallback", "error", remoteErr)
	} else {
		delete(s.localOnly, sessionID)
	}
}
func (s *GrokSessionStore) Get(sessionID string) (*GrokOAuthSession, bool) {
	if s.isLocalOnly(sessionID) {
		return s.getMemory(sessionID)
	}
	if s != nil && s.remote != nil {
		var dto grokSessionDTO
		ok, err := s.remote.Get(context.Background(), sessionID, &dto)
		if err != nil || !ok || time.Since(dto.CreatedAt) > GrokSessionTTL {
			return nil, false
		}
		session := &GrokOAuthSession{

			State:         dto.State,
			CodeVerifier:  dto.CodeVerifier,
			CodeChallenge: dto.CodeChallenge,

			ClientID: dto.ClientID,
			Scope:    dto.Scope,
			ProxyURL: dto.ProxyURL,

			RedirectURI: dto.RedirectURI,
			CreatedAt:   dto.CreatedAt,
		}
		s.mu.Lock()
		s.sessions[sessionID] = session
		s.mu.Unlock()
		return session, true
	}
	return s.getMemory(sessionID)
}
func (s *GrokSessionStore) getMemory(sessionID string) (*GrokOAuthSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if time.Since(session.CreatedAt) > GrokSessionTTL {
		return nil, false
	}
	return session, true
}
func (s *GrokSessionStore) Delete(sessionID string) {
	if s != nil && s.remote != nil {
		_ = s.remote.Delete(context.Background(), sessionID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	delete(s.localOnly, sessionID)
}
func (s *GrokSessionStore) TryConsumeSession(sessionID string) bool {
	if s == nil {
		return false
	}
	if s.isLocalOnly(sessionID) {
		return s.tryConsumeMemory(sessionID)
	}
	if s.remote != nil {
		ok, err := s.remote.TryConsume(context.Background(), sessionID)
		return err == nil && ok
	}
	return s.tryConsumeMemory(sessionID)
}
func (s *GrokSessionStore) isLocalOnly(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.localOnly[sessionID]
	return ok
}
func (s *GrokSessionStore) tryConsumeMemory(sessionID string) bool {
	session, ok := s.getMemory(sessionID)
	return ok && session.TryConsume()
}

// Start 显式启动当前会话实例的清理循环。
func (s *GrokSessionStore) Start() {
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
func (s *GrokSessionStore) Stop() {
	s.runtimeMu.Lock()
	if !s.runtimeStopped {
		s.runtimeStopped = true
		close(s.stopCh)
	}
	s.runtimeMu.Unlock()
	s.runtimeWG.Wait()
}
func (s *GrokSessionStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > GrokSessionTTL {
					delete(s.sessions, id)
					delete(s.localOnly, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
