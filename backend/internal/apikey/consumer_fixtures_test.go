//go:build unit

package apikey_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

const apiKeyLimitUpperBound = apikey.KeyApiKeyLimitUpperBound

// 用户和团队替身只提供 Key 用例实际读取的端口；意外调用仍会失败。
type userRepoStub struct {
	identity.UserRepository
	user *identity.User
}

func (s *userRepoStub) GetByID(context.Context, int64) (*identity.User, error) {
	if s.user == nil {
		return nil, identity.ErrUserNotFound
	}
	return s.user, nil
}

type fakeTeamRepository struct {
	team.TeamRepository
	teamContext *team.TeamContext
}

func (r *fakeTeamRepository) GetContextByUserID(context.Context, int64) (*team.TeamContext, error) {
	return r.teamContext, nil
}

func (r *fakeTeamRepository) GetContextByTeamID(context.Context, int64) (*team.TeamContext, error) {
	return r.teamContext, nil
}

// 并发排序夹具继续经过真实 scheduler 读取器，仅替换其缓存数据源。
type stubConcurrencyCacheForTest struct {
	scheduler.ConcurrencyCache
	apiKeyConcurrency map[int64]int
}

func (c *stubConcurrencyCacheForTest) TrackAPIKeySlot(context.Context, int64, string) error {
	return nil
}

func (c *stubConcurrencyCacheForTest) ReleaseAPIKeySlot(context.Context, int64, string) error {
	return nil
}

func (c *stubConcurrencyCacheForTest) GetAPIKeyConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(ids))
	for _, id := range ids {
		result[id] = c.apiKeyConcurrency[id]
	}
	return result, nil
}
