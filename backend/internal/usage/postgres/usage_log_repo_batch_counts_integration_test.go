//go:build integration

package postgres

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// TestBatchCountsIncludeZeroChargeAndRespectEndBoundary records billable, free, and out-of-range actor requests.
// Counts include the free request, exclude the exact end boundary, and preserve a zero row for an actor without retained usage.
func (s *UsageLogRepoSuite) TestBatchCountsIncludeZeroChargeAndRespectEndBoundary() {
	user := mustCreateUser(s.T(), s.client, &identity.User{Email: "plugin-counts@test.local"})
	other := mustCreateUser(s.T(), s.client, &identity.User{Email: "plugin-counts-empty@test.local"})
	key := mustCreateApiKey(s.T(), s.client, &apikey.APIKey{UserID: user.ID, Key: "plugin-counts-key", Name: "fixture"})
	upstream := mustCreateProvider(s.T(), s.client, &provider.Record{Name: "plugin-counts-provider"})
	start := time.Now().Add(-time.Hour)
	end := start.Add(30 * time.Minute)
	s.createUsageLog(user, key, upstream, 10, 20, 0.5, start)
	s.createUsageLog(user, key, upstream, 5, 7, 0, start.Add(time.Minute))
	s.createUsageLog(user, key, upstream, 100, 200, 1, end)
	result, err := s.repo.GetBatchUserUsageCounts(s.ctx, []int64{user.ID, other.ID}, start, end)
	s.Require().NoError(err)
	s.Require().Equal(int64(2), result[user.ID].Requests)
	s.Require().Equal(int64(42), result[user.ID].Tokens)
	s.Require().Zero(result[other.ID].Requests)
}
