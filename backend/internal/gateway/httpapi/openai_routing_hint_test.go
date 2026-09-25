package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSetOpenAICodexRoutingHintCanonicalizesOfficialServiceTiers(t *testing.T) {
	oauthAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	tests := []struct {
		name        string
		model       string
		serviceTier string
		want        string
	}{
		{name: "fast alias", model: "gpt-5.6", serviceTier: " fast ", want: "model=gpt-5.6;tier=priority"},
		{name: "priority", model: "gpt-5.6", serviceTier: "priority", want: "model=gpt-5.6;tier=priority"},
		{name: "flex", model: "gpt-5.6", serviceTier: "flex", want: "model=gpt-5.6;tier=flex"},
		{name: "ultrafast", model: "gpt-5.6", serviceTier: "ultrafast", want: "model=gpt-5.6;tier=ultrafast"},
		{name: "explicit default sentinel", model: "gpt-5.6", serviceTier: "default", want: "model=gpt-5.6"},
		{name: "omitted tier", model: "gpt-5.6", want: "model=gpt-5.6"},
		{name: "auto is not expanded without catalog support", model: "gpt-5.6", serviceTier: "auto", want: "model=gpt-5.6"},
		{name: "scale is not expanded without catalog support", model: "gpt-5.6", serviceTier: "scale", want: "model=gpt-5.6"},
		{name: "unknown tier does not expand protocol", model: "gpt-5.6", serviceTier: "turbo", want: "model=gpt-5.6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			SetOpenAICodexRoutingHint(headers, oauthAccount, tt.model, tt.serviceTier)
			require.Equal(t, tt.want, headers.Get("x-codex-routing-hint"))
		})
	}

	t.Run("invalid header value is omitted", func(t *testing.T) {
		headers := make(http.Header)
		SetOpenAICodexRoutingHint(headers, oauthAccount, "gpt-5.6\ninvalid", "priority")
		require.Empty(t, headers.Get("x-codex-routing-hint"))
	})

	for _, model := range []string{"gpt-5.6;evil", "gpt=5.6"} {
		t.Run("delimiter in model is omitted: "+model, func(t *testing.T) {
			headers := make(http.Header)
			SetOpenAICodexRoutingHint(headers, oauthAccount, model, "priority")
			require.Empty(t, headers.Get("x-codex-routing-hint"))
		})
	}

	t.Run("api key strips gateway-owned hint in every key casing", func(t *testing.T) {
		headers := make(http.Header)
		headers["x-codex-routing-hint"] = []string{"lowercase-spoof"}
		headers["X-Codex-Routing-Hint"] = []string{"canonical-spoof"}
		SetOpenAICodexRoutingHint(headers, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}, "gpt-5.6", "priority")
		for key := range headers {
			require.False(t, strings.EqualFold(key, "x-codex-routing-hint"))
		}
	})

	t.Run("oauth replaces spoofed lowercase hint", func(t *testing.T) {
		headers := make(http.Header)
		headers["x-codex-routing-hint"] = []string{"model=spoof;tier=flex"}
		SetOpenAICodexRoutingHint(headers, oauthAccount, "gpt-5.6", "priority")
		require.Equal(t, "model=gpt-5.6;tier=priority", headers.Get("x-codex-routing-hint"))
		require.Len(t, headers, 1)
	})
}

func TestOpenAIOAuthHTTPBuildersSendRoutingHintFromFinalBody(t *testing.T) {

	oauthAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "test-account",
		}},
	}
	svc := newWSFixture(wsFixtureInputs{})

	tests := []struct {
		name string
		body []byte
		want string
	}{
		{name: "fast", body: []byte(`{"model":"gpt-5.6-codex","service_tier":"fast"}`), want: "model=gpt-5.6-codex;tier=priority"},
		{name: "flex", body: []byte(`{"model":"gpt-5.6-codex","service_tier":"flex"}`), want: "model=gpt-5.6-codex;tier=flex"},
		{name: "ultrafast", body: []byte(`{"model":"gpt-5.6-codex","service_tier":"ultrafast"}`), want: "model=gpt-5.6-codex;tier=ultrafast"},
		{name: "default", body: []byte(`{"model":"gpt-5.6-codex","service_tier":"default"}`), want: "model=gpt-5.6-codex"},
		{name: "omitted", body: []byte(`{"model":"gpt-5.6-codex"}`), want: "model=gpt-5.6-codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, passthrough := range []bool{false, true} {
				mode := "ordinary"
				if passthrough {
					mode = "passthrough"
				}
				t.Run(mode, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(tt.body))

					var req *http.Request
					var err error
					if passthrough {
						req, err = svc.Requests.BuildPassthrough(context.Background(), c, oauthAccount, tt.body, "test-token")
					} else {
						req, err = svc.Requests.Build(context.Background(), c, oauthAccount, tt.body, "test-token", false, "", true)
					}
					require.NoError(t, err)
					require.Equal(t, tt.want, req.Header.Get("x-codex-routing-hint"))
				})
			}
		})
	}
}

func TestOpenAIHTTPPassthroughStripsOnlyOAuthLegacyResponsesBeta(t *testing.T) {

	svc := newWSFixture(wsFixtureInputs{options: &wsFixtureOptions{Request: OpenAIRequestOptions{URLPolicy: egress.OperatorURLPolicy{Enabled: false}}}})
	body := []byte(`{"model":"gpt-5.6-codex","service_tier":"priority"}`)

	build := func(t *testing.T, account *gatewayprovider.ExecutionAccount, betaValues []string, rawLowercaseKey bool) http.Header {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		if rawLowercaseKey {
			c.Request.Header["openai-beta"] = append([]string(nil), betaValues...)
		} else {
			for _, value := range betaValues {
				c.Request.Header.Add("OpenAI-Beta", value)
			}
		}

		req, err := svc.Requests.BuildPassthrough(context.Background(), c, account, body, "test-token")
		require.NoError(t, err)
		return req.Header
	}

	oauth := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "test-account",
		}},
	}
	apiKey := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-api-key",
		}},
	}

	t.Run("oauth legacy only is removed including raw lowercase key", func(t *testing.T) {
		headers := build(t, oauth, []string{"responses=experimental"}, true)
		require.Empty(t, headers.Values("OpenAI-Beta"))
	})

	t.Run("oauth mixed beta preserves independent tokens", func(t *testing.T) {
		headers := build(t, oauth, []string{
			"responses=experimental, future_feature=v1",
			"another_feature=v2, RESPONSES=EXPERIMENTAL",
		}, false)
		require.Equal(t, []string{"future_feature=v1", "another_feature=v2"}, headers.Values("OpenAI-Beta"))
	})

	t.Run("api key explicit beta remains caller controlled", func(t *testing.T) {
		headers := build(t, apiKey, []string{"responses=experimental, future_feature=v1"}, false)
		require.Equal(t, []string{"responses=experimental, future_feature=v1"}, headers.Values("OpenAI-Beta"))
	})
}

func TestBuildOpenAIWSHeadersSendsOAuthRoutingHintOnly(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	svc := newWSFixture(wsFixtureInputs{})
	decision := egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2}

	build := func(t *testing.T, account *gatewayprovider.ExecutionAccount, tier string) http.Header {
		headers, _, err := svc.buildOpenAIWSHeaders(
			context.Background(),
			c,
			account,
			"test-token",
			decision,
			true,
			"",
			"",
			"",
			"gpt-5.6-codex",
			tier,
		)
		require.NoError(t, err)
		return headers
	}

	oauthAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "test-account",
		}},
	}
	require.Equal(t, "model=gpt-5.6-codex;tier=priority", build(t, oauthAccount, "fast").Get("x-codex-routing-hint"))
	require.Equal(t, "model=gpt-5.6-codex;tier=ultrafast", build(t, oauthAccount, "ultrafast").Get("x-codex-routing-hint"))
	require.Equal(t, "model=gpt-5.6-codex", build(t, oauthAccount, "default").Get("x-codex-routing-hint"))
	require.Empty(t, build(t, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}, "priority").Get("x-codex-routing-hint"))
}

func TestOpenAIRoutingDiagnosticsUseFinalDerivedValuesOnly(t *testing.T) {

	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 917,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "chatgpt-account",
		}},
	}
	body := []byte(`{"model":"gpt-5.6-codex","service_tier":"fast"}`)
	svc := newWSFixture(wsFixtureInputs{})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Authorization", "Bearer caller-secret")
	c.Request.Header.Set("x-codex-routing-hint", "model=caller-secret")
	_, err := svc.Requests.Build(context.Background(), c, account, body, "oauth-secret", false, "", true)
	require.NoError(t, err)

	decision := egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2}
	_, _, err = svc.buildOpenAIWSHeaders(
		context.Background(), c, account, "oauth-secret", decision, true,
		"", "", "", "gpt-5.6-codex", "fast",
	)
	require.NoError(t, err)

	require.True(t, logSink.ContainsMessageAtLevel("openai routing decision", "debug"))
	require.True(t, logSink.ContainsFieldValue("account_id", "917"))
	require.True(t, logSink.ContainsFieldValue("final_model", "gpt-5.6-codex"))
	require.True(t, logSink.ContainsFieldValue("final_service_tier", "priority"))
	require.True(t, logSink.ContainsFieldValue("routing_hint_generated", "true"))
	require.True(t, logSink.ContainsFieldValue("transport", "http"))
	require.True(t, logSink.ContainsFieldValue("transport", string(egress.OpenAIUpstreamTransportResponsesWebsocketV2)))
	require.True(t, logSink.ContainsFieldValue("ws_affinity_decision", "not_applicable"))
	require.True(t, logSink.ContainsFieldValue("ws_affinity_decision", "soft_routing_hint"))
	require.False(t, logSink.ContainsFieldValue("authorization", "caller-secret"))
	require.False(t, logSink.ContainsFieldValue("credentials", "oauth-secret"))
	require.False(t, logSink.ContainsFieldValue("routing_hint", "caller-secret"))
}

func TestOpenAIWSConnPoolPreferredContinuationIgnoresRoutingHintChanges(t *testing.T) {
	options := &wsFixtureOptions{}
	options.Pool.MaxConnsPerAccount = 2
	options.Pool.MinIdlePerAccount = 0
	options.Pool.MaxIdlePerAccount = 2

	pool := newOpenAIWSConnPool(options)
	dialer := &openAIWSCountingDialer{}
	pool.SetClientDialerForTest(dialer)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 913, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	acquire := func(t *testing.T, hint, preferred string, forcePreferred bool) *openai.WSConnLease {
		t.Helper()
		headers := make(http.Header)
		if hint != "" {
			headers.Set("x-codex-routing-hint", hint)
		}
		lease, err := pool.Acquire(context.Background(), openai.WSAcquireRequest{
			Account:            openAIWSPoolAccountView(account),
			WSURL:              "wss://example.com/v1/responses",
			Headers:            headers,
			PreferredConnID:    preferred,
			ForcePreferredConn: forcePreferred,
		})
		require.NoError(t, err)
		require.NotNil(t, lease)
		return lease
	}

	standard := acquire(t, "model=gpt-5.6-codex", "", false)
	connID := standard.ConnID()
	standard.Release()

	priority := acquire(t, "model=gpt-5.6-codex;tier=priority", connID, true)
	require.True(t, priority.Reused())
	require.Equal(t, connID, priority.ConnID())
	priority.Release()

	standardAgain := acquire(t, "model=gpt-5.6-codex", connID, true)
	require.True(t, standardAgain.Reused())
	require.Equal(t, connID, standardAgain.ConnID())
	standardAgain.Release()

	require.Equal(t, 1, dialer.DialCount(), "routing hint is dial-time advisory, not continuation compatibility")
}

func TestOpenAIWSConnPoolUsesRoutingHintAsSoftDialAffinity(t *testing.T) {
	options := &wsFixtureOptions{}
	options.Pool.MaxConnsPerAccount = 4
	options.Pool.MinIdlePerAccount = 0
	options.Pool.MaxIdlePerAccount = 4

	pool := newOpenAIWSConnPool(options)
	dialer := &openAIWSCountingDialer{}
	pool.SetClientDialerForTest(dialer)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 913, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	acquire := func(t *testing.T, hint string) *openai.WSConnLease {
		headers := make(http.Header)
		headers.Set("x-codex-routing-hint", hint)
		lease, err := pool.Acquire(context.Background(), openai.WSAcquireRequest{
			Account: openAIWSPoolAccountView(account),
			WSURL:   "wss://example.com/v1/responses",
			Headers: headers,
		})
		require.NoError(t, err)
		require.NotNil(t, lease)
		return lease
	}

	priority := acquire(t, "model=gpt-5.6-codex;tier=priority")
	priorityConnID := priority.ConnID()
	priority.Release()

	priorityAgain := acquire(t, "model=gpt-5.6-codex;tier=priority")
	require.True(t, priorityAgain.Reused())
	require.Equal(t, priorityConnID, priorityAgain.ConnID())
	priorityAgain.Release()

	flex := acquire(t, "model=gpt-5.6-codex;tier=flex")
	require.False(t, flex.Reused())
	require.NotEqual(t, priorityConnID, flex.ConnID())
	flex.Release()

	otherModel := acquire(t, "model=gpt-5.5-codex;tier=priority")
	require.False(t, otherModel.Reused())
	require.NotEqual(t, priorityConnID, otherModel.ConnID())
	otherModel.Release()

	defaultTier := acquire(t, "model=gpt-5.6-codex")
	require.False(t, defaultTier.Reused())
	require.NotEqual(t, priorityConnID, defaultTier.ConnID())
	defaultConnID := defaultTier.ConnID()
	defaultTier.Release()

	defaultAgain := acquire(t, "model=gpt-5.6-codex")
	require.True(t, defaultAgain.Reused())
	require.Equal(t, defaultConnID, defaultAgain.ConnID())
	defaultAgain.Release()

	require.Equal(t, 4, dialer.DialCount())
}
