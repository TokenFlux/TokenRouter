package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	openaicore "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type liveHTTPUpstreamStub struct {
	request    *http.Request
	body       []byte
	tlsProfile *tlsfingerprint.Profile
}

type liveAttestationStub struct {
	header string
	err    error
}

// newLiveTLSRoutingServices 构造同时覆盖 TLS 模板和身份头的 Live 路由规则。
func newLiveTLSRoutingServices() (*provider.TLSProfiles, *egress.TLSFingerprintRouterService) {
	profileService := provider.NewTLSProfiles(egress.NewTLSFingerprintProfileService(&liveProfileStore{values: []*egress.TLSFingerprintProfile{{ID: 20, Name: "live-routed"}}}, nil))
	profileService.Start()

	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "live-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Name:                    "live-client",
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "test-live-client",
			TLSFingerprintProfileID: 20,
			UpstreamUserAgent:       "codex_vscode/0.144.1 live-test",
			UpstreamOriginator:      "codex_vscode",
		}},
	}
	routerService := egress.NewTLSFingerprintRouterService(&liveRouterStore{values: []*egress.TLSFingerprintRouter{router}}, nil)
	routerService.Start()

	return profileService, routerService
}

func (s liveAttestationStub) Check(context.Context) error {
	return s.err
}

func (s liveAttestationStub) Generate(context.Context) (string, error) {
	return s.header, s.err
}

func (s *liveHTTPUpstreamStub) Do(
	request *http.Request,
	_ string,
	_ int64,
	_ int,
) (*http.Response, error) {
	s.request = request
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	s.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Location": {"/backend-api/codex/call_test"},
		},
		Body: io.NopCloser(strings.NewReader("v=0\r\n")),
	}, nil
}

func (s *liveHTTPUpstreamStub) DoWithTLS(
	request *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	profile *tlsfingerprint.Profile,
) (*http.Response, error) {
	s.tlsProfile = profile
	return s.Do(request, proxyURL, accountID, accountConcurrency)
}

func TestLiveCapabilityOnlyAllowsOpenAIOAuth(t *testing.T) {
	require.True(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}), accountcore.OpenAIEndpointCapabilityLive))
	require.False(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}), accountcore.OpenAIEndpointCapabilityLive))
	require.False(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}), accountcore.OpenAIEndpointCapabilityLive))
	require.False(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			accountcore.OpenAIAuthModeCredentialKey: accountcore.OpenAIAuthModePersonalAccessToken,
		}},
	}), accountcore.OpenAIEndpointCapabilityLive))
	require.False(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			accountcore.OpenAIAuthModeCredentialKey: accountcore.OpenAIAuthModeAgentIdentity,
		}},
	}), accountcore.OpenAIEndpointCapabilityLive))
}

func TestValidateLiveCallRequestDoesNotRequireDelegation(t *testing.T) {
	request := &gatewaysession.LiveCallRequest{
		SDP:     "v=0\r\n",
		Session: json.RawMessage(`{"model":"gpt-live-test","instructions":"hello"}`),
	}
	require.NoError(t, openaicore.ValidateLiveCallRequest(request))
	require.NotContains(t, string(request.Session), "delegation")
}

func TestCreateUpstreamLiveCallPreservesSession(t *testing.T) {
	upstream := &liveHTTPUpstreamStub{}
	profileService, routerService := newLiveTLSRoutingServices()
	service := newLiveFixture(liveFixtureInputs{transport: upstream, profiles: profileService, routers: routerService})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 2,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acct_test",
		},
		Extra: map[string]any{
			"enable_tls_fingerprint":    true,
			"tls_fingerprint_router_id": int64(9),
		}},
	}
	session := json.RawMessage(`{
		"model":"gpt-live-test",
		"delegation":{"type":"client"},
		"custom":{"keep":true}
	}`)

	tlsRouterMatch := service.matchLiveTLSFingerprintRouter(account, "test-live-client")
	created, err := service.createUpstreamLiveCall(context.Background(), account, &gatewaysession.LiveCallRequest{
		SDP:     "v=offer\r\n",
		Session: session,
	}, `{"v":1,"s":0,"t":"v1.test"}`, tlsRouterMatch)
	require.NoError(t, err)
	require.Equal(t, "call_test", created.CallID)
	require.Equal(t, []byte("v=0\r\n"), created.SDP)
	require.NotNil(t, upstream.tlsProfile)
	require.Equal(t, "live-routed", upstream.tlsProfile.Name)

	var forwarded struct {
		SDP     string          `json:"sdp"`
		Session json.RawMessage `json:"session"`
	}
	require.NoError(t, json.Unmarshal(upstream.body, &forwarded))
	require.Equal(t, "v=offer\r\n", forwarded.SDP)
	require.JSONEq(t, string(session), string(forwarded.Session))
	require.Equal(t, "Bearer test-access-token", upstream.request.Header.Get("Authorization"))
	require.Equal(t, "acct_test", upstream.request.Header.Get("Chatgpt-Account-Id"))
	require.Equal(t, "quicksilver=v2", upstream.request.Header.Get("OpenAI-Alpha"))
	require.Equal(t, "codex_vscode/0.144.1 live-test", upstream.request.Header.Get("User-Agent"))
	require.Equal(t, "codex_vscode", upstream.request.Header.Get("Originator"))
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, upstream.request.Header.Get(openai.LiveAttestationHeader))
	require.NotEmpty(t, upstream.request.Header.Get("Session-Id"))
	require.NotEmpty(t, upstream.request.Header.Get("Thread-Id"))
	require.Empty(t, upstream.request.Header.Get("OpenAI-Beta"))
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.request.Context()))
	require.True(t, upstreamcore.HTTPUpstreamRedirectsDisabled(upstream.request.Context()))
}

func TestLiveClientPolicyUsesTLSRouterMatch(t *testing.T) {
	_, routerService := newLiveTLSRoutingServices()
	service := newLiveFixture(liveFixtureInputs{routers: routerService})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Extra: map[string]any{
			"tls_fingerprint_router_id":  int64(9),
			"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
		}},
	}

	matched := service.matchLiveTLSFingerprintRouter(account, "test-live-client")
	result := service.liveClientPolicyResult(
		context.Background(),
		account,
		gatewaysession.LiveCallIdentity{UserAgent: "test-live-client"},
		matched,
	)
	require.True(t, result.Enabled)
	require.True(t, result.Matched)

	notMatched := service.matchLiveTLSFingerprintRouter(account, "unknown-client")
	result = service.liveClientPolicyResult(
		context.Background(),
		account,
		gatewaysession.LiveCallIdentity{UserAgent: "unknown-client"},
		notMatched,
	)
	require.True(t, result.Enabled)
	require.False(t, result.Matched)
	require.Equal(t, accountcore.CodexClientRestrictionReasonNotMatchedTLSRouter, result.Reason)
}

func TestLiveAttestationCipherRoundTripAndRejectsOtherInstanceKey(t *testing.T) {
	first := openai.NewLiveAttestationCipher("first-live-secret")
	second := openai.NewLiveAttestationCipher("second-live-secret")
	require.NotNil(t, first)
	require.NotNil(t, second)

	ciphertext, err := first.Encrypt(`{"v":1,"s":0,"t":"v1.opaque"}`)
	require.NoError(t, err)
	require.NotContains(t, ciphertext, "opaque")

	plaintext, err := first.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.opaque"}`, plaintext)

	_, err = second.Decrypt(ciphertext)
	require.Error(t, err)
}

func TestPrepareLiveAttestationEncryptsHeaderAndReturnsExplicitProviderError(t *testing.T) {
	cipher := openai.NewLiveAttestationCipher("live-attestation-test-secret")
	service := newLiveFixture(liveFixtureInputs{attestation: liveAttestationStub{header: `{"v":1,"s":0,"t":"v1.test"}`}, cipher: cipher})
	header, ciphertext, err := service.prepareLiveAttestation(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, header)
	require.NotContains(t, ciphertext, "v1.test")
	decrypted, err := cipher.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, header, decrypted)

	service.Attestation = liveAttestationStub{err: errors.New("macOS app missing")}
	_, _, err = service.prepareLiveAttestation(context.Background())
	var unavailable *gatewaysession.LiveAttestationUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Contains(t, unavailable.Error(), "macOS app missing")
}

func TestLiveMaxSessionDurationDefaultsAndOverrides(t *testing.T) {
	require.Equal(t, defaultLiveMaxSessionDuration, (newLiveFixture(liveFixtureInputs{})).liveMaxSessionDuration())
	require.Equal(
		t,
		90*time.Second,
		(newLiveFixture(liveFixtureInputs{duration: time.Duration(90) * time.Second})).liveMaxSessionDuration(),
	)
}

func TestLiveSidebandNormalCloseEndsCall(t *testing.T) {
	normalClose := coderws.CloseError{Code: coderws.StatusNormalClosure}
	require.ErrorIs(t, liveSidebandReadError(normalClose), gatewaysession.ErrLiveCallNotFound)

	abnormalClose := coderws.CloseError{Code: coderws.StatusInternalError}
	require.Equal(t, abnormalClose, liveSidebandReadError(abnormalClose))
}

func TestLiveCreateFailoverUsesExistingOpenAIPolicy(t *testing.T) {
	service := newLiveFixture(liveFixtureInputs{})
	require.False(t, service.shouldFailoverLiveCreateError(&forwardcore.UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"message":"invalid session"}}`),
	}))
	require.True(t, service.shouldFailoverLiveCreateError(&forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusForbidden,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(&forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(errors.New("transport failed")))
}

func TestLiveCallIDFromLocation(t *testing.T) {
	callID, err := openai.LiveCallIDFromLocation("https://chatgpt.com/backend-api/codex/call_123?intent=quicksilver")
	require.NoError(t, err)
	require.Equal(t, "call_123", callID)

	callID, err = openai.LiveCallIDFromLocation("/backend-api/codex/call_456")
	require.NoError(t, err)
	require.Equal(t, "call_456", callID)
}

func TestRequestTypeLive(t *testing.T) {
	require.True(t, usage.RequestTypeLive.IsValid())
	require.Equal(t, "live", usage.RequestTypeLive.String())
	parsed, err := usage.ParseUsageRequestType("live")
	require.NoError(t, err)
	require.Equal(t, usage.RequestTypeLive, parsed)
}
