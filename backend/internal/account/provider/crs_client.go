// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	bytes "bytes"
	context "context"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	transfer "github.com/TokenFlux/TokenRouter/internal/account/transfer"
	urlvalidator "github.com/TokenFlux/TokenRouter/internal/egress"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	io "io"
	http "net/http"
	slices "slices"
	strings "strings"
	time "time"
)

// CRSClientOptions 只投影原 CRS 连接约束，不持有完整应用配置。
type CRSClientOptions struct {
	Configured, AllowlistEnabled, AllowInsecureHTTP, AllowPrivateHosts bool
	Hosts                                                              []string
}
type CRSClient struct{ options CRSClientOptions }

func NewCRSClient(options CRSClientOptions) *CRSClient {
	options.Hosts = slices.Clone(options.Hosts)
	return &CRSClient{options: options}
}

type crsLoginResponse struct {
	Success  bool   `json:"success"`
	Token    string `json:"token"`
	Message  string `json:"message"`
	Error    string `json:"error"`
	Username string `json:"username"`
}

// Fetch 按原顺序校验连接、登录并读取导出；同步与预览共用此技术实现。
func (s *CRSClient) Fetch(ctx context.Context, baseURL, username, password string) (*transfer.CRSExportResponse, error) {
	if !s.options.Configured {
		return nil, errors.New("config is not available")
	}
	normalizedURL := strings.TrimSpace(baseURL)
	if s.options.AllowlistEnabled {
		normalized, err := NormalizeCRSBaseURL(normalizedURL, s.options.Hosts, s.options.AllowPrivateHosts)
		if err != nil {
			return nil, err
		}
		normalizedURL = normalized
	} else {
		normalized, err := urlvalidator.ValidateURLFormat(normalizedURL, s.options.AllowInsecureHTTP)
		if err != nil {
			return nil, fmt.Errorf("invalid base_url: %w", err)
		}
		normalizedURL = normalized
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return nil, errors.New("username and password are required")
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:            20 * time.Second,
		ValidateResolvedIP: s.options.AllowlistEnabled,
		AllowPrivateHosts:  s.options.AllowPrivateHosts,
	})
	if err != nil {
		return nil, fmt.Errorf("create http client failed: %w", err)
	}

	adminToken, err := crsLogin(ctx, client, normalizedURL, username, password)
	if err != nil {
		return nil, err
	}

	return crsExportAccounts(ctx, client, normalizedURL, adminToken)
}
func NormalizeCRSBaseURL(raw string, allowlist []string, allowPrivate bool) (string, error) {
	// 当 allowlist 为空时，不强制要求白名单（只进行基本的 URL 和 SSRF 验证）
	requireAllowlist := len(allowlist) > 0
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     allowlist,
		RequireAllowlist: requireAllowlist,
		AllowPrivate:     allowPrivate,
	})
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}
func crsLogin(ctx context.Context, client *http.Client, baseURL, username, password string) (string, error) {
	payload := map[string]any{
		"username": username,
		"password": password,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/web/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("crs login failed: status=%d body=%s", resp.StatusCode, string(raw))
	}

	var parsed crsLoginResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("crs login parse failed: %w", err)
	}
	if !parsed.Success || strings.TrimSpace(parsed.Token) == "" {
		msg := parsed.Message
		if msg == "" {
			msg = parsed.Error
		}
		if msg == "" {
			msg = "unknown error"
		}
		return "", errors.New("crs login failed: " + msg)
	}
	return parsed.Token, nil
}
func crsExportAccounts(ctx context.Context, client *http.Client, baseURL, adminToken string) (*transfer.CRSExportResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/admin/sync/export-accounts?include_secrets=true", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("crs export failed: status=%d body=%s", resp.StatusCode, string(raw))
	}

	var parsed transfer.CRSExportResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("crs export parse failed: %w", err)
	}
	if !parsed.Success {
		msg := parsed.Message
		if msg == "" {
			msg = parsed.Error
		}
		if msg == "" {
			msg = "unknown error"
		}
		return nil, errors.New("crs export failed: " + msg)
	}
	return &parsed, nil
}
