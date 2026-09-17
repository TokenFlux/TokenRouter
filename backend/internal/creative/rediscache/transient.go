package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/redis/go-redis/v9"
)

// 创作台临时存储 Redis 键前缀。
// 输入载荷、源图字节、mask 与输出图片本体只保存在这些键下，TTL 到期即失效。
const (
	defaultCreativePayloadKeyPrefix = "creative:payload:"
	defaultCreativeInputKeyPrefix   = "creative:input:"
	defaultCreativeMaskKeyPrefix    = "creative:mask:"
	defaultCreativeOutputKeyPrefix  = "creative:output:"
)

// creativeTransientStore 基于 Redis 的创作台临时存储。
// Redis 不可用时所有写操作返回明确错误，由服务层 fail-close 拒绝新任务。
type creativeTransientStore struct {
	rdb           *redis.Client
	payloadPrefix string
	inputPrefix   string
	maskPrefix    string
	outputPrefix  string
	defaultTTL    time.Duration
}

// NewCreativeTransientStore 创建创作台临时存储。
func NewCreativeTransientStore(rdb *redis.Client, cfg *TransientOptions) service.CreativeTransientStore {
	store := &creativeTransientStore{
		rdb:           rdb,
		payloadPrefix: defaultCreativePayloadKeyPrefix,
		inputPrefix:   defaultCreativeInputKeyPrefix,
		maskPrefix:    defaultCreativeMaskKeyPrefix,
		outputPrefix:  defaultCreativeOutputKeyPrefix,
		defaultTTL:    30 * time.Minute,
	}
	if cfg != nil && cfg.TransientTTLSeconds > 0 {
		store.defaultTTL = time.Duration(cfg.TransientTTLSeconds) * time.Second
	}
	return store
}

func (s *creativeTransientStore) SavePayload(ctx context.Context, runID string, payload *service.CreativeRunPayload) error {
	if s.rdb == nil {
		return fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	key := s.payloadPrefix + runID
	// 写前删除再设置，保证重复保存同一任务时 TTL 与内容一致（幂等覆盖）。
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	pipe.Set(ctx, key, body, s.defaultTTL)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	return nil
}

func (s *creativeTransientStore) LoadPayload(ctx context.Context, runID string) (*service.CreativeRunPayload, error) {
	if s.rdb == nil {
		return nil, fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	body, err := s.rdb.Get(ctx, s.payloadPrefix+runID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: %w", service.ErrCreativeTransientNotFound, service.ErrCreativeTransientFailed)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	payload := &service.CreativeRunPayload{}
	if err := json.Unmarshal(body, payload); err != nil {
		return nil, fmt.Errorf("%w: %v", service.ErrCreativeTransientCorrupt, err)
	}
	return payload, nil
}

func (s *creativeTransientStore) SaveInput(ctx context.Context, runID string, idx int, data []byte) error {
	if s.rdb == nil {
		return fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	if len(data) == 0 {
		return errors.New("creative input is empty")
	}
	if err := s.rdb.Set(ctx, s.inputKey(runID, idx), data, s.defaultTTL).Err(); err != nil {
		return fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	return nil
}

func (s *creativeTransientStore) LoadInputs(ctx context.Context, runID string, count int) ([][]byte, error) {
	if s.rdb == nil {
		return nil, fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	if count <= 0 {
		return nil, nil
	}
	keys := make([]string, 0, count)
	for idx := 0; idx < count; idx++ {
		keys = append(keys, s.inputKey(runID, idx))
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	out := make([][]byte, 0, count)
	for idx, value := range values {
		raw, ok := value.(string)
		if !ok || raw == "" {
			return nil, fmt.Errorf("%w: %w: input %d for run %s", service.ErrCreativeTransientNotFound, service.ErrCreativeTransientFailed, idx, runID)
		}
		out = append(out, []byte(raw))
	}
	return out, nil
}

func (s *creativeTransientStore) SaveMask(ctx context.Context, runID string, data []byte) error {
	if s.rdb == nil {
		return fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	if len(data) == 0 {
		return errors.New("creative mask is empty")
	}
	if err := s.rdb.Set(ctx, s.maskPrefix+runID, data, s.defaultTTL).Err(); err != nil {
		return fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	return nil
}

func (s *creativeTransientStore) LoadMask(ctx context.Context, runID string) ([]byte, error) {
	if s.rdb == nil {
		return nil, fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	data, err := s.rdb.Get(ctx, s.maskPrefix+runID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: %w", service.ErrCreativeTransientNotFound, service.ErrCreativeTransientFailed)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	return data, nil
}

func (s *creativeTransientStore) SaveOutput(ctx context.Context, runID string, index int, data []byte, ttl time.Duration) error {
	if s.rdb == nil {
		return fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	if len(data) == 0 {
		return errors.New("creative output is empty")
	}
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	if err := s.rdb.Set(ctx, s.outputKey(runID, index), data, ttl).Err(); err != nil {
		return fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	return nil
}

func (s *creativeTransientStore) LoadOutput(ctx context.Context, runID string, index int) ([]byte, error) {
	if s.rdb == nil {
		return nil, fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	data, err := s.rdb.Get(ctx, s.outputKey(runID, index)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: %w", service.ErrCreativeTransientNotFound, service.ErrCreativeTransientFailed)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: %w", service.ErrCreativeTransientCorrupt, service.ErrCreativeTransientFailed)
	}
	return data, nil
}

func (s *creativeTransientStore) DeleteOutput(ctx context.Context, runID string, index int) error {
	if s.rdb == nil {
		return fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	// DEL 对不存在的键天然幂等。
	if err := s.rdb.Del(ctx, s.outputKey(runID, index)).Err(); err != nil {
		return fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	return nil
}

// DeleteRunTransient 删除任务全部临时键；inputCount/outputCount 未知时传 0 会退化为通配扫描。
func (s *creativeTransientStore) DeleteRunTransient(ctx context.Context, runID string, inputCount, outputCount int) error {
	if s.rdb == nil {
		return fmt.Errorf("%w: redis client is nil", service.ErrCreativeTransientUnavailable)
	}
	keys := []string{
		s.payloadPrefix + runID,
		s.maskPrefix + runID,
	}
	for idx := 0; idx < inputCount; idx++ {
		keys = append(keys, s.inputKey(runID, idx))
	}
	for index := 0; index < outputCount; index++ {
		keys = append(keys, s.outputKey(runID, index))
	}
	// 计数未知（如取消路径）时按前缀扫描补齐，保证清理完整。
	if inputCount <= 0 {
		if scanned, err := s.rdb.Keys(ctx, s.inputPrefix+runID+":*").Result(); err == nil {
			keys = append(keys, scanned...)
		}
	}
	if outputCount <= 0 {
		if scanned, err := s.rdb.Keys(ctx, s.outputPrefix+runID+":*").Result(); err == nil {
			keys = append(keys, scanned...)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("%w: %v", service.ErrCreativeTransientUnavailable, err)
	}
	return nil
}

func (s *creativeTransientStore) inputKey(runID string, idx int) string {
	return fmt.Sprintf("%s%s:%d", s.inputPrefix, runID, idx)
}

func (s *creativeTransientStore) outputKey(runID string, index int) string {
	return fmt.Sprintf("%s%s:%d", s.outputPrefix, runID, index)
}

var _ service.CreativeTransientStore = (*creativeTransientStore)(nil)

type TransientOptions struct {
	TransientTTLSeconds int
}
