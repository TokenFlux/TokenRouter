package rediscache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/infra/redis/taskqueue"
)

// queue.go coordinates batch-image queue admission, reservation, retries, and
// lease ownership in Redis.
// Queued image jobs carry one batch ID across ready, delayed, and active
// records. Shared taskqueue primitives manage atomic inflight-plus-ready
// admission, polling waits, delayed promotion, stale-active recovery, and
// token-owned job locks, while reservation removes a ready entry together with
// recording its active timestamp.
//
// Execution ownership is a separate expiring token. Holder operations compare
// that token inside the same script that updates queue records. Queue-level
// heartbeat, acknowledgement, and requeue require an absent lock. Stale recovery
// moves old active entries back to ready, where workers still acquire the job
// lock before processing.

// Queue defaults name ready, delayed, active, inflight, and lock records and
// set marker and lease expiry budgets. Reservation polling bounds idle script
// calls while letting cancellation interrupt waiting workers.
const (
	defaultBatchImageReadyKey       = "batch_image:queue:ready"
	defaultBatchImageDelayedKey     = "batch_image:queue:delayed"
	defaultBatchImageActiveKey      = "batch_image:queue:active"
	defaultBatchImageInflightPrefix = "batch_image:queue:inflight:"
	defaultBatchImageLockPrefix     = "batch_image:queue:lock:"
	defaultBatchImageInflightTTL    = 7 * 24 * time.Hour
	defaultBatchImageJobLockTTL     = 5 * time.Minute

	// batchImageReservePollInterval bounds pauses between empty reservation
	// attempts.
	batchImageReservePollInterval = time.Second
)

var (
	// batchImageReserveScript moves the oldest ready ID into active with its
	// reservation timestamp. Combining both writes leaves a recovery record when
	// the reserving client stops after Redis has applied the script.
	batchImageReserveScript = redis.NewScript(`
local job = redis.call("RPOP", KEYS[1])
if not job then
  return nil
end
redis.call("ZADD", KEYS[2], ARGV[1], job)
return job
`)

	// batchImageOwnedMutation compares execution ownership before applying
	// heartbeat, acknowledgement, or requeue changes. An empty supplied token requires
	// an absent lock; a holder token requires an exact match, so stale workers cannot
	// erase or refresh queue records belonging to a successor.
	batchImageOwnedMutation = redis.NewScript(`
local owner=redis.call("GET",KEYS[1])
if ARGV[2]=="" then
 if owner then return 0 end
elseif owner~=ARGV[2] then return 0 end
if ARGV[3]=="heartbeat" then
 redis.call("ZADD",KEYS[2],"XX",ARGV[4],ARGV[1])
elseif ARGV[3]=="ack" then
 redis.call("ZREM",KEYS[2],ARGV[1]);redis.call("ZREM",KEYS[3],ARGV[1]);redis.call("DEL",KEYS[5])
else
 redis.call("ZREM",KEYS[2],ARGV[1]);redis.call("ZREM",KEYS[3],ARGV[1])
 if tonumber(ARGV[5])<=0 then redis.call("LPUSH",KEYS[4],ARGV[1]) else redis.call("ZADD",KEYS[3],ARGV[6],ARGV[1]) end
end
return 1
`)

	// These assertions keep the Redis holder's renewal and queue operations
	// compatible with the batch-image interfaces consumed by workers.
	_ batchimage.BatchImageJobLockRefresher = (*batchImageRedisJobLock)(nil)

	_ batchimage.BatchImageQueue = (*batchImageQueue)(nil)
)

// batchImageQueue coordinates Redis admission, reservation, recovery, and
// expiring execution ownership for batch image jobs.
type batchImageQueue struct {
	// rdb remains owned by the constructor's caller. Keep the client available
	// through queue operations and use of every lock handle returned from it.
	rdb *redis.Client // borrowed Redis connection client
	// readyKey, delayedKey, activeKey, inflightPrefix, and lockPrefix identify
	// different records for the same batch ID. Queue scripts receive those
	// locations together when transferring or acknowledging work.
	readyKey       string // ready job list key
	delayedKey     string // scheduled job set key
	activeKey      string // reserved job timestamp key
	inflightPrefix string // admission marker key prefix
	lockPrefix     string // execution token key prefix
	// inflightTTL controls duplicate admission lifetime, while lockTTL supplies
	// the default execution lease. Their independent expiries let admission
	// survive a worker lease and expire abandoned markers eventually.
	inflightTTL time.Duration // admission marker expiry duration
	lockTTL     time.Duration // default execution lease duration
}

// QueueOptions names Redis queue records and supplies inflight and execution
// lock expiry budgets.
type QueueOptions struct {
	// ReadyKey, DelayedKey, ActiveKey, InflightPrefix, and LockPrefix identify
	// distinct records shared by all participating workers. ReadyKey is a list,
	// DelayedKey and ActiveKey are sorted sets, and inflight and lock keys hold
	// strings. Empty values select the standard batch-image keys and prefixes.
	ReadyKey       string // ready job list key
	DelayedKey     string // scheduled job set key
	ActiveKey      string // reserved job timestamp key
	InflightPrefix string // admission marker key prefix
	LockPrefix     string // execution token key prefix
	// InflightTTL controls duplicate admission lifetime, while LockTTL supplies
	// the default execution lease. Each nonpositive duration selects its own
	// normalized default during construction, and their expiries remain independent.
	//
	// InflightTTL is passed to Lua as whole milliseconds for the admission
	// marker. Callers supply at least one millisecond for a positive override so
	// Redis accepts the marker's expiry.
	InflightTTL time.Duration // admission marker expiry duration
	LockTTL     time.Duration // default execution lease duration
}

// batchImageRedisJobLock carries one acquisition token used to validate
// subsequent operations on its batch job.
type batchImageRedisJobLock struct {
	// rdb is the acquiring queue's borrowed client. Release and Refresh use it
	// directly, so its owner closes it after outstanding handles finish.
	rdb *redis.Client // borrowed Redis connection client
	// key and token identify this particular lock acquisition. Release, Refresh,
	// and holder queue mutations compare token with Redis so an expired holder
	// cannot authorize work under a successor's value.
	key   string // acquired Redis lock key
	token string // random acquisition identity token
	// queue and id select the queue records changed by holder operations.
	// TryAcquireJobLock initializes both from the acquiring queue and requested
	// batch ID; those operations retain the acquisition token.
	queue *batchImageQueue // acquiring queue record adapter
	id    string           // locked batch job identifier
}

// NewBatchImageQueue constructs a queue over the supplied Redis client.
// A nil configuration selects normalized defaults; the client remains borrowed,
// and its owner controls connection shutdown after queue users finish.
func NewBatchImageQueue(rdb *redis.Client, cfg *QueueOptions) batchimage.BatchImageQueue {
	opts := QueueOptions{}
	if cfg != nil {
		opts = *cfg
	}
	return newBatchImageQueueWithOptions(rdb, opts)
}

// newBatchImageQueueWithOptions retains the Redis client and normalized key
// and expiry options. Callers configure a usable client before queue operations
// and keep it available for every returned job-lock handle.
func newBatchImageQueueWithOptions(rdb *redis.Client, opts QueueOptions) *batchImageQueue {
	opts = normalizeBatchImageQueueOptions(opts)
	return &batchImageQueue{
		rdb:            rdb,
		readyKey:       opts.ReadyKey,
		delayedKey:     opts.DelayedKey,
		activeKey:      opts.ActiveKey,
		inflightPrefix: opts.InflightPrefix,
		lockPrefix:     opts.LockPrefix,
		inflightTTL:    opts.InflightTTL,
		lockTTL:        opts.LockTTL,
	}
}

// normalizeBatchImageQueueOptions returns options with empty keys and prefixes
// filled from queue defaults. Nonpositive expiry budgets select seven days for
// inflight markers and five minutes for execution locks.
func normalizeBatchImageQueueOptions(opts QueueOptions) QueueOptions {
	if opts.ReadyKey == "" {
		opts.ReadyKey = defaultBatchImageReadyKey
	}
	if opts.DelayedKey == "" {
		opts.DelayedKey = defaultBatchImageDelayedKey
	}
	if opts.ActiveKey == "" {
		opts.ActiveKey = defaultBatchImageActiveKey
	}
	if opts.InflightPrefix == "" {
		opts.InflightPrefix = defaultBatchImageInflightPrefix
	}
	if opts.LockPrefix == "" {
		opts.LockPrefix = defaultBatchImageLockPrefix
	}
	if opts.InflightTTL <= 0 {
		opts.InflightTTL = defaultBatchImageInflightTTL
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = defaultBatchImageJobLockTTL
	}
	return opts
}

// Enqueue admits a valid batch ID by creating its inflight marker and ready
// entry together. An existing marker returns ErrBatchImageAlreadyQueued;
// invalid IDs are rejected before Redis is contacted.
func (q *batchImageQueue) Enqueue(ctx context.Context, batchID string) error {
	if !batchimage.IsValidBatchImageID(batchID) {
		return batchimage.ErrInvalidBatchImageQueuePayload
	}
	applied, err := taskqueue.Enqueue(ctx, q.rdb, q.inflightKey(batchID), q.readyKey, batchID, q.inflightTTL)
	if err != nil {
		return err
	}
	if !applied {
		return batchimage.ErrBatchImageAlreadyQueued
	}
	return nil
}

// Reserve polls for the oldest ready job and returns its reserved batch ID.
// It attempts reservation once even with a nonpositive blocking budget.
//
// Only an empty queue triggers another attempt. The timer waits for at most
// one poll interval or the remaining budget, whichever is shorter; cancellation
// during that wait returns the context error, while Redis and payload errors
// stop the attempt immediately.
func (q *batchImageQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (batchimage.ReservedBatchImageJob, error) {
	return taskqueue.PollReserve(ctx, blockTimeout, batchImageReservePollInterval, func(err error) bool {
		return errors.Is(err, batchimage.ErrBatchImageQueueEmpty)
	}, func(callCtx context.Context) (batchimage.ReservedBatchImageJob, error) {
		batchID, err := q.reserveOnce(callCtx)
		if err != nil {
			return batchimage.ReservedBatchImageJob{}, err
		}
		return batchimage.ReservedBatchImageJob{BatchID: batchID}, nil
	})
}

// reserveOnce moves one ready ID into active and returns that ID.
// An absent ready entry becomes ErrBatchImageQueueEmpty, and malformed results
// return ErrInvalidBatchImageQueuePayload after the script's queue mutation.
//
// Invalid nonempty string IDs receive a best-effort active removal so stale
// recovery does not repeatedly reintroduce them.
func (q *batchImageQueue) reserveOnce(ctx context.Context) (string, error) {
	raw, err := batchImageReserveScript.Run(ctx, q.rdb, []string{q.readyKey, q.activeKey}, time.Now().UnixMilli()).Result()
	if errors.Is(err, redis.Nil) {
		return "", batchimage.ErrBatchImageQueueEmpty
	}
	if err != nil {
		return "", err
	}
	batchID, ok := raw.(string)
	if !ok || !batchimage.IsValidBatchImageID(batchID) {
		// The script has already placed this payload in active. Removal is best
		// effort because a Redis error here must still report the invalid payload;
		// otherwise stale recovery can cycle it back into ready.
		if ok && batchID != "" {
			_ = q.rdb.ZRem(ctx, q.activeKey, batchID).Err()
		}
		return "", batchimage.ErrInvalidBatchImageQueuePayload
	}
	return batchID, nil
}

// RequeueAfter moves an unlocked job out of active and delayed records.
// Delays below one millisecond push it to ready; positive whole-millisecond
// delays schedule it. A current lock reports ErrBatchImageLeaseLost before
// those records change.
func (q *batchImageQueue) RequeueAfter(ctx context.Context, id string, delay time.Duration) error {
	return q.mutateOwned(ctx, id, "", "requeue", delay)
}

// Ack clears active, delayed, and inflight records for an unlocked job.
// A current execution lock returns ErrBatchImageLeaseLost; workers holding a
// lock use that holder's Ack method to authorize completion.
func (q *batchImageQueue) Ack(ctx context.Context, id string) error {
	return q.mutateOwned(ctx, id, "", "ack", 0)
}

// Heartbeat refreshes an existing active timestamp when the job has no lock.
// It leaves absent active membership absent and rejects a current lock;
// a processing worker calls its holder's Heartbeat method.
func (q *batchImageQueue) Heartbeat(ctx context.Context, id string) error {
	return q.mutateOwned(ctx, id, "", "heartbeat", 0)
}

// mutateOwned validates an ID and applies the requested queue mutation after
// checking its lock token. An empty token requires no current lock, while a
// nonempty token requires the stored value to match.
//
// Heartbeat updates existing active membership, acknowledgement removes queue
// and inflight records, and requeue uses its whole-millisecond delay to choose
// ready or delayed placement.
func (q *batchImageQueue) mutateOwned(ctx context.Context, id, token, action string, delay time.Duration) error {
	if !batchimage.IsValidBatchImageID(id) {
		return batchimage.ErrInvalidBatchImageQueuePayload
	}
	now := time.Now()
	// Authorize the holder and mutate queue records together so a token mismatch
	// prevents writes. The script compares whole-millisecond delay, making any
	// smaller positive duration an immediate ready requeue.
	n, err := batchImageOwnedMutation.Run(ctx, q.rdb, []string{q.lockKey(id), q.activeKey, q.delayedKey, q.readyKey, q.inflightKey(id)}, id, token, action, now.UnixMilli(), delay.Milliseconds(), now.Add(delay).UnixMilli()).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return batchimage.ErrBatchImageLeaseLost
	}
	return nil
}

// MoveDueDelayedToReady transfers delayed members whose scheduled time has
// arrived into ready and returns the script's reported moved count. Nonpositive
// limits select one hundred members; scheduling scores use Unix milliseconds.
func (q *batchImageQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	return taskqueue.MoveDueDelayedToReady(ctx, q.rdb, q.delayedKey, q.readyKey, time.Now().UnixMilli(), limit)
}

// RecoverStaleActive returns active members at or before the stale cutoff to
// ready. Nonpositive stale durations are invalid, and nonpositive limits select
// one hundred members; processing still requires acquiring each job's lock.
func (q *batchImageQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	if staleAfter <= 0 {
		return 0, batchimage.ErrInvalidBatchImageQueuePayload
	}
	cutoff := time.Now().Add(-staleAfter).UnixMilli()
	return taskqueue.RecoverStaleActive(ctx, q.rdb, q.activeKey, q.readyKey, cutoff, limit)
}

// TryAcquireJobLock attempts an expiring token lock for a valid batch ID.
// Nonpositive TTL selects the configured lock budget; contention returns a nil
// holder, false, and no error. Successful callers release or renew the returned
// holder while using its token-checked queue methods.
func (q *batchImageQueue) TryAcquireJobLock(ctx context.Context, batchID string, ttl time.Duration) (batchimage.BatchImageJobLock, bool, error) {
	if !batchimage.IsValidBatchImageID(batchID) {
		return nil, false, batchimage.ErrInvalidBatchImageQueuePayload
	}
	if ttl <= 0 {
		ttl = q.lockTTL
	}
	key := q.lockKey(batchID)
	token, ok, err := taskqueue.AcquireLock(ctx, q.rdb, key, ttl)
	if err != nil || !ok {
		return nil, false, err
	}
	return &batchImageRedisJobLock{rdb: q.rdb, key: key, token: token, queue: q, id: batchID}, true, nil
}

// inflightKey appends the batch ID to the configured deduplication prefix.
func (q *batchImageQueue) inflightKey(batchID string) string {
	return q.inflightPrefix + batchID
}

// lockKey appends the batch ID to the configured execution-lock prefix.
func (q *batchImageQueue) lockKey(batchID string) string {
	return q.lockPrefix + batchID
}

// Release deletes the Redis lock when its token still matches this holder.
// An incomplete holder is a no-op, and a missing or replacement token also
// succeeds without deleting another acquisition's lock.
func (l *batchImageRedisJobLock) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	return taskqueue.ReleaseLock(ctx, l.rdb, l.key, l.token)
}

// Refresh renews this holder's matching Redis token or returns
// ErrBatchImageLeaseLost when ownership is absent or changed. Incomplete holders
// are no-ops; nonpositive TTL uses the five-minute default. Positive TTL is
// converted to whole milliseconds, so callers supply at least one millisecond
// to retain a live lease.
func (l *batchImageRedisJobLock) Refresh(ctx context.Context, ttl time.Duration) error {
	if l == nil || l.rdb == nil || l.key == "" || l.token == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = defaultBatchImageJobLockTTL
	}
	refreshed, err := taskqueue.RefreshLock(ctx, l.rdb, l.key, l.token, ttl)
	if err != nil {
		return err
	}
	if !refreshed {
		return batchimage.ErrBatchImageLeaseLost
	}
	return nil
}

// Heartbeat refreshes this holder's existing active membership after confirming
// its token. Ownership loss returns ErrBatchImageLeaseLost, allowing the worker
// to stop processing before acting on another acquisition's records.
func (l *batchImageRedisJobLock) Heartbeat(ctx context.Context) error {
	return l.queue.mutateOwned(ctx, l.id, l.token, "heartbeat", 0)
}

// Ack removes active, delayed, and inflight records after confirming this
// holder's token. Ownership loss returns ErrBatchImageLeaseLost; the lock itself
// remains until release or expiry, including after successful acknowledgement.
func (l *batchImageRedisJobLock) Ack(ctx context.Context) error {
	return l.queue.mutateOwned(ctx, l.id, l.token, "ack", 0)
}

// RequeueAfter schedules this holder's job after confirming its token.
// Delays below one millisecond make it ready; positive whole-millisecond delays
// use the delayed set. Ownership loss returns ErrBatchImageLeaseLost before
// queue records change.
func (l *batchImageRedisJobLock) RequeueAfter(ctx context.Context, delay time.Duration) error {
	return l.queue.mutateOwned(ctx, l.id, l.token, "requeue", delay)
}
