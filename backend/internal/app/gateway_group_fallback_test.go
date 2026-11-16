package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// gateway_group_fallback_test.go checks destination permissions and Token price
// protection before funding admission or session ownership changes. Fixtures
// make each phase observable through
// funding and RPM counts, captured isolation identity, and returned Key data,
// while rejected chains leave the caller's starting group intact.

// fallbackGroupRepository supplies one group or a map of fallback destinations.
type fallbackGroupRepository struct {
	// GroupRepository supplies unused methods; this fixture implements the
	// lightweight group read consumed by runtime group authorization in these tests.
	routing.GroupRepository
	// group is the shared single destination returned when groups is nil;
	// callers in these tests let the Key service copy it before changing group state.
	group *routing.Group
	// groups overrides group whenever configured, including returning nil
	// for a missing destination ID during fallback-chain authorization tests.
	groups map[int64]*routing.Group
}

// fallbackUserRepository supplies user access data for runtime group
// authorization.
type fallbackUserRepository struct {
	// UserRepository supplies unused methods; GetByID returns the configured
	// user for the destination permission checks exercised by the Key service.
	identity.UserRepository
	user *identity.User // shared fixture user
}

// fallbackFundingCheck records admission input and returns a configured denial.
type fallbackFundingCheck struct {
	// calls and input capture each Check invocation before denied is returned.
	// Tests compare this state to locate authorization and isolation relative to admission.
	calls  int                // funding invocation count
	input  billing.CheckInput // latest admission input
	denied error              // configured funding denial
}

// fallbackSubscriptionRepository supplies the preferred subscription used by
// fallback checks.
type fallbackSubscriptionRepository struct {
	// UserSubscriptionRepository supplies unused methods; GetByID provides
	// the preferred subscription needed by production group-coverage resolution.
	billing.UserSubscriptionRepository
	value *billing.UserSubscription // shared subscription fixture
}

// fallbackIsolationCache records session namespace writes and supplies an
// existing owner.
type fallbackIsolationCache struct {
	// GatewayCache supplies unused methods; this fixture implements ownership
	// claim, read, and refresh calls used by session isolation during fallback.
	session.GatewayCache
	// ownerID supplies the existing owner while userID captures the claim's user.
	// source and hash capture that same claim namespace for later test assertions.
	ownerID, userID int64  // ownerID: existing owner group; userID: claiming user identity
	source, hash    string // source: claim namespace category; hash: claim session identifier
}

// clientFallbackRPM counts request admission calls during client fallback
// traversal.
type clientFallbackRPM struct {
	calls int // RPM admission count
}

// GetByIDLite returns the mapped group when groups is configured, otherwise
// group. Returned pointers are the fixture's stored values, and an absent
// mapped ID produces nil without an error.
func (r fallbackGroupRepository) GetByIDLite(_ context.Context, id int64) (*routing.Group, error) {
	if r.groups != nil {
		return r.groups[id], nil
	}
	return r.group, nil
}

// GetByID returns the configured user pointer and ignores the requested ID. The
// fixture shares that user with its callers, letting the production Key service
// perform destination authorization.
func (r fallbackUserRepository) GetByID(context.Context, int64) (*identity.User, error) {
	return r.user, nil
}

// Check records the latest input and increments the funding-call count. It
// returns the configured denial after recording, allowing tests to check
// whether authorization or subscription rejection occurred before admission.
func (f *fallbackFundingCheck) Check(_ context.Context, input billing.CheckInput) error {
	f.calls++
	f.input = input
	return f.denied
}

// GetByID returns the fixture's preferred subscription pointer regardless of
// requested ID. The production subscription resolver checks that supplied
// subscription's payer and group coverage in these scenarios.
func (r fallbackSubscriptionRepository) GetByID(context.Context, int64) (*billing.UserSubscription, error) {
	return r.value, nil
}

// SetSessionOwnerGroupID records user, source, and hash but reports an existing
// owner. The requested group and TTL are ignored so production isolation
// proceeds to the fixture's separate owner read.
func (c *fallbackIsolationCache) SetSessionOwnerGroupID(_ context.Context, userID int64, source, hash string, groupID int64, _ time.Duration) (bool, error) {
	c.userID, c.source, c.hash = userID, source, hash
	return false, nil
}

// GetSessionOwnerGroupID returns the configured existing owner for isolation
// comparison. It ignores namespace arguments; SetSessionOwnerGroupID captures
// those arguments separately for the tests to assert.
func (c *fallbackIsolationCache) GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error) {
	return c.ownerID, nil
}

// RefreshSessionOwnerTTL accepts the fixture refresh call without changing
// stored ownership.
func (c *fallbackIsolationCache) RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error {
	return nil
}

// TestRuntimeGroupFallbackRechecksAuthorizationBeforeFunding rejects exclusive,
// disabled-public, protocol-incompatible, and inactive destinations. Each
// rejection must return no Key or subscription and leave the funding checker
// uncalled.
func TestRuntimeGroupFallbackRechecksAuthorizationBeforeFunding(t *testing.T) {
	for _, restriction := range []string{"exclusive", "disabled_public", "protocol", "inactive"} {
		t.Run(restriction, func(t *testing.T) {
			user := &identity.User{ID: 7}
			group := &routing.Group{ID: 20, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}
			switch restriction {
			case "exclusive":
				group.IsExclusive = true
			case "disabled_public":
				user.DisabledPublicGroups = []int64{20}
			case "protocol":
				group.AllowedProtocols = []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}
			case "inactive":
				group.Status = "inactive"
			}
			keys := testkit.NewService(nil, fallbackUserRepository{user: user}, fallbackGroupRepository{group: group}, nil, nil, nil, nil)
			funds := &fallbackFundingCheck{}
			resolver := provideRuntimeGroupFallbackResolver(keys, admission.NewFundingAdmission(funds, nil), nil, nil)
			key, sub, err := resolver(context.Background(), &apikey.APIKey{UserID: 7, User: user}, 20, protocol.ProtocolAnthropicMessages)
			require.Error(t, err)
			require.Nil(t, key)
			require.Nil(t, sub)
			require.Zero(t, funds.calls)
		})
	}
}

// TestRuntimeGroupFallbackRejectsHigherTokenRate selects an authorized group
// whose current price exceeds the credential ceiling. It rejects the fallback
// before funding and leaves the caller's starting group unchanged.
func TestRuntimeGroupFallbackRejectsHigherTokenRate(t *testing.T) {
	user := &identity.User{ID: 7}
	target := &routing.Group{ID: 20, Status: "active", RateMultiplier: .081, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}
	keys := testkit.NewService(nil, fallbackUserRepository{user: user}, fallbackGroupRepository{group: target}, nil, nil, nil, nil)
	t.Cleanup(keys.Stop)
	funds := &fallbackFundingCheck{}
	resolver := provideRuntimeGroupFallbackResolver(keys, admission.NewFundingAdmission(funds, nil), nil, nil)
	groupID := int64(10)
	original := &apikey.APIKey{UserID: 7, User: user, GroupID: &groupID, MaxTokenRateMultiplier: .08}
	resolved, subscription, err := resolver(t.Context(), original, 20, protocol.ProtocolAnthropicMessages)
	require.ErrorIs(t, err, apikey.ErrTokenRateLimitExceeded)
	require.Nil(t, resolved)
	require.Nil(t, subscription)
	require.Zero(t, funds.calls)
	require.Equal(t, int64(10), *original.GroupID)
}

// TestRuntimeGroupFallbackChecksSelectedSubscriptionCoverage supplies a
// preferred subscription covering another group. Resolution must return the
// specific coverage error before calling the funding checker for the target.
func TestRuntimeGroupFallbackChecksSelectedSubscriptionCoverage(t *testing.T) {
	user := &identity.User{ID: 7}
	target := &routing.Group{ID: 20, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}
	keys := testkit.NewService(nil, fallbackUserRepository{user: user}, fallbackGroupRepository{group: target}, nil, nil, nil, nil)
	subscriptionID := int64(99)
	subscription := &billing.UserSubscription{ID: subscriptionID, UserID: 7, Status: "active", StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), Plan: &billing.SubscriptionPlan{GroupIDs: []int64{10}}}
	subscriptions := billing.NewSubscriptionService(nil, fallbackSubscriptionRepository{value: subscription}, nil)
	funds := &fallbackFundingCheck{}
	resolver := provideRuntimeGroupFallbackResolver(keys, admission.NewFundingAdmission(funds, nil), subscriptions, nil)
	_, _, err := resolver(context.Background(), &apikey.APIKey{UserID: 7, User: user, BillingMode: "subscription", PreferredSubscriptionID: &subscriptionID}, 20, protocol.ProtocolAnthropicMessages)
	require.ErrorIs(t, err, billing.ErrPreferredSubscriptionGroup)
	require.Zero(t, funds.calls)
}

// TestRuntimeGroupFallbackPreservesSessionNamespace resolves a destination
// while original owner and billing user differ. It checks target funding data,
// original session namespace, unchanged input group, and either isolation
// conflict or the authorized destination Key.
func TestRuntimeGroupFallbackPreservesSessionNamespace(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		t.Run(map[bool]string{false: "allowed", true: "isolated"}[isolated], func(t *testing.T) {
			user := &identity.User{ID: 7}
			target := &routing.Group{ID: 20, Status: "active", SessionIsolationEnabled: isolated, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}
			keys := testkit.NewService(nil, fallbackUserRepository{user: user}, fallbackGroupRepository{group: target}, nil, nil, nil, nil)
			funds := &fallbackFundingCheck{}
			cache := &fallbackIsolationCache{ownerID: 10}
			resolver := provideRuntimeGroupFallbackResolver(keys, admission.NewFundingAdmission(funds, nil), nil, cache)
			ctx := requeststate.WithSessionIsolation(context.Background(), session.SessionIsolationSourceGateway, "explicit-session")
			originalGroupID := int64(10)
			original := &apikey.APIKey{ID: 1, UserID: 7, User: &identity.User{ID: 8}, GroupID: &originalGroupID, Group: &routing.Group{ID: 10}, BillingMode: "auto"}
			resolved, subscription, err := resolver(ctx, original, 20, protocol.ProtocolAnthropicMessages)
			require.Equal(t, 1, funds.calls)
			require.Equal(t, int64(20), funds.input.Group.ID)
			require.Equal(t, int64(7), funds.input.Payer.ID)
			require.Equal(t, int64(8), cache.userID)
			require.Equal(t, session.SessionIsolationSourceGateway, cache.source)
			require.Equal(t, "explicit-session", cache.hash)
			require.Equal(t, int64(10), *original.GroupID)
			if isolated {
				require.ErrorIs(t, err, session.ErrSessionIsolationConflict)
				require.Nil(t, resolved)
			} else {
				require.NoError(t, err)
				require.Nil(t, subscription)
				require.Equal(t, int64(20), *resolved.GroupID)
				require.Equal(t, int64(7), resolved.User.ID)
			}
		})
	}
}

// TestRuntimeGroupFallbackStopsOnFundingDenial injects a billing limit
// rejection after destination authorization. The returned Key and subscription
// must be nil, and session-cache access must not begin after that denial.
func TestRuntimeGroupFallbackStopsOnFundingDenial(t *testing.T) {
	user := &identity.User{ID: 7}
	target := &routing.Group{ID: 20, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}
	keys := testkit.NewService(nil, fallbackUserRepository{user: user}, fallbackGroupRepository{group: target}, nil, nil, nil, nil)
	funds := &fallbackFundingCheck{denied: billing.ErrAPIKeyRateLimit1dExceeded}
	cache := &fallbackIsolationCache{ownerID: 10}
	resolver := provideRuntimeGroupFallbackResolver(keys, admission.NewFundingAdmission(funds, nil), nil, cache)
	ctx := requeststate.WithSessionIsolation(context.Background(), session.SessionIsolationSourceGateway, "explicit-session")
	resolved, subscription, err := resolver(ctx, &apikey.APIKey{ID: 1, UserID: 7, User: user, BillingMode: "balance"}, 20, protocol.ProtocolAnthropicMessages)
	require.ErrorIs(t, err, billing.ErrAPIKeyRateLimit1dExceeded)
	require.Nil(t, resolved)
	require.Nil(t, subscription)
	require.Equal(t, 1, funds.calls)
	require.Zero(t, cache.userID)
}

// Check increments the fixture RPM count and allows the request.
func (r *clientFallbackRPM) Check(context.Context, *scheduler.RPMUser, *scheduler.RPMGroup) error {
	r.calls++
	return nil
}

// TestClientGroupFallbackAuthorizesWholeChain traverses a three-group client
// restriction chain under multiple destination failures. It checks every
// successful hop's funding count, zero RPM calls, the allowed final group, and
// the unchanged input Key group.
func TestClientGroupFallbackAuthorizesWholeChain(t *testing.T) {
	for _, outcome := range []string{"allowed", "exclusive", "protocol", "subscription", "cycle", "composite", "missing_target"} {
		t.Run(outcome, func(t *testing.T) {
			firstID, secondID, thirdID := int64(1), int64(2), int64(3)
			allowed := []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}
			first := &routing.Group{ID: firstID, Status: "active", AllowedProtocols: allowed, ClaudeCodeOnly: true, FallbackGroupID: &secondID}
			second := &routing.Group{ID: secondID, Status: "active", AllowedProtocols: allowed, ClaudeCodeOnly: true, FallbackGroupID: &thirdID}
			third := &routing.Group{ID: thirdID, Status: "active", AllowedProtocols: allowed}
			user := &identity.User{ID: 7}
			key := &apikey.APIKey{ID: 8, UserID: 7, User: user, GroupID: &firstID, Group: first, BillingMode: "balance"}
			var subscriptions *billing.SubscriptionService
			switch outcome {
			case "exclusive":
				second.IsExclusive = true
			case "protocol":
				second.AllowedProtocols = []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}
			case "subscription":
				subID := int64(99)
				key.BillingMode, key.PreferredSubscriptionID = "subscription", &subID
				subscriptions = billing.NewSubscriptionService(nil, fallbackSubscriptionRepository{value: &billing.UserSubscription{ID: subID, UserID: 7, Plan: &billing.SubscriptionPlan{GroupIDs: []int64{firstID}}}}, nil)
			case "cycle":
				second.FallbackGroupID = &firstID
			case "composite":
				key.IsComposite = true
				key.CompositeGroups = []apikey.APIKeyCompositeGroup{{GroupID: firstID}}
			case "missing_target":
				second.FallbackGroupID = nil
			}
			repo := fallbackGroupRepository{groups: map[int64]*routing.Group{firstID: first, secondID: second, thirdID: third}}
			keys := testkit.NewService(nil, fallbackUserRepository{user: user}, repo, nil, nil, nil, nil)
			funds, rpm := &fallbackFundingCheck{}, &clientFallbackRPM{}
			resolve := provideClientGroupFallbackResolver(keys, admission.NewFundingAdmission(funds, rpm), subscriptions, nil)
			resolved, _, err := resolve(context.Background(), key, protocol.ProtocolOpenAIResponses)
			require.Zero(t, rpm.calls)
			require.Equal(t, firstID, *key.GroupID)
			if outcome == "allowed" {
				require.NoError(t, err)
				require.Equal(t, thirdID, *resolved.GroupID)
				require.Equal(t, 2, funds.calls)
			} else {
				require.Error(t, err)
				require.Nil(t, resolved)
			}
		})
	}
}
