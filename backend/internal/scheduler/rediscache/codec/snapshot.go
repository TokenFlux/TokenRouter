package codec

import (
	"fmt"
	"time"

	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// snapshot.go produces full provider payloads and filtered metadata for
// scheduler cache readers.
// Snapshot buckets read slim provider metadata before an execution adapter
// obtains the full credential-bearing record. Metadata keeps the endpoint,
// protocol, model, quota and session inputs used before hydration, including
// selected credential fields such as api_key, while dropping hydrated group
// graphs and unrelated extra values.
//
// The filtered outer maps and membership slices are newly allocated, but
// retained nested values and scalar pointers continue to refer to the
// supplied Record. Callers treat those values as shared until encoding
// completes. Group IDs combine valid direct IDs with membership IDs in
// first-seen order. Both full and metadata payloads use the same storage wire
// encoder so consumers see compatible field names and collection shapes.

// ProviderCodec converts stored provider records into full and filtered
// scheduler payloads.
type ProviderCodec struct{}

// Encode returns full and metadata JSON for a record wrapped by WrapRecord.
// Unsupported snapshot implementations and nil records return errors; the
// caller keeps the wrapped data stable until encoding finishes.
func (ProviderCodec) Encode(value scheduler.SnapshotProvider) ([]byte, []byte, error) {
	v, err := RecordValue(value)
	if err != nil {
		return nil, nil, err
	}
	if v == nil {
		return nil, nil, fmt.Errorf("nil scheduler provider")
	}
	return marshalSchedulerCacheProvider(*v)
}

// Decode accepts stored JSON as a string or byte slice and wraps the decoded
// provider. Unsupported input types or invalid JSON return errors; JSON null
// produces a wrapped zero record.
func (ProviderCodec) Decode(raw any) (scheduler.SnapshotProvider, error) {
	v, err := decodeCachedProvider(raw)
	return WrapRecord(v), err
}

// LastUsedAt returns the wrapped record's shared last-used pointer for
// comparison with the Redis side key. Nil snapshots produce nil, while
// unsupported snapshot implementations return an error.
func (ProviderCodec) LastUsedAt(value scheduler.SnapshotProvider) (*time.Time, error) {
	v, err := RecordValue(value)
	if err != nil || v == nil {
		return nil, err
	}
	return v.LastUsedAt, nil
}

// SetLastUsedAt installs at directly on the wrapped record for a newer Redis
// side-key observation. Nil snapshots are ignored, and callers treat the
// installed time pointer as shared.
func (ProviderCodec) SetLastUsedAt(value scheduler.SnapshotProvider, at *time.Time) error {
	v, err := RecordValue(value)
	if err != nil {
		return err
	}
	if v != nil {
		v.LastUsedAt = at
	}
	return nil
}

// Metadata builds the provider data used for candidate filtering before full
// hydration. New outer maps and membership slices hold retained values;
// nested mutable values and scalar pointers remain shared with the input.
func (ProviderCodec) Metadata(value providercore.Record) providercore.Record {
	return buildSchedulerMetadataProvider(value)
}

// decodeCachedProvider accepts the string and byte-slice forms returned for
// Redis provider JSON. Other input types return an error; decoding then uses
// the complete storage wire representation.
func decodeCachedProvider(val any) (*providercore.Record, error) {
	var payload []byte
	switch raw := val.(type) {
	case string:
		payload = []byte(raw)
	case []byte:
		payload = raw
	default:
		return nil, fmt.Errorf("unexpected provider cache type: %T", val)
	}
	record, err := UnmarshalProviderRecord(payload)
	return record, err
}

// marshalSchedulerCacheProvider encodes the full record and its filtered
// metadata with the storage wire encoder. A failure in either encoding
// returns an error, so callers publish only successful payload pairs.
func marshalSchedulerCacheProvider(provider providercore.Record) ([]byte, []byte, error) {
	fullPayload, err := MarshalProviderRecord(&provider)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal provider: %w", err)
	}
	metadata := buildSchedulerMetadataProvider(provider)
	metaPayload, err := MarshalProviderRecord(&metadata)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal provider metadata: %w", err)
	}
	return fullPayload, metaPayload, nil
}

// buildSchedulerMetadataProvider selects the record fields needed by
// scheduler candidate checks before execution hydration. It retains selected
// credential and extra values from provider field policy, including api_key,
// precomputes catalog and usage-monitor identity hints, preserves ProxyID and
// clock/timezone hooks, and removes hydrated group graphs.
//
// Membership entries keep positive group IDs and scalar identities. The group
// ID list merges direct IDs with those memberships to keep routing
// association available when either source is incomplete. Filtering allocates
// outer maps and slices, but retained nested values and scalar pointers stay
// shared; consumers need their own recursive copy before mutating that data
// independently.
func buildSchedulerMetadataProvider(provider providercore.Record) providercore.Record {
	qoderIdentity := ""
	if provider.IsQoderCosy() {
		qoderIdentity = providercore.QoderCatalogIdentity(&provider)
	}
	usageMonitorIdentity := ""
	if provider.SupportsUsageMonitoring() {
		usageMonitorIdentity = providercore.UsageMonitorIdentityFingerprint(&provider)
	}
	return providercore.Record{
		QoderCatalogIdentityHint: qoderIdentity,
		UsageMonitorIdentityHint: usageMonitorIdentity,
		ID:                       provider.ID,
		Name:                     provider.Name,
		Platform:                 provider.Platform,
		Type:                     provider.Type,
		ProxyID:                  provider.ProxyID,
		Concurrency:              provider.Concurrency,
		LoadFactor:               provider.LoadFactor,
		Priority:                 provider.Priority,
		RateMultiplier:           provider.RateMultiplier,
		Status:                   provider.Status,
		LastUsedAt:               provider.LastUsedAt,
		ExpiresAt:                provider.ExpiresAt,
		AutoPauseOnExpired:       provider.AutoPauseOnExpired,
		Schedulable:              provider.Schedulable,
		RateLimitedAt:            provider.RateLimitedAt,
		RateLimitResetAt:         provider.RateLimitResetAt,
		OverloadUntil:            provider.OverloadUntil,
		TempUnschedulableUntil:   provider.TempUnschedulableUntil,
		TempUnschedulableReason:  provider.TempUnschedulableReason,
		SessionWindowStart:       provider.SessionWindowStart,
		SessionWindowEnd:         provider.SessionWindowEnd,
		SessionWindowStatus:      provider.SessionWindowStatus,
		ParentProviderID:         provider.ParentProviderID,
		QuotaDimension:           provider.QuotaDimension,
		ProviderGroups:           filterSchedulerProviderGroups(provider.ProviderGroups),
		GroupIDs:                 filterSchedulerGroupIDs(provider.GroupIDs, provider.ProviderGroups),
		Credentials:              filterSchedulerCredentials(provider.Credentials),
		Extra:                    filterSchedulerExtra(provider.Extra),
		Now:                      provider.Now,
		LoadLocation:             provider.LoadLocation,
	}
}

// filterSchedulerProviderGroups copies positive-ID memberships as scalar
// provider/group identities and creation times. Empty or wholly invalid input
// returns nil; hydrated provider and group records are omitted from the new
// entries.
func filterSchedulerProviderGroups(providerGroups []providercore.GroupMembership) []providercore.GroupMembership {
	if len(providerGroups) == 0 {
		return nil
	}

	filtered := make([]providercore.GroupMembership, 0, len(providerGroups))
	for _, ag := range providerGroups {
		if ag.GroupID <= 0 {
			continue
		}
		filtered = append(filtered, providercore.GroupMembership{
			ProviderID: ag.ProviderID,
			GroupID:    ag.GroupID,
			CreatedAt:  ag.CreatedAt,
		})
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// filterSchedulerGroupIDs combines direct group IDs and membership IDs into
// one positive, duplicate-free routing list. Empty input or a list with no
// positive identities returns nil.
//
// Direct IDs appear first in their supplied order; membership IDs append only
// when they were not already encountered. A single seen set spans both
// sources, keeping a provider's group association available when one
// representation lacks an ID. The output slice is independent of the inputs
// and preserves first-seen ordering for downstream bucket expansion.
func filterSchedulerGroupIDs(groupIDs []int64, providerGroups []providercore.GroupMembership) []int64 {
	if len(groupIDs) == 0 && len(providerGroups) == 0 {
		return nil
	}

	// Use one identity set across direct IDs and memberships so the scheduler
	// can expand groups without duplicate entries or losing membership-only
	// identities.
	seen := make(map[int64]struct{}, len(groupIDs)+len(providerGroups))
	filtered := make([]int64, 0, len(groupIDs)+len(providerGroups))
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		filtered = append(filtered, id)
	}
	for _, ag := range providerGroups {
		if ag.GroupID <= 0 {
			continue
		}
		if _, ok := seen[ag.GroupID]; ok {
			continue
		}
		seen[ag.GroupID] = struct{}{}
		filtered = append(filtered, ag.GroupID)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// filterSchedulerCredentials delegates pre-hydration credential selection to
// providercore.FilterSchedulerCredentials so snapshot filtering and provider
// field policy stay synchronized.
func filterSchedulerCredentials(credentials map[string]any) map[string]any {
	return providercore.FilterSchedulerCredentials(credentials)
}

// filterSchedulerExtra delegates pre-hydration Extra selection to
// providercore.FilterSchedulerExtra so snapshot filtering and provider field
// policy stay synchronized.
func filterSchedulerExtra(extra map[string]any) map[string]any {
	return providercore.FilterSchedulerExtra(extra)
}
