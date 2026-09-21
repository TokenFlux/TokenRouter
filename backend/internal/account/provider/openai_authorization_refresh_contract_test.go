package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	accountauth "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type openaiOAuthClientRefreshStub struct {
	refreshCalls int32
	lastOptions  []openai.OAuthTokenRequestOptions
	lastClientID string
}

func (s *openaiOAuthClientRefreshStub) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string, options ...openai.OAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientRefreshStub) RefreshToken(ctx context.Context, refreshToken, proxyURL string, options ...openai.OAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	s.lastOptions = append([]openai.OAuthTokenRequestOptions(nil), options...)
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientRefreshStub) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string, options ...openai.OAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	s.lastClientID = clientID
	s.lastOptions = append([]openai.OAuthTokenRequestOptions(nil), options...)
	return &openai.TokenResponse{AccessToken: "new-at", RefreshToken: "new-rt", ExpiresIn: 3600}, nil
}

type openAIOAuthTokenRouterReaderStub struct {
	routers map[int64]*egress.TLSFingerprintRouter
}

func (s *openAIOAuthTokenRouterReaderStub) GetRuntimeRouter(routerID int64) *egress.TLSFingerprintRouter {
	if s == nil {
		return nil
	}
	return s.routers[routerID]
}

type openAIOAuthTokenProfileResolverStub struct {
	profiles map[int64]*tlsfingerprint.Profile
}

func (s *openAIOAuthTokenProfileResolverStub) ResolveTokenTLSProfileByID(id int64) (*tlsfingerprint.Profile, bool) {
	if s == nil {
		return nil, false
	}
	profile, ok := s.profiles[id]
	return profile, ok
}

type openAIOAuthSettingRepoStub struct {
	values map[string]string
}

func (s *openAIOAuthSettingRepoStub) Get(context.Context, string) (*settings.Setting, error) {
	panic("unexpected Get call")
}

func (s *openAIOAuthSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", settings.ErrSettingNotFound
}

func (s *openAIOAuthSettingRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *openAIOAuthSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *openAIOAuthSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *openAIOAuthSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *openAIOAuthSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func TestOpenAIOAuthService_RefreshAccountToken_NoRefreshTokenUsesExistingAccessToken(t *testing.T) {
	client := &openaiOAuthClientRefreshStub{}
	deps := &OpenAIAuthorizationDependencies{}
	svc := newOpenAIAuthorizationForTest(t, nil, client, deps)
	svc.Start()
	var privacyClientCalls int32
	deps.PrivacyFactory = func(proxyURL string) (*req.Client, error) {
		atomic.AddInt32(&privacyClientCalls, 1)
		return nil, errors.New("stop before request")
	}

	expiresAt := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	account := &accountauth.Record{
		ID:       77,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "existing-access-token",
			"expires_at":   expiresAt,
			"client_id":    "client-id-1",
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "existing-access-token", info.AccessToken)
	require.Equal(t, "client-id-1", info.ClientID)
	require.Zero(t, atomic.LoadInt32(&client.refreshCalls), "已有 access token 应该复用，不能调用 refresh")
	require.Positive(t, atomic.LoadInt32(&privacyClientCalls), "已有 access token 也应该执行账号信息补全")
}

func TestOpenAIOAuthService_RefreshAccountToken_UsesAccountTLSRouterConfig(t *testing.T) {
	profile := &tlsfingerprint.Profile{Name: "profile-55"}
	profileID := int64(55)
	client := &openaiOAuthClientRefreshStub{}
	deps := &OpenAIAuthorizationDependencies{}
	svc := newOpenAIAuthorizationForTest(t, nil, client, deps)
	svc.Start()
	deps.Routers = &openAIOAuthTokenRouterReaderStub{routers: map[int64]*egress.TLSFingerprintRouter{
		9: {
			ID:                                       9,
			Enabled:                                  true,
			ChatGPTOAuthTokenUserAgent:               " Token UA ",
			ChatGPTOAuthTokenTLSFingerprintProfileID: &profileID,
		},
	}}
	deps.Profiles = &openAIOAuthTokenProfileResolverStub{profiles: map[int64]*tlsfingerprint.Profile{55: profile}}

	account := &accountauth.Record{
		ID:          77,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 3,
		Credentials: map[string]any{
			"refresh_token": "old-rt",
			"client_id":     "client-id",
		},
		Extra: map[string]any{
			"tls_fingerprint_router_id": int64(9),
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "new-at", info.AccessToken)
	require.Equal(t, "client-id", client.lastClientID)
	require.Len(t, client.lastOptions, 1)
	require.Equal(t, "Token UA", client.lastOptions[0].UserAgent)
	require.Same(t, profile, client.lastOptions[0].TLSProfile)
	require.Equal(t, int64(77), client.lastOptions[0].AccountID)
	require.Equal(t, 3, client.lastOptions[0].AccountConcurrency)
}

func TestOpenAIOAuthService_RefreshAccountToken_EmptyRouterTokenConfigKeepsOldPath(t *testing.T) {
	client := &openaiOAuthClientRefreshStub{}
	deps := &OpenAIAuthorizationDependencies{}
	svc := newOpenAIAuthorizationForTest(t, nil, client, deps)
	svc.Start()
	deps.Routers = &openAIOAuthTokenRouterReaderStub{routers: map[int64]*egress.TLSFingerprintRouter{
		9: {
			ID:      9,
			Enabled: true,
		},
	}}
	deps.Profiles = nil

	account := &accountauth.Record{
		ID:       77,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "old-rt",
			"client_id":     "client-id",
		},
		Extra: map[string]any{
			"tls_fingerprint_router_id": int64(9),
		},
	}

	_, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, client.lastOptions)
}

func TestOpenAIOAuthService_RefreshTokenWithClientIDAndRouter_UsesCodexUAFallbackWhenTLSProfileConfigured(t *testing.T) {
	profileID := int64(0)
	client := &openaiOAuthClientRefreshStub{}
	deps := &OpenAIAuthorizationDependencies{}
	svc := newOpenAIAuthorizationForTest(t, nil, client, deps)
	svc.Start()
	settingService := gateway.NewRuntimeSettings(&openAIOAuthSettingRepoStub{values: map[string]string{
		gateway.SettingKeyOpenAICodexUserAgent: " codex-custom ",
	}}, settings.ErrSettingNotFound, nil)
	deps.Routers = &openAIOAuthTokenRouterReaderStub{routers: map[int64]*egress.TLSFingerprintRouter{
		10: {
			ID:                                       10,
			Enabled:                                  true,
			ChatGPTOAuthTokenTLSFingerprintProfileID: &profileID,
		},
	}}
	deps.Profiles = &openAIOAuthTokenProfileResolverStub{profiles: map[int64]*tlsfingerprint.Profile{
		0: {Name: "built-in"},
	}}
	deps.CodexUserAgent = settingService.GetOpenAICodexUserAgent

	routerID := int64(10)
	_, err := svc.RefreshTokenWithClientIDAndRouter(context.Background(), "rt", "", "client-id", &routerID)
	require.NoError(t, err)
	require.Len(t, client.lastOptions, 1)
	require.Equal(t, "codex-custom", client.lastOptions[0].UserAgent)
	require.Equal(t, "built-in", client.lastOptions[0].TLSProfile.Name)
}
