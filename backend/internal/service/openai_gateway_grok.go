package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	wireanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	// Composer 图片桥接使用 Grok Build 生成简洁描述，限制输出长度以控制额外用量。
	grokComposerImageBridgeVisionModel     = "grok-build-0.1"
	grokComposerImageBridgeMaxOutputTokens = 512
	grokCLIVersion                         = nativegrok.CLIClientVersion
	grokDefaultResponsesModel              = nativegrok.DefaultResponsesModel
	grokRateLimitFallbackCooldown          = 2 * time.Minute
	grokRateLimitRepeatCooldown            = 10 * time.Minute
	grokRateLimitSustainedCooldown         = 30 * time.Minute
	grokRateLimitMaxAdaptiveCooldown       = time.Hour
	grokRateLimitBackoffQuietPeriod        = time.Hour
)

func (s *OpenAIGatewayService) forwardGrokResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	if account.Type != AccountTypeOAuth && account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("grok account type %s is not supported by Responses forwarding", account.Type)
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if isGrokImageGenerationModel(upstreamModel) {
		err := fmt.Errorf("model %s is an image model and is not available on the Responses endpoint; use /v1/images/generations instead", upstreamModel)
		// 这是客户端点选择错误，直接返回 400，避免 handler 将普通错误改写为通用 502。
		if c != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"type":    "invalid_request_error",
					"message": err.Error(),
					"param":   "model",
				},
			})
		}
		return nil, err
	}
	patchedBody, clientToolMapping, err := patchGrokResponsesBodyWithClientTools(body, upstreamModel)
	if err != nil {
		setOpsUpstreamError(c, http.StatusBadRequest, err.Error(), "")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"type": "invalid_request_error", "message": err.Error(), "param": "tools",
		}})
		return nil, err
	}
	setGrokResponsesClientToolMapping(c, clientToolMapping)
	// xAI 没有原生 /responses/compact；改成普通 Responses 摘要轮次，响应阶段再封装为 compaction 条目。
	if isOpenAIResponsesCompactPath(c) {
		patchedBody, err = buildGrokCompactRequestBody(patchedBody)
		if err != nil {
			return nil, err
		}
	}
	// 从 xAI 实际接收的请求派生身份，使 Codex Responses Lite 的 additional_tools
	// 成为稳定工具前缀的一部分。若 Claude Code session 只存在于 metadata.user_id，
	// 则在 metadata 被剥离前使用原始请求保留该身份。
	cacheIdentityBody := patchedBody
	if extractClaudeCodeSessionIDFromPayload(body) != "" {
		cacheIdentityBody = body
	}
	cacheIdentity := resolveGrokCacheIdentity(c, cacheIdentityBody, "", upstreamModel)
	mixedCacheIntentBody := append([]byte(nil), patchedBody...)
	patchedBody, err = applyGrokResponsesCacheIdentity(patchedBody, body, cacheIdentity, account.IsGrokOAuth())
	if err != nil {
		return nil, fmt.Errorf("apply grok prompt cache identity: %w", err)
	}
	// Free OAuth 携带客户端函数工具时复用混合工具缓存路由，补齐 web_search/x_search，
	// 避免 xAI 强制落到不可缓存的 build-free 层级。
	patchedBody, err = applyGrokFreeRequestToolCacheRoute(c, patchedBody, mixedCacheIntentBody, account, cacheIdentity)
	if err != nil {
		return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", err)
	}

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	upstreamStart := time.Now()
	var handled bool
	var handledResult *OpenAIForwardResult
	var handleErr error
	target := &nativegrok.ResponsesTarget{

		AccountID: account.ID,

		Model: upstreamModel,

		Enter: s.nativeAttemptActivity,

		Exchange: nativegrok.ResponsesExchange{

			Build: func(body []byte) (*http.Request, error) {
				return buildGrokResponsesRequest(upstreamCtx, c, account, body, token, cacheIdentity, s.cfg, s.settingService)
			},

			Do: func(req *http.Request) (*http.Response, error) {
				return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
			},

			ReadError: s.readUpstreamErrorBody,

			AfterExchange: func(err error) error {
				SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
				if err != nil {
					return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
				}
				return nil
			},

			OnReplay: func() {
				slog.Info("grok_replay_decode_retry", "account_id", account.ID, "cache_identity_present", cacheIdentity != "")
			},
		},

		BeforeResponse: func(resp *http.Response, body []byte) (bool, error) {
			patchedBody = body

			if resp.StatusCode >= 400 {
				handled = true
				respBody := s.readUpstreamErrorBody(resp)
				resp.Body = io.NopCloser(bytes.NewReader(respBody))
				upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
				if upstreamMsg == "" {
					upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
				}
				errCtx := withGrokTeamRateLimitModel(ctx, upstreamModel)
				decision := s.applyGrokAccountUpstreamError(errCtx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
				kind := "http_error"
				if decision.ShouldFailover(account, resp.StatusCode, s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)) {
					kind = "failover"
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

					Platform: account.Platform,

					AccountID: account.ID,

					AccountName: account.Name,

					UpstreamStatusCode: resp.StatusCode,

					UpstreamRequestID: firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),

					Kind: kind,

					Message: upstreamMsg,
				})
				if decision.ShouldReturnGenericError() {
					handledResult, handleErr = s.handleErrorResponse(ctx, resp, c, account, patchedBody, upstreamModel)
					return true, handleErr
				}
				// 配额/限流响应写入团队模型覆盖层；容量属于请求压力，不应隐藏健康账号。
				if shouldMarkGrokTeamModelRateLimit(resp.StatusCode, respBody) {
					markGrokTeamModelRateLimit(account, upstreamModel, resolveGrokTeamRateLimitUntil(time.Now().Add(grokTeamRateLimitDefaultTTL), time.Now()))
				}
				if kind == "failover" {
					retryable, retryDelay, retryDeadline, retryMax := grokSameAccountRetryMetadata(account, resp.StatusCode, respBody)
					return true, &UpstreamFailoverError{

						StatusCode: resp.StatusCode,

						ResponseBody: respBody,

						ResponseHeaders: resp.Header.Clone(),

						RetryableOnSameAccount: retryable || decision.RetryableOnSameAccount(account, resp.StatusCode),

						RequestScopedTransient: retryable && resp.StatusCode == http.StatusTooManyRequests,

						SameAccountRetryDelay: retryDelay,

						SameAccountRetryDeadline: retryDeadline,

						SameAccountRetryMax: retryMax,
					}
				}
				handledResult, handleErr = s.handleErrorResponse(ctx, resp, c, account, patchedBody, upstreamModel)
				return true, handleErr
			}

			s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, upstreamModel), account, resp.Header, resp.StatusCode)
			return false, nil
		},

		MaxLineSize: defaultMaxLineSize,

		ClientTools: clientToolMapping,

		ReadResponse: func(resp *http.Response, input upstream.AttemptInput, sink upstream.OutputSink) (upstream.ResponsesObservation, error) {
			// 共享 OpenAI 入站适配暂留 S09.8/S11；平台不持有 Gin，调用时沿用该请求的原响应观察。
			if input.Stream {
				v, err := s.readStreamingResponseObservation(ctx, resp, c, account, startTime, originalModel, upstreamModel, "")
				if v == nil {
					return upstream.ResponsesObservation{}, err
				}
				return upstream.ResponsesObservation{

					Usage: v.usage,

					HasUsage: v.hasUsage,

					Served: v.served,

					HTTPCommitted: v.httpCommitted,

					RetryCommitted: v.retryCommitted,

					ClientDisconnected: v.clientDisconnected,

					FirstSemanticOutput: v.firstSemanticOutput,

					FirstTokenMs: v.firstTokenMs,

					ResponseID: v.responseID,

					SearchCount: v.searchCount,

					ImageCount: v.imageCount,

					ImageOutputSizes: v.imageOutputSizes,
				}, err
			}
			v, err := s.handleNonStreamingResponse(ctx, resp, c, account, originalModel, upstreamModel)
			if err != nil {
				return upstream.ResponsesObservation{}, err
			}
			return upstream.ResponsesObservation{

				Usage: v.usage,

				HasUsage: v.usage != nil,
				Served:   v.served,

				HTTPCommitted: c.Writer.Written(),

				RetryCommitted: IsResponseCommitted(c),

				ResponseID: v.responseID,

				SearchCount: v.searchCount,

				ImageCount: v.imageCount,

				ImageOutputSizes: v.imageOutputSizes,
			}, nil
		},
	}
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		target.MaxLineSize = s.cfg.Gateway.MaxLineSize
	}
	var sink upstream.OutputSink
	if c != nil {
		sink = gatewayhttp.ResponseSink{Writer: c.Writer}
	}
	nativeResult, err := (nativegrok.ResponsesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIResponses, Body: patchedBody, ResponseModel: originalModel, Stream: reqStream, Target: target}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		return nil, err
	}
	usage := &OpenAIUsage{

		InputTokens: nativeResult.Usage.InputTokens,

		ImageInputTokens: nativeResult.ImageInputTokens,

		OutputTokens: nativeResult.Usage.OutputTokens,

		CacheCreationInputTokens: nativeResult.Usage.CacheCreationInputTokens,

		CacheReadInputTokens: nativeResult.Usage.CacheReadInputTokens,

		ImageOutputTokens: nativeResult.Usage.ImageOutputTokens,
	}
	firstTokenMs := nativeResult.FirstTokenMs
	responseID := nativeResult.ResponseID
	searchCount := nativeResult.SearchCount
	imageCount := nativeResult.ObservedImages
	imageOutputSizes := nativeResult.ImageOutputSizes

	reasoningEffort := extractOpenAIReasoningEffortFromBody(patchedBody, originalModel)
	result := &OpenAIForwardResult{

		RequestID: nativeResult.RequestID,

		UpstreamHeaders: nativeResult.UpstreamHeaders,

		ResponseID: responseID,

		Usage: *usage,

		Model: originalModel,

		BillingModel: billingModel,

		UpstreamModel: upstreamModel,

		ReasoningEffort: reasoningEffort,

		Stream: reqStream,

		OpenAIWSMode: false,

		ResponseHeaders: nativeResult.UpstreamHeaders.Clone(),

		Duration: time.Since(startTime),

		FirstTokenMs: firstTokenMs,
	}
	// 从共享 Responses 处理器传递搜索与图片计数；否则流式或 JSON 统计虽会运行，
	// 但 search_price_per_1k 与图片费用不会生效。
	if searchCount > 0 {
		result.SearchCount = searchCount
	}
	if imageCount > 0 {
		result.ImageCount = imageCount
		result.ImageOutputSizes = imageOutputSizes
	}
	return result, nil
}

func isGrokInvalidEncryptedContentResponse(statusCode int, body []byte) bool {
	return grokBodyCodec().IsGrokInvalidEncryptedContentResponse(statusCode, body)
}

func isGrokCompactionReplayDecodeError(statusCode int, body []byte) bool {
	return grokBodyCodec().IsGrokCompactionReplayDecodeError(statusCode, body)
}

func sanitizeGrokCompactionReplayBody(body []byte) ([]byte, bool, error) {
	return grokBodyCodec().SanitizeGrokCompactionReplayBody(body)
}

func requestHasGrokEncryptedReasoning(body []byte) bool {
	return grokBodyCodec().RequestHasGrokEncryptedReasoning(body)
}

func markGrokEncryptedContentStripRetried(ctx context.Context) context.Context {
	return grokBodyCodec().MarkGrokEncryptedContentStripRetried(ctx)
}

func grokEncryptedContentStripRetried(ctx context.Context) bool {
	return grokBodyCodec().GrokEncryptedContentStripRetried(ctx)
}

func stripAnthropicThinkingSignatures(body []byte) ([]byte, bool) {
	return wireanthropic.StripThinkingSignaturesJSON(body)
}

func trimGrokInvalidEncryptedContentRetryBody(body []byte) ([]byte, bool, error) {
	return grokBodyCodec().TrimGrokInvalidEncryptedContentRetryBody(body)
}

func patchGrokResponsesBody(body []byte, upstreamModel string) ([]byte, error) {
	return grokBodyCodec().PatchGrokResponsesBody(body, upstreamModel)
}

func patchGrokResponsesBodyWithClientTools(body []byte, upstreamModel string) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	return grokBodyCodec().PatchGrokResponsesBodyWithClientTools(body, upstreamModel)
}

func normalizeGrokChatReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	return grokBodyCodec().NormalizeGrokChatReasoningEffort(body, upstreamModel)
}

func GrokSupportsXHighReasoningEffort(model string) bool {
	return grokBodyCodec().GrokSupportsXHighReasoningEffort(model)
}

func sanitizeGrokResponsesInput(body []byte) ([]byte, error) {
	return grokBodyCodec().SanitizeGrokResponsesInput(body)
}

func (s *OpenAIGatewayService) bridgeGrokComposerImageInputs(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) ([]byte, OpenAIUsage, bool, error) {
	return nativegrok.BridgeComposerImages(body, func(imageURL string, index int) (string, OpenAIUsage, error) {
		return s.describeGrokComposerImage(ctx, c, account, token, imageURL, index)
	})
}

// describeGrokComposerImage 复用 Grok Responses 转发配置执行单张图片预检，
// 并沿用账号快照、错误记录和故障转移策略。
func (s *OpenAIGatewayService) describeGrokComposerImage(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token string,
	imageURL string,
	index int,
) (string, OpenAIUsage, error) {
	body, err := buildGrokComposerImageDescriptionBody(imageURL, index)
	if err != nil {
		return "", OpenAIUsage{}, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	// 图片描述探测是辅助请求而非会话轮次，不能绑定调用方的 Grok 提示缓存身份。
	upstreamReq, err := buildGrokResponsesRequest(upstreamCtx, c, account, body, token, "", s.cfg)
	releaseUpstreamCtx()
	if err != nil {
		return "", OpenAIUsage{}, fmt.Errorf("build grok composer image bridge request: %w", err)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	var description string
	var usage OpenAIUsage
	target := &nativegrok.ResponsesTarget{

		AccountID: account.ID,

		Model: grokComposerImageBridgeVisionModel,

		Enter: s.nativeAttemptActivity,

		PassRawStream: true,

		Exchange: nativegrok.ResponsesExchange{

			SingleExchange: true,

			Build: func([]byte) (*http.Request, error) { return upstreamReq, nil },

			Do: func(req *http.Request) (*http.Response, error) {
				return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
			},

			ReadError: s.readUpstreamErrorBody,

			AfterExchange: func(err error) error {
				if err != nil {
					return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
				}
				return nil
			},
		},

		BeforeResponse: func(resp *http.Response, _ []byte) (bool, error) {

			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
				if upstreamMsg == "" {
					upstreamMsg = fmt.Sprintf("xAI image bridge upstream returned status %d", resp.StatusCode)
				}
				decision := s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, grokComposerImageBridgeVisionModel)
				kind := "http_error"
				if decision.ShouldFailover(account, resp.StatusCode, s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)) {
					kind = "failover"
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

					Platform: account.Platform,

					AccountID: account.ID,

					AccountName: account.Name,

					UpstreamStatusCode: resp.StatusCode,

					UpstreamRequestID: firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),

					Kind: kind,

					Message: upstreamMsg,
				})
				if decision.ShouldReturnGenericError() {
					return true, fmt.Errorf("grok composer image bridge upstream gateway error")
				}
				if kind == "failover" {
					retryable, retryDelay, retryDeadline, retryMax := grokSameAccountRetryMetadata(account, resp.StatusCode, respBody)
					return true, &UpstreamFailoverError{

						StatusCode: resp.StatusCode,

						ResponseBody: respBody,

						ResponseHeaders: resp.Header.Clone(),

						RetryableOnSameAccount: retryable || decision.RetryableOnSameAccount(account, resp.StatusCode),

						RequestScopedTransient: retryable && resp.StatusCode == http.StatusTooManyRequests,

						SameAccountRetryDelay: retryDelay,

						SameAccountRetryDeadline: retryDeadline,

						SameAccountRetryMax: retryMax,
					}
				}
				return true, fmt.Errorf("grok composer image bridge upstream error: %s", upstreamMsg)
			}

			s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, grokComposerImageBridgeVisionModel), account, resp.Header, resp.StatusCode)
			return false, nil
		},

		ReadResponse: func(resp *http.Response, _ upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			data, readErr := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
			if readErr != nil {
				return upstream.ResponsesObservation{}, fmt.Errorf("read grok composer image bridge response: %w", readErr)
			}
			var decodeErr error
			description, usage, decodeErr = nativegrok.DecodeComposerDescription(data)
			return upstream.ResponsesObservation{Usage: &usage, HasUsage: openAIUsageHasTokens(&usage), Served: description != ""}, decodeErr
		},
	}
	_, err = (nativegrok.ResponsesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{
		Protocol: protocol.ProtocolOpenAIResponses,
		Body:     body,
		Target:   target,
	}, nil)
	return description, usage, err

}

func buildGrokComposerImageDescriptionBody(imageURL string, index int) ([]byte, error) {
	return grokBodyCodec().BuildGrokComposerImageDescriptionBody(imageURL, index)
}

func addOpenAIUsage(dst *OpenAIUsage, usage OpenAIUsage) { protocolopenai.AddForwardUsage(dst, usage) }

func buildGrokResponsesRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token, cacheIdentity string, cfg *config.Config, settings ...*SettingService) (*http.Request, error) {
	targetURL, err := buildGrokResponsesURL(account, cfg, settings...)
	if err != nil {
		return nil, err
	}
	beta := ""
	if c != nil {
		beta = c.GetHeader("OpenAI-Beta")
	}
	return nativegrok.BuildResponsesRequest(ctx, body, nativegrok.ResponsesRequestOptions{

		URL: targetURL,

		Token: token,

		CacheIdentity: cacheIdentity,

		OAuth: account.IsGrokOAuth(),

		OpenAIBeta: beta,

		Profile: func(ctx context.Context) context.Context {
			return WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileGrok)
		},

		ApplyOverrides: account.ApplyHeaderOverrides,
	})
}

func applyGrokCLIHeaders(headers http.Header) { nativegrok.ApplyCLIHeaders(headers) }

func (s *OpenAIGatewayService) updateGrokUsageSnapshot(ctx context.Context, account *Account, snapshot *nativegrok.QuotaSnapshot) {
	s.updateGrokUsageSnapshotWithRateLimit(ctx, account, snapshot, true)
}

func (s *OpenAIGatewayService) updateGrokUsageSnapshotWithRateLimit(ctx context.Context, account *Account, snapshot *nativegrok.QuotaSnapshot, installRateLimit bool) {
	if s == nil || account == nil || account.ID <= 0 || snapshot == nil {
		return
	}
	accountID := account.ID
	now := time.Now()
	resetAt, hasActiveLimit := grokRateLimitResetAtForAccount(account, snapshot, now)
	if hasActiveLimit {
		normalizeGrokExhaustedWindowResets(snapshot, resetAt, now)
	}
	recovery := isSuccessfulGrokRateLimitRecovery(account, snapshot)
	critical := snapshot.StatusCode == http.StatusTooManyRequests || hasActiveLimit || recovery
	if s.codexSnapshotThrottle != nil {
		allowed := s.codexSnapshotThrottle.Allow(accountID, now)
		if !critical && !allowed {
			return
		}
	}

	updates := map[string]any{
		grokQuotaSnapshotExtraKey: snapshot,
	}
	// 同时派生 grokThresholdCandidates 评估器读取的调度阈值扩展字段 grok_sched_*。
	// 缺少此写入逻辑时，管理员配置的 Grok 自动暂停阈值无法触发。
	for k, v := range buildGrokSchedulerExtraUpdates(snapshot) {
		updates[k] = v
	}
	stateCtx := ctx
	if hasActiveLimit {
		var cancel context.CancelFunc
		stateCtx, cancel = openAIAccountStateContext(ctx)
		defer cancel()
	}
	// 请求路径中的 Account 指针来自每次 Redis/DB 解码，不是进程内共享缓存；
	// 这里与 token 刷新和限流写入保持一致，调用方不得跨 goroutine 复用同一指针。
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[grokQuotaSnapshotExtraKey] = snapshot
	if s.accountRepo != nil {
		_ = s.accountRepo.UpdateExtra(stateCtx, accountID, updates)
	}
	// 池模式上游本身负责在真实账号池中切换，额度头只作为观测数据保留，不能反向
	// 冷却本地这个聚合账号。非池模式仍将错误响应或成功后耗尽的窗口写成真实限流。
	if installRateLimit && hasActiveLimit && !account.IsPoolMode() {
		s.rateLimitGrok(stateCtx, account, resetAt)
	} else if recovery {
		clearGrokRateLimitAfterRecovery(stateCtx, s.accountRepo, account)
	}
}

func (s *OpenAIGatewayService) updateGrokUsageFromResponse(ctx context.Context, account *Account, headers http.Header, statusCode int) {
	snapshot := parseGrokQuotaSnapshot(headers, statusCode, time.Now())
	if snapshot != nil {
		stampGrokQuotaSnapshotForPlan(account, snapshot, grokRequestedModelFromCtx(ctx))
		s.updateGrokUsageSnapshot(ctx, account, snapshot)
		return
	}
	// 即使上游没有返回可选的额度响应头，成功响应仍能证明账号已经恢复。
	// 不用空快照覆盖已有信息，只清除本次请求观察到的精确冷却代次。
	recoverySnapshot := &nativegrok.QuotaSnapshot{StatusCode: statusCode}
	if isSuccessfulGrokRateLimitRecovery(account, recoverySnapshot) {
		clearGrokRateLimitAfterRecovery(ctx, s.accountRepo, account)
	}
}

func parseGrokQuotaSnapshot(headers http.Header, statusCode int, now time.Time) *nativegrok.QuotaSnapshot {
	snapshot := nativegrok.ParseQuotaHeaders(headers, statusCode)
	if snapshot == nil && statusCode == http.StatusTooManyRequests {
		return &nativegrok.QuotaSnapshot{
			StatusCode: statusCode,
			UpdatedAt:  now.UTC().Format(time.RFC3339),
		}
	}
	return snapshot
}

func normalizeGrokExhaustedWindowResets(snapshot *nativegrok.QuotaSnapshot, resetAt, now time.Time) {
	accountcore.NormalizeGrokExhaustedWindowResets(snapshot, resetAt, now)
}

func grokRateLimitResetAtForAccount(account *Account, snapshot *nativegrok.QuotaSnapshot, now time.Time) (time.Time, bool) {
	return accountcore.GrokRateLimitResetAtForAccount(AccountRecordView(account), snapshot, now)
}

func normalizeGrokRateLimitResetAt(account *Account, resetAt, now time.Time) time.Time {
	return accountcore.NormalizeGrokRateLimitResetAt(AccountRecordView(account), resetAt, now)
}

type grokRateLimitExtendingRepository interface {
	SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error
}

type grokRateLimitRecoveryRepository interface {
	ClearRateLimitIfObserved(ctx context.Context, id int64, observedLimitedAt, observedResetAt time.Time) (bool, error)
}

func isSuccessfulGrokRateLimitRecovery(account *Account, snapshot *nativegrok.QuotaSnapshot) bool {
	return accountcore.IsSuccessfulGrokRateLimitRecovery(AccountRecordView(account), snapshot)
}

func clearGrokRateLimitAfterRecovery(ctx context.Context, repo AccountRepository, account *Account) {
	if repo == nil || account == nil || account.RateLimitedAt == nil || account.RateLimitResetAt == nil || ctx.Err() != nil {
		return
	}
	recoveryRepo, ok := repo.(grokRateLimitRecoveryRepository)
	if !ok {
		return
	}
	_, err := recoveryRepo.ClearRateLimitIfObserved(ctx, account.ID, *account.RateLimitedAt, *account.RateLimitResetAt)
	if err != nil {
		slog.Warn("grok_rate_limit_recovery_clear_failed", "account_id", account.ID, "error", err)
	}
}

func persistGrokRateLimit(ctx context.Context, repo AccountRepository, account *Account, resetAt time.Time) {
	if repo == nil || account == nil || account.ID <= 0 {
		return
	}
	resetAt = normalizeGrokRateLimitResetAt(account, resetAt, time.Now())
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	var err error
	if extendingRepo, ok := repo.(grokRateLimitExtendingRepository); ok {
		err = extendingRepo.SetRateLimitedIfLater(stateCtx, account.ID, resetAt)
	} else {
		err = repo.SetRateLimited(stateCtx, account.ID, resetAt)
	}
	if err != nil {
		slog.Warn("persist_grok_rate_limit_failed", "account_id", account.ID, "reset_at", resetAt.UTC(), "error", err)
	}
}

func (s *OpenAIGatewayService) rateLimitGrok(ctx context.Context, account *Account, resetAt time.Time) {
	if s == nil || account == nil {
		return
	}
	now := time.Now()
	resetAt = normalizeGrokRateLimitResetAt(account, resetAt, now)

	runtimeUntil := resetAt
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(runtimeUntil) {
		runtimeUntil = *account.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, runtimeUntil, "429")
	persistGrokRateLimit(ctx, s.accountRepo, account, resetAt)

	// 扩散短期团队与模型冷却，使同一 xAI 团队的关联 OAuth 账号跳过热点模型，
	// 无需等待每个账号分别收到 429。模型优先取最新请求上下文；为空时
	// markGrokTeamModelRateLimit 不执行操作。
	if model, _ := ctx.Value(grokTeamRateLimitModelContextKey{}).(string); model != "" {
		markGrokTeamModelRateLimit(account, model, resolveGrokTeamRateLimitUntil(resetAt, now))
	}
}

func buildGrokSchedulerExtraUpdates(snapshot *nativegrok.QuotaSnapshot) map[string]any {
	return accountcore.BuildGrokSchedulerExtraUpdates(snapshot)
}

// grokTeamRateLimitModelContextKey 在上下文中携带团队冷却所需的上游模型。
type grokTeamRateLimitModelContextKey struct{}

// withGrokTeamRateLimitModel 附加上游模型名，供团队与模型冷却等限流副作用使用。
// 模型为空时可安全调用。
func withGrokTeamRateLimitModel(ctx context.Context, model string) context.Context {
	model = strings.TrimSpace(model)
	if model == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, grokTeamRateLimitModelContextKey{}, model)
}

// grokRequestedModelFromCtx 读取当前请求实际使用的 Grok 上游模型。
func grokRequestedModelFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	model, _ := ctx.Value(grokTeamRateLimitModelContextKey{}).(string)
	return strings.TrimSpace(model)
}

func persistGrokTransientModelCooldown(account *Account, decision GrokUpstreamFailureDecision) bool {
	if account == nil {
		return false
	}
	model := strings.TrimSpace(decision.Model)
	if model == "" {
		return false
	}
	cooldown := decision.Cooldown
	if cooldown <= 0 {
		cooldown = 3 * time.Minute
	}
	markGrokModelTransientBlock(account.ID, model, time.Now().Add(cooldown))
	return true
}

// applyGrokAccountUpstreamError 先处理精确模型状态和显式策略，再处理非池模式的 Grok 默认状态。
// 调用方应传入账号映射后的模型；这里仅补做幂等的平台规范化，绝不再次执行账号映射。
func (s *OpenAIGatewayService) applyGrokAccountUpstreamError(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	responseBody []byte,
	canonicalModel ...string,
) UpstreamErrorDecision {
	if s == nil || account == nil {
		return UpstreamErrorDecision{Policy: ErrorPolicyNone}
	}
	if isOpenAIAccountPolicyRequestScopedError(account, statusCode, responseBody) {
		return UpstreamErrorDecision{Policy: ErrorPolicyNone}
	}
	if model := firstRequestedModel(canonicalModel); model != "" {
		canonicalModel = []string{normalizeOpenAIModelForUpstream(account, model)}
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	stateCtx = withTempUnschedulableModel(stateCtx, canonicalModel)

	now := time.Now()
	quotaSnapshot := parseGrokQuotaSnapshot(headers, statusCode, now)
	quotaModel := firstRequestedModel(canonicalModel)
	if quotaModel == "" {
		quotaModel = grokRequestedModelFromCtx(ctx)
	}
	stampGrokQuotaSnapshotForPlan(account, quotaSnapshot, quotaModel)
	// 模型容量 429 属于请求压力，只保存额度观测，不安装账号级限流状态。
	snapshotFailure := classifyGrokUpstreamFailure(statusCode, responseBody, quotaModel)
	s.updateGrokUsageSnapshotWithRateLimit(stateCtx, account, quotaSnapshot, snapshotFailure.Class != GrokFailureModelCapacity)

	decision := upstreamErrorDecisionWithoutPersistence(account, statusCode)
	if s.rateLimitService != nil {
		if account.IsPoolMode() || account.IsCustomErrorCodesEnabled() {
			decision.Policy = s.rateLimitService.ApplyExplicitErrorPolicy(stateCtx, account, statusCode, responseBody, canonicalModel...)
			decision.StopScheduling = decision.Policy == ErrorPolicyCustomMatched || decision.Policy == ErrorPolicyTempUnscheduled
		} else {
			decision = UpstreamErrorDecision{Policy: ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		return decision
	case ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case ErrorPolicyCustomSkipped, ErrorPolicyPoolBypassed:
		return decision
	}

	if s.rateLimitService != nil && len(canonicalModel) > 0 &&
		s.rateLimitService.HandleUpstreamModelNotFound(stateCtx, account, canonicalModel[0], statusCode, responseBody) {
		decision.StopScheduling = true
		return decision
	}
	// 普通账号先保留模型不存在等精确处理，再应用管理员临时规则。
	if s.rateLimitService != nil && statusCode != http.StatusUnauthorized &&
		!account.IsPoolMode() && !account.IsCustomErrorCodesEnabled() &&
		s.rateLimitService.HandleTempUnschedulable(stateCtx, account, statusCode, responseBody, canonicalModel...) {
		decision.Policy = ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if firstRequestedModel(canonicalModel) == "" {
			s.BlockAccountScheduling(account, time.Time{}, "upstream_disable")
		}
		return decision
	}

	// Grok API Key 的 5xx 与 OpenAI API Key 共用账号+最终模型的瞬态冷却；
	// OAuth 和模型未知的请求继续沿用账号级退避，避免扩大既有行为变化。
	model := firstRequestedModel(canonicalModel)
	if model == "" {
		model = quotaModel
	}
	if account.Type == AccountTypeAPIKey && model != "" && statusCode >= 500 &&
		shouldCooldownOpenAITransientUpstreamError(statusCode, responseBody) {
		s.recordOpenAICompatibleModelTransientFailure(account, model)
		return decision
	}

	// 响应体中的免费额度、账单、空输出和容量语义优先于通用状态码处理。
	failure := classifyGrokUpstreamFailure(statusCode, responseBody, model)
	if failure.ShouldCooldown && failure.Class != GrokFailureNone && failure.Class != GrokFailureRateLimit {
		if failure.Class == GrokFailureFreeUsage {
			if resetAt, limited := grokRateLimitResetAtForAccount(account, quotaSnapshot, now); limited && resetAt.After(now) {
				if failure.Model != "" && isGrokModelSpecificFreeUsage(strings.ToLower(failure.Reason), failure.Model) {
					markGrokModelQuotaBlock(account.ID, failure.Model, resetAt)
					decision.StopScheduling = true
					return decision
				}
				s.rateLimitGrok(stateCtx, account, resetAt)
				decision.StopScheduling = true
				return decision
			}
		}
		if s.applyGrokUpstreamFailureDecision(stateCtx, account, failure) {
			decision.StopScheduling = true
			return decision
		}
	}

	switch statusCode {
	case http.StatusUnauthorized:
		s.tempUnscheduleGrok(stateCtx, account, 10*time.Minute, "grok credentials unauthorized")
		decision.StopScheduling = true
		return decision
	case http.StatusPaymentRequired:
		// 402 表示当前账号计费不可用，短期排除以避免后续请求反复命中。
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok payment required")
		decision.StopScheduling = true
		return decision
	case http.StatusForbidden:
		if s.applyGrokForbiddenPolicy(stateCtx, account, responseBody) {
			decision.StopScheduling = true
			return decision
		}
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok access or entitlement denied")
		decision.StopScheduling = true
		return decision
	case http.StatusMethodNotAllowed:
		// 当前账号不支持所选 Grok 端点，临时排除可避免粘性会话反复命中同一账号。
		s.tempUnscheduleGrok(stateCtx, account, 30*time.Minute, "grok endpoint not supported (405)")
		decision.StopScheduling = true
		return decision
	case http.StatusTooManyRequests:
		// updateGrokUsageSnapshot 已同时写入运行时和持久化限流状态。
		decision.StopScheduling = true
		return decision
	default:
		if statusCode >= 500 {
			s.tempUnscheduleGrok(stateCtx, account, 2*time.Minute, "grok upstream temporary error")
			decision.StopScheduling = true
			return decision
		}
	}
	return decision
}

// isGrokSpendingLimitError 判断响应体是否表示 xAI 消费限额或余额耗尽。
func isGrokSpendingLimitError(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "code").String(),
		gjson.GetBytes(responseBody, "error.code").String(),
	)))
	if code == "personal-team-blocked:spending-limit" {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "error").String(),
		gjson.GetBytes(responseBody, "error.message").String(),
		gjson.GetBytes(responseBody, "message").String(),
	)))
	return strings.Contains(message, "spending limit") || strings.Contains(message, "run out of credits")
}

func (s *OpenAIGatewayService) tempUnscheduleGrok(ctx context.Context, account *Account, cooldown time.Duration, reason string) {
	if s == nil || account == nil {
		return
	}
	until := time.Now().Add(cooldown)
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(until) {
		until = *account.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, until, reason)
	if s.accountRepo != nil {
		stateCtx, cancel := openAIAccountStateContext(ctx)
		defer cancel()
		_ = s.accountRepo.SetTempUnschedulable(stateCtx, account.ID, until, reason)
	}
}
