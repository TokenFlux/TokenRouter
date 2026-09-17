// 兼容执行入口投影本次账号的技术能力，平台 Adapter 不持有旧网关或配置。
package service

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/config"

	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

func legacyCreativeTargetFactory(gateway *OpenAIGatewayService, cfg *config.Config, geminiTokens *GeminiTokenProvider, e *CreativeExecutor) func(*Account) *creativeprovider.Target {
	return func(account *Account) *creativeprovider.Target {
		target := &creativeprovider.Target{}
		if gateway == nil {
			return target
		}
		token := func(ctx context.Context) (string, error) {
			value, _, err := gateway.GetAccessToken(ctx, account)
			return value, err
		}
		target.OpenAI = &creativeprovider.OpenAIOptions{Token: token, URL: func(endpoint string) (string, error) {
			targetURL := openAIImagesGenerationsURL
			if endpoint == openAIImagesEditsEndpoint {
				targetURL = openAIImagesEditsURL
			}
			baseURL := account.GetOpenAIBaseURL()
			if baseURL == "" {
				return targetURL, nil
			}
			validated, err := gateway.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return "", creativeNonRetryableError("creative openai base url invalid: %s", err.Error())
			}
			return buildOpenAIImagesURL(validated, endpoint), nil
		}, Prepare: func(req *http.Request) *http.Request {
			return req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
		}, AuthHeaders: func(ctx context.Context, token string) (http.Header, error) {
			return gateway.buildOpenAIAuthenticationHeaders(ctx, account, token)
		}, ApplyHeaders: account.ApplyHeaderOverrides, Do: func(req *http.Request) (*http.Response, error) {
			return gateway.httpUpstream.DoWithTLS(req, accountProxyURL(account), account.ID, account.Concurrency, gateway.resolveOpenAITLSProfile(account))
		}}
		target.Grok = &creativeprovider.GrokOptions{OAuth: account.IsGrokOAuth(), Token: token, URL: func(endpoint GrokMediaEndpoint) (string, error) {
			return buildGrokMediaURL(account, cfg, endpoint, "")
		}, Prepare: func(req *http.Request) *http.Request {
			return req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileGrok))
		}, ApplyHeaders: account.ApplyHeaderOverrides, Do: func(req *http.Request) (*http.Response, error) {
			return gateway.httpUpstream.Do(req, accountProxyURL(account), account.ID, account.Concurrency)
		}}
		target.Gemini = func(model string) gemininative.ImageOptions {
			options := gemininative.ImageOptions{Mode: gemininative.CredentialMode(account.Type), Model: model, ProjectID: account.GetCredential("project_id"), APIKey: func() string { return account.GetCredential("api_key") }, BaseURL: func() string { return account.GetGeminiBaseURL(geminicli.AIStudioBaseURL) }, ValidateURL: gateway.validateUpstreamBaseURL, ValidateGeminiBaseURL: gateway.validateGeminiBaseURL, VertexURL: func() (string, error) {
				return buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(model), model, "generateContent", false)
			}, ApplyHeaders: account.ApplyHeaderOverrides, Do: func(req *http.Request) (*http.Response, error) {
				return gateway.httpUpstream.Do(req, accountProxyURL(account), account.ID, account.Concurrency)
			}, HTTPError: func(status int, message string) error { return creativeHTTPStatusError(status, message) }, Invalid: func(format string, args ...any) error { return creativeNonRetryableError(format, args...) }, ErrorMessage: extractUpstreamErrorMessage, Enter: e.nativeAttemptActivity}
			if geminiTokens != nil {
				options.Token = func(ctx context.Context) (string, error) { return geminiTokens.GetAccessToken(ctx, account) }
			}
			return options
		}
		return target
	}
}

func (e *CreativeExecutor) nativeTarget(account *Account) *creativeprovider.Target {
	if e == nil || e.bindings.Target == nil {
		return &creativeprovider.Target{}
	}
	return e.bindings.Target(account)
}
