//go:build unit

package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

func successfulOpenAIAutomaticProbeResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`)),
	}
}

func TestAccountTestService_AutomaticOpenAIProbeUsesMatchedRoute(t *testing.T) {
	account := accountcore.Record{
		ID:          1001,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
			"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Name:                    "probe-client",
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "probe-client/1.0",
			TLSFingerprintProfileID: 20,
			UpstreamUserAgent:       "codex_vscode/0.144.1 probe-terminal",
			UpstreamOriginator:      "codex_vscode",
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t,
		[]accountcore.Record{account},
		upstream,
		router,
		map[int64]*egress.TLSFingerprintProfile{
			10: {ID: 10, Name: "fixed"},
			20: {ID: 20, Name: "routed"},
		},
		nil,
	)

	result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-5.4", "hi", "probe-client/1.0")

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "routed", upstream.lastTLSProfile.Name)
	require.Equal(t, "codex_vscode/0.144.1 probe-terminal", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "codex_vscode", upstream.lastReq.Header.Get("Originator"))
}

func TestAccountTestService_AutomaticOpenAIProbeRejectsClientPolicyLocally(t *testing.T) {
	tests := []struct {
		name       string
		policy     string
		userAgent  string
		runDefault bool
		reason     string
		router     *egress.TLSFingerprintRouter
		extra      map[string]any
	}{
		{
			name:      "TLS 路由未命中",
			policy:    accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			userAgent: "curl/8.0",
			reason:    accountcore.CodexClientRestrictionReasonNotMatchedTLSRouter,
			router: &egress.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []egress.TLSFingerprintRouterRule{{
					Enabled:   true,
					MatchType: egress.TLSRouterMatchPrefix,
					Pattern:   "allowed-client/",
				}},
			},
			extra: map[string]any{"tls_fingerprint_router_id": int64(9)},
		},
		{
			name:      "Codex 官方身份未命中",
			policy:    accountcore.OpenAIOAuthClientPolicyCodexOnly,
			userAgent: "custom-client/1.0",
			reason:    accountcore.CodexClientRestrictionReasonNotMatchedUA,
		},
		{
			name:       "定时测试执行相同策略",
			policy:     accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			runDefault: true,
			reason:     accountcore.CodexClientRestrictionReasonNotMatchedTLSRouter,
			router: &egress.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []egress.TLSFingerprintRouterRule{{
					Enabled:   true,
					MatchType: egress.TLSRouterMatchPrefix,
					Pattern:   "different-client/",
				}},
			},
			extra: map[string]any{"tls_fingerprint_router_id": int64(9)},
		},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extra := map[string]any{"openai_oauth_client_policy": test.policy}
			for key, value := range test.extra {
				extra[key] = value
			}
			account := accountcore.Record{
				ID:       int64(1100 + i),
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
				Extra:    extra,
			}
			upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
			svc := newOpenAIAutomaticProbeTestService(t, []accountcore.Record{account}, upstream, test.router, nil, nil)

			var result *accountcore.ScheduledTestResult
			var err error
			if test.runDefault {
				result, err = svc.RunTestBackground(context.Background(), account.ID, "gpt-5.4")
			} else {
				result, err = svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-5.4", "hi", test.userAgent)
			}

			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Contains(t, result.ErrorMessage, "policy="+test.policy)
			require.Contains(t, result.ErrorMessage, "reason="+test.reason)
			require.Empty(t, upstream.requests)
		})
	}
}

func TestAccountTestService_AutomaticOpenAIProbeFallsBackToAccountTLSProfile(t *testing.T) {
	account := accountcore.Record{
		ID:          1201,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
			"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyAny,
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "other-client/1.0",
			TLSFingerprintProfileID: 20,
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t,
		[]accountcore.Record{account},
		upstream,
		router,
		map[int64]*egress.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}, 20: {ID: 20, Name: "unused-route"}},
		nil,
	)

	result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-5.4", "hi", "unmatched-client/1.0")

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "fixed", upstream.lastTLSProfile.Name)
}

func TestAccountTestService_AutomaticOpenAIProbeEmptyUserAgentParticipatesInRouting(t *testing.T) {
	parentID := int64(1301)
	parent := accountcore.Record{
		ID:       parentID,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "parent-token",
			"user_agent":   "codex_vscode/0.144.1 parent-terminal",
		},
	}
	shadow := accountcore.Record{
		ID:              1302,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  accountcore.QuotaDimensionSpark,
		Concurrency:     1,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	defaultAccount := accountcore.Record{
		ID:          1303,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "default-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}

	tests := []struct {
		name            string
		accounts        []accountcore.Record
		accountID       int64
		pattern         string
		routeProfileID  int64
		expectedUA      string
		expectedProfile string
	}{
		{
			name:            "凭据账号自定义 UA",
			accounts:        []accountcore.Record{parent, shadow},
			accountID:       shadow.ID,
			pattern:         "codex_vscode/0.144.1 parent-terminal",
			routeProfileID:  20,
			expectedUA:      "codex_vscode/0.144.1 parent-terminal",
			expectedProfile: "routed",
		},
		{
			name:            "内置 Codex UA",
			accounts:        []accountcore.Record{defaultAccount},
			accountID:       defaultAccount.ID,
			pattern:         openai.CodexCLIUserAgent,
			routeProfileID:  0,
			expectedUA:      openai.CodexCLIUserAgent,
			expectedProfile: "Built-in Default (Node.js 24.x)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := &egress.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []egress.TLSFingerprintRouterRule{{
					Enabled:                 true,
					MatchType:               egress.TLSRouterMatchExact,
					Pattern:                 test.pattern,
					TLSFingerprintProfileID: test.routeProfileID,
				}},
			}
			upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
			svc := newOpenAIAutomaticProbeTestService(t,
				test.accounts,
				upstream,
				router,
				map[int64]*egress.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}, 20: {ID: 20, Name: "routed"}},
				nil,
			)

			result, err := svc.RunTestBackground(context.Background(), test.accountID, "gpt-5.4")

			require.NoError(t, err)
			require.Equal(t, "success", result.Status)
			require.Equal(t, test.expectedUA, upstream.lastReq.Header.Get("User-Agent"))
			require.Equal(t, test.expectedProfile, upstream.lastTLSProfile.Name)
		})
	}
}

func TestAccountTestService_AutomaticOpenAIProbeMissingRouteProfileFallsBack(t *testing.T) {
	account := accountcore.Record{
		ID:          1401,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "route-client/1.0",
			TLSFingerprintProfileID: 404,
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t,
		[]accountcore.Record{account},
		upstream,
		router,
		map[int64]*egress.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}},
		nil,
	)

	result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-5.4", "hi", "route-client/1.0")

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "fixed", upstream.lastTLSProfile.Name)
}

func TestAccountTestService_AutomaticOpenAIProbeRoutesChatCompletionsAndImages(t *testing.T) {
	t.Run("Chat Completions", func(t *testing.T) {
		account := accountcore.Record{
			ID:          1501,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://compat-upstream.example/v1",
			},
			Extra: map[string]any{
				accountcore.ExtraKeyTextRouteMode: string(accountcore.TextRouteModeForceChatCompletions),
				"tls_fingerprint_router_id":       int64(9),
			},
		}
		router := &egress.TLSFingerprintRouter{
			ID:      9,
			Name:    "probe-router",
			Enabled: true,
			Rules: []egress.TLSFingerprintRouterRule{{
				Enabled:           true,
				MatchType:         egress.TLSRouterMatchExact,
				Pattern:           "chat-client/1.0",
				UpstreamUserAgent: "chat-upstream/2.0",
			}},
		}
		upstream := &openAIProbeTransport{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
		}}
		cfg := &egress.OperatorURLPolicy{}
		svc := newOpenAIAutomaticProbeTestService(t, []accountcore.Record{account}, upstream, router, nil, cfg)

		result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-5.4", "hi", "chat-client/1.0")

		require.NoError(t, err)
		require.Equal(t, "success", result.Status)
		require.Equal(t, "chat-upstream/2.0", upstream.lastReq.Header.Get("User-Agent"))
		require.Contains(t, upstream.lastReq.URL.Path, "/chat/completions")
	})

	t.Run("OAuth 图片", func(t *testing.T) {
		account := accountcore.Record{
			ID:          1502,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{"access_token": "test-token"},
			Extra: map[string]any{
				"enable_tls_fingerprint":    true,
				"tls_fingerprint_router_id": int64(9),
			},
		}
		router := &egress.TLSFingerprintRouter{
			ID:      9,
			Name:    "probe-router",
			Enabled: true,
			Rules: []egress.TLSFingerprintRouterRule{{
				Enabled:                 true,
				MatchType:               egress.TLSRouterMatchExact,
				Pattern:                 "image-client/1.0",
				TLSFingerprintProfileID: 20,
				UpstreamUserAgent:       "codex-tui/0.144.1 image-terminal",
				UpstreamOriginator:      "codex-tui",
			}},
		}
		upstream := &openAIProbeTransport{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\",\"output_format\":\"png\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		}}
		svc := newOpenAIAutomaticProbeTestService(t,
			[]accountcore.Record{account},
			upstream,
			router,
			map[int64]*egress.TLSFingerprintProfile{20: {ID: 20, Name: "image-route"}},
			nil,
		)

		result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-image-2", "draw", "image-client/1.0")

		require.NoError(t, err)
		require.Equal(t, "success", result.Status)
		require.Equal(t, "image-route", upstream.lastTLSProfile.Name)
		require.Equal(t, "codex-tui/0.144.1 image-terminal", upstream.lastReq.Header.Get("User-Agent"))
		require.Equal(t, "codex-tui", upstream.lastReq.Header.Get("Originator"))
	})
}

func TestAccountTestService_ManualOpenAITestDoesNotEnforceAutomaticPolicy(t *testing.T) {
	account := accountcore.Record{
		ID:          1601,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"openai_oauth_client_policy": accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Enabled:   true,
			MatchType: egress.TLSRouterMatchExact,
			Pattern:   "never-matched-by-manual-test",
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t, []accountcore.Record{account}, upstream, router, nil, nil)
	c, _ := newTestContext()

	err := svc.Test(c.Request.Context(), accountcore.TestRequest{AccountID: account.ID, Model: "gpt-5.4", Prompt: "hi", Mode: accountcore.AccountTestModeDefault}, NewTestEventSink(c.recorder))

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
}

// 构造真实 TLS 缓存、Router 和账号测试，不保留旧网关服务替身。
func newOpenAIAutomaticProbeTestService(t *testing.T, values []accountcore.Record, transport *openAIProbeTransport, router *egress.TLSFingerprintRouter, profiles map[int64]*egress.TLSFingerprintProfile, urlPolicy *egress.OperatorURLPolicy) *accountcore.TestService {
	t.Helper()
	profileStore := &automaticProbeProfileStore{}
	for _, value := range profiles {
		profileStore.values = append(profileStore.values, value)
	}
	profileService := egressprovider.NewTLSProfiles(egress.NewTLSFingerprintProfileService(profileStore, nil))
	profileService.Start()
	t.Cleanup(profileService.Stop)
	records := openAIProbeRecords{accountsByID: make(map[int64]*accountcore.Record, len(values))}
	for i := range values {
		records.accountsByID[values[i].ID] = &values[i]
	}
	store := &openAIProbeStore{openAIProbeRecords: records}
	policy := &accountprovider.OpenAIProbePolicy{Available: true, Read: store.GetByID, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Profiles: profileService, ManualProfiles: profileService}
	if router != nil {
		routers := egress.NewTLSFingerprintRouterService(&automaticProbeRouterStore{values: []*egress.TLSFingerprintRouter{router}}, nil)
		routers.Start()
		t.Cleanup(routers.Stop)
		policy.Routers = routers
	}
	executor := &accountprovider.OpenAIAccountTest{Store: store, Transport: transport, Prepare: policy.Prepare, ApplyRouting: policy.ApplyTestRouting, ResolveTLS: policy.ResolveTestTLS, ValidateURL: func(raw string) (string, error) {
		if urlPolicy == nil {
			return "", errors.New("config is not available")
		}
		return urlPolicy.Validate(raw)
	}}
	return openAIProbeCore(executor)
}

type automaticProbeProfileStore struct {
	egress.TLSFingerprintProfileRepository
	values []*egress.TLSFingerprintProfile
}

func (s *automaticProbeProfileStore) List(context.Context) ([]*egress.TLSFingerprintProfile, error) {
	return s.values, nil
}

type automaticProbeRouterStore struct {
	egress.TLSFingerprintRouterRepository
	values []*egress.TLSFingerprintRouter
}

func (s *automaticProbeRouterStore) List(context.Context) ([]*egress.TLSFingerprintRouter, error) {
	return s.values, nil
}
