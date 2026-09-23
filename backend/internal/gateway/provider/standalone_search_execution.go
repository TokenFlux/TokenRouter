// 独立搜索单次交换复用共享传输，保持原认证、地址、响应限制和失败分类。
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SearchTransport 使用原客户端池的闭合请求接口，不创建独立连接池。
type SearchTransport interface {
	Do(*http.Request, string, int64, int) (*http.Response, error)
}
type GrokSearchExecutor struct {
	Transport      SearchTransport
	DefaultBaseURL func() string
}

func (s *GrokSearchExecutor) responsesURL(value *account.Record) (string, error) {
	validator, err := accountprovider.GrokBaseURLValidator(value, xai.ValidateBaseURL)
	if err != nil {
		return "", err
	}
	base := accountprovider.GrokAccountBaseURL(value)
	if s.DefaultBaseURL != nil {
		base = accountprovider.GrokAccountBaseURLOr(value, s.DefaultBaseURL())
	}
	return xai.BuildResponsesURLWithValidator(base, validator)
}
func (s *GrokSearchExecutor) Execute(ctx context.Context, value *account.Record, body []byte) ([]byte, error) {
	if s == nil || s.Transport == nil {
		return nil, errors.New("http upstream not configured")
	}
	if value == nil || !value.IsGrok() {
		return nil, errors.New("grok account required")
	}
	token, err := account.GrokStoredAccessToken(value)
	if err != nil {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode: http.StatusUnauthorized,
			Reason:     forwardcore.GatewayFailureReason("grok_search_token"),
		}
	}
	targetURL, err := s.responsesURL(value)
	if err != nil {
		return nil, err
	}
	if json.Valid(body) && strings.TrimSpace(gjson.GetBytes(body, "model").String()) == "" {
		if patched, patchErr := sjson.SetBytes(body, "model", xai.DefaultTextModel); patchErr == nil {
			body = patched
		}
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build grok responses request: %w", err)
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "application/json")
	upstreamReq.Header.Set("User-Agent", xai.DefaultGrokUpstreamUserAgent())
	xai.ApplyCLIHeaders(upstreamReq.Header)
	accountprovider.ApplyAccountHeaderOverrides(value, upstreamReq.Header)
	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}
	resp, err := s.Transport.Do(upstreamReq, proxyURL, value.ID, value.Concurrency)
	if err != nil {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode: http.StatusBadGateway,
			Reason:     forwardcore.GatewayFailureReason("grok_search_transport"),
		}
	}
	defer func() { _ = resp.Body.Close() }()
	respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode: http.StatusBadGateway,
			Reason:     forwardcore.GatewayFailureReason("grok_search_read"),
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired ||
			resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= http.StatusInternalServerError {
			return nil, &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBytes}
		}
		message := string(respBytes)
		if len(message) > 200 {
			message = message[:200]
		}
		return nil, fmt.Errorf("grok upstream %d: %s", resp.StatusCode, message)
	}
	return respBytes, nil
}
