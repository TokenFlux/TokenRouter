package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// turnAuthentication supplies current Key results and records later admission
// inputs.
type turnAuthentication struct {
	key        *apikey.APIKey             // returned current authenticated Key
	err        error                      // configured reauthentication failure result
	input      apikey.AuthenticationInput // last reauthentication policy input
	acquired   *apikey.APIKey             // last reserved allowance Key
	acquireErr error                      // configured request allowance error
}

// turnSubscriptions returns a supplied subscription reference for current
// funding checks.
type turnSubscriptions struct {
	current *billing.UserSubscription // returned current subscription reference
}

// entryKeyReader counts unexpected eager lookups while access views are
// constructed.
type entryKeyReader struct {
	calls int /* Key reader invocation count */
}

// GetByKey panics when authorization uses a cached Key lookup in this fixture.
// Later-turn tests require Reauthenticate to supply their current Key instead,
// so the panic exposes an unexpected authentication path.
func (a *turnAuthentication) GetByKey(context.Context, string) (*apikey.APIKey, error) {
	panic("unexpected cached lookup")
}

// Reauthenticate records the supplied client and member-limit input and
// returns the configured Key and error. It keeps the supplied Key pointer
// intact so funding assertions can check the exact current snapshot.
func (a *turnAuthentication) Reauthenticate(_ context.Context, _ *apikey.APIKey, input apikey.AuthenticationInput) (*apikey.APIKey, error) {
	a.input = input
	return a.key, a.err
}

// AcquireRequest records the Key used for allowance reservation and returns
// the supplied context, no-op release and configured error. Its zero wait
// value models immediate admission for later-turn limit assertions.
func (a *turnAuthentication) AcquireRequest(ctx context.Context, key *apikey.APIKey) (context.Context, func(), time.Duration, error) {
	a.acquired = key
	return ctx, func() {}, 0, a.acquireErr
}

// TestAuthorizeTurn compares allowed, deleted, funding-denied and
// missing-subscription reauthorization. Funding receives the current Key only
// when earlier checks permit it, member limits and client IP are supplied, and
// the original rate limit remains unchanged.
func TestAuthorizeTurn(t *testing.T) {
	for _, name := range []string{"allowed", "deleted", "funding_denied", "subscription_missing"} {
		t.Run(name, func(t *testing.T) {
			original := &apikey.APIKey{ID: 1, User: &identity.User{ID: 2}, BillingMode: billing.APIKeyBillingModeBalance, RateLimit5h: 10}
			original.Group = &routing.Group{AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolResponsesWebSocket}}
			current := apikey.CopyAPIKey(original)
			current.RateLimit5h = 1
			auth := &turnAuthentication{key: current}
			if name == "deleted" {
				auth.err = apikey.ErrAPIKeyNotFound
			}
			if name == "subscription_missing" {
				current.BillingMode = billing.APIKeyBillingModeSubscription
			}
			called := false
			denied := errors.New("insufficient funds")
			adapter := &openAIWSEntryAdapter{key: original, bindings: Bindings{Keys: auth, CheckFunding: func(_ context.Context, key *apikey.APIKey, sub *billing.UserSubscription, _ string, afterWait bool) error {
				called = true
				require.Same(t, current, key)
				require.Nil(t, sub)
				require.False(t, afterWait)
				if name == "funding_denied" {
					return denied
				}
				return nil
			}}}
			adapter.call.ClientIP = "192.0.2.1"
			err := adapter.AuthorizeTurn(context.Background())
			if name == "allowed" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, name == "allowed" || name == "funding_denied", called)
			require.True(t, auth.input.CheckMemberLimits)
			require.Equal(t, "192.0.2.1", auth.input.ClientIP)
			require.Equal(t, 10.0, original.RateLimit5h)
		})
	}
}

// GetUsableSubscription returns the fixture's current subscription reference,
// false flag and nil error. Callers use this controlled funding result without
// repository reads or mutation of the supplied subscription.
func (s turnSubscriptions) GetUsableSubscription(context.Context, int64, ...int64) (*billing.UserSubscription, bool, error) {
	return s.current, false, nil
}

// GetSubscriptionForAPIKey returns the supplied current subscription reference
// for every lookup. It ignores user and subscription identifiers, letting
// authorization tests control the funding record while retaining its identity
// for assertions.
func (s turnSubscriptions) GetSubscriptionForAPIKey(context.Context, int64, int64) (*billing.UserSubscription, error) {
	return s.current, nil
}

// ValidateAndCheckLimits accepts the fixture subscription with a false flag
// and nil error. Actual funding denial is injected through CheckFunding in
// these tests, so subscription validation itself supplies no limit failure.
func (s turnSubscriptions) ValidateAndCheckLimits(*billing.UserSubscription) (bool, error) {
	return false, nil
}

// TestAuthorizeTurnReloadsSubscription supplies exhausted current funding
// beside an unused initial subscription. The funding checker sees the current
// subscription and returns its denial while the adapter retains the initial
// subscription reference and usage.
func TestAuthorizeTurnReloadsSubscription(t *testing.T) {
	key := &apikey.APIKey{ID: 1, User: &identity.User{ID: 2}, Group: &routing.Group{AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolResponsesWebSocket}}} // returned current authenticated Key
	old := &billing.UserSubscription{ID: 3, DailyUsageUSD: 0}
	current := &billing.UserSubscription{ID: 3, DailyUsageUSD: 10} // returned current subscription reference
	denied := errors.New("subscription exhausted")
	adapter := &openAIWSEntryAdapter{key: key, subscription: old, bindings: Bindings{
		Keys: &turnAuthentication{key: key}, Subscriptions: turnSubscriptions{current: current},
		CheckFunding: func(_ context.Context, _ *apikey.APIKey, sub *billing.UserSubscription, _ string, _ bool) error {
			require.Same(t, current, sub)
			return denied
		},
	}}
	require.ErrorIs(t, adapter.AuthorizeTurn(context.Background()), denied)
	require.Same(t, old, adapter.subscription)
	require.Zero(t, old.DailyUsageUSD)
}

// TestKeyAdmissionUsesCurrentTurnLimits reserves allowance before and after
// successful reauthorization with a reduced RPM limit. Later admission uses
// the current Key and presents RPM rejection as 1013, while the original Key's
// limit stays unchanged. A Token price rejection closes the current turn with
// policy status 1008 so the client receives a distinct configuration failure.
func TestKeyAdmissionUsesCurrentTurnLimits(t *testing.T) {
	original := &apikey.APIKey{ID: 1, User: &identity.User{ID: 2}, BillingMode: billing.APIKeyBillingModeBalance, RPMLimit: 20}
	original.Group = &routing.Group{AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolResponsesWebSocket}}
	current := apikey.CopyAPIKey(original)
	current.RPMLimit = 1
	auth := &turnAuthentication{key: current}
	adapter := &openAIWSEntryAdapter{key: original, bindings: Bindings{Keys: auth, CheckFunding: func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error { return nil }}}
	_, release, err := adapter.AcquireKey(context.Background())
	require.NoError(t, err)
	release()
	require.Same(t, original, auth.acquired)
	require.NoError(t, adapter.AuthorizeTurn(context.Background()))
	auth.acquireErr = apikey.ErrKeyRPMExceeded
	_, _, err = adapter.AcquireKey(context.Background())
	require.ErrorIs(t, err, apikey.ErrKeyRPMExceeded)
	require.Equal(t, 1013, adapter.CloseInfo(err).Status)
	require.Same(t, current, auth.acquired)
	require.Equal(t, 20, original.RPMLimit)
	auth.acquireErr = apikey.ErrTokenRateLimitExceeded
	_, _, err = adapter.AcquireKey(t.Context())
	require.ErrorIs(t, err, apikey.ErrTokenRateLimitExceeded)
	require.Equal(t, 1008, adapter.CloseInfo(err).Status)
}

// TestAuthorizeTurnRejectsRevokedWSProtocol reauthenticates a Key whose
// current group permits HTTP Responses but revokes WS. Authorization fails
// before funding, requestKey remains unset, and the initial group's WS
// permission stays intact.
func TestAuthorizeTurnRejectsRevokedWSProtocol(t *testing.T) {
	original := &apikey.APIKey{
		ID: 1, User: &identity.User{ID: 2}, BillingMode: billing.APIKeyBillingModeBalance,
		Group: &routing.Group{ID: 3, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolResponsesWebSocket}},
	}
	current := apikey.CopyAPIKey(original)
	current.Group.AllowedProtocols = []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}
	fundingCalled := false
	adapter := &openAIWSEntryAdapter{key: original, bindings: Bindings{
		Keys: &turnAuthentication{key: current},
		CheckFunding: func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error {
			fundingCalled = true
			return nil
		},
	}}
	require.Error(t, adapter.AuthorizeTurn(context.Background()))
	require.False(t, fundingCalled)
	require.True(t, original.Group.AllowsClientProtocol(protocol.ProtocolResponsesWebSocket))
	require.Nil(t, adapter.requestKey)
}

// GetByKey counts a lookup and returns no Key and no error. Access tests
// expect no call while constructing entry data; a later refresh would treat
// this empty result as unusable policy.
func (r *entryKeyReader) GetByKey(context.Context, string) (*apikey.APIKey, error) {
	r.calls++
	return nil, nil
}

// Reauthenticate counts the call and reports API Key not found. This fixture
// leaves current authorization unavailable so entry-view tests can detect any
// unexpected eager identity lookup.
func (r *entryKeyReader) Reauthenticate(context.Context, *apikey.APIKey, apikey.AuthenticationInput) (*apikey.APIKey, error) {
	r.calls++
	return nil, apikey.ErrAPIKeyNotFound
}

// AcquireRequest returns the supplied context with a no-op release, zero wait
// and no error. It models immediate allowance admission without a limiter so
// entry-view fixtures satisfy the bound Key interface.
func (r *entryKeyReader) AcquireRequest(ctx context.Context, _ *apikey.APIKey) (context.Context, func(), time.Duration, error) {
	return ctx, func() {}, 0, nil
}

// TestEntryAccessKeepsProjectionIndependentAndLazy builds an entry view with
// model and effort mappings and an available Key reader. Access enables later
// refresh without querying the reader; mutating the view's model map leaves
// the Key map unchanged and effort storage is distinct.
func TestEntryAccessKeepsProjectionIndependentAndLazy(t *testing.T) {
	reader := &entryKeyReader{}
	key := &apikey.APIKey{ID: 9, UserID: 7, Key: "fixture-key", ModelMapping: map[string]string{"alias": "original"}, Group: &routing.Group{ReasoningEffortMappings: []routing.ReasoningEffortMapping{{}}}} // returned current authenticated Key
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	view, ok := (openAIWSHTTPBackend{bindings: Bindings{Keys: reader}}).Access(c)
	require.True(t, ok)
	require.True(t, view.RefreshFastPolicy)
	require.Zero(t, reader.calls)
	view.ModelMapping["alias"] = "changed"
	require.Equal(t, "original", key.ModelMapping["alias"])
	require.NotSame(t, &key.Group.ReasoningEffortMappings[0], &view.Group.ReasoningEffortMappings[0])
}

// TestWSTurnPricing alternates a zero-priced model with an unpriced model in
// one group, then changes the group. Zero per-request price permits the priced
// turn, while missing or other-group prices produce local admission failure
// that does not report provider health.
func TestWSTurnPricing(t *testing.T) {
	zero := 0.0
	prices := &admission.ModelPricing{Resolver: testkit.ResolverWithCards(t, billing.NewCalculator(nil, billing.CalculatorOptions{}), []routing.ModelPricingEntry{{
		Models: []string{"priced-review"}, BillingMode: pricing.BillingModePerRequest, PerRequestPrice: &zero,
	}})}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	target := &openAIWSEntryTarget{
		provider: gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: "openai", Type: "apikey"}),
		root: &openAIWSEntryAdapter{c: c, key: &apikey.APIKey{GroupID: testkit.GroupID()}, bindings: Bindings{
			Pricing: prices,
			ResolveRouting: func(_ context.Context, _ *int64, _ *gatewayprovider.ExecutionProvider, model string, _ provider.OpenAIEndpointCapability) (string, error) {
				return model, nil
			},
		}},
	}
	for _, model := range []string{"priced-review", "codex-auto-review", "priced-review"} {
		plan := routing.Plan(routing.PlanInput{GroupID: testkit.GroupID(), RequestedModel: model, GroupMapping: routing.GroupMappingResult{MappedModel: model}})
		ctx := requeststate.WithRoutePlan(context.Background(), plan)
		_, err := target.ResolveRouting(ctx, model, false)
		if model == "codex-auto-review" {
			require.ErrorIs(t, err, pricing.ErrModelPricingUnavailable)
			require.Equal(t, admission.ModelPricingUnavailableMessage, gatewayws.EntryLocalRoutingErrorReason(model, err))
			require.False(t, gatewayws.EntryShouldReportFailure(gatewayws.EntryLocalRoutingCause(err)))
		} else {
			require.NoError(t, err)
		}
	}
	otherGroup := int64(101)
	target.root.key.GroupID = &otherGroup
	_, err := target.ResolveRouting(context.Background(), "priced-review", false)
	require.ErrorIs(t, err, pricing.ErrModelPricingUnavailable)
}

// TestPrepareCompletionKeepsTurnIdentity captures two child request contexts
// beneath one connection context. Each completion receives its child request
// ID and response billing key, while the Gin request continues to carry the
// connection ID.
func TestPrepareCompletionKeepsTurnIdentity(t *testing.T) {
	root := telemetry.WithRequestCapture(context.WithValue(context.Background(), telemetry.RequestID, "connection"), telemetry.RequestRecord{RequestID: "connection", UserID: 2, APIKeyID: 1, StartedAt: time.Now(), State: "running"}, nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(root)
	target := &openAIWSEntryTarget{
		root:     &openAIWSEntryAdapter{bindings: Bindings{Common: openaiattempt.Bindings{Support: &openaiattempt.Support{}}}, c: c, key: &apikey.APIKey{ID: 1, UserID: 2, User: &identity.User{ID: 2}}},
		provider: &gatewayprovider.ExecutionProvider{Record: provider.Record{ID: 3, Platform: "openai"}},
	}
	for _, id := range []string{"first-turn", "second-turn"} {
		ctx := telemetry.WithChildRequest(root, telemetry.RequestRecord{RequestID: id, State: "running", StartedAt: time.Now()})
		result := &gatewayws.ForwardResult{RequestID: "response-" + id, Model: "model", OpenAIWSMode: true}
		input := target.PrepareCompletion(ctx, result, gatewayws.TurnCapture{Turn: 1, StartedAt: time.Now()}, "model", routing.GroupMappingResult{}, nil, false)
		require.Equal(t, id, input.RequestID)
		require.Equal(t, "response-"+id, input.BillingKey)
	}
	require.Equal(t, "connection", telemetry.RequestIDValue(c.Request.Context()))
}
