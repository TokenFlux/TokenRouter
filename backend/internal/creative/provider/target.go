package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// target.go dispatches creative image execution through callbacks bound to the
// selected provider.
// Creative platform requests obtain credentials and transport from a target
// bound to one selected provider. OpenAI image retrieval has its own download
// callback, while authenticated generation callbacks prepare requests and
// apply provider headers before the selected transport sends them.

// OpenAIOptions binds credential, generation transport, and public image
// download callbacks for one provider.
type OpenAIOptions struct {
	OAuth     bool   // selected OAuth image bridge
	MainModel string // configured Responses carrier model
	// BuildOAuth applies the same authenticated Responses request construction as
	// the gateway, after creative execution has prepared the hosted image tool.
	BuildOAuth func(context.Context, []byte, string) (*http.Request, error)

	// ImageMode reads the selected endpoint capability. Nil follows the default
	// both-mode behavior for callers that do not bind provider configuration.
	ImageMode func(protocol.ProtocolID) (protocol.ImageStreamMode, error)
	// Token resolves generation credentials when Execute invokes the provider.
	// Configure it with the selected provider token source before calling the
	// platform adapter.
	Token func(context.Context) (string, error)
	// URL resolves the generation endpoint for the selected provider. Its
	// callback validates configured targets and returns errors for the platform
	// adapter to classify.
	URL func(string) (string, error)
	// Prepare supplies the request and transport context used by this provider.
	// The adapter calls it before authentication and expects a nonnil returned
	// request.
	Prepare func(*http.Request) *http.Request
	// AuthHeaders supplies authentication headers for the resolved OpenAI token.
	// ExecuteOpenAI adds its returned header values before the provider header
	// callback runs.
	AuthHeaders func(context.Context, string) (http.Header, error)
	// ApplyHeaders applies provider header values after the adapter
	// authentication and protocol defaults, allowing the configured provider
	// headers to take precedence.
	ApplyHeaders func(http.Header)
	// Do sends the prepared generation request and returns a nonnil response body
	// on success. The platform adapter closes that body after its bounded read.
	Do func(*http.Request) (*http.Response, error)
	// FetchImage downloads a result URL into base64 using the provider proxy.
	// Implementations omit generation credentials and custom generation headers
	// because returned image addresses can identify a separate host.
	FetchImage func(context.Context, string) (string, error)
}

// GrokOptions binds xAI credential, endpoint, and transport callbacks for one
// selected provider.
type GrokOptions struct {
	// OAuth enables Grok CLI headers when the resolved endpoint is a CLI proxy.
	// Other endpoint and credential routes keep the adapter ordinary JSON
	// headers.
	OAuth bool
	// Token resolves generation credentials when Execute invokes the provider.
	// Configure it with the selected provider token source before calling the
	// platform adapter.
	Token func(context.Context) (string, error)
	// URL resolves the generation endpoint for the selected provider. Its
	// callback validates configured targets and returns errors for the platform
	// adapter to classify.
	URL func(grok.GrokMediaEndpoint) (string, error)
	// Prepare supplies the request and transport context used by this provider.
	// The adapter calls it before authentication and expects a nonnil returned
	// request.
	Prepare func(*http.Request) *http.Request
	// ApplyHeaders applies provider header values after the adapter
	// authentication and protocol defaults, allowing the configured provider
	// headers to take precedence.
	ApplyHeaders func(http.Header)
	// Do sends the prepared generation request and returns a nonnil response body
	// on success. The platform adapter closes that body after its bounded read.
	Do func(*http.Request) (*http.Response, error)
}

// Target groups the selected provider callbacks used by creative platform
// execution.
type Target struct {
	// OpenAI holds the callback set used for OpenAI execution. Missing options
	// return a configuration error before request construction can invoke any
	// callback.
	OpenAI *OpenAIOptions // bound OpenAI provider callbacks
	// Grok holds the callback set used for xAI execution. Missing options
	// return a configuration error after operation and edit source checks have
	// passed.
	Grok *GrokOptions // bound xAI provider callbacks
	// Gemini supplies model-specific credentials and endpoint options during
	// execution. A nil factory returns a configuration error before the shared
	// GenerateImages call.
	Gemini func(string) gemininative.ImageOptions
}

// ExecutePlatform invokes the callback set selected by platform and returns
// its image results. Callers configure the corresponding target options;
// unknown platforms return a nonretryable creative execution error.
func (t *Target) ExecutePlatform(ctx context.Context, platform string, run creative.CreativeRun, payload creative.CreativeRunPayload, model string) ([]creative.CreativeOutput, error) {
	switch platform {
	case creative.PlatformOpenAI:
		return t.ExecuteOpenAI(ctx, run, payload, model)
	case creative.PlatformGrok:
		return t.ExecuteGrok(ctx, run, payload, model)
	case creative.PlatformGemini:
		return t.ExecuteGemini(ctx, run, payload, model)
	default:
		return nil, creative.CreativeNonRetryableError("creative executor unsupported provider platform %s", platform)
	}
}
