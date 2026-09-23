// 兼容执行入口投影本次账号的技术能力，平台 Adapter 不持有旧网关或配置。
package service

import (
	"context"
	"net/http"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// CreativeTarget 只为原执行账号投影技术调用，任务运行时由 app 直接构造。
func (gateway *OpenAIGatewayService) CreativeTarget(account *gatewayprovider.ExecutionAccount, cfg *config.Config, geminiTokens *accountcore.GeminiTokenSource, enter func() (func(), error)) *creativeprovider.Target {
	target := &creativeprovider.Target{}
	if gateway == nil {
		return target
	}
	token := func(ctx context.Context) (string, error) {
		value, _, err := gateway.executionCredentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
		return value, err
	}
	target.OpenAI = &creativeprovider.OpenAIOptions{Token: token, URL: func(endpoint string) (string, error) {
		targetURL := openAIImagesGenerationsURL
		if endpoint == upstream.OpenAIImagesEditsEndpoint {
			targetURL = openAIImagesEditsURL
		}
		baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIBaseURL()
		if baseURL == "" {
			return targetURL, nil
		}
		validated, err := gateway.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return "", creative.CreativeNonRetryableError("creative openai base url invalid: %s", err.Error())
		}
		return httpclient.BuildOpenAIEndpointURL(validated, endpoint), nil
	}, Prepare: func(req *http.Request) *http.Request {
		return req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
	}, AuthHeaders: func(ctx context.Context, token string) (http.Header, error) {
		return gateway.agentIdentity.Headers(ctx, account, token)
	}, ApplyHeaders: bindAccountHeaders(account), Do: func(req *http.Request) (*http.Response, error) {
		return gateway.httpUpstream.DoWithTLS(req, accountProxyURL(account), account.Record.ID, account.Record.Concurrency, gateway.resolveOpenAITLSProfile(account))
	}}
	target.Grok = &creativeprovider.GrokOptions{OAuth: account.View().IsGrokOAuth(), Token: token, URL: func(endpoint grok.GrokMediaEndpoint) (string, error) {
		return buildGrokMediaURL(account, cfg, endpoint, "")
	}, Prepare: func(req *http.Request) *http.Request {
		return req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileGrok))
	}, ApplyHeaders: bindAccountHeaders(account), Do: func(req *http.Request) (*http.Response, error) {
		return gateway.httpUpstream.Do(req, accountProxyURL(account), account.Record.ID, account.Record.Concurrency)
	}}
	target.Gemini = func(model string) gemininative.ImageOptions {
		options := gemininative.ImageOptions{Mode: gemininative.CredentialMode(account.Record.Type), Model: model, ProjectID: account.View().GetCredential("project_id"), APIKey: func() string { return account.View().GetCredential("api_key") }, BaseURL: func() string { return account.View().GetGeminiBaseURL(geminicli.AIStudioBaseURL) }, ValidateURL: gateway.validateUpstreamBaseURL, ValidateGeminiBaseURL: gateway.validateGeminiBaseURL, VertexURL: func() (string, error) {
			return vertex.BuildVertexGeminiURL(gatewayprovider.ExecutionProtocolRecord(account).VertexProjectID(vertex.ServiceAccountProjectID), gatewayprovider.ExecutionProtocolRecord(account).VertexLocation(model), model, "generateContent", false)
		}, ApplyHeaders: bindAccountHeaders(account), Do: func(req *http.Request) (*http.Response, error) {
			return gateway.httpUpstream.Do(req, accountProxyURL(account), account.Record.ID, account.Record.Concurrency)
		}, HTTPError: func(status int, message string) error { return creative.CreativeHTTPStatusError(status, message) }, Invalid: func(format string, args ...any) error { return creative.CreativeNonRetryableError(format, args...) }, ErrorMessage: upstream.ExtractErrorMessage, Enter: enter}
		if geminiTokens != nil {
			options.Token = func(ctx context.Context) (string, error) { return accountToken(ctx, geminiTokens, account) }
		}
		return options
	}
	return target
}
