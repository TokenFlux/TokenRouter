//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/model"
	"github.com/BrandonVee/TokenRouter/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func newOpenAIAutomaticProbeTestService(
	accounts []Account,
	upstream *httpUpstreamRecorder,
	router *model.TLSFingerprintRouter,
	profiles map[int64]*model.TLSFingerprintProfile,
	cfg *config.Config,
) *AccountTestService {
	profileService := &TLSFingerprintProfileService{localCache: profiles}
	var routerService *TLSFingerprintRouterService
	if router != nil {
		routerService = &TLSFingerprintRouterService{
			localCache: map[int64]*cachedTLSFingerprintRouter{
				router.ID: newCachedTLSFingerprintRouter(router),
			},
		}
	}
	gateway := &OpenAIGatewayService{
		cfg:                 cfg,
		tlsFPProfileService: profileService,
		tlsFPRouterService:  routerService,
	}
	return &AccountTestService{
		accountRepo:          &snapshotUpdateAccountRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: accounts}},
		httpUpstream:         upstream,
		cfg:                  cfg,
		tlsFPProfileService:  profileService,
		openAIGatewayService: gateway,
	}
}

func successfulOpenAIAutomaticProbeResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`)),
	}
}

func TestAccountTestService_AutomaticOpenAIProbeUsesMatchedRoute(t *testing.T) {
	account := Account{
		ID:          1001,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
			"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
		},
	}
	router := &model.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []model.TLSFingerprintRouterRule{{
			Name:                    "probe-client",
			Enabled:                 true,
			MatchType:               model.TLSRouterMatchExact,
			Pattern:                 "probe-client/1.0",
			TLSFingerprintProfileID: 20,
			UpstreamUserAgent:       "codex_vscode/0.144.1 probe-terminal",
			UpstreamOriginator:      "codex_vscode",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(
		[]Account{account},
		upstream,
		router,
		map[int64]*model.TLSFingerprintProfile{
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
		router     *model.TLSFingerprintRouter
		extra      map[string]any
	}{
		{
			name:      "TLS 路由未命中",
			policy:    OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			userAgent: "curl/8.0",
			reason:    CodexClientRestrictionReasonNotMatchedTLSRouter,
			router: &model.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []model.TLSFingerprintRouterRule{{
					Enabled:   true,
					MatchType: model.TLSRouterMatchPrefix,
					Pattern:   "allowed-client/",
				}},
			},
			extra: map[string]any{"tls_fingerprint_router_id": int64(9)},
		},
		{
			name:      "Codex 官方身份未命中",
			policy:    OpenAIOAuthClientPolicyCodexOnly,
			userAgent: "custom-client/1.0",
			reason:    CodexClientRestrictionReasonNotMatchedUA,
		},
		{
			name:       "定时测试执行相同策略",
			policy:     OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			runDefault: true,
			reason:     CodexClientRestrictionReasonNotMatchedTLSRouter,
			router: &model.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []model.TLSFingerprintRouterRule{{
					Enabled:   true,
					MatchType: model.TLSRouterMatchPrefix,
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
			account := Account{
				ID:       int64(1100 + i),
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Extra:    extra,
			}
			upstream := &httpUpstreamRecorder{resp: successfulOpenAIAutomaticProbeResponse()}
			svc := newOpenAIAutomaticProbeTestService([]Account{account}, upstream, test.router, nil, nil)

			var result *ScheduledTestResult
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
	account := Account{
		ID:          1201,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
			"openai_oauth_client_policy": OpenAIOAuthClientPolicyAny,
		},
	}
	router := &model.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []model.TLSFingerprintRouterRule{{
			Enabled:                 true,
			MatchType:               model.TLSRouterMatchExact,
			Pattern:                 "other-client/1.0",
			TLSFingerprintProfileID: 20,
		}},
	}
	upstream := &httpUpstreamRecorder{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(
		[]Account{account},
		upstream,
		router,
		map[int64]*model.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}, 20: {ID: 20, Name: "unused-route"}},
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
	parent := Account{
		ID:       parentID,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "parent-token",
			"user_agent":   "codex_vscode/0.144.1 parent-terminal",
		},
	}
	shadow := Account{
		ID:              1302,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Concurrency:     1,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	defaultAccount := Account{
		ID:          1303,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
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
		accounts        []Account
		accountID       int64
		pattern         string
		routeProfileID  int64
		expectedUA      string
		expectedProfile string
	}{
		{
			name:            "凭据账号自定义 UA",
			accounts:        []Account{parent, shadow},
			accountID:       shadow.ID,
			pattern:         "codex_vscode/0.144.1 parent-terminal",
			routeProfileID:  20,
			expectedUA:      "codex_vscode/0.144.1 parent-terminal",
			expectedProfile: "routed",
		},
		{
			name:            "内置 Codex UA",
			accounts:        []Account{defaultAccount},
			accountID:       defaultAccount.ID,
			pattern:         codexCLIUserAgent,
			routeProfileID:  0,
			expectedUA:      codexCLIUserAgent,
			expectedProfile: "Built-in Default (Node.js 24.x)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := &model.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []model.TLSFingerprintRouterRule{{
					Enabled:                 true,
					MatchType:               model.TLSRouterMatchExact,
					Pattern:                 test.pattern,
					TLSFingerprintProfileID: test.routeProfileID,
				}},
			}
			upstream := &httpUpstreamRecorder{resp: successfulOpenAIAutomaticProbeResponse()}
			svc := newOpenAIAutomaticProbeTestService(
				test.accounts,
				upstream,
				router,
				map[int64]*model.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}, 20: {ID: 20, Name: "routed"}},
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
	account := Account{
		ID:          1401,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	router := &model.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []model.TLSFingerprintRouterRule{{
			Enabled:                 true,
			MatchType:               model.TLSRouterMatchExact,
			Pattern:                 "route-client/1.0",
			TLSFingerprintProfileID: 404,
		}},
	}
	upstream := &httpUpstreamRecorder{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(
		[]Account{account},
		upstream,
		router,
		map[int64]*model.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}},
		nil,
	)

	result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-5.4", "hi", "route-client/1.0")

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "fixed", upstream.lastTLSProfile.Name)
}

func TestAccountTestService_AutomaticOpenAIProbeRoutesChatCompletionsAndImages(t *testing.T) {
	t.Run("Chat Completions", func(t *testing.T) {
		account := Account{
			ID:          1501,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://compat-upstream.example/v1",
			},
			Extra: map[string]any{
				openai_compat.ExtraKeyResponsesSupported: false,
				"tls_fingerprint_router_id":              int64(9),
			},
		}
		router := &model.TLSFingerprintRouter{
			ID:      9,
			Name:    "probe-router",
			Enabled: true,
			Rules: []model.TLSFingerprintRouterRule{{
				Enabled:           true,
				MatchType:         model.TLSRouterMatchExact,
				Pattern:           "chat-client/1.0",
				UpstreamUserAgent: "chat-upstream/2.0",
			}},
		}
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
		}}
		cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
		svc := newOpenAIAutomaticProbeTestService([]Account{account}, upstream, router, nil, cfg)

		result, err := svc.RunTestBackgroundWithPromptAndUserAgent(context.Background(), account.ID, "gpt-5.4", "hi", "chat-client/1.0")

		require.NoError(t, err)
		require.Equal(t, "success", result.Status)
		require.Equal(t, "chat-upstream/2.0", upstream.lastReq.Header.Get("User-Agent"))
		require.Contains(t, upstream.lastReq.URL.Path, "/chat/completions")
	})

	t.Run("OAuth 图片", func(t *testing.T) {
		account := Account{
			ID:          1502,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{"access_token": "test-token"},
			Extra: map[string]any{
				"enable_tls_fingerprint":    true,
				"tls_fingerprint_router_id": int64(9),
			},
		}
		router := &model.TLSFingerprintRouter{
			ID:      9,
			Name:    "probe-router",
			Enabled: true,
			Rules: []model.TLSFingerprintRouterRule{{
				Enabled:                 true,
				MatchType:               model.TLSRouterMatchExact,
				Pattern:                 "image-client/1.0",
				TLSFingerprintProfileID: 20,
				UpstreamUserAgent:       "codex-tui/0.144.1 image-terminal",
				UpstreamOriginator:      "codex-tui",
			}},
		}
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\",\"output_format\":\"png\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		}}
		svc := newOpenAIAutomaticProbeTestService(
			[]Account{account},
			upstream,
			router,
			map[int64]*model.TLSFingerprintProfile{20: {ID: 20, Name: "image-route"}},
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
	account := Account{
		ID:          1601,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"openai_oauth_client_policy": OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	router := &model.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []model.TLSFingerprintRouterRule{{
			Enabled:   true,
			MatchType: model.TLSRouterMatchExact,
			Pattern:   "never-matched-by-manual-test",
		}},
	}
	upstream := &httpUpstreamRecorder{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService([]Account{account}, upstream, router, nil, nil)
	c, _ := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "gpt-5.4", "hi", AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
}
