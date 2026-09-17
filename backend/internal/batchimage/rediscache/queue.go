package rediscache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/redis/go-redis/v9"
)

const (
	defaultBatchImageReadyKey       = "batch_image:queue:ready"
	defaultBatchImageDelayedKey     = "batch_image:queue:delayed"
	defaultBatchImageActiveKey      = "batch_image:queue:active"
	defaultBatchImageInflightPrefix = "batch_image:queue:inflight:"
	defaultBatchImageLockPrefix     = "batch_image:queue:lock:"
	defaultBatchImageInflightTTL    = 7 * 24 * time.Hour
	defaultBatchImageJobLockTTL     = 5 * time.Minute

	// batchImageReservePollInterval 是原子 Reserve 脚本空轮询的间隔。
	// 用轮询替代 BRPop 是为了保证 "弹出 + 写 active" 的原子性。
	batchImageReservePollInterval = time.Second
)

var batchImageMoveDueDelayedScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
end
return #jobs
`)

var batchImageRecoverStaleActiveScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
end
return #jobs
`)

var batchImageReleaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

var batchImageRefreshLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

// batchImageReserveScript 原子地从 ready 弹出并写入 active zset。
// BRPop + ZAdd 两步方案在两步之间进程崩溃时 job 会脱离所有队列结构，
// 且 inflight 去重键（默认 7 天）会挡住所有重新入队。
var batchImageReserveScript = redis.NewScript(`
local job = redis.call("RPOP", KEYS[1])
if not job then
  return nil
end
redis.call("ZADD", KEYS[2], ARGV[1], job)
return job
`)

// batchImageEnqueueScript 原子地设置 inflight 去重键并推入 ready。
// SetNX + LPush 两步方案在两步之间进程崩溃时，inflight 键（默认 7 天）
// 会挡住所有后续入队，而 job 从未进入 ready。
var batchImageEnqueueScript = redis.NewScript(`
if redis.call("SET", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
  redis.call("LPUSH", KEYS[2], ARGV[1])
  return 1
end
return 0
`)

type batchImageQueue struct {
	rdb            *redis.Client
	readyKey       string
	delayedKey     string
	activeKey      string
	inflightPrefix string
	lockPrefix     string
	inflightTTL    time.Duration
	lockTTL        time.Duration
}

func NewBatchImageQueue(rdb *redis.Client, cfg *QueueOptions) service.BatchImageQueue {
	opts := QueueOptions{}
	if cfg != nil {
		opts = *cfg
	}
	return newBatchImageQueueWithOptions(rdb, opts)
}

type QueueOptions struct {
	ReadyKey       string
	DelayedKey     string
	ActiveKey      string
	InflightPrefix string
	LockPrefix     string
	InflightTTL    time.Duration
	LockTTL        time.Duration
}

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

func (q *batchImageQueue) Enqueue(ctx context.Context, batchID string) error {
	if !service.IsValidBatchImageID(batchID) {
		return service.ErrInvalidBatchImageQueuePayload
	}

	applied, err := batchImageEnqueueScript.Run(ctx, q.rdb,
		[]string{q.inflightKey(batchID), q.readyKey},
		batchID, q.inflightTTL.Milliseconds(),
	).Int()
	if err != nil {
		return err
	}
	if applied == 0 {
		return service.ErrBatchImageAlreadyQueued
	}
	return nil
}

func (q *batchImageQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (service.ReservedBatchImageJob, error) {
	deadline := time.Now().Add(blockTimeout)
	for {
		batchID, err := q.reserveOnce(ctx)
		if err == nil {
			return service.ReservedBatchImageJob{BatchID: batchID}, nil
		}
		if !errors.Is(err, service.ErrBatchImageQueueEmpty) {
			return service.ReservedBatchImageJob{}, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return service.ReservedBatchImageJob{}, service.ErrBatchImageQueueEmpty
		}
		wait := batchImageReservePollInterval
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return service.ReservedBatchImageJob{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (q *batchImageQueue) reserveOnce(ctx context.Context) (string, error) {
	raw, err := batchImageReserveScript.Run(ctx, q.rdb, []string{q.readyKey, q.activeKey}, time.Now().UnixMilli()).Result()
	if errors.Is(err, redis.Nil) {
		return "", service.ErrBatchImageQueueEmpty
	}
	if err != nil {
		return "", err
	}
	batchID, ok := raw.(string)
	if !ok || !service.IsValidBatchImageID(batchID) {
		// 非法 payload 已被脚本写入 active，必须移除，否则 stale 恢复会把它
		// 无限重投回 ready。
		if ok && batchID != "" {
			_ = q.rdb.ZRem(ctx, q.activeKey, batchID).Err()
		}
		return "", service.ErrInvalidBatchImageQueuePayload
	}
	return batchID, nil
}

// 非持有者入口仅用于没有有效锁的恢复，不能修改当前持有者的队列记录。
func (q *batchImageQueue) RequeueAfter(ctx context.Context, id string, delay time.Duration) error {
	return q.mutateOwned(ctx, id, "", "requeue", delay)
}
func (q *batchImageQueue) Ack(ctx context.Context, id string) error {
	return q.mutateOwned(ctx, id, "", "ack", 0)
}
func (q *batchImageQueue) Heartbeat(ctx context.Context, id string) error {
	return q.mutateOwned(ctx, id, "", "heartbeat", 0)
}

// 队列数据格式保持不变；锁 token 比较与队列修改处于同一 Lua 调用。
var batchImageOwnedMutation = redis.NewScript(`
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

func (q *batchImageQueue) mutateOwned(ctx context.Context, id, token, action string, delay time.Duration) error {
	if !service.IsValidBatchImageID(id) {
		return service.ErrInvalidBatchImageQueuePayload
	}
	now := time.Now()
	n, err := batchImageOwnedMutation.Run(ctx, q.rdb, []string{q.lockKey(id), q.activeKey, q.delayedKey, q.readyKey, q.inflightKey(id)}, id, token, action, now.UnixMilli(), delay.Milliseconds(), now.Add(delay).UnixMilli()).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrBatchImageLeaseLost
	}
	return nil
}

func (q *batchImageQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	return batchImageMoveDueDelayedScript.Run(ctx, q.rdb, []string{q.delayedKey, q.readyKey}, time.Now().UnixMilli(), limit).Int()
}

func (q *batchImageQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	if staleAfter <= 0 {
		return 0, service.ErrInvalidBatchImageQueuePayload
	}
	if limit <= 0 {
		limit = 100
	}
	cutoff := time.Now().Add(-staleAfter).UnixMilli()
	return batchImageRecoverStaleActiveScript.Run(ctx, q.rdb, []string{q.activeKey, q.readyKey}, cutoff, limit).Int()
}

func (q *batchImageQueue) TryAcquireJobLock(ctx context.Context, batchID string, ttl time.Duration) (service.BatchImageJobLock, bool, error) {
	if !service.IsValidBatchImageID(batchID) {
		return nil, false, service.ErrInvalidBatchImageQueuePayload
	}
	if ttl <= 0 {
		ttl = q.lockTTL
	}
	token, err := newBatchImageLockToken()
	if err != nil {
		return nil, false, err
	}
	key := q.lockKey(batchID)
	ok, err := q.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return &batchImageRedisJobLock{rdb: q.rdb, key: key, token: token, queue: q, id: batchID}, true, nil
}

func (q *batchImageQueue) inflightKey(batchID string) string {
	return q.inflightPrefix + batchID
}

func (q *batchImageQueue) lockKey(batchID string) string {
	return q.lockPrefix + batchID
}

type batchImageRedisJobLock struct {
	rdb   *redis.Client
	key   string
	token string
	queue *batchImageQueue
	id    string
}

func (l *batchImageRedisJobLock) Release(ctx context.Context) error {
	if l == nil || l.rdb == nil || l.key == "" || l.token == "" {
		return nil
	}
	return batchImageReleaseLockScript.Run(ctx, l.rdb, []string{l.key}, l.token).Err()
}

// Refresh 在仍持有锁（token 匹配）时续期 TTL，供长处理任务的心跳调用。
func (l *batchImageRedisJobLock) Refresh(ctx context.Context, ttl time.Duration) error {
	if l == nil || l.rdb == nil || l.key == "" || l.token == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = defaultBatchImageJobLockTTL
	}
	n, err := batchImageRefreshLockScript.Run(ctx, l.rdb, []string{l.key}, l.token, ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrBatchImageLeaseLost
	}
	return nil
}

var _ service.BatchImageJobLockRefresher = (*batchImageRedisJobLock)(nil)

func newBatchImageLockToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

var _ service.BatchImageQueue = (*batchImageQueue)(nil)

func (l *batchImageRedisJobLock) Heartbeat(ctx context.Context) error {
	return l.queue.mutateOwned(ctx, l.id, l.token, "heartbeat", 0)
}
func (l *batchImageRedisJobLock) Ack(ctx context.Context) error {
	return l.queue.mutateOwned(ctx, l.id, l.token, "ack", 0)
}
func (l *batchImageRedisJobLock) RequeueAfter(ctx context.Context, delay time.Duration) error {
	return l.queue.mutateOwned(ctx, l.id, l.token, "requeue", delay)
}
