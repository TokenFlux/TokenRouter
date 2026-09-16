// 请求体重建、模型层级和 OAuth 规范化的时序由目标 Adapter 唯一拥有。
package openaiforward

import (
	"context"
	"errors"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
	"strings"
)

type TransformResult struct {
	Body                                                                                     []byte
	View                                                                                     requeststate.OpenAIRequestView
	Decoded                                                                                  map[string]any
	Model, RequestedModel, BillingModel, UpstreamModel, PromptCacheKey, ClientPromptCacheKey string
	ImageIntent                                                                              bool
	Fingerprint                                                                              *native.FingerprintIDs
}
type FastDecision struct {
	DeleteField bool
	Tier        string
	Blocked     error
}

func TransformRequest(ctx context.Context, prepared *Prelude, profile Profile, p TransformPorts) (*TransformResult, error) {
	body := prepared.Body
	requestView := prepared.View
	reqModel, promptCacheKey := requestView.Model, requestView.PromptCacheKey
	canonicalImageIntentBody := prepared.CanonicalImageIntentBody
	codexImageGenerationExplicitToolPolicy := prepared.ImageToolPolicy
	isCodexCLI, compactPath, compatMessagesBridge, nativeCNResponses := prepared.CodexCLI, prepared.Compact, prepared.MessagesBridge, profile.NativeCN
	wsDecision := prepared.Transport
	bodyModified := false
	clientPromptCacheKey := promptCacheKey
	var reqBody map[string]any
	ensureReqBody := func() (map[string]any, error) {
		if requestView.HasPatches() {
			patchedBody, patchErr := requestView.ApplyPatches()
			if patchErr != nil {
				return nil, patchErr
			}
			body = patchedBody
			requestView = requeststate.NewOpenAIRequestView(body)
			reqBody = nil
			bodyModified = false
		}
		if reqBody != nil {
			return reqBody, nil
		}
		decoded, decodeErr := p.Decode(requestView.Bytes())
		if decodeErr != nil {
			return nil, decodeErr
		}
		reqBody = decoded
		return reqBody, nil
	}
	markPatchSet := func(path string, value any) {
		bodyModified = true
		if requestView.PatchesDisabled() {
			if reqBody != nil {
				native.SetOpenAIRequestMapPath(reqBody, path, value)
			}
			return
		}
		requestView.MarkPatchSet(path, value)
	}
	markPatchDelete := func(path string) {
		bodyModified = true
		if requestView.PatchesDisabled() {
			if reqBody != nil {
				native.DeleteOpenAIRequestMapPath(reqBody, path)
			}
			return
		}
		requestView.MarkPatchDelete(path)
	}
	disablePatch := func() {
		requestView.DisablePatches()
	}
	markDecodedModified := func() {
		bodyModified = true
		disablePatch()
	}

	codexImageGenerationExplicitToolPolicy = p.GroupImagePolicy(codexImageGenerationExplicitToolPolicy)
	imageGenerationAllowed := p.ImageAllowed()
	codexImageGenerationBridgeEnabled := isCodexCLI &&
		!p.LiteHeader() &&
		imageGenerationAllowed &&
		codexImageGenerationExplicitToolPolicy != "strip" &&
		p.BridgeEnabled(ctx)
	var imageIntent bool
	canonicalImageIntent := p.ImageIntentHint(reqModel, canonicalImageIntentBody)
	// 显式意图只负责权限门禁；宽泛意图仍负责 namespace 工具处理和图片计费。
	explicitImageIntent := p.IsExplicitImageGenerationIntent("/v1/responses", reqModel, canonicalImageIntentBody)
	if codexImageGenerationExplicitToolPolicy == "strip" {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if native.StripOpenAIImageGenerationTools(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Stripped /responses image_generation tool for Codex client by account policy")
		}
		imageIntent = p.IsImageGenerationIntentMap("/v1/responses", reqModel, decoded)
		explicitImageIntent = p.IsExplicitImageGenerationIntentMap("/v1/responses", reqModel, decoded)
	} else {
		imageIntent = canonicalImageIntent
	}
	if explicitImageIntent && !imageGenerationAllowed {
		p.Reject(Rejection{Status: 403, Type: "permission_error", Message: p.ImagePermissionMessage(), FeatureDenied: true})
		return nil, errors.New("image generation disabled for group")
	}

	instructions := gjson.GetBytes(body, "instructions")
	instructionsEmpty := !instructions.Exists() || instructions.Type != gjson.String || strings.TrimSpace(instructions.String()) == ""
	if instructionsEmpty && profile.UsesCodex && !compatMessagesBridge && !nativeCNResponses {
		markPatchSet("instructions", native.DefaultCodexSynthInstructions(reqModel))
	}

	isCompactRequest := compactPath
	requestedModel := reqModel
	billingModel, upstreamModel := p.Models(requestedModel, isCompactRequest)
	if isCompactRequest {
		if compactModel := p.CompactModel(requestedModel); compactModel != "" {
			upstreamModel = compactModel
		}
	}
	if billingModel != requestedModel {
		p.Log("[OpenAI] Model mapping applied: %s -> %s (account: %s, isCodexCLI: %v)", requestedModel, billingModel, profile.Name, isCodexCLI)
	}
	reqModel = billingModel
	if upstreamModel != requestedModel {
		markPatchSet("model", upstreamModel)
	}
	if upstreamModel != billingModel {
		if isCompactRequest {
			p.Log("[OpenAI] Compact model mapping applied: %s -> %s (account: %s, isCodexCLI: %v)", requestedModel, upstreamModel, profile.Name, isCodexCLI)
		} else {
			p.Log("[OpenAI] Upstream model resolved: %s -> %s (account: %s, type: %s, isCodexCLI: %v)", billingModel, upstreamModel, profile.Name, profile.Type, isCodexCLI)
		}
	}
	if strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String()) == "json_schema" ||
		strings.TrimSpace(gjson.GetBytes(body, "response_format.type").String()) == "json_schema" {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if native.NormalizeOpenAIResponseFormatSchemas(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Normalized Responses JSON schema compatibility")
		}
	}

	imageIntent = imageIntent || p.IsImageGenerationIntent("/v1/responses", reqModel, nil) || p.IsOpenAIImageGenerationModel(upstreamModel)
	explicitImageIntent = explicitImageIntent || p.IsExplicitImageGenerationIntent("/v1/responses", reqModel, nil) || p.IsOpenAIImageGenerationModel(upstreamModel)
	if explicitImageIntent && !imageGenerationAllowed {
		p.Reject(Rejection{Status: 403, Type: "permission_error", Message: p.ImagePermissionMessage(), FeatureDenied: true})
		return nil, errors.New("image generation disabled for group")
	}

	if imageGenerationAllowed && !isCompactRequest && (codexImageGenerationBridgeEnabled || p.IsOpenAIImageGenerationModel(requestView.Model) || p.OpenAIRequestBodyImageGenerationToolNeedsNormalization(body) || p.IsOpenAIImageGenerationModel(upstreamModel)) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if codexImageGenerationBridgeEnabled && p.EnsureOpenAIResponsesImageGenerationTool(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Injected /responses image_generation tool for Codex client")
		}
		if codexImageGenerationBridgeEnabled && p.EnsureOpenAIResponsesImageGenerationToolChoiceAuto(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Set /responses image_generation tool_choice=auto for Codex client")
		}
		if native.NormalizeOpenAIResponsesImageGenerationTools(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Normalized /responses image_generation tool payload")
		}
		if p.NormalizeOpenAIResponsesImageOnlyModel(decoded) {
			markDecodedModified()
			if model, ok := decoded["model"].(string); ok {
				upstreamModel = strings.TrimSpace(model)
			}
			p.Log("[OpenAI] Normalized /responses image-only model request inbound_model=%s image_model=%s upstream_model=%s", requestView.Model, billingModel, upstreamModel)
		}
		if err := p.ValidateOpenAIResponsesImageModel(decoded, upstreamModel); err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "model", ObserveUpstream: true})
			return nil, err
		}
		if native.HasOpenAIImageGenerationTool(decoded) {
			imageIntent = true
			p.Log("[OpenAI] /responses image_generation request inbound_model=%s mapped_model=%s account_type=%s", requestView.Model, upstreamModel, profile.Type)
		}
		if codexImageGenerationBridgeEnabled && p.ApplyCodexImageGenerationBridgeInstructions(decoded) {
			markDecodedModified()
			p.Log("[OpenAI] Added Codex image_generation bridge instructions")
		}
	} else if imageGenerationAllowed && imageIntent && p.OpenAIRequestBodyHasImageGenerationDeclaration(body) {
		// 完整 image_generation tool 只做 raw 计费读取，校验/桥接/旧字段迁移命中时才展开大 input map。
		p.Log("[OpenAI] /responses image_generation request inbound_model=%s mapped_model=%s account_type=%s", requestView.Model, upstreamModel, profile.Type)
	}

	if p.IsCodexSparkModel(upstreamModel) && native.OpenAIRequestBodyMayContainImageInput(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err := p.ValidateCodexSparkInput(decoded, upstreamModel); err != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: err.Error(), Param: "input", ObserveUpstream: true})
			return nil, err
		}
	}

	// gpt-5.3-codex-spark 会以 HTTP 400（param=tools）拒绝生图工具。
	// 在此统一剥离，确保 API Key 与 OAuth 路径不受生图功能开关影响。
	if p.IsCodexSparkModel(upstreamModel) && p.OpenAIRequestBodyHasImageGenerationDeclaration(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if native.StripCodexSparkImageGenerationTools(decoded) {
			markDecodedModified()
		}
	}

	var fingerprintIDs *native.FingerprintIDs
	if profile.OAuth {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		// Responses OAuth 与 Chat 兼容入口保持一致：纯文本 system 可以无损提升后删除，
		// JSON object 模式仍需在 input 中保留 JSON 指令供上游兼容校验。
		omitPromotedSystemMessages := !strings.EqualFold(
			strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String()),
			"json_object",
		)
		codexResult := native.CodexTransformResult{}
		if compatMessagesBridge {
			codexResult = p.CodexTransform(decoded, native.CodexOAuthTransformOptions{
				IsCodexCLI:                          isCodexCLI,
				IsCompact:                           isCompactRequest,
				SkipDefaultInstructions:             true,
				PreserveToolCallIDs:                 true,
				OmitPromotedSystemMessagesFromInput: omitPromotedSystemMessages,
			})
			p.EnsureCodexOAuthInstructionsField(decoded)
			markDecodedModified()
		} else {
			codexResult = p.CodexTransform(decoded, native.CodexOAuthTransformOptions{
				IsCodexCLI:                          isCodexCLI,
				IsCompact:                           isCompactRequest,
				OmitPromotedSystemMessagesFromInput: omitPromotedSystemMessages,
			})
		}
		if codexResult.Error != nil {
			p.Reject(Rejection{Status: 400, Type: "invalid_request_error", Message: codexResult.Error.Error()})
			return nil, codexResult.Error
		}
		p.ToolNameReverse(codexResult.ToolNameReverse)
		if codexResult.Modified {
			markDecodedModified()
		}
		// 指纹收敛 ID 只在本次 Forward 内共享，避免跨账号 failover 复用 Gin context 中的旧值。
		// 带真实 device_id 时补齐 client_metadata 安装标识，与真实 Codex 对齐（compact 形态不同，跳过）。
		if !isCompactRequest && p.ClientMetadata(decoded) {
			markDecodedModified()
		}
		if currentClientPromptCacheKey, ok := decoded["prompt_cache_key"].(string); ok {
			clientPromptCacheKey = currentClientPromptCacheKey
		}
		// 账号命名空间与指纹收敛独立：保留客户端身份数量，但不能在换号后跨 OAuth 凭据复用。
		if !isCompactRequest && p.AccountIdentity(decoded) {
			markDecodedModified()
		}
		p.ClearFingerprint()
		if !isCompactRequest {
			var changed bool
			var resolveErr error
			fingerprintIDs, changed, resolveErr = p.Fingerprint(ctx, decoded)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if changed {
				markDecodedModified()
			}
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if strings.TrimSpace(clientPromptCacheKey) != "" {
			// 报文已包含账号隔离值；此处保留原始值，保证 Header 构造只派生命名空间一次。
			promptCacheKey = clientPromptCacheKey
		} else if currentPromptCacheKey, ok := decoded["prompt_cache_key"].(string); ok && currentPromptCacheKey != "" {
			// 客户端未提供键时，保留指纹收敛注入的既有默认值。
			promptCacheKey = currentPromptCacheKey
		} else if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		}
	}

	if !native.SupportsVerbosity(upstreamModel) && gjson.GetBytes(body, "text.verbosity").Exists() {
		markPatchDelete("text.verbosity")
	}

	if !isCodexCLI {
		maxOutputTokens := gjson.GetBytes(body, "max_output_tokens")
		if maxOutputTokens.Exists() {
			switch profile.Platform {
			case "openai", "deepseek":
				// 先保留 Responses 原生输出上限；仅当选中上游明确拒绝时，才在下方有界 HTTP 重试中移除。
			case "anthropic":
				decoded, decodeErr := ensureReqBody()
				if decodeErr != nil {
					return nil, decodeErr
				}
				delete(decoded, "max_output_tokens")
				if _, hasMaxTokens := decoded["max_tokens"]; !hasMaxTokens {
					decoded["max_tokens"] = maxOutputTokens.Value()
				}
				markDecodedModified()
			case "gemini":
				markPatchDelete("max_output_tokens")
			default:
				markPatchDelete("max_output_tokens")
			}
		}
		// /v1/responses 的规范输出上限字段是 max_output_tokens；部分客户端仍按
		// Chat Completions 习惯发送 max_tokens，兼容 Responses 上游会拒绝该字段（#4417）。
		// 仅对 OpenAI 平台归一化：Anthropic 合法使用 max_tokens，其 max_output_tokens
		// 反向转换已在上方 switch 中处理。
		if profile.Platform == "openai" {
			if maxTokens := gjson.GetBytes(body, "max_tokens"); maxTokens.Exists() {
				if !gjson.GetBytes(body, "max_output_tokens").Exists() {
					markPatchSet("max_output_tokens", maxTokens.Value())
				}
				markPatchDelete("max_tokens")
			}
		}
		if gjson.GetBytes(body, "max_completion_tokens").Exists() && (profile.Type == "apikey" || profile.Platform != "openai") {
			markPatchDelete("max_completion_tokens")
		}
		for _, unsupportedField := range []string{"prompt_cache_retention", "safety_identifier", "prompt_cache_options"} {
			if gjson.GetBytes(body, unsupportedField).Exists() {
				markPatchDelete(unsupportedField)
			}
		}
	}
	if wsDecision.Transport != "responses_websockets_v2" && (!profile.OpenAI || !profile.APIKey) && gjson.GetBytes(body, "previous_response_id").Exists() {
		markPatchDelete("previous_response_id")
	}
	if native.OpenAIRequestBodyMayContainEmptyBase64InputImage(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if native.SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(decoded) {
			markDecodedModified()
		}
	}

	decision := p.FastDecision(ctx, upstreamModel, requestView.ServiceTier, requestView.HasServiceTier)
	if decision.Blocked != nil {
		p.FastBlocked(decision.Blocked)
		return nil, decision.Blocked
	}
	if decision.DeleteField {
		markPatchDelete("service_tier")
	} else if decision.Tier != "" && decision.Tier != requestView.ServiceTier {
		markPatchSet("service_tier", decision.Tier)
	}

	if profile.OAuth {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if input, ok := decoded["input"].([]any); ok && p.SanitizeOpenAIResponsesOrphanToolOutputs(
			decoded,
			input,
			strings.TrimSpace(p.FirstNonEmptyString(decoded["previous_response_id"])) != "",
		) {
			markDecodedModified()
		}
	}
	if reqBody != nil || p.OpenAIResponsesInputMayNeedTruncation(body) {
		decoded, decodeErr := ensureReqBody()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if p.TruncateOpenAIResponsesInputText(decoded) {
			markDecodedModified()
		}
	}

	if bodyModified {
		if requestView.HasPatches() {
			if patchedBody, patchErr := requestView.ApplyPatches(); patchErr == nil {
				body = patchedBody
				requestView = requeststate.NewOpenAIRequestView(body)
				reqBody = nil
				bodyModified = false
			}
		}
		if bodyModified {
			decoded, decodeErr := ensureReqBody()
			if decodeErr != nil {
				return nil, decodeErr
			}
			var marshalErr error
			body, marshalErr = p.Marshal(decoded)
			if marshalErr != nil {
				return nil, fmt.Errorf("serialize request body: %w", marshalErr)
			}
			requestView = requeststate.NewOpenAIRequestView(body)
		}
	}
	// 在所有请求体重建完成后排序压缩触发器，确保它始终位于历史输入末尾。
	if normalizedBody, changed, normalizeErr := p.NormalizeTrigger(body); normalizeErr != nil {
		return nil, fmt.Errorf("normalize compaction trigger order: %w", normalizeErr)
	} else if changed {
		body = normalizedBody
		requestView = requeststate.NewOpenAIRequestView(body)
		reqBody = nil
	}

	return &TransformResult{Body: body, View: requestView, Decoded: reqBody, Model: reqModel, RequestedModel: requestedModel, BillingModel: billingModel, UpstreamModel: upstreamModel, PromptCacheKey: promptCacheKey, ClientPromptCacheKey: clientPromptCacheKey, ImageIntent: imageIntent, Fingerprint: fingerprintIDs}, nil
}
