package sdk

import (
	"context"
	"encoding/json"
	"time"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// kv.go maps plugin JSON operations to the host's isolated key-value store.
// Conditional writes expose absent-only and version-match rules explicitly,
// while TTL values remain relative durations. The host owns quotas, atomicity,
// expiration, and plugin namespace isolation for every operation.

// KV accesses the connected plugin's isolated persistent JSON collections.
type KV hostClient

// PutOption changes one condition or lifetime for a KV write request.
type PutOption func(*pluginpb.KVPutRequest)

// IfAbsent requires that the key be absent or expired when written.
// A competing successful insertion causes ErrVersionConflict. The host applies
// the condition atomically with the write, so callers need no preliminary read.
func IfAbsent() PutOption {
	return func(request *pluginpb.KVPutRequest) { request.IfVersion = -1 }
}

// IfVersion requires the currently stored version to equal the supplied value.
// Zero selects an unconditional write and minus one selects absence. Other
// negative values are rejected by the host as invalid arguments.
func IfVersion(version int64) PutOption {
	return func(request *pluginpb.KVPutRequest) { request.IfVersion = version }
}

// TTL sets a write's lifetime in whole seconds, with zero meaning no expiration.
// Positive fractional seconds round upward so a short duration never becomes
// a permanent key. Negative durations remain invalid at the host boundary.
func TTL(duration time.Duration) PutOption {
	return func(request *pluginpb.KVPutRequest) {
		seconds := int64(duration / time.Second)
		// Round positive fractional seconds upward because truncating a subsecond TTL to zero would turn temporary data into permanent storage.
		if duration > 0 && duration%time.Second != 0 {
			seconds++
		}
		if duration < 0 && seconds == 0 {
			seconds = -1
		}
		request.TtlSeconds = seconds
	}
}

// Get reads a key's JSON bytes, presence flag, and current stored version.
func (s KV) Get(ctx context.Context, collection, key string) (*pluginpb.KVGetResponse, error) {
	return hostResult(s.rpc.KVGet(ctx, &pluginpb.KVGetRequest{Collection: collection, Key: key}))
}

// Put encodes a JSON value and returns its newly stored version.
// Conditions and TTL default to unconditional and nonexpiring. Options apply
// in order, so a later condition or lifetime replaces the earlier selection.
func (s KV) Put(ctx context.Context, collection, key string, value any, options ...PutOption) (int64, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	request := &pluginpb.KVPutRequest{Collection: collection, Key: key, ValueJson: data}
	for _, option := range options {
		option(request)
	}
	response, err := hostResult(s.rpc.KVPut(ctx, request))
	if err != nil {
		return 0, err
	}
	return response.Version, nil
}

// Delete removes a key using zero for unconditional or a matching positive version.
// The returned flag distinguishes deletion from an already absent key. A
// mismatched conditional version returns ErrVersionConflict without changing data.
func (s KV) Delete(ctx context.Context, collection, key string, ifVersion int64) (bool, error) {
	response, err := hostResult(s.rpc.KVDelete(ctx, &pluginpb.KVDeleteRequest{Collection: collection, Key: key, IfVersion: ifVersion}))
	if err != nil {
		return false, err
	}
	return response.Deleted, nil
}

// List reads a prefix-filtered page ordered by key with a continuation cursor.
func (s KV) List(ctx context.Context, request *pluginpb.KVListRequest) (*pluginpb.KVListResponse, error) {
	return hostResult(s.rpc.KVList(ctx, request))
}

// Incr atomically adjusts an integer value, starting absent keys from zero.
// The host rejects noninteger stored values and overflowing arithmetic. A zero
// TTL leaves the resulting key without an expiration time.
func (s KV) Incr(ctx context.Context, collection, key string, delta int64, ttl time.Duration) (*pluginpb.KVIncrResponse, error) {
	options := &pluginpb.KVPutRequest{}
	TTL(ttl)(options)
	return hostResult(s.rpc.KVIncr(ctx, &pluginpb.KVIncrRequest{Collection: collection, Key: key, Delta: delta, TtlSeconds: options.TtlSeconds}))
}
