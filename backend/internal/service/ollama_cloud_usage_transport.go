// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	errors "errors"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	io "io"
	http "net/http"
)

// FetchOllamaCloudUsage 只执行既定 URL/会话/响应解析，不写健康或持久化状态；S09 迁供应商 Adapter。
func (s *OllamaCloudUsageService) FetchOllamaCloudUsage(ctx context.Context, input acctcore.OllamaUsageFetchInput) (*acctcore.OllamaUsageObservation, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	now, cookie, proxyURL := input.ObservedAt, input.Cookie, input.ProxyURL
	requestCtx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), ollamaCloudUsageRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, ollamaCloudUsageSettingsURL, nil)
	if err != nil || !isExactOllamaCloudSettingsURL(req.URL) {
		return nil, ErrOllamaCloudUsageUnavailable
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "sub2api-ollama-usage/1")
	resp, err := s.httpUpstream.Do(req, proxyURL, input.AccountID, input.Concurrency)
	if err != nil {
		return &acctcore.OllamaUsageObservation{HTTPStatus: 0, Failure: "request_failed", RetryAfter: 0, Unauthorized: false}, nil
	}
	if resp == nil || resp.Body == nil {
		return &acctcore.OllamaUsageObservation{HTTPStatus: 0, Failure: "empty_response", RetryAfter: 0, Unauthorized: false}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request != nil && !isExactOllamaCloudSettingsURL(resp.Request.URL) {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "response_host_mismatch", RetryAfter: 0, Unauthorized: false}, nil
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "redirect_blocked", RetryAfter: ollamaCloudUsageRetryAfter(resp.Header, now), Unauthorized: false}, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "unauthorized", RetryAfter: ollamaCloudUsageRetryAfter(resp.Header, now), Unauthorized: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "http_error", RetryAfter: ollamaCloudUsageRetryAfter(resp.Header, now), Unauthorized: false}, nil
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, ollamaCloudUsageMaxBodyBytes+1))
	if readErr != nil {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "response_read_failed", RetryAfter: 0, Unauthorized: false}, nil
	}
	if len(body) > ollamaCloudUsageMaxBodyBytes {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "response_too_large", RetryAfter: 0, Unauthorized: false}, nil
	}
	data, parseErr := parseOllamaCloudUsageHTML(body)
	if errors.Is(parseErr, errOllamaCloudUsageUnauthorizedHTML) {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "unauthorized", RetryAfter: 0, Unauthorized: true}, nil
	}
	if parseErr != nil {
		return &acctcore.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "invalid_html", RetryAfter: 0, Unauthorized: false}, nil
	}

	return &acctcore.OllamaUsageObservation{Data: data, HTTPStatus: resp.StatusCode}, nil
}
