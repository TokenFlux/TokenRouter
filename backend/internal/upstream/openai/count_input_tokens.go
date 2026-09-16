// input_tokens 是无结算的查询原语，不把计数响应当作推理用量。
package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

type InputTokensOptions struct {
	Enter          func() (func(), error)
	Do             func(*http.Request) (*http.Response, error)
	TransportError func(error) error
	HTTPError      func(*http.Response, []byte) error
	WriteError     func(int, string, string)
}

// CountInputTokens 保留原读取/错误输出，响应体仍由本次查询释放。
func CountInputTokens(request *http.Request, options InputTokensOptions, sink upstream.OutputSink) error {
	if options.Enter != nil {
		done, err := options.Enter()
		if err != nil {
			return err
		}
		defer done()
	}
	resp, err := options.Do(request)
	if err != nil {
		return options.TransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		options.WriteError(http.StatusBadGateway, "upstream_error", "Failed to read response")
		return fmt.Errorf("read input_tokens response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return options.HTTPError(resp, body)
	}
	tokens := gjson.GetBytes(body, "input_tokens")
	if !tokens.Exists() {
		options.WriteError(http.StatusBadGateway, "upstream_error", "Upstream response missing input_tokens")
		return fmt.Errorf("input_tokens response missing input_tokens field")
	}
	upstream.NewDeferredOutputContext(sink).JSON(http.StatusOK, map[string]any{"input_tokens": int(tokens.Int())})
	return nil
}

// BuildInputTokensRequest 保持独立查询的精简 Header 白名单和认证顺序。
func BuildInputTokensRequest(ctx context.Context, body []byte, options ResponsesRequestOptions, customUserAgent func() string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, options.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	authHeaders, err := options.Authenticate(ctx)
	if err != nil {
		return nil, err
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	{
		for key, values := range options.ForwardHeaders() {
			lower := strings.ToLower(strings.TrimSpace(key))
			if lower != "user-agent" && lower != "accept-language" {
				continue
			}
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
	}
	if customUA := customUserAgent(); customUA != "" {
		req.Header.Set("user-agent", customUA)
	}

	// 账号级请求头覆写（仅 openai api_key 账号启用时生效；OAuth 路径 no-op）
	options.OverrideHeaders(req.Header)

	return req, nil
}

// NativeInputTokensOptions 保留原生响应与兼容计数不同的读取和输出约定。
type NativeInputTokensOptions struct {
	Enter          func() (func(), error)
	Do             func(*http.Request) (*http.Response, error)
	TransportError func(error) error
	ReadBody       func(*http.Response) ([]byte, error)
	HTTPError      func(*http.Response, []byte) error
	WriteError     func(int, string, string)
}

// CountNativeInputTokens 保持数值类型验证，并原样输出供应商完整 JSON。
func CountNativeInputTokens(request *http.Request, options NativeInputTokensOptions, sink upstream.OutputSink) error {
	if options.Enter != nil {
		done, err := options.Enter()
		if err != nil {
			return err
		}
		defer done()
	}
	resp, err := options.Do(request)
	if err != nil {
		return options.TransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := options.ReadBody(resp)
	if err != nil {
		options.WriteError(http.StatusBadGateway, "upstream_error", "Failed to read response")
		return err
	}
	if resp.StatusCode >= 400 {
		return options.HTTPError(resp, body)
	}
	tokens := gjson.GetBytes(body, "input_tokens")
	if !tokens.Exists() || tokens.Type != gjson.Number {
		options.WriteError(http.StatusBadGateway, "upstream_error", "Upstream response missing input_tokens")
		return fmt.Errorf("responses input_tokens: upstream response missing input_tokens")
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	upstream.NewDeferredOutputContext(sink).Data(http.StatusOK, contentType, body)
	return nil
}
