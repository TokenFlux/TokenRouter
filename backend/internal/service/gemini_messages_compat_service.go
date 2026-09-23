package service

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"

	upstream "github.com/TokenFlux/TokenRouter/internal/upstream"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/gin-gonic/gin"
)

const (
	geminiMaxRetries     = 5
	geminiRetryBaseDelay = 1 * time.Second
	geminiRetryMaxDelay  = 16 * time.Second
)

const geminiAppliedTempPolicyHeader = "X-TokenRouter-Internal-Temp-Policy-Applied"

// Gemini tool calling now requires `thoughtSignature` in parts that include `functionCall`.
// Many clients don't send it; we inject a known dummy signature to satisfy the validator.
// Ref: https://ai.google.dev/gemini-api/docs/thought-signatures
const geminiDummyThoughtSignature = "skip_thought_signature_validator"

type GeminiMessagesCompatService struct {
	quotaPrecheck         *accountcore.GeminiPrecheck
	nativeAttemptActivity func() (func(), error)
	accountRepo           gatewayprovider.ExecutionAccountStore

	tokenProvider  *accountcore.GeminiTokenSource
	healthObserver *accountprovider.UpstreamHealth

	httpUpstream              httpclient.UpstreamTransport
	antigravityGatewayService *AntigravityGatewayService
	cfg                       *config.Config
	responseHeaderFilter      *egress.CompiledHeaderFilter
}

func (s *GeminiMessagesCompatService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	limit := gatewayUpstreamErrorBodyReadLimit
	if s != nil && s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody && s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body
}

func NewGeminiMessagesCompatService(
	accountRepo gatewayprovider.ExecutionAccountStore,

	tokenProvider *accountcore.GeminiTokenSource,
	healthObserver *accountprovider.UpstreamHealth,
	httpUpstream httpclient.UpstreamTransport,
	antigravityGatewayService *AntigravityGatewayService,
	cfg *config.Config, headerFilter *egress.CompiledHeaderFilter,
) *GeminiMessagesCompatService {
	return &GeminiMessagesCompatService{
		accountRepo: accountRepo,

		tokenProvider:             tokenProvider,
		healthObserver:            healthObserver,
		httpUpstream:              httpUpstream,
		antigravityGatewayService: antigravityGatewayService,
		cfg:                       cfg,
		responseHeaderFilter:      headerFilter,
	}
}

// GetTokenProvider returns the token provider for OAuth accounts
func (s *GeminiMessagesCompatService) GetTokenProvider() *accountcore.GeminiTokenSource {
	return s.tokenProvider
}

func (s *GeminiMessagesCompatService) GetAntigravityGatewayService() *AntigravityGatewayService {
	return s.antigravityGatewayService
}

func (s *GeminiMessagesCompatService) validateUpstreamBaseURL(raw string) (string, error) {
	if s.cfg != nil && !s.cfg.Security.URLAllowlist.Enabled {
		normalized, err := egress.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

func (s *GeminiMessagesCompatService) Forward(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) (*forwardcore.MessagesResult, error) {
	beginGeminiImageOutputObservation(c)
	startTime := time.Now()

	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("missing model")
	}

	originalModel := req.Model
	// 所有 Gemini 账号类型都执行账号模型映射，OAuth 也必须与调度和可见模型解析保持一致。
	mappedModel := resolveAccountMappedModelForForward(account, req.Model)

	geminiReq, err := convertClaudeMessagesToGeminiGenerateContent(body)
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	geminiReq = ensureGeminiFunctionCallThoughtSignatures(geminiReq)
	originalClaudeBody := body

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	requestIDHeader := "x-request-id"
	switch account.Record.Type {
	case capability.AccountTypeAPIKey, capability.AccountTypeOAuth, capability.AccountTypeServiceAccount:
	default:
		return nil, fmt.Errorf("unsupported account type: %s", account.Record.Type)
	}
	useUpstreamStream := req.Stream
	if account.Record.Type == capability.AccountTypeOAuth && !req.Stream && strings.TrimSpace(account.View().GetCredential("project_id")) != "" {
		useUpstreamStream = true
	}
	plan := s.geminiRequestPlan(account, mappedModel, "", false, req.Stream, useUpstreamStream, false)
	buildReq := func(ctx context.Context) (*http.Request, string, error) {
		return gemininative.BuildRequest(ctx, geminiReq, plan)
	}

	options := s.geminiExchangeOptions(c, ctx, account, mappedModel, geminiExchangeMessages, gemininative.OpenAICompatChatCompletions)
	options.Build = buildReq
	options.RequestIDHeader = requestIDHeader
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
	}
	options.FilterThinking = func() []byte { return gatewayprovider.FilterThinkingBlocksForRetry(originalClaudeBody, originalModel) }
	options.FilterTools = func() []byte {
		return gatewayprovider.FilterSignatureSensitiveBlocksForRetry(originalClaudeBody, originalModel)
	}
	options.ReplaceBody = func(value []byte) { geminiReq = value }
	var requestID string
	var compatibilityResult *forwardcore.MessagesResult
	stopped := false
	target := &gemininative.Target{AccountID: account.Record.ID, Model: mappedModel, Mode: gemininative.MessagesResponse, Exchange: options, Response: s.geminiResponseAdapter(c).Options, StartedAt: startTime, UpstreamStream: useUpstreamStream, OAuth: account.Record.Type == capability.AccountTypeOAuth, Enter: s.nativeAttemptActivity}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response, requestIDHeader string) (bool, error) {
		var callbackErr error
		compatibilityResult, callbackErr = func() (*forwardcore.MessagesResult, error) {

			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				if decision.Policy == accountcore.ErrorPolicyCustomSkipped || decision.Policy == accountcore.ErrorPolicyPoolBypassed {
					if failoverErr := s.skippedErrorPolicyFailoverError(c, account, resp.StatusCode, respBody, upstreamReqID); failoverErr != nil {
						return nil, failoverErr
					}
					if decision.Policy == accountcore.ErrorPolicyCustomSkipped {
						return nil, s.writeGeminiCustomCodeSkippedError(c, account, resp.StatusCode, upstreamReqID, respBody, func() {
							_ = s.writeClaudeError(c, http.StatusInternalServerError, "api_error", geminiCustomCodeSkippedClientMessage)
						})
					}
					return nil, s.writeGeminiMappedError(c, account, resp.StatusCode, upstreamReqID, respBody)
				}
				if decision.ShouldReturnGenericError() {
					genericBody := []byte(`{"error":{"message":"Upstream gateway error"}}`)
					return nil, s.writeGeminiMappedError(c, account, http.StatusInternalServerError, upstreamReqID, genericBody)
				}
				msg400 := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
				googleConfigError := resp.StatusCode == http.StatusBadRequest && upstream.IsGoogleProjectConfigError(msg400)
				defaultFailover := googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)
				if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, defaultFailover) {
					upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
					upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
					upstreamDetail := ""
					if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
						maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
					}
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
						Platform:           account.Record.Platform,
						AccountID:          account.Record.ID,
						AccountName:        account.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  upstreamReqID,
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})
					if googleConfigError {
						log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q account=%d", upstreamMsg, account.Record.ID)
					}
					return nil, &forwardcore.UpstreamFailoverError{
						StatusCode:             resp.StatusCode,
						ResponseBody:           respBody,
						RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
					}
				}
				return nil, s.writeGeminiMappedError(c, account, resp.StatusCode, upstreamReqID, respBody)
			}

			requestID = resp.Header.Get(requestIDHeader)
			if requestID == "" {
				requestID = resp.Header.Get("x-goog-request-id")
			}
			if requestID != "" {
				c.Header("x-request-id", requestID)
			}

			return nil, nil
		}()
		stopped = resp.StatusCode >= 400 || callbackErr != nil || compatibilityResult != nil
		return stopped, callbackErr
	}
	result, executeErr := (gemininative.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolAnthropicMessages, Body: body, Stream: req.Stream, ResponseModel: originalModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if stopped {
		return compatibilityResult, executeErr
	}
	if executeErr != nil {
		return nil, executeErr
	}
	requestID = result.RequestID
	usage := &result.Usage
	firstTokenMs := result.FirstTokenMs

	// 图片生成计费
	imageInputSize := s.extractImageInputSize(body)
	imageSize := media.NormalizeImageSizeTier(imageInputSize)
	imageCount := resolveGeminiImageCount(c, originalModel, mappedModel)

	return &forwardcore.MessagesResult{
		RequestID:       requestID,
		UpstreamHeaders: result.UpstreamHeaders,
		Usage:           *usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		Stream:          req.Stream,
		Duration:        result.Duration,
		FirstTokenMs:    firstTokenMs,
		ImageCount:      imageCount,
		ImageSize:       imageSize,
		ImageInputSize:  imageInputSize,
	}, nil
}

func (s *GeminiMessagesCompatService) ForwardNative(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error) {
	beginGeminiImageOutputObservation(c)
	startTime := time.Now()

	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing model in URL")
	}
	if strings.TrimSpace(action) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing action in URL")
	}
	if len(body) == 0 {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Request body is empty")
	}

	// 过滤掉 parts 为空的消息（Gemini API 不接受空 parts）
	if filteredBody, err := gemini.FilterEmptyParts(body); err == nil {
		body = filteredBody
	}

	switch action {
	case "generateContent", "streamGenerateContent", "countTokens":
		// ok
	default:
		return nil, s.writeGoogleError(c, http.StatusNotFound, "Unsupported action: "+action)
	}

	// Some Gemini upstreams validate tool call parts strictly; ensure any `functionCall` part includes a
	// `thoughtSignature` to avoid frequent INVALID_ARGUMENT 400s.
	body = ensureGeminiFunctionCallThoughtSignatures(body)

	// 渠道映射后的模型进入账号后统一解析为最终上游模型，不按凭据类型跳过。
	mappedModel := resolveAccountMappedModelForForward(account, originalModel)

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	useUpstreamStream := stream
	upstreamAction := action
	if account.Record.Type == capability.AccountTypeOAuth && !stream && action == "generateContent" && strings.TrimSpace(account.View().GetCredential("project_id")) != "" {
		// Code Assist's non-streaming generateContent may return no content; use streaming upstream and aggregate.
		useUpstreamStream = true
		upstreamAction = "streamGenerateContent"
	}
	forceAIStudio := action == "countTokens"

	requestIDHeader := "x-request-id"
	switch account.Record.Type {
	case capability.AccountTypeAPIKey, capability.AccountTypeOAuth, capability.AccountTypeServiceAccount:
	default:
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "Unsupported account type: "+account.Record.Type)
	}
	plan := s.geminiRequestPlan(account, mappedModel, upstreamAction, true, stream, useUpstreamStream, forceAIStudio)
	buildReq := func(ctx context.Context) (*http.Request, string, error) {
		return gemininative.BuildRequest(ctx, body, plan)
	}

	options := s.geminiExchangeOptions(c, ctx, account, mappedModel, geminiExchangeNative, gemininative.OpenAICompatChatCompletions)
	options.Build = buildReq
	options.RequestIDHeader = requestIDHeader
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
	}
	options.CountFallback = action == "countTokens"
	options.EstimateCount = func() int { return gemininative.EstimateGeminiCountTokens(body) }
	var requestID string
	var compatibilityResult *forwardcore.MessagesResult
	stopped := false
	target := &gemininative.Target{AccountID: account.Record.ID, Model: mappedModel, Mode: gemininative.NativeResponse, Exchange: options, Response: s.geminiResponseAdapter(c).Options, StartedAt: startTime, UpstreamStream: useUpstreamStream, OAuth: account.Record.Type == capability.AccountTypeOAuth, Enter: s.nativeAttemptActivity}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response, requestIDHeader string) (bool, error) {
		var callbackErr error
		compatibilityResult, callbackErr = func() (*forwardcore.MessagesResult, error) {

			requestID = resp.Header.Get(requestIDHeader)
			if requestID == "" {
				requestID = resp.Header.Get("x-goog-request-id")
			}
			if requestID != "" {
				c.Header("x-request-id", requestID)
			}

			isOAuth := account.Record.Type == capability.AccountTypeOAuth

			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				// Best-effort fallback for OAuth tokens missing AI Studio scopes when calling countTokens.
				// This avoids Gemini SDKs failing hard during preflight token counting.
				// Checked before error policy so it always works regardless of custom error codes.
				if action == "countTokens" && isOAuth && gemininative.IsGeminiInsufficientScope(resp.Header, respBody) {
					estimated := gemininative.EstimateGeminiCountTokens(body)
					c.JSON(http.StatusOK, map[string]any{"totalTokens": estimated})
					return &forwardcore.MessagesResult{
						RequestID:       requestID,
						UpstreamHeaders: resp.Header,
						Usage:           upstream.TokenUsage{},
						Model:           originalModel,
						UpstreamModel:   mappedModel,
						Stream:          false,
						Duration:        time.Since(startTime),
						FirstTokenMs:    nil,
					}, nil
				}

				decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
				if decision.Policy == accountcore.ErrorPolicyCustomSkipped || decision.Policy == accountcore.ErrorPolicyPoolBypassed {
					if failoverErr := s.skippedErrorPolicyFailoverError(c, account, resp.StatusCode, respBody, requestID); failoverErr != nil {
						return nil, failoverErr
					}
					if decision.Policy == accountcore.ErrorPolicyCustomSkipped {
						return nil, s.writeGeminiCustomCodeSkippedError(c, account, resp.StatusCode, requestID, respBody, func() {
							_ = s.writeGoogleError(c, http.StatusInternalServerError, geminiCustomCodeSkippedClientMessage)
						})
					}
					return nil, s.writeGeminiNativeUpstreamError(c, account, resp, respBody, requestID, isOAuth)
				}
				if decision.ShouldReturnGenericError() {
					gatewayhttp.MarkResponseCommitted(c)
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": gin.H{
							"code":    http.StatusInternalServerError,
							"message": "Upstream gateway error",
							"status":  "INTERNAL",
						},
					})
					return nil, fmt.Errorf("gemini upstream error: %d (not in custom error codes)", resp.StatusCode)
				}
				msg400 := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
				googleConfigError := resp.StatusCode == http.StatusBadRequest && upstream.IsGoogleProjectConfigError(msg400)
				defaultFailover := googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)
				if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, defaultFailover) {
					evBody := gemininative.UnwrapIfNeeded(isOAuth, respBody)
					upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(evBody))
					upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
					upstreamDetail := ""
					if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
						maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = logredact.TruncateUTF8(string(evBody), maxBytes)
					}
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
						Platform:           account.Record.Platform,
						AccountID:          account.Record.ID,
						AccountName:        account.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  requestID,
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})
					if googleConfigError {
						log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q account=%d", upstreamMsg, account.Record.ID)
					}
					return nil, &forwardcore.UpstreamFailoverError{
						StatusCode:             resp.StatusCode,
						ResponseBody:           evBody,
						RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
					}
				}

				return nil, s.writeGeminiNativeUpstreamError(c, account, resp, respBody, requestID, isOAuth)
			}

			return nil, nil
		}()
		stopped = resp.StatusCode >= 400 || callbackErr != nil || compatibilityResult != nil
		return stopped, callbackErr
	}
	result, executeErr := (gemininative.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolGeminiGenerateContent, Body: body, Stream: stream, ResponseModel: originalModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if stopped {
		return compatibilityResult, executeErr
	}
	if executeErr != nil {
		return nil, executeErr
	}
	if result.EstimatedTokenCount != nil {
		return &forwardcore.MessagesResult{Usage: upstream.TokenUsage{}, Model: originalModel, UpstreamModel: mappedModel, Stream: false, Duration: result.Duration}, nil
	}
	requestID = result.RequestID
	usage := &result.Usage
	firstTokenMs := result.FirstTokenMs

	// 图片生成计费
	imageInputSize := s.extractImageInputSize(body)
	imageSize := media.NormalizeImageSizeTier(imageInputSize)
	imageCount := resolveGeminiImageCount(c, originalModel, mappedModel)

	return &forwardcore.MessagesResult{
		RequestID:       requestID,
		UpstreamHeaders: result.UpstreamHeaders,
		Usage:           *usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		Stream:          result.Stream,
		Duration:        result.Duration,
		FirstTokenMs:    firstTokenMs,
		ImageCount:      imageCount,
		ImageSize:       imageSize,
		ImageInputSize:  imageInputSize,
	}, nil
}

// checkErrorPolicyInLoop 在重试循环内预检查错误策略。
// 返回 true 表示策略已匹配（调用者应 break），resp 已重建可直接使用。
// 返回 false 表示 ErrorPolicyNone，resp 已重建，调用者继续走重试逻辑。
func (s *GeminiMessagesCompatService) checkErrorPolicyInLoop(
	ctx context.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, mappedModel string,
) (matched bool, rebuilt *http.Response) {
	if resp.StatusCode < 400 || s.healthObserver == nil {
		return false, resp
	}
	body := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	rebuilt = &http.Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	policy := s.healthObserver.CheckErrorPolicy(ctx, gatewayprovider.ExecutionRecord(account), gatewayprovider.HealthObservationFromContext(ctx, resp.StatusCode, nil, body, []string{mappedModel}))
	if policy == accountcore.ErrorPolicyTempUnscheduled {
		// CheckErrorPolicy 已写入临时不可调度状态，给最终错误处理留下内部标记，
		// 避免同一个响应再次执行规则并重复写库。
		rebuilt.Header.Set(geminiAppliedTempPolicyHeader, "1")
	}
	// 池模式由 handler 层按账号配置的重试预算处理，不能再叠加 Gemini 固定内部重试。
	return policy != accountcore.ErrorPolicyNone, rebuilt
}

func (s *GeminiMessagesCompatService) shouldRetryGeminiUpstreamError(account *gatewayprovider.ExecutionAccount, statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	case 403:
		// GeminiCli OAuth occasionally returns 403 transiently (activation/quota propagation); allow retry.
		if account == nil || account.Record.Type != capability.AccountTypeOAuth {
			return false
		}
		oauthType := strings.ToLower(strings.TrimSpace(account.View().GetCredential("oauth_type")))
		if oauthType == "" && strings.TrimSpace(account.View().GetCredential("project_id")) != "" {
			// Legacy/implicit Code Assist OAuth accounts.
			oauthType = "code_assist"
		}
		return oauthType == "code_assist"
	default:
		return false
	}
}

func (s *GeminiMessagesCompatService) shouldFailoverGeminiUpstreamError(statusCode int) bool {
	switch statusCode {
	case 401, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// skippedErrorPolicyFailoverError 处理 ErrorPolicySkipped：跳过账号状态写入不等于跳过换号。
// 可切换的状态码返回 UpstreamFailoverError；池模式仅对配置的状态允许同账号重试。
func (s *GeminiMessagesCompatService) skippedErrorPolicyFailoverError(c *gin.Context, account *gatewayprovider.ExecutionAccount, statusCode int, respBody []byte, upstreamRequestID string) *forwardcore.UpstreamFailoverError {
	if !s.shouldFailoverGeminiUpstreamError(statusCode) {
		return nil
	}
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: statusCode,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "failover",
		Message:            upstreamMsg,
		Detail:             s.upstreamErrorDetail(respBody),
	})
	return &forwardcore.UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseBody:           respBody,
		RetryableOnSameAccount: account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(statusCode),
	}
}

const geminiCustomCodeSkippedClientMessage = "Upstream gateway error"

// upstreamErrorDetail 按配置截断上游错误体，用于运维日志。
func (s *GeminiMessagesCompatService) upstreamErrorDetail(body []byte) string {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.LogUpstreamErrorBody {
		return ""
	}
	maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	return logredact.TruncateUTF8(string(body), maxBytes)
}

// writeGeminiCustomCodeSkippedError 对自定义错误码未命中的请求隐藏上游细节并返回 500。
func (s *GeminiMessagesCompatService) writeGeminiCustomCodeSkippedError(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstreamStatus int, upstreamRequestID string, body []byte, write func()) error {
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	upstreamDetail := s.upstreamErrorDetail(body)
	gatewayhttp.SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: upstreamStatus,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	write()
	if upstreamMsg == "" {
		return fmt.Errorf("gemini upstream error: %d (not in custom error codes)", upstreamStatus)
	}
	return fmt.Errorf("gemini upstream error: %d (not in custom error codes) message=%s", upstreamStatus, upstreamMsg)
}

// writeGeminiNativeUpstreamError 按原始状态码和响应体透传不可切换的 Gemini 错误。
func (s *GeminiMessagesCompatService) writeGeminiNativeUpstreamError(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, respBody []byte, requestID string, isOAuth bool) error {
	respBody = gemininative.UnwrapIfNeeded(isOAuth, respBody)
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
	upstreamDetail := s.upstreamErrorDetail(respBody)
	gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  requestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	gatewayhttp.MarkResponseCommitted(c)
	c.Data(resp.StatusCode, contentType, respBody)
	if upstreamMsg == "" {
		return fmt.Errorf("gemini upstream error: %d", resp.StatusCode)
	}
	return fmt.Errorf("gemini upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

func (s *GeminiMessagesCompatService) writeGeminiMappedError(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstreamStatus int, upstreamRequestID string, body []byte) error {
	gatewayhttp.MarkResponseCommitted(c)
	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	gatewayhttp.SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: upstreamStatus,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini] upstream error %d: %s", upstreamStatus, truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes))
	}

	if status, errType, errMsg, matched := gatewayhttp.ApplyErrorPassthroughRule(
		c,
		capability.PlatformGemini,
		upstreamStatus,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		c.JSON(status, gin.H{
			"type":  "error",
			"error": gin.H{"type": errType, "message": errMsg},
		})
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d (passthrough rule matched)", upstreamStatus)
		}
		return fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", upstreamStatus, upstreamMsg)
	}

	var statusCode int
	var errType, errMsg string

	if mapped := gatewayhttp.MapGeminiErrorBodyToClaudeError(body); mapped != nil {
		errType = mapped.Type
		if mapped.Message != "" {
			errMsg = mapped.Message
		}
		if mapped.StatusCode > 0 {
			statusCode = mapped.StatusCode
		}
	}

	switch upstreamStatus {
	case 400:
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
		if errType == "" {
			errType = "invalid_request_error"
		}
		if errMsg == "" {
			if upstreamMsg != "" {
				errMsg = upstreamMsg
			} else {
				errMsg = "Invalid request"
			}
		}
	case 401:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "authentication_error"
		}
		if errMsg == "" {
			errMsg = "Upstream authentication failed, please contact administrator"
		}
	case 403:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "permission_error"
		}
		if errMsg == "" {
			errMsg = "Upstream access forbidden, please contact administrator"
		}
	case 404:
		if statusCode == 0 {
			statusCode = http.StatusNotFound
		}
		if errType == "" {
			errType = "not_found_error"
		}
		if errMsg == "" {
			errMsg = "Resource not found"
		}
	case 429:
		if statusCode == 0 {
			statusCode = http.StatusTooManyRequests
		}
		if errType == "" {
			errType = "rate_limit_error"
		}
		if errMsg == "" {
			errMsg = "Upstream rate limit exceeded, please retry later"
		}
	case 529:
		if statusCode == 0 {
			statusCode = http.StatusServiceUnavailable
		}
		if errType == "" {
			errType = "overloaded_error"
		}
		if errMsg == "" {
			errMsg = "Upstream service overloaded, please retry later"
		}
	case 500, 502, 503, 504:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			switch upstreamStatus {
			case 504:
				errType = "timeout_error"
			case 503:
				errType = "overloaded_error"
			default:
				errType = "api_error"
			}
		}
		if errMsg == "" {
			errMsg = "Upstream service temporarily unavailable"
		}
	default:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "upstream_error"
		}
		if errMsg == "" {
			errMsg = "Upstream request failed"
		}
	}

	c.JSON(statusCode, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": errMsg},
	})
	if upstreamMsg == "" {
		return fmt.Errorf("upstream error: %d", upstreamStatus)
	}
	return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
}

func (s *GeminiMessagesCompatService) writeClaudeError(c *gin.Context, status int, errType, message string) error {
	gatewayhttp.MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *GeminiMessagesCompatService) writeGoogleError(c *gin.Context, status int, message string) error {
	gatewayhttp.MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  gatewayhttp.HTTPStatusToGoogleStatus(status),
		},
	})
	return fmt.Errorf("%s", message)
}

func (s *GeminiMessagesCompatService) handleNativeNonStreamingResponse(c *gin.Context, resp *http.Response, isOAuth bool) (*upstream.TokenUsage, error) {
	return s.geminiResponseAdapter(c).HandleNativeNonStreamingResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, isOAuth)
}

func (s *GeminiMessagesCompatService) ForwardAIStudioGET(ctx context.Context, account *gatewayprovider.ExecutionAccount, path string) (*gemininative.HTTPResult, error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	options := gemininative.ModelGetOptions{Mode: gemininative.CredentialMode(account.Record.Type), BaseURL: func() string { return account.View().GetGeminiBaseURL(geminicli.AIStudioBaseURL) }, APIKey: func() string { return account.View().GetCredential("api_key") }, ValidateURL: s.validateUpstreamBaseURL, Enter: s.nativeAttemptActivity, Do: func(req *http.Request) (*http.Response, error) {
		proxy := ""
		if account.Record.ProxyID != nil && account.Record.Proxy != nil {
			proxy = account.Record.Proxy.URL()
		}
		return s.httpUpstream.Do(req, proxy, account.Record.ID, account.Record.Concurrency)
	}, FilterHeaders: func(header http.Header) http.Header {
		return provider.FilterHeaders(header, s.responseHeaderFilter)
	}}
	if s.tokenProvider != nil {
		options.Token = func(ctx context.Context) (string, error) { return accountToken(ctx, s.tokenProvider, account) }
	}
	return gemininative.ReadAIStudioModel(ctx, path, options)
}

func (s *GeminiMessagesCompatService) handleGeminiUpstreamError(ctx context.Context, account *gatewayprovider.ExecutionAccount, statusCode int, headers http.Header, body []byte) {
	// 遵守自定义错误码策略：未命中则跳过所有限流处理
	if !account.View().ShouldHandleErrorCode(statusCode) {
		return
	}
	if s.healthObserver != nil && (statusCode == 401 || statusCode == 403 || statusCode == 529) {
		gatewayprovider.ApplyExecutionHealth(ctx, s.healthObserver, account, gatewayprovider.HealthObservationFromContext(ctx, statusCode, headers, body, nil))
		return
	}
	if statusCode != 429 {
		return
	}
	// 池模式账号保留在上游账号池中，由请求级重试或切号消化 429；
	// 管理员显式配置的自定义错误策略优先，命中时仍允许写入账号状态。
	if account.View().IsPoolMode() && !account.View().IsCustomErrorCodesEnabled() {
		return
	}

	oauthType := account.View().GeminiOAuthType()
	tierID := account.View().GeminiTierID()
	projectID := strings.TrimSpace(account.View().GetCredential("project_id"))
	isCodeAssist := account.View().IsGeminiCodeAssist()

	if account.View().IsGeminiThirdPartyProvider() {
		// 第三方兼容端点不得解析 Google 官方日配额文案，始终使用通用 429 冷却。
		cooldown := 5 * time.Minute
		if s.quotaPrecheck != nil {
			cooldown = s.quotaPrecheck.GeminiCooldown(ctx, gatewayprovider.ExecutionRecord(account))
		}
		ra := time.Now().Add(cooldown)
		_ = s.accountRepo.SetRateLimited(ctx, account.Record.ID, ra)
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (third-party API Key) rate limited, cooldown=%v", account.Record.ID, time.Until(ra).Truncate(time.Second))
		return
	}

	resetAt := ParseGeminiRateLimitResetTime(body)
	if resetAt == nil {
		// 根据账号类型使用不同的默认重置时间
		var ra time.Time
		if isCodeAssist || oauthType == "google_one" {
			// Gemini CLI / Google One：按层级回退冷却时间
			cooldown := accountcore.GeminiCooldownForTier(tierID)
			if s.quotaPrecheck != nil {
				cooldown = s.quotaPrecheck.GeminiCooldown(ctx, gatewayprovider.ExecutionRecord(account))
			}
			ra = time.Now().Add(cooldown)
			if isCodeAssist {
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Code Assist, tier=%s, project=%s) rate limited, cooldown=%v", account.Record.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			} else {
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Google One OAuth, tier=%s, project=%s) rate limited, cooldown=%v", account.Record.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			}
		} else {
			// API Key / AI Studio OAuth: PST 午夜
			if ts := nextGeminiDailyResetUnix(); ts != nil {
				ra = time.Unix(*ts, 0)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (API Key/AI Studio, type=%s) rate limited, reset at PST midnight (%v)", account.Record.ID, account.Record.Type, ra)
			} else {
				// 兜底：5 分钟
				ra = time.Now().Add(5 * time.Minute)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited, fallback to 5min", account.Record.ID)
			}
		}
		_ = s.accountRepo.SetRateLimited(ctx, account.Record.ID, ra)
		return
	}

	// 使用解析到的重置时间
	resetTime := time.Unix(*resetAt, 0)
	_ = s.accountRepo.SetRateLimited(ctx, account.Record.ID, resetTime)
	logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited until %v (oauth_type=%s, tier=%s)",
		account.Record.ID, resetTime, oauthType, tierID)
}

// applyGeminiUpstreamErrorPolicy 统一 Gemini 三种协议入口的显式策略和默认状态处理。
// 池模式绕过时绝不能继续调用 handleGeminiUpstreamError，否则 429 会写入本地限流。
func (s *GeminiMessagesCompatService) applyGeminiUpstreamErrorPolicy(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	statusCode int,
	headers http.Header,
	body []byte,
	mappedModel string,
) accountcore.UpstreamErrorDecision {
	decision := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(account), statusCode)
	if s == nil || account == nil {
		return decision
	}
	if headers != nil && headers.Get(geminiAppliedTempPolicyHeader) == "1" {
		headers.Del(geminiAppliedTempPolicyHeader)
		decision.Policy = accountcore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		return decision
	}
	if s.healthObserver != nil {
		decision.Policy = s.healthObserver.ApplyExplicitErrorPolicy(ctx, gatewayprovider.ExecutionRecord(account), gatewayprovider.HealthObservationFromContext(ctx, statusCode, nil, body, []string{mappedModel}))
		decision.StopScheduling = decision.Policy == accountcore.ErrorPolicyCustomMatched || decision.Policy == accountcore.ErrorPolicyTempUnscheduled
	}
	switch decision.Policy {
	case accountcore.ErrorPolicyCustomMatched, accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}
	s.handleGeminiUpstreamError(ctx, account, statusCode, headers, body)
	return decision
}

func ParseGeminiRateLimitResetTime(body []byte) *int64 {
	return gemininative.ParseGeminiRateLimitResetTime(body, nextGeminiDailyResetUnix)
}

func nextGeminiDailyResetUnix() *int64 {
	reset := geminiDailyResetTime(time.Now())
	ts := reset.Unix()
	return &ts
}

// ensureGeminiFunctionCallThoughtSignatures 委托原生 Gemini 方言的纯转换，调用顺序由旧平台保留。
func ensureGeminiFunctionCallThoughtSignatures(body []byte) []byte {
	return bridge.NativeEnsureGeminiFunctionCallThoughtSignatures(bridge.NativeGeminiOptions{DummyThoughtSignature: geminiDummyThoughtSignature}, body)
}

// convertClaudeMessagesToGeminiGenerateContent 委托原生 Gemini 方言的纯转换，调用顺序由旧平台保留。
func convertClaudeMessagesToGeminiGenerateContent(body []byte) ([]byte, error) {
	return bridge.NativeConvertClaudeMessagesToGeminiGenerateContent(bridge.NativeGeminiOptions{DummyThoughtSignature: geminiDummyThoughtSignature}, body)
}

func (s *GeminiMessagesCompatService) extractImageInputSize(body []byte) string {
	var req struct {
		GenerationConfig *struct {
			ImageConfig *struct {
				ImageSize string `json:"imageSize"`
			} `json:"imageConfig"`
		} `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	if req.GenerationConfig != nil && req.GenerationConfig.ImageConfig != nil {
		return strings.TrimSpace(req.GenerationConfig.ImageConfig.ImageSize)
	}

	return ""
}

// BindNativeAttemptActivity 只绑定 app 拥有的同步资源屏障。
func (s *GeminiMessagesCompatService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}

// BindQuotaPrecheck 在开放请求前绑定唯一配额预检和日统计缓存。
func (s *GeminiMessagesCompatService) BindQuotaPrecheck(precheck *accountcore.GeminiPrecheck) {
	s.quotaPrecheck = precheck
}
