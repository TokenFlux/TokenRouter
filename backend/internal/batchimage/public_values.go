package batchimage

import (
	"strings"
	"time"
)

func BatchImageJobToPublic(job *BatchImageJob) *BatchImagePublicBatch {
	if job == nil {
		return nil
	}
	holdAmount := job.EstimatedCost
	if job.HoldAmount != nil {
		holdAmount = *job.HoldAmount
	}
	return &BatchImagePublicBatch{
		ID:              job.BatchID,
		Object:          "image.batch",
		TaskName:        BatchImagePublicTaskName(job),
		ParentBatchID:   job.ParentBatchID,
		Status:          PublicBatchImageStatus(job.Status),
		Model:           BatchImageRequestedModel(job),
		Provider:        job.Provider,
		ItemCount:       job.ItemCount,
		SuccessCount:    job.SuccessCount,
		FailCount:       job.FailCount,
		EstimatedCost:   job.EstimatedCost,
		HoldAmount:      holdAmount,
		ActualCost:      job.ActualCost,
		CreatedAt:       job.CreatedAt.Unix(),
		SubmittedAt:     BatchImageUnixPtr(job.SubmittedAt),
		SettledAt:       BatchImageUnixPtr(job.SettledAt),
		DownloadedAt:    BatchImageUnixPtr(job.DownloadedAt),
		OutputDeletedAt: BatchImageUnixPtr(job.OutputDeletedAt),
	}
}
func BatchImageItemToPublic(item *BatchImageItem) BatchImagePublicItem {
	out := BatchImagePublicItem{
		CustomID:      item.CustomID,
		Status:        "failed",
		PromptPreview: item.PromptPreview,
		MimeType:      item.MimeType,
		FileExtension: item.FileExtension,
		ImageCount:    item.ImageCount,
	}
	if item.Status == BatchImageItemStatusPending {
		out.Status = "pending"
		return out
	}
	if item.Status == BatchImageItemStatusSuccess {
		out.Status = "succeeded"
		return out
	}
	out.Error = &BatchImagePublicError{
		Code:    BatchImageDerefString(item.ErrorCode),
		Message: SanitizeBatchImagePublicMessage(BatchImageDerefString(item.ErrorMessage)),
		Source:  BatchImageItemErrorSource(item),
	}
	return out
}
func BatchImageUnixPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

func BatchImageItemErrorSource(item *BatchImageItem) string {
	if item == nil || item.ErrorCode == nil {
		return ""
	}
	code := strings.TrimSpace(*item.ErrorCode)
	if BatchImageDerefString(item.ProviderSourceObject) != "" {
		return "provider"
	}
	switch code {
	case "EMPTY_IMAGE_OUTPUT", "PROVIDER_ITEM_FAILED":
		return "provider"
	case "INDEX_OUTPUT_MISSING", "INDEX_PARSE_FAILED", "DUPLICATE_CUSTOM_ID_IN_OUTPUT":
		return "system"
	default:
		return ""
	}
}
func PublicBatchImageStatus(status string) string {
	switch status {
	case BatchImageJobStatusCreated, BatchImageJobStatusUploading, BatchImageJobStatusSubmitted:
		return "queued"
	case BatchImageJobStatusRunning:
		return "running"
	case BatchImageJobStatusIndexing:
		return "processing_results"
	case BatchImageJobStatusSettling:
		return "settling"
	case BatchImageJobStatusCompleted:
		return "completed"
	case BatchImageJobStatusFailed:
		return "failed"
	case BatchImageJobStatusCancelled:
		return "cancelled"
	case BatchImageJobStatusOutputDeleted:
		return "output_deleted"
	default:
		return status
	}
}
func BatchImagePublicTaskName(job *BatchImageJob) string {
	if job == nil {
		return ""
	}
	if strings.TrimSpace(job.TaskName) != "" {
		return strings.TrimSpace(job.TaskName)
	}
	return DefaultBatchImageTaskName(job.CreatedAt)
}
func DefaultBatchImageTaskName(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.Format("2006-01-02 15:04:05")
}
func SanitizeBatchImagePublicMessage(message string) string {
	message = strings.TrimSpace(message)
	for _, marker := range []string{"gs://", "files/", "projects/"} {
		if strings.Contains(message, marker) {
			message = "upstream provider operation failed"
			break
		}
	}
	if len(message) > maxBatchImagePublicErrorChars {
		message = message[:maxBatchImagePublicErrorChars]
	}
	return message
}

const maxBatchImagePublicErrorChars = 500
