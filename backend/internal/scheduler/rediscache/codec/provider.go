package codec

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// provider.go encodes full provider records into cache JSON and reconstructs
// their execution data after decoding.
// Cached provider JSON carries execution credentials that public provider
// JSON omits. Its group relations use provider field names and emit the
// compatibility ProviderGroups member inside each group, while keeping nil
// collections distinct from supplied empty collections.
//
// Conversion builds relation wrappers around the caller's record and reuses
// its maps, scalar pointers, proxy and group configuration until JSON
// marshaling finishes. The caller keeps that data stable during encoding.
// Repeated provider pointers reuse one wire node, but cyclic relations still
// fail JSON encoding. Decoding reconstructs relation slices and restores
// clock/location hooks for provider methods; wrapping a record for
// SnapshotService continues to share that record.

// providerWire is the credential-bearing JSON representation used by
// scheduler storage. Its copied scalar values and reused pointer/map values
// come from Record; callers keep the source graph stable while marshaling.
type providerWire struct {
	QoderCatalogIdentityHint string  `json:",omitempty"` // private account observation identity
	ID                       int64   // persisted provider identity
	Name                     string  // configured display name
	Notes                    *string // optional administrator notes
	Platform                 string  // configured upstream platform
	Type                     string  // provider authentication type
	// Credentials retains the source map during encoding, including secrets
	// omitted by public Record JSON.
	Credentials map[string]any // stored credential field values
	// Extra retains the source map during encoding, so nested runtime
	// observations require stable caller-owned values until marshaling finishes.
	Extra                   map[string]any // stored runtime configuration values
	ProxyID                 *int64         // optional selected proxy identity
	ProxyFallbackOriginID   *int64         // optional fallback origin identity
	ProxyFallbackOriginName *string        // optional fallback origin name
	Concurrency             int            // configured provider slot limit
	Priority                int            // global provider sort priority
	RateMultiplier          *float64       // optional provider billing multiplier
	LoadFactor              *int           // optional scoring load denominator
	Status                  string         // persisted provider availability status
	ErrorMessage            string         // stored provider failure message
	LastUsedAt              *time.Time     // optional latest scheduling time
	ExpiresAt               *time.Time     // optional provider expiration time
	AutoPauseOnExpired      bool           // expiration scheduling pause flag
	CreatedAt               time.Time      // provider creation timestamp
	UpdatedAt               time.Time      // provider update timestamp
	Schedulable             bool           // administrator scheduling enablement flag
	RateLimitedAt           *time.Time     // optional rate-limit observation time
	RateLimitResetAt        *time.Time     // optional global rate-limit reset
	OverloadUntil           *time.Time     // optional overload expiration time
	TempUnschedulableUntil  *time.Time     // optional temporary pause deadline
	TempUnschedulableReason string         // temporary scheduling pause reason
	// QuotaAutoPaused is copied during conversion but excluded by JSON, so
	// cached decoding leaves this derived flag at false.
	QuotaAutoPaused     bool       `json:"-"` // derived quota pause flag
	SessionWindowStart  *time.Time // optional session window start
	SessionWindowEnd    *time.Time // optional session window end
	SessionWindowStatus string     // stored session window status
	ParentProviderID    *int64     // optional shadow parent identity
	QuotaDimension      string     // provider quota accounting dimension
	// Proxy reuses the caller's proxy pointer while encoding; JSON decoding
	// creates the stored proxy representation for the returned record.
	Proxy *egress.Proxy // stored execution proxy data
	// ProviderGroups receives a new relation slice when the source is non-nil,
	// retaining the distinction between null and an empty array.
	ProviderGroups []providerGroupWire // provider group relation entries
	// GroupIDs reuses the source ID slice during encoding and retains its
	// nil-versus-empty shape for stored provider data.
	GroupIDs []int64 // associated routing group identities
	// Groups receives new wire wrappers when supplied; each wrapper reuses its
	// GroupConfig until the caller's graph has been marshaled.
	Groups []*groupWire // hydrated routing group records
}

// groupWire wraps group settings with the ProviderGroups member required by
// cached JSON. Encoding keeps GroupConfig shared and leaves the compatibility
// relation member nil.
type groupWire struct {
	// GroupConfig is the caller's shared group configuration during encoding and
	// the decoded configuration returned directly by decodeGroup.
	*accessview.GroupConfig // wrapped routing group configuration
	// ProviderGroups stays nil in encodeGroup, causing the stored compatibility
	// member to encode as null rather than a populated relation array.
	ProviderGroups []json.RawMessage // compatibility group relation member
}

// providerGroupWire stores one provider membership and its optional hydrated
// relation records.
type providerGroupWire struct {
	ProviderID int64     // associated provider record identity
	GroupID    int64     // associated routing group identity
	CreatedAt  time.Time // membership creation timestamp
	// Provider points to the memoized wire node when a hydrated provider exists,
	// so repeated Record references share the conversion result.
	Provider *providerWire // optional hydrated provider node
	// Group wraps the hydrated group configuration when supplied and keeps the
	// compatibility relation field produced by encodeGroup.
	Group *groupWire // optional hydrated group wrapper
}

// recordSnapshot exposes provider identity to rebuilding while retaining the
// full execution record for codec and adapter access. Copying this wrapper
// keeps the stored record shared.
type recordSnapshot struct {
	// value retains the caller's full record pointer; adapters and last-used
	// updates access that same record through RecordValue.
	value *provider.Record // shared full provider record
}

// encodeGroup wraps a group configuration for the cached JSON representation.
// Nil groups stay nil, and supplied configurations remain shared while the
// compatibility ProviderGroups field stays nil.
func encodeGroup(group *accessview.GroupConfig) *groupWire {
	if group == nil {
		return nil
	}
	return &groupWire{GroupConfig: group}
}

// decodeGroup returns the group configuration carried by a decoded wrapper.
// Nil wrappers produce nil; supplied configurations are returned by pointer,
// so this helper creates no further group copy.
func decodeGroup(group *groupWire) *accessview.GroupConfig {
	if group == nil {
		return nil
	}
	return group.GroupConfig
}

// MarshalProviderRecord encodes a full provider graph through the storage
// wire representation. Nil records encode as null; caller-owned maps and
// pointers remain stable until encoding finishes, and cyclic graphs return
// JSON errors.
func MarshalProviderRecord(value *provider.Record) ([]byte, error) {
	return json.Marshal(recordToWire(value, make(map[*provider.Record]*providerWire)))
}

// recordToWire converts a provider record and its memberships into the
// storage representation, returning nil for a nil record. The caller supplies
// one seen map for the whole conversion.
//
// Registering a wire node before traversing memberships makes repeated
// provider pointers reuse the same node. Relation slices are allocated only
// for non-nil input so empty and absent relations remain distinguishable.
// Credential maps, scalar pointers, proxies and group configurations stay
// shared until marshaling finishes; this memoization does not make JSON
// cycles encodable.
func recordToWire(value *provider.Record, seen map[*provider.Record]*providerWire) *providerWire {
	if value == nil {
		return nil
	}
	if previous, ok := seen[value]; ok {
		return previous
	}
	out := &providerWire{}
	// Publish the conversion node before following provider memberships so a
	// repeated Record pointer refers back to its existing wire node.
	seen[value] = out
	out.ID = value.ID
	out.Name = value.Name
	out.Notes = value.Notes
	out.Platform = value.Platform
	out.Type = value.Type
	out.QoderCatalogIdentityHint = value.QoderCatalogIdentityHint
	out.Credentials = value.Credentials
	out.Extra = value.Extra
	out.ProxyID = value.ProxyID
	out.ProxyFallbackOriginID = value.ProxyFallbackOriginID
	out.ProxyFallbackOriginName = value.ProxyFallbackOriginName
	out.Concurrency = value.Concurrency
	out.Priority = value.Priority
	out.RateMultiplier = value.RateMultiplier
	out.LoadFactor = value.LoadFactor
	out.Status = value.Status
	out.ErrorMessage = value.ErrorMessage
	out.LastUsedAt = value.LastUsedAt
	out.ExpiresAt = value.ExpiresAt
	out.AutoPauseOnExpired = value.AutoPauseOnExpired
	out.CreatedAt = value.CreatedAt
	out.UpdatedAt = value.UpdatedAt
	out.Schedulable = value.Schedulable
	out.RateLimitedAt = value.RateLimitedAt
	out.RateLimitResetAt = value.RateLimitResetAt
	out.OverloadUntil = value.OverloadUntil
	out.TempUnschedulableUntil = value.TempUnschedulableUntil
	out.TempUnschedulableReason = value.TempUnschedulableReason
	out.QuotaAutoPaused = value.QuotaAutoPaused
	out.SessionWindowStart = value.SessionWindowStart
	out.SessionWindowEnd = value.SessionWindowEnd
	out.SessionWindowStatus = value.SessionWindowStatus
	out.ParentProviderID = value.ParentProviderID
	out.QuotaDimension = value.QuotaDimension
	out.Proxy = value.Proxy
	out.GroupIDs = value.GroupIDs

	// Allocate relation slices according to nil presence, allowing JSON to
	// retain absent relations as null and supplied empty relations as empty
	// arrays.
	if value.Groups != nil {
		out.Groups = make([]*groupWire, len(value.Groups))
		for i, g := range value.Groups {
			out.Groups[i] = encodeGroup(g)
		}
	}
	if value.ProviderGroups != nil {
		out.ProviderGroups = make([]providerGroupWire, len(value.ProviderGroups))
		for i, g := range value.ProviderGroups {
			out.ProviderGroups[i] = providerGroupWire{ProviderID: g.ProviderID, GroupID: g.GroupID, CreatedAt: g.CreatedAt, Provider: recordToWire(g.Provider, seen), Group: encodeGroup(g.Group)}
		}
	}
	return out
}

// UnmarshalProviderRecord decodes cached JSON and restores provider
// clock/location hooks. JSON null yields a non-nil zero record because
// decoding targets a wire value; malformed JSON returns an error.
func UnmarshalProviderRecord(payload []byte) (*provider.Record, error) {
	var wire providerWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, err
	}
	return recordFromWire(&wire), nil
}

// recordFromWire reconstructs a provider from a decoded wire tree and
// restores time.Now and time.LoadLocation for runtime accessors. A nil nested
// wire node stays nil.
//
// Group and membership slices are allocated when their wire slices are
// non-nil, keeping empty arrays distinct from null. Map, scalar-pointer and
// proxy fields reuse the decoded values, and group configurations come
// directly from their wrappers. Recursive membership providers are converted
// as a tree; this helper expects the acyclic representation produced by JSON
// decoding.
func recordFromWire(value *providerWire) *provider.Record {
	if value == nil {
		return nil
	}
	out := &provider.Record{Now: time.Now, LoadLocation: time.LoadLocation}
	out.ID = value.ID
	out.Name = value.Name
	out.Notes = value.Notes
	out.Platform = value.Platform
	out.Type = value.Type
	out.QoderCatalogIdentityHint = value.QoderCatalogIdentityHint
	out.Credentials = value.Credentials
	out.Extra = value.Extra
	out.ProxyID = value.ProxyID
	out.ProxyFallbackOriginID = value.ProxyFallbackOriginID
	out.ProxyFallbackOriginName = value.ProxyFallbackOriginName
	out.Concurrency = value.Concurrency
	out.Priority = value.Priority
	out.RateMultiplier = value.RateMultiplier
	out.LoadFactor = value.LoadFactor
	out.Status = value.Status
	out.ErrorMessage = value.ErrorMessage
	out.LastUsedAt = value.LastUsedAt
	out.ExpiresAt = value.ExpiresAt
	out.AutoPauseOnExpired = value.AutoPauseOnExpired
	out.CreatedAt = value.CreatedAt
	out.UpdatedAt = value.UpdatedAt
	out.Schedulable = value.Schedulable
	out.RateLimitedAt = value.RateLimitedAt
	out.RateLimitResetAt = value.RateLimitResetAt
	out.OverloadUntil = value.OverloadUntil
	out.TempUnschedulableUntil = value.TempUnschedulableUntil
	out.TempUnschedulableReason = value.TempUnschedulableReason
	out.QuotaAutoPaused = value.QuotaAutoPaused
	out.SessionWindowStart = value.SessionWindowStart
	out.SessionWindowEnd = value.SessionWindowEnd
	out.SessionWindowStatus = value.SessionWindowStatus
	out.ParentProviderID = value.ParentProviderID
	out.QuotaDimension = value.QuotaDimension
	out.Proxy = value.Proxy
	out.GroupIDs = value.GroupIDs

	// Reconstruct relation slices according to decoded presence so provider
	// adapters retain the cached distinction between an empty array and null.
	if value.Groups != nil {
		out.Groups = make([]*accessview.GroupConfig, len(value.Groups))
		for i, g := range value.Groups {
			out.Groups[i] = decodeGroup(g)
		}
	}
	if value.ProviderGroups != nil {
		out.ProviderGroups = make([]provider.GroupMembership, len(value.ProviderGroups))
		for i, g := range value.ProviderGroups {
			out.ProviderGroups[i] = provider.GroupMembership{ProviderID: g.ProviderID, GroupID: g.GroupID, CreatedAt: g.CreatedAt, Provider: recordFromWire(g.Provider), Group: decodeGroup(g.Group)}
		}
	}
	out.PrepareQoderCatalog()
	return out
}

// SnapshotMetadata returns this record's identity, name, platform and group
// IDs for rebuild routing. The group ID slice is cloned so a metadata
// consumer can edit that slice independently.
func (s recordSnapshot) SnapshotMetadata() scheduler.SnapshotMetadata {
	return scheduler.SnapshotMetadata{
		ID: s.value.ID, Name: s.value.Name, Platform: s.value.Platform,
		GroupIDs: slices.Clone(s.value.GroupIDs),
	}
}

// WrapRecord exposes a non-nil record through SnapshotProvider while
// retaining its pointer for execution adapters. Nil input returns nil;
// callers keep shared record data stable during cache publication.
func WrapRecord(value *provider.Record) scheduler.SnapshotProvider {
	if value == nil {
		return nil
	}
	return recordSnapshot{value: value}
}

// RecordValue returns the shared full record carried by the codec's
// recordSnapshot value. Nil input returns nil, while any other
// SnapshotProvider implementation returns an error for the caller to handle.
func RecordValue(value scheduler.SnapshotProvider) (*provider.Record, error) {
	if value == nil {
		return nil, nil
	}
	stored, ok := value.(recordSnapshot)
	if !ok {
		return nil, fmt.Errorf("unexpected legacy scheduler snapshot data %T", value)
	}
	return stored.value, nil
}
