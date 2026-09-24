// 创作目标在单次任务尝试内绑定凭据、出站参数及供应商执行能力。
package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// CreativeTargets 固定任务所需的技术依赖，每次选号后只创建该账号的受控目标。
type CreativeTargets struct {
	Requests interface {
		ImagesURL(*ExecutionAccount, string) (string, error)
		ValidateBaseURL(string) (string, error)
		TLSProfile(*ExecutionAccount, ...egress.TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile
	}
	Credentials  *accountcore.OpenAIExecutionCredentials
	Identity     *ExecutionAgentIdentity
	Transport    httpclient.UpstreamTransport
	Routes       GrokRoutes
	GeminiTokens *accountcore.GeminiTokenSource
	Enter        func() (func(), error)
}

// ForAccount 不读取凭据或启动请求，各端口继续在实际执行时求值。
func (gateway *CreativeTargets) ForAccount(account *ExecutionAccount) *creativeprovider.Target {
	target := &creativeprovider.Target{}
	if gateway == nil {
		return target
	}
	token := func(ctx context.Context) (string, error) {
		value, _, err := gateway.Credentials.Resolve(ctx, ExecutionRecord(account))
		return value, err
	}
	target.OpenAI = &creativeprovider.OpenAIOptions{
		Token: token,
		URL: func(endpoint string) (string, error) {
			targetURL, err := gateway.Requests.ImagesURL(account, endpoint)
			if err != nil {
				return "", creative.CreativeNonRetryableError("creative openai base url invalid: %s", err.Error())
			}
			return targetURL, nil
		},
		Prepare: func(req *http.Request) *http.Request {
			return req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
		},
		AuthHeaders: func(ctx context.Context, token string) (http.Header, error) {
			return gateway.Identity.Headers(ctx, account, token)
		},
		ApplyHeaders: BindExecutionHeaders(account),
		Do: func(req *http.Request) (*http.Response, error) {
			return gateway.Transport.DoWithTLS(req, creativeTargetProxyURL(account), account.Record.ID, account.Record.Concurrency, gateway.Requests.TLSProfile(account))
		},
	}
	target.Grok = &creativeprovider.GrokOptions{
		OAuth: account.View().IsGrokOAuth(),
		Token: token,
		URL: func(endpoint grok.GrokMediaEndpoint) (string, error) {
			return gateway.Routes.Media(account, endpoint, "")
		},
		Prepare: func(req *http.Request) *http.Request {
			return req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileGrok))
		},
		ApplyHeaders: BindExecutionHeaders(account),
		Do: func(req *http.Request) (*http.Response, error) {
			return gateway.Transport.Do(req, creativeTargetProxyURL(account), account.Record.ID, account.Record.Concurrency)
		},
	}
	target.Gemini = func(model string) gemininative.ImageOptions {
		options := gemininative.ImageOptions{
			Mode:                  gemininative.CredentialMode(account.Record.Type),
			Model:                 model,
			ProjectID:             account.View().GetCredential("project_id"),
			APIKey:                func() string { return account.View().GetCredential("api_key") },
			BaseURL:               func() string { return account.View().GetGeminiBaseURL(geminicli.AIStudioBaseURL) },
			ValidateURL:           gateway.Requests.ValidateBaseURL,
			ValidateGeminiBaseURL: gateway.validateGeminiBaseURL,
			VertexURL: func() (string, error) {
				return vertex.BuildVertexGeminiURL(ExecutionProtocolRecord(account).VertexProjectID(vertex.ServiceAccountProjectID), ExecutionProtocolRecord(account).VertexLocation(model), model, "generateContent", false)
			},
			ApplyHeaders: BindExecutionHeaders(account),
			Do: func(req *http.Request) (*http.Response, error) {
				return gateway.Transport.Do(req, creativeTargetProxyURL(account), account.Record.ID, account.Record.Concurrency)
			},
			HTTPError:    func(status int, message string) error { return creative.CreativeHTTPStatusError(status, message) },
			Invalid:      func(format string, args ...any) error { return creative.CreativeNonRetryableError(format, args...) },
			ErrorMessage: upstream.ExtractErrorMessage,
			Enter:        gateway.Enter,
		}
		if gateway.GeminiTokens != nil {
			options.Token = func(ctx context.Context) (string, error) {
				return ExecutionToken(ctx, gateway.GeminiTokens, account)
			}
		}
		return options
	}
	return target
}

// validateGeminiBaseURL 校验显式目标，错误不会改投到默认地址。
func (s *CreativeTargets) validateGeminiBaseURL(raw string) (string, error) {
	validated, err := s.Requests.ValidateBaseURL(raw)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(validated) == "" {
		return "", errors.New("gemini base url is empty")
	}
	return validated, nil
}

func creativeTargetProxyURL(value *ExecutionAccount) string {
	if value == nil || value.Record.ProxyID == nil || value.Record.Proxy == nil {
		return ""
	}
	return value.Record.Proxy.URL()
}
