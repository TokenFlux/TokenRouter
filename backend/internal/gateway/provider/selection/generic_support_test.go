package selection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestGatewayHotpathHelpers_CacheTTLAndStickyContext supplies present, missing,
// and mismatched request prefetch hints. Sticky identifiers and cost
// observations are reused only for their matching group or provider keys.
func TestGatewayHotpathHelpers_CacheTTLAndStickyContext(t *testing.T) {
	t.Run("prefetched_sticky_provider_id_from_context", func(t *testing.T) {
		require.Equal(t, int64(0), prefetchedStickyProviderIDFromContext(context.TODO(), nil))
		require.Equal(t, int64(0), prefetchedStickyProviderIDFromContext(context.Background(), nil))

		ctx := requeststate.WithPrefetchedStickySession(context.Background(), 123, 0)
		require.Equal(t, int64(123), prefetchedStickyProviderIDFromContext(ctx, nil))

		groupID := int64(9)
		ctx2 := requeststate.WithPrefetchedStickySession(context.Background(), 456, groupID)
		require.Equal(t, int64(456), prefetchedStickyProviderIDFromContext(ctx2, &groupID))

		ctx3 := requeststate.WithExecutionHints(context.Background(), requeststate.ExecutionHints{
			PrefetchedStickyGroupID: requeststate.Hint[int64]{Value: groupID, Set: true},
		})
		require.Equal(t, int64(0), prefetchedStickyProviderIDFromContext(ctx3, &groupID))

		ctx4 := requeststate.WithPrefetchedStickySession(context.Background(), 789, 10)
		require.Equal(t, int64(0), prefetchedStickyProviderIDFromContext(ctx4, &groupID))
	})

	t.Run("window_cost_from_prefetch_context", func(t *testing.T) {
		require.Equal(t, false, func() bool {
			_, ok := billing.PrefetchedWindowCost(context.TODO(), 0)
			return ok
		}())
		require.Equal(t, false, func() bool {
			_, ok := billing.PrefetchedWindowCost(context.Background(), 1)
			return ok
		}())

		ctx := billing.WithPrefetchedWindowCosts(context.Background(), map[int64]float64{
			9: 12.34,
		})
		cost, ok := billing.PrefetchedWindowCost(ctx, 9)
		require.True(t, ok)
		require.Equal(t, 12.34, cost)
	})
}

// TestShouldClearStickySession varies provider status, expiry, model cooldown,
// and quota configuration. Binding invalidation follows schedulability and the
// requested model's active cooldown, while nil and inapplicable OAuth quota
// cases remain usable.
func TestShouldClearStickySession(t *testing.T) {
	now := time.Now()
	future := now.Add(1 * time.Hour)
	past := now.Add(-1 * time.Hour)

	// A short cooldown still clears stickiness.
	shortRateLimitReset := now.Add(5 * time.Second).Format(time.RFC3339)
	// A long cooldown clears stickiness.
	longRateLimitReset := now.Add(30 * time.Second).Format(time.RFC3339)

	tests := []struct {
		name           string                             // named clearing scenario
		provider       *gatewayprovider.ExecutionProvider // provider eligibility fixture
		requestedModel string                             // requested cooldown identity
		want           bool                               // expected binding invalidation
	}{
		{name: "nil provider", provider: nil, requestedModel: "", want: false},
		{name: "status error", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: providercore.StatusError, Schedulable: true}}, requestedModel: "", want: true},
		{name: "status disabled", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusDisabled, Schedulable: true}}, requestedModel: "", want: true},
		{name: "schedulable false", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive, Schedulable: false}}, requestedModel: "", want: true},
		{name: "temp unschedulable", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive, Schedulable: true, TempUnschedulableUntil: &future}}, requestedModel: "", want: true},
		{name: "temp unschedulable expired", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive, Schedulable: true, TempUnschedulableUntil: &past}}, requestedModel: "", want: false},
		{name: "active schedulable", provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive, Schedulable: true}}, requestedModel: "", want: false},
		// Any active cooldown for the requested model clears stickiness.
		{
			name: "model rate limited short duration",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive,
					Schedulable: true,
					Extra: map[string]any{
						"model_rate_limits": map[string]any{
							"claude-sonnet-4": map[string]any{
								"rate_limit_reset_at": shortRateLimitReset,
							},
						},
					},
				},
			},
			requestedModel: "claude-sonnet-4",
			want:           true, // An active cooldown clears stickiness.
		},
		{
			name: "model rate limited long duration",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive,
					Schedulable: true,
					Extra: map[string]any{
						"model_rate_limits": map[string]any{
							"claude-sonnet-4": map[string]any{
								"rate_limit_reset_at": longRateLimitReset,
							},
						},
					},
				},
			},
			requestedModel: "claude-sonnet-4",
			want:           true, // An active cooldown clears stickiness.
		},
		{
			name: "model rate limited different model",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive,
					Schedulable: true,
					Extra: map[string]any{
						"model_rate_limits": map[string]any{
							"claude-sonnet-4": map[string]any{
								"rate_limit_reset_at": longRateLimitReset,
							},
						},
					},
				},
			},
			requestedModel: "claude-opus-4", // Request a different model.
			want:           false,           // The other model's cooldown does not apply.
		},
		{
			name: "apikey quota exceeded",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive,
					Schedulable: true,
					Type:        capability.ProviderTypeAPIKey,
					Extra: map[string]any{
						"quota_daily_limit": 10.0,
						"quota_daily_used":  10.0,
						"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			requestedModel: "",
			want:           true,
		},
		{
			name: "oauth quota exceeded not cleared",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive,
					Schedulable: true,
					Type:        capability.ProviderTypeOAuth,
					Extra: map[string]any{
						"quota_daily_limit": 10.0,
						"quota_daily_used":  10.0,
						"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			requestedModel: "",
			want:           false,
		},
		{
			name: "overloaded provider",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive,
					Schedulable:   true,
					OverloadUntil: &future,
				},
			},
			requestedModel: "",
			want:           true,
		},
		{
			name: "provider-level rate limited",
			provider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					Credentials: map[string]any{"model_whitelist": []string{"*"}}, LoadLocation: time.LoadLocation, Status: billing.StatusActive,
					Schedulable:      true,
					RateLimitResetAt: &future,
				},
			},
			requestedModel: "",
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldClearStickySession(t.Context(), tt.provider, tt.requestedModel))
		})
	}
}

// TestShouldClearStickySession_RespectsEffortMappedCooldown cools only the
// high-effort mapped model on a provider. Sticky invalidation must clear a
// high-effort request context while keeping a low-effort request sticky.
func TestShouldClearStickySession_RespectsEffortMappedCooldown(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			ID:           77,
			Platform:     capability.PlatformAnthropic,
			Type:         capability.ProviderTypeAPIKey,
			Status:       billing.StatusActive,
			Schedulable:  true,
			Now:          time.Now,
			LoadLocation: time.LoadLocation,
			Credentials: map[string]any{
				"model_whitelist": []string{"claude-opus-5-5-low", "claude-opus-5-5-high"},
				providercore.EffortModelMappingCredentialKey: map[string]any{
					"claude-opus-5-5": map[string]any{
						"low":  "claude-opus-5-5-low",
						"high": "claude-opus-5-5-high",
					},
				},
			},
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					"claude-opus-5-5-high": map[string]any{
						"rate_limit_reset_at": future,
					},
				},
			},
		},
	}

	highCtx := requeststate.WithRoutingReasoningEffort(t.Context(), "high")
	lowCtx := requeststate.WithRoutingReasoningEffort(t.Context(), "low")
	require.True(t, shouldClearStickySession(highCtx, provider, "claude-opus-5-5"))
	require.False(t, shouldClearStickySession(lowCtx, provider, "claude-opus-5-5"))
}
