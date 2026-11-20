package provider

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"sync"
	"time"
)

// gemini_quota_policy.go assembles Gemini quota policies from tier defaults
// and ordered configuration overrides.
// Gemini quota policy starts with local tier defaults, then applies static
// tier overrides, static policy JSON, and dynamic settings in that order. New
// quota-rule objects supersede legacy tier objects when their rule map is
// nonempty; absent and malformed settings retain the policy assembled so far.
//
// Policy cache publication is locked, while settings load and construction
// occur outside the lock. Concurrent misses can therefore load independently,
// and the last completion publishes its policy; returned copies keep consumer
// mutation separate from that cached map. Positive shared limits override
// class limits in prechecks, while nonpositive limits expose class values.
// Legacy Pro overrides can change that shared gate before Flash overrides are
// applied. Cooldown zero is stored but lookup selects the five-minute fallback
// for any nonpositive cooldown.

// GeminiQuotaPolicySettingKey and geminiQuotaCacheTTL select the dynamic
// settings entry and the maximum reuse age for assembled quota policy
// observations.
const (
	GeminiQuotaPolicySettingKey = "gemini_quota_policy"
	geminiQuotaCacheTTL         = time.Minute
)

// GeminiTierQuotaOverride represents optional legacy daily limits and cooldown
// updates.
type GeminiTierQuotaOverride struct {
	// ProRPD, FlashRPD, and CooldownMinutes use pointer presence to distinguish
	// omitted updates from supplied zero values. Mutation helpers retain previous
	// values for nil inputs and apply supplied numeric values through their
	// limit-specific clamps.
	// optional legacy Pro daily limit
	ProRPD *int64 `json:"pro_rpd"`
	// optional legacy Flash daily limit
	FlashRPD *int64 `json:"flash_rpd"`
	// optional error cooldown minutes
	CooldownMinutes *int `json:"cooldown_minutes"`
}

// GeminiQuotaOptions supplies static policy input and dynamic settings/cache
// dependencies.
type GeminiQuotaOptions struct {
	// StaticTiers is copied, including numeric pointers, at construction.
	// It supplies the first override layer before either policy JSON source.
	StaticTiers map[string]GeminiTierQuotaOverride
	// StaticPolicy overrides the copied static tier layer when its JSON is
	// usable. Malformed text is diagnosed and leaves that earlier layer available.
	StaticPolicy string
	// LoadPolicy optionally supplies the final override layer on a cache miss.
	// Non-NotFound errors are logged; available text with a tolerated result is
	// parsed.
	LoadPolicy func(context.Context) (string, error)
	// NotFound identifies a tolerated loader error through errors.Is. A nil
	// marker tolerates only nil errors, while nonempty returned text may still be
	// applied.
	NotFound error
	// Now determines cache observation age and defaults to time.Now. The
	// publication timestamp is captured before settings loading rather than after
	// completion.
	Now func() time.Time
	// Log records load and parse failures and defaults to a no-op callback.
	// Such failures retain policy assembled from the preceding successful layers.
	Log func(string, ...any)
}

// GeminiQuotaService assembles layered quota settings and retains a copied
// cached policy.
type GeminiQuotaService struct {
	options GeminiQuotaOptions // captured policy construction dependencies
	// mu protects cachedAt and policy publication. Construction and settings
	// loading run outside the lock; readers clone the retained map before
	// returning it.
	mu       sync.Mutex         // cache publication synchronization mutex
	cachedAt time.Time          // cached observation start timestamp
	policy   *GeminiQuotaPolicy // retained assembled quota policy
}

// GeminiTierPolicy combines request quota settings with its upstream-error
// cooldown duration.
type GeminiTierPolicy struct {
	Quota    GeminiQuota   // configured tier request limits
	Cooldown time.Duration // upstream-error scheduling pause duration
}

// GeminiQuotaPolicy owns mutable quota and cooldown entries keyed by
// normalized tier.
type GeminiQuotaPolicy struct {
	// tiers is initialized by NewGeminiQuotaPolicy and copied by Clone.
	// Mutation helpers require that initialized map and caller synchronization
	// with reads.
	tiers map[string]GeminiTierPolicy
}

// geminiQuotaOverridesV1 decodes legacy tier overrides from a policy JSON
// object.
type geminiQuotaOverridesV1 struct {
	// legacy overrides by tier
	Tiers map[string]GeminiTierQuotaOverride `json:"tiers"`
}

// geminiQuotaOverridesV2 decodes current shared and per-class quota rule
// objects.
type geminiQuotaOverridesV2 struct {
	// current rules by tier
	QuotaRules map[string]GeminiQuotaRuleOverride `json:"quota_rules"`
}

// GeminiQuotaRuleOverride holds optional shared, model-class, and descriptive
// policy inputs.
type GeminiQuotaRuleOverride struct {
	// SharedRPD, SharedRPM, GeminiPro, and GeminiFlash use pointer presence
	// to distinguish omitted updates from supplied zero values. Mutation helpers
	// retain previous values for nil inputs and apply supplied numeric values
	// through their limit-specific clamps.
	// optional shared daily limit
	SharedRPD *int64 `json:"shared_rpd,omitempty"`
	// optional shared minute limit
	SharedRPM *int64 `json:"rpm,omitempty"`
	// optional Pro class overrides
	GeminiPro *GeminiModelQuotaOverride `json:"gemini_pro,omitempty"`
	// optional Flash class overrides
	GeminiFlash *GeminiModelQuotaOverride `json:"gemini_flash,omitempty"`
	// Desc is accepted in quota-rule JSON but mutation ignores its value.
	// It supplies no quota, cooldown, or stored description update in this policy.
	Desc *string `json:"desc,omitempty"`
}

// GeminiModelQuotaOverride represents optional daily and minute request-limit
// updates.
type GeminiModelQuotaOverride struct {
	// RPD and RPM use pointer presence to distinguish omitted updates from
	// supplied zero values. Mutation helpers retain previous values for nil inputs
	// and apply supplied numeric values through their limit-specific clamps.
	RPD *int64 `json:"rpd,omitempty"` // optional daily request limit
	RPM *int64 `json:"rpm,omitempty"` // optional minute request limit
}

// cloneGeminiTierOverrides copies the map and its three optional numeric
// pointers. Nil remains nil; keep input stable during copying so later caller
// mutation cannot change captured static configuration.
func cloneGeminiTierOverrides(values map[string]GeminiTierQuotaOverride) map[string]GeminiTierQuotaOverride {
	if values == nil {
		return nil
	}
	out := make(map[string]GeminiTierQuotaOverride, len(values))
	for id, v := range values {
		v.ProRPD = clonePointer(v.ProRPD)
		v.FlashRPD = clonePointer(v.FlashRPD)
		v.CooldownMinutes = clonePointer(v.CooldownMinutes)
		out[id] = v
	}
	return out
}

// NewGeminiQuotaService captures copied static tier data and callback
// references. Missing clock and logger select time.Now and a no-op;
// construction loads no settings, while later Policy calls require stable
// retained dependencies.
func NewGeminiQuotaService(options GeminiQuotaOptions) *GeminiQuotaService {
	options.StaticTiers = cloneGeminiTierOverrides(options.StaticTiers)
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Log == nil {
		options.Log = func(string, ...any) {}
	}
	return &GeminiQuotaService{options: options}
}

// Policy returns an independent tier-map copy assembled from defaults, static
// overrides, and dynamic settings. A nil service returns fresh defaults;
// cached observations younger than one minute are copied under the mutex.
//
// Cache misses build outside that mutex, so concurrent loads can overlap and
// the last completed publication wins. Static JSON follows static tier
// pointers, then successful or NotFound-tolerated dynamic text follows both. A
// nonempty v2 map takes precedence over v1 decoding; parse and loader failures
// are logged while retaining earlier policy layers. The observation timestamp
// is captured before loading, and returned copies protect the internal cached
// map from caller updates.
func (s *GeminiQuotaService) Policy(ctx context.Context) *GeminiQuotaPolicy {
	if s == nil {
		return NewGeminiQuotaPolicy()
	}

	now := s.options.Now()
	s.mu.Lock()
	// Clone the retained policy while age checking shares the publication lock.
	// Readers receive one complete tier map before later misses replace it.
	if s.policy != nil && now.Sub(s.cachedAt) < geminiQuotaCacheTTL {
		policy := s.policy.Clone()
		s.mu.Unlock()
		return policy
	}
	s.mu.Unlock()

	policy := NewGeminiQuotaPolicy()
	{
		policy.ApplyOverrides(s.options.StaticTiers)
		// Prefer current quota_rules only when that decoded map is nonempty.
		// Otherwise try legacy tiers, retaining static pointer overrides if JSON
		// cannot supply a usable replacement layer.
		if strings.TrimSpace(s.options.StaticPolicy) != "" {
			raw := []byte(s.options.StaticPolicy)
			var overridesV2 geminiQuotaOverridesV2
			if err := json.Unmarshal(raw, &overridesV2); err == nil && len(overridesV2.QuotaRules) > 0 {
				policy.ApplyQuotaRulesOverrides(overridesV2.QuotaRules)
			} else {
				var overridesV1 geminiQuotaOverridesV1
				if err := json.Unmarshal(raw, &overridesV1); err != nil {
					s.options.Log("gemini quota: parse config policy failed: %v", err)
				} else {
					policy.ApplyOverrides(overridesV1.Tiers)
				}
			}
		}
	}

	// A loader failure retains already assembled local policy so quota reads
	// remain available. A tolerated NotFound result can still carry nonempty
	// text, which follows the same current-versus-legacy JSON selection below.
	if s.options.LoadPolicy != nil {
		value, err := s.options.LoadPolicy(ctx)
		if err != nil && !errors.Is(err, s.options.NotFound) {
			s.options.Log("gemini quota: load setting failed: %v", err)
		} else if strings.TrimSpace(value) != "" {
			raw := []byte(value)
			var overridesV2 geminiQuotaOverridesV2
			if err := json.Unmarshal(raw, &overridesV2); err == nil && len(overridesV2.QuotaRules) > 0 {
				policy.ApplyQuotaRulesOverrides(overridesV2.QuotaRules)
			} else {
				var overridesV1 geminiQuotaOverridesV1
				if err := json.Unmarshal(raw, &overridesV1); err != nil {
					s.options.Log("gemini quota: parse setting failed: %v", err)
				} else {
					policy.ApplyOverrides(overridesV1.Tiers)
				}
			}
		}
	}

	// Publish the assembled immutable map only after all selected layers finish.
	// Concurrent misses may complete out of order, so this completion replaces
	// whichever observation was previously retained in the service cache.
	s.mu.Lock()
	s.policy = policy
	s.cachedAt = now
	s.mu.Unlock()

	return policy.Clone()
}

// QuotaForProvider returns the local quota selected by a Gemini record’s
// compatible tier key. Nil, other platforms, and third-party API Key records
// are unavailable; Policy supplies defaults or cached overrides for applicable
// records.
func (s *GeminiQuotaService) QuotaForProvider(ctx context.Context, provider *Record) (GeminiQuota, bool) {
	if provider == nil || provider.Platform != PlatformGemini {
		return GeminiQuota{}, false
	}

	// Normalize OAuth type and legacy tier aliases to the keys used by the policy
	// table.
	tierKey := GeminiQuotaTierKeyForProvider(provider)
	if tierKey == "" {
		return GeminiQuota{}, false
	}

	policy := s.Policy(ctx)
	return policy.QuotaForTier(tierKey)
}

// CooldownForTier obtains the current policy and selects the normalized tier
// cooldown. Unknown or nonpositive configured cooldown uses five minutes, and
// nil service policy lookup supplies the local default table.
func (s *GeminiQuotaService) CooldownForTier(ctx context.Context, tierID string) time.Duration {
	policy := s.Policy(ctx)
	return policy.CooldownForTier(tierID)
}

// CooldownForProvider selects the applicable Gemini tier cooldown or five
// minutes. Nil service/record, another platform, third-party records, or an
// unresolved key use that fallback before any settings read.
func (s *GeminiQuotaService) CooldownForProvider(ctx context.Context, provider *Record) time.Duration {
	if s == nil || provider == nil || provider.Platform != PlatformGemini {
		return 5 * time.Minute
	}
	tierKey := GeminiQuotaTierKeyForProvider(provider)
	if strings.TrimSpace(tierKey) == "" {
		return 5 * time.Minute
	}
	return s.CooldownForTier(ctx, tierKey)
}

// NewGeminiQuotaPolicy creates independent local defaults for four supported
// tier keys. AI Studio uses class quotas and Code Assist uses shared quotas;
// negative daily values disable their local enforcement, while cooldown
// defaults vary by tier.
func NewGeminiQuotaPolicy() *GeminiQuotaPolicy {
	return &GeminiQuotaPolicy{
		tiers: map[string]GeminiTierPolicy{
			// AI Studio and API Key defaults use separate quotas for Pro and Flash.
			GeminiTierAIStudioFree: {Quota: GeminiQuota{ProRPD: 50, ProRPM: 2, FlashRPD: 1500, FlashRPM: 15}, Cooldown: 30 * time.Minute},
			// Daily negative-one skips local daily enforcement; these positive minute
			// limits still constrain the modeled paid-tier request rate.
			GeminiTierAIStudioPaid: {Quota: GeminiQuota{ProRPD: -1, ProRPM: 1000, FlashRPD: -1, FlashRPM: 2000}, Cooldown: 5 * time.Minute},

			// Code Assist shares quota across model classes.
			GeminiTierGCPStandard:   {Quota: GeminiQuota{SharedRPD: 1500, SharedRPM: 120}, Cooldown: 5 * time.Minute},
			GeminiTierGCPEnterprise: {Quota: GeminiQuota{SharedRPD: 2000, SharedRPM: 120}, Cooldown: 5 * time.Minute},
		},
	}
}

// ApplyOverrides mutates a constructor-initialized policy using stable legacy
// inputs. Nil receiver or empty overrides skips work; normalized custom keys
// start with zero quotas and a five-minute cooldown.
//
// A positive shared daily limit redirects ProRPD into that shared value.
// FlashRPD checks the value after the Pro update, so setting shared quota to
// zero or unlimited can expose the Flash class override in the same operation.
// Other legacy limits update their own class values. Numeric clamping retains
// daily unlimited markers, and nonpositive stored cooldown later selects the
// lookup fallback. Callers synchronize policy mutation.
func (p *GeminiQuotaPolicy) ApplyOverrides(tiers map[string]GeminiTierQuotaOverride) {
	if p == nil || len(tiers) == 0 {
		return
	}
	for rawID, override := range tiers {
		tierID := NormalizeGeminiTierID(rawID)
		if tierID == "" {
			continue
		}
		// Normalized aliases can identify the same entry from multiple map keys.
		// This range has no deterministic alias order; callers avoid conflicting
		// overrides for one normalized tier within the same input map.
		policy, ok := p.tiers[tierID]
		if !ok {
			policy = GeminiTierPolicy{Cooldown: 5 * time.Minute}
		}
		// Legacy Pro writes a positive shared gate first. Flash later observes
		// the resulting gate, so clearing shared quota can expose its class update
		// within this same override.
		if override.ProRPD != nil {
			if policy.Quota.SharedRPD > 0 {
				policy.Quota.SharedRPD = clampGeminiQuotaInt64WithUnlimited(*override.ProRPD)
			} else {
				policy.Quota.ProRPD = clampGeminiQuotaInt64WithUnlimited(*override.ProRPD)
			}
		}
		if override.FlashRPD != nil {
			if policy.Quota.SharedRPD > 0 {
				// A shared gate still positive after Pro update
				// suppresses this Flash class write; changing
				// that gate to nonpositive above permits the
				// update.
			} else {
				policy.Quota.FlashRPD = clampGeminiQuotaInt64WithUnlimited(*override.FlashRPD)
			}
		}
		if override.CooldownMinutes != nil {
			minutes := clampGeminiQuotaInt(*override.CooldownMinutes)
			// Convert clamped minute input to the scheduling cooldown duration.
			policy.Cooldown = time.Duration(minutes) * time.Minute
		}
		p.tiers[tierID] = policy
	}
}

// ApplyQuotaRulesOverrides mutates a constructor-initialized policy with
// stable shared and class inputs. Nil receiver or empty rules skips work;
// normalized custom keys begin with a five-minute cooldown.
//
// Supplied shared values and class values are stored independently, even when
// a positive shared gate hides class values from prechecks. Pointer absence
// keeps previous values, daily negative-one remains unlimited, and negative
// minute values become zero. Description input is accepted but unused. These
// updates leave existing cooldown unchanged and require caller synchronization
// with reads of the same policy.
func (p *GeminiQuotaPolicy) ApplyQuotaRulesOverrides(rules map[string]GeminiQuotaRuleOverride) {
	if p == nil || len(rules) == 0 {
		return
	}
	for rawID, override := range rules {
		tierID := NormalizeGeminiTierID(rawID)
		if tierID == "" {
			continue
		}
		// Alias keys can converge on one normalized tier. Keep their supplied
		// values consistent because map iteration determines which conflicting
		// alias update reaches this entry last.
		policy, ok := p.tiers[tierID]
		if !ok {
			policy = GeminiTierPolicy{Cooldown: 5 * time.Minute}
		}

		if override.SharedRPD != nil {
			policy.Quota.SharedRPD = clampGeminiQuotaInt64WithUnlimited(*override.SharedRPD)
		}
		if override.SharedRPM != nil {
			policy.Quota.SharedRPM = clampGeminiQuotaRPM(*override.SharedRPM)
		}
		if override.GeminiPro != nil {
			if override.GeminiPro.RPD != nil {
				policy.Quota.ProRPD = clampGeminiQuotaInt64WithUnlimited(*override.GeminiPro.RPD)
			}
			if override.GeminiPro.RPM != nil {
				policy.Quota.ProRPM = clampGeminiQuotaRPM(*override.GeminiPro.RPM)
			}
		}
		if override.GeminiFlash != nil {
			if override.GeminiFlash.RPD != nil {
				policy.Quota.FlashRPD = clampGeminiQuotaInt64WithUnlimited(*override.GeminiFlash.RPD)
			}
			if override.GeminiFlash.RPM != nil {
				policy.Quota.FlashRPM = clampGeminiQuotaRPM(*override.GeminiFlash.RPM)
			}
		}

		p.tiers[tierID] = policy
	}
}

// QuotaForTier returns scalar quotas for a normalized existing tier. Nil
// policy or unknown key reports false; callers may use a copied result
// independently while synchronizing concurrent mutation of the source policy.
func (p *GeminiQuotaPolicy) QuotaForTier(tierID string) (GeminiQuota, bool) {
	policy, ok := p.policyForTier(tierID)
	if !ok {
		return GeminiQuota{}, false
	}
	return policy.Quota, true
}

// CooldownForTier returns a positive normalized tier cooldown or the
// five-minute fallback. Missing policy, unknown key, and zero/negative stored
// duration all select that same fallback rather than disabling cooldown.
func (p *GeminiQuotaPolicy) CooldownForTier(tierID string) time.Duration {
	policy, ok := p.policyForTier(tierID)
	if ok && policy.Cooldown > 0 {
		return policy.Cooldown
	}
	return 5 * time.Minute
}

// policyForTier reads the scalar entry under a normalized tier key. Nil or
// absent entries report false; callers synchronize this map observation
// against mutations of the same policy.
func (p *GeminiQuotaPolicy) policyForTier(tierID string) (GeminiTierPolicy, bool) {
	if p == nil {
		return GeminiTierPolicy{}, false
	}
	normalized := NormalizeGeminiTierID(tierID)
	if policy, ok := p.tiers[normalized]; ok {
		return policy, true
	}
	return GeminiTierPolicy{}, false
}

// NormalizeGeminiTierID trims input, resolves canonical or historical aliases,
// then checks known uppercase policy keys. Remaining custom text is lowercased
// and blank stays empty; such unknown policy keys remain usable here even when
// credential normalization rejects them.
func NormalizeGeminiTierID(tierID string) string {
	tierID = strings.TrimSpace(tierID)
	if tierID == "" {
		return ""
	}
	// Resolve recognized credential aliases before custom policy names. This
	// keeps historical tiers on their configured family while later unknown
	// names remain available for administrator-defined policy entries.
	if canonical := CanonicalGeminiTierID(tierID); canonical != "" {
		return canonical
	}
	// Retain uppercase policy-key compatibility after credential normalization.
	// Custom names then use lowercase so direct policy lookup can share one
	// stored entry across casing.
	switch strings.ToUpper(tierID) {
	case "AISTUDIO_FREE":
		return GeminiTierAIStudioFree
	case "AISTUDIO_PAID":
		return GeminiTierAIStudioPaid
	case "GCP_STANDARD":
		return GeminiTierGCPStandard
	case "GCP_ENTERPRISE":
		return GeminiTierGCPEnterprise
	}
	return strings.ToLower(tierID)
}

// clampGeminiQuotaInt64WithUnlimited retains negative-one as the daily
// unlimited marker. Values below negative-one become zero; nonnegative values
// pass through for callers that enforce only positive local quotas.
func clampGeminiQuotaInt64WithUnlimited(value int64) int64 {
	// Negative-one remains the supported unlimited daily sentinel.
	if value < -1 {
		return 0
	}
	return value
}

// clampGeminiQuotaInt retains nonnegative cooldown minutes and replaces
// negative values with zero. Cooldown lookup treats that stored zero as the
// default duration, so this clamp does not disable cooldown.
func clampGeminiQuotaInt(value int) int {
	// Stored zero later selects the default cooldown.
	if value < 0 {
		return 0
	}
	return value
}

// clampGeminiQuotaRPM retains nonnegative request-per-minute limits and maps
// negatives to zero. Prechecks enforce only positive limits, so zero disables
// that local minute constraint rather than representing observed usage.
func clampGeminiQuotaRPM(value int64) int64 {
	// Stored zero disables this local minute enforcement.
	if value < 0 {
		return 0
	}
	return value
}

// GeminiCooldownForTier selects cooldown from a fresh default policy without
// dynamic settings. Normalization accepts legacy labels, and unknown or
// nonpositive values select the five-minute default used by quota consumers.
func GeminiCooldownForTier(tierID string) time.Duration {
	policy := NewGeminiQuotaPolicy()
	return policy.CooldownForTier(tierID)
}

// GeminiQuotaTierKeyForProvider selects local quota identity for a Gemini
// record. Nil, other platforms, and third-party API Key records return empty
// so official local quota simulation does not apply.
//
// A recognized compatible tier wins; otherwise the OAuth variant selects Code
// Assist Standard or AI Studio Free. Record.GeminiOAuthType returns empty for
// non-OAuth types, allowing recognized tiers through unknown-variant filtering
// but selecting AI Studio when tier is unresolved.
func GeminiQuotaTierKeyForProvider(provider *Record) string {
	if provider == nil || provider.Platform != PlatformGemini {
		return ""
	}
	// Third-party providers have no Google tier, so local Google quota estimates
	// do not apply.
	if provider.IsGeminiThirdPartyProvider() {
		return ""
	}

	// Legacy records with a project_id default to code_assist in GeminiOAuthType.
	oauthType := strings.ToLower(strings.TrimSpace(provider.GeminiOAuthType()))
	rawTier := strings.TrimSpace(provider.GeminiTierID())

	// Recognized compatible credentials keep their tier-specific policy before
	// variant defaults apply.
	if tierID := CanonicalGeminiTierIDForOAuthType(oauthType, rawTier); tierID != "" {
		return tierID
	}

	// Unresolved tier uses the variant default so local quota admission can
	// still run with its modeled family. Empty or unknown variants select AI
	// Studio.
	switch oauthType {
	case "code_assist":
		return GeminiTierGCPStandard
	case "ai_studio":
		return GeminiTierAIStudioFree
	default:
		// Empty or unknown variants select the AI Studio default here.
		return GeminiTierAIStudioFree
	}
}

// Clone copies the tier map whose current values contain scalar quota and
// cooldown fields. Nil remains nil; callers can independently mutate the
// copied policy while keeping the source stable during copying.
func (p *GeminiQuotaPolicy) Clone() *GeminiQuotaPolicy {
	if p == nil {
		return nil
	}
	out := *p
	out.tiers = maps.Clone(p.tiers)
	return &out
}
