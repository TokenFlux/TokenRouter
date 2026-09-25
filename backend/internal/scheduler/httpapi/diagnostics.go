// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// AccountSchedulerDiagnostics 使用无凭据的只读诊断投影，评分由 scheduler 执行。
type AccountSchedulerDiagnostics interface {
	GetOverview(context.Context, int64) (*policy.AdvancedSchedulerScoreDiagnosticResponse, error)
	GetDetail(context.Context, int64, policy.AdvancedSchedulerScoreDiagnosticRequest) (*policy.AdvancedSchedulerScoreDiagnosticResponse, error)
}

// DiagnosticsHandler 只接收评分用例，不依赖账号管理聚合或存储。
type DiagnosticsHandler struct{ diagnostics AccountSchedulerDiagnostics }

func NewDiagnosticsHandler(source AccountSchedulerDiagnostics) *DiagnosticsHandler {
	return &DiagnosticsHandler{diagnostics: source}
}

// GetAdvancedSchedulerScore 返回账号所属高级调度分组的摘要，或指定分组的完整评分解释。
// GET /api/v1/admin/accounts/:id/advanced-scheduler-score?group_id=:groupID
func (h *DiagnosticsHandler) GetAdvancedSchedulerScore(c *gin.Context) {
	accountID, ok := parseAdvancedSchedulerScoreAccountID(c)
	if !ok {
		return
	}
	if h == nil || h.diagnostics == nil {
		response.InternalError(c, "Advanced scheduler diagnostics are unavailable")
		return
	}

	groupIDRaw := strings.TrimSpace(c.Query("group_id"))
	if groupIDRaw == "" {
		result, err := h.diagnostics.GetOverview(c.Request.Context(), accountID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, result)
		return
	}
	groupID, err := strconv.ParseInt(groupIDRaw, 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return
	}
	result, err := h.diagnostics.GetDetail(c.Request.Context(), accountID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: groupID})
	if err != nil {
		if strings.Contains(err.Error(), "advanced scheduler group") || strings.Contains(err.Error(), "group_id") {
			response.BadRequest(c, err.Error())
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// PreviewAdvancedSchedulerScore 使用安全、无状态的场景字段模拟高级调度评分。
// POST /api/v1/admin/accounts/:id/advanced-scheduler-score/preview
func (h *DiagnosticsHandler) PreviewAdvancedSchedulerScore(c *gin.Context) {
	accountID, ok := parseAdvancedSchedulerScoreAccountID(c)
	if !ok {
		return
	}
	if h == nil || h.diagnostics == nil {
		response.InternalError(c, "Advanced scheduler diagnostics are unavailable")
		return
	}

	var request policy.AdvancedSchedulerScoreDiagnosticRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.BadRequest(c, "Invalid advanced scheduler score preview request: "+err.Error())
		return
	}
	if err := ensureAdvancedSchedulerScorePreviewEOF(decoder); err != nil {
		response.BadRequest(c, "Invalid advanced scheduler score preview request: "+err.Error())
		return
	}
	if request.GroupID <= 0 {
		response.BadRequest(c, "group_id is required")
		return
	}

	result, err := h.diagnostics.GetDetail(c.Request.Context(), accountID, request)
	if err != nil {
		if strings.Contains(err.Error(), "advanced scheduler group") || strings.Contains(err.Error(), "group_id") || strings.Contains(err.Error(), "sticky account") || strings.Contains(err.Error(), "requested_model") {
			response.BadRequest(c, err.Error())
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func parseAdvancedSchedulerScoreAccountID(c *gin.Context) (int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return accountID, true
}

func ensureAdvancedSchedulerScorePreviewEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return io.ErrUnexpectedEOF
	}
	return err
}
