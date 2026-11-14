package apikey

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// token_rate_limit_test.go verifies credential price protection using mutable
// group and funding fixtures. Admission must reject increased rates before
// request reservation, preserve an exact decimal ceiling after multiplication,
// and include subscription and possible balance funding in its price bound.

// tokenLimitGroups supplies mutable group prices for isolated admission scenarios.
type tokenLimitGroups struct {
	GroupRepository                // unused group operations
	group           *routing.Group // current stored group
	err             error          // configured lookup failure
	calls           int            // observed group reads
}

// tokenLimitRates supplies payer-specific rates independently of group defaults.
type tokenLimitRates struct {
	billing.UserGroupRateRepository          // unused rate operations
	rate                            *float64 // optional payer override
	err                             error    // configured lookup failure
}

// tokenLimitSubscriptions supplies current subscriptions for funding price comparisons.
type tokenLimitSubscriptions struct {
	UserSubscriptionRepository                    // unused subscription operations
	rows                       []UserSubscription // current payer grants
}

// GetByIDLite returns the fixture's current group and records each lookup.
func (s *tokenLimitGroups) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	s.calls++
	return s.group, s.err
}

// GetByUserAndGroup returns the configured payer rate or lookup failure.
func (s *tokenLimitRates) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	return s.rate, s.err
}

// ListActiveByUserID returns configured grants for automatic funding selection.
func (s *tokenLimitSubscriptions) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	return s.rows, nil
}

// GetByID returns the matching preferred grant from the fixture.
func (s *tokenLimitSubscriptions) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			return &s.rows[i], nil
		}
	}
	return nil, billing.ErrSubscriptionNotFound
}

// TestCheckTokenRateLimitBoundary protects a decimal ceiling across changing
// group prices. It also verifies rounding, disabled protection, and unavailable
// pricing before the request can acquire capacity.
func TestCheckTokenRateLimitBoundary(t *testing.T) {
	for _, tc := range []struct {
		name  string  // scenario display name
		rate  float64 // current group multiplier
		scale float64 // current peak factor
		cap   float64 // saved credential ceiling
		want  error   // expected rejection category
	}{
		{"equal", .08, 1, .08, nil},
		{"lower", .079, 1, .08, nil},
		{"higher", .081, 1, .08, ErrTokenRateLimitExceeded},
		{"decimal multiplication", .1, .8, .08, nil},
		{"peak increase", .08, 1.25, .08, ErrTokenRateLimitExceeded},
		{"disabled", 5, 1, 0, nil},
		{"negative cap", .08, 1, -.1, ErrAPIKeyLimitInvalid},
		{"nonfinite cap", .08, 1, math.Inf(1), ErrAPIKeyLimitInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups := &tokenLimitGroups{group: &routing.Group{ID: 3, Status: StatusActive, RateMultiplier: tc.rate}}
			service := NewAPIKeyService(nil, nil, groups, nil, nil, nil, &Options{
				TokenRateScale: func(context.Context, int64, time.Time) (float64, error) { return tc.scale, nil },
			})
			t.Cleanup(service.Stop)
			id := int64(3)
			key := &APIKey{GroupID: &id, User: &User{ID: 7}, MaxTokenRateMultiplier: tc.cap}
			err := service.CheckTokenRateLimit(t.Context(), key)
			require.ErrorIs(t, err, tc.want)
			if tc.cap == 0 {
				require.Zero(t, groups.calls)
			}
		})
	}
}

// TestCheckTokenRateLimitCurrentPrices changes stored group and payer rates
// after admission. Subsequent requests observe those prices, and lookup failures
// reject protected work instead of admitting an unverifiable rate.
func TestCheckTokenRateLimitCurrentPrices(t *testing.T) {
	groups := &tokenLimitGroups{group: &routing.Group{ID: 3, Status: StatusActive, RateMultiplier: .1}}
	rates := &tokenLimitRates{}
	service := NewAPIKeyService(nil, nil, groups, nil, rates, nil, nil)
	t.Cleanup(service.Stop)
	id := int64(3)
	key := &APIKey{GroupID: &id, User: &User{ID: 7}, MaxTokenRateMultiplier: .1, BillingMode: APIKeyBillingModeBalance}
	require.NoError(t, service.CheckTokenRateLimit(t.Context(), key))
	groups.group.RateMultiplier = .2
	err := service.CheckTokenRateLimit(t.Context(), key)
	require.ErrorIs(t, err, ErrTokenRateLimitExceeded)
	failure := apperror.FromError(err)
	require.Equal(t, "Token multiplier 0.2x exceeds API key limit 0.1x. Check group pricing or update the key limit.", failure.Message)
	require.Equal(t, map[string]string{"actual_multiplier": "0.2", "max_multiplier": "0.1"}, failure.Metadata)
	require.Equal(t, .1, key.MaxTokenRateMultiplier)
	require.Equal(t, "The group's effective Token multiplier exceeds this API key's limit", ErrTokenRateLimitExceeded.Message)
	override := .1
	rates.rate = &override
	require.NoError(t, service.CheckTokenRateLimit(t.Context(), key))
	rates.err = errors.New("rate store unavailable")
	require.ErrorIs(t, service.CheckTokenRateLimit(t.Context(), key), ErrTokenRateUnavailable)
	rates.err = nil
	groups.err = errors.New("group store unavailable")
	_, release, _, err := service.AcquireRequest(t.Context(), key)
	require.ErrorIs(t, err, ErrTokenRateUnavailable)
	require.Nil(t, release)
}

// TestCheckTokenRateLimitFunding includes rates from possible funding sources.
// Automatic and specified subscription work is protected from expensive plan
// rates and from a more expensive balance rate after quota exhaustion.
func TestCheckTokenRateLimitFunding(t *testing.T) {
	groups := &tokenLimitGroups{group: &routing.Group{ID: 3, Status: StatusActive, RateMultiplier: .08}}
	subs := &tokenLimitSubscriptions{rows: []UserSubscription{{
		ID: 9, UserID: 7, Status: billing.SubscriptionStatusActive,
		StartsAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour),
		Plan: &billing.SubscriptionPlan{GroupIDs: []int64{3}, GroupRateMultipliers: map[int64]float64{3: .1}},
	}}}
	service := NewAPIKeyService(nil, nil, groups, subs, nil, nil, nil)
	t.Cleanup(service.Stop)
	id, subID := int64(3), int64(9)
	key := &APIKey{GroupID: &id, User: &User{ID: 7}, MaxTokenRateMultiplier: .08}
	require.ErrorIs(t, service.CheckTokenRateLimit(t.Context(), key), ErrTokenRateLimitExceeded)
	key.BillingMode = APIKeyBillingModeBalance
	require.NoError(t, service.CheckTokenRateLimit(t.Context(), key))
	key.BillingMode = APIKeyBillingModeSubscription
	key.PreferredSubscriptionID = &subID
	require.ErrorIs(t, service.CheckTokenRateLimit(t.Context(), key), ErrTokenRateLimitExceeded)
	subs.rows[0].Plan.GroupRateMultipliers[3] = .01
	groups.group.RateMultiplier = .09
	require.ErrorIs(t, service.CheckTokenRateLimit(t.Context(), key), ErrTokenRateLimitExceeded)
	groups.group.RateMultiplier = .08
	require.NoError(t, service.CheckTokenRateLimit(t.Context(), key))
}
