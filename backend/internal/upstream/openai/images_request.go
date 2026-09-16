// Images 请求保留原认证、客户端头、UA、内容类型与账号覆写顺序。
package openai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

func BuildImagesRequest(ctx context.Context, body []byte, contentType string, options ResponsesRequestOptions) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, options.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	authHeaders, err := options.Authenticate(ctx)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for key, values := range options.ForwardHeaders() {
		if !options.AllowHeader(strings.ToLower(key)) {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	options.ApplyUserAgent(req)
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// 账号级请求头覆写（仅 openai api_key 账号启用时生效；OAuth 路径 no-op）
	options.OverrideHeaders(req.Header)
	return req, nil
}
