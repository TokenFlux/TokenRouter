//go:build unit

package account

import (
	"context"
	"time"
)

// 只记录实际存储写入，未实现的方法不得被本组用例调用。
type antigravityHealthStoreFixture struct {
	AntigravityHealthStore
	modelRateLimitCalls []struct {
		accountID int64
		modelKey  string
		resetAt   time.Time
	}
	extraUpdateCalls []struct {
		accountID int64
		updates   map[string]any
	}
}

func (s *antigravityHealthStoreFixture) SetModelRateLimit(_ context.Context, id int64, key string, at time.Time, _ ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, struct {
		accountID int64
		modelKey  string
		resetAt   time.Time
	}{id, key, at})
	return nil
}
func (s *antigravityHealthStoreFixture) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	s.extraUpdateCalls = append(s.extraUpdateCalls, struct {
		accountID int64
		updates   map[string]any
	}{id, updates})
	return nil
}
