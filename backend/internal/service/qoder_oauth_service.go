package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
)

const (
	qoderOAuthSessionTTL     = 10 * time.Minute
	qoderOAuthPollInterval   = 2
	qoderOAuthDefaultTimeout = 20 * time.Second
)

type qoderOAuthClient interface {
	PollDeviceToken(ctx context.Context, nonce, verifier string) (*qoder.DeviceTokenResponse, bool, error)
	GetUserInfo(ctx context.Context, token string) (*qoder.UserInfo, error)
	GetOrganizationTags(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error)
}

type qoderOAuthClientFactory func(proxyURL string) (qoderOAuthClient, error)

type qoderOAuthSession struct {
	State              string
	Nonce              string
	CodeVerifier       string
	Machine            *qoder.MachineIdentity
	AuthURL            string
	ProxyURL           string
	CreatedAt          time.Time
	Completing         bool
	CompleteCh         chan struct{}
	CompletedTokenInfo *QoderTokenInfo
}

type qoderOAuthSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*qoderOAuthSession
	stopCh   chan struct{}
}

func newQoderOAuthSessionStore() *qoderOAuthSessionStore {
	store := &qoderOAuthSessionStore{
		sessions: make(map[string]*qoderOAuthSession),
		stopCh:   make(chan struct{}),
	}
	go store.cleanup()
	return store
}

func (s *qoderOAuthSessionStore) Set(sessionID string, session *qoderOAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}

func (s *qoderOAuthSessionStore) Get(sessionID string) (*qoderOAuthSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if time.Since(session.CreatedAt) > qoderOAuthSessionTTL {
		return nil, false
	}
	return session, true
}

func (s *qoderOAuthSessionStore) BeginCompletion(sessionID, state string) (*qoderOAuthSession, *QoderTokenInfo, <-chan struct{}, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil, nil, errors.New("qoder oauth session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok || time.Since(session.CreatedAt) > qoderOAuthSessionTTL {
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

func (s *qoderOAuthSessionStore) FinishCompletion(sessionID string, tokenInfo *QoderTokenInfo) {
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

func (s *qoderOAuthSessionStore) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
		close(s.stopCh)
	}
}

func (s *qoderOAuthSessionStore) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > qoderOAuthSessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

type QoderOAuthService struct {
	sessionStore  *qoderOAuthSessionStore
	proxyRepo     ProxyRepository
	clientFactory qoderOAuthClientFactory
}

func NewQoderOAuthService(proxyRepo ProxyRepository) *QoderOAuthService {
	svc := &QoderOAuthService{
		sessionStore: newQoderOAuthSessionStore(),
		proxyRepo:    proxyRepo,
	}
	svc.clientFactory = svc.defaultClientFactory
	return svc
}

type QoderAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	ExpiresIn int64  `json:"expires_in"`
	Interval  int    `json:"interval"`
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
	Extra              map[string]any `json:"extra,omitempty"`
}

func (s *QoderOAuthService) GenerateAuthURL(ctx context.Context, proxyID *int64) (*QoderAuthURLResult, error) {
	req, err := qoder.NewDeviceAuthRequest()
	if err != nil {
		return nil, fmt.Errorf("generate qoder device auth request: %w", err)
	}
	sessionID := qoder.RandomHex(32)
	state := qoder.RandomToken(32)

	proxyURL, err := s.resolveProxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}

	session := &qoderOAuthSession{
		State:        state,
		Nonce:        req.Nonce,
		CodeVerifier: req.CodeVerifier,
		Machine: &qoder.MachineIdentity{
			MachineID:    req.MachineID,
			MachineToken: qoder.RandomToken(50),
			MachineType:  qoder.RandomHex(18),
		},
		AuthURL:   req.AuthorizationURL(),
		ProxyURL:  proxyURL,
		CreatedAt: time.Now(),
	}
	s.sessionStore.Set(sessionID, session)

	return &QoderAuthURLResult{
		AuthURL:   session.AuthURL,
		SessionID: sessionID,
		State:     state,
		ExpiresIn: int64(qoderOAuthSessionTTL / time.Second),
		Interval:  qoderOAuthPollInterval,
	}, nil
}

func (s *QoderOAuthService) ExchangeCode(ctx context.Context, input *QoderExchangeCodeInput) (*QoderTokenInfo, error) {
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

func (s *QoderOAuthService) Poll(ctx context.Context, sessionID, state string, proxyID *int64) (*QoderPollResult, error) {
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

type QoderPollResult struct {
	Status    string          `json:"status"`
	TokenInfo *QoderTokenInfo `json:"token_info,omitempty"`
}

func (s *QoderOAuthService) completeSession(ctx context.Context, sessionID, state string, proxyID *int64) (*QoderTokenInfo, bool, error) {
	for {
		session, cachedTokenInfo, waitCh, err := s.sessionStore.BeginCompletion(sessionID, state)
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
		tokenInfo, pending, err := s.completeSessionOnce(ctx, session, proxyID)
		if err != nil || pending {
			s.sessionStore.FinishCompletion(sessionID, nil)
			return nil, pending, err
		}
		s.sessionStore.FinishCompletion(sessionID, tokenInfo)
		return tokenInfo, false, nil
	}
}

func (s *QoderOAuthService) completeSessionOnce(ctx context.Context, session *qoderOAuthSession, proxyID *int64) (*QoderTokenInfo, bool, error) {
	proxyURL := session.ProxyURL
	if proxyID != nil {
		resolvedProxyURL, err := s.resolveProxyURL(ctx, proxyID)
		if err != nil {
			return nil, false, err
		}
		proxyURL = resolvedProxyURL
	}
	client, err := s.clientFactory(proxyURL)
	if err != nil {
		return nil, false, err
	}

	tokenResp, ready, err := client.PollDeviceToken(ctx, session.Nonce, session.CodeVerifier)
	if err != nil {
		return nil, false, err
	}
	if !ready {
		return nil, true, nil
	}

	accessToken := tokenResp.AccessTokenValue()
	userInfo, userErr := client.GetUserInfo(ctx, accessToken)
	if userErr != nil {
		userInfo = &qoder.UserInfo{ID: tokenResp.UserID}
	}
	identity := qoder.BuildIdentityFromDeviceToken(userInfo, tokenResp)
	orgErr := populateQoderOrganization(ctx, client, accessToken, identity)
	return buildQoderTokenInfo(identity, session.Machine, userErr, orgErr), false, nil
}

func normalizeQoderExchangeInput(input *QoderExchangeCodeInput) error {
	if input == nil {
		return errors.New("qoder oauth input is required")
	}
	callbackState, callbackCode := parseQoderCallback(input.CallbackURL)
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

func parseQoderCallback(raw string) (state string, code string) {
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

func (s *QoderOAuthService) resolveProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", errors.New("proxy repository is not configured")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		return "", fmt.Errorf("get proxy: %w", err)
	}
	if proxy == nil {
		return "", errors.New("proxy not found")
	}
	return proxy.URL(), nil
}

func (s *QoderOAuthService) defaultClientFactory(proxyURL string) (qoderOAuthClient, error) {
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL: strings.TrimSpace(proxyURL),
		Timeout:  qoderOAuthDefaultTimeout,
	})
	if err != nil {
		return nil, err
	}
	return qoder.NewOAuthClient(qoder.OpenAPIBaseURL, client), nil
}

func populateQoderOrganization(ctx context.Context, client qoderOAuthClient, token string, identity *qoder.AuthIdentity) error {
	if client == nil || identity == nil {
		return nil
	}
	if strings.TrimSpace(identity.OrganizationID) != "" {
		return nil
	}
	uid := strings.TrimSpace(identity.UID)
	if uid == "" {
		uid = strings.TrimSpace(identity.AID)
	}
	if uid == "" {
		return nil
	}
	tags, err := client.GetOrganizationTags(ctx, token, uid)
	if err != nil {
		return err
	}
	if tags == nil {
		return nil
	}
	identity.OrganizationID = strings.TrimSpace(tags.OrganizationID)
	identity.OrganizationName = strings.TrimSpace(tags.OrganizationName)
	return nil
}

func buildQoderTokenInfo(identity *qoder.AuthIdentity, machine *qoder.MachineIdentity, userErr error, orgErr error) *QoderTokenInfo {
	if identity == nil {
		identity = &qoder.AuthIdentity{UserType: "personal_standard"}
	}
	if machine == nil {
		machine = qoder.NewMachine()
	}
	extra := map[string]any{}
	if userErr != nil {
		extra["userinfo_warning"] = sanitizedQoderOAuthWarning("userinfo_unavailable", "Qoder user info could not be loaded")
	}
	if orgErr != nil {
		extra["organization_warning"] = sanitizedQoderOAuthWarning("organization_unavailable", "Qoder organization info could not be loaded")
	}
	if len(extra) == 0 {
		extra = nil
	}
	return &QoderTokenInfo{
		SecurityOauthToken: strings.TrimSpace(identity.SecurityOauthToken),
		RefreshToken:       strings.TrimSpace(identity.RefreshToken),
		MachineID:          strings.TrimSpace(machine.MachineID),
		MachineToken:       strings.TrimSpace(machine.MachineToken),
		MachineType:        strings.TrimSpace(machine.MachineType),
		UID:                strings.TrimSpace(identity.UID),
		AID:                strings.TrimSpace(identity.AID),
		OrganizationID:     strings.TrimSpace(identity.OrganizationID),
		OrganizationName:   strings.TrimSpace(identity.OrganizationName),
		Name:               strings.TrimSpace(identity.Name),
		UserType:           firstNonEmptyQoder(identity.UserType, "personal_standard"),
		Extra:              extra,
	}
}

func (s *QoderOAuthService) BuildAccountCredentials(tokenInfo *QoderTokenInfo) map[string]any {
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
	if len(tokenInfo.Extra) > 0 {
		credentials["extra"] = tokenInfo.Extra
	}
	return credentials
}

func sanitizedQoderOAuthWarning(code, message string) map[string]string {
	return map[string]string{
		"code":    code,
		"message": message,
	}
}

func (s *QoderOAuthService) Stop() {
	if s != nil && s.sessionStore != nil {
		s.sessionStore.Stop()
	}
}
