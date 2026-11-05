//go:build integration

package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"
)

// Overlapping refresh requests can read the same stored token before either
// attempts its one-time Redis consumption. The barrier cache waits until both
// successful reads are observed, then releases both requests to race
// consumption through the actual Redis-backed cache. Their results must contain
// one successful rotation and one invalid-token rejection.
//
// The barrier's arrival send and release wait both observe context
// cancellation, and a sync.Once makes its cleanup release safe to repeat.
// Buffered result slots let the workers finish before the channel is closed for
// counting. A separate adapter returns configured consumption failures while
// retaining underlying reads and token writes, allowing the test to inspect a
// nil successor and an unchanged user-token hash set.

// refreshReadBarrier coordinates successful token reads before the tests
// release competing consumption attempts.
type refreshReadBarrier struct {
	// RefreshTokenCache performs the underlying reads and later service
	// operations; the wrapper retains its returned token data while the read
	// barrier is closed.
	identity.RefreshTokenCache
	// arrived and release are initialized before concurrent calls. arrived reports
	// successful reads, and closing release lets all parked callers continue
	// toward consumption.
	arrived chan struct{} // completed token read signals
	release chan struct{} // competing token consumption gate
}

// refreshDeleteFailure overrides token removal operations while delegating the
// remaining cache behavior.
type refreshDeleteFailure struct {
	// RefreshTokenCache supplies reads and writes that remain available during the
	// injected failure; the two removal overrides never call through to it.
	identity.RefreshTokenCache
	// failure is returned unchanged by both overrides. Nil permits Delete to
	// report success, while Consume still returns false and leaves the embedded
	// cache untouched.
	failure error
}

// GetRefreshToken reads the embedded cache and holds a successful result until
// the release channel closes. Cache failures return before arrival is signaled,
// and a selected cancellation case returns ctx.Err with no token data.
//
// The arrival send records completed reads so tests can release calls after
// both targeted reads have finished. Callers choose the credential keys and
// initialize both channels before calling; a buffered arrival channel lets the
// fixture collect signals while callers remain parked. The original cache value
// is retained across the wait, leaving consumption to subsequent service work.
func (c *refreshReadBarrier) GetRefreshToken(ctx context.Context, key string) (*identity.RefreshTokenData, error) {
	value, err := c.RefreshTokenCache.GetRefreshToken(ctx, key)
	if err != nil {
		return nil, err
	}
	// Announce the successful cached read before waiting for release so the test
	// can gather both arrivals before allowing either request to attempt token
	// consumption.
	select {
	case c.arrived <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Keep the loaded value parked until the shared release or cancellation wins;
	// cancellation returns no value so its caller cannot proceed with this
	// captured token.
	select {
	case <-c.release:
		return value, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DeleteRefreshToken returns the configured failure unchanged and bypasses the
// embedded cache's deletion method. Context and key are ignored, so a nil
// configured failure reports success while leaving stored token data unchanged.
func (c refreshDeleteFailure) DeleteRefreshToken(context.Context, string) error { return c.failure }

// ConsumeRefreshToken reports false with the configured failure and bypasses
// the embedded cache's consume method. Context and key are ignored; nil failure
// still reports that this fixture consumed nothing.
func (c refreshDeleteFailure) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, c.failure
}

// TestRefreshRotationConsumesOnce holds two successful reads of one refresh
// token before releasing PostgreSQL-backed authentication against Redis
// consumption. Exactly one rotation succeeds and one attempt reports
// RefreshTokenInvalid.
func TestRefreshRotationConsumesOnce(t *testing.T) {
	integrationDB, integrationEntClient := identityDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := integrationEntClient
	users := postgres.NewUserStore(client, integrationDB)
	user := mustCreateUser(t, client, &identity.User{})
	cache := rediscache.NewRefreshTokenCache(rediscontainer.New(t))
	barrier := &refreshReadBarrier{RefreshTokenCache: cache, arrived: make(chan struct{}, 2), release: make(chan struct{})}
	// Make release idempotent because normal progress and failing-test cleanup can
	// both try to open the barrier after workers have started waiting for it.
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	t.Cleanup(unblock)
	cfg := &config.Config{}
	cfg.Auth.JWT.Secret = "test-local-fixture-only"
	cfg.Auth.JWT.AccessTokenTTLMinutes = 60
	cfg.Auth.JWT.RefreshTokenTTLDays = 1
	auth := identitytestkit.Auth(client, &identity.AuthDependencies{Users: users, RefreshTokens: barrier, Options: identitytestkit.AuthOptions(cfg)})
	pair, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	// Buffer both rotation results, then wait for both completed reads before
	// releasing the barrier; workers can send independently before the test joins
	// them.
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { ; _, e := auth.RefreshTokenPair(ctx, pair.RefreshToken); results <- e })
	}
	for range 2 {
		select {
		case <-barrier.arrived:
		case <-ctx.Done():
			t.Fatal("两个请求未同时到达读取屏障", ctx.Err())
		}
	}
	// Release consumption only after both read signals have arrived, then join all
	// result sends before closing the channel and counting the two rotation
	// outcomes.
	unblock()
	workers.Wait()
	close(results)
	successes := 0
	invalid := 0
	for e := range results {
		if e == nil {
			successes++
		} else if errors.Is(e, identity.ErrRefreshTokenInvalid) {
			invalid++
		} else {
			t.Errorf("轮换出现非预期错误: %v", e)
		}
	}
	require.Equal(t, 1, successes, "同一 refresh token 只能签发一次后继凭据")
	require.Equal(t, 1, invalid, "已被并发请求消费的 token 必须拒绝")
}

// TestRefreshRotationStorageFailureDoesNotIssueTokens injects deletion and
// consumption failures after issuing a refresh token. Rotation returns
// ServiceUnavailable with no successor, and the underlying user's token hashes
// remain unchanged.
func TestRefreshRotationStorageFailureDoesNotIssueTokens(t *testing.T) {
	integrationDB, integrationEntClient := identityDatabase(t)
	ctx := context.Background()
	client := integrationEntClient
	users := postgres.NewUserStore(client, integrationDB)
	user := mustCreateUser(t, client, &identity.User{})
	cache := rediscache.NewRefreshTokenCache(rediscontainer.New(t))
	failing := refreshDeleteFailure{RefreshTokenCache: cache, failure: errors.New("test injected token consume failure")}
	cfg := &config.Config{}
	cfg.Auth.JWT.Secret = "test-local-fixture-only"
	cfg.Auth.JWT.AccessTokenTTLMinutes = 60
	cfg.Auth.JWT.RefreshTokenTTLDays = 1
	auth := identitytestkit.Auth(client, &identity.AuthDependencies{Users: users, RefreshTokens: failing, Options: identitytestkit.AuthOptions(cfg)})
	pair, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	before, err := cache.GetUserTokenHashes(ctx, user.ID)
	require.NoError(t, err)
	next, err := auth.RefreshTokenPair(ctx, pair.RefreshToken)
	require.ErrorIs(t, err, identity.ErrServiceUnavailable, "无法使旧凭据失效时不得继续签发")
	require.Nil(t, next)
	after, err := cache.GetUserTokenHashes(ctx, user.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
}
