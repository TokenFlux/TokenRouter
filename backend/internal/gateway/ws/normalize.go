package ws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// Normalize 逐轮保持客户端模型、账号身份、图片与 Fast 策略的原执行顺序。
func (s *RequestNormalizer) Normalize(ctx context.Context, raw []byte, applyUserPromptReplacement bool, turn int) (ClientPayload, error) {
	p, o := s.Port, s.Options
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ClientPayload{}, p.CloseError(1008, "empty websocket request payload", nil)
	}
	if !gjson.ValidBytes(trimmed) {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", errors.New("invalid json"))
	}
	if applyUserPromptReplacement {
		// 后续 response.create 帧先执行用户提示词替换，再进入模型归一化、图片桥接和 OpenAI Fast Policy。
		trimmed = p.PromptReplace(ctx, trimmed)
	}

	values := gjson.GetManyBytes(trimmed, "type", "model", "prompt_cache_key", "previous_response_id")
	eventType := strings.TrimSpace(values[0].String())
	normalized := trimmed
	switch eventType {
	case "":
		eventType = "response.create"
		next, setErr := p.Mutate(normalized, "type", eventType)
		if setErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", setErr)
		}
		normalized = next
	case "response.create":
	case "response.append":
		return ClientPayload{}, p.CloseError(
			1008,
			"response.append is not supported in ws v2; use response.create with previous_response_id",
			nil,
		)
	default:
		return ClientPayload{}, p.CloseError(
			1008,
			fmt.Sprintf("unsupported websocket request type: %s", eventType),
			nil,
		)
	}
	policyRequestModel := strings.TrimSpace(values[1].String())
	if policyRequestModel == "" {
		policyRequestModel = s.State.OriginalModel
	}
	requestedReasoningEffort := p.RequestedEffort(normalized, strings.TrimSpace(values[1].String()))
	if next, policyErr := p.Reasoning(normalized, policyRequestModel); policyErr != nil {
		return ClientPayload{}, p.CloseError(1008, policyErr.Error(), policyErr)
	} else {
		normalized = next
	}
	responsesLite := p.IsLite(normalized)
	if compatibilityBody, compatibilityChanged, compatibilityErr := p.Compatibility(normalized, responsesLite); compatibilityErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", compatibilityErr)
	} else if compatibilityChanged {
		normalized = compatibilityBody
	}
	if o.OAuth && !o.ForceHTTPBridge {
		aliasedBody, aliasErr := p.AliasTools(normalized)
		if aliasErr != nil {
			return ClientPayload{}, p.CloseError(1008, aliasErr.Error(), aliasErr)
		}
		normalized = aliasedBody
	}

	originalModel := strings.TrimSpace(values[1].String())
	modelMissing := originalModel == ""
	if originalModel == "" {
		// 入站 WS 长会话里，部分客户端只在第一轮 response.create 上声明
		// model，后续 turn 复用同一 session-level model。为避免因省略
		// model 直接断开用户连接，这里回落到上一轮已通过校验的客户端模型，
		// 并在下方写回上游 payload，保证账号模型映射/fast policy/图片权限
		// 仍按同一模型执行。
		originalModel = s.State.OriginalModel
		if originalModel == "" {
			return ClientPayload{}, p.CloseError(
				1008,
				"model is required in response.create payload",
				nil,
			)
		}
	}
	// 渠道模型必须在账号映射之前逐轮解析；originalModel 继续保留客户端请求语义。
	routingModel, upstreamModel, resolveModelErr := p.Models(turn, originalModel, normalized)
	if resolveModelErr != nil {
		return ClientPayload{}, resolveModelErr
	}
	promptCacheKey := strings.TrimSpace(values[2].String())
	previousResponseID := strings.TrimSpace(values[3].String())
	previousResponseIDKind := p.ClassifyPrevious(previousResponseID)
	if previousResponseID != "" && previousResponseIDKind == "message_id" {
		return ClientPayload{}, p.CloseError(
			1008,
			"previous_response_id must be a response.id (resp_*), not a message id",
			nil,
		)
	}
	if turnMetadata := p.TurnMetadata(); turnMetadata != "" {
		next, setErr := p.Mutate(normalized, "client_metadata.x-codex-turn-metadata", turnMetadata)
		if setErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", setErr)
		}
		normalized = next
	}
	accountIdentitySourceRaw := append([]byte(nil), normalized...)
	accountScopedPayload, accountScoped, scopeErr := p.ScopeIdentity(normalized)
	if scopeErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket identity metadata", scopeErr)
	}
	if accountScoped {
		normalized = accountScopedPayload
	}
	if p.IsLite(normalized) {
		litePayload, liteErr := p.NormalizeLite(normalized)
		if liteErr != nil {
			return ClientPayload{}, p.CloseError(
				1008,
				liteErr.Error(),
				liteErr,
			)
		}
		normalized = litePayload
	}
	imagePolicy := p.ImagePolicy(ctx, normalized)
	imageGenerationAllowed := imagePolicy.Allowed
	codexImageGenerationExplicitToolPolicy := imagePolicy.Explicit
	codexBridgeEnabled := imagePolicy.Bridge
	if codexBridgeEnabled {
		next, err := p.BridgeImages(normalized)
		if err != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", err)
		}
		normalized = next
	}

	if modelMissing || upstreamModel != originalModel {
		next, setErr := p.Mutate(normalized, "model", upstreamModel)
		if setErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", setErr)
		}
		normalized = next
	}
	if codexImageGenerationExplicitToolPolicy == "strip" {
		if stripped, changed, stripErr := p.StripImages(normalized); stripErr != nil {
			return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", stripErr)
		} else if changed {
			normalized = stripped
			p.Log(fmt.Sprintf("ingress_ws_codex_image_tool_stripped_by_policy account_id=%d", o.AccountID))
		}
	}
	if stripped, changed, stripErr := p.StripSparkImages(normalized, upstreamModel); stripErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", stripErr)
	} else if changed {
		normalized = stripped
		p.Log(fmt.Sprintf("ingress_ws_codex_spark_image_tool_stripped account_id=%d", o.AccountID))
	}
	// 生图能力必须按渠道模型 C 判断；账号最终模型 U 只用于真正的上游请求。
	imageIntentBody, imageIntent, explicitImageIntent := p.ImageIntent(routingModel, upstreamModel, normalized)
	if explicitImageIntent && !imageGenerationAllowed {
		p.FeatureDenied()
		return ClientPayload{}, p.CloseError(1008, p.ImageDeniedMessage(), nil)
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfgErr error
		imageCfg, imageCfgErr := p.ImageBilling(imageIntentBody, routingModel)
		if imageCfgErr != nil {
			return ClientPayload{}, p.CloseError(1008, imageCfgErr.Error(), imageCfgErr)
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	// Apply OpenAI Fast Policy on the response.create frame using the same
	// evaluator/normalize/scope rules as the HTTP entrypoints. This is the
	// single integration point for all WS ingress turns (first + follow-up
	// frames flow through here).
	//
	// 模型兜底：首轮在 handler 层仍要求 model；后续 response.create 帧
	// 可以省略并复用 s.State.OriginalModel。进入策略评估前总会写入
	// 具体上游模型，保证白名单和 filter 行为稳定。
	policyCtx := ctx
	policyApplied, blocked, policyErr := p.FastPolicy(policyCtx, turn, upstreamModel, normalized, true)
	if policyErr != nil {
		return ClientPayload{}, p.CloseError(1008, "invalid websocket request payload", policyErr)
	}
	if blocked != nil {
		p.PolicyDenied()
		// Send a Realtime-style error event to the client first, then
		// signal the handler to close the connection with PolicyViolation.
		// We intentionally do NOT forward this frame upstream.
		//
		// coder/websocket@v1.8.14 Conn.Write is synchronous and flushes
		// the underlying bufio writer before returning (write.go:42 →
		// 307-311), and the subsequent close handshake re-acquires the
		// same writeFrameMu, so the error event is guaranteed to reach
		// the kernel send buffer before any close frame is queued.
		eventBytes := p.BlockedEvent(blocked)
		if eventBytes != nil {
			p.WriteBlocked(ctx, eventBytes)
		}
		return ClientPayload{}, p.CloseError(
			1008,
			blocked.Message,
			blocked,
		)
	}
	normalized = policyApplied
	s.State.OriginalModel = originalModel

	return ClientPayload{
		PayloadRaw:               normalized,
		AccountIdentitySourceRaw: accountIdentitySourceRaw,
		RawForHash:               trimmed,
		PromptCacheKey:           promptCacheKey,
		PreviousResponseID:       previousResponseID,
		OriginalModel:            originalModel,
		RoutingModel:             routingModel,
		ImageBillingModel:        imageBillingModel,
		ImageSizeTier:            imageSizeTier,
		ImageInputSize:           imageInputSize,
		PayloadBytes:             len(normalized),
		RequestedReasoningEffort: requestedReasoningEffort,
	}, nil
}
