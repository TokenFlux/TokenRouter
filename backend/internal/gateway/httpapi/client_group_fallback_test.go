package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// Client-group resolution changes the key, subscription, and route that later
// HTTP phases consume. These scenarios check that text and extended-protocol
// entries plan with the resolved group, keep the authenticated actor and forced
// platform, and stop before execution when resolution fails.

// clientFallbackExecutor records execution input after the handler finishes group resolution.
type clientFallbackExecutor struct {
	// request holds the value from the latest Execute call. Tests inspect its
	// group and funding fields after the synchronous handler invocation has returned.
	request execution.Request
	calls   int // execution invocation count
}

// clientFallbackFunding checks that funding admission receives the resolved target group.
type clientFallbackFunding struct {
	t     *testing.T // current test assertions
	calls int        // funding check count
	// subscription is the expected selected object. CheckKey compares its pointer
	// with the supplied subscription, including nil when the scenario uses balance funding.
	subscription *billing.UserSubscription
}

// Execute records the supplied request value and counts one downstream execution.
// It returns an empty successful result so each handler test can inspect group
// and funding selection after admission without requiring an upstream exchange.
func (e *clientFallbackExecutor) Execute(_ context.Context, in execution.Request, _ upstream.OutputSink) (execution.ExecutionResult, error) {
	e.request, e.calls = in, e.calls+1
	return execution.ExecutionResult{}, nil
}

// CheckKey verifies the target group and expected subscription before allowing admission.
// Tests supply a key with GroupID and expect group 20; the counter proves
// that the handler performs its final funding check after group resolution.
func (f *clientFallbackFunding) CheckKey(_ context.Context, key *apikey.APIKey, sub *billing.UserSubscription, _ string, _ bool) error {
	require.Equal(f.t, int64(20), *key.GroupID)
	require.Same(f.t, f.subscription, sub)
	f.calls++
	return nil
}

// TestClientGroupFallbackPrecedesPoliciesAndExecution checks HTTP adapter
// ordering with resolver, funding, and execution fixtures.
// Each entry must plan and execute against the target group and selected
// subscription, while the source key, authenticated actor, and forced-platform
// setting remain available with their asserted identities.
func TestClientGroupFallbackPrecedesPoliciesAndExecution(t *testing.T) {
	cases := []struct {
		name   string              // HTTP entry scenario
		source protocol.ProtocolID // inbound client protocol
	}{
		{"messages", protocol.ProtocolAnthropicMessages},
		{"responses", protocol.ProtocolOpenAIResponses},
		{"chat", protocol.ProtocolOpenAIChatCompletions},
		{"forced_messages", protocol.ProtocolAnthropicMessages},
		{"gemini", protocol.ProtocolGeminiGenerateContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, response := prefaceContext(`{"model":"client-model","messages":[{"role":"user","content":"hi"}],"input":"hi","contents":[]}`)
			targetID, sourceID := int64(20), int64(10)
			key := &apikey.APIKey{ID: 9, UserID: 42, User: &identity.User{ID: 42}, GroupID: &sourceID, Group: &routing.Group{ID: sourceID, Status: "active", ClaudeCodeOnly: true, AllowedProtocols: []protocol.ProtocolID{tc.source}, FallbackGroupID: &targetID}}
			target := apikey.CopyAPIKey(key)
			target.GroupID = &targetID
			target.Group = &routing.Group{ID: targetID, Status: "active", AllowedProtocols: []protocol.ProtocolID{tc.source}}
			sub := &billing.UserSubscription{ID: 77}
			c.Set(string(keyhttp.ContextKeyAPIKey), key)
			c.Set("subscription", &billing.UserSubscription{ID: 11})
			ctx := requeststate.WithClientProtocol(c.Request.Context(), tc.source)
			ctx = requeststate.WithGroup(ctx, key.Group)
			ctx = apikey.WithAccessSnapshot(ctx, apikey.AccessSnapshot{KeyID: key.ID, OwnerUserID: 42, PayerUserID: 42, ActorUserID: 43})
			ctx = apikey.WithRuntimeAPIKey(ctx, key)
			ctx = apikey.WithForcePlatform(ctx, "antigravity")
			c.Request = c.Request.WithContext(ctx)
			resolved, planned := false, false
			resolver := func(ctx context.Context, current *apikey.APIKey, source protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
				require.False(t, requeststate.IsClaudeCodeClient(ctx))
				require.Equal(t, tc.source, source)
				require.Equal(t, sourceID, *current.GroupID)
				resolved = true
				return target, sub, nil
			}
			planner := func(ctx context.Context, current *apikey.APIKey, model string) routing.RoutePlan {
				require.True(t, resolved)
				require.Equal(t, targetID, *current.GroupID)
				group, ok := requeststate.GroupFromContext(ctx)
				require.True(t, ok)
				require.Equal(t, targetID, group.ID)
				planned = true
				return routing.Plan(routing.PlanInput{Group: current.Group, ClientProtocol: tc.source, RequestedModel: model, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: "target-model"}})
			}
			funds := &clientFallbackFunding{t: t, subscription: sub}
			executor := &clientFallbackExecutor{}
			prompt := &prefaceBackend{}
			concurrency := prefaceConcurrency()
			messages := MessagesBindings{ClientGroupFallback: resolver, PlanRoute: planner, Funding: funds, ClientVersions: func(context.Context) (string, string) { return "", "" }, CachedSession: func(context.Context, *int64, string) (int64, error) { return 0, nil }, ObserveCompatibility: func(*zap.Logger) {}}
			openai := OpenAITextBindings{ClientGroupFallback: resolver, PlanRoute: planner, Funding: funds, ReplaceModel: openaiprotocol.ReplaceModelInBody, Resources: &OpenAIHTTPResources{Concurrency: concurrency}, Dependencies: OpenAIDependencies{Handler: true, Gateway: true, Keys: true, Funding: true, Concurrency: true}}
			switch tc.name {
			case "messages":
				NewBoundOpenAITextHandler(OpenAITextOptions{}, openai, prompt, executor).Messages(c)
			case "responses":
				NewBoundOpenAITextHandler(OpenAITextOptions{}, openai, prompt, executor).Responses(c)
			case "chat":
				NewBoundOpenAITextHandler(OpenAITextOptions{}, openai, prompt, executor).ChatCompletions(c)
			case "forced_messages":
				NewBoundMessagesHandler(MessagesHTTPOptions{}, messages, prompt, concurrency, executor).Messages(c)
			case "gemini":
				c.Params = gin.Params{{Key: "modelAction", Value: "/client-model:generateContent"}}
				NewBoundGeminiNativeHandler(GeminiNativeOptions{}, messages, GeminiHTTPBindings{SafeModelSegment: func(string) bool { return true }}, prompt, concurrency, func() string { return "new" }, executor).GeminiV1BetaModels(c)
			}
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.True(t, resolved)
			require.True(t, planned)
			require.Equal(t, 1, executor.calls)
			require.Equal(t, targetID, *executor.request.Funding.Key.GroupID)
			require.Same(t, sub, executor.request.Funding.Subscription)
			require.Equal(t, targetID, executor.request.Route.GroupID())
			require.Equal(t, 1, funds.calls)
			require.Equal(t, sourceID, *key.GroupID)
			platform, forced := apikey.ForcePlatformFromContext(c.Request.Context())
			require.True(t, forced)
			require.Equal(t, "antigravity", platform)
			access, ok := apikey.AccessSnapshotFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, targetID, *access.KeyView().GroupID)
			require.Equal(t, int64(43), access.ActorUserID)
		})
	}
}

// TestClientGroupFallbackFailureKeepsOriginalRequestSnapshot supplies a funding
// denial from the group resolver before planning. Each entry must return
// forbidden, leave its source group in context, and avoid planning or executing a request.
func TestClientGroupFallbackFailureKeepsOriginalRequestSnapshot(t *testing.T) {
	for _, source := range []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions} {
		p, h, c, response, _ := newOpenAITextEntryProbe(t, `{"model":"client-model","messages":[],"input":"hi"}`)
		groupID, targetID := int64(10), int64(20)
		p.key.GroupID = &groupID
		p.key.Group = &routing.Group{ID: groupID, Status: "active", ClaudeCodeOnly: true, FallbackGroupID: &targetID}
		p.bindings.ClientGroupFallback = func(context.Context, *apikey.APIKey, protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
			return nil, nil, billing.ErrPreferredSubscriptionGroup
		}
		c.Request = c.Request.WithContext(requeststate.WithGroup(c.Request.Context(), p.key.Group))
		switch source {
		case protocol.ProtocolAnthropicMessages:
			h.Messages(c)
		case protocol.ProtocolOpenAIResponses:
			h.Responses(c)
		default:
			h.ChatCompletions(c)
		}
		require.Equal(t, http.StatusForbidden, response.Code)
		require.NotContains(t, p.events, "plan")
		require.Nil(t, p.call)
		group, _ := requeststate.GroupFromContext(c.Request.Context())
		require.Equal(t, groupID, group.ID)
	}
}

// TestRouteGuardClientFallbackCoversNonMessageProtocols resolves a restricted group
// at shared route guards across media, live, WS, and auxiliary entries.
// Each allowed case must invoke the resolver and expose the target effective key
// after the guard returns successfully.
func TestRouteGuardClientFallbackCoversNonMessageProtocols(t *testing.T) {
	for _, spec := range []struct {
		method, path string // method: request verb; path: guarded endpoint
	}{
		{"POST", "/v1/images/batches"}, {"POST", "/v1/web_search"}, {"POST", "/v1/x_search"}, {"POST", "/v1/images/generations"}, {"POST", "/v1/videos"}, {"POST", "/v1/tts"}, {"GET", "/v1/realtime"}, {"GET", "/v1/responses"}, {"POST", "/v1/live"}, {"POST", "/v1/systemone"}, {"POST", "/v1/embeddings"}, {"POST", "/v1/alpha/search"}, {"POST", "/v1/responses/input_tokens"},
	} {
		t.Run(spec.method+spec.path, func(t *testing.T) {
			source := ExtendedRouteProtocol(spec.method, spec.path)
			if spec.path == "/v1/responses/input_tokens" {
				source = protocol.ProtocolOpenAIResponses
			}
			require.NotEmpty(t, source)
			c, response := prefaceContext("")
			c.Request.Method = spec.method
			c.Request.URL.Path = spec.path
			id, targetID := int64(10), int64(20)
			key := &apikey.APIKey{GroupID: &id, Group: &routing.Group{ID: id, Status: "active", ClaudeCodeOnly: true, FallbackGroupID: &targetID, AllowedProtocols: []protocol.ProtocolID{source}}}
			c.Set(string(keyhttp.ContextKeyAPIKey), key)
			resolved := false
			guards := NewRouteGuards(RouteMiddleware{
				Access: func(*gin.Context) RouteAccess {
					return RouteAccess{HasGroup: true, AllowedProtocols: []protocol.ProtocolID{source}}
				}, InstallClientProtocol: func(*gin.Context, protocol.ProtocolID) {}, ObserveBusinessLimit: func(*gin.Context, string) {},
				ClientGroupFallback: func(_ context.Context, key *apikey.APIKey, actual protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
					require.Equal(t, source, actual)
					resolved = true
					out := apikey.CopyAPIKey(key)
					out.GroupID = &targetID
					out.Group = &routing.Group{ID: targetID, Status: "active", AllowedProtocols: []protocol.ProtocolID{source}}
					return out, nil, nil
				},
			})
			if spec.path == "/v1/responses/input_tokens" {
				require.True(t, guards.EnforceGroupClientProtocol(c, source, GroupClientProtocolErrorOpenAI))
			} else {
				guards.RequireExtendedProtocol(c)
			}
			require.True(t, resolved)
			require.Equal(t, 200, response.Code)
			effective, ok := EffectiveAPIKey(c)
			require.True(t, ok)
			require.Equal(t, targetID, *effective.GroupID)
		})
	}
}

// TestCountTokensResolvesClientGroupBeforeFundingAndPlanning substitutes a target
// group for an unrecognized Messages client at token counting. Funding and the
// final route must use the target, while the supplied source key stays unchanged.
func TestCountTokensResolvesClientGroupBeforeFundingAndPlanning(t *testing.T) {
	sourceID, targetID := int64(10), int64(20)
	source := &apikey.APIKey{ID: 7, GroupID: &sourceID, Group: &routing.Group{ID: sourceID, Status: "active", ClaudeCodeOnly: true, FallbackGroupID: &targetID}}
	fixture := &countHTTPContract{t: t, group: targetID}
	funds := &clientFallbackFunding{t: t}
	ports := CountHTTPPorts{
		Executor: fixture, Funding: funds, ReadAccess: func(*gin.Context) (*apikey.APIKey, bool) { return source, true }, ObserveCompatibility: func(*zap.Logger) {},
		ClientGroupFallback: func(ctx context.Context, key *apikey.APIKey, p protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
			require.False(t, requeststate.IsClaudeCodeClient(ctx))
			require.Equal(t, protocol.ProtocolAnthropicMessages, p)
			resolved := apikey.CopyAPIKey(key)
			resolved.GroupID = &targetID
			resolved.Group = &routing.Group{ID: targetID, Status: "active", AllowedProtocols: []protocol.ProtocolID{p}}
			return resolved, nil, nil
		},
	}
	c, response := prefaceContext(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`)
	NewCountTokensHandler(1024, 2, ports, fixture).CountTokens(c)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, funds.calls)
	require.Equal(t, 2, fixture.attempts)
	plan, ok := requeststate.RoutePlanFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, targetID, plan.GroupID())
	require.Equal(t, sourceID, *source.GroupID)
}

// TestClientGroupFallbackKeepsDetectedClaudeInOriginalGroup marks a Messages client
// as recognized Claude Code. Resolution must return the same key and skip
// the configured resolver because that client can use its existing restricted group.
func TestClientGroupFallbackKeepsDetectedClaudeInOriginalGroup(t *testing.T) {
	c, _ := prefaceContext("")
	key := &apikey.APIKey{Group: &routing.Group{ID: 10, ClaudeCodeOnly: true}}
	c.Request = c.Request.WithContext(requeststate.SetClaudeCodeClient(c.Request.Context(), true))
	backend := messagesHTTPBackend{bindings: MessagesBindings{ClientGroupFallback: func(context.Context, *apikey.APIKey, protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
		t.Fatal("已识别 Claude Code 不应回退")
		return nil, nil, nil
	}}}
	resolved, err := resolveClientGroupForRequest(c, backend, key, protocol.ProtocolAnthropicMessages)
	require.NoError(t, err)
	require.Same(t, key, resolved)
}

// TestRouteGuardClientFallbackRejectsBeforeHandler makes target authorization fail
// at an image route guard. The guard must return false and abort with forbidden,
// while its existing API-key view remains installed after the rejection.
func TestRouteGuardClientFallbackRejectsBeforeHandler(t *testing.T) {
	c, response := prefaceContext("")
	c.Request.Method, c.Request.URL.Path = "POST", "/v1/images/generations"
	source := protocol.ProtocolImagesGenerations
	key := &apikey.APIKey{Group: &routing.Group{ID: 10, ClaudeCodeOnly: true}}
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	guards := NewRouteGuards(RouteMiddleware{
		Access: func(*gin.Context) RouteAccess {
			return RouteAccess{HasGroup: true, AllowedProtocols: []protocol.ProtocolID{source}}
		}, InstallClientProtocol: func(*gin.Context, protocol.ProtocolID) {},
		ClientGroupFallback: func(context.Context, *apikey.APIKey, protocol.ProtocolID) (*apikey.APIKey, *billing.UserSubscription, error) {
			return nil, nil, apikey.ErrGroupNotAllowed
		},
	})
	require.False(t, guards.EnforceGroupClientProtocol(c, source, GroupClientProtocolErrorOpenAI))
	require.True(t, c.IsAborted())
	require.Equal(t, http.StatusForbidden, response.Code)
	effective, _ := keyhttp.GetAPIKeyFromContext(c)
	require.Same(t, key, effective)
}
