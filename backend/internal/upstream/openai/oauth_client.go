package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/imroc/req/v3"
)

// NewOAuthClient creates a new OpenAI OAuth client
func NewOAuthClient(httpUpstream OAuthHTTPUpstream) *OAuthClient {
	return &OAuthClient{tokenURL: TokenURL, httpUpstream: httpUpstream}
}

type OAuthClient struct {
	tokenURL     string
	httpUpstream OAuthHTTPUpstream
}

func (s *OAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = ClientID
	}

	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("client_id", clientID)
	formData.Set("code", code)
	formData.Set("redirect_uri", redirectURI)
	formData.Set("code_verifier", codeVerifier)

	return s.postTokenForm(ctx, formData, proxyURL, "OPENAI_OAUTH_TOKEN_EXCHANGE_FAILED", "token exchange failed", options...)
}

func (s *OAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	return s.RefreshTokenWithClientID(ctx, refreshToken, proxyURL, "", options...)
}

func (s *OAuthClient) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	// 调用方应始终传入正确的 client_id；为兼容旧数据，未指定时默认使用 OpenAI ClientID
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = ClientID
	}
	return s.refreshTokenWithClientID(ctx, refreshToken, proxyURL, clientID, options...)
}

func (s *OAuthClient) refreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL, clientID string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("refresh_token", refreshToken)
	formData.Set("client_id", clientID)
	formData.Set("scope", RefreshScopes)

	return s.postTokenForm(ctx, formData, proxyURL, "OPENAI_OAUTH_TOKEN_REFRESH_FAILED", "token refresh failed", options...)
}

func (s *OAuthClient) postTokenForm(ctx context.Context, formData url.Values, proxyURL, failureReason, failureMessage string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	option := firstOpenAIOAuthTokenRequestOption(options)
	if option.TLSProfile != nil {
		return s.postTokenFormWithTLS(ctx, formData, proxyURL, failureReason, failureMessage, option)
	}

	client, err := CreateOAuthReqClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	var tokenResp TokenResponse
	userAgent, originator := resolveOpenAIOAuthTokenIdentity(option)

	request := client.R().
		SetContext(ctx).
		SetHeader("User-Agent", userAgent).
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp)
	if originator != "" {
		request.SetHeader("originator", originator)
	}
	resp, err := request.Post(s.tokenURL)

	if err != nil {
		if shouldReturnOpenAINoProxyHint(ctx, proxyURL, err) {
			return nil, newOpenAINoProxyHintError(err)
		}
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}

	if !resp.IsSuccessState() {
		return nil, infraerrors.Newf(http.StatusBadGateway, failureReason, "%s: status %d, body: %s", failureMessage, resp.StatusCode, resp.String())
	}

	return &tokenResp, nil
}

func (s *OAuthClient) postTokenFormWithTLS(ctx context.Context, formData url.Values, proxyURL, failureReason, failureMessage string, option OAuthTokenRequestOptions) (*TokenResponse, error) {
	if s.httpUpstream == nil {
		return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_OAUTH_CLIENT_INIT_FAILED", "HTTP upstream is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	userAgent, originator := resolveOpenAIOAuthTokenIdentity(option)
	req.Header.Set("User-Agent", userAgent)
	if originator != "" {
		req.Header.Set("originator", originator)
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, option.AccountID, option.AccountConcurrency, option.TLSProfile)
	if err != nil {
		if shouldReturnOpenAINoProxyHint(ctx, proxyURL, err) {
			return nil, newOpenAINoProxyHintError(err)
		}
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "read response: %v", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, infraerrors.Newf(http.StatusBadGateway, failureReason, "%s: status %d, body: %s", failureMessage, resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "decode token response: %v", err)
	}
	return &tokenResp, nil
}

func CreateOAuthReqClient(proxyURL string) (*req.Client, error) {
	return httpclient.GetSharedReqClient(httpclient.ReqClientOptions{
		ProxyURL: proxyURL,
		Timeout:  120 * time.Second,
	})
}

func firstOpenAIOAuthTokenRequestOption(options []OAuthTokenRequestOptions) OAuthTokenRequestOptions {
	if len(options) == 0 {
		return OAuthTokenRequestOptions{}
	}
	return options[0]
}

func resolveOpenAIOAuthTokenIdentity(option OAuthTokenRequestOptions) (string, string) {
	if ua := strings.TrimSpace(option.UserAgent); ua != "" {
		return ua, ""
	}
	return CodexCanonicalAuthIdentity()
}

func shouldReturnOpenAINoProxyHint(ctx context.Context, proxyURL string, err error) bool {
	if strings.TrimSpace(proxyURL) != "" || err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled)
}

func newOpenAINoProxyHintError(cause error) error {
	return infraerrors.New(
		http.StatusBadGateway,
		"OPENAI_OAUTH_PROXY_REQUIRED",
		"OpenAI OAuth request failed: no proxy is configured and this server could not reach OpenAI directly. Select a proxy that can access OpenAI, then retry; if the authorization code has expired, regenerate the authorization URL.",
	).WithCause(cause)
}

// OAuthTokenRequestOptions 只描述本次交换的技术身份及 TLS 快照。
type OAuthTokenRequestOptions struct {
	UserAgent          string
	TLSProfile         *tlsfingerprint.Profile
	AccountID          int64
	AccountConcurrency int
}

// OAuthHTTPUpstream 复用应用唯一 HTTP 池，不读取账号实体或配置。
type OAuthHTTPUpstream interface {
	DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)
}
