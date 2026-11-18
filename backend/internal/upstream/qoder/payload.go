package qoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// payload.go converts Chat, Responses, and Messages requests into Qoder
// payloads with paired tool history and model controls.
// Client requests encode instructions and tool history differently across Chat,
// Messages, and Responses. Conversion keeps control roles in Qoder system text,
// pairs tool calls with their results, and selects a session source before
// building the site's model and runtime configuration.
//
// Responses histories can contain orphan outputs or unanswered calls, so pairing
// retains matched calls and orders their results after the owning assistant
// turn. Anthropic text and tool results become separate ordered Qoder messages,
// while thinking history is omitted. Payload construction retains shared tool
// definitions, copies outgoing text-block maps, and mirrors observed context
// and thinking controls. Canonical content carries text and cache annotations;
// scalar tool results use the message-level cache marker accepted by the client.

// FallbackMaxInputTokens supplies the native wire limit when no group, account
// or catalog source provided an input limit for the selected model.
const FallbackMaxInputTokens = 200000

// Billing checksum normalization keeps volatile client billing metadata from
// changing the stable conversation prefix used for session prefix comparison.
var (
	// QoderClaudeBillingCCHRe recognizes five-hex billing checksum text ending in a
	// semicolon. System normalization replaces that volatile value before message
	// payload or conversation fingerprint construction.
	QoderClaudeBillingCCHRe = regexp.MustCompile(`(x-anthropic-billing-header:[^\n\r;]*?(?:;[^\n\r;]*?)*\bcch=)[0-9a-fA-F]{5}(;)`)
)

// QoderThinkingDirective carries normalized client intent before
// route-capability effort mapping.
type QoderThinkingDirective struct {
	// Specified distinguishes explicit disabling from an absent client control.
	// Missing controls inherit the selected account model's current defaults.
	Specified bool // explicit client control
	// Enabled selects whether adjustable routes send thinking controls. Effort is
	// normalized client intent; effort-capable routes translate it only while
	// Enabled is true. Disabled thinking sends none on those adjustable routes.
	Enabled bool   // requested thinking switch
	Effort  string // normalized requested effort
}

// QoderPayloadRequest retains normalized protocol input and session sources for
// Gateway preparation.
type QoderPayloadRequest struct {
	// Capabilities supplies per-attempt resolved attributes and execution rules
	// when present. Nil leaves only execution defaults for an explicitly
	// configured custom route; no site-wide model table is consulted.
	Capabilities *modelrules.Resolution // resolved route attributes
	Model        string                 // client model name
	Site         Site                   // selected credential site
	System       string                 // normalized system text
	// Messages retains mutable raw and text-block maps used by conversion.
	// Enrichment and cache-control insertion can mutate those maps before request
	// dispatch.
	Messages []QoderMessage // ordered normalized history
	// Tools is shared with payloads when tools are included. Callers keep its
	// nested declarations stable while planning fingerprints and preparing request
	// bytes.
	Tools      []any                  // declared function tools
	ToolChoice any                    // normalized per-request tool policy
	MaxTokens  int                    // output tokens
	Thinking   QoderThinkingDirective // normalized thinking intent
	// ExplicitSession, PromptCacheKey, and MetadataUserID retain distinct client
	// session sources. QoderConversationKey selects precedence and applies API-key
	// plus provider isolation after parsing.
	ExplicitSession string // body session identifier
	PromptCacheKey  string // stable prompt-cache identifier
	MetadataUserID  string // Anthropic metadata identity
	ResponseID      string // generated Responses identifier
}

// BuildQoderPayloadFromChatCompletionsForSite parses Chat input and builds a
// complete payload for the supplied site. It sends all parsed messages, system
// text, and tools. The historical user-type argument is ignored.
func BuildQoderPayloadFromChatCompletionsForSite(body []byte, _ string, site Site) (map[string]any, string, error) {
	request, err := ParseQoderChatCompletionsPayload(body)
	if err != nil {
		return nil, "", err
	}
	request.Site = site
	return BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
}

// ParseQoderChatCompletionsPayload parses Chat input into normalized system,
// messages, tools, and controls. A nonempty model is required, unsupported
// message roles return errors, and positive max_completion_tokens takes
// precedence over max_tokens and the execution default.
//
// Control-role messages contribute to system text while assistant/tool history
// retains call identity. Modern tools and legacy functions are combined before
// tool-choice filtering, and thinking controls use Chat's field precedence.
// Session and metadata fields remain separate so conversation selection can
// apply caller and provider isolation later.
func ParseQoderChatCompletionsPayload(body []byte) (QoderPayloadRequest, error) {
	var req protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("parse chat completions request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return QoderPayloadRequest{}, errors.New("model is required")
	}

	messages, err := QoderMessagesFromChatCompletions(req.Messages)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	messages = EnrichQoderToolResultMessages(messages)

	rawReq := QoderRequestMap(body)
	choice := QoderToolChoice(rawReq["tool_choice"])
	if rawReq["tool_choice"] == nil {
		choice = QoderToolChoice(rawReq["function_call"])
	}
	maxTokens := QoderDefaultMaxTokens
	// Chat clients can send both output-limit fields. Prefer the positive
	// completion-specific value so legacy max_tokens cannot override the requested
	// completion budget.
	if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		maxTokens = *req.MaxCompletionTokens
	} else if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	return QoderPayloadRequest{
		Model:           req.Model,
		System:          QoderChatSystemText(req.Messages),
		Messages:        messages,
		Tools:           QoderChatCompletionsTools(req, rawReq),
		ToolChoice:      choice,
		MaxTokens:       maxTokens,
		Thinking:        QoderThinkingDirectiveFromBody(body, "reasoning_effort", "reasoning.effort"),
		ExplicitSession: FirstNonEmptyQoder(QoderStringField(rawReq, "session_id"), QoderStringField(rawReq, "conversation_id")),
		PromptCacheKey:  QoderStringField(rawReq, "prompt_cache_key"),
		MetadataUserID:  QoderMetadataUserID(rawReq["metadata"]),
	}, nil
}

// BuildQoderPayloadFromAnthropicMessages parses Messages input and builds a
// complete global-site payload with all parsed messages, system text, and tool
// definitions. The historical user-type argument is ignored.
func BuildQoderPayloadFromAnthropicMessages(body []byte, _ string) (map[string]any, string, error) {
	request, err := ParseQoderAnthropicMessagesPayload(body)
	if err != nil {
		return nil, "", err
	}
	return BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
}

// ParseQoderResponsesPayload accepts a self-contained Responses turn. Clients
// supply instructions, tools, calls and results for this request. Storage and
// server-side continuation are rejected before constructing a native payload.
// The generated response ID identifies the result in events and usage records.
func ParseQoderResponsesPayload(body []byte) (QoderPayloadRequest, error) {
	var req protocolopenai.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("parse responses request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return QoderPayloadRequest{}, errors.New("model is required")
	}
	rawReq := QoderRequestMap(body)
	if strings.TrimSpace(req.PreviousResponseID) != "" || req.Store != nil && *req.Store || rawReq["conversation"] != nil {
		return QoderPayloadRequest{}, &RequestError{Code: "unsupported_response_state", Message: "Qoder Responses requires stateless input. Set store to false, omit previous_response_id and conversation, and include the full history."}
	}
	messages, err := QoderMessagesFromResponsesInput(req.Input)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	if qoderHasUnpairedToolOutputs(messages) {
		return QoderPayloadRequest{}, &RequestError{Code: "missing_tool_call", Message: "Include the function_call for each function_call_output in the Responses input."}
	}
	messages = NormalizeQoderResponsesToolPairing(messages)
	messages = EnrichQoderToolResultMessages(messages)

	// Responses instructions and control-role items are separate inputs. Combine
	// their text into the Qoder system prompt so role conversion can omit duplicate
	// history.
	systemParts := make([]string, 0)
	if req.Instructions != "" {
		systemParts = append(systemParts, strings.TrimSpace(req.Instructions))
	}
	// The history converter drops developer/system items after this step. Keep
	// their supported text here so those control instructions survive protocol
	// conversion.
	if len(req.Input) > 0 {
		var items []protocolopenai.ResponsesInputItem
		if err := json.Unmarshal(req.Input, &items); err == nil {
			for _, item := range items {
				if item.Type == "item_reference" {
					return QoderPayloadRequest{}, &RequestError{Code: "unsupported_response_state", Message: "Include the complete input item instead of an item_reference for Qoder Responses."}
				}
				if item.Role == "developer" || item.Role == "system" {
					text := QoderResponsesContentText(item.Content)
					if text != "" {
						systemParts = append(systemParts, text)
					}
				}
			}
		}
	}
	system := strings.Join(systemParts, "\n\n")

	sessionID := QoderStringField(rawReq, "session_id")
	conversationID := QoderStringField(rawReq, "conversation_id")
	maxTokens := QoderDefaultMaxTokens
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		maxTokens = *req.MaxOutputTokens
	}
	return QoderPayloadRequest{
		Model:           req.Model,
		System:          system,
		Messages:        messages,
		Tools:           QoderResponsesToolsToQoderTools(req.Tools),
		ToolChoice:      QoderToolChoice(rawReq["tool_choice"]),
		MaxTokens:       maxTokens,
		Thinking:        QoderThinkingDirectiveFromBody(body, "reasoning.effort", "reasoning_effort"),
		ExplicitSession: FirstNonEmptyQoder(sessionID, conversationID),
		PromptCacheKey:  QoderStringField(rawReq, "prompt_cache_key"),
		MetadataUserID:  QoderMetadataUserID(rawReq["metadata"]),
		ResponseID:      QoderResponsesID(),
	}, nil
}

// ParseQoderAnthropicMessagesPayload normalizes Messages system text, tool
// history, and Qoder thinking controls. A nonempty model is required and
// positive max_tokens replaces the execution default. Unsupported system or
// content blocks return errors.
//
// Tool definitions use the shared bridge with messages and thinking fields
// cleared, because Qoder handles those independently. This permits Qoder's
// effort tolerance and preserves text/tool-result ordering in its own history
// converter. Session metadata and the protocol's native output_config effort are
// retained for later planning and capability mapping.
func ParseQoderAnthropicMessagesPayload(body []byte) (QoderPayloadRequest, error) {
	var req protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("parse anthropic messages request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return QoderPayloadRequest{}, errors.New("model is required")
	}

	system, err := QoderAnthropicSystemText(req.System)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	toolReq := req
	toolReq.System = nil
	toolReq.Messages = nil
	// The bridge is used for tool definitions here. Clear thinking controls so its
	// validation cannot reject effort values handled independently by Qoder's
	// capability mapping.
	toolReq.Thinking = nil
	toolReq.OutputConfig = nil
	responsesReq, err := bridge.AnthropicToResponses(&toolReq, bridge.RequestOptions{})
	if err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("convert anthropic messages request: %w", err)
	}
	messages, err := QoderMessagesFromAnthropicMessages(req.Messages)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	messages = EnrichQoderToolResultMessages(messages)

	rawReq := QoderRequestMap(body)
	maxTokens := QoderDefaultMaxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}
	return QoderPayloadRequest{
		Model:           req.Model,
		System:          system,
		Messages:        messages,
		Tools:           QoderResponsesToolsToQoderTools(responsesReq.Tools),
		ToolChoice:      QoderToolChoice(rawReq["tool_choice"]),
		MaxTokens:       maxTokens,
		Thinking:        QoderThinkingDirectiveFromBody(body, "output_config.effort"),
		ExplicitSession: FirstNonEmptyQoder(QoderStringField(rawReq, "session_id"), QoderStringField(rawReq, "conversation_id")),
		PromptCacheKey:  QoderStringField(rawReq, "prompt_cache_key"),
		MetadataUserID:  QoderMetadataUserID(rawReq["metadata"]),
	}, nil
}

// QoderThinkingDirectiveFromBody selects the requested thinking switch and
// normalized effort. Explicit disabled/none/off type or a disable effort in any
// supplied path wins over all enabling inputs. The first valid effort wins among
// enabling paths.
//
// If no valid effort exists, a positive budget or enabled/adaptive type selects
// max. Missing or unusable controls leave thinking disabled. This ordered choice
// keeps an invalid effort eligible for an independent budget or switch while
// honoring a disable even after another effort was found.
func QoderThinkingDirectiveFromBody(body []byte, effortPaths ...string) QoderThinkingDirective {
	thinkingType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()))
	if thinkingType == "disabled" || thinkingType == "none" || thinkingType == "off" {
		return QoderThinkingDirective{Specified: true}
	}

	effort := ""
	// Inspect every supplied effort path after retaining the first valid enable.
	// A later disable still overrides it, so an earlier match cannot end this scan.
	for _, path := range effortPaths {
		raw := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if raw == "" {
			continue
		}
		mapped, disabled := NormalizeQoderThinkingEffort(raw)
		if disabled {
			return QoderThinkingDirective{Specified: true}
		}
		if effort == "" && mapped != "" {
			effort = mapped
		}
	}
	if effort != "" {
		return QoderThinkingDirective{Specified: true, Enabled: true, Effort: effort}
	}

	if gjson.GetBytes(body, "thinking.budget_tokens").Int() > 0 {
		return QoderThinkingDirective{Specified: true, Enabled: true, Effort: "max"}
	}
	if thinkingType == "enabled" || thinkingType == "adaptive" {
		return QoderThinkingDirective{Specified: true, Enabled: true}
	}
	return QoderThinkingDirective{}
}

// NormalizeQoderThinkingEffort maps normalized effort labels and reports an
// explicit disable. Separators and case are ignored; unknown values return empty
// effort with disabled=false so the caller can consider a budget or switch.
func NormalizeQoderThinkingEffort(raw string) (effort string, disabled bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	switch value {
	case "none", "disabled", "off":
		return "", true
	case "minimal", "low":
		return "low", false
	case "medium":
		return "medium", false
	case "high":
		return "high", false
	case "xhigh", "veryhigh", "extrahigh":
		return "xhigh", false
	case "max":
		return "max", false
	default:
		return "", false
	}
}

// QoderClaudeCodeStablePrefixKey joins model, normalized system, tool hash, and
// first user text. The conversation caller applies API-key and provider
// isolation to the result; a missing first user text leaves the other stable
// inputs present.
func QoderClaudeCodeStablePrefixKey(request QoderPayloadRequest) string {
	parts := []string{
		"model=" + strings.TrimSpace(request.Model),
		"system=" + QoderNormalizeSystemForFingerprint(request.System),
		"tools=" + QoderFingerprintAny(request.Tools),
	}
	if firstUser := QoderFirstUserMessageText(request.Messages); firstUser != "" {
		parts = append(parts, "first_user="+firstUser)
	}
	return strings.Join(parts, "|")
}

// QoderFirstUserMessageText returns the first nonblank user text after
// trimming it. Roles are trimmed before matching user; absent qualifying text
// returns empty so stable-prefix construction can use its other inputs.
func QoderFirstUserMessageText(messages []QoderMessage) string {
	for _, message := range messages {
		if strings.TrimSpace(message.Role) != "user" {
			continue
		}
		if text := strings.TrimSpace(message.Text); text != "" {
			return text
		}
	}
	return ""
}

// QoderMetadataUserID reads the user_id field from a decoded request metadata
// object. Absent or non-object metadata returns empty text; QoderStringField
// supplies trimming and tolerated value formatting.
func QoderMetadataUserID(raw any) string {
	metadata, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return QoderStringField(metadata, "user_id")
}

// QoderMessageFingerprints hashes each message's outgoing payload representation
// in order. Outgoing text-block maps are copied, while raw tool data is read
// synchronously. Callers keep the input stable until all history fingerprints
// have been computed.
func QoderMessageFingerprints(messages []QoderMessage) []string {
	fingerprints := make([]string, 0, len(messages))
	for _, message := range messages {
		fingerprints = append(fingerprints, QoderFingerprintAny(QoderPayloadMessageFromMessage(message)))
	}
	return fingerprints
}

// QoderFingerprintString returns the payload fingerprint of the supplied string
// bytes.
func QoderFingerprintString(value string) string {
	return qoderPayloadFingerprint([]byte(value))
}

// QoderSystemFingerprint hashes system text after normalizing recognized
// volatile billing checksums.
func QoderSystemFingerprint(system string) string {
	return QoderFingerprintString(QoderNormalizeSystemForFingerprint(system))
}

// QoderNormalizeSystemForFingerprint replaces recognized five-hex cch values
// before system hashing. Other system text remains supplied, allowing stable
// conversation comparison across checksum variants while retaining the rest of
// the billing header.
func QoderNormalizeSystemForFingerprint(system string) string {
	if !strings.Contains(system, "x-anthropic-billing-header") || !strings.Contains(system, "cch=") {
		return system
	}
	return QoderClaudeBillingCCHRe.ReplaceAllString(system, "${1}<cch>${2}")
}

// QoderFingerprintAny hashes compact JSON, using formatted text when JSON
// marshaling fails. Empty serialized input follows qoderPayloadFingerprint's
// empty result; callers use the returned text for conversation comparison.
func QoderFingerprintAny(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		// Unsupported Go values cannot produce JSON fingerprints. Hash their formatted
		// text so the helper still returns a fingerprint when marshaling fails.
		return qoderPayloadFingerprint([]byte(fmt.Sprint(value)))
	}
	return qoderPayloadFingerprint(body)
}

// BuildQoderPayloadWithOptions builds a complete Qoder request using selected
// messages and inclusion flags. The captured account model supplies the service
// key and execution controls. A blank session ID generates a UUID. The
// supplied MaxTokens is written as provided, and
// callers pass a supported site for business-profile lookup.
//
// Request and chat-record IDs share one UUID while request-set and business IDs
// are separate. Prompt selection considers current messages before the request's
// full history. System/tool inclusion and mirrored context/thinking settings
// shape the outgoing request. Automatic cache markers modify only outgoing
// text-block maps; tool definitions remain shared references in the returned
// payload and must stay stable until serialization finishes.
func BuildQoderPayloadWithOptions(request QoderPayloadRequest, sessionID string, messages []QoderMessage, includeSystem bool, includeTools bool) (map[string]any, string, error) {
	request, err := request.withEffectiveControls()
	if err != nil {
		return nil, "", err
	}
	payload, model := buildQoderPayload(request, sessionID, messages, includeSystem, includeTools)
	return payload, model, nil
}

// withEffectiveControls projects client intent onto the selected route before
// fingerprinting tools or preparing a request. The final thinking switch and
// effort determine conditional tool support, including for clients that rely
// on account defaults. A forced or named tool conflicts with disabled tools.
//
// Optional declarations are omitted from the outbound request when the route
// disables calls. Prior tool calls and results remain part of the conversation
// history. The returned request owns its control fields and leaves the supplied
// draft available for other route attempts.
func (request QoderPayloadRequest) withEffectiveControls() (QoderPayloadRequest, error) {
	thinking, err := ResolveObservedThinking(request.Thinking, request.Capabilities)
	if err != nil {
		return request, err
	}
	request.Thinking = thinking
	if choice, ok := request.ToolChoice.(map[string]any); ok && QoderStringField(choice, "type") == "allowed_tools" {
		request.Tools, request.ToolChoice, err = restrictQoderFunctions(request.Tools, choice)
		if err != nil {
			return request, err
		}
	}
	if request.Capabilities == nil || len(request.Tools) == 0 && request.ToolChoice == nil {
		return request, nil
	}
	attrs := request.Capabilities.Attributes
	if attrs.Conditions != nil {
		state := modelrules.RequestState{Thinking: &thinking.Enabled}
		if thinking.Effort != "" {
			state.Effort = &thinking.Effort
		}
		attrs = request.Capabilities.ForRequest(state).Attributes
	}
	if attrs.ToolCall == nil || *attrs.ToolCall {
		return request, nil
	}
	switch choice := request.ToolChoice.(type) {
	case map[string]any:
		return request, &RequestError{Code: "unsupported_tool_choice", Message: "This Qoder route does not support the selected tool."}
	case string:
		if choice == "required" {
			return request, &RequestError{Code: "unsupported_tool_choice", Message: "This Qoder route does not support required tool calls."}
		}
	}
	request.Tools = []any{}
	request.ToolChoice = nil
	return request, nil
}

// buildQoderPayload writes already validated controls and the planned transcript.
// Conversation planning and this encoder receive the same effective tool list
// so reused sessions cannot fingerprint tools that did not reach the wire.
func buildQoderPayload(request QoderPayloadRequest, sessionID string, messages []QoderMessage, includeSystem, includeTools bool) (map[string]any, string) {
	modelInfo := QoderModelInfo{Key: strings.TrimSpace(request.Model), Source: "system"}
	observed, found := observedModel(request.Capabilities)
	if found {
		modelInfo.Key = observed.Key
	}
	requestID := uuid.NewString()
	prompt := LatestQoderPayloadPromptText(messages, includeTools)
	if prompt == "" {
		prompt = LatestQoderPayloadPromptText(request.Messages, includeTools)
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = uuid.NewString()
	}
	payload := QoderBasePayload()
	payload["request_id"] = requestID
	payload["chat_record_id"] = requestID
	payload["request_set_id"] = uuid.NewString()
	payload["session_id"] = sessionID
	// The current desktop runtime leaves this legacy hint empty. A saved plan
	// label must not replace that protocol value when constructing a new request.
	payload["aliyun_user_type"] = ""
	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	parameters["max_tokens"] = request.MaxTokens
	if request.ToolChoice != nil {
		parameters["tool_choice"] = request.ToolChoice
	}
	modelConfig["key"] = modelInfo.Key
	modelConfig["source"] = modelInfo.Source
	modelConfig["display_name"] = modelInfo.Key
	if found {
		if observed.Name != "" {
			modelConfig["display_name"] = observed.Name
		}
		if observed.Vision != nil {
			modelConfig["is_vl"] = *observed.Vision
		}
	}
	chatContext, _ := payload["chat_context"].(map[string]any)
	extra, _ := chatContext["extra"].(map[string]any)
	extraModelConfig, _ := extra["modelConfig"].(map[string]any)
	chatContext["text"] = prompt
	extraModelConfig["key"] = modelInfo.Key
	extraModelConfig["source"] = modelInfo.Source
	applyContextCapability(payload, request.Capabilities, len(observed.ContextWindows) > 0)
	applyObservedThinking(payload, request.Thinking)
	extra["originalContent"] = prompt
	// Business name is limited to thirty runes while full prompt text remains in
	// chat context and message history. Truncation affects this descriptive title
	// alone.
	payload["business"] = map[string]any{
		"product":  DesktopProduct,
		"version":  MustProfileForSite(request.Site).ClientVersion,
		"type":     "agent",
		"stage":    "init",
		"id":       uuid.NewString(),
		"name":     TruncateRunes(prompt, 30),
		"begin_at": time.Now().UnixMilli(),
	}

	outMessages := make([]any, 0, len(messages)+1)
	if includeSystem && strings.TrimSpace(request.System) != "" {
		outMessages = append(outMessages, QoderPayloadMessage("system", request.System))
	}
	for _, msg := range messages {
		outMessages = append(outMessages, QoderPayloadMessageFromMessage(msg))
	}
	// The last eligible text block defines the outgoing ephemeral cache marker.
	// Apply it after message conversion so included system and history share one
	// selection order.
	AddQoderEphemeralCacheControl(outMessages)
	payload["messages"] = outMessages
	if includeTools {
		payload["tools"] = request.Tools
	} else {
		payload["tools"] = []any{}
	}
	return payload, modelInfo.Key
}

// applyContextCapability encodes the effective input limit in Qoder's nested fields.
// The wire fallback applies only when every attribute source omitted the limit.
// Selectable windows additionally require the runtime override and context length.
func applyContextCapability(payload map[string]any, resolution *modelrules.Resolution, selectable bool) {
	maxInput := FallbackMaxInputTokens
	if resolution != nil && resolution.Attributes.InputLimit != nil {
		maxInput = *resolution.Attributes.InputLimit
	}
	modelConfig, _ := payload["model_config"].(map[string]any)
	modelConfig["max_input_tokens"] = maxInput
	if !selectable {
		return
	}

	parameters, _ := payload["parameters"].(map[string]any)
	parameters["context_length"] = maxInput
	chatContext, _ := payload["chat_context"].(map[string]any)
	extra, _ := chatContext["extra"].(map[string]any)
	runtimeOverride, _ := extra["ideModelConfigOverride"].(map[string]any)
	if runtimeOverride == nil {
		runtimeOverride = map[string]any{}
		extra["ideModelConfigOverride"] = runtimeOverride
	}
	runtimeOverride["max_input_tokens"] = maxInput
}

// AddQoderEphemeralCacheControl marks the final nonblank message content for
// caching. Text arrays carry the marker on their last text block, and scalar
// content carries it on the message. Existing markers remain authoritative;
// callers supply payload-owned message and block maps before this mutation.
func AddQoderEphemeralCacheControl(messages []any) {
	for _, message := range slices.Backward(messages) {
		message, ok := message.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := message["content"].(string); ok && strings.TrimSpace(text) != "" {
			// Scalar tool or assistant content carries cache control on the message,
			// matching the desktop representation without duplicating text in another field.
			if _, exists := message["cache_control"]; !exists {
				message["cache_control"] = map[string]any{"type": "ephemeral"}
			}
			return
		}
		contents, _ := message["content"].([]any)
		for _, content := range slices.Backward(contents) {
			block, ok := content.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			text, _ := block["text"].(string)
			if strings.TrimSpace(text) == "" {
				continue
			}
			if _, exists := block["cache_control"]; exists {
				return
			}
			block["cache_control"] = map[string]any{"type": "ephemeral"}
			return
		}
	}
}

// QoderBasePayload returns newly allocated Qoder request maps with execution
// defaults and empty history. Nested model and chat-context maps are ready for
// capability writers, while messages and tools start as empty slices.
func QoderBasePayload() map[string]any {
	return map[string]any{
		"request_id":       "",
		"request_set_id":   "",
		"chat_record_id":   "",
		"stream":           true,
		"chat_task":        "FREE_INPUT",
		"image_urls":       nil,
		"is_reply":         true,
		"is_retry":         false,
		"session_id":       "",
		"code_language":    "",
		"source":           1,
		"version":          "3",
		"chat_prompt":      "",
		"parameters":       map[string]any{"max_tokens": QoderDefaultMaxTokens},
		"aliyun_user_type": "",
		"session_type":     DesktopProduct,
		"agent_id":         "agent_common",
		"task_id":          "common",
		"chat_context": map[string]any{
			"chatPrompt": "",
			"features":   []any{},
			"imageUrls":  nil,
			"text":       "",
			"extra": map[string]any{
				"context":         []any{},
				"modelConfig":     map[string]any{"is_reasoning": false, "key": "auto"},
				"originalContent": "",
			},
		},
		"model_config": map[string]any{
			"key":              "auto",
			"format":           "openai",
			"model":            "",
			"api_key":          "",
			"url":              "",
			"is_vl":            false,
			"is_reasoning":     false,
			"source":           "system",
			"max_input_tokens": FallbackMaxInputTokens,
		},
		"messages": []any{},
		"tools":    []any{},
	}
}

// QoderPayloadMessage converts a role and text through the standard Qoder
// message builder.
func QoderPayloadMessage(role, text string) map[string]any {
	return QoderPayloadMessageFromMessage(QoderMessage{Role: role, Text: text})
}

// QoderRequestMap decodes optional raw request fields into a JSON object.
// Malformed input returns an empty object, allowing typed request parsers to own
// rejection while optional session/control extraction uses absent values.
func QoderRequestMap(body []byte) map[string]any {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		// Optional raw-field extraction shares typed parsing at the request entry
		// point. Invalid raw-object decoding therefore presents no optional fields to
		// session and control readers.
		return map[string]any{}
	}
	return req
}

// QoderChatSystemText joins nonempty system and developer text in their original
// message order. Other roles do not contribute; QoderChatContentText extracts
// supported text representations from each selected control message.
func QoderChatSystemText(messages []protocolopenai.ChatMessage) string {
	parts := make([]string, 0)
	for _, message := range messages {
		// Chat control roles are collected separately from visible history. Keep both
		// system and developer text here so later role conversion cannot drop
		// developer instructions.
		if message.Role != "system" && message.Role != "developer" {
			continue
		}
		text := QoderChatContentText(message.Content)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// QoderChatContentText reads string content or joins text parts with newlines.
// Empty/null content, malformed representations, and unsupported part types
// contribute empty text for the caller's role conversion.
func QoderChatContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	// Chat content can carry a string or typed parts. Try the part representation
	// after string decoding so control prompts and visible history retain supported
	// text blocks.
	var parts []protocolopenai.ChatContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "text" && part.Text != "" {
				out = append(out, part.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// QoderMessagesFromChatCompletions converts ordered Chat messages and skips
// empty converted roles. A converter error stops the operation; control roles
// are extracted separately by the request parser and omitted from this history.
func QoderMessagesFromChatCompletions(messages []protocolopenai.ChatMessage) ([]QoderMessage, error) {
	out := make([]QoderMessage, 0, len(messages))
	for _, message := range messages {
		converted, err := QoderMessageFromChatCompletionsMessage(message)
		if err != nil {
			return nil, err
		}
		if converted.Role != "" {
			out = append(out, converted)
		}
	}
	return out, nil
}

// QoderMessageFromChatCompletionsMessage maps one Chat role and tool history
// into Qoder representation. Blank role becomes user, system/developer produces
// an empty converted role, and unsupported roles return errors.
//
// Assistant history prefers modern calls and then legacy function_call. Tool
// output carries its call ID and uses an empty-output marker; legacy function
// results use the function name as identity so they pair with the converted
// legacy call. Visible text extraction stays separate from tool-call metadata.
func QoderMessageFromChatCompletionsMessage(message protocolopenai.ChatMessage) (QoderMessage, error) {
	role := strings.TrimSpace(message.Role)
	switch role {
	case "", "user":
		text := QoderChatContentText(message.Content)
		return QoderMessage{Role: "user", Text: text, Raw: map[string]any{"role": "user", "content": text}}, nil
	case "system", "developer":
		return QoderMessage{}, nil
	case "assistant":
		text := QoderChatContentText(message.Content)
		raw := map[string]any{"role": "assistant", "content": text}
		if toolCalls := QoderChatToolCalls(message.ToolCalls); len(toolCalls) > 0 {
			// Modern call history takes precedence when present. A legacy function call
			// fills the empty modern-call case so results still have one owning assistant
			// representation.
			raw["tool_calls"] = toolCalls
		} else if toolCalls := QoderLegacyChatFunctionCall(message.FunctionCall); len(toolCalls) > 0 {
			raw["tool_calls"] = toolCalls
		}
		return QoderMessage{Role: "assistant", Text: text, Raw: raw}, nil
	case "tool":
		text := QoderChatContentText(message.Content)
		if text == "" {
			text = "(empty)"
		}
		raw := map[string]any{
			"role":         "tool",
			"tool_call_id": message.ToolCallID,
			"content":      text,
		}
		if name := strings.TrimSpace(message.Name); name != "" {
			raw["name"] = name
		}
		return QoderMessage{Role: "tool", Text: text, ToolCallID: message.ToolCallID, Raw: raw}, nil
	case "function":
		text := QoderChatContentText(message.Content)
		if text == "" {
			text = "(empty)"
		}
		callID := strings.TrimSpace(message.Name)
		raw := map[string]any{
			"role":         "tool",
			"tool_call_id": callID,
			"content":      text,
		}
		if callID != "" {
			raw["name"] = callID
		}
		return QoderMessage{Role: "tool", Text: text, ToolCallID: callID, Raw: raw}, nil
	default:
		return QoderMessage{}, fmt.Errorf("unsupported chat message role: %s", role)
	}
}

// QoderChatToolCalls converts modern Chat calls into new wire maps while
// retaining call IDs. Empty arguments become an empty JSON object string and
// absent type becomes function; an empty input list returns nil.
func QoderChatToolCalls(toolCalls []protocolopenai.ChatToolCall) []any {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]any, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		arguments := toolCall.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		callType := strings.TrimSpace(toolCall.Type)
		if callType == "" {
			callType = "function"
		}
		out = append(out, map[string]any{
			"id":   toolCall.ID,
			"type": callType,
			"function": map[string]any{
				"name":      toolCall.Function.Name,
				"arguments": arguments,
			},
		})
	}
	return out
}

// QoderLegacyChatFunctionCall builds tool history for a named legacy function
// call. Nil input or a blank name returns nil, while QoderToolCallMap supplies
// an empty-object argument string when needed.
//
// Legacy function_call has no invocation ID. The trimmed function name fills
// the converted call ID and name, matching the function-role result's identity
// selection. This lets ordinary legacy history pair a result with its
// assistant turn; repeated invocations of the same name retain that shared
// identity rather than acquiring a unique modern call ID.
func QoderLegacyChatFunctionCall(functionCall *protocolopenai.ChatFunctionCall) []any {
	if functionCall == nil {
		return nil
	}
	name := strings.TrimSpace(functionCall.Name)
	if name == "" {
		return nil
	}
	// Use the trimmed name passed to the converted function-role result. A separate
	// generated ID here would leave that legacy reply unable to match its call.
	return []any{QoderToolCallMap(name, name, functionCall.Arguments)}
}

// QoderAnthropicSystemText extracts supported Messages system text and
// normalizes recognized billing checksums. Empty or null input returns empty
// text. String input follows the same normalization as each text block, while
// malformed fields or non-text system blocks return errors.
//
// Block text is retained in order and joined with newlines after empty
// normalized parts are skipped. The volatile cch value becomes a stable marker
// before request construction and conversation hashing, so checksum changes
// alone do not change the normalized control text.
func QoderAnthropicSystemText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return QoderStripVolatileSystemText(text), nil
	}
	// A Messages system prompt may be text or an array of text blocks. Decode
	// blocks after the string form so their instructions survive the same
	// normalization.
	var blocks []protocolanthropic.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", errors.New("unsupported system field")
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported system block type: %v", block.Type)
		}
		if text := QoderStripVolatileSystemText(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// QoderStripVolatileSystemText replaces recognized five-hex billing cch values
// with a stable marker.
func QoderStripVolatileSystemText(text string) string {
	return QoderClaudeBillingCCHRe.ReplaceAllString(text, "${1}<cch>${2}")
}

// QoderMessagesFromResponsesInput converts Responses text or ordered items into
// raw Qoder history. Empty/null input produces nil, string input becomes a
// user message, and malformed item arrays return errors.
//
// Consecutive function calls are collected into one assistant turn before the
// next item is converted, so parallel calls remain grouped even when Responses
// represents them as separate items. The parser pairs each output with its
// owning call from this request before constructing the native payload.
func QoderMessagesFromResponsesInput(raw json.RawMessage) ([]QoderMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []QoderMessage{{Role: "user", Text: text, Raw: map[string]any{"role": "user", "content": text}}}, nil
	}
	var items []protocolopenai.ResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse canonical responses input: %w", err)
	}
	messages := make([]QoderMessage, 0, len(items))
	var pendingToolCalls []any
	// Responses calls appear as separate items. Group consecutive calls before
	// another item so matched parallel replies can follow one assistant turn during
	// pairing.
	flushPendingToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		messages = append(messages, QoderMessage{
			Role: "assistant",
			Raw: map[string]any{
				"role":       "assistant",
				"tool_calls": pendingToolCalls,
			},
		})
		pendingToolCalls = nil
	}
	for _, item := range items {
		if item.Type == "function_call" {
			pendingToolCalls = append(pendingToolCalls, QoderToolCallMap(item.CallID, item.Name, item.Arguments))
			continue
		}
		flushPendingToolCalls()
		converted := QoderMessageFromResponsesInputItem(item)
		if converted.Role != "" {
			messages = append(messages, converted)
		}
	}
	flushPendingToolCalls()
	return messages, nil
}

// qoderHasUnpairedToolOutputs checks each result against the calls in the
// submitted history. Parallel invocations of the same function have distinct
// call IDs and must each carry the corresponding result.
func qoderHasUnpairedToolOutputs(messages []QoderMessage) bool {
	calls := map[string]bool{}
	for _, message := range messages {
		for _, raw := range NormalizeQoderToolCalls(message.Raw["tool_calls"]) {
			if call, ok := raw.(map[string]any); ok {
				calls[QoderToolCallIDFromMap(call)] = true
			}
		}
	}
	for _, message := range messages {
		if message.Role == "tool" && !calls[QoderMessageToolCallID(message)] {
			return true
		}
	}
	return false
}

// QoderMessageFromResponsesInputItem maps message, function call, or function
// output items into Qoder history. Control roles and unsupported item types
// produce an empty message; function outputs retain call_id and use an
// empty-output marker when needed.
func QoderMessageFromResponsesInputItem(item protocolopenai.ResponsesInputItem) QoderMessage {
	switch item.Type {
	case "function_call":
		raw := map[string]any{
			"role":       "assistant",
			"tool_calls": []any{QoderToolCallMap(item.CallID, item.Name, item.Arguments)},
		}
		return QoderMessage{Role: "assistant", Raw: raw}
	case "function_call_output":
		output := item.Output
		if output == "" {
			output = "(empty)"
		}
		return QoderMessage{
			Role:       "tool",
			Text:       output,
			ToolCallID: item.CallID,
			Raw: map[string]any{
				"role":         "tool",
				"tool_call_id": item.CallID,
				"content":      output,
			},
		}
	default:
		if item.Type != "" && item.Type != "message" {
			return QoderMessage{}
		}
		role := QoderResponsesRole(item.Role)
		if role == "" {
			return QoderMessage{}
		}
		text := QoderResponsesContentText(item.Content)
		if text == "" && item.Type == "message" && role != "assistant" {
			return QoderMessage{Role: role, Raw: map[string]any{"role": role}}
		}
		return QoderMessage{
			Role: role,
			Text: text,
			Raw:  map[string]any{"role": role, "content": text},
		}
	}
}

// NormalizeQoderResponsesToolPairing filters recognized call-bearing assistant
// turns and places matched replies after them. Replies are indexed by trimmed
// call ID, with the last reply for an ID winning. QoderMessageHasToolCalls
// requires both a usable ID and name before a turn enters call filtering.
//
// Within those turns, normalized calls lacking a reply disappear; retained calls
// keep assistant order. Empty call-only turns are omitted after filtering, while
// visible text survives with tool_calls removed from a shallow raw-map copy.
// Other turns pass through, including call metadata the predicate cannot
// identify; standalone tool outputs are omitted.
func NormalizeQoderResponsesToolPairing(messages []QoderMessage) []QoderMessage {
	// Responses histories can contain unanswered calls or orphan outputs. Index
	// replies first so each retained assistant call is followed by its matching
	// result.
	replies := make(map[string]QoderMessage)
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		if id := QoderMessageToolCallID(message); id != "" {
			replies[id] = message
		}
	}

	out := make([]QoderMessage, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.Role == "tool":
			continue
		case QoderMessageHasToolCalls(message):
			tools := NormalizeQoderToolCalls(message.Raw["tool_calls"])
			keptTools := make([]any, 0, len(tools))
			keptReplies := make([]QoderMessage, 0, len(tools))
			for _, rawTool := range tools {
				tool, ok := rawTool.(map[string]any)
				if !ok {
					continue
				}
				id := QoderToolCallIDFromMap(tool)
				if id == "" {
					continue
				}
				reply, ok := replies[id]
				if !ok {
					continue
				}
				keptTools = append(keptTools, tool)
				keptReplies = append(keptReplies, reply)
			}
			// Removing unanswered calls can leave useful assistant text. Retain that text
			// with a copied raw map, while an empty call-only turn contributes no
			// outgoing history.
			if len(keptTools) == 0 {
				if QoderMessageHasText(message) {
					out = append(out, QoderMessageWithoutToolCalls(message))
				}
				continue
			}
			message.Raw = QoderCopyRawMap(message.Raw)
			message.Raw["tool_calls"] = keptTools
			out = append(out, message)
			out = append(out, keptReplies...)
		default:
			out = append(out, message)
		}
	}
	return out
}

// QoderMessageHasText reports nonblank message text or the presence of
// text-content blocks.
func QoderMessageHasText(message QoderMessage) bool {
	return strings.TrimSpace(message.Text) != "" || len(message.TextContent) > 0
}

// QoderMessageWithoutToolCalls removes tool_calls from a shallow copy of the raw
// map. Nested values and the message's text-block references remain shared;
// callers use it when retaining visible text from an unmatched assistant turn.
func QoderMessageWithoutToolCalls(message QoderMessage) QoderMessage {
	message.Raw = QoderCopyRawMap(message.Raw)
	delete(message.Raw, "tool_calls")
	return message
}

// QoderCopyRawMap returns a new top-level map containing the supplied raw
// entries. Nested values remain shared, and nil or empty input produces an
// editable empty map for message normalization.
func QoderCopyRawMap(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	copied := make(map[string]any, len(raw))
	maps.Copy(copied, raw)
	return copied
}

// QoderResponsesContentText reads strings or joins supported Responses text
// parts with newlines. Empty/null or malformed input becomes empty text;
// input_text, output_text, and text parts contribute to visible history.
func QoderResponsesContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	// Responses content may be a string or typed text parts. Decode the supported
	// part array after string recognition so system and visible text extraction
	// share one rule.
	var parts []protocolopenai.ResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "input_text", "output_text", "text":
				if part.Text != "" {
					out = append(out, part.Text)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// QoderMessagesFromAnthropicMessages converts ordered Messages turns and expands
// their resulting Qoder messages. A content conversion error stops the
// operation, while one user turn can produce alternating text and tool-result
// messages.
func QoderMessagesFromAnthropicMessages(messages []protocolanthropic.AnthropicMessage) ([]QoderMessage, error) {
	out := make([]QoderMessage, 0, len(messages))
	for _, message := range messages {
		converted, err := QoderMessagesFromAnthropicMessage(message)
		if err != nil {
			return nil, err
		}
		out = append(out, converted...)
	}
	return out, nil
}

// QoderMessagesFromAnthropicMessage converts one Messages turn while retaining
// text/tool-result order. Non-user turns use the assistant content converter.
// String user content becomes one message; unsupported blocks return errors.
//
// Text is buffered until a tool result with an ID appears, then emitted before
// that result. Results lacking an ID contribute text instead of inventing call
// identity. Text blocks retain cache-control maps, thinking history is omitted,
// and a turn with no visible output still keeps its role as an empty history
// message.
func QoderMessagesFromAnthropicMessage(message protocolanthropic.AnthropicMessage) ([]QoderMessage, error) {
	if message.Role != "user" {
		text, raw, err := QoderAnthropicMessageTextAndRaw(message)
		if err != nil {
			return nil, err
		}
		return []QoderMessage{{Role: message.Role, Text: text, Raw: raw}}, nil
	}

	blocks, ok, err := QoderAnthropicContentBlocks(message.Content)
	if err != nil {
		return nil, err
	}
	if !ok {
		text := QoderAnthropicContentText(message.Content)
		return []QoderMessage{{Role: message.Role, Text: text, Raw: map[string]any{"role": message.Role, "content": text}}}, nil
	}

	var out []QoderMessage
	var textParts []string
	var textContent []map[string]any
	// Text and identified tool results have different Gateway roles. Flush buffered
	// text before appending a result so their order in the original user turn
	// remains visible.
	flushText := func() {
		text := strings.Join(NonEmptyStrings(textParts), "\n")
		contents := textContent
		textParts = nil
		textContent = nil
		if text == "" {
			return
		}
		out = append(out, QoderMessage{
			Role:        "user",
			Text:        text,
			Raw:         map[string]any{"role": "user", "content": text},
			TextContent: contents,
		})
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			textContent = append(textContent, QoderTextContentBlock(block.Text, block.CacheControl))
		case "tool_result":
			toolText := QoderAnthropicToolResultText(block.Content)
			// A result without tool_use_id cannot identify its owning call. Keep its
			// readable text with the user turn instead of constructing an unrelated
			// Gateway result.
			if strings.TrimSpace(block.ToolUseID) == "" {
				if toolText != "" {
					textParts = append(textParts, toolText)
				}
				continue
			}
			flushText()
			if toolText != "" {
				out = append(out, QoderMessage{
					Role:       "tool",
					Text:       toolText,
					ToolCallID: block.ToolUseID,
					Raw: map[string]any{
						"role":         "tool",
						"tool_call_id": block.ToolUseID,
						"content":      toolText,
					},
				})
			}
		case "tool_use":
			// User tool-use blocks do not become visible text. Assistant conversion owns
			// call metadata, so this path leaves result and text ordering to their own
			// blocks.
		case "thinking", "redacted_thinking":
			// Thinking and redacted-thinking blocks carry protocol-specific history that
			// this converter omits, keeping replayed user content limited to text and
			// tool results.
		default:
			return nil, fmt.Errorf("unsupported content block type: %v", block.Type)
		}
	}
	flushText()
	if len(out) == 0 {
		return []QoderMessage{{Role: message.Role, Raw: map[string]any{"role": message.Role}}}, nil
	}
	return out, nil
}

// QoderAnthropicMessageTextAndRaw converts text and assistant tool-use blocks
// into raw history. String content becomes direct text; unsupported blocks
// return errors, and thinking or redacted-thinking blocks are omitted.
//
// Visible text is flattened separately while raw tool-use blocks retain ID,
// name, and decoded input. CopyQoderToolFields later converts those blocks into
// Qoder calls, preserving tool identity even when the assistant's visible text
// is empty. Missing or invalid input uses the supplied empty-object
// representation.
func QoderAnthropicMessageTextAndRaw(message protocolanthropic.AnthropicMessage) (string, map[string]any, error) {
	blocks, ok, err := QoderAnthropicContentBlocks(message.Content)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		text := QoderAnthropicContentText(message.Content)
		return text, map[string]any{"role": message.Role, "content": text}, nil
	}
	textParts := make([]string, 0, len(blocks))
	rawBlocks := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			rawBlocks = append(rawBlocks, map[string]any{"type": "text", "text": block.Text})
		case "tool_use":
			rawBlocks = append(rawBlocks, map[string]any{
				"type":  "tool_use",
				"id":    block.ID,
				"name":  block.Name,
				"input": QoderRawMessageOrDefault(block.Input, map[string]any{}),
			})
		case "thinking", "redacted_thinking":
			// Thinking history has no outgoing representation in this converter. Keep it
			// out of visible assistant text and call metadata while retaining adjacent
			// tool-use blocks.
		default:
			return "", nil, fmt.Errorf("unsupported content block type: %v", block.Type)
		}
	}
	return strings.Join(NonEmptyStrings(textParts), "\n"), map[string]any{"role": message.Role, "content": rawBlocks}, nil
}

// QoderAnthropicContentBlocks distinguishes block-array content from string,
// empty, or null content. A string reports ok=false with no error, while
// malformed nonstring input returns the array decoding error for callers that
// validate content.
func QoderAnthropicContentBlocks(raw json.RawMessage) ([]protocolanthropic.AnthropicContentBlock, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return nil, false, nil
	}
	var blocks []protocolanthropic.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false, err
	}
	return blocks, true, nil
}

// QoderAnthropicContentText flattens supported Messages text and tool-result
// content with newlines. String input is retained; absent, malformed, or
// unsupported representations contribute empty text for fallback user-message
// conversion.
func QoderAnthropicContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	blocks, ok, err := QoderAnthropicContentBlocks(raw)
	// This tolerant text reader returns no text for unsupported content. The
	// validating message converter handles content errors before callers use this
	// helper's fallback result.
	if err != nil || !ok {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "tool_result":
			parts = append(parts, QoderAnthropicToolResultText(block.Content))
		}
	}
	return strings.Join(NonEmptyStrings(parts), "\n")
}

// QoderAnthropicToolResultText reads string results or concatenates text blocks
// without separators. Absent/null or malformed result content becomes empty
// text, and non-text blocks do not contribute to the converted tool result.
func QoderAnthropicToolResultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []protocolanthropic.AnthropicContentBlock
	// Unreadable result content supplies no supported text blocks. Return empty
	// text so the owning converter can choose its empty-result or role-preserving
	// representation.
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

// QoderResponsesRole maps default/user and assistant roles while omitting
// control roles. ParseQoderResponsesPayload already includes developer/system
// items in system text; other role strings remain supplied for downstream
// history conversion.
func QoderResponsesRole(role string) string {
	switch strings.TrimSpace(role) {
	case "developer", "system":
		// ParseQoderResponsesPayload has already collected these control-role items
		// into system text. Omit their history role so instructions are not replayed
		// twice.
		return ""
	case "assistant":
		return "assistant"
	case "", "user":
		return "user"
	default:
		return role
	}
}

// QoderToolCallMap builds a function-call map with the supplied ID, name, and
// argument text. Blank arguments select an empty JSON object string, giving
// later result pairing a complete argument representation.
func QoderToolCallMap(id, name, arguments string) map[string]any {
	if strings.TrimSpace(arguments) == "" {
		// Blank function arguments need a complete empty-object representation for
		// later client output. Keep that marker separate from call identity, which is
		// supplied by ID and name.
		arguments = "{}"
	}
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	}
}

// QoderChatCompletionsTools combines modern raw tools with converted legacy
// functions and applies tool choice. The outer modern tool slice is copied,
// while its nested maps remain shared with the raw request.
func QoderChatCompletionsTools(req protocolopenai.ChatCompletionsRequest, rawReq map[string]any) []any {
	tools := append([]any(nil), QoderAnySlice(rawReq["tools"])...)
	for _, fn := range req.Functions {
		if tool, ok := QoderChatFunctionToQoderTool(fn); ok {
			tools = append(tools, tool)
		}
	}
	return QoderApplyChatToolChoice(tools, req.ToolChoice, req.FunctionCall)
}

// QoderChatFunctionToQoderTool converts a named legacy function into a
// function-tool definition. Blank names return false; absent or malformed
// parameters use an object schema, and supplied description and strict values
// are retained.
func QoderChatFunctionToQoderTool(fn protocolopenai.ChatFunction) (map[string]any, bool) {
	name := strings.TrimSpace(fn.Name)
	if name == "" {
		return nil, false
	}
	function := map[string]any{
		"name":       name,
		"parameters": QoderRawMessageOrDefault(fn.Parameters, map[string]any{"type": "object", "properties": map[string]any{}}),
	}
	if strings.TrimSpace(fn.Description) != "" {
		function["description"] = fn.Description
	}
	if fn.Strict != nil {
		function["strict"] = *fn.Strict
	}
	return map[string]any{
		"type":     "function",
		"function": function,
	}, true
}

// QoderToolChoice translates public protocol tool-selection intent into the
// OpenAI-shaped policy consumed by the desktop parameters object. Missing or
// unrecognized controls remain absent, while none, auto, and required retain
// their meanings independently from whether tool definitions are replayed.
//
// Anthropic any becomes required, and named Anthropic or Responses selections
// become the same nested function choice as Chat. Keeping this policy separate
// from the declared-tool list lets a continuation prohibit another tool call
// without discarding definitions needed by the upstream conversation history.
func QoderToolChoice(value any) any {
	if text, ok := value.(string); ok {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "none", "auto", "required":
			return strings.ToLower(strings.TrimSpace(text))
		}
		return nil
	}
	choice, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	kind := QoderStringField(choice, "type")
	switch kind {
	case "none", "auto":
		return kind
	case "any":
		return "required"
	case "allowed_tools":
		return choice
	}
	name := QoderStringField(choice, "name")
	if function, ok := choice["function"].(map[string]any); ok {
		name = QoderStringField(function, "name")
	}
	if name != "" && (kind == "" || kind == "tool" || kind == "function") {
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	return nil
}

// restrictQoderFunctions translates an explicit allowed-functions subset into
// native tool declarations and the corresponding auto or required choice.
// Chat nests the policy under allowed_tools; Responses places the same fields
// beside type. Every selected name must match a declared function in this turn.
//
// Filtering before fingerprinting keeps the session's effective tool set equal
// to the outgoing definitions. Preserve their original order and reject choices
// that the function-only native adapter cannot encode rather than expanding an
// unknown subset to every declared tool.
func restrictQoderFunctions(tools []any, choice map[string]any) ([]any, any, error) {
	invalid := &RequestError{Code: "unsupported_tool_choice", Message: "Select declared Qoder functions and an auto or required allowed_tools mode."}
	if nested, present := choice["allowed_tools"]; present {
		var ok bool
		choice, ok = nested.(map[string]any)
		if !ok {
			return nil, nil, invalid
		}
	}
	mode := QoderStringField(choice, "mode")
	if mode != "auto" && mode != "required" {
		return nil, nil, invalid
	}
	allowed := map[string]bool{}
	for _, item := range QoderAnySlice(choice["tools"]) {
		entry, ok := item.(map[string]any)
		name := QoderDeclaredToolName(entry)
		if !ok || name == "" || QoderStringField(entry, "type") != "function" {
			return nil, nil, invalid
		}
		allowed[name] = true
	}
	if len(allowed) == 0 {
		return nil, nil, invalid
	}
	selected := make([]any, 0, len(allowed))
	for _, tool := range tools {
		name := QoderDeclaredToolName(tool)
		if _, ok := allowed[name]; ok {
			selected = append(selected, tool)
			allowed[name] = false
		}
	}
	for _, missing := range allowed {
		if missing {
			return nil, nil, invalid
		}
	}
	return selected, mode, nil
}

// QoderApplyChatToolChoice filters declared tools by a selected name or returns
// the usable set. Disabled choice produces an empty slice, unnamed choice
// retains all tools, and an unknown selected name retains the declarations for
// upstream handling.
func QoderApplyChatToolChoice(tools []any, toolChoice json.RawMessage, functionCall json.RawMessage) []any {
	if len(tools) == 0 {
		return []any{}
	}
	name, disabled := QoderChatToolChoiceTarget(toolChoice, functionCall)
	if disabled {
		return []any{}
	}
	if name == "" {
		return tools
	}
	filtered := make([]any, 0, len(tools))
	for _, tool := range tools {
		if strings.EqualFold(QoderDeclaredToolName(tool), name) {
			filtered = append(filtered, tool)
		}
	}
	// An unknown selected name provides no usable filtered tool set. Keep the
	// declarations so the upstream request can handle that choice with its
	// available tools.
	if len(filtered) == 0 {
		return tools
	}
	return filtered
}

// QoderChatToolChoiceTarget selects recognized modern tool_choice before
// legacy function_call. A missing or malformed modern representation permits
// legacy selection; recognized none disables tools immediately, and auto or
// required retains an unnamed enabled choice.
//
// A decoded modern object counts as recognized even when it has no supported
// name fields. That empty selection therefore retains modern precedence
// instead of falling through to a legacy request. The returned name and
// disable flag let QoderApplyChatToolChoice decide whether to filter, retain,
// or remove declarations.
func QoderChatToolChoiceTarget(toolChoice json.RawMessage, functionCall json.RawMessage) (string, bool) {
	if name, disabled, ok := QoderParseChatToolChoice(toolChoice, true); ok {
		return name, disabled
	}
	if name, disabled, ok := QoderParseChatToolChoice(functionCall, false); ok {
		return name, disabled
	}
	return "", false
}

// QoderParseChatToolChoice returns a selected name, disable flag, and
// recognition flag for modern or legacy input. Empty, null, or malformed
// representations are unrecognized. String none disables tools, auto and
// required retain the set, and other strings become trimmed names.
//
// Any decoded object is recognized. Legacy objects read name; modern objects
// accept type=none, prefer top-level name, then nested function.name. An
// object lacking those fields returns an unnamed enabled choice, which still
// prevents the caller from consulting a lower-precedence legacy choice.
func QoderParseChatToolChoice(raw json.RawMessage, modern bool) (string, bool, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false, false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "none":
			return "", true, true
		case "auto", "required":
			return "", false, true
		default:
			return strings.TrimSpace(value), false, true
		}
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", false, false
	}
	if !modern {
		return QoderStringField(obj, "name"), false, true
	}
	if strings.EqualFold(QoderStringField(obj, "type"), "none") {
		return "", true, true
	}
	if name := QoderStringField(obj, "name"); name != "" {
		return name, false, true
	}
	if fn, ok := obj["function"].(map[string]any); ok {
		return QoderStringField(fn, "name"), false, true
	}
	return "", false, true
}

// QoderResponsesToolsToQoderTools converts named function tools into Qoder
// function definitions. Unsupported types and blank names are skipped; absent
// parameters use an object schema, and description/strict values remain
// available.
func QoderResponsesToolsToQoderTools(tools []protocolopenai.ResponsesTool) []any {
	if len(tools) == 0 {
		return []any{}
	}
	converted := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		function := map[string]any{
			"name":       tool.Name,
			"parameters": QoderRawMessageOrDefault(tool.Parameters, map[string]any{"type": "object", "properties": map[string]any{}}),
		}
		if tool.Description != "" {
			function["description"] = tool.Description
		}
		if tool.Strict != nil {
			function["strict"] = *tool.Strict
		}
		converted = append(converted, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return converted
}

// QoderDeclaredToolNameMapper returns a mapper from normalized aliases to
// client-declared tool names. No usable declarations returns nil. Alias
// construction resolves collisions in declaration order, with the last name
// recorded for a key.
//
// The returned callback trims and normalizes each lookup key, then returns the
// declared spelling when present. Unknown or blank incoming names retain their
// supplied text. The alias map belongs to this callback after construction, so
// subsequent changes to the input declaration maps do not rebuild its
// selections.
func QoderDeclaredToolNameMapper(tools []any) QoderToolNameMapper {
	aliases := QoderDeclaredToolNameAliases(tools)
	if len(aliases) == 0 {
		return nil
	}
	return func(name string) string {
		key := QoderToolNameKey(name)
		if key == "" {
			return name
		}
		if mapped := aliases[key]; mapped != "" {
			return mapped
		}
		return name
	}
}

// QoderDeclaredToolNameAliases builds normalized alias keys for the supplied
// tool definitions. Unnamed declarations are skipped and later declarations
// replace colliding keys; no usable alias returns nil for optional mapping.
func QoderDeclaredToolNameAliases(tools []any) map[string]string {
	aliases := map[string]string{}
	for _, raw := range tools {
		name := QoderDeclaredToolName(raw)
		if name == "" {
			continue
		}
		for _, alias := range QoderToolNameAliases(name) {
			key := QoderToolNameKey(alias)
			if key != "" {
				aliases[key] = name
			}
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

// QoderDeclaredToolName reads a trimmed top-level tool name before nested
// function.name. Non-object or unnamed declarations produce empty text, allowing
// alias construction and selection to skip unusable entries.
func QoderDeclaredToolName(raw any) string {
	tool, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if name := strings.TrimSpace(QoderStringField(tool, "name")); name != "" {
		return name
	}
	function, _ := tool["function"].(map[string]any)
	return strings.TrimSpace(QoderStringField(function, "name"))
}

// QoderToolNameAliases returns the declared spelling and recognized Qoder
// compatibility names. Blank input returns nil; known shell/file/search/todo
// tools add the supported aliases, while other names keep their trimmed spelling
// alone.
func QoderToolNameAliases(name string) []string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	aliases := []string{trimmed}
	switch strings.ToLower(trimmed) {
	case "bash":
		aliases = append(aliases, "Bash", "shell", "execute_bash")
	case "read":
		aliases = append(aliases, "Read")
	case "write":
		aliases = append(aliases, "Write")
	case "edit":
		aliases = append(aliases, "Edit")
	case "multiedit", "multi_edit":
		aliases = append(aliases, "MultiEdit", "multi_edit")
	case "grep":
		aliases = append(aliases, "Grep")
	case "glob":
		aliases = append(aliases, "Glob")
	case "ls":
		aliases = append(aliases, "LS", "Ls")
	case "todowrite", "todo_write":
		aliases = append(aliases, "TodoWrite", "todo_write")
	}
	return aliases
}

// QoderToolNameKey lowercases trimmed tool names and removes underscores for
// alias matching.
func QoderToolNameKey(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", ""))
}

// QoderRawMessageOrDefault decodes JSON or returns the caller's fallback for
// absent or invalid content. The fallback value is returned directly, so callers
// control its shape and any mutable sharing.
func QoderRawMessageOrDefault(raw json.RawMessage, fallback any) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return fallback
	}
	var value any
	// Tool schema and input callers supply the fallback shape they accept. Failed
	// JSON decoding returns that supplied object directly rather than introducing
	// another representation.
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
}

// QoderPayloadMessageFromMessage builds canonical desktop message content and
// retains tool identity. User text uses content blocks so cache annotations
// remain attached to their text; other roles retain scalar content unless
// explicit text blocks were supplied. The obsolete text-only contents field
// is not emitted because current routes consume the canonical content field.
//
// Tool history is normalized from raw calls or Anthropic tool-use blocks.
// Explicit ToolCallID wins over raw aliases so call/result association remains
// coherent. Each emitted text block receives its own map before cache markers
// are added, keeping payload construction from mutating request-owned blocks
// retained by conversation fingerprinting and retry preparation.
func QoderPayloadMessageFromMessage(message QoderMessage) map[string]any {
	msg := map[string]any{"role": message.Role, "content": message.Text}
	if len(message.TextContent) > 0 {
		content := make([]any, 0, len(message.TextContent))
		for _, block := range message.TextContent {
			content = append(content, QoderCopyRawMap(block))
		}
		msg["content"] = content
	} else if message.Role == "user" && message.Text != "" {
		msg["content"] = []any{map[string]any{"type": "text", "text": message.Text}}
	}
	CopyQoderToolFields(msg, message.Raw)
	if strings.TrimSpace(message.ToolCallID) != "" {
		toolCallID := strings.TrimSpace(message.ToolCallID)
		msg["tool_call_id"] = toolCallID
		msg["tool_call_call_id"] = toolCallID
	}
	return msg
}

// QoderTextContentBlock returns a text block with an optional copied
// cache-control type. The returned maps are newly allocated; callers can later
// add the ephemeral marker during payload construction.
func QoderTextContentBlock(text string, cacheControl *protocolanthropic.AnthropicCacheControl) map[string]any {
	block := map[string]any{"type": "text", "text": text}
	if cacheControl != nil {
		block["cache_control"] = map[string]any{"type": cacheControl.Type}
	}
	return block
}

// CopyQoderToolFields writes normalized call history and result identity into
// a usable outgoing message map. Empty raw input leaves the map as supplied.
// Modern tool_calls take precedence when usable; otherwise Anthropic tool-use
// blocks supply function history.
//
// Result identity prefers tool_call_id, tool_call_call_id, then call_id after
// trimming. The selected value fills both Gateway result fields so they refer
// to one invocation. A nonblank string name retains its supplied spelling.
// Normalized call maps are allocated, while this operation mutates the
// caller's outgoing map.
func CopyQoderToolFields(out map[string]any, raw map[string]any) {
	if len(raw) == 0 {
		return
	}
	if toolCalls := NormalizeQoderToolCalls(raw["tool_calls"]); len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	} else if toolCalls := AnthropicToolUseBlocksToQoderToolCalls(raw["content"]); len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	}
	// Raw result histories use several ID spellings. Resolve them once and mirror
	// that choice into both Gateway result fields so they identify one invocation.
	if toolCallID := FirstNonEmptyQoder(
		QoderStringField(raw, "tool_call_id"),
		QoderStringField(raw, "tool_call_call_id"),
		QoderStringField(raw, "call_id"),
	); toolCallID != "" {
		out["tool_call_id"] = toolCallID
		out["tool_call_call_id"] = toolCallID
	}
	if name, ok := raw["name"].(string); ok && strings.TrimSpace(name) != "" {
		out["name"] = name
	}
}

// EnrichQoderToolResultMessages mutates result messages with call IDs and names
// learned while walking history. It indexes each raw message before enriching
// that message, retaining the latest
// name per call ID. Existing result name and explicit ToolCallID remain
// authoritative.
//
// A tool result can therefore gain the owning assistant call's name even when
// the client omitted it. The message slice and raw maps are modified in place,
// creating a raw map when necessary; callers finish enrichment before sharing
// the request with conversation planning.
func EnrichQoderToolResultMessages(messages []QoderMessage) []QoderMessage {
	toolNamesByID := map[string]string{}
	for i := range messages {
		for id, name := range QoderToolCallNamesFromRaw(messages[i].Raw) {
			if id != "" && name != "" {
				toolNamesByID[id] = name
			}
		}
		if messages[i].Role != "tool" {
			continue
		}
		toolCallID := QoderMessageToolCallID(messages[i])
		if toolCallID == "" {
			continue
		}
		if strings.TrimSpace(messages[i].ToolCallID) == "" {
			messages[i].ToolCallID = toolCallID
		}
		if QoderStringField(messages[i].Raw, "name") != "" {
			continue
		}
		name := toolNamesByID[toolCallID]
		if name == "" {
			continue
		}
		if messages[i].Raw == nil {
			messages[i].Raw = map[string]any{}
		}
		messages[i].Raw["name"] = name
	}
	return messages
}

// QoderToolCallIDFromMap selects trimmed id, tool_call_id, then call_id from
// call metadata. Missing or entirely blank fields return empty text so
// Responses pairing can reject a call lacking identity.
func QoderToolCallIDFromMap(tool map[string]any) string {
	return FirstNonEmptyQoder(
		QoderStringField(tool, "id"),
		QoderStringField(tool, "tool_call_id"),
		QoderStringField(tool, "call_id"),
	)
}

// NormalizeQoderToolCalls converts usable nested function calls into newly
// allocated wire maps. Missing name and arguments drops an entry, absent type
// becomes function, and argument values are serialized through
// QoderToolArgumentsString.
func NormalizeQoderToolCalls(raw any) []any {
	tools := QoderAnySlice(raw)
	if len(tools) == 0 {
		return []any{}
	}
	normalized := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, _ := tool["function"].(map[string]any)
		name := QoderStringField(function, "name")
		arguments := QoderToolArgumentsString(function["arguments"])
		if name == "" && strings.TrimSpace(arguments) == "" {
			continue
		}
		id := QoderStringField(tool, "id")
		callType := QoderStringField(tool, "type")
		if callType == "" {
			// Call type is optional in imported history. Supply function for downstream
			// output while retaining the actual ID and name fields used to associate
			// results.
			callType = "function"
		}
		normalized = append(normalized, map[string]any{
			"id":   id,
			"type": callType,
			"function": map[string]any{
				"name":      name,
				"arguments": arguments,
			},
		})
	}
	return normalized
}

// AnthropicToolUseBlocksToQoderToolCalls converts named tool_use blocks into
// function-call maps. Other content and blank names are skipped; IDs remain with
// their serialized inputs for later tool-result association.
func AnthropicToolUseBlocksToQoderToolCalls(raw any) []any {
	blocks := QoderAnySlice(raw)
	if len(blocks) == 0 {
		return []any{}
	}
	toolCalls := make([]any, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		name := QoderStringField(block, "name")
		if name == "" {
			continue
		}
		toolCalls = append(toolCalls, map[string]any{
			"id":   QoderStringField(block, "id"),
			"type": "function",
			"function": map[string]any{
				"name":      name,
				"arguments": QoderToolArgumentsString(block["input"]),
			},
		})
	}
	return toolCalls
}

// qoderPayloadFingerprint returns lowercase SHA-256 text, with empty text for
// zero-length payloads. Callers use that empty result to distinguish absent
// serialized data while comparing conversation fingerprints.
func qoderPayloadFingerprint(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
