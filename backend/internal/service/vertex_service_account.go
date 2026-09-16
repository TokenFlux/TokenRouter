// Vertex 迁移保留原协议及取消边界，旧入口仅投影。
package service

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	accountmodule "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

func (a *Account) IsVertexServiceAccount() bool {
	return a != nil && a.Type == AccountTypeServiceAccount
}
func (a *Account) VertexProjectID() string {
	if a == nil {
		return ""
	}
	return (&accountmodule.Record{Credentials: a.Credentials}).VertexProjectID(func(raw []byte) (string, error) {
		key, err := vertex.ParseVertexServiceAccountJSON(raw)
		if err != nil {
			return "", err
		}
		return key.ProjectID, nil
	})
}
func (a *Account) VertexLocation(model string) string {
	if a == nil {
		return (*accountmodule.Record)(nil).VertexLocation(model)
	}
	return (&accountmodule.Record{Credentials: a.Credentials}).VertexLocation(model)
}
func parseVertexServiceAccountKey(a *Account) (*vertexServiceAccountKey, error) {
	var input *accountmodule.Record
	if a != nil {
		input = &accountmodule.Record{Credentials: a.Credentials}
	}
	raw, err := accountmodule.VertexServiceAccountJSON(input)
	if err != nil {
		return nil, err
	}
	return vertex.ParseVertexServiceAccountJSON(raw)
}

// vertexServiceAccountProxyURL 返回账号绑定的代理地址。
func vertexServiceAccountProxyURL(account *Account) string {
	if account == nil || account.ProxyID == nil || account.Proxy == nil {
		return ""
	}
	return account.Proxy.URL()
}

type vertexServiceAccountKey = google.ServiceAccountKey
type vertexTokenResponse = google.ServiceAccountTokenResponse

const vertexDefaultTokenURL = vertex.DefaultTokenURL
const vertexAnthropicVersion = vertex.AnthropicVersion

func buildVertexGeminiURL(projectID, location, model, action string, stream bool) (string, error) {
	return vertex.BuildVertexGeminiURL(projectID, location, model, action, stream)
}

func buildVertexAnthropicURL(projectID, location, model string, stream bool) (string, error) {
	return vertex.BuildVertexAnthropicURL(projectID, location, model, stream)
}

func normalizeVertexAnthropicModelID(model string) string {
	return vertex.NormalizeVertexAnthropicModelID(model)
}

func buildVertexAnthropicRequestBody(body []byte) ([]byte, error) {
	return vertex.BuildVertexAnthropicRequestBody(body)
}

func vertexServiceAccountCacheKey(a *Account, key *vertexServiceAccountKey) string {
	var id int64
	if a != nil {
		id = a.ID
	}
	if key == nil {
		if a == nil {
			return "vertex:service_account:"
		}
		return accountmodule.VertexServiceAccountCacheKey(id, "", "", false)
	}
	return accountmodule.VertexServiceAccountCacheKey(id, key.ClientEmail, key.PrivateKeyID, true)
}
func getVertexServiceAccountAccessToken(ctx context.Context, cache GeminiTokenCache, a *Account) (string, error) {
	key, err := parseVertexServiceAccountKey(a)
	if err != nil {
		return "", err
	}
	return accountmodule.GetVertexServiceAccountAccessToken(ctx, accountmodule.VertexTokenOptions{AccountID: a.ID, CacheKey: vertexServiceAccountCacheKey(a, key), Cache: cache, Warn: slog.Warn, Exchange: func(ctx context.Context) (string, time.Duration, error) {
		return vertex.ExchangeServiceAccountToken(ctx, key, vertexServiceAccountProxyURL(a))
	}})
}
func newVertexServiceAccountHTTPClient(proxyURL string) (*http.Client, error) {
	return vertex.NewServiceAccountHTTPClient(proxyURL)
}
func exchangeVertexServiceAccountToken(ctx context.Context, key *vertexServiceAccountKey, proxyURL string) (string, time.Duration, error) {
	return vertex.ExchangeServiceAccountToken(ctx, key, proxyURL)
}
