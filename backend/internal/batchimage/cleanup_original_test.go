//go:build unit

package batchimage_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestBatchImageCleanupService_DeleteOutputsForOwner(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes completed output and returns public dto", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()

		got, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, "output_deleted", got.Status)
		require.NotNil(t, got.OutputDeletedAt)
		require.Equal(t, []batchimage.CleanupTarget{batchimage.CleanupTargetOutput}, provider.cleanupTargets)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].OutputDeletedAt)
		require.Equal(t, batchimage.BatchImageJobStatusOutputDeleted, repo.jobs["imgbatch_cleanup"].Status)
		body := mustBatchImageJSON(t, got)
		requireBatchImagePublicJSONHasNoInternals(t, body)
	})

	t.Run("repeated delete is idempotent", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		deletedAt := time.Now()
		repo.jobs["imgbatch_cleanup"].Status = batchimage.BatchImageJobStatusOutputDeleted
		repo.jobs["imgbatch_cleanup"].OutputDeletedAt = &deletedAt

		got, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, "output_deleted", got.Status)
		require.Empty(t, provider.cleanupTargets)
	})

	t.Run("not completed returns not ready", func(t *testing.T) {
		svc, repo, _ := newTestBatchImageCleanupService()
		repo.jobs["imgbatch_cleanup"].Status = batchimage.BatchImageJobStatusRunning

		_, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.ErrorIs(t, err, batchimage.ErrBatchImageOutputDeleteNotReady)
	})

	t.Run("non owner returns not found", func(t *testing.T) {
		svc, _, _ := newTestBatchImageCleanupService()
		_, err := svc.DeleteOutputsForOwner(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 999}, "imgbatch_cleanup")
		require.ErrorIs(t, err, batchimage.ErrBatchImageJobNotFound)
	})

	t.Run("provider not found is success", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = apperror.New(404, "PROVIDER_NOT_FOUND", "provider file not found: gs://hidden")

		got, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, "output_deleted", got.Status)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].OutputDeletedAt)
	})

	t.Run("provider transient error is sanitized and records failure", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = errors.New("temporary cleanup failed for gs://secret-output")

		_, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.ErrorIs(t, err, batchimage.ErrBatchImageProviderCleanupFailed)
		require.Equal(t, "BATCH_IMAGE_PROVIDER_CLEANUP_FAILED", apperror.Reason(err))
		require.NotContains(t, apperror.Message(err), "gs://")
		require.Equal(t, "BATCH_IMAGE_PROVIDER_CLEANUP_FAILED", batchimage.BatchImageDerefString(repo.jobs["imgbatch_cleanup"].LastErrorCode))
		require.Equal(t, "upstream provider operation failed", batchimage.BatchImageDerefString(repo.jobs["imgbatch_cleanup"].LastErrorMessage))
	})

	t.Run("unsafe cleanup path is not swallowed", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = batchimage.ErrBatchImageProviderUnsafeCleanupPath

		_, err := svc.DeleteOutputsForOwner(ctx, testBatchImageOwner(), "imgbatch_cleanup")
		require.ErrorIs(t, err, batchimage.ErrBatchImageCleanupUnsafePath)
		require.Equal(t, "BATCH_IMAGE_CLEANUP_UNSAFE_PATH", batchimage.BatchImageDerefString(repo.jobs["imgbatch_cleanup"].LastErrorCode))
	})
}

func TestBatchImageCleanupService_InputOutputAndWorker(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("input cleanup marks input only", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()

		err := svc.CleanupInput(ctx, "imgbatch_cleanup")
		require.NoError(t, err)
		require.Equal(t, []batchimage.CleanupTarget{batchimage.CleanupTargetInput}, provider.cleanupTargets)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].InputDeletedAt)
		require.Equal(t, batchimage.BatchImageJobStatusCompleted, repo.jobs["imgbatch_cleanup"].Status)

		err = svc.CleanupInput(ctx, "imgbatch_cleanup")
		require.NoError(t, err)
		require.Len(t, provider.cleanupTargets, 1)
	})

	t.Run("output cleanup for failed job keeps status", func(t *testing.T) {
		svc, repo, _ := newTestBatchImageCleanupService()
		repo.jobs["imgbatch_cleanup"].Status = batchimage.BatchImageJobStatusFailed

		err := svc.CleanupOutput(ctx, "imgbatch_cleanup", "ttl")
		require.NoError(t, err)
		require.Equal(t, batchimage.BatchImageJobStatusFailed, repo.jobs["imgbatch_cleanup"].Status)
		require.NotNil(t, repo.jobs["imgbatch_cleanup"].OutputDeletedAt)
	})

	t.Run("worker processes due jobs and continues after failure", func(t *testing.T) {
		svc, repo, provider := newTestBatchImageCleanupService()
		provider.cleanupErr = nil
		old := now.Add(-48 * time.Hour)
		expired := now.Add(-time.Minute)
		future := now.Add(time.Hour)
		repo.jobs["imgbatch_cleanup"].FinishedAt = &old
		repo.jobs["imgbatch_cleanup"].OutputExpiresAt = &expired
		repo.jobs["imgbatch_running"] = cleanupTestJob("imgbatch_running", batchimage.BatchImageJobStatusRunning)
		repo.jobs["imgbatch_running"].FinishedAt = &old
		repo.jobs["imgbatch_running"].OutputExpiresAt = &expired
		repo.jobs["imgbatch_future"] = cleanupTestJob("imgbatch_future", batchimage.BatchImageJobStatusCompleted)
		repo.jobs["imgbatch_future"].FinishedAt = &old
		repo.jobs["imgbatch_future"].OutputExpiresAt = &future

		result, err := svc.RunOnce(ctx, now)
		require.NoError(t, err)
		require.Equal(t, 2, result.InputCleaned)
		require.Equal(t, 1, result.OutputCleaned)
		require.Equal(t, batchimage.BatchImageJobStatusRunning, repo.jobs["imgbatch_running"].Status)
		require.Nil(t, repo.jobs["imgbatch_future"].OutputDeletedAt)
		require.NotContains(t, strings.Join(repo.events["imgbatch_running"], ","), "cleanup")
	})
}

func TestBatchImageDownloadAfterOutputDeletedReturnsGone(t *testing.T) {
	svc, repo, _ := newTestBatchImageDownloadService()
	now := time.Now()
	repo.jobs["imgbatch_download"].Status = batchimage.BatchImageJobStatusOutputDeleted
	repo.jobs["imgbatch_download"].OutputDeletedAt = &now

	stream, err := svc.OpenItemContent(context.Background(), testBatchImageOwner(), "imgbatch_download", "cover/../001", 0)
	require.Nil(t, stream)
	require.ErrorIs(t, err, batchimage.ErrBatchImageOutputDeleted)

	var out strings.Builder
	result, err := svc.StreamZip(context.Background(), testBatchImageOwner(), "imgbatch_download", batchimage.BatchImageZipOptions{}, &out)
	require.Nil(t, result)
	require.ErrorIs(t, err, batchimage.ErrBatchImageOutputDeleted)
}

func newTestBatchImageCleanupService() (*batchimage.Cleanup, *fakeBatchImageRepository, *publicBatchImageProvider) {
	repo := newFakeBatchImageRepository()
	repo.jobs["imgbatch_cleanup"] = cleanupTestJob("imgbatch_cleanup", batchimage.BatchImageJobStatusCompleted)
	provider := &publicBatchImageProvider{name: batchimage.BatchImageProviderGeminiAPI}
	accountID := int64(101)
	svc := newBatchCleanupFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](provider), &resultAccountFixture{account: &account.Record{ID: accountID, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey, Status: billingcore.StatusActive, Schedulable: true}}, &config.Config{BatchImage: config.BatchImageConfig{CleanupBatchSize: 10, InputRetentionAfterTerminalHours: 24}})
	return svc, repo, provider
}

func cleanupTestJob(batchID, status string) *batchimage.BatchImageJob {
	apiKeyID := int64(22)
	accountID := int64(101)
	now := time.Now().Add(-48 * time.Hour)
	return &batchimage.BatchImageJob{
		BatchID:           batchID,
		UserID:            11,
		APIKeyID:          &apiKeyID,
		AccountID:         &accountID,
		Provider:          batchimage.BatchImageProviderGeminiAPI,
		Model:             "gemini-2.5-flash-image",
		Status:            status,
		ProviderJobName:   batchimage.BatchImageStringPtr("providers/internal/job"),
		ProviderInputRef:  batchimage.BatchImageStringPtr("files/internal/input"),
		ProviderOutputRef: batchimage.BatchImageStringPtr("files/internal/output"),
		ItemCount:         1,
		SuccessCount:      1,
		CreatedAt:         now,
		UpdatedAt:         now,
		FinishedAt:        &now,
		SettledAt:         &now,
	}
}

func mustBatchImageJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
