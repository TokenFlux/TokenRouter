package batchimage

import (
	"strings"
)

func BatchImageDerefString(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}
func BatchImageStringPtr(v string) *string {
	return &v
}
func BatchImageOptionalStringPtr(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}
func TruncateBatchImageMessage(message string, limit int) string {
	message = strings.TrimSpace(message)
	if limit <= 0 || len(message) <= limit {
		return message
	}
	return message[:limit]
}

// BatchImageRequestedModel 优先返回客户端模型，并兼容迁移前仅保存实际模型的任务。
func BatchImageRequestedModel(job *BatchImageJob) string {
	if job == nil {
		return ""
	}
	if requestedModel := strings.TrimSpace(job.RequestedModel); requestedModel != "" {
		return requestedModel
	}
	return job.Model
}

// BatchImageInternalModel 返回提交时确定的内部模型，并兼容迁移前仅保存上游模型的任务。
func BatchImageInternalModel(job *BatchImageJob) string {
	if job == nil {
		return ""
	}
	if internalModel := strings.TrimSpace(job.InternalModel); internalModel != "" {
		return internalModel
	}
	return strings.TrimSpace(job.Model)
}

func NormalizeBatchImageReferenceMimeType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	case "image/webp":
		return "image/webp"
	default:
		return ""
	}
}

func BatchImageGCSRef(provider, ref string) string {
	if provider == BatchImageProviderVertex && strings.HasPrefix(strings.TrimSpace(ref), "gs://") {
		return strings.TrimSpace(ref)
	}
	return ""
}
