// 本文件维护 rediscache 的所属能力；兼容入口复用唯一实现。
package rediscache

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/redis/go-redis/v9"
)

const passkeySessionPrefix = "passkey:session:"

type passkeySessionStore struct {
	redis *redis.Client
}

// NewPasskeySessionStore 创建基于 Redis 的一次性 WebAuthn 会话存储。
func NewPasskeySessionStore(redisClient *redis.Client) identity.PasskeySessionStore {
	return &passkeySessionStore{redis: redisClient}
}

func (s *passkeySessionStore) Store(
	ctx context.Context,
	session *identity.PasskeySession,
	ttl time.Duration,
) (string, error) {
	if session == nil || ttl <= 0 {
		return "", fmt.Errorf("invalid passkey session")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate passkey session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	payload, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("encode passkey session: %w", err)
	}
	if err = s.redis.Set(ctx, passkeySessionPrefix+token, payload, ttl).Err(); err != nil {
		return "", fmt.Errorf("store passkey session: %w", err)
	}
	return token, nil
}

func (s *passkeySessionStore) Consume(
	ctx context.Context,
	token string,
) (*identity.PasskeySession, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 128 {
		return nil, identity.ErrPasskeySession
	}
	// GETDEL 保证 challenge 在首次 finish 尝试时即失效，防止并发重放。
	payload, err := s.redis.GetDel(ctx, passkeySessionPrefix+token).Bytes()
	if err == redis.Nil {
		return nil, identity.ErrPasskeySession
	}
	if err != nil {
		return nil, fmt.Errorf("consume passkey session: %w", err)
	}
	var session identity.PasskeySession
	if err = json.Unmarshal(payload, &session); err != nil {
		return nil, identity.ErrPasskeySession
	}
	return &session, nil
}
