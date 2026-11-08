package apikey

import (
	"context"
	"fmt"
	"math"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// token_rate_limit.go resolves the highest group Token rate available to a
// credential's funding sources and enforces its configured ceiling before
// upstream dispatch. Balance uses the payer's group override, subscriptions
// use their plan rate, and both receive the group's current Token peak factor.
// The group is reloaded so a cached credential cannot retain an earlier price.
//
// A subscription-funded request can spend balance after its allowance runs out
// during processing. Protection therefore includes the balance rate as well
// as eligible subscription rates. Console previews use the same calculation
// across permitted groups. Unconfigured ceilings skip all pricing reads;
// configured ceilings reject pricing-read failures because admission cannot
// establish an acceptable rate.

// Token rate errors distinguish a configured price ceiling from unavailable
// pricing observations so gateway adapters can report rejection or temporary failure.
var (
	ErrTokenRateLimitExceeded = apperror.Forbidden("API_KEY_TOKEN_RATE_LIMIT_EXCEEDED", "The group's effective Token multiplier exceeds this API key's limit")
	ErrTokenRateUnavailable   = apperror.ServiceUnavailable("API_KEY_TOKEN_RATE_UNAVAILABLE", "Unable to verify the group's effective Token multiplier")
)

// CheckTokenRateLimit rejects effective group Token rates above a positive
// credential ceiling. Zero disables protection, and callers provide the final
// authorized group before forwarding or reserving request capacity.
// @project-doc docs/domains/routing_and_billing.md#api_key_token_rate_limit
func (s *APIKeyService) CheckTokenRateLimit(ctx context.Context, key *APIKey) error {
	if key == nil || key.MaxTokenRateMultiplier == 0 {
		return nil
	}
	if err := ValidateAPIKeyLimit("max_token_rate_multiplier", key.MaxTokenRateMultiplier); err != nil {
		return err
	}
	rate, err := s.effectiveTokenRate(ctx, key)
	if err != nil {
		return ErrTokenRateUnavailable.WithCause(err)
	}
	// Relative tolerance permits equivalent decimal rates after floating-point multiplication.
	excess := rate - key.MaxTokenRateMultiplier
	if excess > 1e-12*math.Max(rate, key.MaxTokenRateMultiplier) {
		return ErrTokenRateLimitExceeded.WithMetadata(map[string]string{
			"actual_multiplier": fmt.Sprintf("%.12g", rate),
			"max_multiplier":    fmt.Sprintf("%.12g", key.MaxTokenRateMultiplier),
		})
	}
	return nil
}

// effectiveTokenRate resolves the greatest payer rate for the selected group.
// The supplied key needs payer and group identities. Current repository reads
// replace cached group prices, and unavailable observations return an error.
//
// Balance remains a possible funding source after subscription quota exhaustion,
// including for a preferred subscription. Automatic funding considers effective
// subscriptions covering this group; specified funding considers its selected
// subscription. A missing plan override inherits the group's base rate. The
// current peak factor scales the greatest source rate once, matching the
// multiplier used for Token settlement rather than provider cost statistics.
func (s *APIKeyService) effectiveTokenRate(ctx context.Context, key *APIKey) (float64, error) {
	if s == nil || s.groupRepo == nil || key.User == nil || key.GroupID == nil {
		return 0, ErrGroupNotAllowed
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *key.GroupID)
	if err != nil {
		return 0, err
	}
	if group == nil || !group.IsActive() {
		return 0, ErrGroupNotAllowed
	}
	rate := group.RateMultiplier
	if s.userGroupRateRepo != nil {
		override, err := s.userGroupRateRepo.GetByUserAndGroup(ctx, key.User.ID, group.ID)
		if err != nil {
			return 0, err
		}
		if override != nil {
			rate = *override
		}
	}
	mode := APIKeyEffectiveBillingMode(key)
	if s.userSubRepo != nil && mode != APIKeyBillingModeBalance {
		var subscriptions []UserSubscription
		if mode == APIKeyBillingModeSubscription {
			if key.PreferredSubscriptionID == nil {
				return 0, ErrPreferredSubscriptionInvalid
			}
			subscription, err := s.userSubRepo.GetByID(ctx, *key.PreferredSubscriptionID)
			if err != nil {
				return 0, err
			}
			if subscription != nil && subscription.UserID == key.User.ID {
				subscriptions = append(subscriptions, *subscription)
			}
		} else {
			subscriptions, err = s.userSubRepo.ListActiveByUserID(ctx, key.User.ID)
			if err != nil {
				return 0, err
			}
		}
		for i := range subscriptions {
			subscription := &subscriptions[i]
			if !subscription.IsEffective() || !billing.SubscriptionAllowsGroup(subscription, group.ID) {
				continue
			}
			// Plans without a positive group override inherit the group rate, keeping
			// preview and admission consistent with subscription settlement allocation.
			planRate := group.RateMultiplier
			if subscription.Plan != nil {
				if configured := subscription.Plan.GroupRateMultipliers[group.ID]; configured > 0 {
					planRate = configured
				}
			}
			rate = math.Max(rate, planRate)
		}
	}
	if s.cfg != nil && s.cfg.TokenRateScale != nil {
		scale, err := s.cfg.TokenRateScale(ctx, group.ID, s.now())
		if err != nil {
			return 0, err
		}
		rate *= scale
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return 0, ErrAPIKeyLimitInvalid
	}
	return rate, nil
}

// TokenRatesForScope returns current effective Token rates for payer-permitted
// groups under the requested funding mode. Team scope uses its owner, and
// subscription scope validates ownership and group coverage before previewing.
// All group reads use the payer resolved at the start of this operation.
func (s *APIKeyService) TokenRatesForScope(ctx context.Context, userID int64, scope, mode string, subscriptionID *int64) (map[int64]float64, error) {
	payerID, err := s.KeyBillingUserIDForScope(ctx, userID, scope)
	if err != nil {
		return nil, err
	}
	mode, subscriptionID, subscription, err := s.KeyResolveAPIKeyBillingConfiguration(ctx, payerID, mode, subscriptionID)
	if err != nil {
		return nil, err
	}
	groups, err := s.GetAvailableGroups(ctx, payerID)
	if err != nil {
		return nil, err
	}
	rates := make(map[int64]float64, len(groups))
	for i := range groups {
		if subscription != nil && !billing.SubscriptionAllowsGroup(subscription, groups[i].ID) {
			continue
		}
		key := &APIKey{GroupID: &groups[i].ID, User: &User{ID: payerID}, BillingMode: mode, PreferredSubscriptionID: subscriptionID}
		rate, err := s.effectiveTokenRate(ctx, key)
		if err != nil {
			return nil, err
		}
		rates[groups[i].ID] = rate
	}
	return rates, nil
}
