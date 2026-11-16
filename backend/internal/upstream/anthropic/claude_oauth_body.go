package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// claude_oauth_body.go normalizes OAuth request bodies, system instructions,
// metadata, and cache markers.
// OAuth request preparation replaces recognizable client identity text and can
// rebuild the system prompt as configured Claude blocks. Migrated client
// instructions become joined text in an injected message block, with one
// selected cache marker attached to that same block. Other client system
// markers lose their separate positions during this conversion.
//
// Raw JSON field edits retain the surrounding request's layout. Configuration
// distinguishes invalid JSON from an empty block list, with the latter
// selecting defaults; disabled or blank configured blocks are skipped. Cache
// handling then removes thinking-block markers and reduces block markers in a
// defined priority order. These edits are best effort, so callers can receive
// partially normalized bodies when an individual JSON mutation fails.

// ClaudeFableOAuthSystemPromptBlocks supplies billing and Claude Code identity
// blocks for models matching the Fable selector. The template omits the general
// expansion; RewriteSystemForNonClaudeCodeWithPromptBlocks can still migrate
// eligible client instructions into history.
const ClaudeFableOAuthSystemPromptBlocks = `[
	{"type":"text","text":"{billing_header}"},
	{"type":"text","text":"{claude_code_system_prompt}"}
]`

// AnthropicCacheControlPayload encodes an optional lifetime for an ephemeral
// marker.
type AnthropicCacheControlPayload struct {
	Type string `json:"type"`          // cache directive type
	TTL  string `json:"ttl,omitempty"` // optional duration text
}

// AnthropicSystemTextBlockPayload represents system text with an optional cache
// marker.
type AnthropicSystemTextBlockPayload struct {
	Type         string                        `json:"type"`                    // content block type
	Text         string                        `json:"text"`                    // system instruction text
	CacheControl *AnthropicCacheControlPayload `json:"cache_control,omitempty"` // nil omits marker
}

// AnthropicMetadataPayload encodes the user identity string in request
// metadata.
type AnthropicMetadataPayload struct {
	UserID string `json:"user_id"` // encoded client identity
}

// ClaudeOAuthNormalizeOptions selects optional identity insertion and system
// cache-marker removal.
type ClaudeOAuthNormalizeOptions struct {
	// InjectMetadata and MetadataUserID jointly enable absent-identity insertion
	// in NormalizeClaudeOAuthRequestBody. Existing nonempty string identity takes
	// precedence over the supplied candidate.
	InjectMetadata bool   // identity insertion flag
	MetadataUserID string // candidate identity text
	// StripSystemCacheControl removes markers from system-array elements during
	// system normalization, including elements whose text is left unchanged.
	StripSystemCacheControl bool // system marker removal
}

// ClaudeOAuthSystemPromptBlockConfig describes one configurable text template
// and cache marker.
type ClaudeOAuthSystemPromptBlockConfig struct {
	// Enabled selects whether BuildClaudeOAuthSystemPromptBlocksJSON includes this
	// block; nil means enabled, while settings validation still checks disabled
	// blocks.
	Enabled *bool `json:"enabled,omitempty"` // optional inclusion switch
	// Type defaults to text after trimming. Settings validation rejects other
	// kinds for every block; rendering checks the kind of included blocks.
	Type string `json:"type,omitempty"` // configured block kind
	Text string `json:"text,omitempty"` // expandable instruction template
	// CacheControl accepts omitted, null, false, true, or object JSON; true
	// creates the default ephemeral marker during block rendering.
	CacheControl json.RawMessage `json:"cache_control,omitempty"` // raw marker configuration
}

// ClaudeOAuthSystemPromptBlocksEnvelope wraps ordered block configuration for
// object-form settings input.
type ClaudeOAuthSystemPromptBlocksEnvelope struct {
	// Blocks supplies render order; an empty decoded list selects defaults in
	// BuildClaudeOAuthSystemPromptBlocksJSON, while a nonempty disabled list stays
	// empty.
	Blocks []ClaudeOAuthSystemPromptBlockConfig `json:"blocks"` // ordered block configurations
}

// CacheControlPath pairs a removable cache path with its success log.
type CacheControlPath struct {
	Path string // sjson deletion path
	Log  string // successful removal message
}

// SanitizeSystemText replaces each occurrence of the canonical OpenCode
// identity sentence with the trimmed Claude Code banner. Empty input stays
// empty, and all surrounding client text remains in the returned string.
func SanitizeSystemText(text string) string {
	if text == "" {
		return text
	}
	// Restrict identity replacement to the full known OpenCode sentence so
	// surrounding application instructions retain their supplied text during this
	// compatibility rewrite.
	text = strings.ReplaceAll(
		text,
		"You are OpenCode, the best coding agent on the planet.",
		strings.TrimSpace(ClaudeCodeSystemPrompt),
	)
	return text
}

// MarshalAnthropicSystemTextBlock encodes a text block with optional ephemeral
// cache control. The cache flag selects DefaultCacheControlTTL; returned bytes
// form a complete JSON object for raw array assembly.
func MarshalAnthropicSystemTextBlock(text string, includeCacheControl bool) ([]byte, error) {
	block := AnthropicSystemTextBlockPayload{
		Type: "text",
		Text: text,
	}
	if includeCacheControl {
		block.CacheControl = &AnthropicCacheControlPayload{
			Type: "ephemeral",
			TTL:  DefaultCacheControlTTL,
		}
	}
	return json.Marshal(block)
}

// MarshalAnthropicSystemTextBlockWithCacheControl encodes a text block and an
// optional supplied cache value. Nil omits cache_control, while values that
// encoding/json cannot marshal return an error to the builder.
func MarshalAnthropicSystemTextBlockWithCacheControl(text string, cacheControl any) ([]byte, error) {
	block := map[string]any{
		"type": "text",
		"text": text,
	}
	if cacheControl != nil {
		block["cache_control"] = cacheControl
	}
	return json.Marshal(block)
}

// MarshalAnthropicMetadata encodes userID as the metadata object's user_id
// string.
func MarshalAnthropicMetadata(userID string) ([]byte, error) {
	return json.Marshal(AnthropicMetadataPayload{UserID: userID})
}

// BuildJSONArrayRaw joins supplied encoded items into a JSON array, returning
// [] for no items. Callers provide complete valid JSON values because this
// helper copies bytes without validating or re-encoding them.
func BuildJSONArrayRaw(items [][]byte) []byte {
	if len(items) == 0 {
		return []byte("[]")
	}

	total := 2
	for _, item := range items {
		total += len(item)
	}
	total += len(items) - 1

	buf := make([]byte, 0, total)
	buf = append(buf, '[')
	for i, item := range items {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, item...)
	}
	buf = append(buf, ']')
	return buf
}

// SetJSONValueBytes sets path through sjson's ordinary value encoding and
// returns whether that edit succeeded. An error returns the supplied body and
// false, leaving recovery decisions with its caller.
func SetJSONValueBytes(body []byte, path string, value any) ([]byte, bool) {
	// Normalizers advance their working bytes only on a true result, so a failed
	// value edit leaves prior successful request normalization in place.
	next, err := sjson.SetBytes(body, path, value)
	if err != nil {
		return body, false
	}
	return next, true
}

// SetJSONRawBytes installs raw JSON at path and reports success. Callers
// validate raw before supplying it; an edit failure returns the supplied body
// and false for the normalization caller.
func SetJSONRawBytes(body []byte, path string, raw []byte) ([]byte, bool) {
	// Raw edits carry serialized blocks into an existing request; failed insertion
	// returns false so callers can retain their current body or choose recovery.
	next, err := sjson.SetRawBytes(body, path, raw)
	if err != nil {
		return body, false
	}
	return next, true
}

// DeleteJSONPathBytes removes path and reports whether sjson accepted the edit.
// An error returns the supplied body and false; success does not distinguish an
// absent path from a deleted value.
func DeleteJSONPathBytes(body []byte, path string) ([]byte, bool) {
	// Marker-limit callers collect and recheck paths before using a successful
	// deletion to reduce excess. Generic callers cannot infer that a field existed
	// from the success flag alone.
	next, err := sjson.DeleteBytes(body, path)
	if err != nil {
		return body, false
	}
	return next, true
}

// NormalizeClaudeOAuthSystemBody rewrites recognized identity text in string or
// array system content. The strip option additionally deletes array cache
// markers, with successful edits accumulated and failed edits retaining their
// corresponding fields.
func NormalizeClaudeOAuthSystemBody(body []byte, opts ClaudeOAuthNormalizeOptions) ([]byte, bool) {
	sys := gjson.GetBytes(body, "system")
	if !sys.Exists() {
		return body, false
	}

	out := body
	modified := false

	// Normalize a scalar prompt or each array text field at its existing path.
	// Optional marker stripping traverses array elements independently of whether
	// their banner text changes.
	switch {
	case sys.Type == gjson.String:
		sanitized := SanitizeSystemText(sys.String())
		if sanitized != sys.String() {
			if next, ok := SetJSONValueBytes(out, "system", sanitized); ok {
				out = next
				modified = true
			}
		}
	case sys.IsArray():
		index := 0
		sys.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "text" {
				textResult := item.Get("text")
				if textResult.Exists() && textResult.Type == gjson.String {
					text := textResult.String()
					sanitized := SanitizeSystemText(text)
					if sanitized != text {
						if next, ok := SetJSONValueBytes(out, fmt.Sprintf("system.%d.text", index), sanitized); ok {
							out = next
							modified = true
						}
					}
				}
			}

			if opts.StripSystemCacheControl && item.Get("cache_control").Exists() {
				if next, ok := DeleteJSONPathBytes(out, fmt.Sprintf("system.%d.cache_control", index)); ok {
					out = next
					modified = true
				}
			}

			index++
			return true
		})
	}

	return out, modified
}

// EnsureClaudeOAuthMetadataUserID supplies userID when usable existing metadata
// identity is absent. Blank supplied IDs leave the body unchanged, and a
// nonempty string user_id in an object takes precedence.
//
// Missing, null, or nonobject metadata is replaced by a metadata object; an
// existing object gains only the user_id field so other attributes survive.
// Whitespace-only existing IDs are treated as nonempty. Each path reports
// successful writes through the boolean, while serialization or field-update
// failure returns the supplied body and false.
func EnsureClaudeOAuthMetadataUserID(body []byte, userID string) ([]byte, bool) {
	if strings.TrimSpace(userID) == "" {
		return body, false
	}

	// Retain usable object identity before choosing a replacement representation.
	// Filling object user_id keeps other metadata attributes, whereas missing or
	// nonobject metadata is replaced as one object.
	metadata := gjson.GetBytes(body, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		raw, err := MarshalAnthropicMetadata(userID)
		if err != nil {
			return body, false
		}
		return SetJSONRawBytes(body, "metadata", raw)
	}

	trimmedRaw := strings.TrimSpace(metadata.Raw)
	if strings.HasPrefix(trimmedRaw, "{") {
		existing := metadata.Get("user_id")
		if existing.Exists() && existing.Type == gjson.String && existing.String() != "" {
			return body, false
		}
		return SetJSONValueBytes(body, "metadata.user_id", userID)
	}

	raw, err := MarshalAnthropicMetadata(userID)
	if err != nil {
		return body, false
	}
	return SetJSONRawBytes(body, "metadata", raw)
}

// NormalizeClaudeOAuthRequestBody applies OAuth system normalization, metadata
// options, and absent-field defaults to body. Empty body returns directly.
// Absent tools, temperature, and max_tokens receive [], 1, and 128000;
// supplied values and existing context_management remain available.
//
// Exact enabled or adaptive thinking can supply context management, which final
// beta sanitization may later remove. A missing or empty tools array also makes
// tool_choice removable, while nonempty tools retain that choice for tool-name
// rewriting. JSON edit failures keep the affected field, and a body with no
// successful modification is returned as supplied.
func NormalizeClaudeOAuthRequestBody(body []byte, opts ClaudeOAuthNormalizeOptions) []byte {
	if len(body) == 0 {
		return body
	}

	out := body
	modified := false

	if next, changed := NormalizeClaudeOAuthSystemBody(out, opts); changed {
		out = next
		modified = true
	}

	if !gjson.GetBytes(out, "tools").Exists() {
		if next, ok := SetJSONRawBytes(out, "tools", []byte("[]")); ok {
			out = next
			modified = true
		}
	}

	if opts.InjectMetadata && opts.MetadataUserID != "" {
		if next, changed := EnsureClaudeOAuthMetadataUserID(out, opts.MetadataUserID); changed {
			out = next
			modified = true
		}
	}

	if !gjson.GetBytes(out, "temperature").Exists() {
		if next, ok := SetJSONValueBytes(out, "temperature", 1); ok {
			out = next
			modified = true
		}
	}

	if !gjson.GetBytes(out, "max_tokens").Exists() {
		if next, ok := SetJSONValueBytes(out, "max_tokens", 128000); ok {
			out = next
			modified = true
		}
	}

	// Thinking defaults add context management before the final Header is known.
	// Later beta sanitation applies provider overrides to decide whether this
	// extension can remain, keeping normalization and final capability selection
	// ordered.
	if !gjson.GetBytes(out, "context_management").Exists() {
		thinkingType := gjson.GetBytes(out, "thinking.type").String()
		if thinkingType == "enabled" || thinkingType == "adaptive" {
			const cmDefault = `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`
			if next, ok := SetJSONRawBytes(out, "context_management", []byte(cmDefault)); ok {
				out = next
				modified = true
			}
		}
	}

	// An empty or nonarray tools value cannot supply a declared tool for
	// tool_choice. Remove that selector here; nonempty declarations remain available
	// for ApplyToolNameRewriteToBody to rewrite named choices with their tools.
	if !gjson.GetBytes(out, "tools").IsArray() || len(gjson.GetBytes(out, "tools").Array()) == 0 {
		if gjson.GetBytes(out, "tool_choice").Exists() {
			if next, ok := DeleteJSONPathBytes(out, "tool_choice"); ok {
				out = next
				modified = true
			}
		}
	}

	if !modified {
		return body
	}

	return out
}

// BuildStableSessionSeed joins provider ID, client discriminator, and first
// user text for a repeatable metadata seed. Equal inputs produce equal seeds;
// callers choose the discriminator and keep that value and the opening text
// consistent when later turns need the same identity.
func BuildStableSessionSeed(providerID int64, clientDiscriminator, firstUserText string) string {
	var b strings.Builder
	_, _ = b.WriteString(strconv.FormatInt(providerID, 10))
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(clientDiscriminator)
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(firstUserText)
	return b.String()
}

// NormalizeSystemParam decodes json.RawMessage into the ordinary JSON value
// representation. Other inputs pass through, while empty or invalid raw JSON
// returns nil for callers that switch on system shape.
func NormalizeSystemParam(system any) any {
	raw, ok := system.(json.RawMessage)
	if !ok {
		return system
	}
	if len(raw) == 0 {
		return nil
	}
	var parsed any
	// Consumers switch on the normalized system value; failed raw decoding
	// therefore selects their nil-input path, including identity-only prompt
	// injection and empty instruction extraction.
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	return parsed
}

// SystemIncludesClaudeCodePrompt reports a recognized banner at the start of
// system text. Raw JSON is normalized first, and arrays inspect string text
// fields regardless of the block's type.
func SystemIncludesClaudeCodePrompt(system any) bool {
	system = NormalizeSystemParam(system)
	switch v := system.(type) {
	case string:
		return HasClaudeCodePrefix(v)
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok && HasClaudeCodePrefix(text) {
					return true
				}
			}
		}
	}
	return false
}

// HasClaudeCodePrefix reports an exact recognized banner at text's start.
func HasClaudeCodePrefix(text string) bool {
	for _, prefix := range ClaudeCodePromptPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// InjectClaudeCodePrompt constructs a system array starting with a cached
// Claude Code identity block. It removes matching standalone identity entries
// and can prefix a later nonempty text block so client instructions still carry
// the identity.
//
// When body already contains a system array, its raw elements supply the
// rebuilt content. Otherwise supplied []any maps are serialized, and a prefixed
// map can be mutated in place; callers must account for that sharing. Marshal
// failures can skip fallback elements, while a final system write failure
// returns the supplied body.
func InjectClaudeCodePrompt(body []byte, system any) []byte {
	system = NormalizeSystemParam(system)
	// Build the identity block before selecting replacement content, so an
	// encoding failure can return the supplied request instead of installing an
	// incomplete system array.
	claudeCodeBlock, err := MarshalAnthropicSystemTextBlock(ClaudeCodeSystemPrompt, true)
	if err != nil {
		logger.LegacyPrintf("service.gateway", "Warning: failed to build Claude Code prompt block: %v", err)
		return body
	}
	// Put identity into its own cached block, then prefix an additional client
	// text instruction when needed so both positions carry the recognized banner.
	claudeCodePrefix := strings.TrimSpace(ClaudeCodeSystemPrompt)

	var items [][]byte

	switch v := system.(type) {
	case nil:
		items = [][]byte{claudeCodeBlock}
	case string:
		// Trim the identity comparison so surrounding whitespace does not create
		// another instruction block alongside the generated banner for the same text.
		if strings.TrimSpace(v) == "" || strings.TrimSpace(v) == strings.TrimSpace(ClaudeCodeSystemPrompt) {
			items = [][]byte{claudeCodeBlock}
		} else {
			// Retain the custom string as a separate instruction after adding its banner,
			// so replacing system shape does not intentionally discard the client's prompt.
			merged := v
			if !strings.HasPrefix(v, claudeCodePrefix) {
				merged = claudeCodePrefix + "\n\n" + v
			}
			nextBlock, buildErr := MarshalAnthropicSystemTextBlock(merged, false)
			if buildErr != nil {
				logger.LegacyPrintf("service.gateway", "Warning: failed to build prefixed Claude Code system block: %v", buildErr)
				return body
			}
			items = [][]byte{claudeCodeBlock, nextBlock}
		}
	case []any:
		items = make([][]byte, 0, len(v)+1)
		items = append(items, claudeCodeBlock)
		prefixedNext := false
		systemResult := gjson.GetBytes(body, "system")
		if systemResult.IsArray() {
			systemResult.ForEach(func(_, item gjson.Result) bool {
				textResult := item.Get("text")
				if textResult.Exists() && textResult.Type == gjson.String &&
					strings.TrimSpace(textResult.String()) == strings.TrimSpace(ClaudeCodeSystemPrompt) {
					return true
				}

				raw := []byte(item.Raw)
				// Prefix an eligible subsequent raw text block and retain its other fields.
				// A failed edit leaves that block available and allows a later candidate to
				// be tried.
				if !prefixedNext && item.Get("type").String() == "text" && textResult.Exists() && textResult.Type == gjson.String {
					text := textResult.String()
					if strings.TrimSpace(text) != "" && !strings.HasPrefix(text, claudeCodePrefix) {
						next, setErr := sjson.SetBytes(raw, "text", claudeCodePrefix+"\n\n"+text)
						if setErr == nil {
							raw = next
							prefixedNext = true
						}
					}
				}
				items = append(items, raw)
				return true
			})
		} else {
			for _, item := range v {
				m, ok := item.(map[string]any)
				if !ok {
					// An unsupported fallback element can fail JSON serialization. Skip that
					// element so remaining encodable items still follow the generated identity
					// block in order.
					raw, marshalErr := json.Marshal(item)
					if marshalErr == nil {
						items = append(items, raw)
					}
					continue
				}
				if text, ok := m["text"].(string); ok && strings.TrimSpace(text) == strings.TrimSpace(ClaudeCodeSystemPrompt) {
					continue
				}
				if !prefixedNext {
					if blockType, _ := m["type"].(string); blockType == "text" {
						if text, ok := m["text"].(string); ok && strings.TrimSpace(text) != "" && !strings.HasPrefix(text, claudeCodePrefix) {
							// This fallback has caller-supplied maps rather than body raw elements;
							// prefixing writes into that map before serialization, so callers can
							// observe the mutation.
							m["text"] = claudeCodePrefix + "\n\n" + text
							prefixedNext = true
						}
					}
				}
				// Fallback map serialization is best effort; a failed map is omitted after
				// any prefix mutation, while subsequent encodable elements can still be
				// added.
				raw, marshalErr := json.Marshal(m)
				if marshalErr == nil {
					items = append(items, raw)
				}
			}
		}
	default:
		items = [][]byte{claudeCodeBlock}
	}

	result, ok := SetJSONRawBytes(body, "system", BuildJSONArrayRaw(items))
	if !ok {
		logger.LegacyPrintf("service.gateway", "Warning: failed to inject Claude Code prompt")
		return body
	}
	return result
}

// ClaudeOAuthSystemPromptBlocksForModel selects the fixed Fable template when
// model contains fable, ignoring case. Other models keep configured text as
// supplied, leaving configuration decoding to the block builder.
func ClaudeOAuthSystemPromptBlocksForModel(model, configured string) string {
	if IsAnthropicFableModel(model) {
		return ClaudeFableOAuthSystemPromptBlocks
	}
	return configured
}

// DefaultClaudeOAuthExpansionPrompt trims a configured expansion or returns the
// built-in prompt when empty. The returned text is used by both default blocks
// and expansion placeholders in configured blocks.
func DefaultClaudeOAuthExpansionPrompt(expansionPrompt string) string {
	expansionPrompt = strings.TrimSpace(expansionPrompt)
	if expansionPrompt == "" {
		return ClaudeCodeSystemPromptExpansion
	}
	return expansionPrompt
}

// ParseClaudeOAuthSystemPromptBlocksConfig decodes an array or an envelope
// containing blocks. Blank input returns nil without error; malformed JSON
// returns its decode error, and an envelope may contain no blocks.
func ParseClaudeOAuthSystemPromptBlocksConfig(raw string) ([]ClaudeOAuthSystemPromptBlockConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var blocks []ClaudeOAuthSystemPromptBlockConfig
		if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	}
	var envelope ClaudeOAuthSystemPromptBlocksEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, err
	}
	return envelope.Blocks, nil
}

// DecodeClaudeOAuthSystemPromptCacheControl interprets blank, null, or false as
// no marker and true as the default ephemeral object. Object JSON passes
// through after decoding; other shapes and malformed JSON return errors.
func DecodeClaudeOAuthSystemPromptCacheControl(raw json.RawMessage) (any, error) {
	// Boolean cache settings are configuration shorthand: true expands to an
	// ephemeral marker, while false and null omit the field before system block
	// serialization.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("false")) {
		return nil, nil
	}
	if bytes.Equal(trimmed, []byte("true")) {
		return map[string]string{
			"type": "ephemeral",
			"ttl":  DefaultCacheControlTTL,
		}, nil
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("cache_control must be boolean, null, or object")
	}
	return value, nil
}

// ExpandClaudeOAuthSystemPromptTextTemplate replaces recognized billing,
// version, fingerprint, identity, and expansion placeholders. Empty text
// returns empty, a blank expansion selects the default, and unknown placeholder
// text remains literal.
func ExpandClaudeOAuthSystemPromptTextTemplate(body []byte, text string, expansionPrompt string) (string, error) {
	if text == "" {
		return "", nil
	}
	expansionPrompt = DefaultClaudeOAuthExpansionPrompt(expansionPrompt)
	billingText, err := BuildBillingAttributionText(body, CLIVersion())
	if err != nil {
		return "", err
	}
	fp := ComputeClaudeCodeFingerprint(body, CLIVersion())
	replacer := strings.NewReplacer(
		"{billing_header}", billingText,
		"{cc_version}", CLIVersion(),
		"{fp}", fp,
		"{claude_code_system_prompt}", ClaudeCodeSystemPrompt,
		"{claude_code_expansion_prompt}", expansionPrompt,
	)
	return replacer.Replace(text), nil
}

// DefaultClaudeOAuthSystemPromptBlockConfig returns enabled billing, identity,
// and expansion blocks in that order. The expansion owns default cache control;
// all enabled pointers refer to the same locally created boolean.
func DefaultClaudeOAuthSystemPromptBlockConfig() []ClaudeOAuthSystemPromptBlockConfig {
	enabled := true
	return []ClaudeOAuthSystemPromptBlockConfig{
		{
			Enabled: &enabled,
			Type:    "text",
			Text:    "{billing_header}",
		},
		{
			Enabled: &enabled,
			Type:    "text",
			Text:    "{claude_code_system_prompt}",
		},
		{
			Enabled: &enabled,
			Type:    "text",
			Text:    "{claude_code_expansion_prompt}",
			CacheControl: json.RawMessage(
				fmt.Sprintf(`{"type":"ephemeral","ttl":%q}`, DefaultCacheControlTTL),
			),
		},
	}
}

// BuildClaudeOAuthSystemPromptBlocksJSON renders configured text blocks into
// complete JSON objects. A missing or empty decoded block list selects
// defaults, while malformed configuration returns an error before any blocks
// are produced.
//
// Disabled blocks and blank expanded text are omitted in supplied order.
// Missing types become text; unsupported types and invalid cache control return
// indexed errors. Cache control is decoded after expansion, so a blank block is
// skipped before its cache payload is examined. The returned list is ready for
// BuildJSONArrayRaw.
func BuildClaudeOAuthSystemPromptBlocksJSON(body []byte, expansionPrompt string, blocksConfig string) ([][]byte, error) {
	blocks, err := ParseClaudeOAuthSystemPromptBlocksConfig(blocksConfig)
	if err != nil {
		return nil, err
	}
	// An empty parsed list selects defaults. A nonempty configuration can render
	// zero blocks when all its entries are disabled or their expanded text is
	// blank.
	if len(blocks) == 0 {
		blocks = DefaultClaudeOAuthSystemPromptBlockConfig()
	}

	items := make([][]byte, 0, len(blocks))
	for i, block := range blocks {
		if block.Enabled != nil && !*block.Enabled {
			continue
		}
		blockType := strings.TrimSpace(block.Type)
		if blockType == "" {
			blockType = "text"
		}
		if blockType != "text" {
			return nil, fmt.Errorf("system block %d type %q is not supported", i, block.Type)
		}
		text, err := ExpandClaudeOAuthSystemPromptTextTemplate(body, block.Text, expansionPrompt)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		cacheControl, err := DecodeClaudeOAuthSystemPromptCacheControl(block.CacheControl)
		if err != nil {
			return nil, fmt.Errorf("system block %d cache_control: %w", i, err)
		}
		raw, err := MarshalAnthropicSystemTextBlockWithCacheControl(text, cacheControl)
		if err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	return items, nil
}

// ValidateClaudeOAuthSystemPromptBlocksConfig checks parseability, supported
// text types, and cache-control shapes for settings input. Blank input
// succeeds, and invalid configuration becomes a BadRequest; disabled blocks
// still undergo type and cache validation.
func ValidateClaudeOAuthSystemPromptBlocksConfig(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	blocks, err := ParseClaudeOAuthSystemPromptBlocksConfig(raw)
	if err != nil {
		return infraerrors.BadRequest("INVALID_CLAUDE_OAUTH_SYSTEM_PROMPT_BLOCKS", "claude oauth system prompt blocks must be valid JSON")
	}
	for i, block := range blocks {
		blockType := strings.TrimSpace(block.Type)
		if blockType == "" {
			blockType = "text"
		}
		if blockType != "text" {
			return infraerrors.BadRequest("INVALID_CLAUDE_OAUTH_SYSTEM_PROMPT_BLOCKS", fmt.Sprintf("system block %d type must be text", i))
		}
		if _, err := DecodeClaudeOAuthSystemPromptCacheControl(block.CacheControl); err != nil {
			return infraerrors.BadRequest("INVALID_CLAUDE_OAUTH_SYSTEM_PROMPT_BLOCKS", fmt.Sprintf("system block %d cache_control is invalid", i))
		}
	}
	return nil
}

// ExtractSystemTextAndCacheControl gathers system text and the cache marker
// selected for message migration. A string is trimmed; arrays join nonblank
// string text fields in order, accepting fields from any map block type.
//
// The last nonnil cache_control attached to an included text item becomes the
// returned marker. It remains the supplied object, so callers should treat
// shared maps as immutable during serialization. Unsupported system shapes
// return empty text and nil, and earlier block-specific markers cannot all fit
// on the single migrated message block.
func ExtractSystemTextAndCacheControl(system any) (string, any) {
	switch v := system.(type) {
	case string:
		return strings.TrimSpace(v), nil
	case []any:
		var parts []string
		var cacheControl any
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, ok := m["text"].(string)
			if !ok || strings.TrimSpace(text) == "" {
				continue
			}
			parts = append(parts, text)
			// Several system blocks become one instruction message, so multiple markers
			// cannot keep their separate positions. Retain the last supplied nonnil marker
			// from included text with its TTL for that merged message.
			if cc, exists := m["cache_control"]; exists && cc != nil {
				cacheControl = cc
			}
		}
		return strings.Join(parts, "\n\n"), cacheControl
	default:
		return "", nil
	}
}

// RewriteSystemForNonClaudeCodeWithPromptBlocks replaces system with configured
// OAuth blocks and may prepend client instructions plus an assistant
// acknowledgment to messages. Raw system values are decoded first; blank
// instructions or joined text beginning with a recognized Claude banner skip
// migration.
//
// Invalid configured blocks retry with defaults, and failure before system
// replacement returns body. After replacement succeeds, later serialization or
// messages-update failure can return the replaced system without the injected
// pair. Original messages are copied as raw elements behind the pair, and the
// selected client cache marker travels with the instruction block.
func RewriteSystemForNonClaudeCodeWithPromptBlocks(body []byte, system any, expansionPrompt string, blocksConfig string) []byte {
	system = NormalizeSystemParam(system)
	expansionPrompt = DefaultClaudeOAuthExpansionPrompt(expansionPrompt)

	originalSystemText, originalSystemCacheControl := ExtractSystemTextAndCacheControl(system)

	// Try configured blocks before replacing system so malformed settings can
	// recover to the default billing, identity, and cached expansion layout while
	// the original request is still available if both builds fail.
	systemBlocks, blockErr := BuildClaudeOAuthSystemPromptBlocksJSON(body, expansionPrompt, blocksConfig)
	if blockErr != nil {
		logger.LegacyPrintf("service.gateway", "Warning: failed to build configured Claude OAuth system blocks: %v", blockErr)
		systemBlocks, blockErr = BuildClaudeOAuthSystemPromptBlocksJSON(body, expansionPrompt, "")
	}
	if blockErr != nil {
		logger.LegacyPrintf("service.gateway", "Warning: failed to build default Claude OAuth system blocks: %v", blockErr)
		return body
	}
	out, ok := SetJSONRawBytes(body, "system", BuildJSONArrayRaw(systemBlocks))
	if !ok {
		logger.LegacyPrintf("service.gateway", "Warning: failed to set Claude Code system prompt")
		return body
	}

	// Pair migrated client instructions with an acknowledgment before appending
	// raw history, keeping that instruction exchange ahead of every supplied turn
	// after OAuth blocks have replaced the system field.
	ccPromptTrimmed := strings.TrimSpace(ClaudeCodeSystemPrompt)
	if originalSystemText != "" && originalSystemText != ccPromptTrimmed && !HasClaudeCodePrefix(originalSystemText) {
		instructionBlock := map[string]any{
			"type": "text",
			"text": "[System Instructions]\n" + originalSystemText,
		}
		if originalSystemCacheControl != nil {
			instructionBlock["cache_control"] = originalSystemCacheControl
		}
		instrMsg, err1 := json.Marshal(map[string]any{
			"role": "user",
			"content": []map[string]any{
				instructionBlock,
			},
		})
		ackMsg, err2 := json.Marshal(map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "Understood. I will follow these instructions."},
			},
		})
		if err1 != nil || err2 != nil {
			logger.LegacyPrintf("service.gateway", "Warning: failed to marshal system-to-messages injection")
			return out
		}

		items := [][]byte{instrMsg, ackMsg}
		messagesResult := gjson.GetBytes(out, "messages")
		if messagesResult.IsArray() {
			messagesResult.ForEach(func(_, msg gjson.Result) bool {
				items = append(items, []byte(msg.Raw))
				return true
			})
		}

		// Original history follows the injected pair as raw elements to keep its
		// field layout. If this messages replacement fails, the earlier system
		// replacement remains in out.
		if next, setOk := SetJSONRawBytes(out, "messages", BuildJSONArrayRaw(items)); setOk {
			out = next
		}
	}

	return out
}

// CollectCacheControlPaths records array block cache paths for subsequent
// deletion. Thinking markers in system or message content are separated as
// invalid; top-level cache_control is outside this traversal, and tool markers
// form their own ordered list.
func CollectCacheControlPaths(body []byte) (invalidThinking []CacheControlPath, messagePaths []string, toolPaths []string, systemPaths []string) {
	if !mayContainCacheControlToken(body) {
		return nil, nil, nil, nil
	}
	root := parseAnthropicBodyView(body)
	system := root.Get("system")
	if system.IsArray() {
		sysIndex := 0
		system.ForEach(func(_, item gjson.Result) bool {
			if item.Get("cache_control").Exists() {
				path := fmt.Sprintf("system.%d.cache_control", sysIndex)
				if item.Get("type").String() == "thinking" {
					invalidThinking = append(invalidThinking, CacheControlPath{
						Path: path,
						Log:  "[Warning] Removed illegal cache_control from thinking block in system",
					})
				} else {
					systemPaths = append(systemPaths, path)
				}
			}
			sysIndex++
			return true
		})
	}

	messages := root.Get("messages")
	if messages.IsArray() {
		msgIndex := 0
		messages.ForEach(func(_, msg gjson.Result) bool {
			content := msg.Get("content")
			if content.IsArray() {
				contentIndex := 0
				content.ForEach(func(_, item gjson.Result) bool {
					if item.Get("cache_control").Exists() {
						path := fmt.Sprintf("messages.%d.content.%d.cache_control", msgIndex, contentIndex)
						if item.Get("type").String() == "thinking" {
							invalidThinking = append(invalidThinking, CacheControlPath{
								Path: path,
								Log:  fmt.Sprintf("[Warning] Removed illegal cache_control from thinking block in messages[%d].content[%d]", msgIndex, contentIndex),
							})
						} else {
							messagePaths = append(messagePaths, path)
						}
					}
					contentIndex++
					return true
				})
			}
			msgIndex++
			return true
		})
	}

	tools := root.Get("tools")
	if tools.IsArray() {
		toolIndex := 0
		tools.ForEach(func(_, tool gjson.Result) bool {
			if tool.Get("cache_control").Exists() {
				toolPaths = append(toolPaths, fmt.Sprintf("tools.%d.cache_control", toolIndex))
			}
			toolIndex++
			return true
		})
	}

	return invalidThinking, messagePaths, toolPaths, systemPaths
}

// EnforceCacheControlLimit removes collected thinking markers and reduces other
// collected block markers toward MaxCacheControlBlocks. It counts message,
// tool, and system paths, with top-level cache_control outside the count.
//
// Excess deletion prefers tools from the end, then messages from the start,
// then system blocks from the end. This order retains message anchors ahead of
// tool markers and delays system removal until needed. Failed deletions keep
// their fields and do not decrement excess, so the returned body can still
// exceed the intended limit when edits fail.
func EnforceCacheControlLimit(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	invalidThinking, messagePaths, toolPaths, systemPaths := CollectCacheControlPaths(body)
	out := body
	modified := false

	// Thinking marker paths are excluded from the quota count even when their
	// deletion fails. Remove them first, accumulating successful cleanup before
	// reducing the other collected paths.
	for _, item := range invalidThinking {
		if !gjson.GetBytes(out, item.Path).Exists() {
			continue
		}
		next, ok := DeleteJSONPathBytes(out, item.Path)
		if !ok {
			continue
		}
		out = next
		modified = true
		logger.LegacyPrintf("service.gateway", "%s", item.Log)
	}

	// Count collected nonthinking block markers after attempted cleanup. The
	// collected paths exclude top-level cache_control, so that separate marker
	// does not affect this reduction.
	count := len(messagePaths) + len(toolPaths) + len(systemPaths)
	if count <= MaxCacheControlBlocks {
		if modified {
			return out
		}
		return body
	}

	// Reduce excess by removing later tool markers, earlier message markers, then
	// later system markers. Decrement excess only after a successful deletion,
	// retaining other candidate paths when an edit fails.
	remaining := count - MaxCacheControlBlocks
	for i := len(toolPaths) - 1; i >= 0 && remaining > 0; i-- {
		path := toolPaths[i]
		if !gjson.GetBytes(out, path).Exists() {
			continue
		}
		next, ok := DeleteJSONPathBytes(out, path)
		if !ok {
			continue
		}
		out = next
		modified = true
		remaining--
	}

	for _, path := range messagePaths {
		if remaining <= 0 {
			break
		}
		if !gjson.GetBytes(out, path).Exists() {
			continue
		}
		next, ok := DeleteJSONPathBytes(out, path)
		if !ok {
			continue
		}
		out = next
		modified = true
		remaining--
	}

	for i := len(systemPaths) - 1; i >= 0 && remaining > 0; i-- {
		path := systemPaths[i]
		if !gjson.GetBytes(out, path).Exists() {
			continue
		}
		next, ok := DeleteJSONPathBytes(out, path)
		if !ok {
			continue
		}
		out = next
		modified = true
		remaining--
	}

	if modified {
		return out
	}
	return body
}

// InjectAnthropicCacheControlTTL1h changes existing ephemeral cache markers
// through ForceEphemeralCacheControlTTL. It includes top-level and block
// markers, leaves other cache types alone, and returns the body's accumulated
// successful edits.
func InjectAnthropicCacheControlTTL1h(body []byte) []byte {
	return ForceEphemeralCacheControlTTL(body, CacheTTLTarget1h)
}

// ForceEphemeralCacheControlTTL sets ttl on existing ephemeral markers at top
// level and in system, message, or tool arrays. Empty body or ttl returns
// unchanged bytes; failed individual writes leave that marker's value in place.
func ForceEphemeralCacheControlTTL(body []byte, ttl string) []byte {
	if len(body) == 0 || ttl == "" || !mayContainCacheControlToken(body) {
		return body
	}
	root := parseAnthropicBodyView(body)
	out := body
	var paths []string
	addPath := func(path string, value gjson.Result) {
		cc := value.Get("cache_control")
		if !cc.Exists() || cc.Get("type").String() != "ephemeral" {
			return
		}
		if cc.Get("ttl").String() == ttl {
			return
		}
		paths = append(paths, path+".cache_control.ttl")
	}

	if topCC := root.Get("cache_control"); topCC.Exists() && topCC.Get("type").String() == "ephemeral" && topCC.Get("ttl").String() != ttl {
		paths = append(paths, "cache_control.ttl")
	}

	system := root.Get("system")
	if system.IsArray() {
		idx := -1
		system.ForEach(func(_, block gjson.Result) bool {
			idx++
			addPath(fmt.Sprintf("system.%d", idx), block)
			return true
		})
	}

	messages := root.Get("messages")
	if messages.IsArray() {
		msgIdx := -1
		messages.ForEach(func(_, msg gjson.Result) bool {
			msgIdx++
			content := msg.Get("content")
			if !content.IsArray() {
				return true
			}
			contentIdx := -1
			content.ForEach(func(_, block gjson.Result) bool {
				contentIdx++
				addPath(fmt.Sprintf("messages.%d.content.%d", msgIdx, contentIdx), block)
				return true
			})
			return true
		})
	}

	tools := root.Get("tools")
	if tools.IsArray() {
		idx := -1
		tools.ForEach(func(_, tool gjson.Result) bool {
			idx++
			addPath(fmt.Sprintf("tools.%d", idx), tool)
			return true
		})
	}

	// Apply TTL edits after collecting paths so traversal sees one body snapshot.
	// Each failed write leaves that marker unchanged while later paths can still
	// receive the requested TTL.
	for _, path := range paths {
		if next, err := sjson.SetBytes(out, path, ttl); err == nil {
			out = next
		}
	}
	return out
}

// SystemHasClaudeCodeBillingAttribution reports a system-array text value with
// the billing prefix and an entrypoint marker. This identifies proxied traffic;
// callers use separate Claude Code-only validation for authorization.
func SystemHasClaudeCodeBillingAttribution(body []byte) bool {
	system := parseAnthropicBodyView(body).Get("system")
	if !system.IsArray() {
		return false
	}
	found := false
	system.ForEach(func(_, item gjson.Result) bool {
		text := item.Get("text").String()
		if strings.HasPrefix(text, ClaudeCodeBillingHeaderPrefix+":") && strings.Contains(text, ClaudeCodeEntrypointMarker) {
			found = true
			return false
		}
		return true
	})
	return found
}

// IsProxiedClaudeCodeRequest reports parsed metadata identity together with
// system billing attribution. Both checks need to succeed for the request to
// qualify, while authorization policy remains with the caller.
func IsProxiedClaudeCodeRequest(body []byte, metadataUserID string) bool {
	return ParseMetadataUserID(metadataUserID) != nil && SystemHasClaudeCodeBillingAttribution(body)
}

// mayContainCacheControlToken screens raw JSON bytes for literal cache_control
// or Unicode escape sequences before traversing system, message, and tool arrays.
func mayContainCacheControlToken(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	return bytes.Contains(body, []byte("cache_control")) || bytes.Contains(body, []byte(`\u`))
}

// parseAnthropicBodyView borrows raw's backing bytes for read-only gjson lookups
// so top-level message and tool array probes do not heap-copy large payloads.
func parseAnthropicBodyView(raw []byte) gjson.Result {
	if len(raw) == 0 {
		return gjson.Result{}
	}
	return gjson.Parse(unsafe.String(unsafe.SliceData(raw), len(raw)))
}
