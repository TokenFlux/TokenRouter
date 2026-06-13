package admin

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/response"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type QoderModelSyncService interface {
	SyncModels(ctx context.Context, input service.QoderModelSyncInput) (*service.QoderModelSyncResult, error)
}

type QoderModelSyncHandler struct {
	qoderModelSyncService QoderModelSyncService
}

func NewQoderModelSyncHandler(qoderModelSyncService QoderModelSyncService) *QoderModelSyncHandler {
	return &QoderModelSyncHandler{qoderModelSyncService: qoderModelSyncService}
}

type QoderModelSyncRequest struct {
	Source string `json:"source"`
	Apply  bool   `json:"apply"`
}

// SyncModels previews or applies Qoder upstream model key sync.
// POST /api/v1/admin/qoder/models/sync
func (h *QoderModelSyncHandler) SyncModels(c *gin.Context) {
	if h == nil || h.qoderModelSyncService == nil {
		response.InternalError(c, "Qoder model sync service is not configured")
		return
	}
	var req QoderModelSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}
	result, err := h.qoderModelSyncService.SyncModels(c.Request.Context(), service.QoderModelSyncInput{
		Source: req.Source,
		Apply:  req.Apply,
	})
	if err != nil {
		response.BadRequest(c, "Qoder 模型同步失败: "+err.Error())
		return
	}
	response.Success(c, result)
}
