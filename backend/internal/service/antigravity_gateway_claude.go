package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/gin-gonic/gin"
)

// Forward 转发 Claude 协议请求（Claude → Gemini 转换）
//
// 限流处理流程:
//
//	请求 → antigravityRetryLoop → 预检查(remaining>0? → 切换账号) → 发送上游
//	  ├─ 成功 → 正常返回
//	  └─ 429/503 → handleSmartRetry
//	      ├─ retryDelay >= 7s → 设置模型限流 + 清除粘性绑定 → 切换账号
//	      └─ retryDelay <  7s → 等待后重试 1 次
//	          ├─ 成功 → 正常返回
//	          └─ 失败 → 设置模型限流 + 清除粘性绑定 → 切换账号
func (s *AntigravityGatewayService) Forward(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, isStickySession bool) (*forwardcore.MessagesResult, error) {
	// 上游透传账号直接转发，不走 OAuth token 刷新
	if account.Record.Type == capability.AccountTypeUpstream {
		return s.ForwardUpstream(ctx, c, account, body)
	}

	startTime := time.Now()

	sessionID := getSessionID(c)
	prefix := logPrefix(sessionID, account.Record.Name)

	// 解析 Claude 请求
	var claudeReq protocolanthropic.ClaudeRequest
	if err := json.Unmarshal(body, &claudeReq); err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(claudeReq.Model) == "" {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Missing model")
	}

	originalModel := claudeReq.Model
	// thinking 状态必须参与最终模型解析，确保调度限制与真正转发的模型一致。
	thinkingEnabled := claudeReq.Thinking != nil && (claudeReq.Thinking.Type == "enabled" || claudeReq.Thinking.Type == "adaptive")
	modelCtx := requeststate.WithThinkingEnabled(ctx, thinkingEnabled)
	mappedModel := gatewayprovider.ExecutionModelPolicy(account).FinalAntigravityModel(modelCtx, claudeReq.Model)
	if mappedModel == "" {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
		return nil, s.writeClaudeError(c, http.StatusForbidden, "permission_error", fmt.Sprintf("model %s not in whitelist", claudeReq.Model))
	}
	billingModel := mappedModel

	// 获取 access_token
	if s.tokenProvider == nil {
		return nil, s.writeClaudeError(c, http.StatusBadGateway, "api_error", "Antigravity token provider not configured")
	}
	accessToken, err := accountToken(ctx, s.tokenProvider, account)
	if err != nil {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"error":{"type":"authentication_error","message":"Failed to get upstream access token"},"type":"error"}`),
		}
	}

	projectID, err := resolveAntigravityProjectID(account)
	if err != nil {
		_ = s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	// 代理 URL
	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	// 获取转换选项
	// Antigravity 上游要求必须包含身份提示词，否则会返回 429
	transformOpts := s.getClaudeTransformOptions(ctx)
	transformOpts.EnableIdentityPatch = true // 强制启用，Antigravity 上游必需

	// 转换 Claude 请求为 Gemini 格式
	geminiBody, err := antigravity.TransformClaudeToGeminiWithOptions(&claudeReq, projectID, mappedModel, transformOpts)
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request")
	}

	// Antigravity 上游只支持流式请求，统一使用 streamGenerateContent
	// 如果客户端请求非流式，在响应处理阶段会收集完整流式响应后转换返回
	action := "streamGenerateContent"

	// 执行带重试的请求
	retry, params := s.antigravityRetryAdapter(antigravityRetryLoopParams{
		ctx:             ctx,
		prefix:          prefix,
		account:         account,
		proxyURL:        proxyURL,
		accessToken:     accessToken,
		action:          action,
		body:            geminiBody,
		c:               c,
		httpUpstream:    s.httpUpstream,
		settingService:  s.settingService,
		accountRepo:     s.accountRepo,
		handleError:     s.handleUpstreamError,
		requestedModel:  originalModel,
		isStickySession: isStickySession, // Forward 由上层判断粘性会话
		groupID:         0,               // Forward 方法没有 groupID，由上层处理粘性会话清除
		sessionHash:     "",              // Forward 方法没有 sessionHash，由上层处理粘性会话清除
	})
	target := &antigravity.Target{AccountID: account.Record.ID, Model: billingModel, Mode: antigravity.ModeClaudeResponse, StartedAt: startTime, Response: s.antigravityResponseAdapter(c).Options, Enter: s.nativeAttemptActivity}
	target.Exchange = func(context.Context) (*http.Response, error) {
		result, err := retry.AntigravityRetryLoop(params)
		if err != nil {
			// 检查是否是账号切换信号，转换为 UpstreamFailoverError 让 Handler 切换账号
			if switchErr, ok := antigravity.IsAntigravityAccountSwitchError(err); ok {
				return nil, &forwardcore.UpstreamFailoverError{
					StatusCode:        http.StatusServiceUnavailable,
					ForceCacheBilling: switchErr.IsStickySession,
				}
			}
			// 区分客户端取消和真正的上游失败，返回更准确的错误消息
			if c.Request.Context().Err() != nil {
				return nil, s.writeClaudeError(c, http.StatusBadGateway, "client_disconnected", "Client disconnected before upstream response")
			}
			return nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed after retries")
		}
		options := antigravity.ClaudeRecoveryOptions{Retry: func(body []byte) (*http.Response, error) {
			next := params
			next.Body = body
			value, err := retry.AntigravityRetryLoop(next)
			if err != nil {
				return nil, err
			}
			return value.Resp, nil
		}, SignatureEnabled: s.settingService.Gateway.IsSignatureRectifierEnabled, BudgetEnabled: s.settingService.Gateway.IsBudgetRectifierEnabled, TransformOptions: s.getClaudeTransformOptions, LogConfig: s.getLogConfig, ErrorDetail: s.getUpstreamErrorDetail, ReadErrorBody: s.readUpstreamErrorBody, Observe: retry.Options.Observe, IsBudgetConstraint: anthropic.IsThinkingBudgetConstraintError, BudgetTokens: anthropic.BudgetRectifyBudgetTokens, MinMaxTokens: anthropic.BudgetRectifyMinMaxTokens, MaxTokens: anthropic.BudgetRectifyMaxTokens, TruncateForLog: truncateForLog, TruncateString: logredact.TruncateUTF8}
		return antigravity.RecoverClaude(ctx, antigravity.ClaudeRecoveryInput{AccountID: account.Record.ID, AccountName: account.Record.Name, Prefix: prefix, ProjectID: projectID, Model: mappedModel, Request: claudeReq, InitialOptions: transformOpts}, result.Resp, options), nil
	}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response) (bool, error) {
		if resp.StatusCode < 400 {
			return false, nil
		}
		respBody := s.readUpstreamErrorBody(resp)
		if resp.StatusCode >= 400 {
			// 检测 prompt too long 错误，返回特殊错误类型供上层 fallback
			if resp.StatusCode == http.StatusBadRequest && antigravity.IsPromptTooLongError(respBody) {
				upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
				upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
				upstreamDetail := s.getUpstreamErrorDetail(respBody)
				logBody, maxBytes := s.getLogConfig()
				if logBody {
					logging.LegacyPrintf("service.antigravity_gateway", "%s status=400 prompt_too_long=true upstream_message=%q request_id=%s body=%s", prefix, upstreamMsg, resp.Header.Get("x-request-id"), truncateForLog(respBody, maxBytes))
				}
				gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
					Platform:           account.Record.Platform,
					AccountID:          account.Record.ID,
					AccountName:        account.Record.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "prompt_too_long",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})
				return true, &antigravity.PromptTooLongError{
					StatusCode: resp.StatusCode,
					RequestID:  resp.Header.Get("x-request-id"),
					Body:       respBody,
				}
			}

			s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody, originalModel, 0, "", isStickySession)

			// 精确匹配服务端配置类 400 错误，触发同账号重试 + failover
			if resp.StatusCode == http.StatusBadRequest {
				msg := strings.ToLower(strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody)))
				if upstream.IsGoogleProjectConfigError(msg) {
					upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody)))
					upstreamDetail := s.getUpstreamErrorDetail(respBody)
					log.Printf("%s status=400 google_config_error failover=true upstream_message=%q account=%d", prefix, upstreamMsg, account.Record.ID)
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
						Platform:           account.Record.Platform,
						AccountID:          account.Record.ID,
						AccountName:        account.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  resp.Header.Get("x-request-id"),
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})
					return true, &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: true}
				}
			}

			if s.shouldFailoverUpstreamError(resp.StatusCode) {
				upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
				upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
				upstreamDetail := s.getUpstreamErrorDetail(respBody)
				gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
					Platform:           account.Record.Platform,
					AccountID:          account.Record.ID,
					AccountName:        account.Record.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "failover",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})
				return true, &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody}
			}

			return true, s.writeMappedClaudeError(c, account, resp.StatusCode, resp.Header.Get("x-request-id"), respBody)
		}
		return false, nil
	}
	target.OutputError = func(err error) {
		kind := "stream_collect_error"
		if claudeReq.Stream {
			kind = "stream_error"
		}
		logging.LegacyPrintf("service.antigravity_gateway", "%s status=%s error=%v", prefix, kind, err)
	}
	result, err := (antigravity.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, Body: geminiBody, ResponseModel: originalModel, Stream: claudeReq.Stream, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	return &forwardcore.MessagesResult{RequestID: result.RequestID, UpstreamHeaders: result.UpstreamHeaders, Usage: result.Usage, Model: originalModel, UpstreamModel: billingModel, Stream: claudeReq.Stream, Duration: result.Duration, FirstTokenMs: result.FirstTokenMs, ClientDisconnect: result.ClientDisconnect}, nil
}
