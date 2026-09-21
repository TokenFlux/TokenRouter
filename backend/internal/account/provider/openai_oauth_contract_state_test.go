package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

type openaiOAuthClientStateStub struct {
	exchangeCalled int32
	lastClientID   string
	lastOptions    []openai.OAuthTokenRequestOptions
}

func (s *openaiOAuthClientStateStub) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string, options ...openai.OAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.exchangeCalled, 1)
	s.lastClientID = clientID
	s.lastOptions = append([]openai.OAuthTokenRequestOptions(nil), options...)
	return &openai.TokenResponse{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresIn:    3600,
	}, nil
}

func (s *openaiOAuthClientStateStub) RefreshToken(ctx context.Context, refreshToken, proxyURL string, options ...openai.OAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientStateStub) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string, options ...openai.OAuthTokenRequestOptions) (*openai.TokenResponse, error) {
	return s.RefreshToken(ctx, refreshToken, proxyURL)
}

func TestOpenAIOAuthService_ExchangeCode_StateRequired(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := newOpenAIAuthorizationForTest(t, nil, client)
	svc.Start()
	defer stopOpenAIAuthorizationForTest(t, svc)

	svc.Sessions.Set("sid", &accountcore.OpenAIOAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &accountcore.OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "oauth state is required")
	require.Equal(t, int32(0), atomic.LoadInt32(&client.exchangeCalled))
}

func TestOpenAIOAuthService_ExchangeCode_StateMismatch(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := newOpenAIAuthorizationForTest(t, nil, client)
	svc.Start()
	defer stopOpenAIAuthorizationForTest(t, svc)

	svc.Sessions.Set("sid", &accountcore.OpenAIOAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &accountcore.OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
		State:     "wrong-state",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid oauth state")
	require.Equal(t, int32(0), atomic.LoadInt32(&client.exchangeCalled))
}

func TestOpenAIOAuthService_ExchangeCode_StateMatch(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := newOpenAIAuthorizationForTest(t, nil, client)
	svc.Start()
	defer stopOpenAIAuthorizationForTest(t, svc)

	svc.Sessions.Set("sid", &accountcore.OpenAIOAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	info, err := svc.ExchangeCode(context.Background(), &accountcore.OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
		State:     "expected-state",
	})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "at", info.AccessToken)
	require.Equal(t, openai.ClientID, info.ClientID)
	require.Equal(t, openai.ClientID, client.lastClientID)
	require.Equal(t, int32(1), atomic.LoadInt32(&client.exchangeCalled))

	_, ok := svc.Sessions.Get("sid")
	require.False(t, ok)
}

func TestOpenAIOAuthService_ExchangeCode_UsesRequestTLSRouterConfig(t *testing.T) {
	profile := &tlsfingerprint.Profile{Name: "exchange-profile"}
	profileID := int64(42)
	client := &openaiOAuthClientStateStub{}
	deps := &OpenAIAuthorizationDependencies{}
	svc := newOpenAIAuthorizationForTest(t, nil, client, deps)
	svc.Start()
	defer stopOpenAIAuthorizationForTest(t, svc)
	deps.Routers = &openAIOAuthTokenRouterReaderStub{routers: map[int64]*egress.TLSFingerprintRouter{
		7: {
			ID:                                       7,
			Enabled:                                  true,
			ChatGPTOAuthTokenUserAgent:               " Exchange UA ",
			ChatGPTOAuthTokenTLSFingerprintProfileID: &profileID,
		},
	}}
	deps.Profiles = &openAIOAuthTokenProfileResolverStub{profiles: map[int64]*tlsfingerprint.Profile{42: profile}}

	svc.Sessions.Set("sid", &accountcore.OpenAIOAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	routerID := int64(7)
	info, err := svc.ExchangeCode(context.Background(), &accountcore.OpenAIExchangeCodeInput{
		SessionID:              "sid",
		Code:                   "auth-code",
		State:                  "expected-state",
		TLSFingerprintRouterID: &routerID,
	})

	require.NoError(t, err)
	require.NotNil(t, info)
	require.Len(t, client.lastOptions, 1)
	require.Equal(t, "Exchange UA", client.lastOptions[0].UserAgent)
	require.Same(t, profile, client.lastOptions[0].TLSProfile)
}
