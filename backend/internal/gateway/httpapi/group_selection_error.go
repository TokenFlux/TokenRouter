package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeldisplay"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
)

// WriteGroupSelectionBusinessError 在原失败时点读取 Key 展示限制，不执行额外选号。
func WriteGroupSelectionBusinessError(c *gin.Context, err error, streamStarted bool, readAccess func(*gin.Context) (*apikey.APIKey, bool), catalogue modeldisplay.Catalog, writeError func(int, string, string, bool)) bool {
	if errors.Is(err, routing.ErrClaudeCodeOnly) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		writeError(http.StatusForbidden, "permission_error", routing.ErrClaudeCodeOnly.Error(), streamStarted)
		return true
	}

	var modelErr *routing.GroupModelUnsupportedError
	if errors.As(err, &modelErr) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		message := modelErr.Error()
		if apiKey, ok := readAccess(c); ok && apiKey != nil && apiKey.Group != nil && apiKey.Group.CustomModelsListEnabled() {
			platform := strings.TrimSpace(modelErr.Platform)
			if platform == "" {
				platform = apiKey.Group.Platform
			}
			availableModels := FilterModelsByCustomList(modelErr.AvailableModels, modeldisplay.DefaultModelIDs(catalogue, platform), apiKey.Group.ModelsListConfig.Models)
			message = (&routing.GroupModelUnsupportedError{
				RequestedModel:  modelErr.RequestedModel,
				AvailableModels: availableModels,
			}).Error()
		}
		writeError(http.StatusForbidden, "permission_error", message, streamStarted)
		return true
	}
	return false
}
