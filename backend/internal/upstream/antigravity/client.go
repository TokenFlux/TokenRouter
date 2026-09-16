// Package antigravity provides a client for the Antigravity API.
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"

	proxyurl "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// ForbiddenError 表示上游返回 403 Forbidden
type ForbiddenError struct {
	StatusCode int
	Body       string
}

func (e *ForbiddenError) Error() string {
	return fmt.Sprintf("fetchAvailableModels 失败 (HTTP %d): %s", e.StatusCode, e.Body)
}

// NewAPIRequestWithURL 使用指定的 base URL 创建 Antigravity API 请求（v1internal 端点）
func NewAPIRequestWithURL(ctx context.Context, baseURL, action, accessToken string, body []byte) (*http.Request, error) {
	// 构建 URL，流式请求添加 ?alt=sse 参数
	apiURL := fmt.Sprintf("%s/v1internal:%s", baseURL, action)
	isStream := action == "streamGenerateContent"
	if isStream {
		apiURL += "?alt=sse"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// 基础 Headers（与 Antigravity-Manager 保持一致，只设置这 3 个）
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

	return req, nil
}

// NewAPIRequest 使用默认 URL 创建 Antigravity API 请求（v1internal 端点）
// 向后兼容：仅使用默认 BaseURL
func NewAPIRequest(ctx context.Context, action, accessToken string, body []byte) (*http.Request, error) {
	return NewAPIRequestWithURL(ctx, BaseURL, action, accessToken, body)
}

// Client Antigravity API 客户端
type Client struct {
	httpClient *http.Client
}

const (
	// proxyDialTimeout 代理 TCP 连接超时（含代理握手），代理不通时快速失败
	proxyDialTimeout = 5 * time.Second
	// proxyTLSHandshakeTimeout 代理 TLS 握手超时
	proxyTLSHandshakeTimeout = 5 * time.Second
	// clientTimeout 整体请求超时（含连接、发送、等待响应、读取 body）
	clientTimeout = 10 * time.Second
)

func NewClient(proxyURL string) (*Client, error) {
	client := &http.Client{
		Timeout: clientTimeout,
	}

	_, parsed, err := proxyurl.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	if parsed != nil {
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: proxyDialTimeout,
			}).DialContext,
			TLSHandshakeTimeout: proxyTLSHandshakeTimeout,
		}
		if err := proxyurl.ConfigureTransportProxy(transport, parsed); err != nil {
			return nil, fmt.Errorf("configure proxy: %w", err)
		}
		client.Transport = transport
	}
	return &Client{
		httpClient: client,
	}, nil
}

// IsConnectionError 判断是否为连接错误（网络超时、DNS 失败、连接拒绝）
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// 检查超时错误
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// 检查连接错误（DNS 失败、连接拒绝）
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// 检查 URL 错误
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

// shouldFallbackToNextURL 判断是否应切换到下一个 URL
// 与 Antigravity-Manager 保持一致：连接错误、429、408、404、5xx 触发 URL 降级
func shouldFallbackToNextURL(err error, statusCode int) bool {
	if IsConnectionError(err) {
		return true
	}
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusNotFound ||
		statusCode >= 500
}

// ExchangeCode 用 authorization code 交换 token
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier string) (*TokenResponse, error) {
	clientSecret, err := getClientSecret()
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("client_id", ClientID)
	params.Set("client_secret", clientSecret)
	params.Set("code", code)
	params.Set("redirect_uri", RedirectURI)
	params.Set("grant_type", "authorization_code")
	params.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("token 交换请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token 交换失败 (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("token 解析失败: %w", err)
	}

	return &tokenResp, nil
}

// RefreshToken 刷新 access_token
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	clientSecret, err := getClientSecret()
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("client_id", ClientID)
	params.Set("client_secret", clientSecret)
	params.Set("refresh_token", refreshToken)
	params.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("token 刷新请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token 刷新失败 (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("token 解析失败: %w", err)
	}

	return &tokenResp, nil
}

// GetUserInfo 获取用户信息
func (c *Client) GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("用户信息请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取用户信息失败 (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var userInfo UserInfo
	if err := json.Unmarshal(bodyBytes, &userInfo); err != nil {
		return nil, fmt.Errorf("用户信息解析失败: %w", err)
	}

	return &userInfo, nil
}

// LoadCodeAssist 获取账户信息，返回解析后的结构体和原始 JSON
// 支持 URL fallback：sandbox → daily → prod
func (c *Client) LoadCodeAssist(ctx context.Context, accessToken string) (*LoadCodeAssistResponse, map[string]any, error) {
	reqBody := LoadCodeAssistRequest{}
	reqBody.Metadata.IDEType = "ANTIGRAVITY"
	reqBody.Metadata.IDEVersion = GetUserAgentVersionForContext(ctx)
	reqBody.Metadata.IDEName = "antigravity"

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 固定顺序：prod -> daily
	availableURLs := BaseURLs

	var lastErr error
	for urlIdx, baseURL := range availableURLs {
		apiURL := baseURL + "/v1internal:loadCodeAssist"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			lastErr = fmt.Errorf("创建请求失败: %w", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

		resp, err := servertiming.Do(c.httpClient, req)
		if err != nil {
			lastErr = fmt.Errorf("loadCodeAssist 请求失败: %w", err)
			if shouldFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
				log.Printf("[antigravity] loadCodeAssist URL fallback: %s -> %s", baseURL, availableURLs[urlIdx+1])
				continue
			}
			return nil, nil, lastErr
		}

		respBodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close() // 立即关闭，避免循环内 defer 导致的资源泄漏
		if err != nil {
			return nil, nil, fmt.Errorf("读取响应失败: %w", err)
		}

		// 检查是否需要 URL 降级
		if shouldFallbackToNextURL(nil, resp.StatusCode) && urlIdx < len(availableURLs)-1 {
			log.Printf("[antigravity] loadCodeAssist URL fallback (HTTP %d): %s -> %s", resp.StatusCode, baseURL, availableURLs[urlIdx+1])
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("loadCodeAssist 失败 (HTTP %d): %s", resp.StatusCode, string(respBodyBytes))
		}

		var loadResp LoadCodeAssistResponse
		if err := json.Unmarshal(respBodyBytes, &loadResp); err != nil {
			return nil, nil, fmt.Errorf("响应解析失败: %w", err)
		}

		// 解析原始 JSON 为 map
		var rawResp map[string]any
		_ = json.Unmarshal(respBodyBytes, &rawResp)

		// 标记成功的 URL，下次优先使用
		DefaultURLAvailability.MarkSuccess(baseURL)
		return &loadResp, rawResp, nil
	}

	return nil, nil, lastErr
}

// OnboardUser 触发账号 onboarding，并返回 project_id
// 说明：
// 1) 部分账号 loadCodeAssist 不会立即返回 cloudaicompanionProject；
// 2) 这时需要调用 onboardUser 完成初始化，之后才能拿到 project_id。
func (c *Client) OnboardUser(ctx context.Context, accessToken, tierID string) (string, error) {
	tierID = strings.TrimSpace(tierID)
	if tierID == "" {
		return "", fmt.Errorf("tier_id 为空")
	}

	reqBody := OnboardUserRequest{TierID: tierID}
	reqBody.Metadata.IDEType = "ANTIGRAVITY"
	reqBody.Metadata.Platform = "PLATFORM_UNSPECIFIED"
	reqBody.Metadata.PluginType = "GEMINI"

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	availableURLs := BaseURLs
	var lastErr error

	for urlIdx, baseURL := range availableURLs {
		apiURL := baseURL + "/v1internal:onboardUser"

		for attempt := 1; attempt <= 5; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
			if err != nil {
				lastErr = fmt.Errorf("创建请求失败: %w", err)
				break
			}
			req.Header.Set("Authorization", "Bearer "+accessToken)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

			resp, err := servertiming.Do(c.httpClient, req)
			if err != nil {
				lastErr = fmt.Errorf("onboardUser 请求失败: %w", err)
				if shouldFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
					log.Printf("[antigravity] onboardUser URL fallback: %s -> %s", baseURL, availableURLs[urlIdx+1])
					break
				}
				return "", lastErr
			}

			respBodyBytes, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				return "", fmt.Errorf("读取响应失败: %w", err)
			}

			if shouldFallbackToNextURL(nil, resp.StatusCode) && urlIdx < len(availableURLs)-1 {
				log.Printf("[antigravity] onboardUser URL fallback (HTTP %d): %s -> %s", resp.StatusCode, baseURL, availableURLs[urlIdx+1])
				break
			}

			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("onboardUser 失败 (HTTP %d): %s", resp.StatusCode, string(respBodyBytes))
				return "", lastErr
			}

			var onboardResp OnboardUserResponse
			if err := json.Unmarshal(respBodyBytes, &onboardResp); err != nil {
				lastErr = fmt.Errorf("onboardUser 响应解析失败: %w", err)
				return "", lastErr
			}

			if onboardResp.Done {
				if projectID := extractProjectIDFromOnboardResponse(onboardResp.Response); projectID != "" {
					DefaultURLAvailability.MarkSuccess(baseURL)
					return projectID, nil
				}
				lastErr = fmt.Errorf("onboardUser 完成但未返回 project_id")
				return "", lastErr
			}

			// done=false 时等待后重试（与 CLIProxyAPI 行为一致）
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("onboardUser 未返回 project_id")
}

func extractProjectIDFromOnboardResponse(resp map[string]any) string {
	if len(resp) == 0 {
		return ""
	}

	if v, ok := resp["cloudaicompanionProject"]; ok {
		switch project := v.(type) {
		case string:
			return strings.TrimSpace(project)
		case map[string]any:
			if id, ok := project["id"].(string); ok {
				return strings.TrimSpace(id)
			}
		}
	}

	return ""
}

// FetchAvailableModels 获取可用模型和配额信息，返回解析后的结构体和原始 JSON
// 支持 URL fallback：sandbox → daily → prod
func (c *Client) FetchAvailableModels(ctx context.Context, accessToken, projectID string, bodyLimit int64) (*FetchAvailableModelsResponse, map[string]any, error) {
	if c == nil || c.httpClient == nil {
		return nil, nil, errors.New("antigravity client is not configured")
	}
	if bodyLimit <= 0 {
		return nil, nil, errors.New("fetchAvailableModels body limit must be positive")
	}

	reqBody := FetchAvailableModelsRequest{Project: projectID}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 固定顺序：prod -> daily
	availableURLs := BaseURLs

	fetchClient := c.fetchAvailableModelsHTTPClient()
	var lastErr error
	for urlIdx, baseURL := range availableURLs {
		apiURL := baseURL + "/v1internal:fetchAvailableModels"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			lastErr = fmt.Errorf("创建请求失败: %w", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

		resp, err := servertiming.Do(fetchClient, req)
		if err != nil {
			lastErr = fmt.Errorf("fetchAvailableModels 请求失败: %w", err)
			if shouldFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
				log.Printf("[antigravity] fetchAvailableModels URL fallback: %s -> %s", baseURL, availableURLs[urlIdx+1])
				continue
			}
			return nil, nil, lastErr
		}

		respBodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
		_ = resp.Body.Close() // 立即关闭，避免循环内 defer 导致的资源泄漏
		if err != nil {
			return nil, nil, fmt.Errorf("读取响应失败: %w", err)
		}
		if int64(len(respBodyBytes)) > bodyLimit {
			return nil, nil, fmt.Errorf("响应超过 %d 字节", bodyLimit)
		}

		// 检查是否需要 URL 降级
		if shouldFallbackToNextURL(nil, resp.StatusCode) && urlIdx < len(availableURLs)-1 {
			log.Printf("[antigravity] fetchAvailableModels URL fallback (HTTP %d): %s -> %s", resp.StatusCode, baseURL, availableURLs[urlIdx+1])
			continue
		}

		if resp.StatusCode == http.StatusForbidden {
			return nil, nil, &ForbiddenError{
				StatusCode: resp.StatusCode,
				Body:       string(respBodyBytes),
			}
		}

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("fetchAvailableModels 失败 (HTTP %d): %s", resp.StatusCode, string(respBodyBytes))
		}

		var modelsResp FetchAvailableModelsResponse
		if err := json.Unmarshal(respBodyBytes, &modelsResp); err != nil {
			return nil, nil, fmt.Errorf("响应解析失败: %w", err)
		}

		// 解析原始 JSON 为 map
		var rawResp map[string]any
		_ = json.Unmarshal(respBodyBytes, &rawResp)

		// 标记成功的 URL，下次优先使用
		DefaultURLAvailability.MarkSuccess(baseURL)
		return &modelsResp, rawResp, nil
	}

	return nil, nil, lastErr
}

func (c *Client) fetchAvailableModelsHTTPClient() *http.Client {
	fetchClient := *c.httpClient
	fetchClient.CheckRedirect = checkFetchAvailableModelsRedirect
	return &fetchClient
}

func checkFetchAvailableModelsRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req == nil || req.URL == nil {
		return errors.New("redirect url is nil")
	}
	if !isAllowedFetchAvailableModelsRedirectHost(req.URL.Hostname()) {
		return fmt.Errorf("redirect to unsupported host: %s", req.URL.Hostname())
	}
	return nil
}

func isAllowedFetchAvailableModelsRedirectHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, baseURL := range BaseURLs {
		parsed, err := url.Parse(baseURL)
		if err != nil {
			continue
		}
		if strings.EqualFold(host, parsed.Hostname()) {
			return true
		}
	}
	return false
}

// ── Privacy API ──────────────────────────────────────────────────────

// privacyBaseURL 隐私设置 API 仅使用 daily 端点（与 Antigravity 客户端行为一致）
const privacyBaseURL = antigravityDailyBaseURL

// SetUserSettings 调用 setUserSettings API 设置用户隐私，返回解析后的响应
func (c *Client) SetUserSettings(ctx context.Context, accessToken string) (*SetUserSettingsResponse, error) {
	// 发送空 user_settings 以清除隐私设置
	payload := SetUserSettingsRequest{UserSettings: map[string]any{}}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	apiURL := privacyBaseURL + "/v1internal:setUserSettings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", GetUserAgentForContext(ctx))
	req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")
	req.Host = "daily-cloudcode-pa.googleapis.com"

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("setUserSettings 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("setUserSettings 失败 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result SetUserSettingsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}

	return &result, nil
}

// FetchUserInfo 调用 fetchUserInfo API 获取用户隐私设置状态
func (c *Client) FetchUserInfo(ctx context.Context, accessToken, projectID string) (*FetchUserInfoResponse, error) {
	reqBody := FetchUserInfoRequest{Project: projectID}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	apiURL := privacyBaseURL + "/v1internal:fetchUserInfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", GetUserAgentForContext(ctx))
	req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")
	req.Host = "daily-cloudcode-pa.googleapis.com"

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("fetchUserInfo 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetchUserInfo 失败 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result FetchUserInfoResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}

	return &result, nil
}

type AvailableCredit = googlewire.AntigravityAvailableCredit

type DeprecatedModelInfo = googlewire.AntigravityDeprecatedModelInfo

type FetchAvailableModelsRequest = googlewire.AntigravityFetchAvailableModelsRequest

type FetchAvailableModelsResponse = googlewire.AntigravityFetchAvailableModelsResponse

type FetchUserInfoRequest = googlewire.AntigravityFetchUserInfoRequest

type FetchUserInfoResponse = googlewire.AntigravityFetchUserInfoResponse

type IneligibleTier = googlewire.AntigravityIneligibleTier

type LoadCodeAssistRequest = googlewire.AntigravityLoadCodeAssistRequest

type LoadCodeAssistResponse = googlewire.AntigravityLoadCodeAssistResponse

type ModelInfo = googlewire.AntigravityModelInfo

type ModelQuotaInfo = googlewire.AntigravityModelQuotaInfo

type OnboardUserRequest = googlewire.AntigravityOnboardUserRequest

type OnboardUserResponse = googlewire.AntigravityOnboardUserResponse

type PaidTierInfo = googlewire.AntigravityPaidTierInfo

type SetUserSettingsRequest = googlewire.AntigravitySetUserSettingsRequest

type SetUserSettingsResponse = googlewire.AntigravitySetUserSettingsResponse

type TierInfo = googlewire.AntigravityTierInfo

type TokenResponse = googlewire.AntigravityTokenResponse

type UserInfo = googlewire.AntigravityUserInfo

func TierIDToPlanType(tierID string) string { return googlewire.AntigravityTierIDToPlanType(tierID) }

// ClientOptions 接受已经装配的技术客户端，不改变默认代理或超时策略。
type ClientOptions struct{ HTTPClient *http.Client }

// NewClientWithOptions 用于显式传输装配；未提供客户端时仍执行原构造路径。
func NewClientWithOptions(proxyURL string, options ClientOptions) (*Client, error) {
	client, err := NewClient(proxyURL)
	if err != nil {
		return nil, err
	}
	if options.HTTPClient != nil {
		client.httpClient = options.HTTPClient
	}
	return client, nil
}
