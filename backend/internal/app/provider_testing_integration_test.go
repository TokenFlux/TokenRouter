//go:build integration

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// provider_testing_integration_test.go verifies persisted credentials and dispatch through
// application-assembled platform targets. A recording transport supplies each
// platform's stream shape, letting the scenario check its request path and
// selected provider alongside typed test-start and successful completion
// events.

// TestNativeProviderTestAssembly runs OpenAI, Gemini, Anthropic, and Kimi tests
// against stored API-Key providers. It checks construction performs no
// exchange, each platform uses its expected path and provider, and SSE contains
// start and successful completion events.
func TestNativeProviderTestAssembly(t *testing.T) {
	f := newDatabaseFixture(t)
	store := providerpostgres.NewProviderStore(f.client, f.db, providerpostgres.ProviderStoreOptions{})
	transport := &nativeProviderTestTransport{}
	manager := lifecycle.New()
	core := NewProviderTestsForTest(store, nil, nil, nil, nil, transport, &config.Config{}, nil, nil, nil, nil, manager, nil, nil, nil)
	require.Empty(t, transport.requests, "构造不请求供应商")
	for _, test := range []struct {
		platform, model, path, body string // platform: tested provider platform; model: requested test model; path: expected upstream path; body: supplied protocol stream
	}{
		{provider.PlatformOpenAI, "gpt-5.4", "/v1/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"},
		{provider.PlatformGemini, "gemini-2.5-flash", "/v1beta/models/gemini-2.5-flash:streamGenerateContent", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\ndata: [DONE]\n\n"},
		{provider.PlatformAnthropic, "claude-sonnet-4-5", "/v1/messages", "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{provider.PlatformKimi, "kimi-k2", "/v1/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(test.platform, func(t *testing.T) {
			row, err := f.client.Provider.Create().SetName("test-native-test-" + test.platform).SetPlatform(test.platform).SetType(provider.ProviderTypeAPIKey).SetCredentials(map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example", "api_protocol": "chat_completions"}).Save(t.Context())
			require.NoError(t, err)
			transport.body = test.body
			before := len(transport.requests)
			recorder := httptest.NewRecorder()
			kind := provider.ProviderTestTypeText
			err = core.Test(t.Context(), provider.TestRequest{ProviderID: row.ID, Model: test.model, Prompt: "hello", Type: &kind}, providerhttp.NewTestEventSink(recorder))
			require.NoError(t, err)
			require.Len(t, transport.requests, before+1)
			require.Equal(t, row.ID, transport.ids[before])
			require.Equal(t, test.path, transport.requests[before].URL.Path)
			require.Contains(t, recorder.Body.String(), `"type":"test_start"`)
			require.Contains(t, recorder.Body.String(), `"type":"test_complete","success":true`)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Join registered test resources after all platform subtests finish while
	// the fixture database remains available for any final provider cleanup work.
	require.NoError(t, manager.Stop(ctx))
}

// TestProviderTestAssemblyUsesGroupAttributes exercises manual and automatic probes for unknown mapped models.
// API-key and OAuth probes admit the configured image group; absent, rejected,
// or unreadable output declarations stop before upstream traffic reaches transport.
func TestProviderTestAssemblyUsesGroupAttributes(t *testing.T) {
	f := newDatabaseFixture(t)
	store := providerpostgres.NewProviderStore(f.client, f.db, providerpostgres.ProviderStoreOptions{})
	transport := &nativeProviderTestTransport{}
	manager := lifecycle.New()
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	outputs := []string{"image"}
	repo := &probeAttributeRepository{config: &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{{Models: []string{"custom-image-final"}, Attributes: modelcatalog.Attributes{OutputModalities: &outputs}}}}}
	attributes := &routing.ModelAttributeService{Repo: repo, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	core := provideProviderTests(store, nil, nil, &provider.GrokTokenSource{}, nil, transport, &config.Config{}, nil, nil, nil, nil, manager, nil, attributes, nil)
	for _, credential := range []struct {
		platform string // selected platform executor
		kind     string // provider credential kind
	}{
		{provider.PlatformOpenAI, provider.ProviderTypeAPIKey},
		{provider.PlatformGemini, provider.ProviderTypeAPIKey},
		{provider.PlatformGrok, provider.ProviderTypeAPIKey},
		{provider.PlatformGrok, provider.ProviderTypeOAuth},
	} {
		platform := credential.platform
		probeProtocol := "gemini_generate_content"
		if platform == provider.PlatformOpenAI || platform == provider.PlatformGrok {
			probeProtocol = "openai_images_generations"
		}
		repo.config.Rules[0].Match = &routing.ModelAttributeMatch{ClientProtocol: probeProtocol, UpstreamProtocol: probeProtocol}
		row, err := f.client.Provider.Create().SetName("group-image-" + platform + "-" + credential.kind).SetPlatform(platform).SetType(credential.kind).SetCredentials(map[string]any{
			"api_key": "fixture-key", "base_url": "https://upstream.example",
			"access_token": "fixture-access", "refresh_token": "fixture-refresh",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"model_mapping": map[string]any{"alias": "custom-image-final", "custom-image-final": "wrong-second-hop"},
		}).Save(t.Context())
		require.NoError(t, err)
		for _, automatic := range []bool{false, true} {
			for _, groupID := range []int64{0, 59, 60, 61, 62} {
				before := len(transport.requests)
				repo.groups = nil
				if platform == provider.PlatformOpenAI || platform == provider.PlatformGrok {
					transport.body = `{"data":[{"b64_json":"QUJD"}]}`
				} else {
					transport.body = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"inlineData\":{\"mimeType\":\"image/png\",\"data\":\"QUJD\"}}]}}]}\n\n"
				}
				request := provider.TestRequest{ProviderID: row.ID, GroupID: groupID, Model: "alias", Prompt: "draw"}
				success := false
				if automatic {
					result, err := core.RunTestBackgroundRequest(t.Context(), request)
					require.NoError(t, err)
					success = result.Status == "success"
				} else {
					kind := provider.ProviderTestTypeImage
					request.Type = &kind
					recorder := httptest.NewRecorder()
					success = core.Test(t.Context(), request, providerhttp.NewTestEventSink(recorder)) == nil
				}
				require.Equal(t, groupID == 59, success, "platform=%s kind=%s automatic=%v group=%d", platform, credential.kind, automatic, groupID)
				if groupID > 0 {
					require.Contains(t, repo.groups, groupID)
				} else {
					require.Empty(t, repo.groups)
				}
				if groupID == 59 {
					require.Len(t, transport.requests, before+1)
					body, err := io.ReadAll(transport.requests[before].Body)
					require.NoError(t, err)
					require.Contains(t, string(body)+transport.requests[before].URL.Path, "custom-image-final")
					if platform != provider.PlatformGemini {
						require.Equal(t, "/v1/images/generations", transport.requests[before].URL.Path)
						require.Equal(t, "custom-image-final", gjson.GetBytes(body, "model").String())
					}
				} else {
					require.Len(t, transport.requests, before)
				}
			}
		}
	}
}

// TestProviderTestAssemblyMatchesTextAttributeProtocol uses a custom model declared
// on exactly one text endpoint. Automatic tests must resolve that endpoint's group
// attributes and send the same mapped model through the admitted protocol.
func TestProviderTestAssemblyMatchesTextAttributeProtocol(t *testing.T) {
	f := newDatabaseFixture(t)
	store := providerpostgres.NewProviderStore(f.client, f.db, providerpostgres.ProviderStoreOptions{})
	manager := lifecycle.New()
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	outputs := []string{"text"}
	repo := &probeAttributeRepository{config: &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{{Models: []string{"custom-text-final"}, Attributes: modelcatalog.Attributes{OutputModalities: &outputs}}}}}
	attributes := &routing.ModelAttributeService{Repo: repo, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	transport := &nativeProviderTestTransport{}
	core := provideProviderTests(store, nil, nil, nil, nil, transport, &config.Config{}, nil, nil, nil, nil, manager, nil, attributes, nil)
	for _, tc := range []struct {
		name      string                 // selected routing scenario
		mode      provider.TextRouteMode // configured upstream text mode
		requested string                 // explicit probe protocol selector
		protocol  string                 // expected attribute protocol identity
		path      string                 // expected upstream request path
	}{
		{"default responses", "", "", "openai_responses", "/v1/responses"},
		{"forced responses", provider.TextRouteModeForceResponses, "", "openai_responses", "/v1/responses"},
		{"forced chat", provider.TextRouteModeForceChatCompletions, "", "openai_chat_completions", "/v1/chat/completions"},
		{"explicit responses", provider.TextRouteModeForceChatCompletions, "responses", "openai_responses", "/v1/responses"},
		{"explicit chat", provider.TextRouteModeForceResponses, "chat_completions", "openai_chat_completions", "/v1/chat/completions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.config.Rules[0].Match = &routing.ModelAttributeMatch{ClientProtocol: tc.protocol, UpstreamProtocol: tc.protocol}
			repo.groups = nil
			transport.requests = nil
			transport.body = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"
			if tc.protocol == "openai_chat_completions" {
				transport.body = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			}
			row, err := f.client.Provider.Create().SetName("group-text-" + tc.name).SetPlatform("openai").SetType("apikey").SetExtra(map[string]any{provider.ExtraKeyTextRouteMode: string(tc.mode)}).SetCredentials(map[string]any{
				"api_key": "fixture-key", "base_url": "https://upstream.example",
				"upstream_protocols": []string{"openai_responses", "openai_chat_completions"},
				"model_mapping":      map[string]any{"alias": "custom-text-final", "custom-text-final": "wrong-second-hop"},
			}).Save(t.Context())
			require.NoError(t, err)
			result, err := core.RunTestBackgroundRequest(t.Context(), provider.TestRequest{ProviderID: row.ID, GroupID: 59, Model: "alias", Prompt: "hello", Protocol: tc.requested})
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, "ok", result.ResponseText)
			require.Len(t, transport.requests, 1)
			require.Equal(t, tc.path, transport.requests[0].URL.Path)
			body, err := io.ReadAll(transport.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, "custom-text-final", gjson.GetBytes(body, "model").String())
			require.Contains(t, repo.groups, int64(59))
		})
	}
}

// TestProviderTestAssemblyExecutesGroupProtocolRoutes uses protocol-scoped profiles and conflicting provider defaults.
// Each test sends exactly one request to its resolved endpoint with one provider mapping and reads the group profile once.
func TestProviderTestAssemblyExecutesGroupProtocolRoutes(t *testing.T) {
	f := newDatabaseFixture(t)
	store := providerpostgres.NewProviderStore(f.client, f.db, providerpostgres.ProviderStoreOptions{})
	transport := &nativeProviderTestTransport{}
	manager := lifecycle.New()
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	outputs := []string{"text"}
	disabled := false
	repo := &probeAttributeRepository{config: &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{{Models: []string{"final-model"}, Attributes: modelcatalog.Attributes{OutputModalities: &outputs, Temperature: &disabled, TopP: &disabled}}}}}
	attributes := &routing.ModelAttributeService{Repo: repo, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	core := provideProviderTests(store, nil, nil, nil, nil, transport, &config.Config{}, nil, nil, nil, nil, manager, nil, attributes, nil)
	for _, tc := range []struct {
		name     string // route scenario name
		platform string // provider platform identity
		entry    string // selected client entry
		upstream string // resolved upstream endpoint
		path     string // expected request path
		body     string // fixture response body
	}{
		{"messages to responses", "openai", "anthropic_messages", "openai_responses", "/v1/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"},
		{"responses to chat", "openai", "openai_responses", "openai_chat_completions", "/v1/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"chat to messages", "anthropic", "openai_chat_completions", "anthropic_messages", "/v1/messages", "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"gemini", "gemini", "gemini_generate_content", "gemini_generate_content", "/v1beta/models/final-model:streamGenerateContent", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\ndata: [DONE]\n\n"},
		{"cn messages", "kimi", "openai_chat_completions", "anthropic_messages", "/v1/messages", "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"cn chat", "kimi", "anthropic_messages", "openai_chat_completions", "/v1/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"systemone", "jev", "systemone", "systemone", "/v1/systemone", `{"model":"final-model","answers":{"available":{"type":"noul","noul":1}},"usage":{"input_tokens":1,"output_tokens":1}}`},
		{"images", "openai", "openai_images_generations", "openai_images_generations", "/v1/images/generations", `{"data":[{"b64_json":"aGVsbG8="}]}`},
		{"oauth images", "openai", "openai_images_generations", "openai_responses", "/backend-api/codex/responses", "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\"}]}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outputs = []string{"text"}
			if tc.name == "responses to chat" {
				outputs = []string{"text", "image"}
			}
			if tc.entry == "openai_images_generations" {
				outputs = []string{"image"}
			}
			repo.config.Rules[0].Match = &routing.ModelAttributeMatch{ClientProtocol: tc.entry, UpstreamProtocol: tc.upstream}
			repo.groups = nil
			transport.requests = nil
			transport.body = tc.body
			kind := "apikey"
			if tc.name == "oauth images" {
				kind = "oauth"
			}
			row, err := f.client.Provider.Create().SetName("route-" + tc.name).SetPlatform(tc.platform).SetType(kind).SetExtra(map[string]any{provider.ExtraKeyTextRouteMode: string(provider.TextRouteModeForceResponses)}).SetCredentials(map[string]any{
				"api_key": "fixture-key", "access_token": "fixture-access", "base_url": "https://upstream.example", "api_base_urls": map[string]any{"anthropic": "https://upstream.example", "chat_completions": "https://upstream.example", "responses": "https://upstream.example"},
				"upstream_protocols": []string{tc.upstream}, "model_mapping": map[string]any{"alias*": "final-model", "final-model": "wrong-second-hop"},
			}).Save(t.Context())
			require.NoError(t, err)
			request := provider.TestRequest{ProviderID: row.ID, GroupID: 59, Model: "alias", Prompt: "hi", ProtocolRoute: provider.TestProtocolRoute{ClientProtocol: protocol.ProtocolID(tc.entry), UpstreamProtocol: protocol.ProtocolID(tc.upstream)}}
			result, err := core.RunTestBackgroundRequest(t.Context(), request)
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Len(t, transport.requests, 1)
			require.Equal(t, tc.path, transport.requests[0].URL.Path)
			body, err := io.ReadAll(transport.requests[0].Body)
			require.NoError(t, err)
			if tc.name == "oauth images" {
				require.Equal(t, "final-model", gjson.GetBytes(body, "tools.0.model").String())
			} else if tc.platform != "gemini" {
				require.Equal(t, "final-model", gjson.GetBytes(body, "model").String())
			}
			require.False(t, gjson.GetBytes(body, "temperature").Exists())
			require.False(t, gjson.GetBytes(body, "top_p").Exists())
			require.Equal(t, []int64{59}, repo.groups)
			transport.requests = nil
			if tc.entry != "systemone" {
				outputs = []string{}
				result, err = core.RunTestBackgroundRequest(t.Context(), request)
				require.NoError(t, err)
				require.Equal(t, "failed", result.Status)
				require.Contains(t, result.ErrorMessage, "MODEL_CAPABILITY_UNSUPPORTED")
				require.Empty(t, transport.requests)
			}
			request.GroupID = 61
			result, err = core.RunTestBackgroundRequest(t.Context(), request)
			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Contains(t, result.ErrorMessage, "MODEL_ATTRIBUTES_UNAVAILABLE")
			require.Empty(t, transport.requests)
		})
	}
}

// TestQoderAttributeRoutesAcrossPreviewProbeAndGateway loads one saved account and
// sends its readable alias through the assembled probe and gateway. The signed
// native payloads must carry the same model key and protocol-scoped input limit
// shown in the group preview, including requests entering through Responses.
func TestQoderAttributeRoutesAcrossPreviewProbeAndGateway(t *testing.T) {
	f := newDatabaseFixture(t)
	store := providerpostgres.NewProviderStore(f.client, f.db, providerpostgres.ProviderStoreOptions{})
	credentials := map[string]any{"site": "cn", "uid": "fixture", "machine_id": "fixture-machine", "security_oauth_token": "fixture-token", "organization_id": "fixture-org"}
	identity := provider.QoderCatalogIdentity(&provider.Record{Platform: "qoder", Type: "cosy", Credentials: credentials})
	row, err := f.client.Provider.Create().SetName("catalog-route").SetPlatform("qoder").SetType("cosy").SetCredentials(credentials).SetExtra(map[string]any{
		provider.QoderCatalogExtraKey: map[string]any{"identity": identity, "version": "publication", "fetched_at": time.Now(), "models": []map[string]any{{"key": "native", "name": "Account Model", "alias": "public-name", "enabled": true, "input_limit": 1000000, "context_windows": []int{200000, 1000000}}}},
	}).Save(t.Context())
	require.NoError(t, err)
	record, err := store.GetByID(t.Context(), row.ID)
	require.NoError(t, err)
	rule := routing.ModelAttributeRule{Models: []string{"native"}, Match: &routing.ModelAttributeMatch{ClientProtocol: "openai_responses", UpstreamProtocol: "qoder_chat"}, Attributes: modelcatalog.Attributes{InputLimit: new(128000)}}
	profile := &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{rule}}
	attrs := &routing.ModelAttributeService{Repo: &probeAttributeRepository{config: profile}, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	const groupID = 59
	ctx := requeststate.WithGroup(t.Context(), &routing.Group{ID: groupID})
	target := record.ModelAttributeRoute("public-name", "openai_responses", "qoder_chat")
	preview, err := attrs.Preview(ctx, groupID, []modelrules.Target{target}, profile)
	require.NoError(t, err)
	require.Len(t, preview, 1)
	require.Equal(t, new(128000), preview[0].Attributes.InputLimit)
	transport := &nativeProviderTestTransport{body: "data: " + `{"body":"{\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}"}` + "\n\ndata: [DONE]\n\n"}
	manager := lifecycle.New()
	t.Cleanup(func() { require.NoError(t, manager.Stop(context.Background())) })
	probes := provideProviderTests(store, nil, nil, nil, nil, transport, &config.Config{}, nil, nil, nil, nil, manager, nil, attrs, nil)
	recorder := httptest.NewRecorder()
	err = probes.Test(ctx, provider.TestRequest{ProviderID: row.ID, GroupID: groupID, Model: "public-name", Prompt: "test", ProtocolRoute: provider.TestProtocolRoute{ClientProtocol: protocol.ProtocolOpenAIResponses, UpstreamProtocol: protocol.ProtocolQoderChat}}, providerhttp.NewTestEventSink(recorder))
	require.NoError(t, err, recorder.Body.String())
	tokens := provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{})
	tokens.SetHTTPUpstream(transport, nil)
	t.Cleanup(func() { require.NoError(t, tokens.StopContext(context.Background())) })
	runtime := gatewayprovider.NewQoderRuntime(gatewayprovider.QoderRuntimeOptions{Attributes: attrs, Transport: transport, Tokens: tokens})
	body, err := json.Marshal(map[string]any{"model": "public-name", "input": "test"})
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	_, err = gatewayhttp.ForwardQoderAttempt(ctx, c, runtime, record, body, protocol.ProtocolOpenAIResponses)
	require.NoError(t, err)
	require.Len(t, transport.requests, 2)
	for _, request := range transport.requests {
		raw, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		payload, err := qoder.Decode(string(raw))
		require.NoError(t, err)
		require.Equal(t, preview[0].Target.Model, gjson.GetBytes(payload, "model_config.key").String())
		require.EqualValues(t, *preview[0].Attributes.InputLimit, gjson.GetBytes(payload, "model_config.max_input_tokens").Int())
		require.EqualValues(t, *preview[0].Attributes.InputLimit, gjson.GetBytes(payload, "parameters.context_length").Int())
	}
}
