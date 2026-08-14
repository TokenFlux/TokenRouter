package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	teamInvitationRecipientCooldown = time.Minute
	teamInvitationHourlyWindow      = time.Hour
	teamInvitationHourlyLimit       = 20
)

var teamInvitationRateScript = redis.NewScript(`
local cooldown_ttl = redis.call('PTTL', KEYS[1])
if cooldown_ttl > 0 then
  return {0, cooldown_ttl}
end
local hourly_count = tonumber(redis.call('GET', KEYS[2]) or '0')
if hourly_count >= tonumber(ARGV[3]) then
  local hourly_ttl = redis.call('PTTL', KEYS[2])
  if hourly_ttl < 1 then hourly_ttl = tonumber(ARGV[2]) end
  return {0, hourly_ttl}
end
redis.call('SET', KEYS[1], '1', 'PX', ARGV[1])
hourly_count = redis.call('INCR', KEYS[2])
if hourly_count == 1 then redis.call('PEXPIRE', KEYS[2], ARGV[2]) end
return {1, 0}
`)

type teamInvitationLimiter struct {
	redis *redis.Client
}

func NewTeamInvitationLimiter(redisClient *redis.Client) service.TeamInvitationLimiter {
	return &teamInvitationLimiter{redis: redisClient}
}

func (l *teamInvitationLimiter) CheckAndRecord(ctx context.Context, teamID int64, email string) (bool, time.Duration, error) {
	if l == nil || l.redis == nil {
		return false, 0, fmt.Errorf("团队邀请 Redis 未配置")
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	sum := sha256.Sum256([]byte(normalizedEmail))
	recipientKey := fmt.Sprintf("team:invite:%d:recipient:%s", teamID, hex.EncodeToString(sum[:]))
	hourlyKey := fmt.Sprintf("team:invite:%d:hourly", teamID)

	value, err := teamInvitationRateScript.Run(ctx, l.redis, []string{recipientKey, hourlyKey},
		teamInvitationRecipientCooldown.Milliseconds(), teamInvitationHourlyWindow.Milliseconds(), teamInvitationHourlyLimit).Result()
	if err != nil {
		return false, 0, err
	}
	parts, ok := value.([]any)
	if !ok || len(parts) != 2 {
		return false, 0, fmt.Errorf("团队邀请限流返回值无效")
	}
	allowed, err := redisInt64(parts[0])
	if err != nil {
		return false, 0, err
	}
	retryMillis, err := redisInt64(parts[1])
	if err != nil {
		return false, 0, err
	}
	return allowed == 1, time.Duration(retryMillis) * time.Millisecond, nil
}

func redisInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		var parsed int64
		_, err := fmt.Sscan(typed, &parsed)
		return parsed, err
	default:
		// 错误字符串保持小写开头，便于上层继续包装上下文。
		return 0, fmt.Errorf("redis 整数类型无效: %T", value)
	}
}
