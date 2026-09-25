package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sessionIsolationCacheStub struct {
	mu           sync.Mutex
	owners       map[string]int64
	ownerTTLs    map[string]time.Duration
	setCalls     int
	getCalls     int
	refreshCalls int
}

func (c *sessionIsolationCacheStub) ownerKey(userID int64, source, sessionHash string) string {
	return fmt.Sprintf("%d:%s:%s", userID, source, sessionHash)
}

func (c *sessionIsolationCacheStub) ownerGroupID(userID int64, source, sessionHash string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owners[c.ownerKey(userID, source, sessionHash)]
}

func (c *sessionIsolationCacheStub) ownerTTL(userID int64, source, sessionHash string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ownerTTLs[c.ownerKey(userID, source, sessionHash)]
}

func (c *sessionIsolationCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, errors.New("not found")
}

func (c *sessionIsolationCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (c *sessionIsolationCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (c *sessionIsolationCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (c *sessionIsolationCacheStub) SetSessionOwnerGroupID(_ context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls++
	if c.owners == nil {
		c.owners = make(map[string]int64)
	}
	if c.ownerTTLs == nil {
		c.ownerTTLs = make(map[string]time.Duration)
	}
	key := c.ownerKey(userID, source, sessionHash)
	if _, ok := c.owners[key]; ok {
		return false, nil
	}
	c.owners[key] = groupID
	c.ownerTTLs[key] = ttl
	return true, nil
}

func (c *sessionIsolationCacheStub) GetSessionOwnerGroupID(_ context.Context, userID int64, source, sessionHash string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	key := c.ownerKey(userID, source, sessionHash)
	if owner, ok := c.owners[key]; ok {
		return owner, nil
	}
	return 0, errors.New("not found")
}

func (c *sessionIsolationCacheStub) RefreshSessionOwnerTTL(_ context.Context, userID int64, source, sessionHash string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshCalls++
	if c.ownerTTLs == nil {
		c.ownerTTLs = make(map[string]time.Duration)
	}
	c.ownerTTLs[c.ownerKey(userID, source, sessionHash)] = ttl
	return nil
}

func TestEnsureSessionIsolation_FirstBindRecordsOwnerEvenWhenTargetNotIsolated(t *testing.T) {
	ctx := context.Background()
	cache := &sessionIsolationCacheStub{}
	ttl := 2 * time.Minute

	err := EnsureIsolation(ctx, cache, IsolationInput{GroupID: 11, Enabled: false, UserID: 7, Source: SessionIsolationSourceOpenAI, Hash: " session-a ", TTL: ttl})

	require.NoError(t, err)
	require.Equal(t, int64(11), cache.ownerGroupID(7, SessionIsolationSourceOpenAI, "session-a"))
	require.Equal(t, ttl, cache.ownerTTL(7, SessionIsolationSourceOpenAI, "session-a"))
	require.Equal(t, 1, cache.setCalls)
	require.Zero(t, cache.refreshCalls)
}

func TestEnsureSessionIsolation_SameOwnerRefreshesTTL(t *testing.T) {
	ctx := context.Background()
	cache := &sessionIsolationCacheStub{}
	initialTTL := time.Minute
	refreshTTL := 5 * time.Minute
	_, err := cache.SetSessionOwnerGroupID(ctx, 7, SessionIsolationSourceGateway, "session-a", 11, initialTTL)
	require.NoError(t, err)

	err = EnsureIsolation(ctx, cache, IsolationInput{GroupID: 11, Enabled: true, UserID: 7, Source: SessionIsolationSourceGateway, Hash: "session-a", TTL: refreshTTL})

	require.NoError(t, err)
	require.Equal(t, int64(11), cache.ownerGroupID(7, SessionIsolationSourceGateway, "session-a"))
	require.Equal(t, refreshTTL, cache.ownerTTL(7, SessionIsolationSourceGateway, "session-a"))
	require.Equal(t, 1, cache.refreshCalls)
}

func TestEnsureSessionIsolation_NonIsolatedTargetAllowsDifferentOwner(t *testing.T) {
	ctx := context.Background()
	cache := &sessionIsolationCacheStub{}
	_, err := cache.SetSessionOwnerGroupID(ctx, 7, SessionIsolationSourceGateway, "session-a", 11, time.Minute)
	require.NoError(t, err)

	err = EnsureIsolation(ctx, cache, IsolationInput{GroupID: 22, Enabled: false, UserID: 7, Source: SessionIsolationSourceGateway, Hash: "session-a", TTL: time.Minute})

	require.NoError(t, err)
	require.Equal(t, int64(11), cache.ownerGroupID(7, SessionIsolationSourceGateway, "session-a"))
	require.Zero(t, cache.refreshCalls)
}

func TestEnsureSessionIsolation_IsolatedTargetRejectsDifferentOwner(t *testing.T) {
	ctx := context.Background()
	cache := &sessionIsolationCacheStub{}
	_, err := cache.SetSessionOwnerGroupID(ctx, 7, SessionIsolationSourceGemini, "session-a", 11, time.Minute)
	require.NoError(t, err)

	err = EnsureIsolation(ctx, cache, IsolationInput{GroupID: 22, Enabled: true, UserID: 7, Source: SessionIsolationSourceGemini, Hash: "session-a", TTL: time.Minute})

	require.ErrorIs(t, err, ErrSessionIsolationConflict)
	require.Equal(t, int64(11), cache.ownerGroupID(7, SessionIsolationSourceGemini, "session-a"))
	require.Zero(t, cache.refreshCalls)
}

func TestEnsureSessionIsolation_ConcurrentFirstBindAllowsSingleOwner(t *testing.T) {
	ctx := context.Background()
	cache := &sessionIsolationCacheStub{}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, groupID := range []int64{11, 22} {
		groupID := groupID
		go func() {
			<-start
			results <- EnsureIsolation(ctx, cache, IsolationInput{GroupID: groupID, Enabled: true, UserID: 7, Source: SessionIsolationSourceOpenAI, Hash: "session-a", TTL: time.Minute})
		}()
	}

	close(start)
	errA := <-results
	errB := <-results

	successes := 0
	conflicts := 0
	for _, err := range []error{errA, errB} {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSessionIsolationConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	require.Contains(t, []int64{11, 22}, cache.ownerGroupID(7, SessionIsolationSourceOpenAI, "session-a"))
}
