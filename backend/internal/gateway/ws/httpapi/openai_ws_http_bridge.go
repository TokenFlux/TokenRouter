package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_ws_http_bridge.go executes Responses WebSocket turns over HTTP and
// converts upstream SSE data into client events. HTTP conversion removes the
// continuation anchor after ingress rebuilds input, and function-tool
// adaptation retains declarations and name mappings for later turns that omit
// tools. Local generate=false warmups publish matching created and completed
// IDs while saving that tool state for real execution.
//
// First-turn OpenAI progress is staged until client output or a terminal
// commits the attempt. Provider policy executes before capacity-code
// presentation, and a standalone OpenAI error waits for a matching terminal or
// end-of-stream synthesis. Separate replay collections retain tool context for
// local continuation and all output for provider replacement. Recognized client
// disconnect stops writes while the reader collects available usage until a
// terminal, read failure or turn cancellation; results and errors describe both
// observation and delivery. HTTP transport owns each dispatch ordinal; the bridge
// attaches that ordinal to its performance and result instead of registering a
// second socket attempt for the same upstream exchange. Internal HTTP retries
// start fresh performance observations, while total turn duration retains the
// original turn start for lifecycle reporting.

// These byte limits bound client frames, select first-turn HTTP conversion, and
// restrict error-body diagnostics; group conversion permission is checked separately.
const (
	openAIWSClientReadLimitBytesDefault     int64 = 64 * 1024 * 1024 // default client frame byte limit
	openAIWSHTTPBridgeThresholdBytesDefault int64 = 15 * 1024 * 1024 // default first-frame bridge byte threshold
	openAIWSHTTPBridgeErrorBodyLimitBytes         = 64 * 1024        // maximum captured HTTP error bytes
)

// openAIWSToolCallReplayCollector retains completed output for continuation and
// provider replacement. Callers serialize updates for one turn; Items and AllItems
// expose new slice storage whose allocated item bodies remain shared and immutable.
type openAIWSToolCallReplayCollector struct {
	// items and seen track protocol tool-call context, while allItems and allSeen
	// track every typed output object. Each collection deduplicates independently
	// by ID, then call_id, then raw bytes, preserving first-seen item order.
	items    []json.RawMessage   // retained tool call context items
	seen     map[string]struct{} // tool context deduplication keys
	allItems []json.RawMessage   // retained typed upstream output items
	allSeen  map[string]struct{} // full output deduplication keys
}

// decodeOpenAIWSHTTPBridgeLoweredTools decodes saved lowered declarations into
// an optional tool list. Empty or malformed bytes return nil, so tool
// adaptation continues without inherited declarations when saved data cannot
// be decoded.
func decodeOpenAIWSHTTPBridgeLoweredTools(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return nil
	}
	var tools []any
	// Unusable saved declarations become an absent inherited list, leaving current
	// tool adaptation to validate any declarations actually supplied by this frame.
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	return tools
}

// openAIWSHTTPBridgeRawField returns copied bytes and presence for a top-level
// JSON field. Invalid object decoding returns false; a present null field
// remains present, which lets tool adaptation distinguish explicit declaration
// input from omission.
func openAIWSHTTPBridgeRawField(body []byte, name string) (json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	// Presence cannot be trusted after object decoding fails; report no field so
	// callers do not treat partial data as an explicit replacement declaration.
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, false
	}
	raw, present := fields[name]
	return append(json.RawMessage(nil), raw...), present
}

// openAIWSHTTPBridgeToolUpstreamName returns the platform label used by
// client-tool conversion diagnostics.
func openAIWSHTTPBridgeToolUpstreamName(provider *gatewayprovider.ExecutionProvider) string {
	if provider != nil && provider.Record.Platform == capability.PlatformGrok {
		return "Grok WS HTTP bridge"
	}
	return "OpenAI WS HTTP bridge"
}

// ResolveOpenAIWSClientFirstMessageTimeout returns the initial client read
// budget from positive configured seconds. A nil configuration or nonpositive
// value selects thirty seconds; callers pass the duration to the first-frame
// read controller.
func ResolveOpenAIWSClientFirstMessageTimeout(cfg *OpenAIWSOptions) time.Duration {
	seconds := 30
	if cfg != nil && cfg.ClientFirstMessageTimeoutSeconds > 0 {
		seconds = cfg.ClientFirstMessageTimeoutSeconds
	}
	// Convert configured seconds for the first-frame context timer.
	return time.Duration(seconds) * time.Second
}

// ResolveOpenAIWSClientReadLimitBytes returns the configured positive client
// frame byte limit. Nil configuration or a nonpositive value selects 64 MiB,
// providing the entry adapter's bounded read default before upgrade.
func ResolveOpenAIWSClientReadLimitBytes(cfg *OpenAIWSOptions) int64 {
	if cfg == nil || cfg.ClientReadLimitBytes <= 0 {
		return openAIWSClientReadLimitBytesDefault
	}
	return cfg.ClientReadLimitBytes
}

// openAIWSHTTPBridgeEnabled reports whether an executor is present for
// size-based bridge selection. It does not check HTTP capability or group
// permission; execution separately resolves those before choosing the bridge.
func (s *OpenAIWebSocketExecutor) openAIWSHTTPBridgeEnabled() bool {
	return s != nil
}

// openAIWSHTTPBridgeThresholdBytes returns the inclusive payload threshold for
// size-based HTTP conversion. A nil executor, nil Options, or nonpositive
// configured bytes selects 15 MiB; the group still needs an allowed HTTP
// route.
func (s *OpenAIWebSocketExecutor) openAIWSHTTPBridgeThresholdBytes() int64 {
	if s == nil || s.Options == nil || s.Options.HTTPBridgeThresholdBytes <= 0 {
		return openAIWSHTTPBridgeThresholdBytesDefault
	}
	return s.Options.HTTPBridgeThresholdBytes
}

// shouldBridgeOpenAIWSHTTP selects Grok bridging or oversized unanchored
// first-turn conversion. Grok returns true directly; other platforms need an
// executor, an empty trimmed previous_response_id and payload size at least
// the threshold. Group permission is checked by the caller.
func (s *OpenAIWebSocketExecutor) shouldBridgeOpenAIWSHTTP(provider *gatewayprovider.ExecutionProvider, payloadBytes int, previousResponseID string) bool {
	if provider != nil && provider.Record.Platform == capability.PlatformGrok {
		return true
	}
	if !s.openAIWSHTTPBridgeEnabled() {
		return false
	}
	if strings.TrimSpace(previousResponseID) != "" {
		return false
	}
	threshold := s.openAIWSHTTPBridgeThresholdBytes()
	return threshold > 0 && int64(payloadBytes) >= threshold
}

// shouldBridgeOpenAIWSPassthroughFirstMessage selects an oversized
// response.create for initial HTTP conversion before a dedicated relay starts.
// Grok returns true directly. Other providers require a present executor,
// threshold-sized payload, complete valid JSON and a root object; missing,
// null or blank type selects response.create.
//
// The scanner examines only root type and previous_response_id, skipping
// nested values without decoding the large input body. Duplicate relevant keys
// or nonstring values reject conversion, while a nonblank continuation
// prevents changing transport. Bounded key decoding retains ordinary escaped
// field names and limits allocation for unrelated large keys; callers still
// enforce group and upstream HTTP capability.
func (s *OpenAIWebSocketExecutor) shouldBridgeOpenAIWSPassthroughFirstMessage(provider *gatewayprovider.ExecutionProvider, payload []byte) bool {
	if provider != nil && provider.Record.Platform == capability.PlatformGrok {
		return true
	}
	if !s.openAIWSHTTPBridgeEnabled() || int64(len(payload)) < s.openAIWSHTTPBridgeThresholdBytes() {
		return false
	}
	if !json.Valid(payload) {
		return false
	}

	i := skipOpenAIWSJSONSpace(payload, 0)
	if i >= len(payload) || payload[i] != '{' {
		return false
	}
	i++
	eventType := "response.create"
	previousResponseID := ""
	typeSeen, previousResponseIDSeen := false, false
	for {
		i = skipOpenAIWSJSONSpace(payload, i)
		if payload[i] == '}' {
			break
		}
		keyStart := i
		keyEnd := scanOpenAIWSJSONString(payload, keyStart)
		i = skipOpenAIWSJSONSpace(payload, keyEnd)
		i++ // json.Valid guarantees a colon here.
		i = skipOpenAIWSJSONSpace(payload, i)
		valueStart := i
		i = skipOpenAIWSJSONValue(payload, i)

		key := ""
		// Relevant names fit inside this encoded-key bound even with JSON escapes;
		// skip larger unrelated names so a huge frame does not allocate their strings.
		if keyEnd-keyStart <= 128 {
			_ = json.Unmarshal(payload[keyStart:keyEnd], &key)
		}
		switch key {
		case "type":
			if typeSeen {
				return false
			}
			typeSeen = true
			var value *string
			if err := json.Unmarshal(payload[valueStart:i], &value); err != nil {
				return false
			}
			if value == nil || strings.TrimSpace(*value) == "" {
				eventType = "response.create"
			} else {
				eventType = strings.TrimSpace(*value)
			}
		case "previous_response_id":
			if previousResponseIDSeen {
				return false
			}
			previousResponseIDSeen = true
			var value *string
			if err := json.Unmarshal(payload[valueStart:i], &value); err != nil {
				return false
			}
			if value != nil {
				previousResponseID = strings.TrimSpace(*value)
			}
		}
		i = skipOpenAIWSJSONSpace(payload, i)
		if payload[i] == ',' {
			i++
		}
	}
	return eventType == "response.create" && previousResponseID == ""
}

// skipOpenAIWSJSONSpace advances to the next non-JSON-space byte or payload
// end. Its index is supplied by the validated first-frame scanner, which
// checks object shape before reading delimiters at the returned position.
func skipOpenAIWSJSONSpace(payload []byte, i int) int {
	for i < len(payload) {
		switch payload[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// scanOpenAIWSJSONString returns the byte after a quoted JSON string, skipping
// escaped bytes. Callers supply an opening-quote index inside complete
// validated JSON; unmatched input reaches payload end rather than returning a
// parsing error.
func scanOpenAIWSJSONString(payload []byte, i int) int {
	for i++; i < len(payload); i++ {
		switch payload[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(payload)
}

// skipOpenAIWSJSONValue advances past one value in the validated object
// scanner. Callers supply an in-range start after whitespace; strings skip
// escapes, objects and arrays track nesting, and scalar values end at a comma
// or closing root-object brace.
func skipOpenAIWSJSONValue(payload []byte, i int) int {
	if payload[i] == '"' {
		return scanOpenAIWSJSONString(payload, i)
	}
	if payload[i] != '{' && payload[i] != '[' {
		for i < len(payload) && payload[i] != ',' && payload[i] != '}' {
			i++
		}
		return i
	}
	depth := 0
	for ; i < len(payload); i++ {
		switch payload[i] {
		case '"':
			i = scanOpenAIWSJSONString(payload, i) - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(payload)
}

// prepareOpenAIWSHTTPBridgeBody converts a root response.create object into
// streaming HTTP Responses JSON. DecodeUseNumber retains numeric precision;
// null or nonobject input fails, transport fields type and generate are
// removed, and previous_response_id is removed after ingress has prepared
// replay input. Provider is unused by this conversion.
func prepareOpenAIWSHTTPBridgeBody(provider *gatewayprovider.ExecutionProvider, payload []byte) ([]byte, error) {
	var body map[string]any
	if err := wirejson.DecodeUseNumber(payload, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("response.create payload must be a JSON object")
	}
	// Ingress supplies reconstructed input, and the caller retains the WS payload
	// for warmup detection; remove transport controls and its anchor for streaming HTTP.
	delete(body, "type")
	delete(body, "generate")
	delete(body, "previous_response_id")
	body["stream"] = true
	return json.Marshal(body)
}

// AddEvent observes completed output objects and populates both replay
// collections for one turn. It accepts response.output_item.done and completed
// or done response output arrays; other events leave collections unchanged.
// Callers serialize observation and keep returned item bodies immutable.
func (c *openAIWSToolCallReplayCollector) AddEvent(eventType string, message []byte) {
	switch strings.TrimSpace(eventType) {
	case "response.output_item.done":
		item := gjson.GetBytes(message, "item")
		c.addAllItem(item)
		c.addItem(item)
	case "response.completed", "response.done":
		output := gjson.GetBytes(message, "response.output")
		if !output.IsArray() {
			return
		}
		for _, item := range output.Array() {
			c.addAllItem(item)
			c.addItem(item)
		}
	}
}

// Items returns independent slice storage for collected tool-call context
// entries. Item bodies are allocated during observation and shared with the
// collector; callers can change slice membership but keep those bytes
// immutable while replay retains them.
func (c *openAIWSToolCallReplayCollector) Items() []json.RawMessage {
	return slices.Clone(c.items)
}

// AllItems returns independent slice storage for all collected typed output
// objects used by provider replacement. Item bodies remain shared with the
// collector and are immutable; nil collection state yields a nil slice.
func (c *openAIWSToolCallReplayCollector) AllItems() []json.RawMessage {
	return slices.Clone(c.allItems)
}

// addAllItem retains the first typed JSON object for each full-output
// identity. Missing values, non-JSON values, nonobject raw text and blank item
// type are skipped. Identity prefers trimmed id, then call_id, then raw object
// text, and duplicate keys retain the first stored object.
//
// This collection includes assistant output beyond tool calls so provider
// replacement can reconstruct conversation history. The seen map and item
// append advance together in the serialized collector, preserving
// first-observation order. Converting raw text into RawMessage allocates item
// bytes retained independently of the scanned event's storage.
func (c *openAIWSToolCallReplayCollector) addAllItem(item gjson.Result) {
	if !item.Exists() || item.Type != gjson.JSON {
		return
	}
	raw := strings.TrimSpace(item.Raw)
	if raw == "" || !strings.HasPrefix(raw, "{") || strings.TrimSpace(item.Get("type").String()) == "" {
		return
	}
	// Prefer available wire IDs over raw text for deduplication, so repeated
	// terminal observations retain the first stored representation of the same item.
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("call_id").String())
	}
	if key == "" {
		key = raw
	}
	if c.allSeen == nil {
		c.allSeen = make(map[string]struct{})
	}
	if _, ok := c.allSeen[key]; ok {
		return
	}
	c.allSeen[key] = struct{}{}
	c.allItems = append(c.allItems, json.RawMessage(raw))
}

// addItem retains the first protocol tool-call context object for each
// identity. It accepts JSON objects whose type is a Codex tool-call context
// and skips other output. Identity prefers trimmed id, then call_id, then raw
// text, and repeated identity leaves the previously stored item in place.
//
// Tool-context deduplication is separate from full-output replay so a later
// local continuation can pair tool outputs with their calls while provider
// replacement also retains assistant responses. The seen update and append
// occur together in the caller-serialized collector, with new RawMessage bytes
// allocated from the raw text.
func (c *openAIWSToolCallReplayCollector) addItem(item gjson.Result) {
	if !item.Exists() || item.Type != gjson.JSON {
		return
	}
	raw := strings.TrimSpace(item.Raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return
	}
	if !openai.IsCodexToolCallContextItemType(item.Get("type").String()) {
		return
	}
	// Prefer available wire IDs over raw text for deduplication, so repeated
	// terminal observations retain the first stored representation of the same item.
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("call_id").String())
	}
	if key == "" {
		key = raw
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.items = append(c.items, json.RawMessage(raw))
}

// buildOpenAIWSHTTPBridgeErrorEvent returns an error event containing the
// supplied HTTP status and trimmed message. Blank messages use status text,
// then a fixed upstream failure message; an encoding failure returns a fixed
// minimal error frame.
func buildOpenAIWSHTTPBridgeErrorEvent(statusCode int, message string) []byte {
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(statusCode)
	}
	if message == "" {
		message = "upstream request failed"
	}
	event := map[string]any{
		"type":   "error",
		"status": statusCode,
		"error": map[string]any{
			"type":    "upstream_error",
			"message": message,
		},
	}
	// Encode a client protocol frame from scalar fields; retain a fixed
	// error representation if encoding unexpectedly fails so close presentation can continue.
	body, err := json.Marshal(event)
	if err != nil {
		return []byte(`{"type":"error","error":{"type":"upstream_error","message":"upstream request failed"}}`)
	}
	return body
}

// buildOpenAIWSHTTPBridgeFailedEvent creates a terminal response.failed for a
// standalone failure that never supplied one. Type and code prefer the
// top-level error before nested response.error; code defaults to
// upstream_error. The shared extractor supplies a message before the caller's
// fallback and a fixed final default.
//
// Response ID and optional trimmed model anchor the synthesized terminal to
// the current bridge turn, with an empty output array and failed status.
// Encoding failure returns fixed minimal failed bytes. Callers perform
// provider effects before presenting this synthesized terminal so protocol
// repair cannot duplicate health observations.
func buildOpenAIWSHTTPBridgeFailedEvent(responseID, model string, source []byte, fallbackMessage string) []byte {
	errorType := strings.TrimSpace(gjson.GetBytes(source, "error.type").String())
	if errorType == "" {
		errorType = strings.TrimSpace(gjson.GetBytes(source, "response.error.type").String())
	}
	code := strings.TrimSpace(gjson.GetBytes(source, "error.code").String())
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(source, "response.error.code").String())
	}
	if code == "" {
		code = "upstream_error"
	}
	message := upstreamopenai.ExtractOpenAISSEErrorMessage(source)
	if message == "" {
		message = strings.TrimSpace(fallbackMessage)
	}
	if message == "" {
		message = "Upstream response failed"
	}
	errorBody := map[string]any{"code": code, "message": message}
	if errorType != "" {
		errorBody["type"] = errorType
	}
	response := map[string]any{
		"id": responseID, "object": "response", "status": "failed",
		"output": []any{}, "error": errorBody,
	}
	if model = strings.TrimSpace(model); model != "" {
		response["model"] = model
	}
	// A paired terminal must remain available even if encoding fails; use a
	// fixed failed event so clients can finish the current turn's error presentation.
	body, err := json.Marshal(map[string]any{"type": "response.failed", "response": response})
	if err != nil {
		return []byte(`{"type":"response.failed","response":{"status":"failed","output":[],"error":{"code":"upstream_error","message":"Upstream response failed"}}}`)
	}
	return body
}

// proxyOpenAIWSHTTPBridgeTurn executes one WS turn through HTTP and returns
// observed usage, model and replay metadata. args supplies either five strings
// (routing model, image billing model, size tier, input size and Grok identity)
// or the older four-string form without routing model, plus turn number,
// required client writer and optional TLS matches. More strings use the first
// five, unsupported argument types are ignored, and fewer than four strings
// fail.
//
// Grok billing initially resolves routingModel, while body preparation resolves
// originalModel when supplied. The prepared body's model becomes UpstreamModel
// for restoration and health; the earlier billing candidate remains in this
// bridge result until entry applies its settlement model rules.
//
// Tool declarations are adapted and saved before a local warmup returns without
// an HTTP exchange. Real requests can repair explicitly rejected fields under a
// bounded, distinct-body retry state. Initial transport failures and eligible
// policy failures can return provider failover before committed output; later
// policy failover also permits 429. Request-scoped errors bypass the
// corresponding provider policy. Request-bound failures pass applied effects
// directly to error construction. With a nil Gin context, the error builder
// evaluates terminal effects independently.
//
// Initial OpenAI progress stays buffered until output or a terminal selects
// delivery. A bare error waits for its terminal, a successful terminal clears
// it, and stream termination synthesizes response.failed if needed. Recognized
// client disconnect stops writes while reading continues for available usage
// and replay until terminal, read failure or turn cancellation. Result and
// error can coexist; callers apply the turn settlement rules to the observed
// partial result.
func (s *OpenAIWebSocketExecutor) proxyOpenAIWSHTTPBridgeTurn(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	token string,
	payload []byte,
	payloadBytes int,
	originalModel string,
	args ...any,
) (*forward.OpenAIResult, error) {
	responseModelObserver := gatewayhttp.BeginUpstreamResponseModelObservation(c)
	var routingModel, imageBillingModel, imageSizeTier, imageInputSize, grokCacheIdentity string
	var turn int
	var writeClientMessage func([]byte) error
	var routerMatch []egress.TLSFingerprintRouterMatchResult
	var stringArgs []string
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			stringArgs = append(stringArgs, value)
		case int:
			turn = value
		case func([]byte) error:
			writeClientMessage = value
		case egress.TLSFingerprintRouterMatchResult:
			routerMatch = append(routerMatch, value)
		case []egress.TLSFingerprintRouterMatchResult:
			routerMatch = append(routerMatch, value...)
		}
	}
	if len(stringArgs) >= 5 {
		routingModel, imageBillingModel, imageSizeTier, imageInputSize, grokCacheIdentity = stringArgs[0], stringArgs[1], stringArgs[2], stringArgs[3], stringArgs[4]
	} else if len(stringArgs) == 4 {
		imageBillingModel, imageSizeTier, imageInputSize, grokCacheIdentity = stringArgs[0], stringArgs[1], stringArgs[2], stringArgs[3]
	} else {
		return nil, errors.New("invalid websocket HTTP bridge arguments")
	}
	if s == nil {
		return nil, errors.New("service is nil")
	}
	if provider == nil {
		return nil, errors.New("provider is nil")
	}
	if writeClientMessage == nil {
		return nil, errors.New("client websocket writer is nil")
	}
	body, err := prepareOpenAIWSHTTPBridgeBody(provider, payload)
	if err != nil {
		return nil, fmt.Errorf("prepare http bridge body: %w", err)
	}
	grokIntentSourceBody := append([]byte(nil), body...)
	_, grokExplicitToolsField := openAIWSHTTPBridgeRawField(grokIntentSourceBody, "tools")
	grokExplicitToolIntent := provider.Record.Platform == capability.PlatformGrok && grok.HasGrokResponsesToolIntent(grokIntentSourceBody)
	var clientToolMapping bridge.ResponsesClientToolMapping
	functionToolUpstream := (provider.Record.Platform == capability.PlatformOpenAI && provider.Record.Type == capability.ProviderTypeAPIKey) || provider.Record.Platform == capability.PlatformGrok
	if functionToolUpstream {
		if provider.Record.Platform == capability.PlatformGrok {
			body, err = gatewayprovider.GrokBodyCodec().SanitizeGrokResponsesInput(body)
			if err != nil {
				return nil, fmt.Errorf("sanitize Grok WS HTTP bridge input: %w", err)
			}
		}
		inheritedState, _ := gatewayhttp.OpenAIWSHTTPBridgeToolStateFromContext(c)
		inheritedLoweredTools := decodeOpenAIWSHTTPBridgeLoweredTools(inheritedState.LoweredTools)
		body, clientToolMapping, err = bridge.AdaptResponsesClientToolsJSONWithMapping(
			body,
			openAIWSHTTPBridgeToolUpstreamName(provider),
			inheritedState.ClientMapping,
			inheritedLoweredTools,
		)
		if err != nil {
			return nil, fmt.Errorf("adapt %s client tools: %w", openAIWSHTTPBridgeToolUpstreamName(provider), err)
		}
		if provider.Record.Platform == capability.PlatformGrok && !grokExplicitToolsField && !grokExplicitToolIntent && len(inheritedLoweredTools) > 0 && grok.HasGrokResponsesToolIntent(body) {
			// Omitted declarations can inherit real client functions; include the adapted
			// declaration body when deriving Grok tool-cache routing for that continuation.
			grokIntentSourceBody = append(grokIntentSourceBody[:0], body...)
		}
		loweredTools := inheritedState.LoweredTools
		if currentTools, present := openAIWSHTTPBridgeRawField(body, "tools"); present {
			loweredTools = currentTools
		}
		gatewayhttp.SetOpenAIWSHTTPBridgeToolState(c, requeststate.WSBridgeTools{
			ClientMapping: clientToolMapping,
			LoweredTools:  loweredTools,
		})
	}
	// Retain warmup declarations and reverse mapping before returning local events,
	// so the next anchored turn can omit tools without losing its client functions.
	if openai.IsWSWarmupPayload(payload) {
		return completeHTTPBridgeWarmup(originalModel, writeClientMessage)
	}
	if s.Requests == nil || s.Requests.Transport == nil {
		return nil, errors.New("openai http upstream is nil")
	}
	responsesLite := provider.Record.Platform == capability.PlatformOpenAI && gatewayprovider.ImageIntent().IsOpenAIResponsesLiteWebSocketPayload(payload)
	if responsesLite {
		liteBody, changed, liteErr := gatewayprovider.NormalizeResponsesLiteForProvider(provider.View(), body)
		if liteErr != nil {
			return nil, fmt.Errorf("normalize http bridge Lite body: %w", liteErr)
		}
		if changed {
			body = liteBody
		}
	}
	billingModel := ""
	mappedModel := ""
	if provider.Record.Platform == capability.PlatformGrok {
		billingModel, mappedModel = resolveGrokWSModels(provider, body, routingModel)
	} else if routingModel != "" {
		billingModel = gatewayprovider.ExecutionModelPolicy(provider).Mapped(routingModel)
		mappedModel = gatewayprovider.ExecutionModelPolicy(provider).NormalizeOpenAI(billingModel)
	}
	// Track model restoration separately from billing choice; later body preparation
	// supplies the actual wire model before the response metadata rewrite is enabled.
	needModelReplace := routingModel != "" && mappedModel != "" && mappedModel != originalModel
	var mappedModelBytes []byte
	if needModelReplace {
		mappedModelBytes = []byte(mappedModel)
	}

	if provider.Record.Platform == capability.PlatformGrok {
		upstreamModel := resolveGrokWSUpstreamModel(provider, body, originalModel)
		body, err = s.prepareGrokResponses(ctx, provider, upstreamModel, body)
		if err != nil {
			return nil, err
		}
		grokMixedCacheIntentBody := append([]byte(nil), body...)
		body, err = grok.ApplyGrokResponsesCacheIdentity(body, grokIntentSourceBody, grokCacheIdentity, provider.View().IsGrokOAuth())
		if err != nil {
			return nil, fmt.Errorf("apply grok prompt cache identity: %w", err)
		}
		body, err = gatewayhttp.ApplyGrokFreeRequestToolCacheRoute(c, body, grokMixedCacheIntentBody, provider, grokCacheIdentity)
		if err != nil {
			return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", err)
		}
	}
	actualModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	// When prepared bytes omit model, retain the canonical original-model key for
	// diagnostics and health; this fallback leaves the outgoing body bytes unchanged.
	if actualModel == "" {
		actualModel = gatewayprovider.ExecutionModelPolicy(provider).CanonicalSchedulingModel(originalModel)
	}
	if actualModel != "" {
		mappedModel = actualModel
	}
	gatewayhttp.SetOpsUpstreamModel(c, actualModel)
	needModelReplace = originalModel != "" && mappedModel != "" && mappedModel != originalModel
	if needModelReplace {
		mappedModelBytes = []byte(mappedModel)
	}

	buildUpstreamRequest := func(requestBody []byte) (*http.Request, error) {
		// Detach client cancellation for the HTTP request while retaining server abort;
		// the body-close callback below still ends reads when this turn context cancels.
		upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
		defer releaseUpstreamCtx()
		var upstreamReq *http.Request
		var buildErr error
		if provider.Record.Platform == capability.PlatformGrok {
			upstreamReq, buildErr = s.Grok.BuildResponsesRequest(upstreamCtx, c, provider, requestBody, token, grokCacheIdentity, true)
		} else {
			upstreamReq, buildErr = s.Requests.BuildPassthrough(upstreamCtx, c, provider, requestBody, token, routerMatch...)
		}
		if buildErr != nil {
			return nil, buildErr
		}
		if provider.Record.Platform != capability.PlatformGrok && responsesLite {
			upstreamReq.Header.Set(media.ResponsesLiteHeader, "true")
		}
		return upstreamReq, nil
	}

	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}
	if c != nil {
		c.Set("openai_passthrough", true)
		c.Set("openai_ws_http_bridge", true)
	}

	turnStart := time.Now()
	var performance *timing.Performance
	rejectedFieldRetryState := upstreamopenai.NewOpenAIResponsesRejectedFieldRetryState(body)
	var resp *http.Response
	for {
		upstreamReq, buildErr := buildUpstreamRequest(body)
		if buildErr != nil {
			return nil, buildErr
		}
		// Rejected-field recovery dispatches another request. Start its performance
		// here so the accepted response's ordinal cannot inherit the rejected exchange's wait.
		performance = timing.NewRequestPerformance(ctx, time.Now())
		resp, err = s.Requests.Transport.DoWithTLS(upstreamReq, proxyURL, provider.Record.ID, provider.Record.Concurrency, s.Requests.TLSProfile(provider, routerMatch...))
		if err != nil {
			if turn == 1 {
				return nil, s.Requests.Failure.Handle(ctx, c, provider, err, true)
			}
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			clientError := buildOpenAIWSHTTPBridgeErrorEvent(http.StatusBadGateway, "Upstream request failed")
			if writeErr := writeClientMessage(clientError); writeErr == nil {
				markOpenAIWSClientVisibleFailure(c, "error", clientError)
			}
			return nil, fmt.Errorf("upstream http bridge request failed: %s", safeErr)
		}
		if resp.StatusCode < 400 {
			break
		}

		// Bound diagnostic capture before retry or policy selection, retaining partial
		// error bytes after read failure and closing the response before another exchange.
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, openAIWSHTTPBridgeErrorBodyLimitBytes))
		_ = resp.Body.Close()
		gatewayhttp.MarkOpenAICyberPolicyEvent(c, respBody, resp.StatusCode, nil)
		if resp.StatusCode == http.StatusBadRequest &&
			upstream.ExtractErrorCode(respBody) == gatewayhttp.OpenAIInvalidEncryptedContentReason {
			s.Lineage.MarkPayload(
				c, body, "ingress_ws_http_bridge_invalid_encrypted_lineage_mark", provider.Record.ID, turn,
			)
		}
		retryBody, retryReason, changed, retryErr := upstreamopenai.NormalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, respBody)
		if retryErr != nil {
			return nil, fmt.Errorf("normalize websocket http bridge rejected field retry: %w", retryErr)
		}
		// Retry only a recognized field repair with a previously unseen body inside
		// the shared six-repair budget; release of the rejected response already occurred.
		if changed && rejectedFieldRetryState.Allow(retryBody) {
			gatewayprovider.LogOpenAIWSModeInfo(
				"ingress_ws_http_bridge_rejected_field_retry provider_id=%d turn=%d reason=%s",
				provider.Record.ID,
				turn, gatewayprovider.TruncateOpenAIWSLogValue(retryReason, gatewayprovider.OpenAIWSLogValueMaxLen),
			)
			body = retryBody
			payloadBytes = len(body)
			continue
		}

		upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
		if upstreamMsg == "" {
			upstreamMsg = http.StatusText(resp.StatusCode)
		}
		requestScopedError := gatewayprovider.OpenAIWSHTTPBridgeRequestScopedError(provider, resp.StatusCode, upstreamMsg, respBody)
		decision := providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
		defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, respBody)
		if provider.Record.Platform == capability.PlatformGrok {
			defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, respBody)
		}
		if !requestScopedError {
			if provider.Record.Platform == capability.PlatformGrok {
				decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, provider, resp.StatusCode, resp.Header, respBody, "", mappedModel)
			} else {
				decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, resp.StatusCode, resp.Header, respBody, false, mappedModel)
			}
		}
		if decision.ShouldReturnGenericError() {
			// If generic-error delivery fails, retain the provider-policy failure as this
			// turn's returned error so entry still receives its selected failure reason.
			_ = writeClientMessage(buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error"))
			return nil, fmt.Errorf("upstream http bridge error: status=%d (not in custom error codes)", resp.StatusCode)
		}
		if !requestScopedError && decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, defaultFailover) &&
			(turn == 1 || resp.StatusCode == http.StatusTooManyRequests) {
			return nil, gatewayprovider.NewOpenAIUpstreamFailure(
				resp.StatusCode,
				resp.Header,
				respBody,
				upstreamMsg,
				decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode),
			)
		}
		clientError := buildOpenAIWSHTTPBridgeErrorEvent(resp.StatusCode, upstreamMsg)
		if writeErr := writeClientMessage(clientError); writeErr == nil {
			markOpenAIWSClientVisibleFailure(c, "error", clientError)
		}
		return nil, fmt.Errorf("upstream http bridge error: status=%d message=%s", resp.StatusCode, upstreamMsg)
	}
	// Close the accepted response on return and on turn cancellation so scanner
	// reads cannot remain attached to a canceled bridge operation.
	defer func() { _ = resp.Body.Close() }()
	stopCancelBody := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
	defer stopCancelBody()
	dispatch := telemetry.RequestAttemptFromResponse(resp)
	if dispatch.Number > 0 {
		performance.Values["attempt_number"] = int64(dispatch.Number)
	}
	if provider.Record.Platform == capability.PlatformGrok {
		s.Output.GrokHealth.ObserveResponse(ctx, provider.View(), resp.Header, resp.StatusCode, resolveGrokWSUpstreamModel(provider, body, originalModel))
	}

	responseID := ""
	usage := openai.ForwardUsage{}
	imageCounter := openai.NewOpenAIImageOutputCounter()

	reqStream := openai.WSPayloadBoolFromRaw(body, "stream", true)
	eventCount := 0
	tokenEventCount := 0
	terminalEventCount := 0
	replayCollector := &openAIWSToolCallReplayCollector{}
	firstEventType := ""
	lastEventType := ""
	upstreamTerminalEvent := ""
	sawDone := false
	wroteDownstream := false
	pendingClientMessages := make([][]byte, 0, 4)
	pendingClientMessageBytes := int64(0)
	capacityFailoverSuppressedLogged := false
	clientDisconnected := false
	officialOpenAIResponses := provider != nil && provider.Record.Platform == capability.PlatformOpenAI
	bareErrorPending := false
	var bareErrorPayload []byte
	bareErrorMessage := ""
	failureProviderSideEffectsApplied := false
	resultWithUsage := func() *forward.OpenAIResult {
		performance.FinishAt(time.Now())
		imageCount := imageCounter.Count()
		result := &forward.OpenAIResult{
			RequestAttempt:              dispatch,
			RequestID:                   responseID,
			ResponseID:                  responseID,
			Usage:                       usage,
			Model:                       originalModel,
			BillingModel:                billingModel,
			UpstreamModel:               mappedModel,
			UpstreamResponseServiceTier: responseModelObserver.ServiceTier(),
			UpstreamResponseModel:       responseModelObserver.Model(),
			ServiceTier:                 gatewayhttp.ResolvedOpenAIUpstreamServiceTierFromObserver(responseModelObserver, requeststate.ExtractOpenAIServiceTierFromBody(body)),
			ReasoningEffort:             gatewayprovider.ApplyThinkingEnabledFallback(requeststate.ExtractOpenAIReasoningEffortFromBody(body), body, mappedModel),
			RequestedReasoningEffort:    requeststate.CanonicalRequestedReasoningEffort(body),
			Stream:                      reqStream,
			OpenAIWSMode:                true,
			UpstreamTerminalEvent:       upstreamTerminalEvent,
			ResponseHeaders:             upstream.CloneHeader(resp.Header),
			Duration:                    time.Since(turnStart),
			Timings:                     performance.Clone(),
		}
		if replayInput := replayCollector.Items(); len(replayInput) > 0 {
			result.SetWSReplayInput(replayInput, true)
		}
		result.SetWSProviderFailoverReplayInput(replayCollector.AllItems())
		if imageCount > 0 {
			result.ImageCount = imageCount
			result.ImageFrameCount = imageCounter.FrameCount()
			result.ImageSize = imageSizeTier
			result.ImageInputSize = imageInputSize
			result.ImageOutputSizes = imageCounter.Sizes()
			result.BillingModel = imageBillingModel
		}
		return result
	}

	maxLineSize := gatewayhttp.OpenAIResponseDefaultMaxLineSize
	if s.Options != nil && s.Output.Options.MaxLineSize > 0 {
		maxLineSize = s.Output.Options.MaxLineSize
	}
	if gatewayhttp.HasOpenAIResponsesClientToolMapping(clientToolMapping) {
		// Restore client tool representation before replay and WS delivery, using the
		// mapping captured for this turn rather than later session tool declarations.
		resp.Body = upstream.NewResponsesClientToolStreamBody(resp.Body, clientToolMapping, maxLineSize)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanBuf := httpclient.GetSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	defer httpclient.PutSSEScannerBuf64K(scanBuf)

	pendingSSEEventType := ""
	finalizeBareError := func() error {
		if !bareErrorPending {
			return nil
		}
		// End-of-stream synthesis shares provider effects with any earlier error event;
		// retain the applied flag so paired terminal repair cannot repeat health writes.
		if !failureProviderSideEffectsApplied {
			failureProviderSideEffectsApplied = s.handleOpenAIWSFailureProviderSideEffects(ctx, provider, mappedModel, resp.Header, bareErrorPayload)
		}
		upstreamTerminalEvent = "response.failed"
		if clientDisconnected {
			return nil
		}
		clientMessage := buildOpenAIWSHTTPBridgeFailedEvent(responseID, originalModel, bareErrorPayload, bareErrorMessage)
		if rewritten, changed := upstreamopenai.SanitizeOpenAICapacityShedErrorCodeForClient(clientMessage); changed {
			clientMessage = rewritten
		}
		messages := append(pendingClientMessages, clientMessage)
		pendingClientMessages = nil
		pendingClientMessageBytes = 0
		for _, message := range messages {
			if err := writeClientMessage(message); err != nil {
				if gatewayprovider.IsOpenAIWSClientDisconnectError(err) {
					clientDisconnected = true
					timing.ObserveClientDisconnect(ctx, time.Now())
					return nil
				}
				return fmt.Errorf("write synthesized websocket response.failed: %w", err)
			}
			wroteDownstream = true
		}
		markOpenAIWSClientVisibleFailure(c, "response.failed", clientMessage)
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if eventType, ok := openai.ExtractSSEEventLine(line); ok {
			pendingSSEEventType = eventType
			continue
		}
		if strings.TrimSpace(line) == "" {
			pendingSSEEventType = ""
			continue
		}
		data, ok := openai.ExtractSSEDataLine(line)
		if !ok {
			continue
		}
		trimmedData := strings.TrimSpace(data)
		if trimmedData == "" {
			continue
		}
		if trimmedData == "[DONE]" {
			sawDone = true
			continue
		}

		// SSE may carry its label outside JSON. Try filling a blank JSON type before
		// WS parsing; an existing type or a failed patch keeps the source text.
		upstreamMessage := []byte(openai.OpenAICompatPayloadWithEventType(trimmedData, pendingSSEEventType))
		// Normalize image terminal status before observation and replay inspect this
		// event, so completed image results use the shared protocol status representation.
		if normalized, changed := openai.NormalizeCompletedImageGenerationStatus(upstreamMessage); changed {
			upstreamMessage = normalized
		}
		eventType, eventResponseID, _ := openai.ParseWSEventEnvelope(upstreamMessage)
		responseModelObserver.ObserveOpenAI(upstreamMessage, eventType)
		performance.ObserveAt(string(upstreamMessage), eventType, time.Now())
		if responseID == "" && eventResponseID != "" {
			responseID = eventResponseID
			// Retain parsed identity before a later stream failure can discard the
			// result, using the same HTTP dispatch that owns its selected response header.
			telemetry.ObserveResponseID(ctx, dispatch, responseID)
		}
		if eventType != "" {
			eventCount++
			if firstEventType == "" {
				firstEventType = eventType
			}
			lastEventType = eventType
		}
		// Event-label counts describe the upstream frame mix for diagnostics. The
		// performance observer independently checks payload content when recording arrival milestones.
		if openai.IsWSTokenEvent(eventType) {
			tokenEventCount++
		}
		if openai.WSMessageShouldParseUsage(eventType, upstreamMessage) {
			openai.ParseWSResponseUsageFromCompletedEvent(upstreamMessage, &usage)
		}
		if eventType == "error" || eventType == "response.failed" {
			gatewayhttp.MarkOpenAICyberPolicyEvent(c, upstreamMessage, http.StatusOK, &usage)
		}
		if err := openai.ValidateGIFImageOutputs(upstreamMessage); err != nil {
			return resultWithUsage(), err
		}
		imageCounter.AddSSEData(upstreamMessage)

		// Observe actual upstream model and usage first, then restore matching client
		// model fields for delivery and replay without replacing arbitrary response text.
		if needModelReplace && len(mappedModelBytes) > 0 && openai.WSEventMayContainModel(eventType) && strings.Contains(trimmedData, mappedModel) {
			upstreamMessage = openai.ReplaceWSMessageModel(upstreamMessage, mappedModel, originalModel)
		}
		// Apply the shared Codex tool correction after model restoration so collected
		// replay and delivered events use the same client-facing tool representation.
		if s.Output.Corrector != nil && openai.WSEventMayContainToolCalls(eventType) && openai.WSMessageLikelyContainsToolCalls(upstreamMessage) {
			if corrected, changed := s.Output.Corrector.CorrectToolCallsInSSEBytes(upstreamMessage); changed {
				upstreamMessage = corrected
			}
		}
		replayCollector.AddEvent(eventType, upstreamMessage)

		var upstreamEventErr error
		if officialOpenAIResponses && bareErrorPending && (eventType == "response.completed" || eventType == "response.done") {
			// A later success resolves the pending error as recoverable progress; clear
			// it before terminal handling so end-of-stream cannot synthesize a stale failure.
			bareErrorPending = false
			bareErrorPayload = nil
			bareErrorMessage = ""
		}
		suppressClientMessage := bareErrorPending && eventType != "response.failed"
		requestScopedCapacity := provider.Record.Platform == capability.PlatformOpenAI &&
			(eventType == "error" || eventType == "response.failed") &&
			upstreamopenai.IsOpenAIUpstreamCapacityShedEvent(upstreamMessage)
		terminalPolicy := openAIWSTerminalPolicyDecision{
			TerminalEvent: normalizeOpenAIWSTerminalEvent(eventType),
			Decision:      providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone},
		}
		if openai.IsWSTerminalEvent(eventType) && !requestScopedCapacity &&
			(eventType != "response.failed" || !failureProviderSideEffectsApplied) {
			terminalPolicy = s.handleOpenAIWSTerminalTransientFailure(
				ctx,
				provider,
				mappedModel,
				resp.Header,
				upstreamMessage,
			)
		}
		if eventType == "response.failed" {
			errMessage := upstreamopenai.ExtractOpenAISSEErrorMessage(upstreamMessage)
			if errMessage == "" {
				errMessage = "upstream error event"
			}
			shouldFailover := requestScopedCapacity
			if terminalPolicy.Decision.ShouldReturnGenericError() && !requestScopedCapacity {
				upstreamMessage = buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
				upstreamEventErr = errors.New("upstream response failed with status not in custom error codes")
			} else if !requestScopedCapacity {
				shouldFailover = terminalPolicy.Decision.ShouldFailoverWithDefaults(
					gatewayprovider.ExecutionErrorPolicy(provider),
					terminalPolicy.StatusCode,
					false,
					s.shouldFailoverOpenAIWSError(provider, terminalPolicy.StatusCode, upstreamMessage),
				)
			}
			statusCode := terminalPolicy.StatusCode
			if statusCode == 0 {
				statusCode = http.StatusServiceUnavailable
			}
			if !wroteDownstream && shouldFailover &&
				(turn == 1 || statusCode == http.StatusTooManyRequests) {
				var appliedEffects *gatewayhttp.AppliedOpenAIResponseEffects
				if c != nil && !requestScopedCapacity {
					// Policy effects already ran for this failed event. Pass their result
					// directly so error construction reuses the request's existing health decision.
					appliedEffects = &gatewayhttp.AppliedOpenAIResponseEffects{
						StatusCode:     statusCode,
						StopScheduling: terminalPolicy.Decision.StopScheduling,
					}
				}
				return nil, s.Output.NewStreamPolicyFailureWithModel(
					c, provider, true, resp.Header.Get("x-request-id"), resp.Header, upstreamMessage, errMessage, appliedEffects, mappedModel,
				)
			}
			if wroteDownstream && requestScopedCapacity && !capacityFailoverSuppressedLogged {
				gatewayhttp.LogOpenAICapacityFailoverSuppressed(ctx, provider, "ws_http_bridge", resp.Header.Get("x-request-id"), eventType)
				capacityFailoverSuppressedLogged = true
			}
		}
		if eventType == "error" {
			errCodeRaw, errTypeRaw, _ := openai.ParseWSErrorEventFields(upstreamMessage)
			if reason, _ := upstreamopenai.ClassifyWSErrorEventFromRaw(errCodeRaw, errTypeRaw, upstreamopenai.ExtractOpenAISSEErrorMessage(upstreamMessage)); reason == gatewayhttp.OpenAIInvalidEncryptedContentReason {
				s.Lineage.MarkPayload(
					c, body, "ingress_ws_http_bridge_invalid_encrypted_lineage_mark", provider.Record.ID, turn,
				)
			}
			_, _, errMsgRaw := openai.ParseWSErrorEventFields(upstreamMessage)
			errMessage := strings.TrimSpace(errMsgRaw)
			if errMessage == "" {
				errMessage = "upstream error event"
			}
			statusCode := openAIWSErrorPolicyStatus(upstreamMessage)
			policyStatus := statusCode
			requestScopedError := gatewayprovider.OpenAIWSHTTPBridgeRequestScopedError(provider, statusCode, errMessage, upstreamMessage)
			decision := providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
			defaultFailover := s.shouldFailoverOpenAIWSError(provider, policyStatus, upstreamMessage)
			if requestScopedCapacity {
				requestScopedError = true
				defaultFailover = true
			} else if provider.Record.Platform == capability.PlatformGrok {
				// SSE errors may omit an HTTP status; unknown xAI codes such as new_sensitive
				// then map to 502. Check the body for request-scoped content rejection first,
				// before applying status-based failover or provider health changes.
				if grok.IsGrokContentPolicyRejection(http.StatusForbidden, upstreamMessage) {
					requestScopedError = true
					defaultFailover = false
				} else {
					defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(statusCode, upstreamMessage)
					decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, provider, statusCode, resp.Header, upstreamMessage, "", mappedModel)
				}
			} else if !requestScopedError {
				defaultFailover = s.shouldFailoverOpenAIWSError(provider, policyStatus, upstreamMessage)
				semanticHeaders := resp.Header
				// Semantic 429 retains handshake quota headers only for Spark OAuth-like
				// providers; other models receive no unrelated handshake quota in health handling.
				if policyStatus == http.StatusTooManyRequests {
					semanticHeaders = gatewayprovider.OpenAISemantic429Headers(provider, mappedModel, semanticHeaders)
				}
				decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, policyStatus, semanticHeaders, upstreamMessage, false, mappedModel)
			}
			if decision.StopScheduling {
				failureProviderSideEffectsApplied = true
			}
			if !requestScopedError && provider.Record.Platform == capability.PlatformOpenAI &&
				(policyStatus == http.StatusUnauthorized || policyStatus == http.StatusTooManyRequests || policyStatus == 529 ||
					(policyStatus == http.StatusForbidden && upstreamopenai.OpenAIStream403ProviderFailure(upstreamMessage, errMessage))) {
				// The standalone error already supplied these provider-wide effects; carry
				// that observation into paired terminal handling to suppress duplicate updates.
				failureProviderSideEffectsApplied = true
			}
			if decision.ShouldReturnGenericError() && !requestScopedCapacity {
				upstreamMessage = buildOpenAIWSHTTPBridgeErrorEvent(http.StatusInternalServerError, "Upstream gateway error")
				upstreamEventErr = errors.New("upstream error not in custom error codes")
			} else if !wroteDownstream && (requestScopedCapacity || (!requestScopedError && decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), policyStatus, defaultFailover))) &&
				(turn == 1 || policyStatus == http.StatusTooManyRequests) {
				var appliedEffects *gatewayhttp.AppliedOpenAIResponseEffects
				if c != nil && !requestScopedCapacity && !requestScopedError {
					appliedEffects = &gatewayhttp.AppliedOpenAIResponseEffects{
						StatusCode:     policyStatus,
						StopScheduling: decision.StopScheduling,
					}
				}
				return nil, s.Output.NewStreamPolicyFailureWithModel(
					c, provider, true, resp.Header.Get("x-request-id"), resp.Header, upstreamMessage, errMessage, appliedEffects, mappedModel,
				)
			}
			if wroteDownstream && requestScopedCapacity && !capacityFailoverSuppressedLogged {
				gatewayhttp.LogOpenAICapacityFailoverSuppressed(ctx, provider, "ws_http_bridge", resp.Header.Get("x-request-id"), eventType)
				capacityFailoverSuppressedLogged = true
			}
			if provider.Record.Platform == capability.PlatformGrok {
				upstreamEventErr = errors.New(errMessage)
			} else {
				bareErrorPending = true
				bareErrorPayload = append(bareErrorPayload[:0], upstreamMessage...)
				bareErrorMessage = errMessage
				suppressClientMessage = true
			}
		}
		if eventType == "response.failed" {
			bareErrorPending = false
		}

		// After provider policy classifies the event, replace capacity labels in the
		// client copy with server_error. Terminal completion retains the health decision
		// computed from the event before this presentation rewrite.
		clientMessage := upstreamMessage
		if eventType == "error" || eventType == "response.failed" {
			if rewritten, changed := upstreamopenai.SanitizeOpenAICapacityShedErrorCodeForClient(clientMessage); changed {
				clientMessage = rewritten
			}
		}
		if !clientDisconnected && !suppressClientMessage {
			stageBeforeSemanticOutput := turn == 1 && provider.Record.Platform == capability.PlatformOpenAI && !wroteDownstream
			commitStagedMessages := !stageBeforeSemanticOutput ||
				upstreamopenai.OpenAIStreamDataStartsClientOutput(string(clientMessage), eventType) || openai.IsWSTerminalEvent(eventType)
			if stageBeforeSemanticOutput && !commitStagedMessages {
				// Keep pre-output memory bounded before committing this attempt; exceeding the
				// shared staging cap returns a policy failure with no buffered client delivery.
				if pendingClientMessageBytes+int64(len(clientMessage)) > upstreamopenai.OpenAIFirstOutputStageMaxBytes {
					return nil, s.Output.NewStreamPolicyFailure(
						c,
						provider,
						true,
						resp.Header.Get("x-request-id"),
						resp.Header,
						nil,
						"OpenAI WS HTTP bridge first-output staging limit exceeded",
					)
				}
				// Copy staged bytes before the scanner advances; a later commit flushes
				// them in order ahead of its output event under the same writer.
				pendingClientMessages = append(pendingClientMessages, append([]byte(nil), clientMessage...))
				pendingClientMessageBytes += int64(len(clientMessage))
			} else {
				messages := append(pendingClientMessages, clientMessage)
				pendingClientMessages = nil
				pendingClientMessageBytes = 0
				for _, message := range messages {
					if err := writeClientMessage(message); err != nil {
						if gatewayprovider.IsOpenAIWSClientDisconnectError(err) {
							// Stop further writes after recognized disconnect, but keep scanning the
							// response so final usage and output replay can settle this admitted turn.
							clientDisconnected = true
							timing.ObserveClientDisconnect(ctx, time.Now())
							closeStatus, closeReason := gatewayprovider.SummarizeOpenAIWSReadCloseError(err)
							gatewayprovider.LogOpenAIWSModeInfo(
								"ingress_ws_http_bridge_client_disconnected_drain provider_id=%d turn=%d close_status=%s close_reason=%s",
								provider.Record.ID,
								turn,
								closeStatus, gatewayprovider.TruncateOpenAIWSLogValue(closeReason, gatewayprovider.OpenAIWSHeaderValueMaxLen),
							)
							break
						}
						return nil, ws.WrapIngressTurnError(
							"write_client",
							fmt.Errorf("write client websocket event: %w", err),
							wroteDownstream,
						)
					}
					wroteDownstream = true
				}
			}
		}
		if !clientDisconnected && !suppressClientMessage {
			markOpenAIWSClientVisibleFailure(c, eventType, upstreamMessage)
		}

		if upstreamEventErr != nil {
			return resultWithUsage(), upstreamEventErr
		}
		if openai.IsWSTerminalEvent(eventType) && !bareErrorPending {
			upstreamTerminalEvent = terminalPolicy.TerminalEvent
			terminalEventCount++
			gatewayprovider.LogOpenAIWSModeInfo(
				"ingress_ws_http_bridge_turn_completed provider_id=%d turn=%d response_id=%s payload_bytes=%d duration_ms=%d events=%d token_events=%d terminal_events=%d first_event=%s last_event=%s client_disconnected=%v",
				provider.Record.ID,
				turn, gatewayprovider.TruncateOpenAIWSLogValue(responseID, gatewayprovider.OpenAIWSIDValueMaxLen), payloadBytes,
				time.Since(turnStart).Milliseconds(),
				eventCount,
				tokenEventCount,
				terminalEventCount, gatewayprovider.TruncateOpenAIWSLogValue(firstEventType, gatewayprovider.OpenAIWSLogValueMaxLen), gatewayprovider.TruncateOpenAIWSLogValue(lastEventType, gatewayprovider.OpenAIWSLogValueMaxLen),
				clientDisconnected,
			)
			return resultWithUsage(), nil
		}
	}
	if bareErrorPending {
		if finalizeErr := finalizeBareError(); finalizeErr != nil {
			return resultWithUsage(), finalizeErr
		}
		if scanErr := scanner.Err(); scanErr != nil {
			return resultWithUsage(), fmt.Errorf("read upstream http bridge stream after error event: %w", scanErr)
		}
		// A synthesized failed terminal resolves protocol shape, while this error
		// still reports the upstream failure alongside any usage observed before it.
		return resultWithUsage(), errors.New(bareErrorMessage)
	}
	if err := scanner.Err(); err != nil {
		streamErr := fmt.Errorf("read upstream http bridge stream: %w", err)
		if turn == 1 && !wroteDownstream {
			return nil, s.Requests.Failure.Handle(ctx, c, provider, streamErr, true)
		}
		return resultWithUsage(), streamErr
	}
	terminalErr := errors.New("upstream http bridge stream ended before terminal event")
	if sawDone {
		terminalErr = errors.New("upstream http bridge stream sent [DONE] before terminal event")
	}
	// With the initial attempt still staged, missing terminal permits the shared
	// transport failure policy; later or committed turns retain their partial result.
	if turn == 1 && !wroteDownstream {
		return nil, s.Requests.Failure.Handle(ctx, c, provider, terminalErr, true)
	}
	return resultWithUsage(), terminalErr
}

// resolveGrokWSCacheIdentity derives the bridge cache key from HTTP-converted
// payload and model rules. It prepares the resolved upstream model before
// passing request context, prepared body and model to the shared identity
// helper; conversion or preparation errors stop identity selection.
func (s *OpenAIWebSocketExecutor) resolveGrokWSCacheIdentity(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, payload []byte, routingModel string) (string, error) {
	body, err := prepareOpenAIWSHTTPBridgeBody(provider, payload)
	if err != nil {
		return "", err
	}
	upstreamModel := resolveGrokWSUpstreamModel(provider, body, routingModel)
	body, err = s.prepareGrokResponses(ctx, provider, upstreamModel, body)
	if err != nil {
		return "", err
	}
	return gatewayhttp.ResolveGrokCacheIdentity(c, body, "", upstreamModel), nil
}

// resolveGrokWSUpstreamModel returns the upstream component of bridge model
// resolution. A nonblank supplied model takes precedence over body.model;
// provider mapping and normalization follow, and an empty normalized result
// selects Grok's default Responses model.
func resolveGrokWSUpstreamModel(provider *gatewayprovider.ExecutionProvider, body []byte, originalModel string) string {
	_, upstreamModel := resolveGrokWSModels(provider, body, originalModel)
	return upstreamModel
}

// resolveGrokWSModels returns separate billing and actual upstream model
// choices. A trimmed supplied model wins over the body's model; provider
// mapping changes billing identity before platform normalization chooses the
// upstream spelling. Missing provider skips mapping, and an empty normalized
// upstream choice uses Grok's default Responses model.
//
// The billing result remains the requested or mapped value, including empty
// when no model was supplied. A default upstream model can therefore coexist
// with empty BillingModel in bridge results. Callers pass the selected actual
// model to preparation and health updates so an omitted model does not create
// an empty cooldown key.
func resolveGrokWSModels(provider *gatewayprovider.ExecutionProvider, body []byte, originalModel string) (string, string) {
	requestedModel := strings.TrimSpace(originalModel)
	if requestedModel == "" {
		requestedModel = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	billingModel := requestedModel
	if provider != nil {
		billingModel = providercore.ResolveForwardMappedModel(gatewayprovider.ExecutionRecord(provider), requestedModel, provideradapter.ModelDefaults())
	}
	upstreamModel := gatewayprovider.ExecutionModelPolicy(provider).NormalizeOpenAI(billingModel)
	// An empty normalized model needs a wire model for Grok preparation. Use the
	// protocol default while retaining the separate, possibly empty billing candidate.
	if upstreamModel == "" {
		upstreamModel = grok.DefaultResponsesModel
	}
	return billingModel, upstreamModel
}

// completeHTTPBridgeWarmup emits local created and completed events under one
// generated response ID and returns a LocalWarmup result. Event encoding or
// either write failure stops before returning completion. Ingress retains
// replay input, while entry recognizes LocalWarmup and skips normal billing
// and provider feedback.
//
// The generated ID belongs to this local turn and appears in both events,
// allowing the next client frame to name it as previous_response_id. The
// result has zero usage, streaming WS metadata and response.completed
// identity; no HTTP request is sent by this helper and the caller has already
// retained warmup tool declarations.
func completeHTTPBridgeWarmup(model string, write func([]byte) error) (*forward.OpenAIResult, error) {
	started := time.Now()
	responseID := "resp_ws_warmup_" + uuid.NewString()
	// Protocol warmup event timestamps use seconds since the Unix epoch.
	events, err := openai.WSWarmupEvents(responseID, model, started.Unix())
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if err := write(event); err != nil {
			return nil, err
		}
	}
	return &forward.OpenAIResult{
		LocalWarmup: true, RequestID: responseID, ResponseID: responseID, Model: model,
		Stream: true, OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed", Duration: time.Since(started),
	}, nil
}

// prepareGrokResponses applies shared Grok preparation using the supplied
// upstream model and optional attribute reader. Missing Requests or Readers
// leaves the attribute service nil; the provider helper supplies its protocol
// normalization and returns any preparation error.
func (s *OpenAIWebSocketExecutor) prepareGrokResponses(ctx context.Context, provider *gatewayprovider.ExecutionProvider, model string, body []byte) ([]byte, error) {
	var service *routing.ModelAttributeService
	if s.Requests != nil && s.Requests.Readers != nil {
		service = s.Requests.Readers.Attributes
	}
	return gatewayprovider.PrepareGrokResponses(ctx, provider, model, body, service)
}
