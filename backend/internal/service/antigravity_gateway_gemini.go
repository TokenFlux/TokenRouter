package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	geminiwire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/gin-gonic/gin"
)

// ForwardGemini 转发 Gemini 协议请求
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
type ForwardGeminiOption func(*forwardGeminiOptions)

type forwardGeminiOptions struct {
	groupID     int64
	sessionHash string
}

func WithForwardGeminiSession(groupID int64, sessionHash string) ForwardGeminiOption {
	return func(opts *forwardGeminiOptions) {
		opts.groupID = groupID
		opts.sessionHash = sessionHash
	}
}

func (s *AntigravityGatewayService) ForwardGemini(ctx context.Context, c *gin.Context, account *Account, originalModel string, action string, stream bool, body []byte, isStickySession bool, options ...ForwardGeminiOption) (*ForwardResult, error) {
	startTime := time.Now()
	forwardOpts := forwardGeminiOptions{}
	for _, apply := range options {
		if apply != nil {
			apply(&forwardOpts)
		}
	}

	sessionID := getSessionID(c)
	prefix := logPrefix(sessionID, account.Name)

	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing model in URL")
	}
	if strings.TrimSpace(action) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing action in URL")
	}
	if len(body) == 0 {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Request body is empty")
	}

	// 解析请求以获取 image_size（用于图片计费）
	imageInputSize := s.extractImageInputSize(body)
	imageSize := normalizeOpenAIImageSizeTier(imageInputSize)

	switch action {
	case "generateContent", "streamGenerateContent":
		// ok
	case "countTokens":
		// 直接返回空值，不透传上游
		c.JSON(http.StatusOK, map[string]any{"totalTokens": 0})
		return &ForwardResult{
			RequestID:    "",
			Usage:        ClaudeUsage{},
			Model:        originalModel,
			Stream:       false,
			Duration:     time.Since(startTime),
			FirstTokenMs: nil,
		}, nil
	default:
		return nil, s.writeGoogleError(c, http.StatusNotFound, "Unsupported action: "+action)
	}

	mappedModel := s.getMappedModel(account, originalModel)
	if mappedModel == "" {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		return nil, s.writeGoogleError(c, http.StatusForbidden, fmt.Sprintf("model %s not in whitelist", originalModel))
	}
	billingModel := mappedModel

	// 获取 access_token
	if s.tokenProvider == nil {
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "Antigravity token provider not configured")
	}
	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"error":{"message":"Failed to get upstream access token","status":"UNAVAILABLE"}}`),
		}
	}

	projectID, err := resolveAntigravityProjectID(account)
	if err != nil {
		_ = s.writeGoogleError(c, http.StatusBadRequest, err.Error())
		return nil, err
	}

	// 代理 URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// Antigravity 上游要求必须包含身份提示词，注入到请求中
	injectedBody, err := injectIdentityPatchToGeminiRequest(body)
	if err != nil {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Invalid request body")
	}

	// 清理 Schema
	if cleanedBody, err := cleanGeminiRequest(injectedBody); err == nil {
		injectedBody = cleanedBody
		logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] Cleaned request schema in forwarded request for account %s", account.Name)
	} else {
		logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] Failed to clean schema: %v", err)
	}

	// 包装请求
	wrappedBody, err := s.wrapV1InternalRequest(projectID, mappedModel, injectedBody)
	if err != nil {
		if errors.Is(err, errAntigravityProjectIDRequired) {
			return nil, s.writeGoogleError(c, http.StatusBadRequest, err.Error())
		}
		return nil, s.writeGoogleError(c, http.StatusInternalServerError, "Failed to build upstream request")
	}

	// Antigravity 上游只支持流式请求，统一使用 streamGenerateContent
	// 如果客户端请求非流式，在响应处理阶段会收集完整流式响应后返回
	upstreamAction := "streamGenerateContent"

	// 执行带重试的请求
	retry, params := s.antigravityRetryAdapter(antigravityRetryLoopParams{
		ctx:             ctx,
		prefix:          prefix,
		account:         account,
		proxyURL:        proxyURL,
		accessToken:     accessToken,
		action:          upstreamAction,
		body:            wrappedBody,
		c:               c,
		httpUpstream:    s.httpUpstream,
		settingService:  s.settingService,
		accountRepo:     s.accountRepo,
		handleError:     s.handleUpstreamError,
		requestedModel:  originalModel,
		isStickySession: isStickySession, // ForwardGemini 由上层判断粘性会话
		groupID:         forwardOpts.groupID,
		sessionHash:     forwardOpts.sessionHash,
	})
	var recovered antigravity.GeminiRecoveryResult
	target := &antigravity.Target{AccountID: account.ID, Model: billingModel, Mode: antigravity.ModeGeminiResponse, StartedAt: startTime, Response: s.antigravityResponseAdapter(c).Options, Enter: s.nativeAttemptActivity}
	target.Exchange = func(context.Context) (*http.Response, error) {
		result, err := retry.AntigravityRetryLoop(params)
		if err != nil {
			// 检查是否是账号切换信号，转换为 UpstreamFailoverError 让 Handler 切换账号
			if switchErr, ok := IsAntigravityAccountSwitchError(err); ok {
				return nil, &UpstreamFailoverError{
					StatusCode:        http.StatusServiceUnavailable,
					ForceCacheBilling: switchErr.IsStickySession,
				}
			}
			// 区分客户端取消和真正的上游失败，返回更准确的错误消息
			if c.Request.Context().Err() != nil {
				return nil, s.writeGoogleError(c, http.StatusBadGateway, "Client disconnected before upstream response")
			}
			return nil, s.writeGoogleError(c, http.StatusBadGateway, "Upstream request failed after retries")
		}
		opts := antigravity.GeminiRecoveryOptions{Retry: func(body []byte) (*http.Response, error) {
			next := params
			next.Body = body
			value, err := retry.AntigravityRetryLoop(next)
			if err != nil {
				return nil, err
			}
			return value.Resp, nil
		}, Do: retry.Options.Do, FallbackEnabled: func(ctx context.Context) bool {
			return s.settingService != nil && s.settingService.IsModelFallbackEnabled(ctx)
		}, SignatureEnabled: func(ctx context.Context) bool {
			return s.settingService != nil && s.settingService.IsSignatureRectifierEnabled(ctx)
		}, FallbackModel: func(ctx context.Context) string { return s.settingService.GetFallbackModel(ctx, PlatformAntigravity) }, IsModelNotFound: isModelNotFoundError, CleanSignatures: CleanGeminiNativeThoughtSignatures, ReadErrorBody: s.readUpstreamErrorBody, ErrorDetail: s.getUpstreamErrorDetail, Observe: retry.Options.Observe}
		recovered, err = antigravity.RecoverGemini(ctx, antigravity.GeminiRecoveryInput{AccountID: account.ID, AccountName: account.Name, ProjectID: projectID, Model: mappedModel, Action: upstreamAction, AccessToken: accessToken, Body: injectedBody}, result.Resp, opts)
		if err != nil {
			if switchErr, ok := IsAntigravityAccountSwitchError(err); ok {
				return nil, &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ForceCacheBilling: switchErr.IsStickySession}
			}
			return nil, err
		}
		return recovered.Response, nil
	}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response) (bool, error) {
		if resp.StatusCode < 400 {
			return false, nil
		}
		respBody, contentType := recovered.ErrorBody, recovered.ContentType

		requestID := resp.Header.Get("x-request-id")
		if requestID != "" {
			c.Header("x-request-id", requestID)
		}

		unwrapped, unwrapErr := s.unwrapV1InternalResponse(respBody)
		unwrappedForOps := unwrapped
		if unwrapErr != nil || len(unwrappedForOps) == 0 {
			unwrappedForOps = respBody
		}
		s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody, originalModel, forwardOpts.groupID, forwardOpts.sessionHash, isStickySession)
		upstreamMsg := strings.TrimSpace(extractAntigravityErrorMessage(unwrappedForOps))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		upstreamDetail := s.getUpstreamErrorDetail(unwrappedForOps)

		// Always record upstream context for Ops error logs, even when we will failover.
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

		// 精确匹配服务端配置类 400 错误，触发同账号重试 + failover
		if resp.StatusCode == http.StatusBadRequest && isGoogleProjectConfigError(strings.ToLower(upstreamMsg)) {
			log.Printf("%s status=400 google_config_error failover=true upstream_message=%q account=%d", prefix, upstreamMsg, account.ID)
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  requestID,
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			return true, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: unwrappedForOps, RetryableOnSameAccount: true}
		}

		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  requestID,
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			return true, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: unwrappedForOps}
		}
		if contentType == "" {
			contentType = "application/json"
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  requestID,
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		logger.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] upstream error status=%d body=%s", resp.StatusCode, truncateForLog(unwrappedForOps, 500))
		MarkResponseCommitted(c)
		c.Data(resp.StatusCode, contentType, unwrappedForOps)
		return true, fmt.Errorf("antigravity upstream error: %d", resp.StatusCode)
	}
	target.OutputError = func(err error) {
		kind := "stream_collect_error"
		if stream {
			kind = "stream_error"
		}
		logger.LegacyPrintf("service.antigravity_gateway", "%s status=%s error=%v", prefix, kind, err)
	}
	result, err := (antigravity.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolGeminiGenerateContent, Body: wrappedBody, ResponseModel: originalModel, Stream: stream, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	imageCount := 0
	if isImageGenerationModel(mappedModel) {
		imageCount = 1
	}
	return &ForwardResult{RequestID: result.RequestID, UpstreamHeaders: result.UpstreamHeaders, Usage: result.Usage, Model: originalModel, UpstreamModel: billingModel, Stream: stream, Duration: result.Duration, FirstTokenMs: result.FirstTokenMs, ClientDisconnect: result.ClientDisconnect, ImageCount: imageCount, ImageSize: imageSize, ImageInputSize: imageInputSize}, nil
}

func filterEmptyPartsFromGeminiRequest(body []byte) ([]byte, error) {
	return geminiwire.FilterEmptyParts(body)
}
