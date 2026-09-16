// Claude 的平台恢复只操作当前 attempt 的报文；开关和账号副作用由端口提供。
package antigravity

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

type ClaudeRecoveryInput struct {
	AccountID                             int64
	AccountName, Prefix, ProjectID, Model string
	Request                               protocolanthropic.ClaudeRequest
	InitialOptions                        TransformOptions
}
type ClaudeRecoveryOptions struct {
	Retry                                 func([]byte) (*http.Response, error)
	SignatureEnabled, BudgetEnabled       func(context.Context) bool
	TransformOptions                      func(context.Context) TransformOptions
	LogConfig                             func() (bool, int)
	ErrorDetail                           func([]byte) string
	ReadErrorBody                         func(*http.Response) []byte
	Observe                               func(RetryObservation)
	IsBudgetConstraint                    func(string) bool
	BudgetTokens, MinMaxTokens, MaxTokens int
	TruncateForLog                        func([]byte, int) string
	TruncateString                        func(string, int) string
}

func RecoverClaude(ctx context.Context, input ClaudeRecoveryInput, resp *http.Response, options ClaudeRecoveryOptions) *http.Response {
	if resp.StatusCode < 400 {
		return resp
	}
	respBody := options.ReadErrorBody(resp)
	claudeReq, projectID, mappedModel, transformOpts, prefix := input.Request, input.ProjectID, input.Model, input.InitialOptions, input.Prefix
	// 优先检测 thinking block 的 signature 相关错误（400）并重试一次：
	// Antigravity /v1internal 链路在部分场景会对 thought/thinking signature 做严格校验，
	// 当历史消息携带的 signature 不合法时会直接 400；去除 thinking 后可继续完成请求。
	if resp.StatusCode == http.StatusBadRequest && IsSignatureRelatedError(respBody) && options.SignatureEnabled(ctx) {
		upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
		upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
		logBody, maxBytes := options.LogConfig()
		upstreamDetail := options.ErrorDetail(respBody)
		options.Observe(RetryObservation{
			AccountID:          input.AccountID,
			AccountName:        input.AccountName,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "signature_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})

		// Conservative two-stage fallback:
		// 1) Disable top-level thinking + thinking->text
		// 2) Only if still signature-related 400: also downgrade tool_use/tool_result to text.

		retryStages := []struct {
			name  string
			strip func(*protocolanthropic.ClaudeRequest) (bool, error)
		}{
			{name: "thinking-only", strip: protocolanthropic.StripThinkingFromClaudeRequest},
			{name: "thinking+tools", strip: protocolanthropic.StripSignatureSensitiveBlocksFromClaudeRequest},
		}

		for _, stage := range retryStages {
			retryClaudeReq := claudeReq
			retryClaudeReq.Messages = append([]protocolanthropic.ClaudeMessage(nil), claudeReq.Messages...)

			stripped, stripErr := stage.strip(&retryClaudeReq)
			if stripErr != nil || !stripped {
				continue
			}

			logger.LegacyPrintf("service.antigravity_gateway", "Antigravity account %d: detected signature-related 400, retrying once (%s)", input.AccountID, stage.name)

			retryGeminiBody, txErr := TransformClaudeToGeminiWithOptions(&retryClaudeReq, projectID, mappedModel, options.TransformOptions(ctx))
			if txErr != nil {
				continue
			}
			retryResult, retryErr := options.Retry(retryGeminiBody)
			if retryErr != nil {
				options.Observe(RetryObservation{
					AccountID:          input.AccountID,
					AccountName:        input.AccountName,
					UpstreamStatusCode: 0,
					Kind:               "signature_retry_request_error",
					Message:            logredact.SanitizeUpstreamQueries(retryErr.Error()),
				})
				logger.LegacyPrintf("service.antigravity_gateway", "Antigravity account %d: signature retry request failed (%s): %v", input.AccountID, stage.name, retryErr)
				continue
			}

			retryResp := retryResult
			if retryResp.StatusCode < 400 {
				_ = resp.Body.Close()
				resp = retryResp
				respBody = nil
				break
			}

			retryBody, _ := io.ReadAll(io.LimitReader(retryResp.Body, 8<<10))
			_ = retryResp.Body.Close()
			if retryResp.StatusCode == http.StatusTooManyRequests {
				retryBaseURL := ""
				if retryResp.Request != nil && retryResp.Request.URL != nil {
					retryBaseURL = retryResp.Request.URL.Scheme + "://" + retryResp.Request.URL.Host
				}
				logger.LegacyPrintf("service.antigravity_gateway", "%s status=429 rate_limited base_url=%s retry_stage=%s body=%s", prefix, retryBaseURL, stage.name, options.TruncateForLog(retryBody, 200))
			}
			kind := "signature_retry"
			if strings.TrimSpace(stage.name) != "" {
				kind = "signature_retry_" + strings.ReplaceAll(stage.name, "+", "_")
			}
			retryUpstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(retryBody))
			retryUpstreamMsg = logredact.SanitizeUpstreamQueries(retryUpstreamMsg)
			retryUpstreamDetail := ""
			if logBody {
				retryUpstreamDetail = options.TruncateString(string(retryBody), maxBytes)
			}
			options.Observe(RetryObservation{
				AccountID:          input.AccountID,
				AccountName:        input.AccountName,
				UpstreamStatusCode: retryResp.StatusCode,
				UpstreamRequestID:  retryResp.Header.Get("x-request-id"),
				Kind:               kind,
				Message:            retryUpstreamMsg,
				Detail:             retryUpstreamDetail,
			})

			// If this stage fixed the signature issue, we stop; otherwise we may try the next stage.
			if retryResp.StatusCode != http.StatusBadRequest || !IsSignatureRelatedError(retryBody) {
				respBody = retryBody
				resp = &http.Response{
					StatusCode: retryResp.StatusCode,
					Header:     retryResp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(retryBody)),
				}
				break
			}

			// Still signature-related; capture context and allow next stage.
			respBody = retryBody
			resp = &http.Response{
				StatusCode: retryResp.StatusCode,
				Header:     retryResp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(retryBody)),
			}
		}
	}

	// Budget 整流：检测 budget_tokens 约束错误并自动修正重试
	if resp.StatusCode == http.StatusBadRequest && respBody != nil && !IsSignatureRelatedError(respBody) {
		errMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
		if options.IsBudgetConstraint(errMsg) && options.BudgetEnabled(ctx) {
			options.Observe(RetryObservation{
				AccountID:          input.AccountID,
				AccountName:        input.AccountName,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "budget_constraint_error",
				Message:            errMsg,
				Detail:             options.ErrorDetail(respBody),
			})

			// 修正 claudeReq 的 thinking 参数（adaptive 模式不修正）
			if claudeReq.Thinking == nil || claudeReq.Thinking.Type != "adaptive" {
				retryClaudeReq := claudeReq
				retryClaudeReq.Messages = append([]protocolanthropic.ClaudeMessage(nil), claudeReq.Messages...)
				// 创建新的 ThinkingConfig 避免修改原始 claudeReq.Thinking 指针
				retryClaudeReq.Thinking = &protocolanthropic.ThinkingConfig{
					Type:         "enabled",
					BudgetTokens: options.BudgetTokens,
				}
				if retryClaudeReq.MaxTokens < options.MinMaxTokens {
					retryClaudeReq.MaxTokens = options.MaxTokens
				}

				logger.LegacyPrintf("service.antigravity_gateway", "Antigravity account %d: detected budget_tokens constraint error, retrying with rectified budget (budget_tokens=%d, max_tokens=%d)", input.AccountID, options.BudgetTokens, options.MaxTokens)

				retryGeminiBody, txErr := TransformClaudeToGeminiWithOptions(&retryClaudeReq, projectID, mappedModel, transformOpts)
				if txErr == nil {
					retryResult, retryErr := options.Retry(retryGeminiBody)
					if retryErr == nil {
						retryResp := retryResult
						if retryResp.StatusCode < 400 {
							_ = resp.Body.Close()
							resp = retryResp
							respBody = nil
						} else {
							retryBody := options.ReadErrorBody(retryResp)
							_ = retryResp.Body.Close()
							respBody = retryBody
							resp = &http.Response{
								StatusCode: retryResp.StatusCode,
								Header:     retryResp.Header.Clone(),
								Body:       io.NopCloser(bytes.NewReader(retryBody)),
							}
						}
					} else {
						logger.LegacyPrintf("service.antigravity_gateway", "Antigravity account %d: budget rectifier retry failed: %v", input.AccountID, retryErr)
					}
				}
			}
		}
	}

	if resp.StatusCode >= 400 {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
	}
	return resp
}
