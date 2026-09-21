package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

const CodexInviteDefaultUserAgent = "Codex Desktop/0.0.0 (Linux; x86_64)"
const codexBackendAPIBaseURL = "https://chatgpt.com/backend-api"
const codexInviteResetUnavailable = "CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE"
const codexInviteResetUnavailableMessage = "当前 Codex 推荐邀请入口暂不可用，但已有重置次数仍可使用"

// CodexInviteClient 只拥有一次邀请请求的传输参数与响应体。
type CodexInviteClient struct {
	Token          string
	UserAgent      string
	AccountHeaders func(http.Header)
	Do             func(*http.Request) (*http.Response, error)
}

func (s *CodexInviteClient) GetJSON(ctx context.Context, path string, query map[string]string) (map[string]any, error) {
	target, err := BuildCodexBackendURL(path, query)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	s.applyHeaders(req)
	return s.doJSON(req)
}

func (s *CodexInviteClient) PostJSON(ctx context.Context, path string, body map[string]any) (map[string]any, error) {
	target, err := BuildCodexBackendURL(path, nil)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.applyHeaders(req)
	return s.doJSON(req)
}

func (s *CodexInviteClient) applyHeaders(req *http.Request) {
	*req = *req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OpenAI-Beta", openaiQuotaCodexBeta)
	req.Header.Set("OAI-Language", "zh-CN")
	req.Header.Set("originator", "Codex Desktop")
	req.Header.Set("X-OpenAI-Attach-Auth", "1")
	req.Header.Set("X-OpenAI-Attach-Integrity-State", "1")
	req.Header.Set("User-Agent", s.UserAgent)
	req.Header.Set("sec-fetch-site", "none")
	req.Header.Set("sec-fetch-mode", "no-cors")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("priority", "u=4, i")
	s.AccountHeaders(req.Header)
}

func (s *CodexInviteClient) doJSON(req *http.Request) (map[string]any, error) {
	if s.Do == nil {
		return nil, apperror.InternalServer("HTTP_UPSTREAM_NOT_CONFIGURED", "http upstream is not configured")
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	resp, err := s.Do(req)
	if err != nil {
		return nil, err
	}
	// 响应体会被完整读取，关闭失败不影响本次调用结果。
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, ImageErrorBodyReadLimit))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		if err := codexInviteResetUpstreamBusinessError(resp.StatusCode, message); err != nil {
			return nil, err
		}
		return nil, apperror.Newf(apperror.Category(resp.StatusCode), "CODEX_INVITE_RESET_UPSTREAM_ERROR", "codex invite reset upstream returned %d: %s", resp.StatusCode, message)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode codex invite reset response: %w", err)
	}
	return result, nil
}

func codexInviteResetUpstreamBusinessError(statusCode int, body string) error {
	detail := codexInviteResetUpstreamDetail(body)
	if statusCode != http.StatusForbidden || !strings.Contains(detail, "推荐邀请不可用") {
		return nil
	}
	// 上游在活动关闭或账号不具备推荐资格时会返回 403，仍可能保留可用重置次数。
	return apperror.Forbidden(codexInviteResetUnavailable, codexInviteResetUnavailableMessage).WithMetadata(map[string]string{
		"upstream_status": fmt.Sprint(statusCode),
		"upstream_detail": detail,
	})
}

func codexInviteResetUpstreamDetail(body string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		if detail := codexInviteDetail(payload["detail"]); detail != "" {
			return detail
		}
	}
	return strings.TrimSpace(body)
}

func BuildCodexBackendURL(path string, query map[string]string) (string, error) {
	base, err := url.Parse(codexBackendAPIBaseURL)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	if len(query) > 0 {
		values := base.Query()
		for key, value := range query {
			values.Set(key, value)
		}
		base.RawQuery = values.Encode()
	}
	return base.String(), nil
}

// codexInviteDetail 保留字符串及其它 JSON 值的原展示转换。
func codexInviteDetail(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
