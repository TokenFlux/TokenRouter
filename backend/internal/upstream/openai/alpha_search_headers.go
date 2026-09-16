// Alpha Search 专用请求头不复用 Responses 的会话协议，保留原最小头集合。
package openai

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

type AlphaSearchRequestOptions struct {
	ResponsesRequestOptions
	Query               func() url.Values
	OAuth               func() bool
	InboundHeader       func(string) string
	IdentityWithKey     func(http.Header, int64)
	ResponsesLiteHeader string
}

// BuildAlphaSearchRequest 保留官方独立搜索/PAT 回退的原协议头顺序。
func BuildAlphaSearchRequest(ctx context.Context, body []byte, options AlphaSearchRequestOptions) (*http.Request, error) {
	parsedURL, err := url.Parse(options.URL)
	if err != nil {
		return nil, fmt.Errorf("parse alpha search URL: %w", err)
	}
	if incomingQuery := options.Query(); incomingQuery != nil {
		query := parsedURL.Query()
		for key, values := range incomingQuery {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		parsedURL.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
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

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if options.OAuth() {
		req.Host = "chatgpt.com"
		if err := options.AccountHeaders(ctx, req.Header); err != nil {
			return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
		}

		if turnMetadata := options.InboundHeader("X-Codex-Turn-Metadata"); turnMetadata != "" {
			req.Header.Set("X-Codex-Turn-Metadata", turnMetadata)
		}
		options.ApplyAccountIdentity(req.Header)
		if version := options.InboundHeader("Version"); version != "" {
			req.Header.Set("Version", version)
		} else {
			req.Header.Set("Version", CodexCanonicalClientVersion())
		}
		req.Header.Set("Originator", options.Originator())
		options.ApplyUserAgent(req)
		EnforceCodexIdentityHeadersWithUA(req.Header, "")
	}

	options.OverrideHeaders(req.Header)
	StripAlphaSearchResponsesHeaders(req.Header, options.ResponsesLiteHeader)
	return req, nil
}

// BuildAlphaSearchResponsesRequest 保留官方独立搜索/PAT 回退的原协议头顺序。
func BuildAlphaSearchResponsesRequest(ctx context.Context, alphaBody []byte, body []byte, options AlphaSearchRequestOptions) (*http.Request, error) {
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
	req.Host = "chatgpt.com"
	if err := options.AccountHeaders(ctx, req.Header); err != nil {
		return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if turnMetadata := options.InboundHeader("X-Codex-Turn-Metadata"); turnMetadata != "" {
		req.Header.Set("X-Codex-Turn-Metadata", turnMetadata)
	}
	if version := options.InboundHeader("Version"); version != "" {
		req.Header.Set("Version", version)
	} else {
		req.Header.Set("Version", CodexCanonicalClientVersion())
	}
	req.Header.Set("Originator", options.Originator())
	apiKeyID := options.APIKeyID()
	if sessionID := strings.TrimSpace(gjson.GetBytes(alphaBody, "id").String()); sessionID != "" {
		isolated := options.IsolateSession(apiKeyID, sessionID)
		req.Header.Set("Session_ID", isolated)
		req.Header.Set("Conversation_ID", isolated)
	}
	options.ApplyUserAgent(req)
	options.IdentityWithKey(req.Header, apiKeyID)
	EnforceCodexIdentityHeadersWithUA(req.Header, "")
	options.OverrideHeaders(req.Header)
	return req, nil
}

// stripOpenAIAlphaSearchResponsesHeaders 让独立搜索请求与官方 Codex
// SearchClient 的线协议保持一致。alpha/search 不是 /responses 的子请求：官方
// 客户端仅在 Provider/Auth 基础头之外附加 x-codex-turn-metadata，不发送
// OpenAI-Beta、会话隔离或 Responses Lite 状态头。originator 与 User-Agent
// 属于官方默认客户端头，必须保留。
//
// alpha/search 使用专用构造器生成官方 SearchClient 的最小线协议形态；
// 该函数作为最后一道防线，避免账号 header 覆写或后续改动重新带入
// Responses 专用头，使 PAT 的 alpha/search 被上游按错误认证路径处理。
func StripAlphaSearchResponsesHeaders(headers http.Header, liteHeader string) {
	if headers == nil {
		return
	}
	for _, key := range []string{
		"OpenAI-Beta",
		"Session_ID",
		"Conversation_ID",
		"X-Codex-Beta-Features",
		"X-Codex-Turn-State",
		liteHeader,
	} {
		headers.Del(key)
	}
}
