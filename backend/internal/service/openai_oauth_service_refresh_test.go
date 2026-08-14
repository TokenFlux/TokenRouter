package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/model"
	"github.com/BrandonVee/TokenRouter/internal/pkg/openai"
	"github.com/BrandonVee/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type openaiOAuthClientRefreshStub struct {
	refreshCalls int32
	lastOptions  []OpenAIOAuthTokenRequestOptions
	lastClientID string
}

func (s *openaiOAuthClientRefreshStub) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string, options ...OpenAIOAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientRefreshStub) RefreshToken(ctx context.Context, refreshToken, proxyURL string, options ...OpenAIOAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	s.lastOptions = append([]OpenAIOAuthTokenRequestOptions(nil), options...)
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientRefreshStub) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string, options ...OpenAIOAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	s.lastClientID = clientID
	s.lastOptions = append([]OpenAIOAuthTokenRequestOptions(nil), options...)
	return &openai.TokenResponse{AccessToken: "new-at", RefreshToken: "new-rt", ExpiresIn: 3600}, nil
}

type openAIOAuthTokenRouterReaderStub struct {
	routers map[int64]*model.TLSFingerprintRouter
}

func (s *openAIOAuthTokenRouterReaderStub) GetRuntimeRouter(routerID int64) *model.TLSFingerprintRouter {
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

func (s *openAIOAuthSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *openAIOAuthSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
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
	svc := NewOpenAIOAuthService(nil, client)
	var privacyClientCalls int32
	svc.SetPrivacyClientFactory(func(proxyURL string) (*req.Client, error) {
		atomic.AddInt32(&privacyClientCalls, 1)
		return nil, errors.New("stop before request")
	})

	expiresAt := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	account := &Account{
		ID:       77,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
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

func TestOpenAITokenRefresher_NeedsRefresh_SkipsAccountWithoutRefreshToken(t *testing.T) {
	refresher := NewOpenAITokenRefresher(nil, nil)
	expiresAt := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)

	withoutRT := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "access-token",
			"expires_at":   expiresAt,
		},
	}
	require.False(t, refresher.NeedsRefresh(withoutRT, 5*time.Minute))

	withRT := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiresAt,
		},
	}
	require.True(t, refresher.NeedsRefresh(withRT, 5*time.Minute))
}

func TestOpenAITokenProvider_NoRefreshTokenExpiredAccessTokenReturnsError(t *testing.T) {
	provider := NewOpenAITokenProvider(nil, nil, nil)
	expiresAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "expired-access-token",
			"expires_at":   expiresAt,
		},
	}

	token, err := provider.GetAccessToken(context.Background(), account)
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "refresh_token is missing")
}

func TestOpenAIOAuthService_RefreshAccountToken_UsesAccountTLSRouterConfig(t *testing.T) {
	profile := &tlsfingerprint.Profile{Name: "profile-55"}
	profileID := int64(55)
	client := &openaiOAuthClientRefreshStub{}
	svc := NewOpenAIOAuthService(nil, client)
	svc.SetTokenTLSRouterDeps(nil, &openAIOAuthTokenRouterReaderStub{routers: map[int64]*model.TLSFingerprintRouter{
		9: {
			ID:                                       9,
			Enabled:                                  true,
			ChatGPTOAuthTokenUserAgent:               " Token UA ",
			ChatGPTOAuthTokenTLSFingerprintProfileID: &profileID,
		},
	}}, &openAIOAuthTokenProfileResolverStub{profiles: map[int64]*tlsfingerprint.Profile{55: profile}})

	account := &Account{
		ID:          77,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
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
	svc := NewOpenAIOAuthService(nil, client)
	svc.SetTokenTLSRouterDeps(nil, &openAIOAuthTokenRouterReaderStub{routers: map[int64]*model.TLSFingerprintRouter{
		9: {
			ID:      9,
			Enabled: true,
		},
	}}, nil)

	account := &Account{
		ID:       77,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
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
	svc := NewOpenAIOAuthService(nil, client)
	settingService := NewSettingService(&openAIOAuthSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexUserAgent: " codex-custom ",
	}}, nil)
	svc.SetTokenTLSRouterDeps(settingService, &openAIOAuthTokenRouterReaderStub{routers: map[int64]*model.TLSFingerprintRouter{
		10: {
			ID:                                       10,
			Enabled:                                  true,
			ChatGPTOAuthTokenTLSFingerprintProfileID: &profileID,
		},
	}}, &openAIOAuthTokenProfileResolverStub{profiles: map[int64]*tlsfingerprint.Profile{
		0: {Name: "built-in"},
	}})

	routerID := int64(10)
	_, err := svc.RefreshTokenWithClientIDAndRouter(context.Background(), "rt", "", "client-id", &routerID)
	require.NoError(t, err)
	require.Len(t, client.lastOptions, 1)
	require.Equal(t, "codex-custom", client.lastOptions[0].UserAgent)
	require.Equal(t, "built-in", client.lastOptions[0].TLSProfile.Name)
}
