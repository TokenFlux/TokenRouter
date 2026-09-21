//go:build unit

package batchimage_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBatchImageSettlementOutputExpiration(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_expire")
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo,
		billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, &config.Config{BatchImage: config.BatchImageConfig{OutputRetentionAfterTerminalHours: 5}})

	_, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.NotNil(t, repo.jobs[job.BatchID].OutputExpiresAt)
	require.WithinDuration(t, time.Now().Add(5*time.Hour), *repo.jobs[job.BatchID].OutputExpiresAt, time.Minute)

	existing := time.Now().Add(time.Hour)
	second := testSettlingBatchImageJob("imgbatch_keep_expire")
	second.OutputExpiresAt = &existing
	repo.jobs[second.BatchID] = second
	_, err = svc.Settle(context.Background(), second.BatchID)
	require.NoError(t, err)
	require.Equal(t, existing, *repo.jobs[second.BatchID].OutputExpiresAt)
}
