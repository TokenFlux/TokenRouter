package openaiforward

import (
	"context"
	"net/http"
	"time"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// raw_chat_ports.go defines the platform-specific callbacks and retry
// observations used by raw Chat forwarding.
// Native Chat can still need platform request repairs and auxiliary image
// descriptions before exchange. Grok-specific health and retry evidence
// supplements shared model, credential, and output bindings so the runner can
// retain description usage alongside the main response and return provider-loop
// recovery guidance.

// RawChatPorts extends Chat fallback bindings with native platform preparation and health operations.
type RawChatPorts interface {
	RawFallbackPorts // shared Chat attempt bindings
	// GrokCacheIdentity derives a stable cache identity from client bytes and
	// model before bridging.
	GrokCacheIdentity([]byte, string, string) string
	// FastRaw applies the selected provider's fast-tier policy to native Chat
	// bytes.
	FastRaw(context.Context, string, []byte) ([]byte, error)
	// RawCredential returns credential text and its kind through the shared
	// request budget. Errors stop request preparation; RunRawChat separately
	// rejects a successful result whose trimmed credential text is empty.
	RawCredential(context.Context) (string, string, error)
	// BridgeImages replaces eligible Composer image input with descriptions and
	// returns their usage. Its changed flag identifies adoption, and returned
	// errors stop the main exchange before auxiliary usage is added to a final
	// result.
	BridgeImages(context.Context, []byte, string) ([]byte, protocolopenai.ForwardUsage, bool, error)
	// GrokEffort normalizes the selected Grok model's reasoning controls before
	// exchange.
	GrokEffort([]byte, string) ([]byte, error)
	// OllamaBody applies recognized Ollama Cloud Chat request compatibility
	// repairs to supplied bytes.
	OllamaBody([]byte) []byte
	// RawTarget resolves the selected provider's native Chat endpoint URL for
	// exchange.
	RawTarget() (string, error)
	// UserAgent reads the configured upstream User-Agent for the selected provider
	// attempt.
	UserAgent() string
	// SendRaw exchanges prepared native Chat bytes with the supplied credential,
	// identity, and User-Agent. The runner retains successful response-body
	// ownership until reading or final error handling is finished.
	SendRaw(context.Context, string, []byte, bool, string, string, string) (*http.Response, error)
	// GrokDecision applies provider health and returns generic, failover, and
	// same-provider retry choices.
	GrokDecision(context.Context, *http.Response, []byte, string) RawGrokDecision
	// ObserveGrokError records vendor request identity, message, and handling kind
	// in request diagnostics.
	ObserveGrokError(*http.Response, string, string)
	// GrokRetry resolves same-provider delay, deadline, and attempt-count guidance
	// from failure evidence.
	GrokRetry(int, []byte) RawGrokRetry
	// GrokFailover packages failure evidence and retry guidance for the provider
	// loop. The installed adapter clones headers and permits reuse when either
	// health or retry metadata allows it, while request-local transience requires
	// a metadata-eligible 429.
	GrokFailover(*http.Response, []byte, RawGrokRetry, bool) error
	// ChatErrorResponse applies shared Chat failure rules and converts any
	// returned observation. The execution caller retains response-body cleanup and
	// honors returned recovery guidance before another provider attempt.
	ChatErrorResponse(*http.Response, string) (*Result, error)
	// UpdateGrokUsage observes successful response headers and status for Grok
	// provider health.
	UpdateGrokUsage(context.Context, string, http.Header, int)
}

// RawGrokDecision contains Grok health handling's output and retry decisions.
type RawGrokDecision struct {
	// Failover requests provider switching; Generic requests final error output;
	// RetrySame permits this provider's reuse.
	Failover, Generic, RetrySame bool
}

// RawGrokRetry contains the budget and timing for recovery on the same provider.
type RawGrokRetry struct {
	Retryable bool          // response permits provider reuse
	Delay     time.Duration // minimum retry wait
	Deadline  time.Time     // latest allowed retry time
	Max       int           // maximum provider retry count
}
