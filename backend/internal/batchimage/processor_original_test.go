//go:build unit

package batchimage_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/stretchr/testify/require"
)

const batchImageTestData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJ"

func TestParseBatchImageResultLine_SuccessShapes(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantID    string
		wantMime  string
		wantExt   string
		wantCount int
	}{
		{
			name:   "gemini_inlineData",
			line:   `{"key":"cover_001","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + batchImageTestData + `"}}]}}]}}`,
			wantID: "cover_001", wantMime: "image/png", wantExt: "png", wantCount: 1,
		},
		{
			name:   "snake_case_inline_data",
			line:   `{"custom_id":"cover_002","response":{"candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/jpeg","data":"` + batchImageTestData + `"}}]}}]}}`,
			wantID: "cover_002", wantMime: "image/jpeg", wantExt: "jpg", wantCount: 1,
		},
		{
			name:   "vertex_top_level_response",
			line:   `{"customId":"cover_003","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/webp","data":"` + batchImageTestData + `"}}]}}]}}`,
			wantID: "cover_003", wantMime: "image/webp", wantExt: "webp", wantCount: 1,
		},
		{
			name:   "top_level_candidates",
			line:   `{"request":{"key":"cover_004"},"candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/png","data":"` + batchImageTestData + `"}},{"inlineData":{"mimeType":"image/png","data":"` + batchImageTestData + `"}}]}}]}`,
			wantID: "cover_004", wantMime: "image/png", wantExt: "png", wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := batchimage.ParseBatchImageResultLine([]byte(tt.line), 7)
			require.NoError(t, err)
			require.Equal(t, tt.wantID, got.CustomID)
			require.Equal(t, batchimage.BatchImageParsedStatusSucceeded, got.Status)
			require.Equal(t, tt.wantMime, got.MimeType)
			require.Equal(t, tt.wantExt, got.FileExtension)
			require.Equal(t, tt.wantCount, got.ImageCount)
			require.Equal(t, 7, got.SourceLineNumber)
			require.NotContains(t, fmt.Sprintf("%+v", got), batchImageTestData)
		})
	}
}

func TestParseBatchImageResultLine_FailureShapes(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantCode string
	}{
		{name: "status_row", line: `{"key":"cover_001","status":{"code":3,"message":"invalid argument: bad prompt"}}`, wantCode: "INVALID_ARGUMENT"},
		{name: "error_row", line: `{"key":"cover_002","error":{"code":"SAFETY","message":"blocked by safety policy"}}`, wantCode: "SAFETY_BLOCKED"},
		{name: "quota_row", line: `{"key":"cover_003","error":{"code":"RESOURCE_EXHAUSTED","message":"quota exceeded"}}`, wantCode: "PROVIDER_RATE_LIMITED"},
		{name: "empty_image_output", line: `{"key":"cover_004","response":{"candidates":[{"content":{"parts":[{"text":"no image"}]}}]}}`, wantCode: "EMPTY_IMAGE_OUTPUT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := batchimage.ParseBatchImageResultLine([]byte(tt.line), 1)
			require.NoError(t, err)
			require.Equal(t, batchimage.BatchImageParsedStatusFailed, got.Status)
			require.Equal(t, tt.wantCode, got.ErrorCode)
		})
	}
}

func TestParseBatchImageResultLine_RejectsMissingCustomIDAndDoesNotLeakData(t *testing.T) {
	_, err := batchimage.ParseBatchImageResultLine([]byte(`{"response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"`+batchImageTestData+`"}}]}}]}}`), 3)
	require.ErrorIs(t, err, batchimage.ErrBatchImageIndexParseFailed)
	require.NotContains(t, err.Error(), batchImageTestData)
}

func TestBatchImageResultIndexer_WritesCountsAndReplacesItems(t *testing.T) {
	output := strings.Join([]string{
		`{"key":"ok","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + batchImageTestData + `"}}]}}]}}`,
		`{"key":"bad","error":{"code":"SAFETY","message":"blocked by safety policy"}}`,
	}, "\n") + "\n"
	repo := newFakeBatchImageRepository()
	outputRef := "files/output"
	job := &batchimage.BatchImageJob{BatchID: "imgbatch_index", ProviderOutputRef: &outputRef}
	provider := &fakeProcessorProvider{result: output}

	result, err := (&batchimage.ResultIndexer{Repo: repo, Observe: resultObserve}).Index(context.Background(), job, batchimageprovider.BindAccount(provider, accountcore.CloneRecord(&accountcore.Record{})))
	require.NoError(t, err)
	require.True(t, provider.openResultCalled)
	require.Equal(t, 1, result.SuccessCount)
	require.Equal(t, 1, result.FailCount)
	require.Equal(t, 2, result.TotalCount)
	require.Equal(t, 1, repo.replaceCalls)
	require.Len(t, repo.items[job.BatchID], 2)
	require.Equal(t, batchimage.BatchImageItemStatusSuccess, repo.items[job.BatchID][0].Status)
	require.Equal(t, batchimage.BatchImageItemStatusFailed, repo.items[job.BatchID][1].Status)
	require.Equal(t, batchimage.BatchImageCounts{SuccessCount: 1, FailCount: 1}, repo.counts[job.BatchID])
	require.NotContains(t, fmt.Sprintf("%+v", repo.items[job.BatchID]), batchImageTestData)

	// 重新索引时与现有 custom_id 集对账：未知的 "ok2" 被丢弃，
	// 输出中缺失的 ok/bad 补为 PROVIDER_RESULT_MISSING 失败记录。
	provider.result = `{"key":"ok2","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/webp","data":"` + batchImageTestData + `"}}]}}]}}` + "\n"
	result, err = (&batchimage.ResultIndexer{Repo: repo, Observe: resultObserve}).Index(context.Background(), job, batchimageprovider.BindAccount(provider, accountcore.CloneRecord(&accountcore.Record{})))
	require.NoError(t, err)
	require.Equal(t, 2, result.TotalCount)
	require.Equal(t, 0, result.SuccessCount)
	require.Equal(t, 2, result.FailCount)
	require.Len(t, repo.items[job.BatchID], 2)
	gotIDs := []string{repo.items[job.BatchID][0].CustomID, repo.items[job.BatchID][1].CustomID}
	require.ElementsMatch(t, []string{"ok", "bad"}, gotIDs)
	for _, item := range repo.items[job.BatchID] {
		require.Equal(t, batchimage.BatchImageItemStatusFailed, item.Status)
		require.Equal(t, "PROVIDER_RESULT_MISSING", batchimage.BatchImageDerefString(item.ErrorCode))
	}
}

func TestBatchImageResultIndexer_ReconcilesMissingAndUnknownCustomIDs(t *testing.T) {
	repo := newFakeBatchImageRepository()
	outputRef := "files/output"
	job := &batchimage.BatchImageJob{BatchID: "imgbatch_reconcile", ProviderOutputRef: &outputRef, ItemCount: 3}
	// 预创建提交时的 pending 条目（提交流程的行为）。
	require.NoError(t, repo.BulkCreateBatchImageItems(context.Background(), []batchimage.CreateBatchImageItemParams{
		{JobID: job.BatchID, CustomID: "a", Status: batchimage.BatchImageItemStatusPending},
		{JobID: job.BatchID, CustomID: "b", Status: batchimage.BatchImageItemStatusPending},
		{JobID: job.BatchID, CustomID: "c", Status: batchimage.BatchImageItemStatusPending},
	}))
	// provider 输出：a 成功，b 失败，c 漏掉，多出未知的 x。
	output := strings.Join([]string{
		`{"key":"a","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + batchImageTestData + `"}}]}}]}}`,
		`{"key":"b","error":{"code":"SAFETY","message":"blocked"}}`,
		`{"key":"x","error":{"code":"UNKNOWN","message":"not ours"}}`,
	}, "\n") + "\n"
	provider := &fakeProcessorProvider{result: output}

	result, err := (&batchimage.ResultIndexer{Repo: repo, Observe: resultObserve}).Index(context.Background(), job, batchimageprovider.BindAccount(provider, accountcore.CloneRecord(&accountcore.Record{})))
	require.NoError(t, err)
	require.Equal(t, 3, result.TotalCount)
	require.Equal(t, 1, result.SuccessCount)
	require.Equal(t, 2, result.FailCount)
	require.Len(t, repo.items[job.BatchID], 3)
	byID := make(map[string]batchimage.CreateBatchImageItemParams)
	for _, item := range repo.items[job.BatchID] {
		byID[item.CustomID] = item
	}
	require.NotContains(t, byID, "x")
	require.Equal(t, batchimage.BatchImageItemStatusSuccess, byID["a"].Status)
	require.Equal(t, batchimage.BatchImageItemStatusFailed, byID["b"].Status)
	require.Equal(t, batchimage.BatchImageItemStatusFailed, byID["c"].Status)
	require.Equal(t, "PROVIDER_RESULT_MISSING", batchimage.BatchImageDerefString(byID["c"].ErrorCode))
	// 对账后 success+fail == item_count，结算计数校验可通过。
	require.Equal(t, job.ItemCount, result.SuccessCount+result.FailCount)
}

func TestBatchImageResultIndexer_EmptyInvalidAndDuplicateOutput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "empty", body: "\n", want: batchimage.ErrBatchImageIndexNoResultLines},
		{name: "invalid_json", body: "{bad-json}\n", want: batchimage.ErrBatchImageIndexParseFailed},
		{name: "duplicate_custom_id", body: `{"key":"dup","error":{"message":"one"}}` + "\n" + `{"key":"dup","error":{"message":"two"}}` + "\n", want: batchimage.ErrBatchImageDuplicateCustomID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeBatchImageRepository()
			_, err := (&batchimage.ResultIndexer{Repo: repo, Observe: resultObserve}).Index(context.Background(), &batchimage.BatchImageJob{BatchID: "imgbatch_bad"}, batchimageprovider.BindAccount(&fakeProcessorProvider{result: tt.body}, accountcore.CloneRecord(&accountcore.Record{})))
			require.ErrorIs(t, err, tt.want)
			require.Empty(t, repo.items["imgbatch_bad"])
		})
	}
}

func TestBatchImageProviderProcessor_ValidationAndTerminalCases(t *testing.T) {
	ctx := context.Background()
	accountID := int64(10)
	providerJob := "providers/job"

	t.Run("terminal job returns without provider call", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_done"] = &batchimage.BatchImageJob{BatchID: "imgbatch_done", Status: batchimage.BatchImageJobStatusFailed}
		provider := &fakeProcessorProvider{}
		got, err := (newBatchProcessorFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](provider), &fakeBatchImageAccountResolver{account: &accountcore.Record{}}, nil, nil, nil, 0)).Process(ctx, "imgbatch_done")
		require.NoError(t, err)
		require.True(t, got.Terminal)
		require.False(t, provider.getCalled)
	})

	t.Run("missing provider", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_missing_provider"] = &batchimage.BatchImageJob{BatchID: "imgbatch_missing_provider", Status: batchimage.BatchImageJobStatusSubmitted, Provider: "missing", AccountID: &accountID, ProviderJobName: &providerJob}
		_, err := (newBatchProcessorFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](), &fakeBatchImageAccountResolver{account: &accountcore.Record{}}, nil, nil, nil, 0)).Process(ctx, "imgbatch_missing_provider")
		require.ErrorIs(t, err, batchimage.ErrBatchImageUnsupportedProvider)
	})

	t.Run("missing account id", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_missing_account"] = &batchimage.BatchImageJob{BatchID: "imgbatch_missing_account", Status: batchimage.BatchImageJobStatusSubmitted, Provider: "fake", ProviderJobName: &providerJob}
		_, err := (newBatchProcessorFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](&fakeProcessorProvider{}), &fakeBatchImageAccountResolver{account: &accountcore.Record{}}, nil, nil, nil, 0)).Process(ctx, "imgbatch_missing_account")
		require.ErrorIs(t, err, batchimage.ErrBatchImageMissingAccountID)
	})

	t.Run("missing provider job name", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_missing_name"] = &batchimage.BatchImageJob{BatchID: "imgbatch_missing_name", Status: batchimage.BatchImageJobStatusSubmitted, Provider: "fake", AccountID: &accountID}
		_, err := (newBatchProcessorFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](&fakeProcessorProvider{}), &fakeBatchImageAccountResolver{account: &accountcore.Record{}}, nil, nil, nil, 0)).Process(ctx, "imgbatch_missing_name")
		require.ErrorIs(t, err, batchimage.ErrBatchImageMissingProviderJobName)
	})
}

func TestBatchImageProviderProcessor_StatusFlow(t *testing.T) {
	ctx := context.Background()
	accountID := int64(10)
	providerJob := "providers/job"
	newJob := func(status string) *batchimage.BatchImageJob {
		return &batchimage.BatchImageJob{BatchID: "imgbatch_flow", Status: status, Provider: "fake", AccountID: &accountID, ProviderJobName: &providerJob}
	}

	t.Run("running status updates and requeues", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_flow"] = newJob(batchimage.BatchImageJobStatusSubmitted)
		provider := &fakeProcessorProvider{status: &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateRunning, RawState: "RUNNING", SuggestedRequeueAfter: 12 * time.Second}}
		got, err := newTestBatchImageProcessor(repo, provider).Process(ctx, "imgbatch_flow")
		require.NoError(t, err)
		require.False(t, got.Terminal)
		require.Equal(t, 12*time.Second, got.RequeueAfter)
		require.Equal(t, batchimage.BatchImageJobStatusRunning, repo.jobs["imgbatch_flow"].Status)
	})

	t.Run("queued status requeues", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_flow"] = newJob(batchimage.BatchImageJobStatusSubmitted)
		provider := &fakeProcessorProvider{status: &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateQueued}}
		got, err := newTestBatchImageProcessor(repo, provider).Process(ctx, "imgbatch_flow")
		require.NoError(t, err)
		require.False(t, got.Terminal)
		require.Equal(t, batchimage.DefaultBatchImageProcessorRequeue, got.RequeueAfter)
		require.Equal(t, batchimage.BatchImageJobStatusSubmitted, repo.jobs["imgbatch_flow"].Status)
	})

	t.Run("transient provider get error requeues", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_flow"] = newJob(batchimage.BatchImageJobStatusSubmitted)
		provider := &fakeProcessorProvider{getErr: errors.New("temporary upstream failure")}
		got, err := newTestBatchImageProcessor(repo, provider).Process(ctx, "imgbatch_flow")
		require.NoError(t, err)
		require.False(t, got.Terminal)
		require.Equal(t, time.Minute, got.RequeueAfter)
	})

	t.Run("succeeded indexes and settles from submitted", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_flow"] = newJob(batchimage.BatchImageJobStatusSubmitted)
		provider := &fakeProcessorProvider{
			status: &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateSucceeded, RawState: "SUCCEEDED", ProviderOutputRef: "files/output"},
			result: `{"key":"ok","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + batchImageTestData + `"}}]}}]}}` + "\n",
		}
		got, err := newTestBatchImageProcessor(repo, provider).Process(ctx, "imgbatch_flow")
		require.NoError(t, err)
		require.False(t, got.Terminal)
		require.Equal(t, time.Millisecond, got.RequeueAfter)
		require.Equal(t, batchimage.BatchImageJobStatusSettling, repo.jobs["imgbatch_flow"].Status)
		require.Equal(t, "files/output", batchimage.BatchImageDerefString(repo.jobs["imgbatch_flow"].ProviderOutputRef))
		require.Equal(t, []string{batchimage.BatchImageJobStatusIndexing, batchimage.BatchImageJobStatusSettling}, repo.transitions["imgbatch_flow"])
		require.Equal(t, batchimage.BatchImageCounts{SuccessCount: 1}, repo.counts["imgbatch_flow"])
	})

	t.Run("failed provider marks job failed", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_flow"] = newJob(batchimage.BatchImageJobStatusRunning)
		provider := &fakeProcessorProvider{status: &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateFailed, RawState: "FAILED", ErrorCode: "BAD_PROMPT", ErrorMessage: "bad prompt"}}
		got, err := newTestBatchImageProcessor(repo, provider).Process(ctx, "imgbatch_flow")
		require.NoError(t, err)
		require.True(t, got.Terminal)
		require.Equal(t, batchimage.BatchImageJobStatusFailed, repo.jobs["imgbatch_flow"].Status)
		require.Equal(t, "BAD_PROMPT", batchimage.BatchImageDerefString(repo.jobs["imgbatch_flow"].LastErrorCode))
	})

	t.Run("cancelled provider marks job cancelled", func(t *testing.T) {
		repo := newFakeBatchImageRepository()
		repo.jobs["imgbatch_flow"] = newJob(batchimage.BatchImageJobStatusRunning)
		apiKeyID := int64(22)
		holdAmount := 0.5
		repo.jobs["imgbatch_flow"].UserID = 11
		repo.jobs["imgbatch_flow"].APIKeyID = &apiKeyID
		repo.jobs["imgbatch_flow"].EstimatedCost = holdAmount
		repo.jobs["imgbatch_flow"].HoldAmount = &holdAmount
		provider := &fakeProcessorProvider{status: &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateCancelled, RawState: "CANCELLED"}}
		processor := newTestBatchImageProcessor(repo, provider)
		billing := &fakeBatchImageBillingRepo{}
		processor.Funding = nativeTaskFundingFixture(billing)
		got, err := processor.Process(ctx, "imgbatch_flow")
		require.NoError(t, err)
		require.True(t, got.Terminal)
		require.Equal(t, batchimage.BatchImageJobStatusCancelled, repo.jobs["imgbatch_flow"].Status)
		require.Len(t, billing.releases, 1)
		require.Equal(t, batchimage.BatchImageReleaseRequestID("imgbatch_flow"), billing.releases[0].RequestID)
	})
}

func TestCanTransitionBatchImageJob_PR5DirectIndexing(t *testing.T) {
	require.True(t, batchimage.CanTransitionBatchImageJob(batchimage.BatchImageJobStatusSubmitted, batchimage.BatchImageJobStatusIndexing))
	require.True(t, batchimage.CanTransitionBatchImageJob(batchimage.BatchImageJobStatusSubmitted, batchimage.BatchImageJobStatusFailed))
	require.True(t, batchimage.CanTransitionBatchImageJob(batchimage.BatchImageJobStatusIndexing, batchimage.BatchImageJobStatusFailed))
}

func newTestBatchImageProcessor(repo *fakeBatchImageRepository, provider *fakeProcessorProvider) *batchimage.ProviderProcessor {
	return newBatchProcessorFixture(repo,
		batchimage.NewRegistry[batchimageprovider.BatchImageProvider](provider),
		&fakeBatchImageAccountResolver{account: &accountcore.Record{}},
		&batchimage.ResultIndexer{Repo: repo, Observe: resultObserve}, nil, nil, 0)

}

type fakeBatchImageAccountResolver struct {
	account *accountcore.Record
	err     error
}

func (r *fakeBatchImageAccountResolver) GetByID(context.Context, int64) (*accountcore.Record, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.account, nil
}

type fakeProcessorProvider struct {
	status *batchimage.BatchProviderStatus
	getErr error
	result string

	getCalled        bool
	openResultCalled bool
}

func (p *fakeProcessorProvider) Name() string { return "fake" }
func (p *fakeProcessorProvider) SupportsAccount(*accountcore.Record) bool {
	return true
}
func (p *fakeProcessorProvider) Submit(context.Context, *batchimage.BatchImageJob, *accountcore.Record, batchimage.BatchImageInput) (*batchimage.BatchProviderJob, error) {
	panic("Submit must not be called by PR5 processor")
}
func (p *fakeProcessorProvider) Get(context.Context, *batchimage.BatchImageJob, *accountcore.Record) (*batchimage.BatchProviderStatus, error) {
	p.getCalled = true
	if p.getErr != nil {
		return nil, p.getErr
	}
	if p.status == nil {
		return &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateQueued}, nil
	}
	return p.status, nil
}
func (p *fakeProcessorProvider) Cancel(context.Context, *batchimage.BatchImageJob, *accountcore.Record) error {
	return nil
}
func (p *fakeProcessorProvider) OpenResult(context.Context, *batchimage.BatchImageJob, *accountcore.Record) (io.ReadCloser, string, error) {
	p.openResultCalled = true
	return io.NopCloser(strings.NewReader(p.result)), "application/jsonl", nil
}
func (p *fakeProcessorProvider) Cleanup(context.Context, *batchimage.BatchImageJob, *accountcore.Record, batchimage.CleanupTarget) error {
	return nil
}

func resultObserve(event string, values ...any) {
	logging.LegacyPrintf("service.batch_image", "%s %v", event, values)
}
