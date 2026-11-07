package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// creative_targets.go binds image download and generation callbacks to
// selected providers.
// Creative image tasks need downloaded image bytes even when ordinary API
// response backfill is disabled. Target binding supplies separate public-image
// download and authenticated generation operations, using the selected
// provider's proxy, headers, TLS, and credentials when each callback executes.
// Gemini options also capture the requested model and current project ID when
// constructed.

// CreativeTargets binds app-owned request, credential, transport, and activity
// dependencies to creative execution.
type CreativeTargets struct {
	// ImageResponses constructs an OAuth carrier request using gateway headers
	// and identity. It is required only for selected OAuth creative execution.
	ImageResponses func(context.Context, *ExecutionProvider, []byte, string) (*http.Request, error)

	// Requests supplies endpoint validation and generation transport policy. Configure
	// it before invoking target callbacks that resolve URLs, downloads, or TLS profiles.
	Requests interface {
		// ImagesURL resolves the selected provider's image endpoint for generation requests.
		ImagesURL(*ExecutionProvider, string) (string, error)
		// ValidateBaseURL applies outbound URL policy to generation and public image download targets.
		ValidateBaseURL(string) (string, error)
		// TLSProfile resolves the selected provider's TLS profile for authenticated image generation.
		TLSProfile(*ExecutionProvider, ...egress.TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile
	}
	// OpenAI and Grok credential source
	Credentials *providercore.OpenAIExecutionCredentials
	// Agent Identity authentication adapter
	Identity *ExecutionAgentIdentity
	// shared upstream request transport
	Transport httpclient.UpstreamTransport
	// Grok media endpoint policy
	Routes GrokRoutes
	// optional Gemini access token source
	GeminiTokens *providercore.GeminiTokenSource
	// creative request activity admission callback
	Enter func() (func(), error)
}

// ForProvider builds OpenAI, Grok, and Gemini callbacks for the selected
// execution target. A nil receiver returns an empty target; other calls
// require a nonnil provider and dependencies for the invoked platform
// operations. Callers keep the target stable while callbacks read credentials
// and transport settings. Public image download uses the provider proxy and
// isolation parameters separately from generation authentication.
func (s *CreativeTargets) ForProvider(provider *ExecutionProvider) *creativeprovider.Target {
	target := &creativeprovider.Target{}
	if s == nil {
		return target
	}
	token := func(ctx context.Context) (string, error) {
		value, _, err := s.Credentials.Resolve(ctx, ExecutionRecord(provider))
		return value, err
	}
	// Creative result storage needs bytes even when API URL backfill is disabled.
	// Bind a public-image downloader through the selected proxy, separately from generation authentication.
	imageDownload := openai.ImageBackfillOptions{
		ValidateURL: func(raw string) (string, error) {
			return s.Requests.ValidateBaseURL(raw)
		},
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Transport.Do(req, creativeTargetProxyURL(provider), provider.Record.ID, provider.Record.Concurrency)
		},
	}
	target.OpenAI = &creativeprovider.OpenAIOptions{
		OAuth:     provider.View().IsOpenAIOAuthLike(),
		MainModel: provider.View().CodexImageResponsesModel(),
		BuildOAuth: func(ctx context.Context, body []byte, token string) (*http.Request, error) {
			if s.ImageResponses == nil {
				return nil, errors.New("creative OAuth image bridge is unavailable")
			}
			return s.ImageResponses(ctx, provider, body, token)
		},
		ImageMode:  ExecutionProtocolRecord(provider).ImageStreamMode,
		FetchImage: imageDownload.FetchBase64,
		Token:      token,
		URL: func(endpoint string) (string, error) {
			targetURL, err := s.Requests.ImagesURL(provider, endpoint)
			if err != nil {
				return "", creative.CreativeNonRetryableError("creative openai base url invalid: %s", err.Error())
			}
			return targetURL, nil
		},
		Prepare: func(req *http.Request) *http.Request {
			return req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))
		},
		AuthHeaders: func(ctx context.Context, token string) (http.Header, error) {
			return s.Identity.Headers(ctx, provider, token)
		},
		ApplyHeaders: BindExecutionHeaders(provider),
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Transport.DoWithTLS(req, creativeTargetProxyURL(provider), provider.Record.ID, provider.Record.Concurrency, s.Requests.TLSProfile(provider))
		},
	}
	target.Grok = &creativeprovider.GrokOptions{
		OAuth: provider.View().IsGrokOAuth(),
		Token: token,
		URL: func(endpoint grok.GrokMediaEndpoint) (string, error) {
			return s.Routes.Media(provider, endpoint, "")
		},
		Prepare: func(req *http.Request) *http.Request {
			return req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileGrok))
		},
		ApplyHeaders: BindExecutionHeaders(provider),
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Transport.Do(req, creativeTargetProxyURL(provider), provider.Record.ID, provider.Record.Concurrency)
		},
	}
	target.Gemini = func(model string) gemininative.ImageOptions {
		options := gemininative.ImageOptions{
			Mode:                  gemininative.CredentialMode(provider.Record.Type),
			Model:                 model,
			ProjectID:             provider.View().GetCredential("project_id"),
			APIKey:                func() string { return provider.View().GetCredential("api_key") },
			BaseURL:               func() string { return provider.View().GetGeminiBaseURL(geminicli.AIStudioBaseURL) },
			ValidateURL:           s.Requests.ValidateBaseURL,
			ValidateGeminiBaseURL: s.validateGeminiBaseURL,
			VertexURL: func() (string, error) {
				return vertex.BuildVertexGeminiURL(ExecutionProtocolRecord(provider).VertexProjectID(vertex.ServiceAccountProjectID), ExecutionProtocolRecord(provider).VertexLocation(model), model, "generateContent", false)
			},
			ApplyHeaders: BindExecutionHeaders(provider),
			Do: func(req *http.Request) (*http.Response, error) {
				return s.Transport.Do(req, creativeTargetProxyURL(provider), provider.Record.ID, provider.Record.Concurrency)
			},
			HTTPError:    func(status int, message string) error { return creative.CreativeHTTPStatusError(status, message) },
			Invalid:      func(format string, args ...any) error { return creative.CreativeNonRetryableError(format, args...) },
			ErrorMessage: upstream.ExtractErrorMessage,
			Enter:        s.Enter,
		}
		if s.GeminiTokens != nil {
			options.Token = func(ctx context.Context) (string, error) {
				return ExecutionToken(ctx, s.GeminiTokens, provider)
			}
		}
		return options
	}
	return target
}

// validateGeminiBaseURL applies the configured URL validator and requires a
// nonblank validated result. The Requests dependency must be configured;
// validator errors propagate, and an empty successful result becomes a local
// error.
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

// creativeTargetProxyURL returns the hydrated proxy URL for a target with both
// proxy ID and record. Nil targets or incomplete proxy hydration select an
// empty transport proxy string.
func creativeTargetProxyURL(value *ExecutionProvider) string {
	if value == nil || value.Record.ProxyID == nil || value.Record.Proxy == nil {
		return ""
	}
	return value.Record.Proxy.URL()
}
