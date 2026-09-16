package rediscache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	anthropic "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/redis/go-redis/v9"
)

const (
	FingerprintKeyPrefix   = "fingerprint:"
	FingerprintTTL         = 7 * 24 * time.Hour // 7天，配合每24小时懒续期可保持活跃账号永不过期
	maskedSessionKeyPrefix = "masked_session:"
	maskedSessionTTL       = 15 * time.Minute
)

// fingerprintKey generates the Redis key for account fingerprint cache.
func fingerprintKey(accountID int64) string {
	return fmt.Sprintf("%s%d", FingerprintKeyPrefix, accountID)
}

// maskedSessionKey generates the Redis key for masked session ID cache.
func maskedSessionKey(accountID int64) string {
	return fmt.Sprintf("%s%d", maskedSessionKeyPrefix, accountID)
}

type FingerprintStore struct {
	rdb *redis.Client
}

func NewFingerprintStore(rdb *redis.Client) anthropic.FingerprintCache {
	return &FingerprintStore{rdb: rdb}
}

func (c *FingerprintStore) GetFingerprint(ctx context.Context, accountID int64) (*anthropic.Fingerprint, error) {
	key := fingerprintKey(accountID)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	var fp anthropic.Fingerprint
	if err := json.Unmarshal([]byte(val), &fp); err != nil {
		return nil, err
	}
	return &fp, nil
}

func (c *FingerprintStore) SetFingerprint(ctx context.Context, accountID int64, fp *anthropic.Fingerprint) error {
	key := fingerprintKey(accountID)
	val, err := json.Marshal(fp)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, val, FingerprintTTL).Err()
}

func (c *FingerprintStore) GetMaskedSessionID(ctx context.Context, accountID int64) (string, error) {
	key := maskedSessionKey(accountID)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", err
	}
	return val, nil
}

func (c *FingerprintStore) SetMaskedSessionID(ctx context.Context, accountID int64, sessionID string) error {
	key := maskedSessionKey(accountID)
	return c.rdb.Set(ctx, key, sessionID, maskedSessionTTL).Err()
}
