package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// opencode_test_target_test.go exercises OpenCode probe selection and captures
// the resulting native HTTP requests. Protocol replies, model aliases, scoped
// attributes, caller identity, and failure paths are checked through the target
// so validation and actual outbound preparation use the same model.

// openCodeProbeSink records probe events and can fail a sink write.
type openCodeProbeSink struct {
	events []provider.TestEvent // emitted probe observations
	err    error                // scripted event emission failure
}

// openCodeProbeTransport captures prepared native requests and supplies protocol replies.
type openCodeProbeTransport struct {
	request  *http.Request // captured outbound request pointer
	body     []byte        // consumed request JSON bytes
	response string        // scripted protocol response body
	// status selects the scripted HTTP response code. Zero uses success for the
	// protocol fixtures that focus on payloads and stream parsing.
	status int
	calls  int  // observed transport invocation count
	closed bool // response body close observation
	// send optionally replaces response construction to inspect request cancellation.
	// A successful callback supplies a response whose body the executor closes.
	send func(*http.Request) (*http.Response, error)
}

// openCodeProbeStore records provider error-state writes from failed probes.
type openCodeProbeStore struct {
	ids      []int64  // provider identifiers passed to SetError
	messages []string // error-state messages passed to SetError
}

// openCodeProbeBody records cleanup of an in-memory protocol response body.
type openCodeProbeBody struct {
	io.Reader                         // supplied protocol response reader
	owner     *openCodeProbeTransport // response cleanup observation owner
}

// Begin accepts probe preparation for this in-memory event recorder.
func (*openCodeProbeSink) Begin(context.Context, bool) error { return nil }

// SetError retains one provider error-state write and reports success.
func (s *openCodeProbeStore) SetError(_ context.Context, id int64, message string) error {
	s.ids = append(s.ids, id)
	s.messages = append(s.messages, message)
	return nil
}

// Emit retains one probe event for subsequent sequential assertions.
func (s *openCodeProbeSink) Emit(_ context.Context, event provider.TestEvent) error {
	s.events = append(s.events, event)
	return s.err
}

// TestOpenCodeProbeRejectsUnknownAndConflictingRoutes exercises preparation with
// unavailable native executors. Every invalid request produces an error event
// before any executor is invoked or credentials could be sent upstream.
func TestOpenCodeProbeRejectsUnknownAndConflictingRoutes(t *testing.T) {
	record := &provider.Record{Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test", "opencode_model_protocols": map[string]any{"payg": map[string]any{"custom": "openai_chat_completions"}}}}
	target := openCodeTestTarget{record: record}
	for _, request := range []provider.PreparedTestRequest{
		{TestRequest: provider.TestRequest{Model: "unknown"}},
		{TestRequest: provider.TestRequest{Model: "custom", Protocol: "anthropic"}},
		{TestRequest: provider.TestRequest{Model: "custom"}, TestType: provider.ProviderTestTypeDecision},
		{TestRequest: provider.TestRequest{Model: "custom", ProtocolRoute: provider.TestProtocolRoute{ClientProtocol: protocol.ProtocolAnthropicMessages, UpstreamProtocol: protocol.ProtocolAnthropicMessages}}},
	} {
		sink := &openCodeProbeSink{}
		require.Error(t, target.Execute(t.Context(), request, sink))
		require.Len(t, sink.events, 1)
		require.Equal(t, "error", sink.events[0].Type)
	}
}

// TestOpenCodeProbeGeminiWildcardKeepsValidatedModel reproduces a standalone
// wildcard alias whose target declares Gemini. The URL must use that target
// selected during validation, including for probes without a group route.
func TestOpenCodeProbeGeminiWildcardKeepsValidatedModel(t *testing.T) {
	transport := &openCodeProbeTransport{response: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}\n\n"}
	record := &provider.Record{ID: 9, Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "model_mapping": map[string]any{"alias-*": "gemini-2.5-flash"},
		"opencode_model_protocols": map[string]any{"payg": map[string]any{"gemini-2.5-flash": "gemini_generate_content"}},
	}}
	target := openCodeTestTarget{record: record, executor: &NativeProtocolTest{Transport: transport, ValidateURL: func(raw string) (string, error) { return raw, nil }}}
	require.NoError(t, target.Execute(t.Context(), provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "alias-fast"}, TestType: provider.ProviderTestTypeText}, &openCodeProbeSink{}))
	require.Equal(t, "/inference/google/v1beta/models/gemini-2.5-flash:streamGenerateContent", transport.request.URL.Path)
}

// Close marks response cleanup for the transport's sequential test assertions.
func (b *openCodeProbeBody) Close() error {
	b.owner.closed = true
	return nil
}

// DoWithTLS captures one request and returns its scripted protocol reply.
// A supplied send callback can observe cancellation. Otherwise the caller owns
// the returned body and closing it records cleanup in this fixture.
func (f *openCodeProbeTransport) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	f.calls++
	f.request = req
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	f.body = body
	if f.send != nil {
		return f.send(req)
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: &openCodeProbeBody{Reader: strings.NewReader(f.response), owner: f}}, nil
}

// TestOpenCodeProbeNativeProtocolsKeepOneMappedIdentity exercises all five
// protocols with exact aliases, wildcard aliases, and chained mapping rules.
// Standalone and converted group probes must send the first mapped model,
// consult attributes for that route, and emit one start and completion.
func TestOpenCodeProbeNativeProtocolsKeepOneMappedIdentity(t *testing.T) {
	for _, wire := range []struct {
		name     string              // readable protocol case name
		id       protocol.ProtocolID // selected native wire protocol
		model    string              // final first-hop model name
		path     string              // expected outbound URL path
		response string              // successful native response bytes
	}{
		{"chat", protocol.ProtocolOpenAIChatCompletions, "model-b", "/inference/openai/v1/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"messages", protocol.ProtocolAnthropicMessages, "model-b", "/inference/anthropic/v1/messages", "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"responses", protocol.ProtocolOpenAIResponses, "model-b", "/inference/openai/v1/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"},
		{"gemini", protocol.ProtocolGeminiGenerateContent, "model-b", "/inference/google/v1beta/models/model-b:streamGenerateContent", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}\n\n"},
		{"systemone", protocol.ProtocolSystemOne, "model-b", "/inference/systemone/v1/systemone", `{"model":"model-b-version","answers":{"available":{"type":"noul","noul":1}},"usage":{"input_tokens":11,"output_tokens":2}}`},
	} {
		for _, alias := range []struct {
			name    string         // readable mapping case name
			mapping map[string]any // saved provider alias rules
		}{
			{"exact", map[string]any{"alias-fast": wire.model}},
			{"wildcard", map[string]any{"alias-*": wire.model}},
			{"single-hop", map[string]any{"alias-fast": wire.model, wire.model: "model-c"}},
		} {
			for _, routed := range []bool{false, true} {
				name := wire.name + "/" + alias.name + "/standalone"
				if routed {
					name = wire.name + "/" + alias.name + "/group"
				}
				t.Run(name, func(t *testing.T) {
					transport := &openCodeProbeTransport{response: wire.response}
					record := &provider.Record{ID: 8, Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
						"api_key": " fixture-key \n", "model_mapping": alias.mapping,
						"header_override_enabled":  true,
						"opencode_model_protocols": map[string]any{"payg": map[string]any{wire.model: string(wire.id), "model-c": "systemone"}},
						"header_overrides":         map[string]any{"User-Agent": "configured-agent", "X-OpenCode-Session": "configured-session"},
					}}
					entry := wire.id
					if routed && wire.id != protocol.ProtocolSystemOne {
						entry = protocol.ProtocolOpenAIChatCompletions
					}
					lookups := 0
					executor := &NativeProtocolTest{Transport: transport, ValidateURL: func(raw string) (string, error) { return raw, nil }, Attributes: func(ctx context.Context, _ *provider.Record, group int64, model, endpoint string) (modelrules.Resolution, error) {
						lookups++
						require.Equal(t, int64(17), group)
						require.Equal(t, wire.model, model)
						require.Equal(t, string(wire.id), endpoint)
						require.Equal(t, entry, provider.TestProtocolRouteFromContext(ctx).ClientProtocol)
						disabled := false
						return modelrules.Resolution{Attributes: modelcatalog.Attributes{Temperature: &disabled}}, nil
					}}
					request := provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "alias-fast", Prompt: "hello", GroupID: 17, UserAgent: "caller-agent", Automatic: true}, TestType: provider.ProviderTestTypeText}
					if routed {
						request.ProtocolRoute = provider.TestProtocolRoute{ClientProtocol: entry, UpstreamProtocol: wire.id}
					}
					if wire.id == protocol.ProtocolSystemOne {
						request.TestType = provider.ProviderTestTypeDecision
					}
					sink := &openCodeProbeSink{}
					target := openCodeTestTarget{record: record, executor: executor}
					require.NoError(t, target.Execute(t.Context(), request, sink))
					require.Equal(t, 1, transport.calls)
					require.True(t, transport.closed)
					require.Equal(t, wire.path, transport.request.URL.Path)
					require.Equal(t, "Bearer fixture-key", transport.request.Header.Get("Authorization"))
					require.Empty(t, transport.request.Header.Get("x-api-key"))
					require.Empty(t, transport.request.Header.Get("x-goog-api-key"))
					require.Equal(t, "caller-agent", transport.request.Header.Get("User-Agent"))
					require.Subset(t, transport.request.Header, http.Header{"x-opencode-session": []string{"configured-session"}})
					require.Positive(t, lookups)
					var payload map[string]any
					require.NoError(t, json.Unmarshal(transport.body, &payload))
					if wire.id != protocol.ProtocolGeminiGenerateContent {
						require.Equal(t, wire.model, payload["model"])
					}
					require.NotContains(t, payload, "temperature")
					if wire.id == protocol.ProtocolOpenAIResponses {
						require.NotContains(t, payload, "store")
						require.NotContains(t, payload, "instructions")
					}
					wantEvents := []string{"test_start", "content", "test_complete"}
					if wire.id == protocol.ProtocolOpenAIChatCompletions {
						wantEvents = []string{"test_start", "content", "status", "test_complete"}
					}
					eventTypes := make([]string, len(sink.events))
					for i, event := range sink.events {
						eventTypes[i] = event.Type
					}
					require.Equal(t, wantEvents, eventTypes)
					require.Equal(t, wire.model, sink.events[0].Model)
					require.True(t, sink.events[len(sink.events)-1].Success)
				})
			}
		}
	}
}

// TestOpenCodeSystemOneImages retains explicit image opt-in alongside OpenCode
// authentication and model selection. The shared decision executor forwards the
// inline image and accepts a Box answer whose confidence is explicitly unknown.
func TestOpenCodeSystemOneImages(t *testing.T) {
	transport := &openCodeProbeTransport{response: `{"model":"jev-1.14.0","answers":{"target":{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":null}},"usage":{"input_tokens":2,"output_tokens":0}}`}
	record := &provider.Record{ID: 8, Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "opencode_model_protocols": map[string]any{"payg": map[string]any{"jev-latest": "systemone"}},
	}}
	executor := &NativeProtocolTest{Transport: transport, ValidateURL: func(raw string) (string, error) { return raw, nil }}
	target := openCodeTestTarget{record: record, executor: executor}
	request := provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "jev-latest", SystemOneImages: "true", SystemOne: json.RawMessage(`{"state":"photo","questions":{"target":{"type":"box"}},"assets":{"photo":{"format":"png","data_base64":"aW1hZ2U="}}}`)}, TestType: provider.ProviderTestTypeDecision}
	sink := &openCodeProbeSink{}
	require.NoError(t, target.Execute(t.Context(), request, sink))
	require.Equal(t, "/inference/systemone/v1/systemone", transport.request.URL.Path)
	require.Equal(t, "Bearer test-key", transport.request.Header.Get("Authorization"))
	require.Equal(t, "true", transport.request.Header.Get("TypeSafe-Danger-Zone-Images"))
	require.Contains(t, string(transport.body), `"data_base64":"aW1hZ2U="`)
	require.True(t, sink.events[len(sink.events)-1].Success)
}

// TestOpenCodeProbeFailureAndCancellationKeepsOneRun checks an upstream rejection,
// a cancelled caller, and a failed event sink. Each failure must stop the probe,
// with no successful completion or extra upstream attempt.
func TestOpenCodeProbeFailureAndCancellationKeepsOneRun(t *testing.T) {
	for _, mode := range []string{"upstream-error", "cancelled", "during-send", "sink-error"} {
		t.Run(mode, func(t *testing.T) {
			transport := &openCodeProbeTransport{status: http.StatusTooManyRequests, response: "limited"}
			record := &provider.Record{Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
				"api_key": "key", "opencode_model_protocols": map[string]any{"payg": map[string]any{"probe": "gemini_generate_content"}},
			}}
			target := openCodeTestTarget{record: record, executor: &NativeProtocolTest{Transport: transport, ValidateURL: func(raw string) (string, error) { return raw, nil }}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sink := &openCodeProbeSink{}
			if mode == "cancelled" {
				cancel()
			}
			if mode == "during-send" {
				transport.send = func(req *http.Request) (*http.Response, error) {
					// Cancel after dispatch to verify that the same run context reaches
					// transport and its cancellation survives the returned diagnostic.
					cancel()
					return nil, req.Context().Err()
				}
			}
			if mode == "sink-error" {
				sink.err = errors.New("closed sink")
			}
			err := target.Execute(ctx, provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "probe"}, TestType: provider.ProviderTestTypeText}, sink)
			require.Error(t, err)
			switch mode {
			case "upstream-error":
				require.Equal(t, 1, transport.calls)
				require.True(t, transport.closed)
				require.Contains(t, err.Error(), "429: limited")
			case "cancelled":
				require.ErrorIs(t, err, context.Canceled)
				require.Zero(t, transport.calls)
			case "during-send":
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, 1, transport.calls)
			case "sink-error":
				require.ErrorIs(t, err, sink.err)
				require.Zero(t, transport.calls)
			}
			for _, event := range sink.events {
				require.NotEqual(t, "test_complete", event.Type)
			}
		})
	}
}

// TestOpenCodeProbePreparationRejectsDisabledAndFailedAttributes supplies a
// disabled endpoint or a failing group profile. Both failures must terminate
// preparation before the configured transport receives any request.
func TestOpenCodeProbePreparationRejectsDisabledAndFailedAttributes(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		transport := &openCodeProbeTransport{}
		record := &provider.Record{Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
			"api_key": "key", "opencode_model_protocols": map[string]any{"payg": map[string]any{"probe": "openai_chat_completions"}},
		}}
		if disabled {
			record.Credentials[provider.UpstreamProtocolsKey] = []string{}
		}
		executor := &NativeProtocolTest{Transport: transport, ValidateURL: func(raw string) (string, error) { return raw, nil }, Attributes: func(context.Context, *provider.Record, int64, string, string) (modelrules.Resolution, error) {
			return modelrules.Resolution{}, errors.New("profile unavailable")
		}}
		target := openCodeTestTarget{record: record, executor: executor}
		err := target.Execute(t.Context(), provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "probe"}, TestType: provider.ProviderTestTypeText}, &openCodeProbeSink{})
		require.Error(t, err)
		require.Zero(t, transport.calls)
		if disabled {
			require.Contains(t, err.Error(), "no enabled protocol")
		} else {
			require.Contains(t, err.Error(), "profile unavailable")
		}
	}
}

// TestOpenCodeProbeUserAgentDefaultsDoesNotInventSession compares caller,
// configured, and default User-Agent selection. Each probe sends one identity
// while a missing conversation identifier leaves the session header absent.
func TestOpenCodeProbeUserAgentDefaultsDoesNotInventSession(t *testing.T) {
	for _, test := range []struct {
		caller     string // caller supplied agent value
		configured string // saved provider agent override
		want       string // expected final agent value
	}{
		{"caller", "configured", "caller"},
		{"", "configured", "configured"},
		{"", "", "TokenRouter"},
	} {
		transport := &openCodeProbeTransport{response: "data: {\"type\":\"response.completed\"}\n\n"}
		record := &provider.Record{Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
			"api_key": "key", "header_override_enabled": true, "header_overrides": map[string]any{"User-Agent": test.configured},
			"opencode_model_protocols": map[string]any{"payg": map[string]any{"probe": "openai_responses"}},
		}}
		target := openCodeTestTarget{record: record, executor: &NativeProtocolTest{Transport: transport, ValidateURL: func(raw string) (string, error) { return raw, nil }}}
		err := target.Execute(t.Context(), provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "probe", UserAgent: test.caller}, TestType: provider.ProviderTestTypeText}, &openCodeProbeSink{})
		require.NoError(t, err)
		require.Equal(t, test.want, transport.request.Header.Get("User-Agent"))
		for name := range transport.request.Header {
			require.False(t, strings.EqualFold(name, "x-opencode-session"))
		}
	}
}

// TestOpenCodeProbeUnauthorizedRecordsProviderError replies 401 and 429 to
// streaming probes. A rejected credential writes the provider's error state
// with the upstream diagnostic, while a rate limit leaves provider state alone.
func TestOpenCodeProbeUnauthorizedRecordsProviderError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			transport := &openCodeProbeTransport{status: status, response: "rejected"}
			store := &openCodeProbeStore{}
			record := &provider.Record{ID: 31, Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
				"api_key": "key", "opencode_model_protocols": map[string]any{"payg": map[string]any{"probe": "anthropic_messages"}},
			}}
			target := openCodeTestTarget{record: record, executor: &NativeProtocolTest{Transport: transport, Store: store, ValidateURL: func(raw string) (string, error) { return raw, nil }}}
			err := target.Execute(t.Context(), provider.PreparedTestRequest{TestRequest: provider.TestRequest{Model: "probe"}, TestType: provider.ProviderTestTypeText}, &openCodeProbeSink{})
			require.Error(t, err)
			want := fmt.Sprintf("Native probe returned %d: rejected", status)
			require.EqualError(t, err, want)
			if status == http.StatusUnauthorized {
				require.Equal(t, []int64{31}, store.ids)
				require.Equal(t, []string{want}, store.messages)
			} else {
				require.Empty(t, store.ids)
			}
		})
	}
}
