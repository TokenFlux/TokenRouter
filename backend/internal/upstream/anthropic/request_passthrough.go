// API Key 直通构造保留原白名单、认证清理和 beta 净化顺序。
package anthropic

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

func BuildRequestPassthrough(ctx context.Context, body []byte, token string, options RequestOptions) (*http.Request, []byte, error) {
	body = StripDeferredToolCacheControl(body)
	targetURL, err := options.URL()
	if err != nil {
		return nil, nil, err
	}
	clientHeaders := options.ClientHeaders
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	filterSet, err := options.OriginalPolicy(ctx, GetHeaderRaw(clientHeaders, "anthropic-beta"), model)
	if err != nil {
		return nil, nil, err
	}
	body, clientHeaders, err = options.FastMode(ctx, body, clientHeaders)
	if err != nil {
		return nil, nil, err
	}
	clientBeta := StripBetaTokensWithSet(GetHeaderRaw(clientHeaders, "anthropic-beta"), filterSet)
	// 账号覆写了 anthropic-beta 时，覆写值即最终上游值：净化以覆写值为准
	if beta, ok := options.BetaOverride(); ok {
		clientBeta = beta
	}
	if ContainsBetaToken(clientBeta, BetaFastMode) {
		if blockErr := options.CheckFastBeta(ctx); blockErr != nil {
			return nil, nil, blockErr
		}
	}
	if sanitized, changed := SanitizeAnthropicBodyForBetaTokens(body, clientBeta); changed {
		body = sanitized
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}

	for key, values := range clientHeaders {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if !AllowedHeaders[lowerKey] {
			continue
		}
		wireKey := ResolveWireCasing(key)
		for _, v := range values {
			AddHeaderRaw(req.Header, wireKey, v)
		}
	}
	// 透传白名单可能写入 Key 改写前的值，按系统策略裁决后的 beta 覆盖。
	DeleteHeaderAllForms(req.Header, "anthropic-beta")
	if clientBeta != "" {
		SetHeaderRaw(req.Header, "anthropic-beta", clientBeta)
	}

	// 覆盖入站鉴权残留，并注入上游认证
	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")
	req.Header.Del("cookie")
	SetAPIKeyAuthHeader(req.Header, options.APIKeyBearer, token)

	if GetHeaderRaw(req.Header, "content-type") == "" {
		SetHeaderRaw(req.Header, "content-type", "application/json")
	}
	if GetHeaderRaw(req.Header, "anthropic-version") == "" {
		SetHeaderRaw(req.Header, "anthropic-version", "2023-06-01")
	}

	// 账号级请求头覆写（最终生效，覆盖上面所有来源的同名头）
	options.ApplyOverrides(req.Header)

	return req, body, nil
}
