package antigravity

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// payload.go prepares Antigravity request envelopes, identity instructions,
// schemas, and token-limit overrides.
// Antigravity generation payloads carry a project and internal model route
// outside the Gemini request object. The envelope carries the model supplied by
// the caller alongside a new agent request ID and a fixed client identity.
//
// Identity injection and schema cleanup decode mutable JSON maps and reencode
// when their conditions require changes. Existing camelCase declaration arrays take
// precedence, and mixed Search/function declarations enable server-side
// invocations. Token limits preserve max_completion_tokens presence precedence
// even when its value prevents a positive override. Directly supplied schema
// maps can be mutated by bridge cleaners; byte wrappers work on separately
// decoded objects.

var (
	// AntigravityPassthroughErrorMessages lists lowercase fragments that permit upstream
	// text to reach clients through substring matching in local error handling.
	AntigravityPassthroughErrorMessages = []string{
		"prompt is too long",
	}

	// ErrProjectIDRequired reports a missing trimmed project for the generic
	// v1internal wrapper, before generation data can be serialized for sending.
	ErrProjectIDRequired = errors.New("this standard-tier Antigravity provider requires project_id")

	// DefaultAntigravityModelMapping is an allocated empty map so the admin
	// defaults endpoint encodes it as a JSON object.
	DefaultAntigravityModelMapping = map[string]string{}
)

// PromptTooLongError retains a prompt-length response status, request
// identity, and captured body.
type PromptTooLongError struct {
	StatusCode int    // upstream HTTP status code
	RequestID  string // upstream response request identifier
	Body       []byte // captured upstream error bytes
}

// Error returns the prompt-length failure label and its upstream HTTP status.
func (e *PromptTooLongError) Error() string {
	return fmt.Sprintf("prompt too long: status=%d", e.StatusCode)
}

// ApplyThinkingModelSuffix selects the Sonnet 4.5 thinking name when enabled
// and the mapped name exactly matches its base. Other names and disabled
// thinking retain their supplied model identity.
func ApplyThinkingModelSuffix(mappedModel string, thinkingEnabled bool) string {
	if !thinkingEnabled {
		return mappedModel
	}
	if mappedModel == "claude-sonnet-4-5" {
		return "claude-sonnet-4-5-thinking"
	}
	return mappedModel
}

// InjectIdentityPatchToGeminiRequest adds the default identity before existing
// camelCase systemInstruction parts. Callers supply a nonnull JSON object;
// decoded numeric values use float64 when a changed request is reencoded.
//
// A text part containing the identity marker anywhere selects the supplied
// bytes unchanged. Otherwise existing parts follow the injected part, while
// absent or differently shaped instruction/parts values are replaced. This
// ordering keeps valid user instructions after the platform identity without
// appending another identity to a marked request.
func InjectIdentityPatchToGeminiRequest(body []byte) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("parse Gemini request: %w", err)
	}

	if sysInst, ok := request["systemInstruction"].(map[string]any); ok {
		if parts, ok := sysInst["parts"].([]any); ok {
			for _, part := range parts {
				if partMap, ok := part.(map[string]any); ok {
					if text, ok := partMap["text"].(string); ok {
						if strings.Contains(text, "You are Antigravity") {
							return body, nil
						}
					}
				}
			}
		}
	}

	identityPatch := GetDefaultIdentityPatch()

	// Prepend the identity to existing parts so user instructions remain present;
	// a supplied marker selects unchanged bytes before any reserialization
	// occurs.
	newPart := map[string]any{"text": identityPatch}

	if existing, ok := request["systemInstruction"].(map[string]any); ok {
		if parts, ok := existing["parts"].([]any); ok {
			existing["parts"] = append([]any{newPart}, parts...)
		} else {
			existing["parts"] = []any{newPart}
		}
	} else {
		request["systemInstruction"] = map[string]any{
			"parts": []any{newPart},
		}
	}

	return json.Marshal(request)
}

// WrapV1InternalRequest adds project/model routing and a fresh agent ID around
// decoded request data. projectID is trimmed and required; request JSON can be
// any decoded value, and reencoding uses ordinary float64 numbers.
func WrapV1InternalRequest(projectID, model string, originalBody []byte) ([]byte, error) {
	var request any
	if err := json.Unmarshal(originalBody, &request); err != nil {
		return nil, fmt.Errorf("parse request body: %w", err)
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, ErrProjectIDRequired
	}

	wrapped := map[string]any{
		"project":     projectID,
		"requestId":   "agent-" + uuid.New().String(),
		"userAgent":   "antigravity", // fixed protocol identity
		"requestType": "agent",
		"model":       model,
		"request":     request,
	}

	return json.Marshal(wrapped)
}

// IsSignatureRelatedError searches extracted platform text or the raw body for
// signature and expected-thinking wording. Matching is case-insensitive and
// broad; callers use HTTP status and policy to decide whether it permits
// recovery.
func IsSignatureRelatedError(respBody []byte) bool {
	msg := strings.ToLower(strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody)))
	if msg == "" {
		// Search captured body text when structured extraction is blank so
		// compatible or malformed envelopes can still supply a signature recovery
		// marker.
		msg = strings.ToLower(string(respBody))
	}

	// Accept both signature and thought_signature wording.
	if strings.Contains(msg, "thought_signature") || strings.Contains(msg, "signature") {
		return true
	}

	// Expected-thinking wording also selects the signature-related category here;
	// the response owner still checks status and policy before attempting recovery.
	if strings.Contains(msg, "expected") && (strings.Contains(msg, "thinking") || strings.Contains(msg, "redacted_thinking")) {
		return true
	}

	return false
}

// IsPromptTooLongError recognizes prompt/context length or max_tokens wording
// in platform text. Blank extraction searches the raw body; callers determine
// status and model-fallback eligibility separately from this textual
// classifier.
func IsPromptTooLongError(respBody []byte) bool {
	msg := strings.ToLower(strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody)))
	if msg == "" {
		// Search the captured body when structured extraction is blank so an
		// unrecognized envelope can still provide a prompt-length recovery marker.
		msg = strings.ToLower(string(respBody))
	}
	return strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "request is too long") ||
		strings.Contains(msg, "context length exceeded") ||
		strings.Contains(msg, "max_tokens")
}

// IsPassthroughErrorMessage reports whether case-folded text contains a
// locally allowed message pattern.
func IsPassthroughErrorMessage(msg string) bool {
	lower := strings.ToLower(msg)
	for _, pattern := range AntigravityPassthroughErrorMessages {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// GetPassthroughOrDefault returns upstream text when its case-folded spelling
// contains an allowed pattern. Otherwise it returns defaultMsg, letting caller
// error handling retain fixed client-facing text for other failures.
func GetPassthroughOrDefault(upstreamMsg, defaultMsg string) string {
	if IsPassthroughErrorMessage(upstreamMsg) {
		return upstreamMsg
	}
	return defaultMsg
}

// CleanGeminiRequest cleans decoded object schemas under Gemini function
// declarations. Invalid JSON returns an error; a request without a selected
// object parameters map returns its supplied bytes.
//
// An existing camelCase declaration array takes precedence over snake_case
// even when empty. Selected parameter maps pass through undefined removal and
// schema adaptation, and processing any such map selects request reencoding
// even if the cleaner makes no visible change. Other tool entries retain their
// decoded values, including float64 numbers, within that reencoded request.
// Schema cleanup requires terminating references and comparable entries in
// union required arrays; valid JSON alone does not establish those conditions.
func CleanGeminiRequest(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	modified := false

	if tools, ok := payload["tools"].([]any); ok && len(tools) > 0 {
		for _, t := range tools {
			toolMap, ok := t.(map[string]any)
			if !ok {
				continue
			}

			// Prefer an existing camelCase declaration array over snake_case so cleanup
			// uses one selected schema source; unchanged tools bypass request
			// reserialization.
			var funcs []any
			if f, ok := toolMap["functionDeclarations"].([]any); ok {
				funcs = f
			} else if f, ok := toolMap["function_declarations"].([]any); ok {
				funcs = f
			}

			if len(funcs) == 0 {
				continue
			}

			for _, f := range funcs {
				funcMap, ok := f.(map[string]any)
				if !ok {
					continue
				}

				if params, ok := funcMap["parameters"].(map[string]any); ok {
					DeepCleanUndefined(params)
					cleaned := CleanJSONSchema(params)
					funcMap["parameters"] = cleaned
					modified = true
				}
			}
		}
	}

	if !modified {
		return body, nil
	}

	return json.Marshal(payload)
}

// PreserveChatCompletionTokenLimit copies the selected positive Chat token
// limit into claudeRequest. Nil request pointers leave the destination
// unchanged, and an absent limit supplies no override.
//
// Selection uses max_completion_tokens presence before testing positivity. For
// example, max_tokens=100 with max_completion_tokens=0 keeps the existing
// Claude value rather than copying 100. A positive completion limit wins over
// max_tokens; when the completion field is absent, a positive max_tokens value
// becomes the converted limit.
func PreserveChatCompletionTokenLimit(request *protocolopenai.ChatCompletionsRequest, claudeRequest *protocolanthropic.AnthropicRequest) {
	if request == nil || claudeRequest == nil {
		return
	}
	limit := request.MaxTokens
	// Select by presence before positivity so an explicit completion limit can
	// suppress the older max_tokens value, matching the Chat request precedence
	// used by conversion.
	if request.MaxCompletionTokens != nil {
		limit = request.MaxCompletionTokens
	}
	if limit != nil && *limit > 0 {
		claudeRequest.MaxTokens = *limit
	}
}

// EnableMixedGeminiToolInvocations sets the server-side invocation flag when
// decoded tools contain both camelCase search-key presence and a nonempty
// function declaration array. Callers supply a nonnull JSON object, and
// invalid JSON returns an error.
//
// The observations can come from different tool entries. Search-key presence
// counts even when its value is null, while an empty declaration array does
// not count. Requests missing either observation return their supplied bytes;
// qualifying requests retain other object toolConfig fields and reencode the
// request after replacing any nonobject toolConfig.
func EnableMixedGeminiToolInvocations(body []byte) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}

	// Combine search presence and nonempty function declarations across tool
	// entries because either observation alone leaves server-side invocation
	// configuration untouched.
	var hasGoogleSearch, hasFunctionDeclarations bool
	if tools, ok := request["tools"].([]any); ok {
		for _, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			_, hasSearch := tool["googleSearch"]
			declarations, hasFunctions := tool["functionDeclarations"].([]any)
			hasGoogleSearch = hasGoogleSearch || hasSearch
			hasFunctionDeclarations = hasFunctionDeclarations || hasFunctions && len(declarations) > 0
		}
	}
	if !hasGoogleSearch || !hasFunctionDeclarations {
		return body, nil
	}

	toolConfig, _ := request["toolConfig"].(map[string]any)
	if toolConfig == nil {
		toolConfig = make(map[string]any)
		request["toolConfig"] = toolConfig
	}
	toolConfig["includeServerSideToolInvocations"] = true
	return json.Marshal(request)
}

// CleanJSONSchema delegates recursive schema adaptation and returns the
// bridge-selected map. Schema and nested maps can be mutated, so callers copy
// mutable schema data shared with other requests before cleanup. Acyclic
// containers, terminating references, and comparable union-required entries
// are required by the shared cleaner; malformed values can panic.
func CleanJSONSchema(schema map[string]any) map[string]any { return bridge.CleanJSONSchema(schema) }

// DeepCleanUndefined delegates recursive undefined-field removal to the
// bridge. Object maps within value can be mutated, so callers isolate mutable
// schema data they share with other requests before applying cleanup. The
// input needs acyclic ordinary maps and arrays because recursive traversal
// has no cycle guard.
func DeepCleanUndefined(value any) { bridge.DeepCleanUndefined(value) }
