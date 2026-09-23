package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
)

func buildOpenAIImagesResponsesRequest(parsed *gatewaymedia.ImageRequest, toolModel string) ([]byte, error) {
	return openai.BuildOpenAIImagesResponsesRequest(gatewaymedia.NativeImageRequest(parsed), toolModel)
}

func collectOpenAIImagesFromResponsesBody(body []byte) ([]openai.OpenAIResponsesImageResult, int64, []byte, openai.OpenAIResponsesImageResult, bool, error) {
	return openai.CollectOpenAIImagesFromResponsesBody(body, time.Now)
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
	return openai.SummarizeOpenAIImagesNoOutputBodyWithSnippet(body, includeBody, maxSnippet)
}

func (s *OpenAIGatewayService) handleOpenAIImagesErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestBody []byte,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := s.readUpstreamErrorBody(resp)

	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logging.LegacyPrintf("service.openai_gateway",
			"OpenAI images upstream error %d (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.Record.ID,
			account.Record.Platform,
			account.Record.Type,
			truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	var decision accountcore.UpstreamErrorDecision
	return nil, gatewaymedia.ResolveImageResponseFailure(resp.StatusCode, upstreamMsg, gatewaymedia.ImageResponseFailurePorts{
		CyberMessage: func() (string, bool) {
			if !gatewayprovider.IsOpenAICyberWarningPayload(body, upstreamMsg) {
				return "", false
			}
			return gatewayprovider.ExtractOpenAICyberWarningMessage(body, upstreamMsg), true
		},
		Observe: func(kind, message string) {
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"), Kind: kind, Message: message, Detail: upstreamDetail})
		},
		Write: func(response gatewaymedia.ErrorResponse) error {
			upErr := &openai.OpenAIImagesUpstreamError{StatusCode: response.Status, ErrorType: response.Type, Message: response.Message, UpstreamRequestID: strings.TrimSpace(resp.Header.Get("x-request-id"))}
			writeOpenAIImagesUpstreamErrorResponse(c, upErr)
			return upErr
		},
		WrapCyber: func(cause error) error {
			return gatewayprovider.WrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, body, gatewayprovider.ExtractOpenAICyberWarningMessage(body, upstreamMsg), cause)
		},
		ApplyPolicy: func() {
			model := ""
			if len(requestedModel) > 0 {
				model = strings.TrimSpace(requestedModel[0])
			}
			decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, model)
		},
		Generic: func() bool { return decision.ShouldReturnGenericError() },
		Failover: func() bool {
			return decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, body))
		},
		NewFailover: func() error {
			return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body, RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode)}
		},
		Rewrite: func() (gatewaymedia.ErrorResponse, bool) {
			status, typ, message, matched := gatewayhttp.ApplyErrorPassthroughRule(c, account.Record.Platform, resp.StatusCode, body, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return gatewaymedia.ErrorResponse{Status: status, Type: typ, Message: logredact.SanitizeUpstreamQueries(message)}, matched
		},
		DefaultResponse: func() error {
			upErr := openai.OpenAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, body)
			writeOpenAIImagesUpstreamErrorResponse(c, upErr)
			return upErr
		},
	})
}

func openAIImagesStreamPrefix(parsed *gatewaymedia.ImageRequest) string {
	return openai.OpenAIImagesStreamPrefix(gatewaymedia.NativeImageRequest(parsed))
}

func writeOpenAIImagesUpstreamErrorResponse(c *gin.Context, err *openai.OpenAIImagesUpstreamError) bool {
	if err == nil {
		return false
	}
	return gatewayhttp.WriteImageError(c, &gatewayhttp.ImageErrorResponse{Status: err.ClientStatusCode(), Type: err.ClientErrorType(), Message: err.ClientMessage(), Code: err.Code, Param: err.Param}, func() int { return gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) }, func() { gatewayhttp.StopOpenAIImagesJSONKeepaliveCommitted(c) })
}

func (s *OpenAIGatewayService) parseOpenAIImagesSSEUsageBytes(data []byte, usage *s09openai.ForwardUsage) {
	openai.ParseOpenAIImagesSSEUsageBytes(data, usage)
}

func (s *OpenAIGatewayService) handleOpenAIImagesOAuthNonStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	responseFormat string,
	fallbackModel string,
) (s09openai.ForwardUsage, int, []string, error) {
	return openai.ReadImagesOAuthNonStreaming(resp, gatewayhttp.ResponseSink{Writer: c.Writer}, s.nativeImageResponseOptions(c), responseFormat, fallbackModel)
}

func (s *OpenAIGatewayService) handleOpenAIImagesOAuthStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	startTime time.Time,
	responseFormat string,
	streamPrefix string,
	fallbackModel string,
) (s09openai.ForwardUsage, int, []string, *int, error) {
	return openai.ReadImagesOAuthStreaming(resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeImageResponseOptions(c), startTime, responseFormat, streamPrefix, fallbackModel)
}

func (s *OpenAIGatewayService) forwardOpenAIImagesOAuth(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	parsed *gatewaymedia.ImageRequest,
	channelMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()
	requestModel, upstreamModel, err := gatewaymedia.ResolveImageModels(parsed.Model, channelMappedModel, "gpt-image-2", func(model string) string {
		return gatewayprovider.ExecutionModelPolicy(account).OpenAIUpstream(model, false, false)
	})
	if err != nil {
		return nil, err
	}
	logging.LegacyPrintf(
		"service.openai_gateway",
		"[OpenAI] Images request routing request_model=%s upstream_model=%s endpoint=%s account_type=%s uploads=%d",
		requestModel,
		upstreamModel,
		parsed.Endpoint,
		account.Record.Type,
		len(parsed.Uploads),
	)
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.executionCredentials.Resolve(upstreamCtx, gatewayprovider.ExecutionRecord(account))
	if err != nil {
		return nil, err
	}

	responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, upstreamModel)
	if err != nil {
		return nil, err
	}
	upstreamCtx = openai.WithOpenAIImagesSelfBuiltRequest(upstreamCtx)
	upstreamReq, err := s.buildUpstreamRequest(upstreamCtx, c, account, responsesBody, token, true, parsed.StickySessionSeed(), false, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "text/event-stream")
	upstreamReq.Header.Set("OpenAI-Beta", "responses=experimental")

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	options := s.nativeImageResponseOptions(c)
	var legacyHTTPResult *forwardcore.OpenAIResult
	httpFailure := false
	retryAgent := false
	target := &mediaprovider.ImagesOptions{

		AccountID: account.Record.ID,
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
			resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
			gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			return resp, err
		},

		TransportError: func(err error) error {
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			gatewayhttp.SetOpsUpstreamError(c, 0, safeErr, "")
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           account.Record.Platform,
				AccountID:          account.Record.ID,
				AccountName:        account.Record.Name,
				UpstreamStatusCode: 0,
				UpstreamURL:        logredact.SafeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "request_error",
				Message:            safeErr,
			})
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.readUpstreamErrorBody,

		RedactErrorBody: func(body []byte) []byte { return s.agentIdentity.Redact(upstreamCtx, account, body) },

		HTTPError: func(resp *http.Response, respBody []byte) error {
			httpFailure = true
			upstreamMsg := ""
			var decision accountcore.UpstreamErrorDecision
			retry, err := gatewaymedia.ResolveImageFailure(gatewaymedia.ImageFailurePorts{
				Recover: func() (bool, error) {
					if requeststate.AgentTaskRecoveryTried(ctx) || !s.agentIdentity.UsesAgentIdentity(ctx, account) || !openai.IsAgentTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
						return false, nil
					}
					return true, s.agentIdentity.Recover(ctx, account, account.View().GetCredential("task_id"))
				},
				Failover: func() bool {
					upstreamMsg = logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
					return s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody)
				},
				Observe: func() {
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

						Platform: account.Record.Platform,

						AccountID: account.Record.ID,

						AccountName: account.Record.Name,

						UpstreamStatusCode: resp.StatusCode,

						UpstreamRequestID: resp.Header.Get("x-request-id"),

						UpstreamURL: logredact.SafeUpstreamURL(upstreamReq.URL.String()),

						Kind: "failover",

						Message: upstreamMsg,
					})
				},
				ApplyPolicy: func() bool {
					decision = s.applyFailoverSideEffects(upstreamCtx, resp, account, respBody, requestModel)
					return decision.ShouldReturnGenericError()
				},
				NewFailover: func() error {
					retryableOnSameAccount := decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode)
					if account.View().IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
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
					return &forwardcore.UpstreamFailoverError{

						StatusCode: resp.StatusCode,

						ResponseBody: respBody,

						RetryableOnSameAccount: retryableOnSameAccount,
					}
				},
				Handle: func() error {
					var failure error
					legacyHTTPResult, failure = s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, account, responsesBody, requestModel)
					return failure
				},
			})
			retryAgent = retry
			return err
		},

		ResponseError: func(resp *http.Response, before int, err error) error {
			return s.handleOpenAIImagesOAuthResponseError(upstreamCtx, c, account, requestModel, logredact.SafeUpstreamURL(upstreamReq.URL.String()), resp, before, err)
		},
	}
	protocolID := protocol.ProtocolImagesGenerations
	if parsed.IsEdits() {
		protocolID = protocol.ProtocolImagesEdits
	}
	result, err := (mediaprovider.Images{Options: *target}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocolID, ResponseModel: requestModel, Stream: parsed.Stream}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if retryAgent {
		return s.forwardOpenAIImagesOAuth(requeststate.WithAgentTaskRecovery(ctx), c, account, parsed, channelMappedModel)
	}
	if httpFailure {
		return legacyHTTPResult, err
	}
	imageCount, retain := gatewaymedia.ImageOutcome(parsed.Stream, true, isEventStreamResponse(result.UpstreamHeaders), parsed.N, result.ObservedImages, err)
	if !retain {
		return nil, err
	}
	return openAIImagesForwardResult(result, parsed, imageCount), err
}

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
func shouldCoolOpenAIImagesToolForError(upstreamErr *openai.OpenAIImagesUpstreamError) bool {
	return upstreamErr != nil && !upstreamErr.SynthesizedFromModelText
}

func (s *OpenAIGatewayService) coolOpenAIImagesOAuthTool(ctx context.Context, account *gatewayprovider.ExecutionAccount) {
	if s == nil || s.accountRepo == nil || account == nil || account.Record.Platform != capability.PlatformOpenAI {
		return
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	cooldown := openai.OpenAIImagesOAuthUnavailableDefaultCooldown
	if s.settingService != nil {
		settings, err := s.settingService.Account.GetOpenAIImagesOAuthUnavailableCooldownSettings(stateCtx)
		if err != nil {
			logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool cooldown setting read failed error=%v", err)
		} else {
			cooldown = time.Duration(settings.CooldownMinutes) * time.Minute
		}
	}
	resetAt := time.Now().Add(cooldown)
	if err := s.accountRepo.SetModelRateLimit(stateCtx, account.Record.ID, accountcore.OpenAIImageGenerationRateLimitKey, resetAt, openai.OpenAIImagesOAuthUnavailableReason); err != nil {
		logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool cooldown write failed account_id=%d error=%v", account.Record.ID, err)
		return
	}
	logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool unavailable account_id=%d reset_in=%s", account.Record.ID, time.Until(resetAt).Truncate(time.Second))
}

func (s *OpenAIGatewayService) handleOpenAIImagesOAuthResponseError(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestedModel string,
	upstreamURL string,
	resp *http.Response,
	writerSizeBeforeResponse int,
	err error,
) error {
	responseWritten := c != nil && c.Writer != nil && gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) != writerSizeBeforeResponse
	if code, message, ok := openai.OpenAIUpstreamStreamReadErrorDetails(err); ok {
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
		retryable := decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), statusCode, true)
		kind := "http_error"
		if retryable {
			kind = "failover"
			if responseWritten {
				kind = "retry_exhausted_failover"
			}
		}
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

			Platform: account.Record.Platform,

			AccountID: account.Record.ID,

			AccountName: account.Record.Name,

			UpstreamStatusCode: statusCode,

			UpstreamRequestID: requestID,

			UpstreamURL: upstreamURL,

			Kind: kind,

			Message: message,
		})
		if !retryable || responseWritten {
			return err
		}
		return &forwardcore.UpstreamFailoverError{

			StatusCode: statusCode,

			ResponseBody: responseBody,

			ResponseHeaders: headers,

			RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), statusCode),
		}
	}

	var upstreamErr *openai.OpenAIImagesUpstreamError
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
	responseBody := openai.OpenAIImagesUpstreamErrorResponseBody(upstreamErr)
	decision := s.applyOpenAIAccountUpstreamError(ctx, account, upstreamErr.StatusCode, headers, responseBody, requestedModel)
	retryable := decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), upstreamErr.StatusCode, openai.IsOpenAIImagesRetryableUpstreamError(upstreamErr))
	kind := "http_error"
	if retryable {
		kind = "failover"
		if responseWritten {
			kind = "retry_exhausted_failover"
		}
	}
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

		Platform: account.Record.Platform,

		AccountID: account.Record.ID,

		AccountName: account.Record.Name,

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
		!shouldDisable && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(upstreamErr.StatusCode),
	)
}
