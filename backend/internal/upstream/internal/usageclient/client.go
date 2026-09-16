// 固定查询的 HTTP 技术读取器保留认证 Header 顺序、重定向策略与读取边界。
package usageclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

const MaxBodyBytes = 512 * 1024

type Client struct{ *usagecontract.Request }

func New(input *usagecontract.Request) *Client { return &Client{Request: input} }
func (c *Client) Get(ctx context.Context, path string, authenticated bool) ([]byte, int, error) {
	endpoint, err := UpstreamUsageEndpoint(c.BaseURL, path, c.Endpoint)
	if err != nil {
		return nil, 0, usageview.ErrUpstreamUsageConfigInvalid.WithCause(err)
	}
	token := ""
	if authenticated {
		token = c.APIKey
	}
	return c.GetURLWithBearer(ctx, endpoint, token, "")
}
func UpstreamUsageEndpoint(base, path string, build func(string, string) string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base URL")
	}
	// 账号 Base URL 沿用 OpenAI 兼容端点约定：/v1、/v4、/v1beta
	// 等版本段作为根路径，不能再重复拼接一个 /v1。复用现有端点
	// 构造器，确保用量查询和转发对同一类 Base URL 的解释一致。
	endpoint := build(base, path)
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return "", errors.New("invalid endpoint URL")
	}
	// 调用方已经校验过 Base URL；这里再次清理非路径部分，避免未来新增
	// 调用路径时把历史查询串、片段或用户信息带到上游请求。
	parsedEndpoint.User = nil
	parsedEndpoint.RawQuery = ""
	parsedEndpoint.ForceQuery = false
	parsedEndpoint.Fragment = ""
	return strings.TrimRight(parsedEndpoint.String(), "/"), nil
}
func UpstreamUsageStatusEndpoint(base string) (string, error) {
	return UpstreamUsageRootEndpoint(base, "/api/status")
}

// UpstreamUsageTokenEndpoint 构造 New API Token 专用端点并保留尾斜杠，
// 避免部分实例把无尾斜杠请求重定向后被客户端的禁止重定向策略拦截。
func UpstreamUsageTokenEndpoint(base string) (string, error) {
	endpoint, err := UpstreamUsageRootEndpoint(base, "/api/usage/token")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(endpoint, "/") + "/", nil
}

// UpstreamUsageWalletEndpoint 构造兼容 New API 分支的钱包余额端点。
// 该端点固定为 /user/balance，不允许管理员从配置中注入路径。
func UpstreamUsageWalletEndpoint(base string) (string, error) {
	return UpstreamUsageRootEndpoint(base, "/user/balance")
}

// UpstreamUsageUserSelfEndpoint 构造官方 New API 用户自查询端点。
// 该端点需要用户级 Access Token，而不是 relay API Key。
func UpstreamUsageUserSelfEndpoint(base string) (string, error) {
	return UpstreamUsageRootEndpoint(base, "/api/user/self")
}

// UpstreamUsageRootEndpoint 从账号 Base URL 去掉末尾的 OpenAI 版本段，
// 用于 New API 这类挂在站点根路径下的管理接口。
func UpstreamUsageRootEndpoint(base, path string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base URL")
	}
	rootPath := strings.TrimRight(parsed.Path, "/")
	if httpclient.OpenAIBaseURLHasVersionSuffix(rootPath) {
		if index := strings.LastIndex(rootPath, "/"); index >= 0 {
			rootPath = rootPath[:index]
		} else {
			rootPath = ""
		}
	}
	parsed.Path = strings.TrimRight(rootPath, "/") + "/" + strings.TrimLeft(path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return strings.TrimRight(parsed.String(), "/"), nil
}
func UpstreamUsageHTTPError(status int, unsupported bool) error {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return nil
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return usageview.ErrUpstreamUsageAuthFailed
	case http.StatusTooManyRequests:
		return usageview.ErrUpstreamUsageRateLimited
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		if unsupported {
			return usageview.ErrUpstreamUsageUnsupported
		}
	}
	return usageview.ErrUpstreamUsageInvalidResponse
}
func UpstreamUsageOperationError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return usageview.ErrUpstreamUsageTimeout
	}
	return usageview.ErrUpstreamUsageRequestFailed
}
func (c *Client) GetURL(ctx context.Context, endpoint string, authenticated bool) ([]byte, int, error) {
	token := ""
	if authenticated {
		token = c.APIKey
	}
	return c.GetURLWithBearer(ctx, endpoint, token, "")
}

// GetURLWithHeaders 请求内置适配器声明的固定请求头。
// 账号级覆写先应用、再被固定头覆盖，避免探测请求改变认证或团队身份。
func (c *Client) GetURLWithHeaders(ctx context.Context, endpoint string, fixedHeaders map[string]string) ([]byte, int, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, 0, usageview.ErrUpstreamUsageConfigInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, usageview.ErrUpstreamUsageRequestFailed
	}
	req = req.WithContext(c.Context(req.Context()))
	req.Header.Set("Accept", "application/json")
	c.ApplyHeaders(req.Header)
	// 账号级覆写不得改变内置适配器的认证身份。
	req.Header.Del("Authorization")
	req.Header.Del("api-key")
	for headerName, headerValue := range fixedHeaders {
		name := strings.TrimSpace(headerName)
		value := strings.TrimSpace(headerValue)
		if name == "" || value == "" {
			continue
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return nil, 0, usageview.ErrUpstreamUsageConfigInvalid
		}
		// 固定头不允许被账号覆写；先删除所有大小写变体，再写入规范值。
		for existing := range req.Header {
			if strings.EqualFold(existing, name) {
				delete(req.Header, existing)
			}
		}
		req.Header.Set(name, value)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, UpstreamUsageOperationError(ctx, err)
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, usageview.ErrUpstreamUsageInvalidResponse
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, 0, UpstreamUsageOperationError(ctx, readErr)
	}
	if int64(len(body)) > MaxBodyBytes {
		return nil, 0, usageview.ErrUpstreamUsageInvalidResponse
	}
	return body, resp.StatusCode, nil
}

// GetURLWithBearer 使用固定的 Bearer 令牌和可选用户 ID 请求管理端点。
// 用户 ID 只会写入适配器定义的 New-Api-User 头，不接受任意 Header 配置。
func (c *Client) GetURLWithBearer(ctx context.Context, endpoint, bearerToken, userID string) ([]byte, int, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, 0, usageview.ErrUpstreamUsageConfigInvalid
	}
	// 复用统一请求实现，但不再拼接路径。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, usageview.ErrUpstreamUsageRequestFailed
	}
	req = req.WithContext(c.Context(req.Context()))
	req.Header.Set("Accept", "application/json")
	c.ApplyHeaders(req.Header)
	// Header Override 不能改变管理查询实际使用的认证身份。
	req.Header.Del("Authorization")
	if strings.TrimSpace(bearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearerToken))
	}
	req.Header.Del("New-Api-User")
	if strings.TrimSpace(userID) != "" {
		req.Header.Set("New-Api-User", strings.TrimSpace(userID))
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, UpstreamUsageOperationError(ctx, err)
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, usageview.ErrUpstreamUsageInvalidResponse
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, 0, UpstreamUsageOperationError(ctx, readErr)
	}
	if int64(len(body)) > MaxBodyBytes {
		return nil, 0, usageview.ErrUpstreamUsageInvalidResponse
	}
	return body, resp.StatusCode, nil
}
