package selection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

func TestGetStickySessionAccountID_FallbackToLegacyKey(t *testing.T) {
	stats := &scheduler.StickyStats{}
	beforeFallbackTotal, beforeFallbackHit, _ := stats.Snapshot()

	cache := &stickyCacheFixture{
		sessionBindings: map[string]int64{
			"openai:legacy-hash": 42,
		},
	}
	svc := NewCompatible(CompatibleDependencies{Shared: Shared{
		Cache: cache}, StickyStats: stats}, Options{StickyTTL: time.Hour, ReadLegacySticky: true, WriteLegacySticky: false})

	ctx := requeststate.WithOpenAILegacySessionHash(context.Background(), "legacy-hash")

	accountID, err := svc.getStickySessionAccountID(ctx, nil, "new-hash")
	require.NoError(t, err)
	require.Equal(t, int64(42), accountID)

	afterFallbackTotal, afterFallbackHit, _ := stats.Snapshot()
	require.Equal(t, beforeFallbackTotal+1, afterFallbackTotal)
	require.Equal(t, beforeFallbackHit+1, afterFallbackHit)
}

func TestSetStickySessionAccountID_DualWriteOldEnabled(t *testing.T) {
	stats := &scheduler.StickyStats{}
	_, _, beforeDualWriteTotal := stats.Snapshot()

	cache := &stickyCacheFixture{sessionBindings: map[string]int64{}}
	svc := NewCompatible(CompatibleDependencies{Shared: Shared{
		Cache: cache}, StickyStats: stats}, Options{StickyTTL: time.Hour, ReadLegacySticky: false, WriteLegacySticky: true})

	ctx := requeststate.WithOpenAILegacySessionHash(context.Background(), "legacy-hash")

	err := svc.setStickySessionAccountID(ctx, nil, "new-hash", 9, openaiStickySessionTTL)
	require.NoError(t, err)
	require.Equal(t, int64(9), cache.sessionBindings["openai:new-hash"])
	require.Equal(t, int64(9), cache.sessionBindings["openai:legacy-hash"])

	_, _, afterDualWriteTotal := stats.Snapshot()
	require.Equal(t, beforeDualWriteTotal+1, afterDualWriteTotal)
}

func TestSetStickySessionAccountID_DualWriteOldDisabled(t *testing.T) {
	cache := &stickyCacheFixture{sessionBindings: map[string]int64{}}
	svc := NewCompatible(CompatibleDependencies{Shared: Shared{
		Cache: cache}, StickyStats: nil}, Options{StickyTTL: time.Hour, ReadLegacySticky: false, WriteLegacySticky: false})

	ctx := requeststate.WithOpenAILegacySessionHash(context.Background(), "legacy-hash")
	err := svc.setStickySessionAccountID(ctx, nil, "new-hash", 9, openaiStickySessionTTL)
	require.NoError(t, err)
	require.Equal(t, int64(9), cache.sessionBindings["openai:new-hash"])
	_, exists := cache.sessionBindings["openai:legacy-hash"]
	require.False(t, exists)
}

// stickyCacheFixture 保留原键空间和未命中错误，只实现本合同实际使用的四项能力。
type stickyCacheFixture struct {
	sessionBindings map[string]int64
	deletedSessions map[string]int
}

func (c *stickyCacheFixture) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	if id, ok := c.sessionBindings[key]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}
func (c *stickyCacheFixture) SetSessionAccountID(_ context.Context, _ int64, key string, id int64, _ time.Duration) error {
	if c.sessionBindings == nil {
		c.sessionBindings = map[string]int64{}
	}
	c.sessionBindings[key] = id
	return nil
}
func (*stickyCacheFixture) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (c *stickyCacheFixture) DeleteSessionAccountID(_ context.Context, _ int64, key string) error {
	if c.sessionBindings == nil {
		return nil
	}
	if c.deletedSessions == nil {
		c.deletedSessions = map[string]int{}
	}
	c.deletedSessions[key]++
	delete(c.sessionBindings, key)
	return nil
}
