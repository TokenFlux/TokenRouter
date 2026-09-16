// count_tokens 使用独立构造规则，保持与推理端点的字段及 Header 差异。
package anthropic

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

func BuildCountTokensRequestPassthrough(ctx context.Context, body []byte, token string, options RequestOptions) (*http.Request, error) {
	body = StripDeferredToolCacheControl(body)
	targetURL, err := options.URL()
	if err != nil {
		return nil, err
	}
	body = SanitizeCountTokensRequestBody(body)

	// 同 buildUpstreamRequestAnthropicAPIKeyPassthrough：能力维度 sanitize。
	clientBeta := ""
	if options.ClientHeaders != nil {
		clientBeta = GetHeaderRaw(options.ClientHeaders, "anthropic-beta")
	}
	// 账号覆写了 anthropic-beta 时，覆写值即最终上游值：净化以覆写值为准
	if beta, ok := options.BetaOverride(); ok {
		clientBeta = beta
	}
	if sanitized, changed := SanitizeAnthropicBodyForBetaTokens(body, clientBeta); changed {
		body = sanitized
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	if options.ClientHeaders != nil {
		for key, values := range options.ClientHeaders {
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			if !AllowedHeaders[lowerKey] {
				continue
			}
			wireKey := ResolveWireCasing(key)
			for _, v := range values {
				AddHeaderRaw(req.Header, wireKey, v)
			}
		}
	}

	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")
	req.Header.Del("cookie")
	SetAPIKeyAuthHeader(req.Header, options.APIKeyBearer, token)

	if req.Header.Get("content-type") == "" {
		req.Header.Set("content-type", "application/json")
	}
	if req.Header.Get("anthropic-version") == "" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	// 账号级请求头覆写（最终生效，覆盖上面所有来源的同名头）
	options.ApplyOverrides(req.Header)

	return req, nil
}

// buildCountTokensRequest 构建 count_tokens 上游请求
func BuildCountTokensRequest(ctx context.Context, body []byte, token, tokenType, modelID string, mimicClaudeCode bool, options RequestOptions) (*http.Request, []byte, error) {
	body = StripDeferredToolCacheControl(body)
	targetURL, err := options.URL()
	if err != nil {
		return nil, nil, err
	}
	clientHeaders := options.ClientHeaders
	// OAuth 账号：应用统一指纹和重写 userID（受设置开关控制）
	// 如果启用了会话ID伪装，会在重写后替换 session 部分为固定值
	ctEnableFP, ctEnableMPT := true, false
	if options.Forwarding != nil {
		ctEnableFP, ctEnableMPT = options.Forwarding(ctx)
	}
	var ctFingerprint *Fingerprint
	if options.OAuth && options.Fingerprint != nil {
		fp, err := options.Fingerprint.GetOrCreateFingerprint(ctx, options.AccountID, clientHeaders)
		if err == nil {
			ctFingerprint = fp
			if !ctEnableMPT {
				accountUUID := options.AccountUUID
				if accountUUID != "" && fp.ClientID != "" {
					if newBody, err := options.Fingerprint.RewriteUserIDWithMasking(ctx, body, options.AccountID, options.MaskSession, accountUUID, fp.ClientID, fp.UserAgent); err == nil && len(newBody) > 0 {
						body = newBody
					}
				}
			}
		}
	}

	// 禁用指纹统一不会禁用伪装强制头，计费版本仍要跟随实际出站 UA。
	var billingFingerprint *Fingerprint
	if ctEnableFP {
		billingFingerprint = ctFingerprint
	}
	if billingUA := EffectiveBillingUserAgent(tokenType, mimicClaudeCode, billingFingerprint); billingUA != "" {
		body = SyncBillingHeaderVersion(body, billingUA)
	}

	// === 计算最终 anthropic-beta header（先于 body sanitize）===
	// 顺序约束同 buildUpstreamRequest。
	ctEffectiveDropSet := MergeDropSets(options.FilterSet(ctx))
	finalBetaHeader, finalBetaShouldSet := ComputeFinalCountTokensAnthropicBeta(
		tokenType, mimicClaudeCode, modelID, clientHeaders, body, ctEffectiveDropSet, options.InjectAPIKeyBeta,
	)

	// 账号覆写了 anthropic-beta 时，覆写值即最终上游值：净化以覆写值为准
	if beta, ok := options.BetaOverride(); ok {
		finalBetaHeader, finalBetaShouldSet = beta, true
	}

	// 能力维度 body sanitize：与最终 anthropic-beta header 对称
	if sanitized, changed := SanitizeAnthropicBodyForBetaTokens(body, finalBetaHeader); changed {
		body = sanitized
	}
	body = SanitizeCountTokensRequestBody(body)

	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}

	// 设置认证头（保持原始大小写）
	if tokenType == "oauth" {
		SetHeaderRaw(req.Header, "authorization", "Bearer "+token)
	} else {
		SetAPIKeyAuthHeader(req.Header, options.APIKeyBearer, token)
	}

	// 白名单透传 headers（恢复真实 wire casing）
	for key, values := range clientHeaders {
		lowerKey := strings.ToLower(key)
		if AllowedHeaders[lowerKey] {
			wireKey := ResolveWireCasing(key)
			for _, v := range values {
				AddHeaderRaw(req.Header, wireKey, v)
			}
		}
	}

	// OAuth 账号：应用指纹到请求头（受设置开关控制）
	if ctEnableFP && ctFingerprint != nil {
		options.Fingerprint.ApplyFingerprint(req, ctFingerprint)
	}

	// 确保必要的 headers 存在（保持原始大小写）
	if GetHeaderRaw(req.Header, "content-type") == "" {
		SetHeaderRaw(req.Header, "content-type", "application/json")
	}
	if GetHeaderRaw(req.Header, "anthropic-version") == "" {
		SetHeaderRaw(req.Header, "anthropic-version", "2023-06-01")
	}
	if tokenType == "oauth" {
		ApplyClaudeOAuthHeaderDefaults(req)
	}

	// OAuth + mimic Claude Code：强制注入 CLI 指纹 header
	if tokenType == "oauth" && mimicClaudeCode {
		ApplyClaudeCodeMimicHeaders(req, false)
	}

	// 写入最终 anthropic-beta header（Del 一次避免白名单透传值残留）
	DeleteHeaderAllForms(req.Header, "anthropic-beta")
	if finalBetaShouldSet {
		SetHeaderRaw(req.Header, "anthropic-beta", finalBetaHeader)
	}

	// 同步 X-Claude-Code-Session-Id 头：取 body 中已处理的 metadata.user_id 的 session_id 覆盖
	if sessionHeader := GetHeaderRaw(req.Header, "X-Claude-Code-Session-Id"); sessionHeader != "" {
		if uid := gjson.GetBytes(body, "metadata.user_id").String(); uid != "" {
			if parsed := ParseMetadataUserID(uid); parsed != nil {
				SetHeaderRaw(req.Header, "X-Claude-Code-Session-Id", parsed.SessionID)
			}
		}
	}

	// 账号级请求头覆写（仅 anthropic/openai api_key 账号启用时生效；OAuth 路径 no-op）
	options.ApplyOverrides(req.Header)

	if options.Capture != nil {
		options.Capture(req, body)
	}

	return req, body, nil
}
func SanitizeCountTokensRequestBody(body []byte) []byte {
	out := body
	for _, path := range []string{
		"temperature",
		"top_p",
		"top_k",
		"stream",
		"stop_sequences",
		"stop",
		// Anthropic 的 /v1/messages/count_tokens 只接受请求输入字段。
		// max_tokens 是生成参数，OAuth mimic 可能为普通 messages 请求注入它，不能发送到该端点。
		"max_tokens",
	} {
		if next, ok := DeleteJSONPathBytes(out, path); ok {
			out = next
		}
	}
	return out
}
