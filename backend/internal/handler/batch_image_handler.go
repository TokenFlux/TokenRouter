package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type BatchImageHandler struct {
	service  *service.BatchImagePublicService
	download *service.BatchImageDownloadService
	cleanup  *service.BatchImageCleanupService
}

func NewBatchImageHandler(service *service.BatchImagePublicService, download *service.BatchImageDownloadService, cleanup *service.BatchImageCleanupService) *BatchImageHandler {
	return &BatchImageHandler{service: service, download: download, cleanup: cleanup}
}

func (h *BatchImageHandler) Submit(c *gin.Context) {
	var req service.BatchImageSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		batchImageError(c, service.ErrBatchImageInvalidItems)
		return
	}
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	if sessionID := service.ExtractClientSessionID(c); sessionID != "" {
		req.SessionID = &sessionID
	}
	got, err := h.service.Submit(c.Request.Context(), owner, req, c.GetHeader("Idempotency-Key"))
	if err != nil {
		batchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) Get(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.service.Get(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		batchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) List(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
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
		batchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) Models(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	apiKey, _ := middleware.GetAPIKeyFromContext(c)
	if apiKey != nil && apiKey.IsComposite {
		preferredSubscription, ready := compositePreferredSubscription(c, apiKey)
		if !ready {
			c.JSON(http.StatusOK, &service.BatchImagePublicModelsResponse{Object: "list", Data: make([]service.BatchImagePublicModel, 0)})
			return
		}
		out := &service.BatchImagePublicModelsResponse{Object: "list", Data: make([]service.BatchImagePublicModel, 0)}
		for _, binding := range apiKey.CompositeGroups {
			if !compositeGroupAvailableToUser(apiKey, preferredSubscription, binding.Group) || !binding.Group.AllowBatchImageGeneration {
				continue
			}
			groupOwner := owner
			groupID := binding.GroupID
			groupOwner.GroupID = &groupID
			models, err := h.service.ListModels(c.Request.Context(), groupOwner)
			if err != nil {
				batchImageError(c, err)
				return
			}
			for _, model := range appendBatchImageAPIKeyModelAliases(models.Data, apiKey.ModelMapping) {
				model.ID = binding.Prefix + "/" + model.ID
				out.Data = append(out.Data, model)
			}
		}
		c.JSON(http.StatusOK, out)
		return
	}
	got, err := h.service.ListModels(c.Request.Context(), owner)
	if err != nil {
		batchImageError(c, err)
		return
	}
	if apiKey != nil {
		got.Data = appendBatchImageAPIKeyModelAliases(got.Data, apiKey.ModelMapping)
	}
	c.JSON(http.StatusOK, got)
}

// appendBatchImageAPIKeyModelAliases 按目标模型的提供方克隆批量图片模型别名。
func appendBatchImageAPIKeyModelAliases(models []service.BatchImagePublicModel, mapping map[string]string) []service.BatchImagePublicModel {
	modelIDs := make([]string, 0, len(models))
	templates := make(map[string][]service.BatchImagePublicModel)
	for _, model := range models {
		modelIDs = append(modelIDs, model.ID)
		templates[model.ID] = append(templates[model.ID], model)
	}
	result := append([]service.BatchImagePublicModel(nil), models...)
	for _, alias := range service.AvailableAPIKeyModelAliases(modelIDs, mapping) {
		for _, template := range templates[mapping[alias]] {
			template.ID = alias
			result = append(result, template)
		}
	}
	return result
}

func (h *BatchImageHandler) Items(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	got, err := h.service.ListItems(c.Request.Context(), owner, c.Param("id"), service.BatchImageItemsQuery{
		Status: c.Query("status"),
		Limit:  limit,
		Cursor: c.Query("cursor"),
	})
	if err != nil {
		batchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) Cancel(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.service.Cancel(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		batchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func (h *BatchImageHandler) ItemContent(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	imageIndex := 0
	if raw := c.Query("image_index"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			batchImageError(c, service.ErrBatchImageItemImageIndexOutOfRange)
			return
		}
		imageIndex = parsed
	}
	stream, err := h.download.OpenItemContent(c.Request.Context(), owner, c.Param("id"), c.Param("custom_id"), imageIndex)
	if err != nil {
		batchImageError(c, err)
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
		logger.L().Warn("batch_image.mark_downloaded_failed",
			zap.String("batch_id", c.Param("id")),
			zap.Error(err),
		)
	}
}

func (h *BatchImageHandler) Download(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
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
			batchImageError(c, err)
		}
		return
	}
	h.markDownloadedBestEffort(c, owner)
}

func (h *BatchImageHandler) DeleteRecord(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	if err := h.service.DeleteRecord(c.Request.Context(), owner, c.Param("id")); err != nil {
		batchImageError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *BatchImageHandler) DeleteOutputs(c *gin.Context) {
	owner, ok := batchImageOwnerFromContext(c)
	if !ok {
		batchImageError(c, infraerrors.New(http.StatusUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.cleanup.DeleteOutputsForOwner(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		batchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

func batchImageOwnerFromContext(c *gin.Context) (service.BatchImageOwner, bool) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
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
		BillingMode:             service.APIKeyEffectiveBillingMode(apiKey),
		PreferredSubscriptionID: apiKey.PreferredSubscriptionID,
	}, true
}

func batchImageError(c *gin.Context, err error) {
	status := infraerrors.Code(err)
	code := infraerrors.Reason(err)
	message := infraerrors.Message(err)
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
