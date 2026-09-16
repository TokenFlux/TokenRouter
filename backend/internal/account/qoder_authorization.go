// 本文件拥有 Qoder 账号授权会话、完成认领与凭据投影，供应商交换通过端口执行。
package account

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

const QoderOAuthSessionTTL = 10 * time.Minute
const QoderOAuthPollInterval = 2

type QoderAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	ExpiresIn int64  `json:"expires_in"`
	Interval  int    `json:"interval"`
	Site      string `json:"site"`
}
type QoderExchangeCodeInput struct {
	SessionID   string
	State       string
	Code        string
	CallbackURL string
	ProxyID     *int64
}
type QoderTokenInfo struct {
	SecurityOauthToken string         `json:"security_oauth_token"`
	RefreshToken       string         `json:"refresh_token,omitempty"`
	MachineID          string         `json:"machine_id"`
	MachineToken       string         `json:"machine_token,omitempty"`
	MachineType        string         `json:"machine_type,omitempty"`
	UID                string         `json:"uid,omitempty"`
	AID                string         `json:"aid,omitempty"`
	OrganizationID     string         `json:"organization_id,omitempty"`
	OrganizationName   string         `json:"organization_name,omitempty"`
	Name               string         `json:"name,omitempty"`
	UserType           string         `json:"user_type,omitempty"`
	Site               string         `json:"site"`
	RefreshMode        string         `json:"refresh_mode"`
	ExpiresAt          string         `json:"expires_at,omitempty"`
	Extra              map[string]any `json:"extra,omitempty"`
}
type QoderPollResult struct {
	Status    string          `json:"status"`
	TokenInfo *QoderTokenInfo `json:"token_info,omitempty"`
}
type QoderAuthorizationSession[F any] struct {
	State              string
	Flow               F
	CreatedAt          time.Time
	Completing         bool
	CompleteCh         chan struct{}
	CompletedTokenInfo *QoderTokenInfo
}
type QoderAuthorizationStore[F any] struct {
	runtimeMu      sync.Mutex
	runtimeStarted bool
	runtimeStopped bool
	runtimeWG      sync.WaitGroup

	mu       sync.RWMutex
	sessions map[string]*QoderAuthorizationSession[F]
	stopCh   chan struct{}
}

func NewQoderAuthorizationStore[F any]() *QoderAuthorizationStore[F] {
	store := &QoderAuthorizationStore[F]{
		sessions: make(map[string]*QoderAuthorizationSession[F]),
		stopCh:   make(chan struct{}),
	}

	return store
}
func (s *QoderAuthorizationStore[F]) Set(sessionID string, session *QoderAuthorizationSession[F]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}
func (s *QoderAuthorizationStore[F]) Get(sessionID string) (*QoderAuthorizationSession[F], bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if time.Since(session.CreatedAt) > QoderOAuthSessionTTL {
		return nil, false
	}
	return session, true
}
func (s *QoderAuthorizationStore[F]) BeginCompletion(sessionID, state string) (*QoderAuthorizationSession[F], *QoderTokenInfo, <-chan struct{}, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil, nil, errors.New("qoder oauth session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok || time.Since(session.CreatedAt) > QoderOAuthSessionTTL {
		if ok {
			delete(s.sessions, sessionID)
		}
		return nil, nil, nil, errors.New("qoder oauth session not found or expired")
	}
	if strings.TrimSpace(state) == "" || strings.TrimSpace(state) != session.State {
		return nil, nil, nil, errors.New("qoder oauth state is invalid")
	}
	if session.CompletedTokenInfo != nil {
		return session, session.CompletedTokenInfo, nil, nil
	}
	if session.Completing {
		if session.CompleteCh == nil {
			session.CompleteCh = make(chan struct{})
		}
		return nil, nil, session.CompleteCh, nil
	}
	session.Completing = true
	session.CompleteCh = make(chan struct{})
	return session, nil, nil, nil
}
func (s *QoderAuthorizationStore[F]) FinishCompletion(sessionID string, tokenInfo *QoderTokenInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return
	}
	if tokenInfo != nil {
		session.CompletedTokenInfo = tokenInfo
	}
	session.Completing = false
	if session.CompleteCh != nil {
		close(session.CompleteCh)
		session.CompleteCh = nil
	}
}

// Start 显式启动当前会话实例的清理循环。
func (s *QoderAuthorizationStore[F]) Start() {
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
func (s *QoderAuthorizationStore[F]) Stop() {
	s.runtimeMu.Lock()
	if !s.runtimeStopped {
		s.runtimeStopped = true
		close(s.stopCh)
	}
	s.runtimeMu.Unlock()
	s.runtimeWG.Wait()
}
func (s *QoderAuthorizationStore[F]) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > QoderOAuthSessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

type QoderAuthorization[F any] struct {
	Store    *QoderAuthorizationStore[F]
	Prepare  func(context.Context, string, *int64) (string, *QoderAuthorizationSession[F], *QoderAuthURLResult, error)
	Complete func(context.Context, F) (*QoderTokenInfo, bool, error)
	activity operationActivity
}

func (s *QoderAuthorization[F]) GenerateAuthURLForSite(ctx context.Context, site string, proxyID *int64) (*QoderAuthURLResult, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("qoder authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	id, session, result, err := s.Prepare(operation, site, proxyID)
	if err != nil {
		return nil, err
	}
	s.Store.Set(id, session)
	return result, nil
}
func (s *QoderAuthorization[F]) Start() { s.Store.Start() }
func (s *QoderAuthorization[F]) StopContext(ctx context.Context) error {
	s.Store.Stop()
	return s.activity.stop(ctx, "qoder authorization")
}
func (s *QoderAuthorization[F]) ExchangeCode(ctx context.Context, input *QoderExchangeCodeInput) (*QoderTokenInfo, error) {
	if input == nil {
		return nil, errors.New("qoder oauth input is required")
	}
	if err := normalizeQoderExchangeInput(input); err != nil {
		return nil, err
	}
	tokenInfo, pending, err := s.completeSession(ctx, input.SessionID, input.State, input.ProxyID)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, errors.New("qoder authorization is still pending; finish authorization in the browser and try again")
	}
	return tokenInfo, nil
}
func (s *QoderAuthorization[F]) Poll(ctx context.Context, sessionID, state string, proxyID *int64) (*QoderPollResult, error) {
	tokenInfo, pending, err := s.completeSession(ctx, sessionID, state, proxyID)
	if err != nil {
		return nil, err
	}
	if pending {
		return &QoderPollResult{Status: "pending"}, nil
	}
	return &QoderPollResult{
		Status:    "completed",
		TokenInfo: tokenInfo,
	}, nil
}
func (s *QoderAuthorization[F]) completeSession(ctx context.Context, sessionID, state string, proxyID *int64) (*QoderTokenInfo, bool, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("qoder authorization is stopped"))
	if err != nil {
		return nil, false, err
	}
	defer done()
	ctx = operation
	// proxyID 仅为旧客户端兼容字段；OAuth 会话必须使用创建时冻结的代理。
	_ = proxyID
	for {
		session, cachedTokenInfo, waitCh, err := s.Store.BeginCompletion(sessionID, state)
		if err != nil {
			return nil, false, err
		}
		if cachedTokenInfo != nil {
			return cachedTokenInfo, false, nil
		}
		if waitCh != nil {
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case <-waitCh:
				continue
			}
		}
		tokenInfo, pending, err := s.Complete(ctx, session.Flow)
		if err != nil || pending {
			s.Store.FinishCompletion(sessionID, nil)
			return nil, pending, err
		}
		s.Store.FinishCompletion(sessionID, tokenInfo)
		return tokenInfo, false, nil
	}
}
func normalizeQoderExchangeInput(input *QoderExchangeCodeInput) error {
	if input == nil {
		return errors.New("qoder oauth input is required")
	}
	callbackState, callbackCode := ParseQoderCallback(input.CallbackURL)
	if strings.TrimSpace(input.State) == "" && callbackState != "" {
		input.State = callbackState
	}
	if strings.TrimSpace(input.Code) == "" && callbackCode != "" {
		input.Code = callbackCode
	}
	if callbackState != "" && strings.TrimSpace(input.State) != "" && callbackState != strings.TrimSpace(input.State) {
		return errors.New("qoder oauth callback state does not match request state")
	}
	return nil
}
func ParseQoderCallback(raw string) (state string, code string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	u, err := url.Parse(raw)
	if err == nil && u != nil {
		values := u.Query()
		if u.Fragment != "" {
			if fragmentValues, fragmentErr := url.ParseQuery(u.Fragment); fragmentErr == nil {
				for key, vals := range fragmentValues {
					if len(vals) > 0 && values.Get(key) == "" {
						values.Set(key, vals[0])
					}
				}
			}
		}
		state = strings.TrimSpace(values.Get("state"))
		code = strings.TrimSpace(values.Get("code"))
		if state != "" || code != "" {
			return state, code
		}
	}
	if strings.Contains(raw, "=") {
		values, err := url.ParseQuery(strings.TrimPrefix(raw, "?"))
		if err == nil {
			return strings.TrimSpace(values.Get("state")), strings.TrimSpace(values.Get("code"))
		}
	}
	return "", raw
}
func (s *QoderAuthorization[F]) BuildAccountCredentials(tokenInfo *QoderTokenInfo) map[string]any {
	credentials := map[string]any{}
	if tokenInfo == nil {
		return credentials
	}
	if tokenInfo.SecurityOauthToken != "" {
		credentials["security_oauth_token"] = tokenInfo.SecurityOauthToken
	}
	if tokenInfo.RefreshToken != "" {
		credentials["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.MachineID != "" {
		credentials["machine_id"] = tokenInfo.MachineID
	}
	if tokenInfo.MachineToken != "" {
		credentials["machine_token"] = tokenInfo.MachineToken
	}
	if tokenInfo.MachineType != "" {
		credentials["machine_type"] = tokenInfo.MachineType
	}
	if tokenInfo.UID != "" {
		credentials["uid"] = tokenInfo.UID
	}
	if tokenInfo.AID != "" {
		credentials["aid"] = tokenInfo.AID
	}
	if tokenInfo.OrganizationID != "" {
		credentials["organization_id"] = tokenInfo.OrganizationID
	}
	if tokenInfo.OrganizationName != "" {
		credentials["organization_name"] = tokenInfo.OrganizationName
	}
	if tokenInfo.Name != "" {
		credentials["name"] = tokenInfo.Name
	}
	if tokenInfo.UserType != "" {
		credentials["user_type"] = tokenInfo.UserType
	}
	if tokenInfo.Site != "" {
		credentials["site"] = tokenInfo.Site
	}
	if tokenInfo.RefreshMode != "" {
		credentials["refresh_mode"] = tokenInfo.RefreshMode
	}
	if tokenInfo.ExpiresAt != "" {
		credentials["expires_at"] = tokenInfo.ExpiresAt
	}
	if len(tokenInfo.Extra) > 0 {
		credentials["extra"] = tokenInfo.Extra
	}
	return credentials
}
