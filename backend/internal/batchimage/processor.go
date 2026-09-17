// 批量处理只推进当前任务的查询、索引和状态，供应商执行通过绑定端口提供。
package batchimage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type BoundProvider interface {
	Submit(context.Context, *BatchImageJob, BatchImageInput) (*BatchProviderJob, error)
	Get(context.Context, *BatchImageJob) (*BatchProviderStatus, error)
	Cancel(context.Context, *BatchImageJob) error
	OpenResult(context.Context, *BatchImageJob) (io.ReadCloser, string, error)
	Cleanup(context.Context, *BatchImageJob, CleanupTarget) error
}
type ProviderProcessor struct {
	Now             func() time.Time
	Repo            BatchImageRepository
	ResolveProvider func(context.Context, *BatchImageJob) (BoundProvider, error)
	Indexer         *ResultIndexer
	Funding         Funding
	InvalidateAuth  func(context.Context, int64)
	DefaultRequeue  time.Duration
	Observe         func(string, ...any)
}

func (p *ProviderProcessor) warn(event string, values ...any) {
	if p.Observe != nil {
		p.Observe(event, values...)
	}
}

type ResultIndexer struct {
	Now     func() time.Time
	Repo    BatchImageRepository
	Observe func(string, ...any)
}

const (
	BatchImageParsedStatusSucceeded = "succeeded"
	BatchImageParsedStatusFailed    = "failed"

	DefaultBatchImageProcessorRequeue = 30 * time.Second
	BatchImageProviderErrorRequeue    = time.Minute
	BatchImageMaxErrorMessageLength   = 1000
)

func (p *ProviderProcessor) Process(ctx context.Context, batchID string) (BatchImageProcessResult, error) {
	if p == nil || p.Repo == nil || p.ResolveProvider == nil {
		return BatchImageProcessResult{}, infraerrors.New(infraerrors.CategoryInternalServer, "BATCH_IMAGE_PROCESSOR_NOT_CONFIGURED", "batch image processor is not configured")
	}

	job, err := p.Repo.GetBatchImageJobByBatchID(ctx, batchID)
	if err != nil {
		return BatchImageProcessResult{}, err
	}
	if IsBatchImageProcessorDoneStatus(job.Status) {
		if err := p.ReleaseTerminalHold(ctx, job); err != nil {
			return BatchImageProcessResult{}, err
		}
		return BatchImageProcessResult{Terminal: true}, nil
	}

	provider, err := p.ResolveProvider(ctx, job)
	if err != nil {
		return BatchImageProcessResult{}, err
	}
	if strings.TrimSpace(BatchImageDerefString(job.ProviderJobName)) == "" {
		return BatchImageProcessResult{}, ErrBatchImageMissingProviderJobName
	}

	if job.Status == BatchImageJobStatusIndexing {
		return p.IndexAndSettle(ctx, job, provider)
	}

	status, err := provider.Get(ctx, job)
	if ctx.Err() != nil {
		return BatchImageProcessResult{}, ctx.Err()
	}
	if err != nil {
		p.warn("batch_image.provider_status_check_failed",
			"batch_id", job.BatchID,
			"provider", job.Provider,
			"provider_job_name", BatchImageDerefString(job.ProviderJobName),
			"error", err,
		)
		return BatchImageProcessResult{RequeueAfter: BatchImageProviderErrorRequeue}, nil
	}
	if status == nil {
		return BatchImageProcessResult{RequeueAfter: p.RequeueDelay(0)}, nil
	}
	if err := p.PersistProviderOutputRef(ctx, job, status.ProviderOutputRef); err != nil {
		return BatchImageProcessResult{}, err
	}

	switch status.InternalState {
	case BatchProviderStateQueued:
		return BatchImageProcessResult{RequeueAfter: p.RequeueDelay(status.SuggestedRequeueAfter)}, nil
	case BatchProviderStateRunning:
		if job.Status != BatchImageJobStatusRunning {
			if err := p.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusRunning, BatchImageTransitionOptions{
				EventType:    "provider_status_checked",
				EventPayload: map[string]any{"provider_state": status.RawState},
			}); err != nil {
				return BatchImageProcessResult{}, err
			}
			job.Status = BatchImageJobStatusRunning
		}
		return BatchImageProcessResult{RequeueAfter: p.RequeueDelay(status.SuggestedRequeueAfter)}, nil
	case BatchProviderStateSucceeded:
		if job.Status != BatchImageJobStatusIndexing {
			if err := p.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusIndexing, BatchImageTransitionOptions{
				EventType:    "indexing_started",
				EventPayload: map[string]any{"provider_state": status.RawState},
			}); err != nil {
				return BatchImageProcessResult{}, err
			}
			job.Status = BatchImageJobStatusIndexing
		}
		return p.IndexAndSettle(ctx, job, provider)
	case BatchProviderStateFailed, BatchProviderStateExpired:
		code := strings.TrimSpace(status.ErrorCode)
		if code == "" && status.InternalState == BatchProviderStateExpired {
			code = "PROVIDER_BATCH_EXPIRED"
		}
		if code == "" {
			code = "PROVIDER_BATCH_FAILED"
		}
		msg := TruncateBatchImageMessage(status.ErrorMessage, BatchImageMaxErrorMessageLength)
		if err := p.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusFailed, BatchImageTransitionOptions{
			EventType:    "job_failed",
			EventPayload: map[string]any{"provider_state": status.RawState, "error_code": code},
			ErrorCode:    BatchImageStringPtr(code),
			ErrorMessage: BatchImageOptionalStringPtr(msg),
		}); err != nil {
			return BatchImageProcessResult{}, err
		}
		job.Status = BatchImageJobStatusFailed
		if err := p.ReleaseTerminalHold(ctx, job); err != nil {
			return BatchImageProcessResult{}, err
		}
		return BatchImageProcessResult{Terminal: true}, nil
	case BatchProviderStateCancelled:
		if err := p.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusCancelled, BatchImageTransitionOptions{
			EventType:    "job_failed",
			EventPayload: map[string]any{"provider_state": status.RawState, "error_code": "PROVIDER_BATCH_CANCELLED"},
		}); err != nil {
			return BatchImageProcessResult{}, err
		}
		job.Status = BatchImageJobStatusCancelled
		if err := p.ReleaseTerminalHold(ctx, job); err != nil {
			return BatchImageProcessResult{}, err
		}
		return BatchImageProcessResult{Terminal: true}, nil
	default:
		return BatchImageProcessResult{RequeueAfter: p.RequeueDelay(status.SuggestedRequeueAfter)}, nil
	}
}
func (p *ProviderProcessor) IndexAndSettle(ctx context.Context, job *BatchImageJob, provider BoundProvider) (BatchImageProcessResult, error) {
	indexer := p.Indexer
	if indexer == nil {
		indexer = &ResultIndexer{Repo: p.Repo, Observe: p.Observe}
	}
	if indexer.Repo == nil {
		indexer.Repo = p.Repo
	}

	result, err := indexer.Index(ctx, job, provider)
	if ctx.Err() != nil {
		return BatchImageProcessResult{}, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, ErrBatchImageIndexOutputMissing) {
			return BatchImageProcessResult{}, err
		}
		// job 状态已被并发方推进（如已进入 settling/终态）：不是索引数据问题，
		// 短延迟 requeue 让下一轮按最新状态处理，不能误转 failed。
		if errors.Is(err, ErrBatchImageIndexStateConflict) {
			return BatchImageProcessResult{RequeueAfter: time.Millisecond}, nil
		}
		code := "INDEX_PARSE_FAILED"
		if errors.Is(err, ErrBatchImageDuplicateCustomID) {
			code = "DUPLICATE_CUSTOM_ID_IN_OUTPUT"
		}
		msg := TruncateBatchImageMessage(err.Error(), BatchImageMaxErrorMessageLength)
		transitionErr := p.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusFailed, BatchImageTransitionOptions{
			EventType:    "indexing_failed",
			EventPayload: map[string]any{"error_code": code},
			ErrorCode:    BatchImageStringPtr(code),
			ErrorMessage: BatchImageOptionalStringPtr(msg),
		})
		if transitionErr != nil {
			return BatchImageProcessResult{}, transitionErr
		}
		job.Status = BatchImageJobStatusFailed
		if err := p.ReleaseTerminalHold(ctx, job); err != nil {
			return BatchImageProcessResult{}, err
		}
		return BatchImageProcessResult{Terminal: true}, nil
	}

	if err := p.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusSettling, BatchImageTransitionOptions{
		EventType: "indexing_completed",
		EventPayload: map[string]any{
			"success_count": result.SuccessCount,
			"fail_count":    result.FailCount,
			"total_count":   result.TotalCount,
		},
	}); err != nil {
		return BatchImageProcessResult{}, err
	}
	return BatchImageProcessResult{RequeueAfter: time.Millisecond}, nil
}
func (p *ProviderProcessor) ReleaseTerminalHold(ctx context.Context, job *BatchImageJob) error {
	if p == nil || job == nil {
		return nil
	}
	if job.Status != BatchImageJobStatusFailed && job.Status != BatchImageJobStatusCancelled {
		return nil
	}
	if err := p.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
		return err
	}
	if billingUserID := BillingUserID(job); p.InvalidateAuth != nil && billingUserID > 0 {
		p.InvalidateAuth(ctx, billingUserID)
	}
	return nil
}
func (p *ProviderProcessor) PersistProviderOutputRef(ctx context.Context, job *BatchImageJob, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" || job == nil || BatchImageDerefString(job.ProviderOutputRef) == ref {
		return nil
	}
	if err := p.Repo.UpdateBatchImageJobProviderOutputRef(ctx, job.BatchID, ref); err != nil {
		return err
	}
	job.ProviderOutputRef = &ref
	return nil
}
func (p *ProviderProcessor) RequeueDelay(suggested time.Duration) time.Duration {
	if suggested > 0 {
		return suggested
	}
	if p != nil && p.DefaultRequeue > 0 {
		return p.DefaultRequeue
	}
	return DefaultBatchImageProcessorRequeue
}
func IsBatchImageProcessorDoneStatus(status string) bool {
	if status == BatchImageJobStatusSettling {
		return true
	}
	return IsTerminalBatchImageJobStatus(status)
}

type BatchImageIndexResult struct {
	SuccessCount int
	FailCount    int
	TotalCount   int
}

func (i *ResultIndexer) Index(ctx context.Context, job *BatchImageJob, provider BoundProvider) (*BatchImageIndexResult, error) {
	if i == nil || i.Repo == nil || job == nil || provider == nil {
		return nil, ErrBatchImageIndexOutputMissing
	}
	expected, err := i.ListExpectedCustomIDs(ctx, job.BatchID)
	if err != nil {
		return nil, err
	}

	r, _, err := provider.OpenResult(ctx, job)
	if ctx.Err() != nil {
		if r != nil {
			_ = r.Close()
		}
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, ErrBatchImageIndexOutputMissing.WithCause(err)
	}
	defer func() { _ = r.Close() }()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	seen := make(map[string]int)
	unknownCount := 0
	var items []CreateBatchImageItemParams
	result := &BatchImageIndexResult{}
	lineNumber := 0
	now := i.now()
	sourceObject := BatchImageDerefString(job.ProviderOutputRef)
	if sourceObject == "" {
		sourceObject = BatchImageDerefString(job.ProviderJobName)
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parsed, err := ParseBatchImageResultLine([]byte(line), lineNumber)
		if err != nil {
			return nil, err
		}
		// 与提交时的 custom_id 集对账：provider 输出中未知/多余的行不能进入 item 表，
		// 否则 success+fail > item_count 会让结算永远校验失败。
		if len(expected) > 0 {
			if _, ok := expected[parsed.CustomID]; !ok {
				unknownCount++
				continue
			}
		}
		if firstLine, ok := seen[parsed.CustomID]; ok {
			return nil, ErrBatchImageDuplicateCustomID.WithCause(fmt.Errorf("custom id %q duplicated at lines %d and %d", parsed.CustomID, firstLine, lineNumber))
		}
		seen[parsed.CustomID] = lineNumber

		lineNo := parsed.SourceLineNumber
		item := CreateBatchImageItemParams{
			JobID:                job.BatchID,
			CustomID:             parsed.CustomID,
			Status:               BatchImageItemStatusFailed,
			ProviderSourceObject: BatchImageOptionalStringPtr(sourceObject),
			SourceLineNumber:     &lineNo,
			ImageCount:           parsed.ImageCount,
			IndexedAt:            &now,
		}
		if parsed.Status == BatchImageParsedStatusSucceeded {
			item.Status = BatchImageItemStatusSuccess
			item.MimeType = BatchImageOptionalStringPtr(parsed.MimeType)
			item.FileExtension = BatchImageOptionalStringPtr(parsed.FileExtension)
			result.SuccessCount++
		} else {
			item.ErrorCode = BatchImageOptionalStringPtr(parsed.ErrorCode)
			item.ErrorMessage = BatchImageOptionalStringPtr(parsed.ErrorMessage)
			result.FailCount++
		}
		items = append(items, item)
		result.TotalCount++
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrBatchImageIndexParseFailed.WithCause(err)
		}
		return nil, err
	}
	// 输出中漏掉的已提交项必须补失败记录，而不是静默消失：
	// 否则用户看不到该项，且只按成功数计费会掩盖 provider 的丢单。
	missingCount := 0
	if len(expected) > 0 {
		missingIDs := make([]string, 0)
		for customID := range expected {
			if _, ok := seen[customID]; !ok {
				missingIDs = append(missingIDs, customID)
			}
		}
		sort.Strings(missingIDs)
		for _, customID := range missingIDs {
			items = append(items, CreateBatchImageItemParams{
				JobID:                job.BatchID,
				CustomID:             customID,
				Status:               BatchImageItemStatusFailed,
				ProviderSourceObject: BatchImageOptionalStringPtr(sourceObject),
				ErrorCode:            BatchImageStringPtr("PROVIDER_RESULT_MISSING"),
				ErrorMessage:         BatchImageStringPtr("provider output did not include a result for this item"),
				IndexedAt:            &now,
			})
			result.FailCount++
			result.TotalCount++
		}
		missingCount = len(missingIDs)
	}
	if result.TotalCount == 0 {
		return nil, ErrBatchImageIndexNoResultLines
	}
	if unknownCount > 0 || missingCount > 0 {
		i.warn("batch_image.index_reconciled",
			"batch_id", job.BatchID,
			"unknown_custom_ids", unknownCount,
			"missing_custom_ids", missingCount,
		)
		if err := i.Repo.AppendBatchImageEvent(ctx, job.BatchID, "index_reconciled", map[string]any{
			"batch_id":           job.BatchID,
			"unknown_custom_ids": unknownCount,
			"missing_custom_ids": missingCount,
		}); err != nil {
			i.warn("batch_image.index_reconcile_event_failed",
				"batch_id", job.BatchID,
				"error", err,
			)
		}
	}
	if err := i.Repo.ReplaceBatchImageItemsForJob(ctx, job.BatchID, items, BatchImageCounts{
		SuccessCount: result.SuccessCount,
		FailCount:    result.FailCount,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// ListExpectedCustomIDs 返回该 job 当前 item 表中的全部 custom_id 集合，
// 即提交时预创建（或上一轮索引重建）的完整条目清单，用于与 provider 输出对账。
func (i *ResultIndexer) ListExpectedCustomIDs(ctx context.Context, batchID string) (map[string]struct{}, error) {
	const pageSize = 500
	expected := make(map[string]struct{})
	offset := 0
	for {
		page, err := i.Repo.ListBatchImageItems(ctx, batchID, BatchImageItemFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, item := range page {
			if item != nil {
				expected[item.CustomID] = struct{}{}
			}
		}
		if len(page) < pageSize {
			return expected, nil
		}
		offset += len(page)
	}
}

type ParsedBatchImageResult struct {
	CustomID      string
	Status        string
	MimeType      string
	FileExtension string
	ImageCount    int

	ErrorCode    string
	ErrorMessage string

	SourceLineNumber int
}

func ParseBatchImageResultLine(line []byte, lineNumber int) (*ParsedBatchImageResult, error) {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return nil, ErrBatchImageIndexParseFailed.WithCause(fmt.Errorf("line %d: %w", lineNumber, err))
	}

	customID := BatchImageFirstNonEmptyString(
		BatchImageMapString(obj, "key"),
		BatchImageMapString(obj, "custom_id"),
		BatchImageMapString(obj, "customId"),
		BatchImageNestedString(obj, "request", "key"),
	)
	if customID == "" {
		return nil, ErrBatchImageIndexParseFailed.WithCause(fmt.Errorf("line %d: missing custom id", lineNumber))
	}

	parsed := &ParsedBatchImageResult{
		CustomID:         customID,
		SourceLineNumber: lineNumber,
	}
	imageCount, mimeType := BatchImageFindImageParts(obj)
	if imageCount > 0 {
		parsed.Status = BatchImageParsedStatusSucceeded
		parsed.ImageCount = imageCount
		parsed.MimeType = mimeType
		parsed.FileExtension = BatchImageFileExtension(mimeType)
		return parsed, nil
	}

	if code, message, ok := BatchImageFailureFromProviderFields(obj); ok {
		parsed.Status = BatchImageParsedStatusFailed
		parsed.ErrorCode = code
		parsed.ErrorMessage = TruncateBatchImageMessage(message, BatchImageMaxErrorMessageLength)
		return parsed, nil
	}

	if _, hasResponse := obj["response"]; hasResponse || BatchImageHasCandidates(obj) {
		parsed.Status = BatchImageParsedStatusFailed
		parsed.ErrorCode = "EMPTY_IMAGE_OUTPUT"
		parsed.ErrorMessage = "provider response contained no image output"
		return parsed, nil
	}

	parsed.Status = BatchImageParsedStatusFailed
	parsed.ErrorCode = "PROVIDER_ITEM_FAILED"
	parsed.ErrorMessage = "provider result line contained no image output"
	return parsed, nil
}
func BatchImageFindImageParts(obj map[string]any) (int, string) {
	count, mimeType := BatchImageFindImagePartsInCandidates(BatchImageNestedAny(obj, "response", "candidates"))
	if count > 0 {
		return count, mimeType
	}
	return BatchImageFindImagePartsInCandidates(obj["candidates"])
}
func BatchImageFindImagePartsInCandidates(raw any) (int, string) {
	candidates, ok := raw.([]any)
	if !ok {
		return 0, ""
	}
	count := 0
	firstMime := ""
	for _, candidateRaw := range candidates {
		candidate, ok := candidateRaw.(map[string]any)
		if !ok {
			continue
		}
		partsRaw := BatchImageNestedAny(candidate, "content", "parts")
		parts, ok := partsRaw.([]any)
		if !ok {
			continue
		}
		for _, partRaw := range parts {
			part, ok := partRaw.(map[string]any)
			if !ok {
				continue
			}
			inline, ok := FirstMap(part["inlineData"], part["inline_data"])
			if !ok {
				continue
			}
			data := strings.TrimSpace(BatchImageMapString(inline, "data"))
			mime := BatchImageFirstNonEmptyString(BatchImageMapString(inline, "mimeType"), BatchImageMapString(inline, "mime_type"))
			if data == "" || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mime)), "image/") {
				continue
			}
			count++
			if firstMime == "" {
				firstMime = strings.TrimSpace(mime)
			}
		}
	}
	return count, firstMime
}
func BatchImageFailureFromProviderFields(obj map[string]any) (string, string, bool) {
	if status, ok := obj["status"].(map[string]any); ok {
		message := BatchImageFirstNonEmptyString(BatchImageMapString(status, "message"), BatchImageMapString(status, "details"))
		code := BatchImageFirstNonEmptyString(BatchImageMapString(status, "code"), BatchImageMapString(status, "status"))
		return BatchImageMapFailureCode(code, message), message, true
	}
	if errObj, ok := obj["error"].(map[string]any); ok {
		message := BatchImageFirstNonEmptyString(BatchImageMapString(errObj, "message"), BatchImageMapString(errObj, "details"))
		code := BatchImageFirstNonEmptyString(BatchImageMapString(errObj, "code"), BatchImageMapString(errObj, "status"))
		return BatchImageMapFailureCode(code, message), message, true
	}
	return "", "", false
}
func BatchImageMapFailureCode(code, message string) string {
	text := strings.ToLower(strings.TrimSpace(code + " " + message))
	switch {
	case strings.Contains(text, "safety"), strings.Contains(text, "policy"), strings.Contains(text, "blocked"), strings.Contains(text, "prohibited"):
		return "SAFETY_BLOCKED"
	case strings.Contains(text, "invalid_argument"), strings.Contains(text, "invalid argument"), strings.Contains(text, "bad request"):
		return "INVALID_ARGUMENT"
	case strings.Contains(text, "quota"), strings.Contains(text, "rate"), strings.Contains(text, "resource_exhausted"), strings.Contains(text, "too many requests"):
		return "PROVIDER_RATE_LIMITED"
	default:
		return "PROVIDER_ITEM_FAILED"
	}
}
func BatchImageFileExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return ""
	}
}
func BatchImageHasCandidates(obj map[string]any) bool {
	if _, ok := obj["candidates"]; ok {
		return true
	}
	_, ok := BatchImageNestedAny(obj, "response", "candidates").([]any)
	return ok
}
func BatchImageMapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}
func BatchImageNestedString(m map[string]any, keys ...string) string {
	if nested, ok := BatchImageNestedAny(m, keys...).(string); ok {
		return strings.TrimSpace(nested)
	}
	return ""
}
func BatchImageNestedAny(m map[string]any, keys ...string) any {
	var current any = m
	for _, key := range keys {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = cm[key]
	}
	return current
}
func FirstMap(values ...any) (map[string]any, bool) {
	for _, value := range values {
		if m, ok := value.(map[string]any); ok {
			return m, true
		}
	}
	return nil, false
}
func BatchImageFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (i *ResultIndexer) warn(event string, values ...any) {
	if i.Observe != nil {
		i.Observe(event, values...)
	}
}

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s *ResultIndexer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
