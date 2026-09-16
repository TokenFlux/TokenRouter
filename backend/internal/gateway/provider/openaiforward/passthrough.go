// 透传保留共享拒绝字段预算、compact 恢复及独立输出语义，不与普通 HTTP 合并策略。
package openaiforward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"io"
	"net/http"
	"strings"
	"time"
)

type PassthroughInput struct {
	Body, CanonicalImageIntentBody []byte
	Model                          string
	ImageIntentInvalidated         bool
	ReasoningEffort                *string
	Stream                         bool
	StartedAt                      time.Time
}
type ImageBilling struct{ Model, SizeTier, InputSize string }

func RunPassthrough(ctx context.Context, in PassthroughInput, p PassthroughPorts) (*Result, error) {
	profile := p.Profile()
	body, canonicalImageIntentBody, reqModel := in.Body, in.CanonicalImageIntentBody, in.Model
	attemptImageIntentInvalidated, reasoningEffort, reqStream, startTime := in.ImageIntentInvalidated, in.ReasoningEffort, in.Stream, in.StartedAt
	requestedModel := reqModel
	upstreamPassthroughModel := ""
	if p.CompactPath() {
		compactMappedModel := p.CompactModel(reqModel)
		if compactMappedModel != "" && compactMappedModel != reqModel {
			nextBody, setErr := sjson.SetBytes(body, "model", compactMappedModel)
			if setErr != nil {
				return nil, fmt.Errorf("set compact passthrough model: %w", setErr)
			}
			body = nextBody
			upstreamPassthroughModel = compactMappedModel
			attemptImageIntentInvalidated = true
		}
	}

	if profile.UsesCodex {
		if rejectReason := p.InstructionsRejection(reqModel, body); rejectReason != "" {
			rejectMsg := "OpenAI codex passthrough requires a non-empty instructions field"
			p.PolicyDenied()
			p.LogInstructionsRejected(ctx, reqModel, rejectReason, body)
			p.Reject(403, "forbidden_error", rejectMsg, "")
			return nil, fmt.Errorf("openai passthrough rejected before upstream: %s", rejectReason)
		}
		// Codex passthrough 允许省略 instructions，但仍拒绝显式的非法值。
		if p.CodexModel(reqModel) && !gjson.GetBytes(body, "instructions").Exists() {
			nextBody, setErr := sjson.SetBytes(body, "instructions", native.DefaultCodexSynthInstructions(reqModel))
			if setErr != nil {
				return nil, fmt.Errorf("set passthrough codex instructions: %w", setErr)
			}
			body = nextBody
		}

		normalizedBody, normalized, err := p.OAuthBody(body, p.CompactPath())
		if err != nil {
			return nil, err
		}
		if normalized {
			body = normalizedBody
		}
		reqStream = gjson.GetBytes(body, "stream").Bool()

		accountScopedBody, accountScoped, scopeErr := p.AccountIdentityRaw(body)
		if scopeErr != nil {
			return nil, scopeErr
		}
		if accountScoped {
			body = accountScopedBody
		}

		p.StageFingerprint(nil)
		// 透传与普通转换路径共享指纹收敛语义。只局部改写 client_metadata，
		// 避免为大请求体做整包反序列化。
		if !p.CompactPath() {
			fingerprintIDs := p.Fingerprint()
			if fingerprintIDs != nil {
				updatedBody, changed, fingerprintErr := p.FingerprintBody(body, fingerprintIDs)
				if fingerprintErr != nil {
					return nil, fingerprintErr
				}
				if changed {
					body = updatedBody
				}
			}
			// nil 也必须覆盖，避免 failover 复用前一个账号的收敛 ID。
			p.StageFingerprint(fingerprintIDs)
		}
	}
	if profile.OpenAI {
		responsesLite := false
		if p.HasContext() {
			responsesLite = p.LiteHeader()
		}
		responsesLite = responsesLite || p.LitePayloadFlag(body)
		normalizedBody, normalized, normalizeErr := p.CompatibilityBody(body, responsesLite)
		if normalizeErr != nil {
			return nil, fmt.Errorf("normalize passthrough Responses compatibility: %w", normalizeErr)
		}
		if normalized {
			body = normalizedBody
		}
		if profile.OAuthLike {
			aliasedBody, reverse, aliased, aliasErr := p.ReservedToolNames(body)
			if aliasErr != nil {
				return nil, aliasErr
			}
			p.MergeToolNames(reverse)
			if aliased {
				body = aliasedBody
			}
		}
	}

	if profile.OpenAI && profile.APIKey &&
		!p.CompactPath() && p.NeedsClientTools(body) {
		adaptedBody, adaptErr := p.AdaptClientTools(body)
		if adaptErr != nil {
			return nil, adaptErr
		}
		body = adaptedBody
	}

	sanitizedBody, sanitized, err := native.SanitizeEmptyBase64InputImagesInOpenAIBody(body)
	if err != nil {
		return nil, err
	}
	if sanitized {
		body = sanitizedBody
	}
	// 透传分支后续的 OAuth/APIKey 兼容归一化可能删除无工具请求的
	// parallel_tool_calls；Responses Lite 契约仍要求显式发送 false。
	if p.HasContext() && p.LiteHeader() {
		liteBody, liteChanged, liteErr := p.NormalizeLite(body)
		if liteErr != nil {
			return nil, liteErr
		}
		if liteChanged {
			body = liteBody
		}
	}

	policyModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if policyModel == "" {
		policyModel = reqModel
	}
	updatedBody, policyErr := p.ApplyFastPass(ctx, policyModel, body)
	if policyErr != nil {

		return nil, policyErr
	}
	body = updatedBody

	// 宽泛意图保留给图片状态和计费，显式意图单独负责权限门禁。
	imageIntent := p.ImageIntent(reqModel, canonicalImageIntentBody, policyModel, body, attemptImageIntentInvalidated)
	explicitImageIntent := p.ExplicitImageIntent(policyModel, body)
	if explicitImageIntent && !p.ImageAllowed() {
		p.FeatureDenied()
		p.Reject(403, "permission_error", p.ImagePermissionMessage(), "")
		return nil, errors.New("image generation disabled for group")
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfgErr error
		imageCfg, imageCfgErr := p.ImageBilling(body, reqModel)
		if imageCfgErr != nil {
			p.UpstreamError(400, imageCfgErr.Error())
			p.Reject(400, "invalid_request_error", imageCfgErr.Error(), "size")
			return nil, imageCfgErr
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	p.Log(
		"[OpenAI 自动透传] 命中自动透传分支: account=%d name=%s type=%s model=%s stream=%v",
		profile.ID,
		profile.Name,
		profile.Type,
		reqModel,
		reqStream,
	)
	if reqStream {
		p.WarnTimeoutHeaders(ctx)
	}

	token, err := p.AccessToken(ctx)
	if err != nil {
		return nil, err
	}

	p.PrepareTransportPass()
	p.MarkPassthrough()

	agentTaskRecoveryTried := false
	compactModelFallbackRetried := false
	rejectedFieldRetryState := p.RetryState(body)
	var resp *http.Response
	var usage *wire.ForwardUsage
	var firstTokenMs *int
	responseID := ""
	imageCount := 0
	var imageOutputSizes []string
	for {
		actualModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
		if actualModel == "" {
			actualModel = reqModel
		}
		p.UpstreamModelObserved(actualModel)
		upstreamCtx, releaseUpstreamCtx := p.UpstreamContext(ctx)
		upstreamReq, buildErr := p.BuildPass(upstreamCtx, body, token)
		releaseUpstreamCtx()
		if buildErr != nil {
			return nil, buildErr
		}

		upstreamStart := time.Now()
		resp, err = p.SendPass(upstreamReq)
		p.Latency(time.Since(upstreamStart))
		if err != nil {
			// 未收到 HTTP 响应时交给外层切换账号，持久故障仍由统一处理器临时摘除。
			return nil, p.TransportErrorPass(ctx, err)
		}
		if resp.StatusCode >= 400 {
			// 只为识别失效任务而预读；恢复响应体，保证恢复失败后的错误处理仍读取原响应。
			probeBody := p.ReadErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(probeBody))
			if retryBody, reason, changed, retryErr := native.NormalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, probeBody); retryErr != nil {
				return nil, fmt.Errorf("normalize passthrough rejected Responses field retry body: %w", retryErr)
			} else if changed && rejectedFieldRetryState.Allow(retryBody) {
				body = retryBody
				p.Log("[OpenAI] Retrying passthrough request after %s (account: %s)", reason, profile.Name)
				continue
			}
			if !agentTaskRecoveryTried && p.IsAgentIdentity(ctx) && p.InvalidAgentTask(resp.StatusCode, probeBody) {
				agentTaskRecoveryTried = true
				if recoveryErr := p.RecoverAgentTask(ctx); recoveryErr != nil {
					return nil, fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
				}
				continue
			}
			upstreamMsg := p.ErrorMessage(probeBody)
			if retryBody, fallbackModel, retry := p.CompactRetry(requestedModel, body, resp.StatusCode, upstreamMsg, probeBody, compactModelFallbackRetried); retry {
				p.CompactObserved(resp, probeBody, upstreamMsg)
				fromModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
				body = retryBody
				upstreamPassthroughModel = fallbackModel
				compactModelFallbackRetried = true
				p.UpstreamModelObserved(fallbackModel)
				p.Log(
					"[OpenAI passthrough] Retrying explicit compact request once with fallback model (account: %s, from: %s, to: %s, upstream_code: %s)",
					profile.Name, fromModel, fallbackModel, p.ErrorCode(probeBody),
				)
				continue
			}

			// 透传模式默认保持原样代理；容量错误以及 API-key 上游的瞬时
			// 5xx 应先触发多账号 failover，且此时尚未写入下游响应。
			// probeBody 已在上方任务探测时读取过一次，直接复用避免重复读取。
			if p.ShouldFailover(resp.StatusCode, probeBody) {
				return nil, p.FailoverError(ctx, resp, body, probeBody)
			}
			return nil, p.ErrorResponsePass(ctx, resp, body, probeBody)
		}

		p.WrapResponseBody(resp)
		p.ObserveProvenance(resp.Header)

		if reqStream {
			result, handleErr := native.ReadPassthroughStreaming(ctx, resp, upstream.NewOutputContext(p.Sink()), p.ResponseOptions(ctx), startTime, reqModel, upstreamPassthroughModel)
			if handleErr != nil {
				if retryBody, fallbackModel, retry := p.CompactFromSignal(requestedModel, body, handleErr, compactModelFallbackRetried, resp); retry {
					body = retryBody
					upstreamPassthroughModel = fallbackModel
					compactModelFallbackRetried = true
					continue
				}
				if signal, ok := p.CompactSignal(handleErr); ok {
					_ = resp.Body.Close()
					compactResp, compactBody := p.CompactErrorResponse(resp, signal)
					if p.ShouldFailover(compactResp.StatusCode, compactBody) {
						return nil, p.FailoverError(ctx, compactResp, body, compactBody)
					}
					return nil, p.ErrorResponsePass(ctx, compactResp, body, compactBody)
				}
				_ = resp.Body.Close()
				return nil, handleErr
			}
			usage = result.Usage
			firstTokenMs = result.FirstTokenMs
			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageOutputSizes = result.ImageOutputSizes
		} else {
			result, handleErr := native.ReadPassthroughNonStreaming(ctx, resp, p.Sink(), p.ResponseOptions(ctx), reqModel, upstreamPassthroughModel)
			if handleErr != nil {
				if retryBody, fallbackModel, retry := p.CompactFromSignal(requestedModel, body, handleErr, compactModelFallbackRetried, resp); retry {
					body = retryBody
					upstreamPassthroughModel = fallbackModel
					compactModelFallbackRetried = true
					continue
				}
				if signal, ok := p.CompactSignal(handleErr); ok {
					_ = resp.Body.Close()
					compactResp, compactBody := p.CompactErrorResponse(resp, signal)
					if p.ShouldFailover(compactResp.StatusCode, compactBody) {
						return nil, p.FailoverError(ctx, compactResp, body, compactBody)
					}
					return nil, p.ErrorResponsePass(ctx, compactResp, body, compactBody)
				}
				_ = resp.Body.Close()
				return nil, handleErr
			}
			usage = result.Usage
			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageOutputSizes = result.ImageOutputSizes
		}
		break
	}
	defer func() { _ = resp.Body.Close() }()
	serviceTier := p.ServiceTier(body)
	p.BindOwner(ctx, responseID)

	if !profile.Shadow {
		p.UpdateCodexUsage(ctx, resp.Header)
	}

	if usage == nil {
		usage = &wire.ForwardUsage{}
	}

	forwardResult := &Result{
		RequestID:                   resp.Header.Get("x-request-id"),
		Headers:                     resp.Header,
		ResponseID:                  responseID,
		Usage:                       *usage,
		Model:                       reqModel,
		UpstreamModel:               upstreamPassthroughModel,
		UpstreamResponseServiceTier: p.ObservedServiceTier(),
		ServiceTier:                 p.ResolvedServiceTier(serviceTier),
		ReasoningEffort:             reasoningEffort,
		Stream:                      reqStream,
		OpenAIWSMode:                false,
		Duration:                    time.Since(startTime),
		FirstTokenMs:                firstTokenMs,
	}
	if imageCount > 0 {
		forwardResult.ImageCount = imageCount
		forwardResult.ImageSize = imageSizeTier
		forwardResult.ImageInputSize = imageInputSize
		forwardResult.ImageOutputSizes = imageOutputSizes
		forwardResult.BillingModel = imageBillingModel
	}
	return forwardResult, nil
}
