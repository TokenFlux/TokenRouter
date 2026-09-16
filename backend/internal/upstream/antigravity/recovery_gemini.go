// Gemini 方言保留模型兜底和签名恢复的原条件、顺序及单次预算。
package antigravity

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

type GeminiRecoveryInput struct {
	AccountID                                          int64
	AccountName, ProjectID, Model, Action, AccessToken string
	Body                                               []byte
}
type GeminiRecoveryOptions struct {
	Retry                             func([]byte) (*http.Response, error)
	Do                                func(*http.Request) (*http.Response, error)
	FallbackEnabled, SignatureEnabled func(context.Context) bool
	FallbackModel                     func(context.Context) string
	IsModelNotFound                   func(int, []byte) bool
	CleanSignatures                   func([]byte) []byte
	ReadErrorBody                     func(*http.Response) []byte
	ErrorDetail                       func([]byte) string
	Observe                           func(RetryObservation)
}
type GeminiRecoveryResult struct {
	Response    *http.Response
	ErrorBody   []byte
	ContentType string
}

func RecoverGemini(ctx context.Context, input GeminiRecoveryInput, resp *http.Response, options GeminiRecoveryOptions) (result GeminiRecoveryResult, failure error) {
	var respBody []byte
	contentType := resp.Header.Get("Content-Type")
	defer func() {
		result.Response = resp
		result.ErrorBody = respBody
		result.ContentType = contentType
		if failure != nil && resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode < 400 {
		return result, nil
	}
	respBody = options.ReadErrorBody(resp)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	projectID, mappedModel, injectedBody, upstreamAction, accessToken := input.ProjectID, input.Model, input.Body, input.Action, input.AccessToken
	// 模型兜底：模型不存在且开启 fallback 时，自动用 fallback 模型重试一次
	if options.FallbackEnabled(ctx) &&
		options.IsModelNotFound(resp.StatusCode, respBody) {
		fallbackModel := options.FallbackModel(ctx)
		if fallbackModel != "" && fallbackModel != mappedModel {
			logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] Model not found (%s), retrying with fallback model %s (account: %s)", mappedModel, fallbackModel, input.AccountName)

			fallbackWrapped, err := WrapV1InternalRequest(projectID, fallbackModel, injectedBody)
			if err == nil {
				fallbackReq, err := NewAPIRequest(ctx, upstreamAction, accessToken, fallbackWrapped)
				if err == nil {
					fallbackResp, err := options.Do(fallbackReq)
					if err == nil && fallbackResp.StatusCode < 400 {
						_ = resp.Body.Close()
						resp = fallbackResp
					} else if fallbackResp != nil {
						_ = fallbackResp.Body.Close()
					}
				}
			}
		}
	}

	// Gemini 原生请求中的 thoughtSignature 可能来自旧上下文/旧账号，触发上游严格校验后返回
	// "Corrupted thought signature."。检测到此类 400 时，将 thoughtSignature 清理为 dummy 值后重试一次。
	signatureCheckBody := respBody
	if unwrapped, unwrapErr := (&ResponseAdapter{}).UnwrapV1InternalResponse(respBody); unwrapErr == nil && len(unwrapped) > 0 {
		signatureCheckBody = unwrapped
	}
	if resp.StatusCode == http.StatusBadRequest &&
		options.SignatureEnabled(ctx) &&
		IsSignatureRelatedError(signatureCheckBody) &&
		bytes.Contains(injectedBody, []byte(`"thoughtSignature"`)) {
		upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(googlewire.ExtractPlatformMessage(signatureCheckBody)))
		upstreamDetail := options.ErrorDetail(signatureCheckBody)
		options.Observe(RetryObservation{
			AccountID:          input.AccountID,
			AccountName:        input.AccountName,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "signature_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})

		logger.LegacyPrintf("service.antigravity_gateway", "Antigravity Gemini account %d: detected signature-related 400, retrying with cleaned thought signatures", input.AccountID)

		cleanedInjectedBody := options.CleanSignatures(injectedBody)
		retryWrappedBody, wrapErr := WrapV1InternalRequest(projectID, mappedModel, cleanedInjectedBody)
		if wrapErr == nil {
			retryResult, retryErr := options.Retry(retryWrappedBody)
			if retryErr == nil {
				retryResp := retryResult
				if retryResp.StatusCode < 400 {
					resp = retryResp
				} else {
					retryRespBody := options.ReadErrorBody(retryResp)
					_ = retryResp.Body.Close()
					retryOpsBody := retryRespBody
					if retryUnwrapped, unwrapErr := (&ResponseAdapter{}).UnwrapV1InternalResponse(retryRespBody); unwrapErr == nil && len(retryUnwrapped) > 0 {
						retryOpsBody = retryUnwrapped
					}
					options.Observe(RetryObservation{
						AccountID:          input.AccountID,
						AccountName:        input.AccountName,
						UpstreamStatusCode: retryResp.StatusCode,
						UpstreamRequestID:  retryResp.Header.Get("x-request-id"),
						Kind:               "signature_retry",
						Message:            logredact.SanitizeUpstreamQueries(strings.TrimSpace(googlewire.ExtractPlatformMessage(retryOpsBody))),
						Detail:             options.ErrorDetail(retryOpsBody),
					})
					respBody = retryRespBody
					resp = &http.Response{
						StatusCode: retryResp.StatusCode,
						Header:     retryResp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(retryRespBody)),
					}
					contentType = resp.Header.Get("Content-Type")
				}
			} else {
				if switchErr, ok := IsAntigravityAccountSwitchError(retryErr); ok {
					options.Observe(RetryObservation{
						AccountID:          input.AccountID,
						AccountName:        input.AccountName,
						UpstreamStatusCode: http.StatusServiceUnavailable,
						Kind:               "failover",
						Message:            logredact.SanitizeUpstreamQueries(retryErr.Error()),
					})
					return result, switchErr
				}
				options.Observe(RetryObservation{
					AccountID:          input.AccountID,
					AccountName:        input.AccountName,
					UpstreamStatusCode: 0,
					Kind:               "signature_retry_request_error",
					Message:            logredact.SanitizeUpstreamQueries(retryErr.Error()),
				})
				logger.LegacyPrintf("service.antigravity_gateway", "Antigravity Gemini account %d: signature retry request failed: %v", input.AccountID, retryErr)
			}
		} else {
			logger.LegacyPrintf("service.antigravity_gateway", "Antigravity Gemini account %d: signature retry wrap failed: %v", input.AccountID, wrapErr)
		}
	}

	return result, nil
}
