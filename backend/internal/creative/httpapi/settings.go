package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

type SettingsCandidates interface {
	ListCreativeModelCandidates(context.Context) ([]creative.CreativeModelCandidate, error)
}

// SettingsHandler 只读取任务领域投影，不依赖综合设置或聚合 handler。
type SettingsHandler struct {
	reader SettingsCandidates
	status func() creative.CreativeWorkerStatus
}

func NewSettingsHandler(reader SettingsCandidates, status func() creative.CreativeWorkerStatus) *SettingsHandler {
	return &SettingsHandler{reader: reader, status: status}
}
func (h *SettingsHandler) ListCreativeModelCandidates(c *gin.Context) {
	if h == nil || h.reader == nil {
		response.Error(c, 500, "creative model candidate service is not configured")
		return
	}
	values, err := h.reader.ListCreativeModelCandidates(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, values)
}
func (h *SettingsHandler) GetCreativeWorkerStatus(c *gin.Context) {
	if h == nil || h.status == nil {
		response.Error(c, 500, "setting service is not configured")
		return
	}
	response.Success(c, h.status())
}
