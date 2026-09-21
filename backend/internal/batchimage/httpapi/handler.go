package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	service "github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type BatchImageHandler struct {
	enter    func() (func(), error)
	service  UseCases
	download DownloadUseCases
	cleanup  CleanupUseCases
	access   AccessPorts
}

func NewBatchImageHandler(service UseCases, download DownloadUseCases, cleanup CleanupUseCases, access AccessPorts) *BatchImageHandler {
	return &BatchImageHandler{service: service, download: download, cleanup: cleanup, access: access}
}

func (h *BatchImageHandler) Submit(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	var req service.BatchImageSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BatchImageError(c, service.ErrBatchImageInvalidItems)
		return
	}
	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	if sessionID := h.access.SessionID(c); sessionID != "" {
		req.SessionID = &sessionID
	}
	got, err := h.service.Submit(c.Request.Context(), owner, req, c.GetHeader("Idempotency-Key"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) Get(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.service.Get(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) List(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	got, err := h.service.List(c.Request.Context(), owner, service.BatchImageJobsQuery{
		Status:     c.Query("status"),
		TaskName:   c.Query("task_name"),
		Downloaded: c.Query("downloaded"),
		From:       c.Query("from"),
		To:         c.Query("to"),
		Limit:      limit,
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) Models(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	apiKey, _ := h.access.Key(c)
	if apiKey != nil && apiKey.IsComposite {
		preferredSubscription, ready := h.preferred(c, apiKey)
		if !ready {
			c.JSON(http.StatusOK, &service.BatchImagePublicModelsResponse{Object: "list", Data: make([]service.BatchImagePublicModel, 0)})
			return
		}
		out := &service.BatchImagePublicModelsResponse{Object: "list", Data: make([]service.BatchImagePublicModel, 0)}
		for _, binding := range apiKey.CompositeGroups {
			if !gatewayhttp.CompositeGroupAvailableToUser(apiKey, preferredSubscription, binding.Group) || !binding.Group.AllowBatchImageGeneration {
				continue
			}
			groupOwner := owner
			groupID := binding.GroupID
			groupOwner.GroupID = &groupID
			models, err := h.service.ListModels(c.Request.Context(), groupOwner)
			if err != nil {
				BatchImageError(c, err)
				return
			}
			for _, model := range AppendBatchImageAPIKeyModelAliases(models.Data, apiKey.ModelMapping) {
				model.ID = binding.Prefix + "/" + model.ID
				out.Data = append(out.Data, model)
			}
		}
		c.JSON(http.StatusOK, out)
		return
	}
	got, err := h.service.ListModels(c.Request.Context(), owner)
	if err != nil {
		BatchImageError(c, err)
		return
	}
	if apiKey != nil {
		got.Data = AppendBatchImageAPIKeyModelAliases(got.Data, apiKey.ModelMapping)
	}
	c.JSON(http.StatusOK, got)
}

// AppendBatchImageAPIKeyModelAliases 按目标模型的提供方克隆批量图片模型别名。
func AppendBatchImageAPIKeyModelAliases(models []service.BatchImagePublicModel, mapping map[string]string) []service.BatchImagePublicModel {
	modelIDs := make([]string, 0, len(models))
	templates := make(map[string][]service.BatchImagePublicModel)
	for _, model := range models {
		modelIDs = append(modelIDs, model.ID)
		templates[model.ID] = append(templates[model.ID], model)
	}
	result := append([]service.BatchImagePublicModel(nil), models...)
	for _, alias := range apikey.AvailableAPIKeyModelAliases(modelIDs, mapping) {
		for _, template := range templates[mapping[alias]] {
			template.ID = alias
			result = append(result, template)
		}
	}
	return result
}

func (h *BatchImageHandler) Items(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	got, err := h.service.ListItems(c.Request.Context(), owner, c.Param("id"), service.BatchImageItemsQuery{
		Status: c.Query("status"),
		Limit:  limit,
		Cursor: c.Query("cursor"),
	})
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) Cancel(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.service.Cancel(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) ItemContent(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	imageIndex := 0
	if raw := c.Query("image_index"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			BatchImageError(c, service.ErrBatchImageItemImageIndexOutOfRange)
			return
		}
		imageIndex = parsed
	}
	stream, err := h.download.OpenItemContent(c.Request.Context(), owner, c.Param("id"), c.Param("custom_id"), imageIndex)
	if err != nil {
		BatchImageError(c, err)
		return
	}
	defer func() { _ = stream.Reader.Close() }()

	c.Header("Content-Type", stream.ContentType)
	c.Header("Content-Disposition", service.BatchImageContentDispositionAttachment(stream.Filename))
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	if stream.ContentLength != nil && *stream.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(*stream.ContentLength, 10))
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, stream.Reader); err != nil {
		return
	}
	h.markDownloadedBestEffort(c, owner)
}

// markDownloadedBestEffort 在响应体已写出后标记下载状态；
// 此时无法再向客户端返回错误，失败只能记日志（不能静默丢弃）。
func (h *BatchImageHandler) markDownloadedBestEffort(c *gin.Context, owner service.BatchImageOwner) {
	if err := h.service.MarkDownloaded(c.Request.Context(), owner, c.Param("id")); err != nil {
		logging.L().Warn("batch_image.mark_downloaded_failed",
			zap.String("batch_id", c.Param("id")),
			zap.Error(err),
		)
	}
}

func (h *BatchImageHandler) Download(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	maxItems, _ := strconv.Atoi(c.Query("max_items"))

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", service.BatchImageContentDispositionAttachment(c.Param("id")+".zip"))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	result, err := h.download.StreamZip(c.Request.Context(), owner, c.Param("id"), service.BatchImageZipOptions{
		Status:          c.Query("status"),
		MaxItems:        maxItems,
		IncludeManifest: true,
	}, c.Writer)
	if err != nil {
		if result == nil || !c.Writer.Written() {
			BatchImageError(c, err)
		}
		return
	}
	h.markDownloadedBestEffort(c, owner)
}

func (h *BatchImageHandler) DeleteRecord(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	if err := h.service.DeleteRecord(c.Request.Context(), owner, c.Param("id")); err != nil {
		BatchImageError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *BatchImageHandler) DeleteOutputs(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.cleanup.DeleteOutputsForOwner(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) owner(c *gin.Context) (service.BatchImageOwner, bool) {
	apiKey, ok := h.access.Key(c)
	if !ok || apiKey == nil || apiKey.ID <= 0 || apiKey.UserID <= 0 {
		return service.BatchImageOwner{}, false
	}
	billingUserID := apiKey.UserID
	if apiKey.User != nil && apiKey.User.ID > 0 {
		billingUserID = apiKey.User.ID
	}
	return service.BatchImageOwner{
		UserID:                  apiKey.UserID,
		BillingUserID:           billingUserID,
		TeamID:                  apiKey.TeamID,
		APIKeyID:                apiKey.ID,
		GroupID:                 apiKey.GroupID,
		BillingMode:             apikey.APIKeyEffectiveBillingMode(apiKey),
		PreferredSubscriptionID: apiKey.PreferredSubscriptionID,
	}, true
}

func BatchImageError(c *gin.Context, err error) {
	status := infraerrors.ErrorCode(err)
	code := apperror.Reason(err)
	message := apperror.Message(err)
	if err == nil {
		status = http.StatusInternalServerError
		code = "INTERNAL_ERROR"
		message = "internal error"
	}
	if status == 0 || (status == http.StatusInternalServerError && strings.TrimSpace(code) == "") {
		status = http.StatusInternalServerError
		code = "INTERNAL_ERROR"
		message = "internal error"
	}
	if errors.Is(err, service.ErrBatchImageJobNotFound) {
		status = http.StatusNotFound
		code = "BATCH_IMAGE_NOT_FOUND"
		message = "batch image job not found"
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    code,
			"message": message,
		},
	})
}

// AccessPorts 读取已认证请求的唯一快照；任务 handler 不再次解析或验证凭据。
type AccessPorts struct {
	Key                   func(*gin.Context) (*apikey.APIKey, bool)
	PreferredSubscription func(*gin.Context) (*billing.UserSubscription, bool)
	SessionID             func(*gin.Context) string
}

func (h *BatchImageHandler) preferred(c *gin.Context, k *apikey.APIKey) (*billing.UserSubscription, bool) {
	if apikey.APIKeyEffectiveBillingMode(k) != apikey.APIKeyBillingModeSubscription {
		return nil, true
	}
	value, ok := h.access.PreferredSubscription(c)
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

type UseCases interface {
	Submit(context.Context, service.BatchImageOwner, service.BatchImageSubmitRequest, string) (*service.BatchImagePublicBatch, error)
	Get(context.Context, service.BatchImageOwner, string) (*service.BatchImagePublicBatch, error)
	List(context.Context, service.BatchImageOwner, service.BatchImageJobsQuery) (*service.BatchImagePublicListResponse, error)
	ListModels(context.Context, service.BatchImageOwner) (*service.BatchImagePublicModelsResponse, error)
	ListItems(context.Context, service.BatchImageOwner, string, service.BatchImageItemsQuery) (*service.BatchImagePublicItemsResponse, error)
	Cancel(context.Context, service.BatchImageOwner, string) (*service.BatchImagePublicBatch, error)
	MarkDownloaded(context.Context, service.BatchImageOwner, string) error
	DeleteRecord(context.Context, service.BatchImageOwner, string) error
}
type DownloadUseCases interface {
	OpenItemContent(context.Context, service.BatchImageOwner, string, string, int) (*service.BatchImageContentStream, error)
	StreamZip(context.Context, service.BatchImageOwner, string, service.BatchImageZipOptions, io.Writer) (*service.BatchImageZipResult, error)
}
type CleanupUseCases interface {
	DeleteOutputsForOwner(context.Context, service.BatchImageOwner, string) (*service.BatchImagePublicBatch, error)
}

// BindActivity 在构造阶段绑定任务入口关闭屏障，释放覆盖完整 HTTP 流式输出。
func (h *BatchImageHandler) BindActivity(enter func() (func(), error)) { h.enter = enter }
