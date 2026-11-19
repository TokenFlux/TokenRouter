package rediscache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// snapshot.go publishes versioned scheduler buckets and reads provider
// metadata with recent-use updates.
// Bucket versions contain ordered provider IDs, while full provider JSON and
// selection metadata live in shared provider keys. A writer captures the
// bucket epoch, allocates a version, writes provider data and membership,
// then conditionally activates that version. Epoch and retirement checks
// protect the bucket's active membership; earlier writes to shared provider
// records remain possible even when activation is rejected.
//
// Readers obtain readiness, active version and membership separately before
// fetching metadata and recent-use values in aligned chunks. Empty membership
// or a missing metadata value produces a cache miss so SnapshotService can
// query the database. A separate monotonic last-used key survives stale
// payload writes, and readers prefer the later of its millisecond time and
// the embedded timestamp. Retirement keeps a tombstone, while explicit
// reopening returns the generation usable by later writers.

// Scheduler Redis names separate shared provider records, bucket publication
// state and the poller watermark; chunk limits bound the payload or member
// work submitted per command phase.
const (
	schedulerBucketSetKey           = "sched:v6:buckets"             // registered scheduler bucket identities
	schedulerOutboxWatermarkKey     = "sched:v6:outbox:watermark"    // shared event polling cursor
	schedulerProviderPrefix         = "sched:v6:provider:"           // full provider JSON namespace
	schedulerProviderMetaPrefix     = "sched:v6:meta:"               // selection metadata JSON namespace
	schedulerProviderLastUsedPrefix = "sched:v6:provider:last_used:" // monotonic scheduling millisecond namespace
	schedulerActivePrefix           = "sched:v6:active:"             // active bucket version namespace
	schedulerReadyPrefix            = "sched:v6:ready:"              // bucket read readiness namespace
	schedulerVersionPrefix          = "sched:v6:ver:"                // bucket version allocation namespace
	schedulerEpochPrefix            = "sched:v6:epoch:"              // bucket writer generation namespace
	schedulerRetiredPrefix          = "sched:v6:retired:"            // persistent bucket retirement namespace
	schedulerSnapshotPrefix         = "sched:v6:"                    // versioned bucket membership namespace

	defaultSchedulerSnapshotMGetChunkSize  = 128 // default keys per batch read
	defaultSchedulerSnapshotWriteChunkSize = 256 // default provider or member chunk
	schedulerLastUsedUpdateChunkSize       = 256 // provider updates per Lua batch

	// snapshotGraceTTLSeconds retains superseded or retired membership sets
	// briefly after their active marker changes, giving readers holding the
	// prior version a bounded read window.
	snapshotGraceTTLSeconds = 60

	schedulerGroupLifecycleLockPrefix      = "sched:v6:group:lifecycle-lock:" // group lifecycle owner lock namespace
	schedulerGroupLifecycleOwnerTokenBytes = 16                               // random lifecycle owner byte count
)

// Publication scripts check bucket epochs and retirement within Redis, while
// last-used scripts compare separate observations and lifecycle-release
// scripts verify the stored owner token before deleting locks.
var (
	// updateSchedulerLastUsedScript advances each side timestamp when the full
	// provider key exists and the candidate is newer. Paired KEYS preserve each
	// provider/value association within the supplied update chunk.
	updateSchedulerLastUsedScript = redis.NewScript(`
local updated = 0
for index = 1, #ARGV do
    local key_index = (index - 1) * 2 + 1
    local candidate = tonumber(ARGV[index])
    if candidate == nil then
        return redis.error_reply('invalid last_used value')
    end
    if redis.call('EXISTS', KEYS[key_index]) == 1 then
        local current = tonumber(redis.call('GET', KEYS[key_index + 1]))
        if current == nil or candidate > current then
            redis.call('SET', KEYS[key_index + 1], ARGV[index])
            updated = updated + 1
        end
    end
end
return updated
`)

	// captureBucketWriteTokenScript initializes or reads a positive writer
	// epoch, returning retired/fenced sentinels for unusable buckets. Ordinary
	// capture leaves retirement markers in place.
	captureBucketWriteTokenScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 1 then
    return -1
end

local currentEpoch = redis.call('GET', KEYS[1])
if currentEpoch == false then
    redis.call('SET', KEYS[1], '1')
    return 1
end

local parsedEpoch = tonumber(currentEpoch)
if parsedEpoch == nil or parsedEpoch < 1 then
    return -2
end
return parsedEpoch
`)

	// allocateSnapshotVersionScript validates retirement and the expected epoch
	// before incrementing this bucket's version counter, so a fenced writer
	// cannot allocate a new membership version.
	allocateSnapshotVersionScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 1 then
    return -1
end

local currentEpoch = tonumber(redis.call('GET', KEYS[1]))
local expectedEpoch = tonumber(ARGV[1])
if currentEpoch == nil or expectedEpoch == nil or currentEpoch ~= expectedEpoch then
    return -2
end

return redis.call('INCR', KEYS[3])
`)

	// retireBucketScript advances the epoch on first retirement, keeps the
	// tombstone and removes readiness/active registration. The prior active
	// membership receives a grace expiry; provider records stay shared.
	retireBucketScript = redis.NewScript(`
local retired = redis.call('GET', KEYS[2])
local currentEpoch = tonumber(redis.call('GET', KEYS[1])) or 0

if retired == false then
    currentEpoch = currentEpoch + 1
    if currentEpoch < 1 then
        currentEpoch = 1
    end
    redis.call('SET', KEYS[1], tostring(currentEpoch))
    redis.call('SET', KEYS[2], tostring(currentEpoch))
elseif currentEpoch < 1 then
    currentEpoch = tonumber(retired) or 1
    redis.call('SET', KEYS[1], tostring(currentEpoch))
end

redis.call('SREM', KEYS[3], ARGV[1])
local currentActive = redis.call('GET', KEYS[5])
if currentActive ~= false then
    redis.call('EXPIRE', ARGV[2] .. currentActive, tonumber(ARGV[3]))
end
redis.call('DEL', KEYS[4], KEYS[5])
return currentEpoch
`)

	// reopenBucketScript clears a valid retirement marker while retaining its
	// generation and removing retired publication markers. Already-open buckets
	// return their current epoch without replacing active membership.
	reopenBucketScript = redis.NewScript(`
local currentEpochRaw = redis.call('GET', KEYS[1])
local currentEpoch = tonumber(currentEpochRaw)
local retiredEpochRaw = redis.call('GET', KEYS[2])

if retiredEpochRaw == false then
    if currentEpochRaw == false then
        redis.call('SET', KEYS[1], '1')
        return 1
    end
    if currentEpoch == nil or currentEpoch < 1 then
        return -2
    end
    return currentEpoch
end

local retiredEpoch = tonumber(retiredEpochRaw)
if retiredEpoch == nil or retiredEpoch < 1 then
    return -2
end
if currentEpoch == nil or currentEpoch < retiredEpoch then
    currentEpoch = retiredEpoch
end

redis.call('SET', KEYS[1], tostring(currentEpoch))
redis.call('DEL', KEYS[2])
redis.call('SREM', KEYS[3], ARGV[1])
local currentActive = redis.call('GET', KEYS[5])
if currentActive ~= false then
    redis.call('EXPIRE', ARGV[2] .. currentActive, tonumber(ARGV[3]))
end
redis.call('DEL', KEYS[4], KEYS[5])
return currentEpoch
`)

	// releaseGroupLifecycleLeaseScript deletes this group's lifecycle lock when
	// the stored owner matches, protecting a successor whose token replaced an
	// expired lease.
	releaseGroupLifecycleLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`)

	// activateSnapshotScript rejects retired or mismatched epochs and deletes
	// their unpublished membership sets. An older version is also deleted and
	// returns zero; accepted versions update active/readiness/registration and
	// expire the previous membership set.
	activateSnapshotScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[6]) == 1 then
    redis.call('DEL', KEYS[4])
    return -1
end

local currentEpoch = tonumber(redis.call('GET', KEYS[5]))
local expectedEpoch = tonumber(ARGV[5])
if currentEpoch == nil or expectedEpoch == nil or currentEpoch ~= expectedEpoch then
    redis.call('DEL', KEYS[4])
    return -2
end

local currentActive = redis.call('GET', KEYS[1])
local newVersion = tonumber(ARGV[1])

if currentActive ~= false then
	local curVersion = tonumber(currentActive)
	if curVersion and newVersion < curVersion then
		redis.call('DEL', KEYS[4])
		return 0
	end
end

redis.call('SET', KEYS[1], ARGV[1])
redis.call('SET', KEYS[2], '1')
redis.call('SADD', KEYS[3], ARGV[2])

if currentActive ~= false and currentActive ~= ARGV[1] then
	redis.call('EXPIRE', ARGV[3] .. currentActive, tonumber(ARGV[4]))
end

return 1
`)
)

// SnapshotCache stores shared provider payloads and token-checked bucket
// membership versions in Redis.
type SnapshotCache struct {
	// codec encodes both provider representations and updates decoded scheduling
	// times; its implementation remains usable for the lifetime of this storage
	// wrapper.
	codec SnapshotCodec // shared provider representation codec
	// rdb remains caller-owned and open through bucket publication, lifecycle
	// lease release, provider lookup and separate side-key maintenance.
	rdb *redis.Client // shared snapshot storage client
	// mgetChunkSize limits keys per MGET when assembling ordered reads;
	// mgetChunked substitutes the read default when this value is nonpositive.
	mgetChunkSize int // keys per read chunk
	// writeChunkSize is positive after construction and bounds provider records
	// per write pipeline plus members per ZADD; writing requires a positive
	// value.
	writeChunkSize int // providers or members per write
}

// SnapshotCacheOptions supplies constructor overrides for ordered reads and
// provider/member write chunks.
type SnapshotCacheOptions struct {
	// MGetChunkSize overrides keys per MGET when positive; NewSnapshotCache
	// keeps its default for a nonpositive first-option value.
	MGetChunkSize int // keys per MGET command
	// WriteChunkSize overrides provider/member chunk size when positive;
	// NewSnapshotCache keeps its default for a nonpositive first-option value.
	WriteChunkSize int // providers or members per chunk
}

// NewSnapshotCache constructs Redis snapshot storage with the supplied codec
// and caller-owned client. It reads only the first options value, applying
// positive sizes and keeping defaults for missing or nonpositive overrides.
func NewSnapshotCache(rdb *redis.Client, codec SnapshotCodec, options ...SnapshotCacheOptions) *SnapshotCache {
	opts := SnapshotCacheOptions{MGetChunkSize: defaultSchedulerSnapshotMGetChunkSize, WriteChunkSize: defaultSchedulerSnapshotWriteChunkSize}
	if len(options) > 0 {
		if options[0].MGetChunkSize > 0 {
			opts.MGetChunkSize = options[0].MGetChunkSize
		}
		if options[0].WriteChunkSize > 0 {
			opts.WriteChunkSize = options[0].WriteChunkSize
		}
	}
	return &SnapshotCache{rdb: rdb, codec: codec, mgetChunkSize: opts.MGetChunkSize, writeChunkSize: opts.WriteChunkSize}
}

// GetSnapshot returns ordered selection metadata for a ready bucket. Absent
// readiness or a value other than "1", a missing active key, empty membership
// or missing metadata returns hit=false; decoding and Redis errors abort the
// read.
//
// Membership IDs determine the order of two aligned key lists for metadata
// and last-used values. Chunk assembly retains nil positions so each side
// value stays associated with the decoded provider at that position. Reads
// are separate commands and may observe concurrent publication or deletion; a
// miss for missing metadata lets SnapshotService apply database fallback
// policy to the whole bucket.
func (c *SnapshotCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]scheduler.SnapshotProvider, bool, error) {
	// Resolve readiness and active membership markers before provider reads so
	// absent publication state directs SnapshotService toward its bucket-level
	// fallback policy.
	readyKey := schedulerBucketKey(schedulerReadyPrefix, bucket)
	readyVal, err := c.rdb.Get(ctx, readyKey).Result()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if readyVal != "1" {
		return nil, false, nil
	}

	activeKey := schedulerBucketKey(schedulerActivePrefix, bucket)
	activeVal, err := c.rdb.Get(ctx, activeKey).Result()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	snapshotKey := schedulerSnapshotKey(bucket, activeVal)
	ids, err := c.rdb.ZRange(ctx, snapshotKey, 0, -1).Result()
	if err != nil {
		return nil, false, err
	}
	if len(ids) == 0 {
		// Treat an empty snapshot as a cache miss so the caller can query the database.
		// A newly created group may have gained providers since its empty snapshot was written.
		return nil, false, nil
	}

	keys := make([]string, 0, len(ids))
	lastUsedKeys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, schedulerProviderMetaKey(id))
		lastUsedKeys = append(lastUsedKeys, schedulerLastUsedKey(id))
	}
	values, err := c.mgetChunked(ctx, keys)
	if err != nil {
		return nil, false, err
	}
	lastUsedValues, err := c.mgetChunked(ctx, lastUsedKeys)
	if err != nil {
		return nil, false, err
	}

	providers := make([]scheduler.SnapshotProvider, 0, len(values))
	for i, val := range values {
		// Provider deletion can leave a cached membership ID in place; miss the
		// whole bucket when its metadata is absent so selection does not silently
		// shrink that candidate list.
		if val == nil {
			return nil, false, nil
		}
		provider, err := c.codec.Decode(val)
		if err != nil {
			return nil, false, err
		}
		if err := c.applySchedulerLastUsed(provider, lastUsedValues[i]); err != nil {
			return nil, false, err
		}
		providers = append(providers, provider)
	}

	return providers, true, nil
}

// CaptureBucketWriteToken returns this bucket's current epoch, initializing
// its first generation when absent. Retirement returns a typed error, as do
// stored epochs that are nonnumeric or below one.
func (c *SnapshotCache) CaptureBucketWriteToken(ctx context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	result, err := captureBucketWriteTokenScript.Run(ctx, c.rdb, []string{
		schedulerBucketKey(schedulerEpochPrefix, bucket),
		schedulerBucketKey(schedulerRetiredPrefix, bucket),
	}).Int64()
	if err != nil {
		return scheduler.SchedulerBucketWriteToken{}, err
	}
	if err := schedulerBucketWriteResultError(result, bucket); err != nil {
		return scheduler.SchedulerBucketWriteToken{}, err
	}
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: result}, nil
}

// RetireBucket marks this bucket retired, invalidates new reads and gives the
// prior membership a grace expiry. Repeated retirement retains the
// generation; shared provider payloads remain available to other buckets.
func (c *SnapshotCache) RetireBucket(ctx context.Context, bucket scheduler.SchedulerBucket) error {
	snapshotKeyPrefix := fmt.Sprintf("%s%d:%s:%s:v", schedulerSnapshotPrefix, bucket.GroupID, bucket.Platform, bucket.Mode)
	result, err := retireBucketScript.Run(ctx, c.rdb, []string{
		schedulerBucketKey(schedulerEpochPrefix, bucket),
		schedulerBucketKey(schedulerRetiredPrefix, bucket),
		schedulerBucketSetKey,
		schedulerBucketKey(schedulerReadyPrefix, bucket),
		schedulerBucketKey(schedulerActivePrefix, bucket),
	}, bucket.String(), snapshotKeyPrefix, snapshotGraceTTLSeconds).Int64()
	if err != nil {
		return err
	}
	if result < 1 {
		return fmt.Errorf("retire scheduler bucket %s returned invalid epoch %d", bucket.String(), result)
	}
	return nil
}

// ReopenBucket clears a valid retirement marker and returns the generation
// for subsequent publication, recovering a missing, invalid or lower epoch
// from that marker. An invalid retirement marker, or invalid epoch with no
// retirement marker, returns an error; an already-open valid bucket keeps
// its active state.
func (c *SnapshotCache) ReopenBucket(ctx context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	snapshotKeyPrefix := fmt.Sprintf("%s%d:%s:%s:v", schedulerSnapshotPrefix, bucket.GroupID, bucket.Platform, bucket.Mode)
	result, err := reopenBucketScript.Run(ctx, c.rdb, []string{
		schedulerBucketKey(schedulerEpochPrefix, bucket),
		schedulerBucketKey(schedulerRetiredPrefix, bucket),
		schedulerBucketSetKey,
		schedulerBucketKey(schedulerReadyPrefix, bucket),
		schedulerBucketKey(schedulerActivePrefix, bucket),
	}, bucket.String(), snapshotKeyPrefix, snapshotGraceTTLSeconds).Int64()
	if err != nil {
		return scheduler.SchedulerBucketWriteToken{}, err
	}
	if err := schedulerBucketWriteResultError(result, bucket); err != nil {
		return scheduler.SchedulerBucketWriteToken{}, err
	}
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: result}, nil
}

// TryAcquireGroupLifecycleLease attempts a random-token group lock using a
// positive group ID and TTL. Invalid inputs return a lease-invalid error;
// contention returns no lease, and successful callers release their owner
// token. go-redis uses at least one millisecond for positive expiry values and
// discards finer duration precision when building the Redis command.
func (c *SnapshotCache) TryAcquireGroupLifecycleLease(ctx context.Context, groupID int64, ttl time.Duration) (scheduler.SchedulerGroupLifecycleLease, bool, error) {
	if groupID <= 0 {
		return scheduler.SchedulerGroupLifecycleLease{}, false, fmt.Errorf("%w: group id must be positive", scheduler.ErrSchedulerGroupLifecycleLeaseInvalid)
	}
	if ttl <= 0 {
		return scheduler.SchedulerGroupLifecycleLease{}, false, fmt.Errorf("%w: ttl must be positive", scheduler.ErrSchedulerGroupLifecycleLeaseInvalid)
	}
	ownerToken, err := newSchedulerGroupLifecycleOwnerToken()
	if err != nil {
		return scheduler.SchedulerGroupLifecycleLease{}, false, err
	}
	acquired, err := c.rdb.SetNX(ctx, schedulerGroupLifecycleLockKey(groupID), ownerToken, ttl).Result()
	if err != nil {
		return scheduler.SchedulerGroupLifecycleLease{}, false, err
	}
	if !acquired {
		return scheduler.SchedulerGroupLifecycleLease{}, false, nil
	}
	return scheduler.SchedulerGroupLifecycleLease{GroupID: groupID, OwnerToken: ownerToken}, true, nil
}

// ReleaseGroupLifecycleLease releases the lock held by this valid group/owner
// token. Missing or replaced tokens report lease loss; a successful release
// is followed by lease loss on another release attempt.
func (c *SnapshotCache) ReleaseGroupLifecycleLease(ctx context.Context, lease scheduler.SchedulerGroupLifecycleLease) error {
	if !lease.ValidFor(lease.GroupID) {
		return scheduler.ErrSchedulerGroupLifecycleLeaseInvalid
	}
	result, err := releaseGroupLifecycleLeaseScript.Run(
		ctx,
		c.rdb,
		[]string{schedulerGroupLifecycleLockKey(lease.GroupID)},
		lease.OwnerToken,
	).Int64()
	if err != nil {
		return err
	}
	if result == 0 {
		return fmt.Errorf("%w: group=%d", scheduler.ErrSchedulerGroupLifecycleLeaseLost, lease.GroupID)
	}
	if result != 1 {
		return fmt.Errorf("release scheduler group lifecycle lease returned %d", result)
	}
	return nil
}

// newSchedulerGroupLifecycleOwnerToken returns random bytes encoded as the
// lifecycle owner's hexadecimal identity.
func newSchedulerGroupLifecycleOwnerToken() (string, error) {
	raw := make([]byte, schedulerGroupLifecycleOwnerTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate scheduler group lifecycle owner token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// SetSnapshot delegates a token-checked bucket write with readable, non-nil
// provider snapshots. Encoding failures can omit entries, and nil error
// includes an older version whose activation was ignored by Redis.
func (c *SnapshotCache) SetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, providers []scheduler.SnapshotProvider) error {
	_, err := c.SetSnapshotAndReturnProviderIDs(ctx, bucket, token, providers)
	return err
}

// SetSnapshotAndReturnProviderIDs writes shared provider payloads and a
// bucket version, then returns encoded IDs in input order after a non-error
// activation response. Supply non-nil snapshots whose metadata remains
// readable even if encoding fails.
//
// The token binds bucket identity and epoch, checked at allocation and
// activation. Encoding failures are logged and skipped; duplicate IDs remain
// in the returned list but Redis membership keeps their final ordinal. Shared
// provider writes precede membership activation and can remain after fencing
// or write failure. An older activation returns zero without error, retaining
// the newer active bucket while the encoded IDs are still returned.
func (c *SnapshotCache) SetSnapshotAndReturnProviderIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, providers []scheduler.SnapshotProvider) ([]int64, error) {
	if !token.ValidFor(bucket) {
		return nil, fmt.Errorf("%w: bucket=%s", scheduler.ErrSchedulerBucketWriteFenced, bucket.String())
	}
	// Check the generation before writing and again before switching membership
	// because retirement can occur after allocation while shared payload and
	// member writes are in flight.
	version, err := c.allocateSnapshotVersion(ctx, bucket, token)
	if err != nil {
		return nil, err
	}
	providerIDs, err := c.writeSnapshotVersionAndReturnProviderIDs(ctx, bucket, version, providers)
	if err != nil {
		return nil, err
	}
	if err := c.activateSnapshotVersion(ctx, bucket, token, version); err != nil {
		return nil, err
	}
	return providerIDs, nil
}

// SetSnapshotByProviderIDs publishes membership using IDs already encoded in
// the same rebuild batch. It requires a token for this bucket, and each
// bucket allocates and checks its own version.
//
// This path writes bucket membership and leaves shared provider payloads
// untouched, so it cannot itself recreate payloads deleted after the earlier
// write. A read misses when that metadata is absent. Membership writes
// precede activation, with epoch/retirement checks at both allocation and
// activation. Older versions are ignored successfully; callers keep reused
// IDs associated with the intended batch's shared provider data.
func (c *SnapshotCache) SetSnapshotByProviderIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, providerIDs []int64) error {
	if !token.ValidFor(bucket) {
		return fmt.Errorf("%w: bucket=%s", scheduler.ErrSchedulerBucketWriteFenced, bucket.String())
	}
	// Allocate and recheck this bucket's own generation even when provider JSON
	// is reused, keeping ID-only publication subject to retirement and
	// active-version ordering.
	version, err := c.allocateSnapshotVersion(ctx, bucket, token)
	if err != nil {
		return err
	}
	if err := c.writeSnapshotProviderIDs(ctx, bucket, version, providerIDs); err != nil {
		return err
	}
	return c.activateSnapshotVersion(ctx, bucket, token, version)
}

// allocateSnapshotVersion allocates this bucket's next version after Redis
// checks token epoch and retirement. A rejected writer receives a typed error
// before provider payload or membership work begins.
func (c *SnapshotCache) allocateSnapshotVersion(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken) (string, error) {
	result, err := allocateSnapshotVersionScript.Run(ctx, c.rdb, []string{
		schedulerBucketKey(schedulerEpochPrefix, bucket),
		schedulerBucketKey(schedulerRetiredPrefix, bucket),
		schedulerBucketKey(schedulerVersionPrefix, bucket),
	}, token.Epoch).Int64()
	if err != nil {
		return "", err
	}
	if err := schedulerBucketWriteResultError(result, bucket); err != nil {
		return "", err
	}
	return strconv.FormatInt(result, 10), nil
}

// writeSnapshotVersionAndReturnProviderIDs writes encodable shared provider
// payloads before this version's membership and returns their IDs in input
// order. Callers supply snapshots with readable metadata, and bad encodings
// are omitted by the provider writer.
//
// Membership is constructed from the IDs whose payload write phase completed,
// keeping omitted providers out of this bucket version. Provider-phase
// failure stops before membership work but can follow earlier shared writes.
// Membership failure also leaves those completed payload writes in place. The
// caller later attempts token-checked activation; this helper orders
// preparation without making shared records part of the bucket's epoch check.
func (c *SnapshotCache) writeSnapshotVersionAndReturnProviderIDs(ctx context.Context, bucket scheduler.SchedulerBucket, version string, providers []scheduler.SnapshotProvider) ([]int64, error) {
	// Write the shared payloads before membership refers to their IDs; a later
	// membership error leaves those earlier completed provider writes available
	// to other buckets.
	providerIDs, err := c.writeProviderIDs(ctx, providers)
	if err != nil {
		return nil, err
	}
	if err := c.writeSnapshotProviderIDs(ctx, bucket, version, providerIDs); err != nil {
		return nil, err
	}
	return providerIDs, nil
}

// writeSnapshotProviderIDs converts supplied IDs to ordinal Redis members and
// writes this bucket version. Empty IDs create no membership key; duplicate
// IDs later receive their final ordinal during ZADD.
func (c *SnapshotCache) writeSnapshotProviderIDs(ctx context.Context, bucket scheduler.SchedulerBucket, version string, providerIDs []int64) error {
	members := schedulerSnapshotMembers(providerIDs)
	return c.writeSnapshotMembers(ctx, bucket, version, members)
}

// schedulerSnapshotMembers returns one ordinal-scored Redis entry per
// supplied ID, or nil for empty input. The returned slice keeps input order
// and duplicates for the writer to submit.
//
// Redis identifies each member by its decimal provider ID, so repeated IDs
// acquire their last submitted ordinal when ZADD writes them. For IDs 701,
// 702, 701, the stored member order is 702 followed by 701. This keeps final
// occurrences aligned with the supplied selection order rather than sorting
// candidates by numeric provider identity.
func schedulerSnapshotMembers(providerIDs []int64) []redis.Z {
	if len(providerIDs) == 0 {
		return nil
	}
	// Use input ordinals for selection order; repeated decimal IDs share a Redis
	// member, so later ZADD submissions replace that member's earlier score.
	members := make([]redis.Z, 0, len(providerIDs))
	for idx, providerID := range providerIDs {
		members = append(members, redis.Z{
			Score:  float64(idx),
			Member: strconv.FormatInt(providerID, 10),
		})
	}
	return members
}

// writeSnapshotMembers queues ordinal members in positive-size ZADD chunks
// for an unpublished bucket version. Empty input returns successfully with no
// key; write errors can leave partial unpublished membership data.
func (c *SnapshotCache) writeSnapshotMembers(ctx context.Context, bucket scheduler.SchedulerBucket, version string, members []redis.Z) error {
	if len(members) == 0 {
		return nil
	}
	snapshotKey := schedulerSnapshotKey(bucket, version)
	pipe := c.rdb.Pipeline()
	// Submit membership chunks before activation so the active marker can refer
	// to the version after queued ZADD work succeeds, with partial writes
	// possible on failure.
	for start := 0; start < len(members); start += c.writeChunkSize {
		end := min(start+c.writeChunkSize, len(members))
		pipe.ZAdd(ctx, snapshotKey, members[start:end]...)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// activateSnapshotVersion asks Redis to activate this version after epoch and
// retirement checks. Retired/fenced responses return typed errors; an older
// version is deleted and accepted as a non-error ignored write.
func (c *SnapshotCache) activateSnapshotVersion(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, version string) error {
	snapshotKey := schedulerSnapshotKey(bucket, version)
	// Recheck generation and retirement in the activation script so a completed
	// membership write from an obsolete writer cannot become this bucket's
	// active version.
	activeKey := schedulerBucketKey(schedulerActivePrefix, bucket)
	readyKey := schedulerBucketKey(schedulerReadyPrefix, bucket)
	snapshotKeyPrefix := fmt.Sprintf("%s%d:%s:%s:v", schedulerSnapshotPrefix, bucket.GroupID, bucket.Platform, bucket.Mode)

	keys := []string{
		activeKey,
		readyKey,
		schedulerBucketSetKey,
		snapshotKey,
		schedulerBucketKey(schedulerEpochPrefix, bucket),
		schedulerBucketKey(schedulerRetiredPrefix, bucket),
	}
	args := []any{version, bucket.String(), snapshotKeyPrefix, snapshotGraceTTLSeconds, token.Epoch}

	result, err := activateSnapshotScript.Run(ctx, c.rdb, keys, args...).Int64()
	if err != nil {
		return err
	}
	return schedulerBucketWriteResultError(result, bucket)
}

// schedulerBucketWriteResultError maps retired and mismatched-epoch Lua
// sentinels to their typed bucket errors. Other results return nil, including
// zero from an older activation that Redis ignored.
func schedulerBucketWriteResultError(result int64, bucket scheduler.SchedulerBucket) error {
	switch result {
	case -1:
		return fmt.Errorf("%w: bucket=%s", scheduler.ErrSchedulerBucketRetired, bucket.String())
	case -2:
		return fmt.Errorf("%w: bucket=%s", scheduler.ErrSchedulerBucketWriteFenced, bucket.String())
	default:
		return nil
	}
}

// GetProvider reads the full payload and last-used side value, then applies
// the newer scheduling time. Missing payloads or an unexpected value count
// return nil; decoding and side-value failures propagate.
func (c *SnapshotCache) GetProvider(ctx context.Context, providerID int64) (scheduler.SnapshotProvider, error) {
	id := strconv.FormatInt(providerID, 10)
	values, err := c.rdb.MGet(ctx, schedulerProviderKey(id), schedulerLastUsedKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(values) != 2 || values[0] == nil {
		return nil, nil
	}
	provider, err := c.codec.Decode(values[0])
	if err != nil {
		return nil, err
	}
	if err := c.applySchedulerLastUsed(provider, values[1]); err != nil {
		return nil, err
	}
	return provider, nil
}

// SetProvider writes full and metadata payloads for a positive-ID snapshot,
// ignoring nil or nonpositive identities. A sole encoding failure is logged
// and can return nil after deleting prior full/metadata/side values; Redis
// failures propagate.
func (c *SnapshotCache) SetProvider(ctx context.Context, provider scheduler.SnapshotProvider) error {
	if provider == nil || provider.SnapshotMetadata().ID <= 0 {
		return nil
	}
	providerIDs, err := c.writeProviderIDs(ctx, []scheduler.SnapshotProvider{provider})
	if err != nil {
		return err
	}
	// Encoding failed for the sole supplied provider; remove its stored
	// representations so subsequent lookup observes absence after this
	// failed-encoding update path.
	if len(providerIDs) == 0 {
		return c.DeleteProvider(ctx, provider.SnapshotMetadata().ID)
	}
	return nil
}

// DeleteProvider removes the full payload, metadata and last-used side value
// for a positive provider ID. Nonpositive IDs are ignored, and bucket
// membership remains to produce a miss until rebuilt.
func (c *SnapshotCache) DeleteProvider(ctx context.Context, providerID int64) error {
	if providerID <= 0 {
		return nil
	}
	id := strconv.FormatInt(providerID, 10)
	return c.rdb.Del(ctx, schedulerProviderKey(id), schedulerProviderMetaKey(id), schedulerLastUsedKey(id)).Err()
}

// UpdateLastUsed records recent-use observations in side keys for positive
// provider IDs. Encodable updates leave full and metadata JSON untouched
// while Redis compares the candidate with the side value only when a full key
// exists.
//
// Updates form chunks of paired provider/side keys with one argument per pair
// so Lua associates every candidate with its intended provider. An
// unencodable time queues deletion of that provider's full, metadata and side
// values while other updates continue. Separate script chunks and deletion
// commands can complete before a pipeline error; supplied map order does not
// establish cross-provider update order.
func (c *SnapshotCache) UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	if len(updates) == 0 {
		return nil
	}

	pipe := c.rdb.Pipeline()
	queued := 0
	keys := make([]string, 0, schedulerLastUsedUpdateChunkSize*2)
	args := make([]any, 0, schedulerLastUsedUpdateChunkSize)
	queueBatch := func() {
		if len(args) == 0 {
			return
		}
		// Keep one timestamp argument paired with its full/side key positions
		// inside the script chunk so unordered map iteration cannot mix provider
		// observations.
		updateSchedulerLastUsedScript.Eval(ctx, pipe, keys, args...)
		queued++
		// Start empty key/argument slices for the next chunk so each timestamp
		// again maps to its own pair of chunk-local key positions.
		keys = make([]string, 0, schedulerLastUsedUpdateChunkSize*2)
		args = make([]any, 0, schedulerLastUsedUpdateChunkSize)
	}
	for id, usedAt := range updates {
		if id <= 0 {
			continue
		}
		millis, err := schedulerLastUsedMillis(usedAt)
		// Queue removal of all three stored representations after an unencodable
		// time update, making provider lookup miss while other valid time
		// observations continue.
		if err != nil {
			slog.Warn("scheduler cache removes provider with unencodable payload",
				"provider_id", id,
				"error", err,
			)
			idText := strconv.FormatInt(id, 10)
			pipe.Del(ctx, schedulerProviderKey(idText), schedulerProviderMetaKey(idText), schedulerLastUsedKey(idText))
			queued++
			continue
		}
		idText := strconv.FormatInt(id, 10)
		keys = append(keys, schedulerProviderKey(idText), schedulerLastUsedKey(idText))
		args = append(args, millis)
		if len(args) >= schedulerLastUsedUpdateChunkSize {
			queueBatch()
		}
	}
	queueBatch()
	if queued == 0 {
		return nil
	}
	_, err := pipe.Exec(ctx)
	return err
}

// ListBuckets returns bucket identities parsed from the shared registration
// set. Malformed members are skipped and Redis set order is retained as
// returned; this method supplies no sorted ordering.
func (c *SnapshotCache) ListBuckets(ctx context.Context) ([]scheduler.SchedulerBucket, error) {
	raw, err := c.rdb.SMembers(ctx, schedulerBucketSetKey).Result()
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.SchedulerBucket, 0, len(raw))
	for _, entry := range raw {
		bucket, ok := scheduler.ParseSchedulerBucket(entry)
		// Skip malformed registration members while retaining usable bucket
		// identities, so one invalid set entry does not discard maintenance work
		// for the remaining buckets.
		if !ok {
			continue
		}
		out = append(out, bucket)
	}
	return out, nil
}

// GetOutboxWatermark reads the shared polling cursor, returning zero when its
// key is absent. Malformed integer values and Redis failures return errors so
// polling can avoid consuming from an unconfirmed cursor.
func (c *SnapshotCache) GetOutboxWatermark(ctx context.Context) (int64, error) {
	val, err := c.rdb.Get(ctx, schedulerOutboxWatermarkKey).Result()
	// An absent shared cursor starts polling from zero; distinguish that initial
	// state from malformed data or unavailable Redis before selecting persistent
	// events.
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// SetOutboxWatermark replaces the shared polling cursor with a persistent
// decimal ID. The caller orders completed event batches; this write does not
// compare the new ID with the stored value.
func (c *SnapshotCache) SetOutboxWatermark(ctx context.Context, id int64) error {
	return c.rdb.Set(ctx, schedulerOutboxWatermarkKey, strconv.FormatInt(id, 10), 0).Err()
}

// schedulerBucketKey joins group, platform and mode with the supplied
// bucket-state prefix.
func schedulerBucketKey(prefix string, bucket scheduler.SchedulerBucket) string {
	return fmt.Sprintf("%s%d:%s:%s", prefix, bucket.GroupID, bucket.Platform, bucket.Mode)
}

// schedulerGroupLifecycleLockKey returns the owner-lock key for one group's
// lifecycle operations.
func schedulerGroupLifecycleLockKey(groupID int64) string {
	return schedulerGroupLifecycleLockPrefix + strconv.FormatInt(groupID, 10)
}

// schedulerSnapshotKey joins bucket identity and version into its ordered
// membership key.
func schedulerSnapshotKey(bucket scheduler.SchedulerBucket, version string) string {
	return fmt.Sprintf("%s%d:%s:%s:v%s", schedulerSnapshotPrefix, bucket.GroupID, bucket.Platform, bucket.Mode, version)
}

// schedulerProviderKey returns the full provider payload key for a decimal
// identity.
func schedulerProviderKey(id string) string {
	return schedulerProviderPrefix + id
}

// schedulerProviderMetaKey returns the selection metadata key for a decimal
// provider identity.
func schedulerProviderMetaKey(id string) string {
	return schedulerProviderMetaPrefix + id
}

// schedulerLastUsedKey returns the monotonic side-time key for a decimal
// provider identity.
func schedulerLastUsedKey(id string) string {
	return schedulerProviderLastUsedPrefix + id
}

// ptrTime returns a pointer to the supplied scheduling timestamp value.
func ptrTime(t time.Time) *time.Time {
	return &t
}

// schedulerLastUsedMillis validates JSON encodability before returning the
// time's UTC Unix milliseconds. Millisecond conversion truncates
// submillisecond precision; invalid time representations return errors for
// the caller's cache-removal policy.
func schedulerLastUsedMillis(value time.Time) (int64, error) {
	if _, err := value.MarshalJSON(); err != nil {
		return 0, err
	}
	// Use UTC milliseconds to match the side key's integer representation,
	// truncating finer timestamp precision.
	return value.UTC().UnixMilli(), nil
}

// applySchedulerLastUsed installs the side-key time when it is later than the
// decoded provider's embedded last-used time. Nil providers or side values
// are ignored, while unsupported types and malformed integers return errors.
//
// Redis strings and byte slices become UTC millisecond timestamps, then the
// codec reads the embedded value for comparison. An absent embedded time
// accepts the side observation; an older or equal side time leaves the
// provider unchanged. This comparison keeps a delayed side observation from
// rolling back a more recent embedded value, including finer submillisecond
// precision already stored there.
func (c *SnapshotCache) applySchedulerLastUsed(provider scheduler.SnapshotProvider, value any) error {
	if provider == nil || value == nil {
		return nil
	}
	var raw string
	switch typed := value.(type) {
	case string:
		raw = typed
	case []byte:
		raw = string(typed)
	default:
		return fmt.Errorf("unexpected last_used cache type: %T", value)
	}
	millis, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid last_used cache value %q: %w", raw, err)
	}
	lastUsedAt := time.UnixMilli(millis).UTC()
	current, err := c.codec.LastUsedAt(provider)
	if err != nil {
		return err
	}
	// Replace the decoded timestamp only when the side observation advances it,
	// so delayed millisecond updates cannot overwrite a later embedded
	// scheduling time.
	if current == nil || lastUsedAt.After(*current) {
		return c.codec.SetLastUsedAt(provider, ptrTime(lastUsedAt))
	}
	return nil
}

// writeProviderIDs writes full and metadata payloads for successfully encoded
// snapshots and returns their IDs in input order. Callers supply entries with
// readable metadata during error logging; encoding failures are logged and
// omitted from this batch.
//
// Each successfully encoded provider queues two persistent shared-key writes,
// with pipelines flushed by provider count. Earlier chunks or commands can
// remain after a later Redis failure, and the returned list retains duplicate
// IDs for ordinal membership construction. Last-used side keys stay untouched
// so stale rebuild payloads cannot replace newer side observations. These
// shared writes have no bucket epoch check; bucket allocation/activation
// protects membership separately.
func (c *SnapshotCache) writeProviderIDs(ctx context.Context, providers []scheduler.SnapshotProvider) ([]int64, error) {
	if len(providers) == 0 {
		return nil, nil
	}

	pipe := c.rdb.Pipeline()
	providerIDs := make([]int64, 0, len(providers))
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		// Finish each queued provider chunk before starting another; an error stops
		// ID publication while writes already completed in earlier chunks remain
		// shared.
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		pipe = c.rdb.Pipeline()
		pending = 0
		return nil
	}

	for _, provider := range providers {
		fullPayload, metaPayload, err := c.codec.Encode(provider)
		// Skip an unencodable provider while retaining other encodable entries for
		// this membership batch; prior shared values remain unless the
		// single-provider caller deletes them.
		if err != nil {
			slog.Warn("scheduler cache skips provider with unencodable payload",
				"provider_id", provider.SnapshotMetadata().ID,
				"error", err,
			)
			continue
		}

		id := strconv.FormatInt(provider.SnapshotMetadata().ID, 10)
		pipe.Set(ctx, schedulerProviderKey(id), fullPayload, 0)
		pipe.Set(ctx, schedulerProviderMetaKey(id), metaPayload, 0)
		// Leave the separate side timestamp intact during shared JSON replacement
		// so a delayed rebuild's embedded time cannot replace the newer hot
		// scheduling observation.
		providerIDs = append(providerIDs, provider.SnapshotMetadata().ID)
		pending++
		if pending >= c.writeChunkSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}

	if err := flush(); err != nil {
		return nil, err
	}
	return providerIDs, nil
}

// mgetChunked returns Redis values aligned with the supplied key order,
// including nil values for missing keys. Empty input returns an empty slice,
// and a nonpositive configured size selects the read default.
//
// Each chunk result appends in place within the overall key sequence,
// allowing metadata and side-time lists to retain the same positions across
// independent reads. Dropping absent values would associate later timestamps
// with the wrong providers. A Redis error aborts the aggregate result after
// any earlier reads, and different chunks can observe concurrent key changes
// rather than one transaction-wide state.
func (c *SnapshotCache) mgetChunked(ctx context.Context, keys []string) ([]any, error) {
	if len(keys) == 0 {
		return []any{}, nil
	}

	out := make([]any, 0, len(keys))
	chunkSize := c.mgetChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultSchedulerSnapshotMGetChunkSize
	}
	// Append each chunk in key order with missing positions retained, keeping
	// separate metadata and last-used batches aligned with the bucket's provider
	// sequence.
	for start := 0; start < len(keys); start += chunkSize {
		end := min(start+chunkSize, len(keys))
		part, err := c.rdb.MGet(ctx, keys[start:end]...).Result()
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

// AcquireBucketLease delegates token-owned bucket locking to BucketLocks
// using the caller's TTL. Use a positive lifetime for an expiring lease;
// zero creates a persistent lock under go-redis SETNX rules. A successful
// caller releases the returned lease after rebuilding, and contention
// returns no lease for this attempt.
func (c *SnapshotCache) AcquireBucketLease(ctx context.Context, bucket scheduler.SchedulerBucket, ttl time.Duration) (*scheduler.BucketLease, bool, error) {
	return NewBucketLocks(c.rdb).AcquireBucketLease(ctx, bucket, ttl)
}
