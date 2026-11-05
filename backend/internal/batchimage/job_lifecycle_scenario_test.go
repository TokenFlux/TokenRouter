package batchimage_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// job_lifecycle_scenario_test.go follows one successful image and one failed
// item through public.go, processor.go, settlement.go, download.go and cleanup.go.
// Group and provider admission close after submission; persisted jobs retain
// their execution binding while shared in-memory records expose funding actions,
// public resource filtering, download bytes and repeated output deletion.

// The compile-time assertion keeps the smoke backend compatible with the
// provider operations used by this lifecycle scenario.
var _ batchimageprovider.BatchImageProvider = (*batchImageSmokeProvider)(nil)

// batchImageSmokeProvider supplies ordered upstream observations and records
// submission and cleanup calls.
type batchImageSmokeProvider struct {
	name           string                                  // registered smoke backend name
	states         []batchimage.BatchProviderInternalState // remaining provider observation sequence
	submits        []batchimage.BatchImageInput            // observed expanded submission inputs
	result         string                                  // provider result JSONL text
	cleanupTargets []batchimage.CleanupTarget              // observed resource deletion targets
}

// TestBatchImageMVPFlow submits a two-item batch, observes running and
// successful provider states, then settles its indexed outcomes after both
// group and provider admission close. It checks downloads and archive contents
// before repeated output deletion prevents both download paths.
func TestBatchImageMVPFlow(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBatchImageRepository()
	queue := &publicBatchImageQueue{}
	platform := &batchImageSmokeProvider{
		name: batchimage.BatchImageProviderGeminiAPI,
		states: []batchimage.BatchProviderInternalState{
			batchimage.BatchProviderStateRunning,
			batchimage.BatchProviderStateSucceeded,
		},
		result: batchImageSmokeResultJSONL(),
	}
	providerID := int64(101)
	providerRepo := &publicBatchImageProviderRepo{providers: []providercore.Record{testBatchImageProvider(providerID, capability.ProviderTypeAPIKey)}}
	cfg := &config.Config{BatchImage: config.BatchImageConfig{
		MaxItemsPerJob:                    10,
		MaxPromptBytesPerItem:             8000,
		DefaultResponseMimeType:           "image/png",
		DefaultImageSize:                  "1K",
		MaxDownloadItemsZip:               10,
		MaxDownloadDurationSeconds:        60,
		OutputRetentionAfterTerminalHours: 72,
	}}
	registry := batchimage.NewRegistry[batchimageprovider.BatchImageProvider](platform)
	billing := &fakeBatchImageBillingRepo{}
	pricing := &fakeBatchImagePricingResolver{unitPrice: 0.25}
	owner := testBatchImageOwner()

	publicSvc := newBatchPublicFixture(repo,
		providerRepo, nil, nil, nil, queue,
		registry,
		pricing,
		billing, nil, cfg)

	processor := &batchimage.PipelineProcessor{
		ProviderProcessor: newBatchProcessorFixture(repo,
			registry,
			&fakeBatchImageProviderResolver{provider: &providerRepo.providers[0]}, nil, billing, nil, 0),

		SettlementService: newBatchSettlementFixture(repo,
			billing, nil, pricing, nil, cfg),
	}
	downloadSvc := newBatchDownloadFixture(repo, registry, &fakeBatchImageProviderResolver{provider: &providerRepo.providers[0]}, &fakeBatchImageDownloadLimiter{}, cfg)
	cleanupSvc := newBatchCleanupFixture(repo, registry, &fakeBatchImageProviderResolver{provider: &providerRepo.providers[0]}, cfg)

	submitted, err := publicSvc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")
	require.NoError(t, err)
	require.Equal(t, "image.batch", submitted.Object)
	require.True(t, strings.HasPrefix(submitted.ID, "imgbatch_"))
	require.Equal(t, "queued", submitted.Status)
	require.Equal(t, 2, submitted.ItemCount)
	require.Equal(t, []string{submitted.ID}, queue.enqueued)
	require.Len(t, platform.submits, 1)
	require.Len(t, billing.reserves, 1)
	require.Equal(t, batchimage.BatchImageHoldRequestID(submitted.ID), billing.reserves[0].RequestID)
	require.InDelta(t, 0.3, billing.reserves[0].HoldAmount, 1e-12)
	requireBatchImagePublicJSONHasNoInternals(t, mustMarshalBatchImageSmokeJSON(t, submitted))

	// Close both creation permissions after storing the upstream identity.
	// The remaining lifecycle must use that binding for processing, settlement,
	// downloads and cleanup while another submission is refused before reserving funds.
	group, err := publicSvc.GroupRepo.GetByIDLite(ctx, *owner.GroupID)
	require.NoError(t, err)
	group.AllowBatchImageGeneration = false
	providerRepo.providers[0].Credentials["upstream_protocols"] = []string{}
	_, err = publicSvc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")
	require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
	require.Len(t, billing.reserves, 1)
	require.Len(t, platform.submits, 1)

	firstProcess, err := processor.Process(ctx, submitted.ID)
	require.NoError(t, err)
	require.False(t, firstProcess.Terminal)
	require.Equal(t, batchimage.BatchImageJobStatusRunning, repo.jobs[submitted.ID].Status)

	indexProcess, err := processor.Process(ctx, submitted.ID)
	require.NoError(t, err)
	require.False(t, indexProcess.Terminal)
	require.Equal(t, time.Millisecond, indexProcess.RequeueAfter)
	require.Equal(t, batchimage.BatchImageJobStatusSettling, repo.jobs[submitted.ID].Status)
	require.Equal(t, batchimage.BatchImageCounts{SuccessCount: 1, FailCount: 1}, repo.counts[submitted.ID])

	settleProcess, err := processor.Process(ctx, submitted.ID)
	require.NoError(t, err)
	require.True(t, settleProcess.Terminal)
	job := repo.jobs[submitted.ID]
	require.Equal(t, batchimage.BatchImageJobStatusCompleted, job.Status)
	require.NotNil(t, job.OutputExpiresAt)
	require.Equal(t, 1, job.SuccessCount)
	require.Equal(t, 1, job.FailCount)
	require.Len(t, billing.captures, 1)
	require.Equal(t, batchimage.BatchImageCaptureRequestID(submitted.ID), billing.captures[0].RequestID)
	require.InDelta(t, 0.3, billing.captures[0].HoldAmount, 1e-12)
	require.InDelta(t, 0.125, billing.captures[0].ActualAmount, 1e-12)

	secondSettlement, err := processor.SettlementService.Settle(ctx, submitted.ID)
	require.NoError(t, err)
	require.True(t, secondSettlement.AlreadySettled)
	require.Len(t, billing.captures, 1)

	status, err := publicSvc.Get(ctx, owner, submitted.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", status.Status)
	require.Equal(t, 1, status.SuccessCount)
	require.Equal(t, 1, status.FailCount)
	require.NotNil(t, status.ActualCost)
	requireBatchImagePublicJSONHasNoInternals(t, mustMarshalBatchImageSmokeJSON(t, status))

	items, err := publicSvc.ListItems(ctx, owner, submitted.ID, batchimage.BatchImageItemsQuery{Limit: 100})
	require.NoError(t, err)
	require.False(t, items.HasMore)
	require.Len(t, items.Data, 2)
	require.Equal(t, "cover_001", items.Data[0].CustomID)
	require.Equal(t, "succeeded", items.Data[0].Status)
	require.Equal(t, "cover_002", items.Data[1].CustomID)
	require.Equal(t, "failed", items.Data[1].Status)
	require.NotNil(t, items.Data[1].Error)
	require.Nil(t, repo.items[submitted.ID][1].BilledAmount)
	requireBatchImagePublicJSONHasNoInternals(t, mustMarshalBatchImageSmokeJSON(t, items))

	stream, err := downloadSvc.OpenItemContent(ctx, owner, submitted.ID, "cover_001", 0)
	require.NoError(t, err)
	body, err := io.ReadAll(stream.Reader)
	require.NoError(t, err)
	require.NoError(t, stream.Reader.Close())
	require.Equal(t, []byte("smoke-png"), body)
	require.Equal(t, "image/png", stream.ContentType)
	require.Equal(t, "cover_001.png", stream.Filename)

	var zipBuf bytes.Buffer
	zipResult, err := downloadSvc.StreamZip(ctx, owner, submitted.ID, batchimage.BatchImageZipOptions{}, &zipBuf)
	require.NoError(t, err)
	require.Equal(t, 1, zipResult.FileCount)
	require.Equal(t, 1, zipResult.ErrorCount)
	zipFiles := readZipFiles(t, zipBuf.Bytes())
	require.Equal(t, []byte("smoke-png"), zipFiles["images/cover_001.png"])
	require.Contains(t, zipFiles, "manifest.json")
	require.Contains(t, zipFiles, "errors.json")
	requireBatchImagePublicJSONHasNoInternals(t, string(bytes.Join(mapValues(zipFiles), []byte("\n"))))

	zipReader, err := zip.NewReader(bytes.NewReader(zipBuf.Bytes()), int64(zipBuf.Len()))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"images/cover_001.png", "manifest.json", "errors.json"}, batchImageSmokeZipNames(zipReader))

	deleted, err := cleanupSvc.DeleteOutputsForOwner(ctx, owner, submitted.ID)
	require.NoError(t, err)
	require.Equal(t, "output_deleted", deleted.Status)
	require.Equal(t, []batchimage.CleanupTarget{batchimage.CleanupTargetOutput}, platform.cleanupTargets)
	requireBatchImagePublicJSONHasNoInternals(t, mustMarshalBatchImageSmokeJSON(t, deleted))

	deletedAgain, err := cleanupSvc.DeleteOutputsForOwner(ctx, owner, submitted.ID)
	require.NoError(t, err)
	require.Equal(t, "output_deleted", deletedAgain.Status)
	require.Equal(t, []batchimage.CleanupTarget{batchimage.CleanupTargetOutput}, platform.cleanupTargets)

	stream, err = downloadSvc.OpenItemContent(ctx, owner, submitted.ID, "cover_001", 0)
	require.Nil(t, stream)
	require.ErrorIs(t, err, batchimage.ErrBatchImageOutputDeleted)
	var afterDelete bytes.Buffer
	zipResult, err = downloadSvc.StreamZip(ctx, owner, submitted.ID, batchimage.BatchImageZipOptions{}, &afterDelete)
	require.Nil(t, zipResult)
	require.ErrorIs(t, err, batchimage.ErrBatchImageOutputDeleted)
	require.Empty(t, afterDelete.Bytes())
}

// batchImageSmokeResultJSONL returns one image and one safety failure for the
// smoke task.
func batchImageSmokeResultJSONL() string {
	return `{"key":"cover_001","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"c21va2UtcG5n"}}]}}]}}` + "\n" + `{"key":"cover_002","status":{"code":3,"message":"blocked by safety policy"}}` + "\n"
}

// mustMarshalBatchImageSmokeJSON encodes a public smoke value and fails its
// calling test when serialization fails. The returned text is screened for
// internal provider references by shared public-response assertions.
func mustMarshalBatchImageSmokeJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return string(body)
}

// batchImageSmokeZipNames lists archive entry names in their stored ZIP
// order.
func batchImageSmokeZipNames(reader *zip.Reader) []string {
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}

// Name returns the registered backend label used by the smoke scenario.
func (p *batchImageSmokeProvider) Name() string { return p.name }

// SupportsSubmission reuses the fixture credential rules for new jobs.
// Tests inject provider protocol selections through candidate preparation, while
// this adapter supplies deterministic local execution without deployment storage.
func (p *batchImageSmokeProvider) SupportsSubmission(provider *providercore.Record) bool {
	return p.SupportsProvider(provider)
}

// SupportsProvider accepts a present schedulable provider for the smoke
// backend.
func (p *batchImageSmokeProvider) SupportsProvider(provider *providercore.Record) bool {
	return provider != nil && provider.IsSchedulable()
}

// Submit records the expanded smoke input and returns fixed internal job and
// file references. The caller saves those references before polling, indexing
// and cleanup can inspect the shared smoke task.
func (p *batchImageSmokeProvider) Submit(_ context.Context, _ *batchimage.BatchImageJob, _ *providercore.Record, input batchimage.BatchImageInput) (*batchimage.BatchProviderJob, error) {
	p.submits = append(p.submits, input)
	return &batchimage.BatchProviderJob{
		ProviderJobName:   "providers/fake-platform-job/raw-id",
		ProviderInputRef:  "files/fake-platform-job/input.jsonl",
		ProviderOutputRef: "files/fake-platform-job/output.jsonl",
	}, nil
}

// Get consumes the next configured observation or reports success after the
// sequence is empty. Its raw state is uppercase and its output reference
// remains fixed for later indexing and download.
func (p *batchImageSmokeProvider) Get(context.Context, *batchimage.BatchImageJob, *providercore.Record) (*batchimage.BatchProviderStatus, error) {
	state := batchimage.BatchProviderStateSucceeded
	if len(p.states) > 0 {
		state = p.states[0]
		p.states = p.states[1:]
	}
	return &batchimage.BatchProviderStatus{
		RawState:          strings.ToUpper(string(state)),
		InternalState:     state,
		Done:              state == batchimage.BatchProviderStateSucceeded,
		ProviderOutputRef: "files/fake-platform-job/output.jsonl",
	}, nil
}

// Cancel accepts upstream cancellation requests made against the smoke task
// fixture.
func (p *batchImageSmokeProvider) Cancel(context.Context, *batchimage.BatchImageJob, *providercore.Record) error {
	return nil
}

// OpenResult returns a closable reader over the retained smoke JSONL and its
// content type. Indexing and download consumers own closing their reader,
// while the fixture retains its source string.
func (p *batchImageSmokeProvider) OpenResult(context.Context, *batchimage.BatchImageJob, *providercore.Record) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(p.result)), "application/jsonl", nil
}

// Cleanup records the resource target requested by the smoke cleanup service.
func (p *batchImageSmokeProvider) Cleanup(_ context.Context, _ *batchimage.BatchImageJob, _ *providercore.Record, target batchimage.CleanupTarget) error {
	p.cleanupTargets = append(p.cleanupTargets, target)
	return nil
}
