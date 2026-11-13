package googleforward

import (
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// gemini_response.go binds Gemini response readers and tool replay to the
// selected attempt.
// Gemini response callbacks connect native readers and compatibility converters
// to the same request output. Bounded body reading and header filtering remain
// HTTP operations, while the attempt receives image observations and restores
// tool names during synchronous response adaptation.
//
// A compatible response exposes a client tool ID whose original Gemini fields
// are stored by the gateway. Request conversion looks up that ID using the
// same API Key, provider, and mapped model. This lets both codecs remain
// independent of session storage and keeps the opaque signature out of
// client history sent to other upstream protocols.

// geminiResponseAdapter binds response reading to this attempt's image,
// tool, and HTTP output state. The replay scope belongs to the selected
// Key and mapped upstream, so another destination cannot load its signatures.
// Native responses do not use the compatibility tool callback.
func (s *Gemini) geminiResponseAdapter(c *attempt, scope session.GeminiToolScope) *gemininative.ResponseAdapter {
	options := gemininative.ResponseOptions{
		Context:     c.RequestContext(),
		Stream:      upstream.GeminiStreamOptions{IdleTimeout: time.Duration(s.Options.StreamInterval) * time.Second, MaxFrameBytes: s.Options.MaxLineSize},
		GoogleError: func(status int, message string) error { return c.GoogleError(status, message) },

		ReadBody: func(body io.Reader) ([]byte, error) {
			return c.ReadBody(body, s.Options.ResponseReadLimit)
		},
		WriteHeaders:    func(dst, src http.Header) { c.WriteHeaders(dst, src, s.HeaderFilter) },
		DebugHeaders:    s.Options.Configured && s.Options.DebugHeaders,
		HasHeaderFilter: s.HeaderFilter != nil,

		ObserveImages: func(body []byte) { c.observeImages(body) },
		ReverseTools:  func(body []byte) []byte { return c.reverseTools(body) },
		ClaudeError:   func(status int, kind, message string) error { return c.ClaudeError(status, kind, message) },
		ChatError: func(status int, kind, message string) error {
			return c.ChatError(status, kind, message)
		},
		CompatError: func(protocol gemininative.OpenAICompatProtocol, status int, kind, message string) error {
			return c.GeminiOpenAICompatError(protocol, status, kind, message)
		},
	}
	if scope.APIKeyID > 0 {
		options.RememberToolCall = func(clientID string, call bridge.NativeGeminiToolCall) {
			s.toolCalls.Remember(scope, clientID, call.ID, call.Signature)
		}
	}
	return &gemininative.ResponseAdapter{Options: options}
}

// geminiToolOptions configures request conversion for the selected destination.
// Without a scoped hit, the converter retains its existing compatibility
// behavior for historical tool calls and calls from other providers.
func (s *Gemini) geminiToolOptions(scope session.GeminiToolScope) bridge.NativeGeminiOptions {
	options := bridge.NativeGeminiOptions{DummyThoughtSignature: geminiDummyThoughtSignature}
	if scope.APIKeyID > 0 {
		options.LookupToolCall = func(clientID string) (bridge.NativeGeminiToolCall, bool) {
			id, signature, ok := s.toolCalls.Lookup(scope, clientID)
			return bridge.NativeGeminiToolCall{ID: id, Signature: signature}, ok
		}
	}
	return options
}
