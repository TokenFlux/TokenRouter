package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type marketplaceRecentRequestRepoSpy struct {
	limitPerModel int
}

func (s *marketplaceRecentRequestRepoSpy) ListRecentRequestStatusesByGroupModels(ctx context.Context, pairs []ModelMarketplaceRecentRequestPair, limitPerModel int) (map[int64]map[string][]ModelMarketplaceRecentRequest, error) {
	s.limitPerModel = limitPerModel
	return map[int64]map[string][]ModelMarketplaceRecentRequest{}, nil
}

func TestModelMarketplaceRecentRequestWindowCoversDetailStatusBar(t *testing.T) {
	repo := &marketplaceRecentRequestRepoSpy{}
	svc := &ModelMarketplaceService{recentRequestRepo: repo}

	_ = svc.getPublicRecentRequestMap(context.Background(), []ModelMarketplaceRecentRequestPair{{GroupID: 1, ModelID: "gpt-5.5"}})

	require.Equal(t, marketplaceRecentRequestStatusBarLimit, repo.limitPerModel)
	require.GreaterOrEqual(t, repo.limitPerModel, 24)
	require.LessOrEqual(t, repo.limitPerModel, 200)
}
