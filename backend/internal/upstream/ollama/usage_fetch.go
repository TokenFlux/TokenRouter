// 原生抓取只返回脱敏观测，账号的失败计数和 CAS 由外层负责。
package ollama

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

type FetchInput struct {
	ObservedAt time.Time
	Cookie     string `json:"-"`
}

func (i FetchInput) String() string   { return "ollama usage input" }
func (i FetchInput) GoString() string { return i.String() }

type FetchOptions struct {
	Do          func(*http.Request) (*http.Response, error)
	Context     func(context.Context) context.Context
	Unavailable error
}

// FetchUsage 只执行既定 URL、会话和响应解析，不写健康或持久化状态。
func FetchUsage(ctx context.Context, input FetchInput, options FetchOptions) (*usageview.OllamaUsageObservation, error) {
	if options.Do == nil {
		return nil, options.Unavailable
	}
	now, cookie := input.ObservedAt, input.Cookie
	requestCtx, cancel := context.WithTimeout(options.Context(ctx), RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, SettingsURL, nil)
	if err != nil || !IsExactSettingsURL(req.URL) {
		return nil, options.Unavailable
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "sub2api-ollama-usage/1")
	resp, err := options.Do(req)
	if err != nil {
		return &usageview.OllamaUsageObservation{HTTPStatus: 0, Failure: "request_failed", RetryAfter: 0, Unauthorized: false}, nil
	}
	if resp == nil || resp.Body == nil {
		return &usageview.OllamaUsageObservation{HTTPStatus: 0, Failure: "empty_response", RetryAfter: 0, Unauthorized: false}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request != nil && !IsExactSettingsURL(resp.Request.URL) {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "response_host_mismatch", RetryAfter: 0, Unauthorized: false}, nil
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "redirect_blocked", RetryAfter: RetryAfter(resp.Header, now), Unauthorized: false}, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "unauthorized", RetryAfter: RetryAfter(resp.Header, now), Unauthorized: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "http_error", RetryAfter: RetryAfter(resp.Header, now), Unauthorized: false}, nil
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if readErr != nil {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "response_read_failed", RetryAfter: 0, Unauthorized: false}, nil
	}
	if len(body) > MaxBodyBytes {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "response_too_large", RetryAfter: 0, Unauthorized: false}, nil
	}
	data, parseErr := ParseOllamaCloudUsageHTML(body)
	if errors.Is(parseErr, ErrUnauthorizedHTML) {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "unauthorized", RetryAfter: 0, Unauthorized: true}, nil
	}
	if parseErr != nil {
		return &usageview.OllamaUsageObservation{HTTPStatus: resp.StatusCode, Failure: "invalid_html", RetryAfter: 0, Unauthorized: false}, nil
	}

	return &usageview.OllamaUsageObservation{Data: data, HTTPStatus: resp.StatusCode}, nil
}
