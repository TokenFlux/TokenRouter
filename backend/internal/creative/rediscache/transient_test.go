//go:build unit

package rediscache

import (
	"context"
	"testing"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newCreativeTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, client
}

func newCreativeTestTransientStore(client *redis.Client) service.CreativeTransientStore {
	return NewCreativeTransientStore(client, &TransientOptions{TransientTTLSeconds: 60})
}

// TestCreativeTransientStoreRoundTrip 校验临时存储的读写删全链路。
func TestCreativeTransientStoreRoundTrip(t *testing.T) {
	_, client := newCreativeTestRedis(t)
	store := newCreativeTestTransientStore(client)
	ctx := context.Background()
	runID := "crun_testroundtrip0123"

	payload := &service.CreativeRunPayload{
		RunID:       runID,
		UserID:      7,
		GroupID:     12,
		APIKeyID:    900,
		Model:       "gemini-3.1-flash-image",
		Operation:   service.CreativeOperationInpaint,
		Prompt:      "临时 prompt",
		SourceCount: 2,
		HasMask:     true,
	}
	require.NoError(t, store.SavePayload(ctx, runID, payload))
	loaded, err := store.LoadPayload(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, payload.Prompt, loaded.Prompt)

	require.NoError(t, store.SaveInput(ctx, runID, 0, []byte("img0")))
	require.NoError(t, store.SaveInput(ctx, runID, 1, []byte("img1")))
	inputs, err := store.LoadInputs(ctx, runID, 2)
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("img0"), []byte("img1")}, inputs)

	require.NoError(t, store.SaveMask(ctx, runID, []byte("mask")))
	mask, err := store.LoadMask(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, []byte("mask"), mask)

	require.NoError(t, store.SaveOutput(ctx, runID, 0, []byte("out0"), time.Minute))
	output, err := store.LoadOutput(ctx, runID, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("out0"), output)

	// 删除单张输出：幂等，重复删除不报错。
	require.NoError(t, store.DeleteOutput(ctx, runID, 0))
	require.NoError(t, store.DeleteOutput(ctx, runID, 0))
	_, err = store.LoadOutput(ctx, runID, 0)
	require.Error(t, err)

	// 删除任务全部临时数据。
	require.NoError(t, store.DeleteRunTransient(ctx, runID, 2, 1))
	_, err = store.LoadPayload(ctx, runID)
	require.Error(t, err)
	_, err = store.LoadMask(ctx, runID)
	require.Error(t, err)
	_, err = store.LoadInputs(ctx, runID, 2)
	require.Error(t, err)
}

// TestCreativeTransientStoreExpiry 校验 TTL 到期后无法读取。
func TestCreativeTransientStoreExpiry(t *testing.T) {
	server, client := newCreativeTestRedis(t)
	store := newCreativeTestTransientStore(client)
	ctx := context.Background()
	runID := "crun_testexpiry012345"

	require.NoError(t, store.SavePayload(ctx, runID, &service.CreativeRunPayload{RunID: runID, Prompt: "x"}))
	require.NoError(t, store.SaveOutput(ctx, runID, 0, []byte("out"), time.Minute))

	// 快进 61 秒越过 60 秒 TTL。
	server.FastForward(61 * time.Second)

	_, err := store.LoadPayload(ctx, runID)
	require.ErrorIs(t, err, service.ErrCreativeTransientFailed)
	_, err = store.LoadOutput(ctx, runID, 0)
	require.ErrorIs(t, err, service.ErrCreativeTransientFailed)
}

// TestCreativeTransientStoreInputsMissing 校验缺失任一张源图时报错。
func TestCreativeTransientStoreInputsMissing(t *testing.T) {
	_, client := newCreativeTestRedis(t)
	store := newCreativeTestTransientStore(client)
	ctx := context.Background()
	runID := "crun_testmissing012345"

	require.NoError(t, store.SaveInput(ctx, runID, 0, []byte("img0")))
	_, err := store.LoadInputs(ctx, runID, 2)
	require.Error(t, err)
}

// TestCreativeQueueEnqueueReserveAck 校验队列的入队/保留/确认语义。
func TestCreativeQueueEnqueueReserveAck(t *testing.T) {
	_, client := newCreativeTestRedis(t)
	queue := NewCreativeQueue(client, &QueueOptions{
		QueueReadyKey:      "creative:queue:ready",
		QueueDelayedKey:    "creative:queue:delayed",
		QueueActiveKey:     "creative:queue:active",
		InflightKeyPrefix:  "creative:queue:inflight:",
		LockKeyPrefix:      "creative:queue:lock:",
		InflightTTLSeconds: 3600,
		JobLockTTLSeconds:  60,
	})
	ctx := context.Background()
	runID := "crun_testqueue0123456"

	require.NoError(t, queue.Enqueue(ctx, runID))
	// 重复入队返回明确冲突。
	require.ErrorIs(t, queue.Enqueue(ctx, runID), service.ErrCreativeAlreadyQueued)

	reserved, err := queue.Reserve(ctx, time.Second)
	require.NoError(t, err)
	require.Equal(t, runID, reserved.RunID)

	// 队列已空。
	_, err = queue.Reserve(ctx, 50*time.Millisecond)
	require.ErrorIs(t, err, service.ErrCreativeQueueEmpty)

	// 非法 payload 校验。
	require.ErrorIs(t, queue.Enqueue(ctx, "bad-id"), service.ErrInvalidCreativeQueuePayload)

	// Ack 后任务彻底离开队列结构。
	require.NoError(t, queue.Ack(ctx, runID, reserved.LeaseToken))
	moved, err := queue.MoveDueDelayedToReady(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 0, moved)
}

// TestCreativeQueueRecoverStaleActive 校验 stale active 恢复重投 ready。
func TestCreativeQueueRecoverStaleActive(t *testing.T) {
	_, client := newCreativeTestRedis(t)
	queue := NewCreativeQueue(client, &QueueOptions{
		QueueReadyKey:      "creative:queue:ready",
		QueueDelayedKey:    "creative:queue:delayed",
		QueueActiveKey:     "creative:queue:active",
		InflightKeyPrefix:  "creative:queue:inflight:",
		LockKeyPrefix:      "creative:queue:lock:",
		InflightTTLSeconds: 3600,
		JobLockTTLSeconds:  60,
	})
	ctx := context.Background()
	runID := "crun_testrecover012345"

	require.NoError(t, queue.Enqueue(ctx, runID))
	reserved, err := queue.Reserve(ctx, time.Second)
	require.NoError(t, err)
	require.Equal(t, runID, reserved.RunID)

	// 立刻恢复不会动新鲜任务。
	recovered, err := queue.RecoverStaleActive(ctx, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, 0, recovered)

	// staleAfter 为 0 视为非法参数。
	_, err = queue.RecoverStaleActive(ctx, 0, 10)
	require.ErrorIs(t, err, service.ErrInvalidCreativeQueuePayload)
}

// TestCreativeQueueLeaseFencingOldWorkerCannotAck 校验 stale 接管后旧 worker token 不能再确认任务。
func TestCreativeQueueLeaseFencingOldWorkerCannotAck(t *testing.T) {
	_, client := newCreativeTestRedis(t)
	queue := NewCreativeQueue(client, &QueueOptions{
		QueueReadyKey: "creative:queue:ready", QueueDelayedKey: "creative:queue:delayed", QueueActiveKey: "creative:queue:active",
		InflightKeyPrefix: "creative:queue:inflight:", InflightTTLSeconds: 3600,
	})
	ctx := context.Background()
	runID := "crun_testfencing012345"
	require.NoError(t, queue.Enqueue(ctx, runID))
	first, err := queue.Reserve(ctx, time.Second)
	require.NoError(t, err)
	// 模拟 worker A 长时间失联，恢复脚本原子转移并清除旧 token。
	require.NoError(t, client.ZAdd(ctx, "creative:queue:active", redis.Z{Score: float64(time.Now().Add(-time.Hour).UnixMilli()), Member: runID}).Err())
	moved, err := queue.RecoverStaleActive(ctx, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, 1, moved)
	second, err := queue.Reserve(ctx, time.Second)
	require.NoError(t, err)
	require.NotEqual(t, first.LeaseToken, second.LeaseToken)
	require.ErrorIs(t, queue.Ack(ctx, runID, first.LeaseToken), service.ErrCreativeLeaseLost)
	require.NoError(t, queue.Ack(ctx, runID, second.LeaseToken))
}
