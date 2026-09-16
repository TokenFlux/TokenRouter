// Vertex 迁移保留原协议及取消边界，旧入口仅投影。
package googleauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/golang-jwt/jwt/v5"
)

const CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
const ServiceAccountCacheSkew = 5 * time.Minute

// NewServiceAccountHTTPClient 创建用于服务账号换 token 的 HTTP 客户端。
func NewServiceAccountHTTPClient(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return timing.InstrumentClient(&http.Client{Timeout: 15 * time.Second}), nil
	}

	_, parsedProxy, err := proxy.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("unexpected default transport type %T", http.DefaultTransport)
	}
	transport := defaultTransport.Clone()
	transport.Proxy = nil
	if err := proxy.ConfigureTransportProxy(transport, parsedProxy); err != nil {
		return nil, err
	}
	return timing.InstrumentClient(&http.Client{Timeout: 15 * time.Second, Transport: transport}), nil
}

// ExchangeServiceAccountToken 使用服务账号私钥向 Google token 端点换取访问令牌。
func ExchangeServiceAccountToken(ctx context.Context, key *google.ServiceAccountKey, proxyURL string) (string, time.Duration, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   key.ClientEmail,
		"scope": CloudPlatformScope,
		"aud":   key.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if strings.TrimSpace(key.PrivateKeyID) != "" {
		token.Header["kid"] = key.PrivateKeyID
	}
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key.PrivateKey))
	if err != nil {
		return "", 0, fmt.Errorf("parse service account private key: %w", err)
	}
	assertion, err := token.SignedString(privateKey)
	if err != nil {
		return "", 0, fmt.Errorf("sign service account assertion: %w", err)
	}

	values := url.Values{}
	values.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	values.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, key.TokenURI, strings.NewReader(values.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client, err := NewServiceAccountHTTPClient(proxyURL)
	if err != nil {
		return "", 0, fmt.Errorf("configure service account token proxy: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("service account token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed google.ServiceAccountTokenResponse
	_ = json.Unmarshal(body, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(parsed.ErrorDesc)
		if msg == "" {
			msg = strings.TrimSpace(parsed.Error)
		}
		if msg == "" {
			msg = string(bytes.TrimSpace(body))
		}
		return "", 0, fmt.Errorf("service account token request returned %d: %s", resp.StatusCode, msg)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return "", 0, errors.New("service account token response missing access_token")
	}
	ttl := time.Duration(parsed.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	if ttl > ServiceAccountCacheSkew {
		ttl -= ServiceAccountCacheSkew
	}
	return parsed.AccessToken, ttl, nil
}
