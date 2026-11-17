package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// duplicate.go creates disabled provider copies and recovers completed
// duplication requests by operation identity.
// A duplicate reuses static provider configuration while beginning with clean
// runtime observations and disabled scheduling. Credentials and Extra pass
// through JSON encoding and decoding before normalization, isolating supported
// JSON containers while converting JSON numbers to the decoder's numeric
// representation. Rotating credential types and linked shadows require their
// own creation workflows.
//
// A supplied operation key is combined with the administrator scope and source
// ID to locate a previously saved duplicate when a response is retried. The
// copied record and memberships use the group-aware creation store together.
// This lookup supports recovery of a committed operation; callers coordinate
// concurrent attempts because this method does not serialize the lookup and
// creation sequence.

const (
	// Duplication reserves the copy suffix within its rune budget and stores a
	// scoped retry digest in Extra; storage validates name length separately.
	maxProviderNameRunes                 = 100
	duplicateProviderOperationIDExtraKey = "duplicate_operation_id"
)

// duplicateProviderDiscardedExtraKeys identifies state tied to the source's
// observations, synchronization identity, quota history, or previous
// duplication operation.
var duplicateProviderDiscardedExtraKeys = map[string]struct{}{
	// Each copy requires its own reminder opt-in and recipient review.
	UpstreamBalanceAlertExtraKey: {},
	// Retry identifiers belong to the operation that produced the record.
	duplicateProviderOperationIDExtraKey: {},
	// External synchronization identifiers belong to one local provider.
	"crs_account_id": {},
	"crs_kind":       {},
	"crs_synced_at":  {},
	// Start local usage counters and derived quota windows empty.
	"quota_used":             {},
	"quota_daily_used":       {},
	"quota_weekly_used":      {},
	"quota_monthly_used":     {},
	"quota_daily_start":      {},
	"quota_weekly_start":     {},
	"quota_monthly_start":    {},
	"quota_daily_reset_at":   {},
	"quota_weekly_reset_at":  {},
	"quota_monthly_reset_at": {},
	// Generate a fresh provider-specific Codex fingerprint seed for the duplicate.
	CodexFingerprintSeedExtraKey: {},
	// Upstream observations, capability probes, and temporary scheduling state
	// are not copied as configuration.
	"model_rate_limits":                      {},
	"session_window_utilization":             {},
	"passive_usage_7d_utilization":           {},
	"passive_usage_7d_reset":                 {},
	"passive_usage_7d_oi_utilization":        {},
	"passive_usage_7d_oi_reset":              {},
	"passive_usage_sampled_at":               {},
	"grok_usage_snapshot":                    {},
	"grok_billing_snapshot":                  {},
	"qoder_quota_snapshot":                   {},
	QoderCatalogExtraKey:                     {},
	"qoder_quota_updated_at":                 {},
	UsageMonitorSnapshotExtraKey:             {},
	"antigravity_credits_overages":           {},
	"antigravity_force_token_refresh":        {},
	"antigravity_force_token_refresh_at":     {},
	"antigravity_force_token_refresh_reason": {},
	"drive_storage_limit":                    {},
	"drive_storage_usage":                    {},
	"drive_tier_updated_at":                  {},
	"codex_primary_used_percent":             {},
	"codex_primary_reset_after_seconds":      {},
	"codex_primary_window_minutes":           {},
	"codex_secondary_used_percent":           {},
	"codex_secondary_reset_after_seconds":    {},
	"codex_secondary_window_minutes":         {},
	"codex_primary_over_secondary_percent":   {},
	"codex_usage_updated_at":                 {},
	"codex_5h_used_percent":                  {},
	"codex_5h_reset_after_seconds":           {},
	"codex_5h_window_minutes":                {},
	"codex_5h_reset_at":                      {},
	"codex_7d_used_percent":                  {},
	"codex_7d_reset_after_seconds":           {},
	"codex_7d_window_minutes":                {},
	"codex_7d_reset_at":                      {},
	"upstream_billing_probe":                 {},
	"upstream_billing_probe_enabled":         {},
}

// duplicateProviderName trims the source label and appends the copy suffix
// within the rune limit. Long labels are truncated by rune so the suffix fits
// without splitting a Unicode code point. Storage's separate byte-length
// validator can still reject the resulting multibyte name.
func duplicateProviderName(sourceName string) string {
	const suffix = " (Copy)"
	nameRunes := []rune(strings.TrimSpace(sourceName))
	maxBaseRunes := maxProviderNameRunes - len([]rune(suffix))
	// Reserve suffix runes so truncation keeps the complete copy label.
	if len(nameRunes) > maxBaseRunes {
		nameRunes = nameRunes[:maxBaseRunes]
	}
	return string(nameRunes) + suffix
}

// cloneProviderJSONMap copies a JSON-encodable map through encoding and
// decoding. Nil stays nil; nested JSON containers become separate values,
// decoded numbers use float64, and unsupported values return an encoding error.
func cloneProviderJSONMap(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	cloned := make(map[string]any, len(value))
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

// duplicateProviderExtra copies JSON settings and removes the keys classified
// as source-owned runtime state. Nil remains nil, and encoding failures stop
// preparation before any duplicate is persisted.
func duplicateProviderExtra(value map[string]any) (map[string]any, error) {
	cloned, err := cloneProviderJSONMap(value)
	if err != nil {
		return nil, err
	}
	// Remove source-owned observations and synchronization identity after
	// cloning, keeping configured settings while the copy starts its own usage
	// and retry history.
	for key := range duplicateProviderDiscardedExtraKeys {
		delete(cloned, key)
	}
	return cloned, nil
}

// canDuplicateProviderType reports whether credentials belong to a supported
// static authentication type.
func canDuplicateProviderType(providerType string) bool {
	switch providerType {
	case ProviderTypeAPIKey, ProviderTypeUpstream, ProviderTypeBedrock, ProviderTypeServiceAccount:
		return true
	default:
		return false
	}
}

// duplicateProviderGroups creates membership rows and group IDs for the copy.
// Loaded ProviderGroups take precedence over GroupIDs; each new row starts with
// its group ID and receives the duplicate ID during persistence.
func duplicateProviderGroups(source *Record) ([]GroupMembership, []int64) {
	if len(source.ProviderGroups) > 0 {
		groups := make([]GroupMembership, 0, len(source.ProviderGroups))
		groupIDs := make([]int64, 0, len(source.ProviderGroups))
		for _, sourceGroup := range source.ProviderGroups {
			groups = append(groups, GroupMembership{GroupID: sourceGroup.GroupID})
			groupIDs = append(groupIDs, sourceGroup.GroupID)
		}
		return groups, groupIDs
	}

	// Records lacking hydrated memberships still carry GroupIDs. Rebuild fresh
	// membership rows from those IDs so the copy retains its stored group
	// selection.
	groups := make([]GroupMembership, 0, len(source.GroupIDs))
	groupIDs := append([]int64(nil), source.GroupIDs...)
	for _, groupID := range groupIDs {
		groups = append(groups, GroupMembership{GroupID: groupID})
	}
	return groups, groupIDs
}

// duplicateProviderOperationID derives the durable recovery key for one source
// and administrator operation. Blank operation keys disable recovery lookup,
// and a blank trimmed actor scope uses admin:0.
//
// The operation domain, actor, source ID, and trimmed operation key are
// separated before hashing. That representation keeps the initiating
// administrator and source attached to a retry even when different callers
// reuse the same operation text. The resulting hexadecimal digest is stored in
// the duplicate's Extra.
func duplicateProviderOperationID(sourceID int64, actorScope, operationKey string) string {
	operationKey = strings.TrimSpace(operationKey)
	if operationKey == "" {
		return ""
	}
	actorScope = strings.TrimSpace(actorScope)
	if actorScope == "" {
		actorScope = "admin:0"
	}
	payload := "admin.providers.duplicate\x00" + actorScope + "\x00" + strconv.FormatInt(sourceID, 10) + "\x00" + operationKey
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", digest)
}

// findDuplicateByOperationID reads the first record matching the saved
// duplicate operation digest. Empty digests and absent matches return nil
// successfully; store failures are wrapped for the recovery caller.
func (s *Admin) findDuplicateByOperationID(ctx context.Context, operationID string) (*Record, error) {
	if operationID == "" {
		return nil, nil
	}
	providers, err := s.providerRepo.FindByExtraField(ctx, duplicateProviderOperationIDExtraKey, operationID)
	if err != nil {
		return nil, fmt.Errorf("find duplicate provider operation: %w", err)
	}
	if len(providers) == 0 {
		return nil, nil
	}
	provider := providers[0]
	return &provider, nil
}

// RecoverDuplicateProvider finds a saved duplicate using its source,
// administrator scope, and operation key. An empty key disables lookup, and an
// unmatched key returns nil so the caller can decide whether to create.
func (s *Admin) RecoverDuplicateProvider(ctx context.Context, id int64, actorScope, operationKey string) (*Record, error) {
	return s.findDuplicateByOperationID(ctx, duplicateProviderOperationID(id, actorScope, operationKey))
}

// cloneProviderValuePointer copies the pointed-to value into a new outer
// pointer. Nil remains nil; reference fields inside a generic value retain
// their referenced data, so this helper is used for scalar inputs here.
func cloneProviderValuePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// DuplicateProvider reuses an existing operation match or creates an
// unschedulable copy of a static-credential provider. Linked shadows and
// unsupported rotating types are rejected. JSON copies are normalized into
// fresh active configuration, with usage observations and source
// synchronization keys removed.
//
// The original configured proxy takes precedence over a temporary fallback
// proxy. New membership rows and the copied record are saved through
// DuplicateStore together. Recovery lookup precedes creation, so the caller's
// idempotency coordinator still governs concurrent requests that share an
// operation key.
func (s *Admin) DuplicateProvider(ctx context.Context, id int64, actorScope, operationKey string) (*Record, error) {
	operationID := duplicateProviderOperationID(id, actorScope, operationKey)
	existing, err := s.RecoverDuplicateProvider(ctx, id, actorScope, operationKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	source, err := s.providerRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if source.IsCredentialShadow() {
		return nil, infraerrors.BadRequest(
			"PROVIDER_DUPLICATE_SHADOW_UNSUPPORTED",
			"linked credential shadow providers cannot be duplicated; duplicate the parent provider instead",
		)
	}
	if !canDuplicateProviderType(source.Type) {
		return nil, infraerrors.BadRequest(
			"PROVIDER_DUPLICATE_CREDENTIAL_TYPE_UNSUPPORTED",
			"providers with rotating or unsupported credential types cannot be duplicated",
		)
	}

	credentials, err := cloneProviderJSONMap(source.Credentials)
	if err != nil {
		return nil, fmt.Errorf("clone provider credentials: %w", err)
	}
	extra, err := duplicateProviderExtra(source.Extra)
	if err != nil {
		return nil, fmt.Errorf("clone provider extra configuration: %w", err)
	}
	if operationID != "" {
		if extra == nil {
			extra = make(map[string]any, 1)
		}
		extra[duplicateProviderOperationIDExtraKey] = operationID
	}

	var expiresAt *int64
	if source.ExpiresAt != nil {
		// Creation accepts Unix seconds, so subsecond expiration precision is
		// discarded.
		unix := source.ExpiresAt.Unix()
		expiresAt = &unix
	}
	autoPauseOnExpired := source.AutoPauseOnExpired
	groups, groupIDs := duplicateProviderGroups(source)
	proxyID := source.ProxyID
	if source.ProxyFallbackOriginID != nil {
		// The live proxy can be a temporary fallback. Copy the saved origin so
		// the duplicate begins with the administrator's configured proxy selection.
		proxyID = source.ProxyFallbackOriginID
	}
	input := &CreateProviderInput{
		Name:               duplicateProviderName(source.Name),
		Notes:              cloneProviderValuePointer(source.Notes),
		Platform:           source.Platform,
		Type:               source.Type,
		Credentials:        credentials,
		Extra:              extra,
		ProxyID:            cloneProviderValuePointer(proxyID),
		Concurrency:        source.Concurrency,
		Priority:           source.Priority,
		RateMultiplier:     cloneProviderValuePointer(source.RateMultiplier),
		LoadFactor:         cloneProviderValuePointer(source.LoadFactor),
		GroupIDs:           groupIDs,
		ExpiresAt:          expiresAt,
		AutoPauseOnExpired: &autoPauseOnExpired,
	}
	// Normalize copied header overrides before building the new record so saved
	// header spellings follow the same casing and validation rules as direct
	// creation.
	if err := egress.NormalizeHeaderOverrideCredentials(input.Credentials); err != nil {
		return nil, err
	}
	duplicate, err := BuildProviderForCreate(input, input.Extra, s.options.Creation)
	if err != nil {
		return nil, err
	}
	// The copy carries the same static authentication material as its source.
	// Start it unschedulable so an administrator chooses when shared
	// credentials serve traffic.
	duplicate.Schedulable = false
	if s.options.Duplicates == nil {
		return nil, errors.New("provider duplicate repository is not configured")
	}
	// Membership rows need the generated provider ID. Submit them with the
	// provider so storage assigns that identity and links the rows within its
	// selected transaction.
	if err := s.options.Duplicates.CreateWithProviderGroups(ctx, duplicate, groups); err != nil {
		return nil, fmt.Errorf("create duplicate provider: %w", err)
	}
	for i := range groups {
		groups[i].ProviderID = duplicate.ID
	}
	duplicate.ProviderGroups = groups
	duplicate.GroupIDs = groupIDs
	return duplicate, nil
}
