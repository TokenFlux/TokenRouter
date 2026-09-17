package rediscache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/redis/go-redis/v9"
)

const (
	defaultCreativeReadyKey       = "creative:queue:ready"
	defaultCreativeDelayedKey     = "creative:queue:delayed"
	defaultCreativeActiveKey      = "creative:queue:active"
	defaultCreativeInflightPrefix = "creative:queue:inflight:"
	defaultCreativeLockPrefix     = "creative:queue:lock:"
	defaultCreativeInflightTTL    = 7 * 24 * time.Hour
	defaultCreativeJobLockTTL     = 5 * time.Minute

	// creativeReservePollInterval 是原子 Reserve 脚本空轮询的间隔。
	// 用轮询替代 BRPop 是为了保证 "弹出 + 写 active" 的原子性。
	creativeReservePollInterval = time.Second
)

var creativeMoveDueDelayedScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
end
return #jobs
`)

var creativeRecoverStaleActiveScript = redis.NewScript(`
local jobs = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, job in ipairs(jobs) do
  redis.call("ZREM", KEYS[1], job)
  redis.call("LPUSH", KEYS[2], job)
  redis.call("DEL", KEYS[3] .. job)
end
return #jobs
`)

var creativeAckScript = redis.NewScript(`
if redis.call("ZSCORE", KEYS[1], ARGV[1]) and redis.call("GET", KEYS[3] .. ARGV[1]) == ARGV[2] then
  redis.call("ZREM", KEYS[1], ARGV[1])
  redis.call("ZREM", KEYS[2], ARGV[1])
  redis.call("DEL", KEYS[3] .. ARGV[1])
  return 1
end
return 0
`)

var creativeRequeueScript = redis.NewScript(`
if redis.call("ZSCORE", KEYS[1], ARGV[1]) and redis.call("GET", KEYS[3] .. ARGV[1]) == ARGV[2] then
  redis.call("ZREM", KEYS[1], ARGV[1])
  redis.call("ZREM", KEYS[2], ARGV[1])
  if tonumber(ARGV[4]) <= 0 then
    redis.call("LPUSH", KEYS[4], ARGV[1])
  else
    redis.call("ZADD", KEYS[2], ARGV[3], ARGV[1])
  end
  return 1
end
return 0
`)

var creativeHeartbeatScript = redis.NewScript(`
if redis.call("ZSCORE", KEYS[1], ARGV[1]) and redis.call("GET", KEYS[2] .. ARGV[1]) == ARGV[2] then
  redis.call("ZADD", KEYS[1], ARGV[3], ARGV[1])
  return 1
end
return 0
`)

var creativeReleaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

var creativeRefreshLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

// creativeReserveScript 原子地从 ready 弹出并写入 active zset。
// BRPop + ZAdd 两步方案在两步之间进程崩溃时 job 会脱离所有队列结构，
// 且 inflight 去重键（默认 7 天）会挡住所有重新入队。
var creativeReserveScript = redis.NewScript(`
local job = redis.call("RPOP", KEYS[1])
if not job then
  return nil
end
redis.call("ZADD", KEYS[2], ARGV[1], job)
redis.call("SET", KEYS[3] .. job, ARGV[2], "PX", ARGV[3])
return job
`)

// creativeEnqueueScript 原子地设置 inflight 去重键并推入 ready。
// SetNX + LPush 两步方案在两步之间进程崩溃时，inflight 键（默认 7 天）
// 会挡住所有后续入队，而 job 从未进入 ready。
var creativeEnqueueScript = redis.NewScript(`
if redis.call("SET", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
  redis.call("LPUSH", KEYS[2], ARGV[1])
  return 1
end
return 0
`)

type creativeQueue struct {
	rdb            *redis.Client
	readyKey       string
	delayedKey     string
	activeKey      string
	inflightPrefix string
	lockPrefix     string
	inflightTTL    time.Duration
	lockTTL        time.Duration
}

// NewCreativeQueue 创建创作台 Redis 队列，键前缀全部来自 cfg.Creative。
func NewCreativeQueue(rdb *redis.Client, cfg *QueueOptions) service.CreativeRunQueue {
	queue := &creativeQueue{
		rdb:            rdb,
		readyKey:       defaultCreativeReadyKey,
		delayedKey:     defaultCreativeDelayedKey,
		activeKey:      defaultCreativeActiveKey,
		inflightPrefix: defaultCreativeInflightPrefix,
		lockPrefix:     defaultCreativeLockPrefix,
		inflightTTL:    defaultCreativeInflightTTL,
		lockTTL:        defaultCreativeJobLockTTL,
	}
	if cfg != nil {
		if cfg.QueueReadyKey != "" {
			queue.readyKey = cfg.QueueReadyKey
		}
		if cfg.QueueDelayedKey != "" {
			queue.delayedKey = cfg.QueueDelayedKey
		}
		if cfg.QueueActiveKey != "" {
			queue.activeKey = cfg.QueueActiveKey
		}
		if cfg.InflightKeyPrefix != "" {
			queue.inflightPrefix = cfg.InflightKeyPrefix
		}
		if cfg.LockKeyPrefix != "" {
			queue.lockPrefix = cfg.LockKeyPrefix
		}
		if cfg.InflightTTLSeconds > 0 {
			queue.inflightTTL = time.Duration(cfg.InflightTTLSeconds) * time.Second
		}
		if cfg.JobLockTTLSeconds > 0 {
			queue.lockTTL = time.Duration(cfg.JobLockTTLSeconds) * time.Second
		}
	}
	return queue
}

func (q *creativeQueue) Enqueue(ctx context.Context, runID string) error {
	if !service.IsValidCreativeRunID(runID) {
		return service.ErrInvalidCreativeQueuePayload
	}
	applied, err := creativeEnqueueScript.Run(ctx, q.rdb,
		[]string{q.inflightKey(runID), q.readyKey},
		runID, q.inflightTTL.Milliseconds(),
	).Int()
	if err != nil {
		return err
	}
	if applied == 0 {
		return service.ErrCreativeAlreadyQueued
	}
	return nil
}

func (q *creativeQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (service.ReservedCreativeRun, error) {
	deadline := time.Now().Add(blockTimeout)
	for {
		runID, leaseToken, err := q.reserveOnce(ctx)
		if err == nil {
			return service.ReservedCreativeRun{RunID: runID, LeaseToken: leaseToken}, nil
		}
		if !errors.Is(err, service.ErrCreativeQueueEmpty) {
			return service.ReservedCreativeRun{}, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return service.ReservedCreativeRun{}, service.ErrCreativeQueueEmpty
		}
		wait := creativeReservePollInterval
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return service.ReservedCreativeRun{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (q *creativeQueue) reserveOnce(ctx context.Context) (string, string, error) {
	leaseToken, err := newCreativeLockToken()
	if err != nil {
		return "", "", err
	}
	raw, err := creativeReserveScript.Run(ctx, q.rdb, []string{q.readyKey, q.activeKey, q.inflightPrefix}, time.Now().UnixMilli(), leaseToken, q.inflightTTL.Milliseconds()).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", service.ErrCreativeQueueEmpty
	}
	if err != nil {
		return "", "", err
	}
	runID, ok := raw.(string)
	if !ok || !service.IsValidCreativeRunID(runID) {
		// 非法 payload 已被脚本写入 active，必须移除，否则 stale 恢复会把它
		// 无限重投回 ready。
		if ok && runID != "" {
			_ = q.rdb.ZRem(ctx, q.activeKey, runID).Err()
			_ = q.rdb.Del(ctx, q.inflightKey(runID)).Err()
		}
		return "", "", service.ErrInvalidCreativeQueuePayload
	}
	return runID, leaseToken, nil
}

func (q *creativeQueue) RequeueAfter(ctx context.Context, runID, leaseToken string, delay time.Duration) error {
	if !service.IsValidCreativeRunID(runID) {
		return service.ErrInvalidCreativeQueuePayload
	}
	if leaseToken == "" {
		return service.ErrCreativeLeaseLost
	}
	result, err := creativeRequeueScript.Run(ctx, q.rdb,
		[]string{q.activeKey, q.delayedKey, q.inflightPrefix, q.readyKey},
		runID, leaseToken, time.Now().Add(delay).UnixMilli(), delay.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return service.ErrCreativeLeaseLost
	}
	return nil
}

func (q *creativeQueue) Ack(ctx context.Context, runID, leaseToken string) error {
	if !service.IsValidCreativeRunID(runID) {
		return service.ErrInvalidCreativeQueuePayload
	}
	if leaseToken == "" {
		return service.ErrCreativeLeaseLost
	}
	result, err := creativeAckScript.Run(ctx, q.rdb,
		[]string{q.activeKey, q.delayedKey, q.inflightPrefix}, runID, leaseToken).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return service.ErrCreativeLeaseLost
	}
	return nil
}

func (q *creativeQueue) Heartbeat(ctx context.Context, runID, leaseToken string) (bool, error) {
	if !service.IsValidCreativeRunID(runID) {
		return false, service.ErrInvalidCreativeQueuePayload
	}
	if leaseToken == "" {
		return false, service.ErrCreativeLeaseLost
	}
	// XX：只刷新已存在的 active 成员。无条件 ZAdd 会在 Ack/Requeue 之后的
	// 竞态心跳里把幽灵成员塞回 active zset。
	result, err := creativeHeartbeatScript.Run(ctx, q.rdb,
		[]string{q.activeKey, q.inflightPrefix}, runID, leaseToken, time.Now().UnixMilli()).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (q *creativeQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	return creativeMoveDueDelayedScript.Run(ctx, q.rdb, []string{q.delayedKey, q.readyKey}, time.Now().UnixMilli(), limit).Int()
}

func (q *creativeQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	if staleAfter <= 0 {
		return 0, service.ErrInvalidCreativeQueuePayload
	}
	if limit <= 0 {
		limit = 100
	}
	cutoff := time.Now().Add(-staleAfter).UnixMilli()
	return creativeRecoverStaleActiveScript.Run(ctx, q.rdb, []string{q.activeKey, q.readyKey, q.inflightPrefix}, cutoff, limit).Int()
}

func (q *creativeQueue) TryAcquireJobLock(ctx context.Context, runID string, ttl time.Duration) (service.CreativeRunJobLock, bool, error) {
	if !service.IsValidCreativeRunID(runID) {
		return nil, false, service.ErrInvalidCreativeQueuePayload
	}
	if ttl <= 0 {
		ttl = q.lockTTL
	}
	token, err := newCreativeLockToken()
	if err != nil {
		return nil, false, err
	}
	key := q.lockKey(runID)
	ok, err := q.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return &creativeRedisJobLock{rdb: q.rdb, key: key, token: token}, true, nil
}

func (q *creativeQueue) inflightKey(runID string) string {
	return q.inflightPrefix + runID
}

func (q *creativeQueue) lockKey(runID string) string {
	return q.lockPrefix + runID
}

type creativeRedisJobLock struct {
	rdb   *redis.Client
	key   string
	token string
}

func (l *creativeRedisJobLock) Release(ctx context.Context) error {
	if l == nil || l.rdb == nil || l.key == "" || l.token == "" {
		return nil
	}
	return creativeReleaseLockScript.Run(ctx, l.rdb, []string{l.key}, l.token).Err()
}

// Refresh 在仍持有锁（token 匹配）时续期 TTL，供长处理任务的心跳调用。
func (l *creativeRedisJobLock) Refresh(ctx context.Context, ttl time.Duration) (bool, error) {
	if l == nil || l.rdb == nil || l.key == "" || l.token == "" {
		return false, nil
	}
	if ttl <= 0 {
		ttl = defaultCreativeJobLockTTL
	}
	result, err := creativeRefreshLockScript.Run(ctx, l.rdb, []string{l.key}, l.token, ttl.Milliseconds()).Int()
	return result == 1, err
}

var _ service.CreativeRunQueue = (*creativeQueue)(nil)
var _ service.CreativeRunJobLockRefresher = (*creativeRedisJobLock)(nil)

func newCreativeLockToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

type QueueOptions struct {
	InflightKeyPrefix  string
	InflightTTLSeconds int
	JobLockTTLSeconds  int
	LockKeyPrefix      string
	QueueActiveKey     string
	QueueDelayedKey    string
	QueueReadyKey      string
}
