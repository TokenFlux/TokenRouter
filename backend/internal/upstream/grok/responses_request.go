// 请求构造只接收技术快照；URL 策略与账号配置投影仍由调用方在原时点提供。
package grok

import (
	"bytes"
	"context"
	"net/http"
	"strings"
)

type ResponsesRequestOptions struct {
	URL, Token, CacheIdentity, OpenAIBeta string
	OAuth                                 bool
	Profile                               func(context.Context) context.Context
	ApplyOverrides                        func(http.Header)
}

func BuildResponsesRequest(ctx context.Context, body []byte, o ResponsesRequestOptions) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if o.Profile != nil {
		req = req.WithContext(o.Profile(req.Context()))
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if o.OAuth {
		ApplyCLIHeaders(req.Header)
	}
	ApplyGrokCacheHeaders(req.Header, o.CacheIdentity)
	if strings.TrimSpace(o.OpenAIBeta) != "" {
		req.Header.Set("OpenAI-Beta", o.OpenAIBeta)
	}
	if o.ApplyOverrides != nil {
		o.ApplyOverrides(req.Header)
	}
	return req, nil
}

// ApplyCLIHeaders 将订阅流量标识为受支持的 Grok CLI 版本；缺少这些标识时，
// CLI 网关会拒绝其它字段均有效的 OAuth 请求。
func ApplyCLIHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	version := ResolveCLIVersion()
	headers.Set("User-Agent", CLIUserAgent(version))
	headers.Set("X-Grok-Client-Version", version)
	headers.Set("x-grok-client-version", version)
	headers.Set("x-grok-client-identifier", CLIClientIdentifier)
	headers.Set("X-Grok-Client-Mode", "interactive")
}
