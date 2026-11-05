package apikey_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// TestAPIKeyService_InvalidateAuthCacheByUserID enumerates two credentials for
// an owner with shared caching enabled. It checks that invalidation records
// two attempts through the fixture's shared deletion operation.
func TestAPIKeyService_InvalidateAuthCacheByUserID(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByUserID: func(ctx context.Context, userID int64) ([]string, error) {
			return []string{"k1", "k2"}, nil
		},
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{
		L2TTLSeconds:       60,
		NegativeTTLSeconds: 30,
	}}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	svc.InvalidateAuthCacheByUserID(context.Background(), 7)
	require.Len(t, cache.deleteAuthKeys, 2)
}

// TestAPIKeyService_InvalidateAuthCacheByGroupID enumerates two credentials for
// a group through the repository fixture. It checks that both supplied
// credentials produce deletion attempts through the configured shared cache
// adapter.
func TestAPIKeyService_InvalidateAuthCacheByGroupID(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByGroupID: func(ctx context.Context, groupID int64) ([]string, error) {
			return []string{"k1", "k2"}, nil
		},
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	svc.InvalidateAuthCacheByGroupID(context.Background(), 9)
	require.Len(t, cache.deleteAuthKeys, 2)
}

// TestAPIKeyService_InvalidateAuthCacheByKey supplies one credential to a
// service with shared caching enabled. It checks that direct invalidation
// records one deletion attempt through the fixture's shared-cache operation.
func TestAPIKeyService_InvalidateAuthCacheByKey(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByUserID: func(ctx context.Context, userID int64) ([]string, error) {
			return nil, nil
		},
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	svc.InvalidateAuthCacheByKey(context.Background(), "k1")
	require.Len(t, cache.deleteAuthKeys, 1)
}
