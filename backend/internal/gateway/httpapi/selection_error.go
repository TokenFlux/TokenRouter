package httpapi

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// SelectionErrorResponse 保留原错误 envelope 的状态、类别和消息。
type SelectionErrorResponse struct {
	Status           int
	ErrType, Message string
	ModelNotFound    bool
}

func selectionErrorResponse(problem admission.SelectionProblem) SelectionErrorResponse {
	switch problem.Kind {
	case admission.SelectionModelNotFound:
		return SelectionErrorResponse{Status: http.StatusNotFound, ErrType: "model_not_found", Message: problem.Message, ModelNotFound: true}
	case admission.SelectionRateLimited:
		return SelectionErrorResponse{Status: http.StatusTooManyRequests, ErrType: "rate_limit_error", Message: problem.Message}
	default:
		return SelectionErrorResponse{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: problem.Message}
	}
}

// ClassifySelectionError 将核心诊断投影成原客户端错误；不执行存储写入。
func ClassifySelectionError(ctx context.Context, diag routing.ModelAvailabilityDiagnoser, groupID *int64, routingModel, displayModel, platform string) SelectionErrorResponse {
	return selectionErrorResponse(admission.DiagnoseSelection(ctx, diag, groupID, routingModel, displayModel, platform))
}
func RefineSelectionError(err error, fallback SelectionErrorResponse) SelectionErrorResponse {
	kind := admission.SelectionUnavailable
	if fallback.ModelNotFound {
		kind = admission.SelectionModelNotFound
	}
	problem := admission.RefineSelectionFailure(err, admission.SelectionProblem{Kind: kind, Message: fallback.Message})
	if problem.Kind == kind {
		return fallback
	}
	return selectionErrorResponse(problem)
}
