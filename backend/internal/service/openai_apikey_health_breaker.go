package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func classifyOpenAIAPIKeyHealthFailure(err error) (int, []byte, bool) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, nil, false
	}

	var failoverErr *forwardcore.UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		// These failures already have dedicated recovery/state handling or are not
		// attributable to the selected account.
		if failoverErr.IsCredentialFailure() ||
			failoverErr.RequestScopedTransient ||
			failoverErr.RetryableOnSameAccount ||
			failoverErr.Scope == forwardcore.GatewayFailureScopeRequest ||
			failoverErr.Scope == forwardcore.GatewayFailureScopeProvider {
			return failoverErr.StatusCode, failoverErr.ResponseBody, false
		}
		if failoverErr.StatusCode == http.StatusTooManyRequests || failoverErr.StatusCode >= http.StatusInternalServerError {
			return failoverErr.StatusCode, failoverErr.ResponseBody, true
		}
		return failoverErr.StatusCode, failoverErr.ResponseBody, false
	}

	var imageErr *openai.OpenAIImagesUpstreamError
	if errors.As(err, &imageErr) {
		if imageErr.StatusCode == http.StatusTooManyRequests || imageErr.StatusCode >= http.StatusInternalServerError {
			return imageErr.StatusCode, []byte(strings.TrimSpace(imageErr.Message)), true
		}
	}
	return 0, nil, false
}

// 网关错误归因暂留调用侧，窗口计数和健康状态只由 account 持有。
func (s *RateLimitService) ObserveOpenAIAPIKeyHealthFailure(ctx context.Context, value *Account, err error) bool {
	if s == nil {
		return false
	}
	status, body, eligible := classifyOpenAIAPIKeyHealthFailure(err)
	record := AccountRecordView(value)
	handled := s.HealthCore().ApplyAPIKeyHealthFailure(ctx, record, status, body, eligible)
	if value != nil && record != nil {
		value.TempUnschedulableUntil = record.TempUnschedulableUntil
		value.TempUnschedulableReason = record.TempUnschedulableReason
	}
	return handled
}
func (s *RateLimitService) ObserveOpenAIAPIKeyHealthSuccess(ctx context.Context, value *Account) {
	// 保留成功路径不读设置、不碰 Redis；原生用例同样不重置滚动计数。
}
