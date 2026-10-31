package plugintest

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// kv.go supplies conditional JSON storage and integer counters for author tests.
// The shared fixture lock covers each read-modify-write operation, so repeated
// requests exercise version conflicts and atomic increments without a database.
// Expired records behave as absent for reads, writes, deletes, and listings;
// successful writes copy incoming bytes to avoid sharing mutable protobuf data.
//
// The fixture implements storage semantics but intentionally omits production
// key quotas and maintenance scheduling. Tests control those failures through
// injected RPC errors. Pagination uses the last returned key as its exclusive
// cursor, matching the public protocol, while list values are copied so a
// caller cannot modify the stored record through a received response.

// KVGet returns a copied value and version for an unexpired key.
func (h *Host) KVGet(_ context.Context, request *pluginpb.KVGetRequest) (*pluginpb.KVGetResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	value, found := h.get(request.Collection, request.Key)
	return &pluginpb.KVGetResponse{Found: found, ValueJson: bytes.Clone(value.value), Version: value.version}, nil
}

// KVPut atomically applies a version condition and publishes copied JSON bytes.
// Zero writes unconditionally, minus one requires absence, and positive values
// require the current version. Failed conditions leave both value and expiry unchanged.
//
// The fixture lock spans the expiration-aware read, version test, and record
// replacement. An expired record contributes version zero, while an active
// record increments its stored version. JSON bytes are copied before storage
// so a plugin cannot change the fake database by mutating its request after
// the RPC returns, preserving the behavior expected from the real transport.
func (h *Host) KVPut(_ context.Context, request *pluginpb.KVPutRequest) (*pluginpb.KVPutResponse, error) {
	if !json.Valid(request.ValueJson) || len(request.ValueJson) > 64<<10 {
		return nil, Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	}
	expires, err := expiry(request.TtlSeconds)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	previous, found := h.get(request.Collection, request.Key)
	if err := condition(request.IfVersion, previous.version, found); err != nil {
		return nil, err
	}
	value := record{value: bytes.Clone(request.ValueJson), version: previous.version + 1, expires: expires}
	h.records[request.Collection+"\x00"+request.Key] = value
	return &pluginpb.KVPutResponse{Version: value.version}, nil
}

// KVDelete removes a key only when its requested version condition holds.
// An unconditional absent-key deletion returns deleted=false. Expired records
// count as absent and cannot satisfy a positive current-version condition.
func (h *Host) KVDelete(_ context.Context, request *pluginpb.KVDeleteRequest) (*pluginpb.KVDeleteResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous, found := h.get(request.Collection, request.Key)
	if err := condition(request.IfVersion, previous.version, found); err != nil {
		return nil, err
	}
	delete(h.records, request.Collection+"\x00"+request.Key)
	return &pluginpb.KVDeleteResponse{Deleted: found}, nil
}

// KVList returns unexpired keys after the cursor in ascending order.
// A zero limit selects one hundred records and values above five hundred
// clamp to five hundred, matching the host's public pagination contract.
func (h *Host) KVList(_ context.Context, request *pluginpb.KVListRequest) (*pluginpb.KVListResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	limit := int(request.Limit)
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	keys := make([]string, 0)
	prefix := request.Collection + "\x00"
	for compound := range h.records {
		key, ok := strings.CutPrefix(compound, prefix)
		if !ok || key <= request.Cursor || !strings.HasPrefix(key, request.Prefix) {
			continue
		}
		if _, found := h.get(request.Collection, key); found {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	response := &pluginpb.KVListResponse{}
	for _, key := range keys[:min(len(keys), limit)] {
		value, _ := h.get(request.Collection, key)
		response.Items = append(response.Items, &pluginpb.KVItem{Key: key, ValueJson: bytes.Clone(value.value), Version: value.version})
	}
	if len(keys) > limit {
		response.NextCursor = keys[limit-1]
	}
	return response, nil
}

// KVIncr atomically adds a signed delta to an integer JSON value.
// Absent or expired keys begin at zero. Noninteger values and signed overflow
// return INVALID_ARGUMENT without replacing the original record.
//
// The read and integer addition remain under the same fixture mutex, preventing
// concurrent increments from overwriting each other. Signed bounds are checked
// before addition, and successful publication advances the version together
// with the value and expiry. The resulting protobuf contains copied scalars,
// allowing callers to change their response without affecting subsequent reads.
func (h *Host) KVIncr(_ context.Context, request *pluginpb.KVIncrRequest) (*pluginpb.KVIncrResponse, error) {
	expires, err := expiry(request.TtlSeconds)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	previous, found := h.get(request.Collection, request.Key)
	var value int64
	if found {
		if err := json.Unmarshal(previous.value, &value); err != nil {
			return nil, Error(codes.InvalidArgument, "INVALID_ARGUMENT")
		}
	}
	if (request.Delta > 0 && value > math.MaxInt64-request.Delta) || (request.Delta < 0 && value < math.MinInt64-request.Delta) {
		return nil, Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	}
	value += request.Delta
	h.records[request.Collection+"\x00"+request.Key] = record{value: []byte(strconv.FormatInt(value, 10)), version: previous.version + 1, expires: expires}
	return &pluginpb.KVIncrResponse{Value: value, Version: previous.version + 1}, nil
}

// get reads one unexpired record while its caller holds the fixture mutex.
// Expired entries return an empty record, resetting the version for subsequent
// absent-key writes. It does not expose its stored slice outside fixture code.
func (h *Host) get(collection, key string) (record, bool) {
	value, ok := h.records[collection+"\x00"+key]
	if !ok || (!value.expires.IsZero() && !value.expires.After(time.Now())) {
		return record{}, false
	}
	return value, true
}

// condition enforces the protocol's unconditional, absent-only, and version-match rules.
func condition(wanted, current int64, found bool) error {
	if wanted < -1 {
		return Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	}
	if (wanted == -1 && found) || (wanted > 0 && (!found || wanted != current)) {
		return Error(codes.FailedPrecondition, "VERSION_CONFLICT")
	}
	return nil
}

// expiry converts a nonnegative seconds count into an optional expiration timestamp.
// Zero means no expiry. Durations that cannot fit Go's time.Duration are
// rejected instead of wrapping into an already expired record.
func expiry(seconds int64) (time.Time, error) {
	if seconds < 0 || seconds > math.MaxInt64/int64(time.Second) {
		return time.Time{}, Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	}
	if seconds == 0 {
		return time.Time{}, nil
	}
	return time.Now().Add(time.Duration(seconds) * time.Second), nil
}
