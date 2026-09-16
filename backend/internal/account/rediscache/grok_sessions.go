// Redis 序列化、前缀、TTL 和一次性标记继续使用已有技术 Store。
package rediscache

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/redis/session"
	"github.com/redis/go-redis/v9"
)

func NewGrokSessionStore(rdb *redis.Client) *account.GrokSessionStore {
	if rdb == nil {
		return account.NewGrokSessionStore(nil)
	}
	return account.NewGrokSessionStore(session.New(rdb, "oauth:session:xai", account.GrokSessionTTL))
}
