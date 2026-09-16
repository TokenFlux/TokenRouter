package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type openAIResponsesImageResult = nativeopenai.OpenAIResponsesImageResult

type OpenAIImagesUpstreamError = nativeopenai.OpenAIImagesUpstreamError

func IsOpenAIImagesRetryableUpstreamError(err *OpenAIImagesUpstreamError) bool {
	return nativeopenai.IsOpenAIImagesRetryableUpstreamError(err)
}

func openAIImagesSSEErrorStatus(errType, code string) int {
	return nativeopenai.OpenAIImagesSSEErrorStatus(errType, code)
}

func openAIImagesUpstreamErrorResponseBody(err *OpenAIImagesUpstreamError) []byte {
	return nativeopenai.OpenAIImagesUpstreamErrorResponseBody(err)
}

func openAIImageOutputMIMEType(outputFormat string) string {
	return nativeopenai.OpenAIImageOutputMIMEType(outputFormat)
}

func withOpenAIImagesSelfBuiltRequest(ctx context.Context) context.Context {
	return nativeopenai.WithOpenAIImagesSelfBuiltRequest(ctx)
}

func isOpenAIImagesSelfBuiltRequest(ctx context.Context) bool {
	return nativeopenai.IsOpenAIImagesSelfBuiltRequest(ctx)
}

func buildOpenAIImagesResponsesRequest(parsed *OpenAIImagesRequest, toolModel string) ([]byte, error) {
	return nativeopenai.BuildOpenAIImagesResponsesRequest(nativeImageRequestView(parsed), toolModel)
}

func collectOpenAIImagesFromResponsesBody(body []byte) ([]openAIResponsesImageResult, int64, []byte, openAIResponsesImageResult, bool, error) {
	return nativeopenai.CollectOpenAIImagesFromResponsesBody(body, time.Now)
}

func extractOpenAIImagesUpstreamError(body []byte) *OpenAIImagesUpstreamError {
	return nativeopenai.ExtractOpenAIImagesUpstreamError(body)
}

func extractOpenAIImagesModelRefusal(body []byte) string {
	return nativeopenai.ExtractOpenAIImagesModelRefusal(body)
}

func summarizeOpenAIImagesNoOutputBody(body []byte) string {
	return nativeopenai.SummarizeOpenAIImagesNoOutputBody(body)
}

func (s *OpenAIGatewayService) summarizeOpenAIImagesNoOutputBody(body []byte) string {
	includeBody := true
	maxSnippet := 1024
	if s != nil && s.cfg != nil {
		includeBody = s.cfg.Gateway.LogUpstreamErrorBody
		if cfgMax := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes; cfgMax > 0 && cfgMax < maxSnippet {
			maxSnippet = cfgMax
		}
	}
	return summarizeOpenAIImagesNoOutputBodyWithSnippet(body, includeBody, maxSnippet)
}

func summarizeOpenAIImagesNoOutputBodyWithSnippet(body []byte, includeBody bool, maxSnippet int) string {
	return nativeopenai.SummarizeOpenAIImagesNoOutputBodyWithSnippet(body, includeBody, maxSnippet)
}

func openAIImagesUpstreamErrorFromHTTP(statusCode int, header http.Header, body []byte) *OpenAIImagesUpstreamError {
	return nativeopenai.OpenAIImagesUpstreamErrorFromHTTP(statusCode, header, body)
}

func (s *OpenAIGatewayService) handleOpenAIImagesErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestBody []byte,
	requestedModel ...string,
) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)

	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logger.LegacyPrintf("service.openai_gateway",
			"OpenAI images upstream error %d (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.ID,
			account.Platform,
			account.Type,
			truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	if IsOpenAICyberWarningPayload(body, upstreamMsg) {
		errMsg := ExtractOpenAICyberWarningMessage(body, upstreamMsg)
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

			Platform: account.Platform,

			AccountID: account.ID,

			AccountName: account.Name,

			UpstreamStatusCode: resp.StatusCode,

			UpstreamRequestID: resp.Header.Get("x-request-id"),

			Kind: "http_error",

			Message: errMsg,

			Detail: upstreamDetail,
		})
		upErr := &OpenAIImagesUpstreamError{

			StatusCode: resp.StatusCode,

			ErrorType: "invalid_request_error",

			Message: errMsg,

			UpstreamRequestID: strings.TrimSpace(resp.Header.Get("x-request-id")),
		}
		writeOpenAIImagesUpstreamErrorResponse(c, upErr)
		return nil, wrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, body, errMsg, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, errMsg))
	}

	var modelForCooldown string
	if len(requestedModel) > 0 {
		modelForCooldown = strings.TrimSpace(requestedModel[0])
	}
	decision := s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, modelForCooldown)
	if decision.ShouldReturnGenericError() {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

			Platform: account.Platform,

			AccountID: account.ID,

			AccountName: account.Name,

			UpstreamStatusCode: resp.StatusCode,

			UpstreamRequestID: resp.Header.Get("x-request-id"),

			Kind: "http_error",

			Message: upstreamMsg,

			Detail: upstreamDetail,
		})
		upErr := &OpenAIImagesUpstreamError{

			StatusCode: http.StatusInternalServerError,

			ErrorType: "upstream_error",

			Message: "Upstream gateway error",

			UpstreamRequestID: strings.TrimSpace(resp.Header.Get("x-request-id")),
		}
		writeOpenAIImagesUpstreamErrorResponse(c, upErr)
		return nil, upErr
	}

	kind := "http_error"
	if decision.ShouldFailover(account, resp.StatusCode, s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, body)) {
		kind = "failover"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

		Platform: account.Platform,

		AccountID: account.ID,

		AccountName: account.Name,

		UpstreamStatusCode: resp.StatusCode,

		UpstreamRequestID: resp.Header.Get("x-request-id"),

		Kind: kind,

		Message: upstreamMsg,

		Detail: upstreamDetail,
	})
	if kind == "failover" {
		return nil, &UpstreamFailoverError{

			StatusCode: resp.StatusCode,

			ResponseBody: body,

			RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
		}
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		upErr := &OpenAIImagesUpstreamError{

			StatusCode: status,

			ErrorType: errType,

			Message: sanitizeUpstreamErrorMessage(errMsg),

			UpstreamRequestID: strings.TrimSpace(resp.Header.Get("x-request-id")),
		}
		writeOpenAIImagesUpstreamErrorResponse(c, upErr)
		return nil, upErr
	}

	upErr := openAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, body)
	writeOpenAIImagesUpstreamErrorResponse(c, upErr)
	return nil, upErr
}

func openAIImagesStreamPrefix(parsed *OpenAIImagesRequest) string {
	return nativeopenai.OpenAIImagesStreamPrefix(nativeImageRequestView(parsed))
}

func writeOpenAIImagesUpstreamErrorResponse(c *gin.Context, err *OpenAIImagesUpstreamError) bool {
	if c == nil || c.Writer == nil || err == nil {
		return false
	}
	if c.Writer.Written() && OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) >= 0 {
		return false
	}
	StopOpenAIImagesJSONKeepaliveCommitted(c)
	errorObj := gin.H{
		"type":    err.ClientErrorType(),
		"message": err.ClientMessage(),
	}
	if code := strings.TrimSpace(err.Code); code != "" {
		errorObj["code"] = code
	}
	if param := strings.TrimSpace(err.Param); param != "" {
		errorObj["param"] = param
	}
	c.JSON(err.ClientStatusCode(), gin.H{
		"error": errorObj,
	})
	return true
}

func (s *OpenAIGatewayService) parseOpenAIImagesSSEUsageBytes(data []byte, usage *OpenAIUsage) {
	nativeopenai.ParseOpenAIImagesSSEUsageBytes(data, usage)
}

func boundedJSONNonNegativeInt(value gjson.Result) (int, bool) {
	return s09openai.BoundedJSONNonNegativeInt(value)
}

func (s *OpenAIGatewayService) handleOpenAIImagesOAuthNonStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	responseFormat string,
	fallbackModel string,
) (OpenAIUsage, int, []string, error) {
	return nativeopenai.ReadImagesOAuthNonStreaming(resp, gatewayhttp.ResponseSink{Writer: c.Writer}, s.nativeImageResponseOptions(c), responseFormat, fallbackModel)
}

func (s *OpenAIGatewayService) handleOpenAIImagesOAuthStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	startTime time.Time,
	responseFormat string,
	streamPrefix string,
	fallbackModel string,
) (OpenAIUsage, int, []string, *int, error) {
	return nativeopenai.ReadImagesOAuthStreaming(resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeImageResponseOptions(c), startTime, responseFormat, streamPrefix, fallbackModel)
}

func (s *OpenAIGatewayService) forwardOpenAIImagesOAuth(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	if requestModel == "" {
		requestModel = "gpt-image-2"
	}
	if err := validateOpenAIImagesModel(requestModel); err != nil {
		return nil, err
	}
	upstreamModel := resolveOpenAIAccountUpstreamModelForRequest(account, requestModel, false, false)
	if err := validateOpenAIImagesModel(upstreamModel); err != nil {
		return nil, err
	}
	logger.LegacyPrintf(
		"service.openai_gateway",
		"[OpenAI] Images request routing request_model=%s upstream_model=%s endpoint=%s account_type=%s uploads=%d",
		requestModel,
		upstreamModel,
		parsed.Endpoint,
		account.Type,
		len(parsed.Uploads),
	)
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}

	responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, upstreamModel)
	if err != nil {
		return nil, err
	}
	upstreamCtx = withOpenAIImagesSelfBuiltRequest(upstreamCtx)
	upstreamReq, err := s.buildUpstreamRequest(upstreamCtx, c, account, responsesBody, token, true, parsed.StickySessionSeed(), false, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "text/event-stream")
	upstreamReq.Header.Set("OpenAI-Beta", "responses=experimental")

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	options := s.nativeImageResponseOptions(c)
	var legacyHTTPResult *OpenAIForwardResult
	httpFailure := false
	retryAgent := false
	target := &nativeopenai.ImagesTarget{

		AccountID: account.ID,
		OAuth:     true,
		Model:     upstreamModel,
		StartedAt: startTime,

		Request: upstreamReq,
		Options: options,
		Enter:   s.nativeAttemptActivity,

		ResponseFormat: parsed.ResponseFormat,
		StreamPrefix:   openAIImagesStreamPrefix(parsed),

		Do: func(req *http.Request) (*http.Response, error) {
			upstreamStart := time.Now()
			resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			return resp, err
		},

		TransportError: func(err error) error {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "request_error",
				Message:            safeErr,
			})
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.readUpstreamErrorBody,

		RedactErrorBody: func(body []byte) []byte { return s.redactAgentIdentitySensitiveBody(upstreamCtx, account, body) },

		HTTPError: func(resp *http.Response, respBody []byte) error {
			httpFailure = true

			if !agentIdentityTaskRecoveryWasTried(ctx) && s.isAgentIdentityAccount(ctx, account) && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
				expectedTaskID := account.GetCredential("task_id")
				if err := s.recoverAgentIdentityTask(ctx, account, expectedTaskID); err != nil {
					return fmt.Errorf("agent identity task recovery failed: %w", err)
				}
				retryAgent = true
				return nil
			}
			upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
			upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
			if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

					Platform: account.Platform,

					AccountID: account.ID,

					AccountName: account.Name,

					UpstreamStatusCode: resp.StatusCode,

					UpstreamRequestID: resp.Header.Get("x-request-id"),

					UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),

					Kind: "failover",

					Message: upstreamMsg,
				})
				decision := s.applyFailoverSideEffects(upstreamCtx, resp, account, respBody, requestModel)
				if decision.ShouldReturnGenericError() {
					var failure error
					legacyHTTPResult, failure = s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, account, responsesBody, requestModel)
					return failure
				}
				retryableOnSameAccount := decision.RetryableOnSameAccount(account, resp.StatusCode)
				if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
					return s.newOpenAIAccountFailoverError(
						account,
						resp.StatusCode,
						resp.Header,
						respBody,
						upstreamMsg,
						false,
						retryableOnSameAccount,
					)
				}
				return &UpstreamFailoverError{

					StatusCode: resp.StatusCode,

					ResponseBody: respBody,

					RetryableOnSameAccount: retryableOnSameAccount,
				}
			}
			var failure error
			legacyHTTPResult, failure = s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, account, responsesBody, requestModel)
			return failure

		},

		ResponseError: func(resp *http.Response, before int, err error) error {
			return s.handleOpenAIImagesOAuthResponseError(upstreamCtx, c, account, requestModel, safeUpstreamURL(upstreamReq.URL.String()), resp, before, err)
		},
	}
	protocolID := protocol.ProtocolImagesGenerations
	if parsed.IsEdits() {
		protocolID = protocol.ProtocolImagesEdits
	}
	result, err := (nativeopenai.ImagesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocolID, ResponseModel: requestModel, Stream: parsed.Stream, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if retryAgent {
		return s.forwardOpenAIImagesOAuth(markAgentIdentityTaskRecoveryTried(ctx), c, account, parsed, channelMappedModel)
	}
	if httpFailure {
		return legacyHTTPResult, err
	}
	if err != nil && (!parsed.Stream || result.ObservedImages <= 0) {
		return nil, err
	}
	imageCount := result.ObservedImages
	if err == nil && imageCount <= 0 {
		imageCount = parsed.N
	}
	return openAIImagesForwardResult(result, parsed, imageCount), err
}

const openAIImagesOAuthUnavailableDefaultCooldown = nativeopenai.OpenAIImagesOAuthUnavailableDefaultCooldown
const openAIImagesOAuthUnavailableReason = nativeopenai.OpenAIImagesOAuthUnavailableReason

// shouldCoolOpenAIImagesToolForError 判断 image_generation_unavailable 判定是否足够持久，
// 可以将账号图片工具置于 openAIImagesOAuthUnavailableCooldown 冷却期。
//
// 只有上游错误帧明确指出该状态时才符合条件。网关从模型纯文本回复合成的判定不符合：
// 它仅说明当前提示词得到文字而非图片，取决于提示词，健康账号也可能出现。为此写入
// 30 分钟账号级冷却尤其不合理，因为同一错误会被判定为可重试
// （IsOpenAIImagesRetryableUpstreamError：状态码 >= 500）并驱动 newOpenAIAccountFailoverError，
// 使一次回复沿账号池重试并冷却所有被触及的账号。
//
// 这与 alpha/search 路径已有的规则一致：工具端点故障“仍允许本次请求换号，但不修改任何账号状态”
// （参见 shouldApplyOpenAIAlphaSearchAccountErrorSideEffects）。
func shouldCoolOpenAIImagesToolForError(upstreamErr *OpenAIImagesUpstreamError) bool {
	return upstreamErr != nil && !upstreamErr.SynthesizedFromModelText
}

func (s *OpenAIGatewayService) coolOpenAIImagesOAuthTool(ctx context.Context, account *Account) {
	if s == nil || s.accountRepo == nil || account == nil || account.Platform != PlatformOpenAI {
		return
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	cooldown := openAIImagesOAuthUnavailableDefaultCooldown
	if s.settingService != nil {
		settings, err := s.settingService.GetOpenAIImagesOAuthUnavailableCooldownSettings(stateCtx)
		if err != nil {
			logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool cooldown setting read failed error=%v", err)
		} else {
			cooldown = time.Duration(settings.CooldownMinutes) * time.Minute
		}
	}
	resetAt := time.Now().Add(cooldown)
	if err := s.accountRepo.SetModelRateLimit(stateCtx, account.ID, openAIImageGenerationRateLimitKey, resetAt, openAIImagesOAuthUnavailableReason); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool cooldown write failed account_id=%d error=%v", account.ID, err)
		return
	}
	logger.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool unavailable account_id=%d reset_in=%s", account.ID, time.Until(resetAt).Truncate(time.Second))
}

func (s *OpenAIGatewayService) handleOpenAIImagesOAuthResponseError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	requestedModel string,
	upstreamURL string,
	resp *http.Response,
	writerSizeBeforeResponse int,
	err error,
) error {
	responseWritten := c != nil && c.Writer != nil && OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) != writerSizeBeforeResponse
	if code, message, ok := OpenAIUpstreamStreamReadErrorDetails(err); ok {
		// HTTP 已成功但响应体传输中断时，仅在尚未输出真实图片内容前允许重试；
		// 同时克隆上游响应头，避免后续释放响应后污染 failover 诊断信息。
		headers := http.Header(nil)
		requestID := ""
		statusCode := http.StatusBadGateway
		if resp != nil {
			headers = resp.Header.Clone()
			requestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
		}
		responseBody := []byte(fmt.Sprintf(`{"error":{"type":"upstream_error","code":%q,"message":%q}}`, code, message))
		decision := s.applyOpenAIAccountUpstreamError(ctx, account, statusCode, headers, responseBody, requestedModel)
		retryable := decision.ShouldFailover(account, statusCode, true)
		kind := "http_error"
		if retryable {
			kind = "failover"
			if responseWritten {
				kind = "retry_exhausted_failover"
			}
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

			Platform: account.Platform,

			AccountID: account.ID,

			AccountName: account.Name,

			UpstreamStatusCode: statusCode,

			UpstreamRequestID: requestID,

			UpstreamURL: upstreamURL,

			Kind: kind,

			Message: message,
		})
		if !retryable || responseWritten {
			return err
		}
		return &UpstreamFailoverError{

			StatusCode: statusCode,

			ResponseBody: responseBody,

			ResponseHeaders: headers,

			RetryableOnSameAccount: decision.RetryableOnSameAccount(account, statusCode),
		}
	}

	var upstreamErr *OpenAIImagesUpstreamError
	if !errors.As(err, &upstreamErr) {
		return err
	}

	requestID := strings.TrimSpace(upstreamErr.UpstreamRequestID)
	headers := http.Header(nil)
	if resp != nil {
		headers = resp.Header.Clone()
		if requestID == "" {
			requestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
		}
	}
	responseBody := openAIImagesUpstreamErrorResponseBody(upstreamErr)
	decision := s.applyOpenAIAccountUpstreamError(ctx, account, upstreamErr.StatusCode, headers, responseBody, requestedModel)
	retryable := decision.ShouldFailover(account, upstreamErr.StatusCode, IsOpenAIImagesRetryableUpstreamError(upstreamErr))
	kind := "http_error"
	if retryable {
		kind = "failover"
		if responseWritten {
			kind = "retry_exhausted_failover"
		}
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

		Platform: account.Platform,

		AccountID: account.ID,

		AccountName: account.Name,

		UpstreamStatusCode: upstreamErr.StatusCode,

		UpstreamRequestID: requestID,

		UpstreamURL: upstreamURL,

		Kind: kind,

		Message: upstreamErr.ClientMessage(),
	})

	if upstreamErr.Code == "image_generation_unavailable" {
		if shouldCoolOpenAIImagesToolForError(upstreamErr) {
			s.coolOpenAIImagesOAuthTool(ctx, account)
		}
		if responseWritten {
			return err
		}
		return s.newOpenAIAccountFailoverError(
			account,
			upstreamErr.StatusCode,
			headers,
			responseBody,
			upstreamErr.ClientMessage(),
			false,
			false,
		)
	}
	if !retryable || responseWritten {
		return err
	}
	shouldDisable := s.handleOpenAIAccountUpstreamError(ctx, account, upstreamErr.StatusCode, headers, responseBody, requestedModel)
	return s.newOpenAIAccountFailoverError(
		account,
		upstreamErr.StatusCode,
		headers,
		responseBody,
		upstreamErr.ClientMessage(),
		shouldDisable,
		!shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(upstreamErr.StatusCode),
	)
}
