package plugin

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"regexp"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// kv.go validates plugin-scoped JSON records and applies sampled storage limits.
// Every storage call receives its namespace from the authenticated process
// binding. Keys and collections are validated before reaching PostgreSQL, while
// the store owns atomic version conditions, expiration, and integer arithmetic.
// Callers receive detached records and cannot select another plugin's namespace.
// List pages also bound combined key and value bytes, keeping legal collections
// readable through the callback transport even when many values reach their limit.
//
// Quotas use the latest successful background measurement, allowing temporary
// overshoot between samples. A blocked namespace can still change or delete
// live keys; storage checks existence at the mutation boundary. Expired keys
// count as absent for that admission decision, even when their physical rows
// remain until cleanup. Lifecycle ownership and periodic measurement are defined
// in kv_maintenance.go and share the same protected usage snapshot.

const (
	// KV limits bound individual payloads and sampled namespace growth while allowing existing records to be repaired or removed.
	KVMaxValueBytes = 64 << 10
	KVMaxKeys       = 50000
	KVMaxBytes      = 64 << 20

	// KVMaxPageBytes bounds key and value payloads, leaving room for at most five
	// hundred records' metadata within the sixteen-MiB callback transport.
	KVMaxPageBytes = 8 << 20
)

// collectionPattern restricts storage collection names to the portable lowercase namespace syntax declared by the plugin protocol.
var collectionPattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// KVEntry contains a detached JSON value and its current physical row version.
type KVEntry struct {
	Key     string          // plugin-local record key text
	Value   json.RawMessage // independently owned JSON value
	Version int64           // monotonically advanced physical row version
}

// KVPutInput supplies a validated conditional JSON replacement and expiration policy.
type KVPutInput struct {
	Collection string          // plugin-local storage collection name
	Key        string          // printable UTF-8 record key
	Value      json.RawMessage // caller-owned input JSON bytes
	IfVersion  int64           // zero unconditional, negative one absent, positive matching
	TTLSeconds int64           // seconds; zero disables expiration
}

// KVListInput identifies one bounded lexicographic page within a collection.
type KVListInput struct {
	Collection string // plugin-local storage collection name
	Prefix     string // optional literal key prefix
	Cursor     string // exclusive previous continuation key
	Limit      int    // requested maximum page size
}

// KVPage contains ordered detached records and an optional continuation key.
type KVPage struct {
	Items      []KVEntry // independently owned ordered records
	NextCursor string    // last key when more records exist
}

// KVCounter contains the integer value and version committed by one increment.
type KVCounter struct {
	Value   int64 // signed current counter value
	Version int64 // committed physical row version
}

// KVUsage measures physically retained keys and their PostgreSQL storage accounting size.
type KVUsage struct {
	Keys  int64 // retained namespace row count
	Bytes int64 // JSON text and key bytes
}

// KVStore owns atomic expiration-aware operations in an authenticated plugin namespace.
type KVStore interface {
	// KVGet returns a detached live row or nil when absent or expired.
	// Expiration is evaluated by the database clock. Missing data is an ordinary
	// result and never exposes a stale physical row's value or version.
	KVGet(context.Context, string, string, string) (*KVEntry, error)
	// KVPut commits a conditional replacement and advances the physical row version.
	// Expired rows satisfy absence but preserve their previous version increment.
	// blocked prohibits new logical keys while allowing live records to change.
	KVPut(context.Context, string, KVPutInput, bool) (int64, error)
	// KVDelete removes a key when its expiration-aware version condition holds.
	// Unconditional or absent-only deletion of an absent key returns false.
	// A positive condition on absent or expired data returns ErrVersionConflict.
	KVDelete(context.Context, string, string, string, int64) (bool, error)
	// KVList returns at most limit live records in database byte ordering.
	// Prefix is literal and cursor exclusive. Returned byte slices belong to the
	// caller and do not share a reused database scan buffer.
	KVList(context.Context, string, KVListInput) ([]KVEntry, error)
	// KVIncr atomically adds a signed delta and advances the physical row version.
	// Absent or expired counters start at zero. Nonintegral values and overflow
	// fail without mutation; blocked prohibits creating an absent logical key.
	KVIncr(context.Context, string, string, string, int64, int64, bool) (KVCounter, error)
	// KVMeasure returns detached usage for every namespace, including retained expired records.
	// The result replaces the previous sample only after a complete successful
	// query; no installation record is required for retained plugin data.
	KVMeasure(context.Context) (map[string]KVUsage, error)
	// KVExpire deletes at most the supplied number of expired physical records.
	// Each call commits its own batch. Cancellation can stop a maintenance run
	// between batches without restoring deletions already completed by earlier calls.
	KVExpire(context.Context, int) (int64, error)
}

// KV owns input admission, sampled quotas, and one maintenance worker lifetime.
type KV struct {
	store  KVStore      // expiration-aware namespace persistence adapter
	logger *slog.Logger // background maintenance diagnostic destination
	// mu protects usage and lifecycle publication. started and stopped prevent duplicate workers or late restart; cancel and done identify the owned worker.
	mu      sync.RWMutex
	usage   map[string]KVUsage // last complete namespace usage sample
	started bool               // background worker already launched
	stopped bool               // terminal maintenance shutdown requested
	cancel  context.CancelFunc // owned worker cancellation signal
	done    chan struct{}      // owned worker completion signal
}

// NewKV constructs a dormant storage capability with an initially empty quota sample.
// Start owns background sampling and expiration cleanup. Until its first successful
// sample, new keys remain allowed under the deliberately soft quota policy.
func NewKV(store KVStore, logger *slog.Logger) *KV {
	if logger == nil {
		logger = slog.Default()
	}
	return &KV{store: store, logger: logger, usage: make(map[string]KVUsage)}
}

// Get reads a live JSON record from the bound plugin's collection.
// Missing or expired records return nil without an error. Invalid collection
// or key syntax fails before the namespace is passed to persistence.
func (s *KV) Get(ctx context.Context, binding Binding, collection, key string) (*KVEntry, error) {
	if !validKVIdentity(collection, key) {
		return nil, ErrInvalidArgument
	}
	return s.store.KVGet(ctx, binding.PluginID, collection, key)
}

// Put validates JSON, version semantics, and expiration before replacing one record.
// The caller keeps Value stable until return. The sampled quota is passed to
// storage so existence and mutation are decided together under its key lock.
// @project-doc docs/architecture/plugin_system.md#kv_storage
func (s *KV) Put(ctx context.Context, binding Binding, input KVPutInput) (int64, error) {
	if !validKVIdentity(input.Collection, input.Key) || input.IfVersion < -1 ||
		!validKVTTL(input.TTLSeconds) || len(input.Value) > KVMaxValueBytes || !utf8.Valid(input.Value) || !json.Valid(input.Value) {
		return 0, ErrInvalidArgument
	}
	return s.store.KVPut(ctx, binding.PluginID, input, s.blocked(binding.PluginID))
}

// Delete removes one record according to unconditional, absent-only, or matching-version semantics.
// Expired records count as absent. Negative conditions other than minus one
// fail before storage, and namespace growth limits do not prevent deletion.
func (s *KV) Delete(ctx context.Context, binding Binding, collection, key string, version int64) (bool, error) {
	if !validKVIdentity(collection, key) || version < -1 {
		return false, ErrInvalidArgument
	}
	return s.store.KVDelete(ctx, binding.PluginID, collection, key, version)
}

// List returns one ascending page and a cursor only when another row exists.
// Nonpositive limits use one hundred; larger requests clamp at five hundred.
// Prefix and cursor may be empty, but otherwise follow the printable key rules.
//
// Combined key and value bytes can shorten a page before the requested count.
// The cursor always identifies the last returned record, so subsequent calls
// retain rows omitted by either bound. Every valid record fits the byte budget,
// ensuring that a nonempty collection always makes progress. Metadata for the
// bounded record count fits the remaining callback transport allowance.
func (s *KV) List(ctx context.Context, binding Binding, input KVListInput) (*KVPage, error) {
	if !collectionPattern.MatchString(input.Collection) || (input.Prefix != "" && !validKVKey(input.Prefix)) ||
		(input.Cursor != "" && !validKVKey(input.Cursor)) {
		return nil, ErrInvalidArgument
	}
	if input.Limit <= 0 {
		input.Limit = 100
	}
	// One extra row distinguishes a full final page from a page requiring continuation.
	limit := min(input.Limit, 500)
	input.Limit = limit + 1
	entries, err := s.store.KVList(ctx, binding.PluginID, input)
	if err != nil {
		return nil, err
	}
	count := min(len(entries), limit)
	payloadBytes := 0
	// Stop before the first record exceeding the payload budget; the retained
	// last key resumes before every omitted record on the next call.
	for index, entry := range entries[:count] {
		payloadBytes += len(entry.Key) + len(entry.Value)
		if payloadBytes > KVMaxPageBytes {
			count = index
			break
		}
	}
	page := &KVPage{Items: entries[:count]}
	if count < len(entries) {
		page.NextCursor = page.Items[count-1].Key
	}
	return page, nil
}

// Incr atomically adds a signed delta to one integer JSON counter.
// Missing or expired values start at zero. Storage rejects nonintegral values
// and signed overflow without changing the value, version, or expiration.
func (s *KV) Incr(ctx context.Context, binding Binding, collection, key string, delta, ttl int64) (KVCounter, error) {
	if !validKVIdentity(collection, key) || !validKVTTL(ttl) {
		return KVCounter{}, ErrInvalidArgument
	}
	return s.store.KVIncr(ctx, binding.PluginID, collection, key, delta, ttl, s.blocked(binding.PluginID))
}

// blocked reports whether the latest complete sample reached either namespace growth ceiling.
func (s *KV) blocked(id string) bool {
	s.mu.RLock()
	usage := s.usage[id]
	s.mu.RUnlock()
	return usage.Keys >= KVMaxKeys || usage.Bytes >= KVMaxBytes
}

// validKVIdentity checks both portable collection syntax and printable bounded key bytes.
func validKVIdentity(collection, key string) bool {
	return collectionPattern.MatchString(collection) && validKVKey(key)
}

// validKVKey admits nonempty printable UTF-8 strings within the protocol byte bound.
func validKVKey(key string) bool {
	if key == "" || len(key) > 256 || !utf8.ValidString(key) {
		return false
	}
	for _, character := range key {
		if !unicode.IsPrint(character) {
			return false
		}
	}
	return true
}

// validKVTTL keeps seconds representable by the SDK's signed duration-based expiration option.
func validKVTTL(seconds int64) bool {
	return seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second)
}
