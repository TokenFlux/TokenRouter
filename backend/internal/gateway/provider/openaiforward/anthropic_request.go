// 原生 Anthropic 请求构造保留 beta 清洗、鉴权覆盖及最终账号 Header 的顺序。
package openaiforward

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
)

// NativeAnthropicRequestOptions 只投影本次请求的 Header 和安全策略。
type NativeAnthropicRequestOptions struct {
	Headers        http.Header
	GetHeader      func(http.Header, string) string
	OverrideValue  func(string) (string, bool)
	Sanitize       func([]byte, string) ([]byte, bool)
	AllowedHeader  func(string) bool
	WireCasing     func(string) string
	AddHeader      func(http.Header, string, string)
	SetHeader      func(http.Header, string, string)
	AuthHeader     func(http.Header, string)
	ApplyOverrides func(http.Header)
}

func NativeAnthropicTargetURL(accountID int64, baseURL string, validate func(string) (string, error)) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("account %d has no anthropic protocol base url", accountID)
	}
	validatedURL, err := validate(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return strings.TrimRight(validatedURL, "/") + "/v1/messages", nil
}
func BuildNativeAnthropicRequest(ctx context.Context, body []byte, apiKey, targetURL string, o NativeAnthropicRequestOptions) (*http.Request, []byte, error) {
	// 能力维度 body sanitize：与 Anthropic 平台 passthrough 相同，按 beta
	// header 决定是否保留 body 中的 beta 能力字段，避免客户端"body 带字段但
	// header 忘带 token"的 bug 让第三方上游 400。
	clientBeta := ""
	if o.Headers != nil {
		clientBeta = o.GetHeader(o.Headers, "anthropic-beta")
	}
	if beta, ok := o.OverrideValue("anthropic-beta"); ok {
		clientBeta = beta
	}
	if sanitized, changed := o.Sanitize(body, clientBeta); changed {
		body = sanitized
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}

	if o.Headers != nil {
		for key, values := range o.Headers {
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			if !o.AllowedHeader(lowerKey) {
				continue
			}
			wireKey := o.WireCasing(key)
			for _, v := range values {
				o.AddHeader(req.Header, wireKey, v)
			}
		}
	}

	// 覆盖入站鉴权残留，注入上游认证（默认 x-api-key；可经 extra
	// anthropic_apikey_auth_scheme 切换 Authorization: Bearer）。
	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")
	req.Header.Del("cookie")
	o.AuthHeader(req.Header, apiKey)

	if o.GetHeader(req.Header, "content-type") == "" {
		o.SetHeader(req.Header, "content-type", "application/json")
	}
	if o.GetHeader(req.Header, "anthropic-version") == "" {
		o.SetHeader(req.Header, "anthropic-version", "2023-06-01")
	}

	// 账号级请求头覆写（最终生效，覆盖上面所有来源的同名头）
	o.ApplyOverrides(req.Header)

	return req, body, nil
}
