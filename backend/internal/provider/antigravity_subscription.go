package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// antigravity_subscription.go combines registered tier and eligibility warnings
// into Antigravity subscription display fields.
// Antigravity tier discovery can return paid status together with eligibility
// warnings. Normalization keeps a paid plan visible, marks the subscription
// abnormal, and surfaces the first warning while an absent response supplies
// the Free display value.
//
// GetTier chooses paid metadata ahead of current-tier metadata.
// IneligibleTiers changes an empty or Free plan to Abnormal, but an
// unrecognized nonempty plan keeps its supplied spelling. Only the first entry
// supplies the trimmed diagnostic, and a nil first entry still marks the
// subscription abnormal. Callers use these fields together when assembling
// authorization credentials and quota display.

// antigravitySubscriptionAbnormal marks discovery warnings in normalized
// credentials, even when the paid plan remains known.
const antigravitySubscriptionAbnormal = "abnormal"

// AntigravitySubscriptionResult pairs display plan identity with normalized
// eligibility warnings.
type AntigravitySubscriptionResult struct {
	// PlanType remains the discovered paid label when SubscriptionStatus marks
	// ineligibility. SubscriptionError carries the first diagnostic, so consumers
	// interpret plan and warning fields together.
	PlanType           string // normalized subscription plan label
	SubscriptionStatus string // abnormal eligibility status label
	SubscriptionError  string // first trimmed eligibility diagnostic
}

// NormalizeAntigravitySubscription combines discovered tier and eligibility
// warnings into display state. A nil response returns Free; other responses
// use GetTier and the platform plan mapper before examining ineligibility.
//
// Any ineligible entry marks SubscriptionStatus abnormal. An empty or Free
// plan becomes Abnormal, while paid and other nonempty plan labels remain. The
// first entry alone supplies SubscriptionError when nonnil, so callers can
// display a known paid plan alongside its separate eligibility problem.
func NormalizeAntigravitySubscription(resp *google.AntigravityLoadCodeAssistResponse) AntigravitySubscriptionResult {
	if resp == nil {
		return AntigravitySubscriptionResult{PlanType: "Free"}
	}
	tierID := resp.GetTier()
	planType := google.AntigravityTierIDToPlanType(tierID)
	// Discovery can report a paid tier alongside ineligibility. Retain that tier
	// while marking the warning separately, and use Abnormal when Free supplies
	// no paid plan identity.
	if len(resp.IneligibleTiers) > 0 {
		if planType == "" || planType == "Free" {
			planType = "Abnormal"
		}
		result := AntigravitySubscriptionResult{
			PlanType:           planType,
			SubscriptionStatus: antigravitySubscriptionAbnormal,
		}
		if resp.IneligibleTiers[0] != nil {
			result.SubscriptionError = strings.TrimSpace(resp.IneligibleTiers[0].ReasonMessage)
		}
		return result
	}
	return AntigravitySubscriptionResult{
		PlanType: planType,
	}
}

// ObservedAntigravityPlanType maps a nonblank upstream SubscriptionTierRaw from
// UsageInfo to its display plan label. Empty or nil observations return empty
// so failed LoadCodeAssist queries do not overwrite stored paid plans with Free.
func ObservedAntigravityPlanType(info *UsageInfo) string {
	if info == nil {
		return ""
	}
	raw := strings.TrimSpace(info.SubscriptionTierRaw)
	if raw == "" {
		return ""
	}
	return google.AntigravityTierIDToPlanType(raw)
}

// PersistAntigravityObservedPlan updates stored and in-memory plan_type
// credentials for an independent Antigravity provider when planType is nonblank
// and differs case-insensitively from the current value. Nil stores, shadow
// providers, blank plans, and storage errors leave the caller record unchanged.
func PersistAntigravityObservedPlan(ctx context.Context, repo OpenAIPlanWriter, provider *Record, planType string, info, warn func(string, ...any)) {
	if repo == nil || provider == nil || provider.Platform != PlatformAntigravity {
		return
	}
	// Credential shadows inherit plan metadata from their parent provider.
	if provider.IsCredentialShadow() {
		return
	}

	planType = strings.TrimSpace(planType)
	if planType == "" {
		return
	}

	current := strings.TrimSpace(provider.GetCredential("plan_type"))
	if strings.EqualFold(current, planType) {
		return
	}

	// Persist before mutating the caller record so failed storage leaves the
	// in-memory credentials unchanged.
	if _, err := repo.BulkUpdate(ctx, []int64{provider.ID}, ProviderBulkUpdate{
		Credentials: map[string]any{"plan_type": planType},
	}); err != nil {
		if warn != nil {
			warn("antigravity_plan_type_sync_failed", "provider_id", provider.ID, "plan_type", planType, "error", err)
		}
		return
	}

	if provider.Credentials == nil {
		provider.Credentials = make(map[string]any, 1)
	}
	provider.Credentials["plan_type"] = planType
	if info != nil {
		info("antigravity_plan_type_synced", "provider_id", provider.ID, "previous_plan_type", current, "plan_type", planType)
	}
}
