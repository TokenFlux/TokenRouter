// 任务公共值与供应商结果契约不携带账号凭据或旧服务实体。
package batchimage

import (
	"context"
	"time"
)

type BatchImageInput struct {
	BatchID     string
	Model       string
	DisplayName string
	Items       []BatchImageInputItem

	ResponseMimeType string
	AspectRatio      string
	ImageSize        string

	Metadata map[string]string
}
type BatchImageInputItem struct {
	CustomID string
	Prompt   string

	ReferenceImages []BatchImageReference
}
type BatchImageReference struct {
	ID       string
	Type     string
	MimeType string
	Data     []byte
	FileURI  string
}
type BatchProviderJob struct {
	ProviderJobName   string
	ProviderInputRef  string
	ProviderOutputRef string
	RawState          string
}
type BatchProviderInternalState string

const (
	BatchProviderStateQueued    BatchProviderInternalState = "queued"
	BatchProviderStateRunning   BatchProviderInternalState = "running"
	BatchProviderStateSucceeded BatchProviderInternalState = "succeeded"
	BatchProviderStateFailed    BatchProviderInternalState = "failed"
	BatchProviderStateCancelled BatchProviderInternalState = "cancelled"
	BatchProviderStateExpired   BatchProviderInternalState = "expired"
)

type BatchProviderStatus struct {
	RawState string

	InternalState BatchProviderInternalState
	Done          bool

	ProviderOutputRef string

	ErrorCode    string
	ErrorMessage string

	SuggestedRequeueAfter time.Duration
}
type CleanupTarget string

const (
	CleanupTargetInput  CleanupTarget = "input"
	CleanupTargetOutput CleanupTarget = "output"
	CleanupTargetAll    CleanupTarget = "all"
)

type BatchImageUserGroupRateRepository interface {
	GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error)
}
type BatchImageSubmitRequest struct {
	Model            string                 `json:"model"`
	TaskName         string                 `json:"task_name"`
	ParentBatchID    string                 `json:"parent_batch_id"`
	Provider         string                 `json:"provider"`
	Items            []BatchImageSubmitItem `json:"items"`
	ResponseMimeType string                 `json:"response_mime_type"`
	AspectRatio      string                 `json:"aspect_ratio"`
	ImageSize        string                 `json:"image_size"`
	Metadata         map[string]string      `json:"metadata"`
	SessionID        *string                `json:"-"`
}
type BatchImageSubmitItem struct {
	CustomID        string                     `json:"custom_id"`
	Prompt          string                     `json:"prompt"`
	OutputCount     int                        `json:"output_count,omitempty"`
	ReferenceImages []BatchImageReferenceInput `json:"reference_images,omitempty"`
}
type BatchImageReferenceInput struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	MimeType string `json:"mime_type"`
	Data     []byte `json:"data,omitempty"`
	FileURI  string `json:"file_uri,omitempty"`
}
type BatchImageOwner struct {
	UserID                  int64
	BillingUserID           int64
	TeamID                  *int64
	APIKeyID                int64
	GroupID                 *int64
	BillingMode             string
	PreferredSubscriptionID *int64
}

// EffectiveBillingUserID 兼容个人 Key 和迁移前创建的批量任务调用参数。
func (o BatchImageOwner) EffectiveBillingUserID() int64 {
	if o.BillingUserID > 0 {
		return o.BillingUserID
	}
	return o.UserID
}

type BatchImagePricingSnapshot struct {
	BaseUnitPrice              float64
	GroupRateMultiplier        float64
	SubscriptionRateMultiplier float64
	BalanceRateMultiplier      float64
	PlanGroupRateEnabled       bool
	AccountRateMultiplier      float64
	BatchDiscountMultiplier    float64
	HoldMultiplier             float64
	BillableUnitPrice          float64
	HoldUnitPrice              float64
	EstimatedCost              float64
	HoldAmount                 float64
}
type BatchImagePublicBatch struct {
	ID              string   `json:"id"`
	Object          string   `json:"object"`
	TaskName        string   `json:"task_name"`
	ParentBatchID   *string  `json:"parent_batch_id,omitempty"`
	Status          string   `json:"status"`
	Model           string   `json:"model"`
	Provider        string   `json:"provider"`
	ItemCount       int      `json:"item_count"`
	SuccessCount    int      `json:"success_count"`
	FailCount       int      `json:"fail_count"`
	EstimatedCost   float64  `json:"estimated_cost"`
	HoldAmount      float64  `json:"hold_amount"`
	ActualCost      *float64 `json:"actual_cost"`
	CreatedAt       int64    `json:"created_at"`
	SubmittedAt     *int64   `json:"submitted_at"`
	SettledAt       *int64   `json:"settled_at"`
	DownloadedAt    *int64   `json:"downloaded_at,omitempty"`
	OutputDeletedAt *int64   `json:"output_deleted_at,omitempty"`
}
type BatchImagePublicItem struct {
	CustomID      string                 `json:"custom_id"`
	Status        string                 `json:"status"`
	PromptPreview *string                `json:"prompt_preview,omitempty"`
	MimeType      *string                `json:"mime_type"`
	FileExtension *string                `json:"file_extension"`
	ImageCount    int                    `json:"image_count"`
	Error         *BatchImagePublicError `json:"error"`
}
type BatchImagePublicError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
}
type BatchImagePublicItemsResponse struct {
	Object  string                 `json:"object"`
	Data    []BatchImagePublicItem `json:"data"`
	HasMore bool                   `json:"has_more"`
}
type BatchImagePublicListResponse struct {
	Object  string                   `json:"object"`
	Data    []*BatchImagePublicBatch `json:"data"`
	HasMore bool                     `json:"has_more"`
}
type BatchImagePublicModel struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Provider string `json:"provider"`
}
type BatchImagePublicModelsResponse struct {
	Object string                  `json:"object"`
	Data   []BatchImagePublicModel `json:"data"`
}
type BatchImageJobsQuery struct {
	Status     string
	TaskName   string
	Downloaded string
	From       string
	To         string
	Limit      int
	Cursor     string
}
type BatchImageItemsQuery struct {
	Status string
	Limit  int
	Cursor string
}
