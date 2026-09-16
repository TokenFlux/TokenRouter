package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

const defaultClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// 默认 User-Agent，与用户抓包的请求一致
const defaultUsageUserAgent = "claude-code/2.1.7"

type UsageClient struct {
	UsageURL          string
	AllowPrivateHosts bool
	DoTLS             func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)
}

// NewClaudeUsageFetcher 创建 Claude 用量获取服务
// httpUpstream: 可选，如果提供则支持 TLS 指纹伪装
func NewUsageClient(doTLS func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)) *UsageClient {
	return &UsageClient{UsageURL: defaultClaudeUsageURL, DoTLS: doTLS}
}

// FetchUsage 简单版本，不支持 TLS 指纹（向后兼容）
func (s *UsageClient) FetchUsage(ctx context.Context, accessToken, proxyURL string) (*wire.ClaudeUsageResponse, error) {
	return s.FetchUsageWithOptions(ctx, &UsageFetchOptions{
		AccessToken: accessToken,
		ProxyURL:    proxyURL,
	})
}

// FetchUsageWithOptions 完整版本，支持 TLS 指纹和自定义 User-Agent
func (s *UsageClient) FetchUsageWithOptions(ctx context.Context, opts *UsageFetchOptions) (*wire.ClaudeUsageResponse, error) {
	if opts == nil {
		return nil, fmt.Errorf("options is nil")
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "GET", s.UsageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	// 设置请求头（与抓包一致，但不设置 Accept-Encoding，让 Go 自动处理压缩）
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	// 设置 User-Agent（优先使用缓存的 Fingerprint，否则使用默认值）
	userAgent := defaultUsageUserAgent
	if opts.Fingerprint != nil && opts.Fingerprint.UserAgent != "" {
		userAgent = opts.Fingerprint.UserAgent
	}
	req.Header.Set("User-Agent", userAgent)

	var resp *http.Response

	// 如果有 TLS Profile 且有 HTTPUpstream，使用 DoWithTLS
	if opts.TLSProfile != nil && s.DoTLS != nil {
		resp, err = s.DoTLS(req, opts.ProxyURL, opts.AccountID, 0, opts.TLSProfile)
		if err != nil {
			return nil, fmt.Errorf("request with TLS fingerprint failed: %w", err)
		}
	} else {
		// 不启用 TLS 指纹，使用普通 HTTP 客户端
		client, err := httpclient.GetClient(httpclient.Options{
			ProxyURL:           opts.ProxyURL,
			Timeout:            30 * time.Second,
			ValidateResolvedIP: true,
			AllowPrivateHosts:  s.AllowPrivateHosts,
		})
		if err != nil {
			return nil, fmt.Errorf("create http client failed: %w", err)
		}

		resp, err = client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := fmt.Sprintf("API returned status %d: %s", resp.StatusCode, string(body))
		return nil, infraerrors.InternalServer("UPSTREAM_ERROR", msg)
	}

	var usageResp wire.ClaudeUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usageResp); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	return &usageResp, nil
}

// UsageFetchOptions 包含获取 Claude 用量数据所需的所有选项
type UsageFetchOptions struct {
	AccessToken string                  // OAuth access token
	ProxyURL    string                  // 代理 URL（可选）
	AccountID   int64                   // 账号 ID（用于连接池隔离）
	TLSProfile  *tlsfingerprint.Profile // TLS 指纹 Profile（nil 表示不启用）
	Fingerprint *Fingerprint            // 缓存的指纹信息（User-Agent 等）
}
