package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"time"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// Forward 保持单次 Responses 的准备、协议分派和执行顺序。
func (s *OpenAIResponsesExecutor) Forward(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) (*forwardcore.OpenAIResult, error) {
	var routeErr error
	account, routeErr = gatewayprovider.AccountForProtocolAttempt(ctx, account)
	if routeErr != nil {
		return nil, routeErr
	}
	profile := openAIForwardProfile(account)
	prepared, err := openaiexecution.PreparePrelude(ctx, body, profile, openAIForwardPreludeAdapter{s: s, c: c, account: account, ctx: ctx})
	if err != nil {
		return nil, err
	}
	body = prepared.Body
	startTime := prepared.StartedAt
	canonicalImageIntentBody := prepared.CanonicalImageIntentBody
	tlsRouterMatch := prepared.TLS
	wsDecision := egress.OpenAIWSProtocolDecision{Transport: egress.OpenAIUpstreamTransport(prepared.Transport.Transport), Reason: prepared.Transport.Reason}
	originalBody := prepared.OriginalBody
	requestView := prepared.View
	reqModel, reqStream, promptCacheKey := requestView.Model, requestView.Stream, requestView.PromptCacheKey
	originalModel := reqModel
	isCodexCLI := prepared.CodexCLI
	switch prepared.Route {
	case openaiexecution.DispatchRawChat:
		return s.Text.ResponsesViaRawChat(ctx, c, account, body, tlsRouterMatch)
	case openaiexecution.DispatchGrok:
		return s.Grok.ForwardResponses(ctx, c, account, body, originalModel, reqStream, startTime)
	case openaiexecution.DispatchAnthropic:
		return s.Text.NativeResponses(ctx, c, account, body, reqModel)
	case openaiexecution.DispatchPassthrough:
		return s.Text.Passthrough(ctx, c, account, originalBody, canonicalImageIntentBody, reqModel, prepared.ImageIntentInvalidated, prepared.ReasoningEffort, reqStream, startTime, tlsRouterMatch)
	}

	transformed, err := openaiexecution.TransformRequest(ctx, prepared, profile, openAIForwardTransformAdapter{openAIForwardPreludeAdapter{s: s, c: c, account: account, ctx: ctx}})
	if err != nil {
		return nil, err
	}
	body = transformed.Body
	requestView = transformed.View
	reqModel = transformed.Model
	promptCacheKey = transformed.PromptCacheKey
	clientPromptCacheKey := transformed.ClientPromptCacheKey
	requestedModel, billingModel, upstreamModel := transformed.RequestedModel, transformed.BillingModel, transformed.UpstreamModel
	imageIntent := transformed.ImageIntent
	fingerprintIDs := transformed.Fingerprint
	reqBody := transformed.Decoded
	// 后续 WS 或密文恢复按需读取同一份准备结果，不重新运行模型映射和策略。
	ensureReqBody := func() (map[string]any, error) {
		if requestView.HasPatches() {
			patched, err := requestView.ApplyPatches()
			if err != nil {
				return nil, err
			}
			body = patched
			requestView = requeststate.NewOpenAIRequestView(body)
			reqBody = nil
		}
		if reqBody != nil {
			return reqBody, nil
		}
		decoded, err := requestView.Decode()
		if err != nil {
			return nil, err
		}
		reqBody = decoded
		return reqBody, nil
	}
	// 剥离本会话已被上游判定失效的加密项（invalid_encrypted_content lineage），
	// 阻断同一失效密文随客户端历史在每一轮重复触发"被拒→剥离→重试/重连"。
	// lineage 会话键统一按进场形态的 body 派生：后续重试可能改写 body，
	// 延迟计算会与下一请求的进场键漂移。
	lineageGroupID := OpenAIResponseGroupID(c)
	lineageEntryBody := body
	lineageSessionHash := ""
	if stateStore := s.Lineage.Store; stateStore != nil && stateStore.HasAnySessionInvalidEncryptedContent() {
		lineageSessionHash = GenerateOpenAISessionHash(c, body)
		if invalidDigests := stateStore.GetSessionInvalidEncryptedContentDigests(lineageGroupID, lineageSessionHash); len(invalidDigests) > 0 {
			strippedBody, strippedCount := s.Lineage.Strip(
				body, invalidDigests, "invalid_encrypted_lineage_strip", account.Record.ID, 0,
			)
			if strippedCount > 0 {
				body = strippedBody
				requestView = requeststate.NewOpenAIRequestView(body)
				reqBody = nil
			}
		}
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfg media.OpenAIResponsesImageBillingConfig
		var imageCfgErr error
		if reqBody != nil {
			imageCfg, imageCfgErr = gatewayprovider.ImageIntent().ResolveOpenAIResponsesImageBillingConfigDetailed(reqBody, billingModel)
		} else {
			imageCfg, imageCfgErr = gatewayprovider.ImageIntent().ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, billingModel)
		}
		if imageCfgErr != nil {
			openAIForwardPreludeAdapter{s: s, c: c, account: account, ctx: ctx}.Reject(openaiexecution.Rejection{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: imageCfgErr.Error(), Param: "size", ObserveUpstream: true})
			return nil, imageCfgErr
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	token, _, err := s.Requests.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
	if err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)

	if wsDecision.Transport == egress.OpenAIUpstreamTransportResponsesWebsocketV2 {
		wsReqBody, err := ensureReqBody()
		if err != nil {
			return nil, err
		}
		return s.WebSocket(ctx, c, account, wsReqBody, OpenAIHTTPWSAttempt{
			ClientPromptCacheKey: clientPromptCacheKey, Token: token, Decision: wsDecision,
			CodexCLI: isCodexCLI, Stream: reqStream, OriginalModel: originalModel,
			UpstreamModel: upstreamModel, StartedAt: startTime, TLS: tlsRouterMatch,
			LineageGroupID: lineageGroupID, LineageSessionHash: lineageSessionHash,
			BillingModel: billingModel, ImageBillingModel: imageBillingModel,
			ImageSizeTier: imageSizeTier, ImageInputSize: imageInputSize, LineageEntryBody: lineageEntryBody,
		})
	}

	reasoningEffort := requeststate.ExtractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	// 国产模型默认 effort 补充：此处 reqModel 已被 mapping 重写为 billingModel。
	reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(reasoningEffort, body, reqModel)
	reasoningEffortValue := ""
	if reasoningEffort != nil {
		reasoningEffortValue = *reasoningEffort
	}
	firstOutputTimeout := time.Duration(0)
	if reqStream && account.Record.Platform == capability.PlatformOpenAI {
		firstOutputTimeout = s.Output.FirstOutputTimeout(reasoningEffortValue)
	}

	input := openaiexecution.HTTPInput{
		Body: body, LineageEntryBody: lineageEntryBody, AccountID: account.Record.ID, AccountName: account.Record.Name, Platform: account.Record.Platform,
		RequestedModel: requestedModel, OriginalModel: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel,
		ReasoningEffort: reasoningEffort, ReasoningEffortValue: reasoningEffortValue,
		ImageBillingModel: imageBillingModel, ImageSizeTier: imageSizeTier, ImageInputSize: imageInputSize,
		Stream: reqStream, OAuth: account.View().IsOAuth(), Shadow: account.View().IsShadow(), Grok: account.View().IsGrok(), StartedAt: startTime,
	}
	exchange := openai.HTTPExchangeOptions{
		StartedAt:          startTime,
		FirstOutputTimeout: firstOutputTimeout,
		RequestContext:     gatewayprovider.DetachUpstreamContext,
		Build: func(ctx context.Context, body []byte) (*http.Request, error) {
			return s.Requests.Build(ctx, c, account, body, token, reqStream, promptCacheKey, isCodexCLI, tlsRouterMatch)
		},
		ApplyHeaders: func(headers http.Header) { openai.ApplyCodexFingerprintHeaders(headers, fingerprintIDs) },
		Do: func(request *http.Request) (*http.Response, error) {
			proxyURL := ""
			if account.Record.ProxyID != nil && account.Record.Proxy != nil {
				proxyURL = account.Record.Proxy.URL()
			}
			return s.Requests.Transport.DoWithTLS(request, proxyURL, account.Record.ID, account.Record.Concurrency, s.Requests.TLSProfile(account, tlsRouterMatch))
		},
		Latency: func(elapsed time.Duration) {
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
		},
		HeaderTimeout: func() error {
			return s.Output.FirstOutputFailure(ctx, c, account, startTime, originalModel, reasoningEffortValue, firstOutputTimeout, "response_headers", nil)
		},
		TransportError: func(err error) error { return s.Requests.Failure.Handle(ctx, c, account, err, false) },
	}
	options := s.nativeForwardHTTPOptions(ctx, c, account, input, exchange,
		func(current []byte) ([]byte, bool, error) {
			return prepareOpenAIHTTPEncryptedRetry(current, func(current []byte) (map[string]any, error) {
				if !bytes.Equal(body, current) {
					body = current
					requestView = requeststate.NewOpenAIRequestView(body)
					reqBody = nil
				}
				return ensureReqBody()
			})
		},
		func(entry []byte) {
			digests := openai.CollectOpenAIEncryptedContentDigestsRaw(entry)
			if len(digests) == 0 {
				return
			}
			if lineageSessionHash == "" {
				lineageSessionHash = GenerateOpenAISessionHash(c, entry)
			}
			s.Lineage.Mark(lineageGroupID, lineageSessionHash, digests)
		},
	)
	options.ReleaseDecodedRequest = func() { reqBody = nil }
	result, err := openaiexecution.RunHTTP(ctx, input, options)
	return openaiexecution.ToForwardResult(result), err
}

func shouldForwardOpenAIResponsesViaRawChatCompletions(account *gatewayprovider.ExecutionAccount) bool {
	return gatewayprovider.ExecutionModelPolicy(account).RawChat()
}
