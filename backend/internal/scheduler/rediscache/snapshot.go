package rediscache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

const (
	schedulerBucketSetKey          = "sched:v2:buckets"
	schedulerOutboxWatermarkKey    = "sched:v2:outbox:watermark"
	schedulerAccountPrefix         = "sched:v2:acc:"
	schedulerAccountMetaPrefix     = "sched:v2:meta:"
	schedulerAccountLastUsedPrefix = "sched:v2:acc:last_used:"
	schedulerActivePrefix          = "sched:v2:active:"
	schedulerReadyPrefix           = "sched:v2:ready:"
	schedulerVersionPrefix         = "sched:v2:ver:"
	schedulerEpochPrefix           = "sched:v2:epoch:"
	schedulerRetiredPrefix         = "sched:v2:retired:"
	schedulerSnapshotPrefix        = "sched:v2:"
	schedulerLockPrefix            = "sched:v2:lock:"

	defaultSchedulerSnapshotMGetChunkSize  = 128
	defaultSchedulerSnapshotWriteChunkSize = 256
	schedulerLastUsedUpdateChunkSize       = 256

	// snapshotGraceTTLSeconds 旧快照过期的宽限期（秒）。
	// 替代立即 DEL，让正在读取旧版本的 reader 有足够时间完成 ZRANGE。
	snapshotGraceTTLSeconds = 60
)

const (
	schedulerGroupLifecycleLockPrefix      = "sched:v2:group:lifecycle-lock:"
	schedulerGroupLifecycleOwnerTokenBytes = 16
)

var updateSchedulerLastUsedScript = redis.NewScript(`
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

var (
	// epoch 标识 bucket writer 的代际，retired key 是持久退休标记。
	// Capture、allocate、activate 都在 Lua 内同时校验两者：-1 表示已退休，-2 表示 epoch 无效或与 token 代际不匹配；
	// allocate 与 activate 的双重校验可拦截快照写入期间发生的 Retire。
	// Retire 仅在首次退休时推进 epoch，Reopen 只清除标记并沿用该代际，因此重复调用保持幂等。
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

	// 释放租约必须先比较所有者令牌再删除，过期持有者的延迟释放不能误删继任租约。
	releaseGroupLifecycleLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`)

	// activateSnapshotScript 原子 CAS 切换快照版本。
	// 仅当新版本号 >= 当前激活版本时才切换，防止并发写入导致版本回滚。
	// 旧快照使用 EXPIRE 设置宽限期而非立即 DEL，避免与 reader 竞态。
	//
	// KEYS[1] = activeKey     (sched:active:{bucket})
	// KEYS[2] = readyKey      (sched:ready:{bucket})
	// KEYS[3] = bucketSetKey  (sched:buckets)
	// KEYS[4] = snapshotKey   (新写入的快照 key)
	// KEYS[5]：epoch key。
	// KEYS[6]：退休标记 key。
	// ARGV[1] = 新版本号字符串
	// ARGV[2] = bucket 字符串 (用于 SADD)
	// ARGV[3] = 快照 key 前缀 (用于构造旧快照 key)
	// ARGV[4] = 宽限期 TTL 秒数
	// ARGV[5]：writer epoch。
	//
	// 返回 1 = 已激活, 0 = 版本过旧未激活
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

type SnapshotCache struct {
	codec          SnapshotCodec
	rdb            *redis.Client
	mgetChunkSize  int
	writeChunkSize int
}

// SnapshotCacheOptions 仅控制原读写分块，零值采用原默认参数。
type SnapshotCacheOptions struct {
	MGetChunkSize  int
	WriteChunkSize int
}

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

func (c *SnapshotCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]scheduler.SnapshotAccount, bool, error) {
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
		// 空快照视为缓存未命中，触发数据库回退查询
		// 这解决了新分组创建后立即绑定账号时的竞态条件问题
		return nil, false, nil
	}

	keys := make([]string, 0, len(ids))
	lastUsedKeys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, schedulerAccountMetaKey(id))
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

	accounts := make([]scheduler.SnapshotAccount, 0, len(values))
	for i, val := range values {
		if val == nil {
			return nil, false, nil
		}
		account, err := c.codec.Decode(val)
		if err != nil {
			return nil, false, err
		}
		if err := c.applySchedulerLastUsed(account, lastUsedValues[i]); err != nil {
			return nil, false, err
		}
		accounts = append(accounts, account)
	}

	return accounts, true, nil
}

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

func newSchedulerGroupLifecycleOwnerToken() (string, error) {
	raw := make([]byte, schedulerGroupLifecycleOwnerTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate scheduler group lifecycle owner token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func (c *SnapshotCache) SetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, accounts []scheduler.SnapshotAccount) error {
	if !token.ValidFor(bucket) {
		return fmt.Errorf("%w: bucket=%s", scheduler.ErrSchedulerBucketWriteFenced, bucket.String())
	}
	// 分配版本与激活指针是两个 fencing 边界；中间写入的数据只有通过第二次校验才能发布。
	version, err := c.allocateSnapshotVersion(ctx, bucket, token)
	if err != nil {
		return err
	}
	// 快照成员最终只依赖可编码账号的有序 ID；直接复用 ID 路径，避免为
	// 随后立即丢弃的完整 Account 再分配一份临时切片。
	if _, err := c.writeSnapshotVersionAndReturnAccountIDs(ctx, bucket, version, accounts); err != nil {
		return err
	}
	return c.activateSnapshotVersion(ctx, bucket, token, version)
}

// SetSnapshotAndReturnAccountIDs 完整发布快照，并返回实际成功编码并写入的有序账号 ID。
// 该可选能力只供同一重建批次复用，返回前仍会完成版本激活与 fencing 校验。
func (c *SnapshotCache) SetSnapshotAndReturnAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, accounts []scheduler.SnapshotAccount) ([]int64, error) {
	if !token.ValidFor(bucket) {
		return nil, fmt.Errorf("%w: bucket=%s", scheduler.ErrSchedulerBucketWriteFenced, bucket.String())
	}
	// 分配版本与激活指针是两个 fencing 边界；中间写入的数据只有通过第二次校验才能发布。
	version, err := c.allocateSnapshotVersion(ctx, bucket, token)
	if err != nil {
		return nil, err
	}
	accountIDs, err := c.writeSnapshotVersionAndReturnAccountIDs(ctx, bucket, version, accounts)
	if err != nil {
		return nil, err
	}
	if err := c.activateSnapshotVersion(ctx, bucket, token, version); err != nil {
		return nil, err
	}
	return accountIDs, nil
}

// SetSnapshotByAccountIDs 复用同批次首次完整写入后得到的账号成员。
// 每个桶仍独立分配版本、写入有序集合并执行激活 fencing，只省略重复的账号 JSON 与全局键写入。
func (c *SnapshotCache) SetSnapshotByAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, accountIDs []int64) error {
	if !token.ValidFor(bucket) {
		return fmt.Errorf("%w: bucket=%s", scheduler.ErrSchedulerBucketWriteFenced, bucket.String())
	}
	version, err := c.allocateSnapshotVersion(ctx, bucket, token)
	if err != nil {
		return err
	}
	if err := c.writeSnapshotAccountIDs(ctx, bucket, version, accountIDs); err != nil {
		return err
	}
	return c.activateSnapshotVersion(ctx, bucket, token, version)
}

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

func (c *SnapshotCache) writeSnapshotVersionAndReturnAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, version string, accounts []scheduler.SnapshotAccount) ([]int64, error) {
	accountIDs, err := c.writeAccountIDs(ctx, accounts)
	if err != nil {
		return nil, err
	}
	if err := c.writeSnapshotAccountIDs(ctx, bucket, version, accountIDs); err != nil {
		return nil, err
	}
	return accountIDs, nil
}

func (c *SnapshotCache) writeSnapshotAccountIDs(ctx context.Context, bucket scheduler.SchedulerBucket, version string, accountIDs []int64) error {
	members := schedulerSnapshotMembers(accountIDs)
	return c.writeSnapshotMembers(ctx, bucket, version, members)
}

func schedulerSnapshotMembers(accountIDs []int64) []redis.Z {
	if len(accountIDs) == 0 {
		return nil
	}
	// 使用序号作为 score，保持数据库返回的排序语义；重复 ID 继续交由 Redis ZADD
	// 按最后一个 score 覆盖，与直接从账号切片构造成员时的行为一致。
	members := make([]redis.Z, 0, len(accountIDs))
	for idx, accountID := range accountIDs {
		members = append(members, redis.Z{
			Score:  float64(idx),
			Member: strconv.FormatInt(accountID, 10),
		})
	}
	return members
}

func (c *SnapshotCache) writeSnapshotMembers(ctx context.Context, bucket scheduler.SchedulerBucket, version string, members []redis.Z) error {
	if len(members) == 0 {
		return nil
	}
	snapshotKey := schedulerSnapshotKey(bucket, version)
	pipe := c.rdb.Pipeline()
	for start := 0; start < len(members); start += c.writeChunkSize {
		end := start + c.writeChunkSize
		if end > len(members) {
			end = len(members)
		}
		pipe.ZAdd(ctx, snapshotKey, members[start:end]...)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (c *SnapshotCache) activateSnapshotVersion(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, version string) error {
	snapshotKey := schedulerSnapshotKey(bucket, version)
	// 第二阶段：原子 CAS 切换版本，同时再次校验退休状态与 writer epoch。
	// Lua 脚本保证：仅当新版本 >= 当前激活版本时才切换 active 指针，
	// 防止并发写入导致版本回滚。
	// 旧快照使用 EXPIRE 宽限期而非立即 DEL，避免 reader 竞态。
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

func (c *SnapshotCache) GetAccount(ctx context.Context, accountID int64) (scheduler.SnapshotAccount, error) {
	id := strconv.FormatInt(accountID, 10)
	values, err := c.rdb.MGet(ctx, schedulerAccountKey(id), schedulerLastUsedKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(values) != 2 || values[0] == nil {
		return nil, nil
	}
	account, err := c.codec.Decode(values[0])
	if err != nil {
		return nil, err
	}
	if err := c.applySchedulerLastUsed(account, values[1]); err != nil {
		return nil, err
	}
	return account, nil
}

func (c *SnapshotCache) SetAccount(ctx context.Context, account scheduler.SnapshotAccount) error {
	if account == nil || account.SnapshotMetadata().ID <= 0 {
		return nil
	}
	accountIDs, err := c.writeAccountIDs(ctx, []scheduler.SnapshotAccount{account})
	if err != nil {
		return err
	}
	if len(accountIDs) == 0 {
		return c.DeleteAccount(ctx, account.SnapshotMetadata().ID)
	}
	return nil
}

func (c *SnapshotCache) DeleteAccount(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return nil
	}
	id := strconv.FormatInt(accountID, 10)
	return c.rdb.Del(ctx, schedulerAccountKey(id), schedulerAccountMetaKey(id), schedulerLastUsedKey(id)).Err()
}

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
		updateSchedulerLastUsedScript.Eval(ctx, pipe, keys, args...)
		queued++
		keys = make([]string, 0, schedulerLastUsedUpdateChunkSize*2)
		args = make([]any, 0, schedulerLastUsedUpdateChunkSize)
	}
	for id, usedAt := range updates {
		if id <= 0 {
			continue
		}
		millis, err := schedulerLastUsedMillis(usedAt)
		if err != nil {
			slog.Warn("scheduler cache removes account with unencodable payload",
				"account_id", id,
				"error", err,
			)
			idText := strconv.FormatInt(id, 10)
			pipe.Del(ctx, schedulerAccountKey(idText), schedulerAccountMetaKey(idText), schedulerLastUsedKey(idText))
			queued++
			continue
		}
		idText := strconv.FormatInt(id, 10)
		keys = append(keys, schedulerAccountKey(idText), schedulerLastUsedKey(idText))
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

func (c *SnapshotCache) ListBuckets(ctx context.Context) ([]scheduler.SchedulerBucket, error) {
	raw, err := c.rdb.SMembers(ctx, schedulerBucketSetKey).Result()
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.SchedulerBucket, 0, len(raw))
	for _, entry := range raw {
		bucket, ok := scheduler.ParseSchedulerBucket(entry)
		if !ok {
			continue
		}
		out = append(out, bucket)
	}
	return out, nil
}

func (c *SnapshotCache) GetOutboxWatermark(ctx context.Context) (int64, error) {
	val, err := c.rdb.Get(ctx, schedulerOutboxWatermarkKey).Result()
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

func (c *SnapshotCache) SetOutboxWatermark(ctx context.Context, id int64) error {
	return c.rdb.Set(ctx, schedulerOutboxWatermarkKey, strconv.FormatInt(id, 10), 0).Err()
}

func schedulerBucketKey(prefix string, bucket scheduler.SchedulerBucket) string {
	return fmt.Sprintf("%s%d:%s:%s", prefix, bucket.GroupID, bucket.Platform, bucket.Mode)
}

func schedulerGroupLifecycleLockKey(groupID int64) string {
	return schedulerGroupLifecycleLockPrefix + strconv.FormatInt(groupID, 10)
}

func schedulerSnapshotKey(bucket scheduler.SchedulerBucket, version string) string {
	return fmt.Sprintf("%s%d:%s:%s:v%s", schedulerSnapshotPrefix, bucket.GroupID, bucket.Platform, bucket.Mode, version)
}

func schedulerAccountKey(id string) string {
	return schedulerAccountPrefix + id
}

func schedulerAccountMetaKey(id string) string {
	return schedulerAccountMetaPrefix + id
}

func schedulerLastUsedKey(id string) string {
	return schedulerAccountLastUsedPrefix + id
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func schedulerLastUsedMillis(value time.Time) (int64, error) {
	if _, err := value.MarshalJSON(); err != nil {
		return 0, err
	}
	return value.UTC().UnixMilli(), nil
}

func (c *SnapshotCache) applySchedulerLastUsed(account scheduler.SnapshotAccount, value any) error {
	if account == nil || value == nil {
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
	current, err := c.codec.LastUsedAt(account)
	if err != nil {
		return err
	}
	if current == nil || lastUsedAt.After(*current) {
		return c.codec.SetLastUsedAt(account, ptrTime(lastUsedAt))
	}
	return nil
}

func (c *SnapshotCache) writeAccountIDs(ctx context.Context, accounts []scheduler.SnapshotAccount) ([]int64, error) {
	if len(accounts) == 0 {
		return nil, nil
	}

	pipe := c.rdb.Pipeline()
	accountIDs := make([]int64, 0, len(accounts))
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		pipe = c.rdb.Pipeline()
		pending = 0
		return nil
	}

	for _, account := range accounts {
		fullPayload, metaPayload, err := c.codec.Encode(account)
		if err != nil {
			slog.Warn("scheduler cache skips account with unencodable payload",
				"account_id", account.SnapshotMetadata().ID,
				"error", err,
			)
			continue
		}

		id := strconv.FormatInt(account.SnapshotMetadata().ID, 10)
		pipe.Set(ctx, schedulerAccountKey(id), fullPayload, 0)
		pipe.Set(ctx, schedulerAccountMetaKey(id), metaPayload, 0)
		// 保持高频 LastUsedAt 旁路键不变，防止滞后的快照重建覆盖更新的调度时间。
		accountIDs = append(accountIDs, account.SnapshotMetadata().ID)
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
	return accountIDs, nil
}

func (c *SnapshotCache) mgetChunked(ctx context.Context, keys []string) ([]any, error) {
	if len(keys) == 0 {
		return []any{}, nil
	}

	out := make([]any, 0, len(keys))
	chunkSize := c.mgetChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultSchedulerSnapshotMGetChunkSize
	}
	for start := 0; start < len(keys); start += chunkSize {
		end := start + chunkSize
		if end > len(keys) {
			end = len(keys)
		}
		part, err := c.rdb.MGet(ctx, keys[start:end]...).Result()
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

func (c *SnapshotCache) AcquireBucketLease(ctx context.Context, bucket scheduler.SchedulerBucket, ttl time.Duration) (*scheduler.BucketLease, bool, error) {
	return NewBucketLocks(c.rdb).AcquireBucketLease(ctx, bucket, ttl)
}
