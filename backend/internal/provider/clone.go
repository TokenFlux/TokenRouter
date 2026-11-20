package provider

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// clone.go copies provider records and supported mutable values, reconnecting
// memoized record links and JSON-container references.
// Provider records and their JSON-shaped maps are passed between storage,
// request snapshots, and caches, where mutable data needs a separate owner.
// Copying through JSON would change numeric representations and reject cycles
// before the owning validation step can report them.
//
// map[string]any and []any traversal memoizes each of those containers before
// descending, retaining their repeated references and cycles within one value
// copy. Slice identity includes length because different views can share the
// same first element. Record traversal has a separate memo table so a membership
// that points to its provider root points to the copied root. Other supported
// maps and slices are copied independently on each occurrence. Unsupported
// dynamic values and injected function values remain shared, so callers still
// treat those retained references as immutable.

// sliceIdentity identifies one slice view during recursive JSON-container
// copying.
type sliceIdentity struct {
	// ptr and length distinguish prefix views that start at the same element.
	// Capacity is absent because traversal copies the currently visible elements.
	ptr    uintptr // source first-element address
	length int     // visible source element count
}

// valueCopyState retains copied destinations while one JSON-shaped graph is
// traversed.
type valueCopyState struct {
	// maps and slices register destinations before recursive descent. Returning
	// these destinations for repeated containers retains cycles for the later
	// JSON encoder to accept or reject.
	maps   map[uintptr]map[string]any // map address to copied destination
	slices map[sliceIdentity][]any    // slice view to copied destination
}

// CloneValues copies the supported maps, slices, and byte payloads nested in
// values while retaining numeric values and nil containers. Callers need a
// stable input graph during the traversal.
//
// Map[string]any and []any copies retain their repeated references and cycles
// through memoization, so cyclic input remains cyclic for later JSON
// validation. The concrete container cases in cloneValue define the copy scope;
// unknown values, including pointers and custom container types, are returned
// unchanged and remain shared with the source.
func CloneValues(values map[string]any) map[string]any {
	return cloneValues(values, &valueCopyState{})
}

// cloneValues copies a string-keyed JSON object using the traversal's map memo.
// A nil map remains nil, and a previously visited map returns its existing
// copy.
//
// The destination is registered before visiting values, allowing self-links and
// indirect cycles to resolve to the same copied map. state belongs to this
// single traversal; reusing it for unrelated copies would retain prior
// destinations and prevent those calls from receiving separate ownership.
func cloneValues(values map[string]any, state *valueCopyState) map[string]any {
	if values == nil {
		return nil
	}
	id := reflect.ValueOf(values).Pointer()
	if previous, ok := state.maps[id]; ok {
		return previous
	}
	if state.maps == nil {
		state.maps = make(map[uintptr]map[string]any)
	}
	out := make(map[string]any, len(values))
	state.maps[id] = out
	for key, value := range values {
		out[key] = cloneValue(value, state)
	}
	return out
}

// cloneValue copies one supported mutable value and returns scalar or
// unsupported values unchanged. Typed nil []any values retain their slice type,
// while listed typed maps and slices use their respective clone helpers.
//
// The []any memo uses both its first-element pointer and length, distinguishing
// prefix views over the same backing array. The destination enters the memo
// before element traversal, keeping repeated views and cyclic graphs finite.
// Separate views need not retain their source backing-array relationship, and
// unlisted dynamic types remain shared.
func cloneValue(value any, state *valueCopyState) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneValues(v, state)
	case []any:
		if v == nil {
			return []any(nil)
		}
		id := sliceIdentity{ptr: reflect.ValueOf(v).Pointer(), length: len(v)}
		if previous, ok := state.slices[id]; ok {
			return previous
		}
		if state.slices == nil {
			state.slices = make(map[sliceIdentity][]any)
		}
		out := make([]any, len(v))
		state.slices[id] = out
		for i := range v {
			out[i] = cloneValue(v[i], state)
		}
		return out
	case map[string]string:
		return maps.Clone(v)
	case map[string]map[string]string:
		return CloneEffortModelMapping(v)
	case EffortModelMapping:
		return CloneEffortModelMapping(v)
	case map[string]bool:
		return maps.Clone(v)
	case []protocol.ProtocolID:
		return slices.Clone(v)
	case []string:
		return slices.Clone(v)
	case []int64:
		return slices.Clone(v)
	case []int:
		return slices.Clone(v)
	case []float64:
		return slices.Clone(v)
	case []bool:
		return slices.Clone(v)
	case json.RawMessage:
		return slices.Clone(v)
	case []byte:
		return slices.Clone(v)
	default:
		return value
	}
}

// clonePointer allocates a copy of the pointed-to value, retaining nil as nil.
// T is copied by assignment, so nested references within T remain shared;
// Record uses this helper for its scalar and time pointers.
func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

// CloneRecord returns a separate provider record with copied supported
// credential and Extra containers, optional scalar pointers, proxy fields, and
// group data. Nil input returns nil, and the source must remain stable while it
// is copied.
//
// ProviderGroups can refer back to the root or revisit another provider record.
// The record memo registers each destination before following these links,
// making those references point into the copied graph. Injected functions and
// dynamic values outside CloneValues' supported cases remain shared, so the
// result does not isolate arbitrary objects stored in a map.
func CloneRecord(record *Record) *Record { return cloneRecord(record, make(map[*Record]*Record)) }

// cloneRecord copies a provider graph through an existing record memo. Nil
// returns nil, and an already visited provider returns the destination
// previously allocated for it.
//
// A fresh destination is populated by copyRecordInto, which registers the
// source before descending through ProviderGroups. This ordering allows a root
// membership or a cycle between provider records to reuse the copied provider
// instead of allocating indefinitely. The memo is private to one top-level
// copying operation.
func cloneRecord(record *Record, seen map[*Record]*Record) *Record {
	if record == nil {
		return nil
	}
	if copy, ok := seen[record]; ok {
		return copy
	}
	out := new(Record)
	copyRecordInto(out, record, seen)
	return out
}

// CopyRecordInto replaces an existing destination with copied provider data
// while retaining the destination address. A nil destination is ignored; a nil
// source clears the destination record. Callers keep the source stable and
// exclude concurrent access to out during replacement.
//
// The source and destination may be the same address. copyRecordInto first
// snapshots the source fields and registers the original source as the
// destination root, so membership links return to that address while mutable
// supported values receive new copies. References retained outside the
// destination continue to address the same record after the replacement.
func CopyRecordInto(out, record *Record) {
	if out == nil {
		return
	}
	if record == nil {
		*out = Record{}
		return
	}
	copyRecordInto(out, record, make(map[*Record]*Record))
}

// copyRecordInto populates a destination and registers its source in the record
// memo before copying relationships. Both records are nonnil and callers own
// the memo for this traversal.
//
// A value snapshot protects the source fields when copying in place, while the
// memo maps the original source address to out so self-referential
// ProviderGroups resolve correctly. Optional pointers, proxy values, group
// data, and supported JSON containers are then copied from the snapshot. Each
// collection uses its own copy helper, so cross-collection aliasing is not
// promised to survive.
func copyRecordInto(out, record *Record, seen map[*Record]*Record) {
	// Register the original source after taking its value snapshot. A membership
	// pointing to that original address then resolves to out, including when
	// the caller requested an in-place copy.
	snapshot := *record
	seen[record] = out
	record = &snapshot
	*out = snapshot
	out.Credentials = CloneValues(record.Credentials)
	out.Extra = CloneValues(record.Extra)
	out.Notes = clonePointer(record.Notes)
	out.ProxyID = clonePointer(record.ProxyID)
	out.ProxyFallbackOriginID = clonePointer(record.ProxyFallbackOriginID)
	out.ProxyFallbackOriginName = clonePointer(record.ProxyFallbackOriginName)
	out.RateMultiplier = clonePointer(record.RateMultiplier)
	out.LoadFactor = clonePointer(record.LoadFactor)
	out.LastUsedAt = clonePointer(record.LastUsedAt)
	out.ExpiresAt = clonePointer(record.ExpiresAt)
	out.RateLimitedAt = clonePointer(record.RateLimitedAt)
	out.RateLimitResetAt = clonePointer(record.RateLimitResetAt)
	out.OverloadUntil = clonePointer(record.OverloadUntil)
	out.TempUnschedulableUntil = clonePointer(record.TempUnschedulableUntil)
	out.SessionWindowStart = clonePointer(record.SessionWindowStart)
	out.SessionWindowEnd = clonePointer(record.SessionWindowEnd)
	out.ParentProviderID = clonePointer(record.ParentProviderID)
	out.GroupIDs = slices.Clone(record.GroupIDs)
	if record.Proxy != nil {
		proxy := *record.Proxy
		proxy.ExpiresAt = clonePointer(proxy.ExpiresAt)
		proxy.BackupProxyID = clonePointer(proxy.BackupProxyID)
		out.Proxy = &proxy
	}
	if record.Groups != nil {
		out.Groups = make([]*accessview.GroupConfig, len(record.Groups))
		for i := range record.Groups {
			out.Groups[i] = accessview.CloneGroupConfig(record.Groups[i])
		}
	}
	if record.ProviderGroups != nil {
		out.ProviderGroups = make([]GroupMembership, len(record.ProviderGroups))
		for i, link := range record.ProviderGroups {
			out.ProviderGroups[i] = link
			out.ProviderGroups[i].Provider = cloneRecord(link.Provider, seen)
			out.ProviderGroups[i].Group = accessview.CloneGroupConfig(link.Group)
		}
	}
}
