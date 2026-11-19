package wirejson

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unsafe"
)

// request_validation.go validates inbound JSON request payloads against
// duplicate object member names and case-variant protocol field collisions
// before gateway routing, policy enforcement, or upstream forwarding.
// Byte-view readers such as gjson and sjson inspect and rewrite the first
// matching case-sensitive member, whereas Go struct and map decoders retain
// the last matching member and fold letter case using Unicode simple case
// folding. Rejecting ambiguous member sets at ingress keeps both parser
// families aligned on one unambiguous field binding without reserializing valid
// client payloads.
//
// Validation combines encoding/json syntax checking with a single structural
// scan that unescapes member names only when backslash escapes appear, keeping
// unescaped small objects on a stack-allocated member buffer before spilling
// larger objects to a map. Protocol-scoped objects compare member names after
// Unicode simple case folding so repeated names and case-equivalent pairs such
// as model/MODEL or stream/ſstream are rejected even for extension fields, and
// core gateway-inspected fields also require canonical spelling when supplied
// once. User-owned schema, tool-argument, and unstructured metadata values drop
// back to exact-duplicate enforcement so caller-defined uppercase identifiers
// remain accepted.

const (
	// fieldScope values select which member deduplication and canonical protocol
	// field rules apply to an object; scopeExactDupOnly checks exact repeats alone.
	scopeExactDupOnly fieldScope = iota
	scopeRoot
	scopeMetadata
	scopeThinking
	scopeReasoning
	scopeOutputConfig
	scopeStreamOptions
	scopeGenerationConfig
	scopeGeminiThinkingConfig
	scopeGeminiImageConfig
	scopeMessage
	scopeMessageContentBlock
	scopeToolCall
	scopeSystemBlock
	scopeInputItem
	scopeInputContentPart
	scopeGeminiContent
	scopeGeminiPart
	scopeTool
	scopeToolFunction
	scopeToolChoice
	scopeBatchItem
	fieldScopeCount

	// inlineObjectKeyCap bounds stack-held member names per object before
	// allocating an overflow map for wide objects.
	inlineObjectKeyCap = 16
)

var (
	// ErrInvalidRequestJSON and ErrRequestJSONMustBeObject classify malformed or
	// non-object request bodies before protocol handlers inspect individual fields.
	ErrInvalidRequestJSON      = errors.New("invalid JSON request body")
	ErrRequestJSONMustBeObject = errors.New("JSON request body must be an object")

	// canonicalFieldsByScope maps Unicode-folded protocol field names to their
	// required wire spelling within each gateway-interpreted object scope.
	canonicalFieldsByScope = buildCanonicalFieldsByScope()
)

// fieldScope identifies the protocol schema context of the current JSON object.
type fieldScope uint8

// RequestFieldError reports a duplicate or case-ambiguous JSON object member
// name encountered during request validation.
type RequestFieldError struct {
	// Key holds an independent copy of the offending decoded member spelling,
	// while Canonical is empty for duplicate-key errors and names the expected
	// wire field when a single member uses non-canonical protocol casing.
	Key       string // decoded offending member spelling
	Canonical string // expected canonical field spelling when non-empty
}

// objectMemberSet records member keys seen within one JSON object.
type objectMemberSet struct {
	// inline holds the first inlineObjectKeyCap member keys on the stack;
	// overflow is allocated when an object exceeds inlineObjectKeyCap members and
	// then holds all seen keys for constant-time lookup.
	inline   [inlineObjectKeyCap]string // initial stack-resident member keys
	count    int                        // number of entries populated in inline
	overflow map[string]struct{}        // heap set allocated past inlineObjectKeyCap
}

// Error formats a client-safe rejection message quoting at most 64 runes of Key
// so responses identify the offending field without echoing unbounded input.
func (e *RequestFieldError) Error() string {
	if e == nil {
		return "Request body contains invalid JSON key"
	}
	if e.Canonical != "" {
		return fmt.Sprintf("Request body contains ambiguous JSON key %.64q for protocol field %q", e.Key, e.Canonical)
	}
	return fmt.Sprintf("Request body contains duplicate JSON key %.64q", e.Key)
}

// ValidateRequestObject verifies that data encodes one valid JSON object with
// no duplicate decoded member names and no case-variant protocol field names.
// Empty inputs, non-object JSON values, trailing tokens, and ambiguous member
// names return descriptive validation errors without mutating data.
func ValidateRequestObject(data []byte) error {
	pos := skipWhitespace(data, 0)
	if pos >= len(data) {
		return ErrInvalidRequestJSON
	}
	if data[pos] != '{' {
		if !json.Valid(data) {
			return ErrInvalidRequestJSON
		}
		return ErrRequestJSONMustBeObject
	}
	return ValidateRequestJSON(data)
}

// ValidateRequestJSON verifies that data encodes one valid RFC 8259 JSON value
// whose nested objects contain no duplicate decoded member names and whose
// protocol-scoped objects use canonical field casing. Callers may retain and
// forward data unchanged after a nil return.
func ValidateRequestJSON(data []byte) error {
	if !json.Valid(data) {
		return ErrInvalidRequestJSON
	}
	pos := skipWhitespace(data, 0)
	end, err := validateValue(data, pos, scopeRoot)
	if err != nil {
		return err
	}
	if skipWhitespace(data, end) != len(data) {
		return ErrInvalidRequestJSON
	}
	return nil
}

// validateValue advances across one syntactically valid JSON value starting at
// pos and applies scope when that value is a JSON object.
func validateValue(data []byte, pos int, scope fieldScope) (int, error) {
	pos = skipWhitespace(data, pos)
	switch data[pos] {
	case '{':
		return validateObject(data, pos, scope)
	case '[':
		return validateArray(data, pos, scopeExactDupOnly)
	case '"':
		return skipString(data, pos), nil
	default:
		return skipPrimitive(data, pos), nil
	}
}

// validateObject checks one JSON object starting at '{' for duplicate member
// names and scoped canonical field casing, recursing into member values.
// Protocol scopes deduplicate after Unicode simple case folding, whereas user
// data and metadata scopes deduplicate exact decoded names so caller-chosen
// distinct casings such as id and ID remain accepted.
func validateObject(data []byte, pos int, scope fieldScope) (int, error) {
	pos = skipWhitespace(data, pos+1)
	if data[pos] == '}' {
		return pos + 1, nil
	}

	var seen objectMemberSet
	foldDuplicates := scope != scopeExactDupOnly && scope != scopeMetadata
	for {
		key, nextPos, err := readObjectKey(data, pos)
		if err != nil {
			return 0, err
		}
		dedupKey := key
		folded := ""
		if foldDuplicates {
			folded = foldMemberName(key)
			dedupKey = folded
		}
		if !seen.add(dedupKey) {
			return 0, &RequestFieldError{Key: strings.Clone(key)}
		}
		if scope != scopeExactDupOnly {
			if !foldDuplicates {
				folded = foldMemberName(key)
			}
			if err := validateCanonicalFieldCase(scope, key, folded); err != nil {
				return 0, err
			}
		}

		// Advance past ':' to inspect the member value under its child scope.
		pos = skipWhitespace(data, nextPos)
		pos = skipWhitespace(data, pos+1)
		if data[pos] == '[' {
			pos, err = validateArray(data, pos, childArrayElemScope(scope, key))
		} else {
			pos, err = validateValue(data, pos, childObjectScope(scope, key))
		}
		if err != nil {
			return 0, err
		}

		pos = skipWhitespace(data, pos)
		if data[pos] == '}' {
			return pos + 1, nil
		}
		// Advance past ',' to the next member name.
		pos = skipWhitespace(data, pos+1)
	}
}

// validateArray checks each element of a JSON array starting at '[', applying
// elemScope to direct object elements and exact-duplicate rules to nested arrays.
func validateArray(data []byte, pos int, elemScope fieldScope) (int, error) {
	pos = skipWhitespace(data, pos+1)
	if data[pos] == ']' {
		return pos + 1, nil
	}
	for {
		var err error
		pos, err = validateValue(data, pos, elemScope)
		if err != nil {
			return 0, err
		}
		pos = skipWhitespace(data, pos)
		if data[pos] == ']' {
			return pos + 1, nil
		}
		pos = skipWhitespace(data, pos+1)
	}
}

// add inserts name into the object's member set and reports whether name was
// absent before this call. Once count reaches inlineObjectKeyCap, existing
// inline names move into overflow so wider objects continue constant-time checks.
func (s *objectMemberSet) add(name string) bool {
	if s.overflow != nil {
		if _, exists := s.overflow[name]; exists {
			return false
		}
		s.overflow[name] = struct{}{}
		return true
	}
	for i := range s.count {
		if s.inline[i] == name {
			return false
		}
	}
	if s.count < inlineObjectKeyCap {
		s.inline[s.count] = name
		s.count++
		return true
	}
	s.overflow = make(map[string]struct{}, inlineObjectKeyCap*2)
	for i := range inlineObjectKeyCap {
		s.overflow[s.inline[i]] = struct{}{}
	}
	s.overflow[name] = struct{}{}
	return true
}

// validateCanonicalFieldCase rejects non-canonical spellings of core protocol
// fields registered in scope while ignoring unlisted extension fields.
func validateCanonicalFieldCase(scope fieldScope, name, folded string) error {
	if int(scope) >= len(canonicalFieldsByScope) {
		return nil
	}
	table := canonicalFieldsByScope[scope]
	if len(table) == 0 {
		return nil
	}
	canonical, ok := table[folded]
	if ok && name != canonical {
		return &RequestFieldError{
			Key:       strings.Clone(name),
			Canonical: canonical,
		}
	}
	return nil
}

// childObjectScope selects the validation scope for a direct JSON object value
// stored under key within parent. User-defined schema and tool-input objects
// return scopeExactDupOnly so arbitrary caller property names stay valid.
func childObjectScope(parent fieldScope, key string) fieldScope {
	switch parent {
	case scopeRoot:
		switch key {
		case "thinking":
			return scopeThinking
		case "reasoning":
			return scopeReasoning
		case "output_config":
			return scopeOutputConfig
		case "stream_options":
			return scopeStreamOptions
		case "metadata":
			return scopeMetadata
		case "response", "session":
			return scopeRoot
		case "generationConfig", "generation_config":
			return scopeGenerationConfig
		case "systemInstruction", "system_instruction":
			return scopeGeminiContent
		case "tool_choice", "toolConfig", "tool_config":
			return scopeToolChoice
		default:
			return scopeExactDupOnly
		}
	case scopeGenerationConfig:
		switch key {
		case "thinkingConfig", "thinking_config":
			return scopeGeminiThinkingConfig
		case "imageConfig", "image_config":
			return scopeGeminiImageConfig
		default:
			return scopeExactDupOnly
		}
	case scopeTool, scopeToolCall, scopeToolChoice:
		if key == "function" {
			return scopeToolFunction
		}
		return scopeExactDupOnly
	default:
		return scopeExactDupOnly
	}
}

// childArrayElemScope selects the validation scope for object elements inside a
// JSON array stored under key within parent.
func childArrayElemScope(parent fieldScope, key string) fieldScope {
	switch parent {
	case scopeRoot:
		switch key {
		case "messages":
			return scopeMessage
		case "system":
			return scopeSystemBlock
		case "input":
			return scopeInputItem
		case "contents":
			return scopeGeminiContent
		case "tools", "functions":
			return scopeTool
		case "items":
			return scopeBatchItem
		default:
			return scopeExactDupOnly
		}
	case scopeMessage:
		switch key {
		case "content":
			return scopeMessageContentBlock
		case "tool_calls":
			return scopeToolCall
		default:
			return scopeExactDupOnly
		}
	case scopeMessageContentBlock:
		if key == "content" {
			return scopeMessageContentBlock
		}
		return scopeExactDupOnly
	case scopeInputItem:
		switch key {
		case "content", "summary":
			return scopeInputContentPart
		case "tools":
			return scopeTool
		default:
			return scopeExactDupOnly
		}
	case scopeGeminiContent:
		if key == "parts" {
			return scopeGeminiPart
		}
		return scopeExactDupOnly
	default:
		return scopeExactDupOnly
	}
}

// readObjectKey decodes one JSON string starting at data[pos] == '"' and
// returns its unescaped Go string alongside the byte index after the closing quote.
// Unescaped names borrow data's backing bytes for the active validation call,
// while escaped names allocate through encoding/json.
func readObjectKey(data []byte, pos int) (string, int, error) {
	start := pos + 1
	i := start
	hasEscape := false
	for i < len(data) {
		switch data[i] {
		case '\\':
			hasEscape = true
			i += 2
		case '"':
			if !hasEscape {
				raw := data[start:i]
				if len(raw) == 0 {
					return "", i + 1, nil
				}
				// Unescaped key views stay confined to the synchronous validation
				// stack and never outlive data during this call.
				return unsafe.String(unsafe.SliceData(raw), len(raw)), i + 1, nil
			}
			var decoded string
			// Delegate escaped member names to encoding/json so Unicode and surrogate
			// escapes match downstream struct and map decoding byte for byte.
			if err := json.Unmarshal(data[pos:i+1], &decoded); err != nil {
				return "", 0, ErrInvalidRequestJSON
			}
			return decoded, i + 1, nil
		default:
			i++
		}
	}
	return "", 0, ErrInvalidRequestJSON
}

// skipString advances past one syntactically valid JSON string starting at '"'.
func skipString(data []byte, pos int) int {
	i := pos + 1
	for i < len(data) {
		switch data[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return len(data)
}

// skipPrimitive advances past a JSON number, boolean, or null literal.
func skipPrimitive(data []byte, pos int) int {
	for pos < len(data) {
		switch data[pos] {
		case ' ', '\t', '\n', '\r', ',', '}', ']':
			return pos
		default:
			pos++
		}
	}
	return pos
}

// skipWhitespace advances pos past JSON whitespace bytes.
func skipWhitespace(data []byte, pos int) int {
	for pos < len(data) {
		switch data[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// foldMemberName returns the canonical representative shared by every spelling
// of name that encoding/json treats as the same struct field under Unicode
// simple case folding. Lowercase ASCII names return unchanged without copying.
func foldMemberName(name string) string {
	needsFold := false
	for i := range len(name) {
		b := name[i]
		if (b >= 'A' && b <= 'Z') || b >= 0x80 {
			needsFold = true
			break
		}
	}
	if !needsFold {
		return name
	}
	return strings.Map(foldRune, name)
}

// foldRune maps r to one canonical representative of its Unicode simple
// case-folding orbit, choosing ASCII lowercase for Latin letters so ordinary
// lowercase names require no allocation.
func foldRune(r rune) rune {
	folded := r
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		if next < folded {
			folded = next
		}
	}
	if folded >= 'A' && folded <= 'Z' {
		folded += 'a' - 'A'
	}
	return folded
}

// buildCanonicalFieldsByScope constructs immutable lookup tables restricted to
// gateway-inspected routing, moderation, session-ownership, and billing fields
// whose single-field case variants could otherwise bypass gjson checks before
// struct decoding.
func buildCanonicalFieldsByScope() [fieldScopeCount]map[string]string {
	var scopes [fieldScopeCount]map[string]string
	register := func(scope fieldScope, fields ...string) {
		m := make(map[string]string, len(fields))
		for _, field := range fields {
			m[foldMemberName(field)] = field
		}
		scopes[scope] = m
	}

	register(scopeRoot,
		"model", "messages", "system", "system_instruction", "systemInstruction",
		"input", "instructions", "prompt", "contents", "tools", "tool_choice",
		"generation_config", "generationConfig", "stream", "stream_options",
		"max_tokens", "max_completion_tokens", "max_output_tokens",
		"thinking", "reasoning", "reasoning_effort", "output_config",
		"service_tier", "speed", "previous_response_id", "metadata",
		"prompt_cache_key", "background", "size", "quality", "n",
		"output_format", "image", "images", "mask", "resolution",
		"duration", "reference_images", "sdp", "session", "response",
		"query", "items",
	)
	register(scopeMetadata, "user_id")
	register(scopeThinking, "type", "budget_tokens")
	register(scopeReasoning, "effort")
	register(scopeOutputConfig, "effort")
	register(scopeStreamOptions, "include_usage")
	register(scopeGenerationConfig,
		"thinkingConfig", "thinking_config",
		"imageConfig", "image_config",
		"maxOutputTokens", "max_output_tokens",
	)
	register(scopeGeminiThinkingConfig,
		"thinkingBudget", "thinking_budget",
		"thinkingLevel", "thinking_level",
		"includeThoughts", "include_thoughts",
	)
	register(scopeGeminiImageConfig,
		"imageSize", "image_size",
		"aspectRatio", "aspect_ratio",
	)
	register(scopeMessage, "role", "content", "tool_calls")
	register(scopeMessageContentBlock,
		"type", "text", "thinking", "input", "content", "source", "image_url",
	)
	register(scopeToolCall, "type", "function")
	register(scopeSystemBlock, "type", "text")
	register(scopeInputItem,
		"type", "role", "content", "output", "summary", "tools",
	)
	register(scopeInputContentPart,
		"type", "text", "image_url", "file_url", "file_data",
	)
	register(scopeGeminiContent, "role", "parts")
	register(scopeGeminiPart,
		"text", "thought", "inlineData", "inline_data",
		"fileData", "file_data", "functionCall", "function_call",
		"functionResponse", "function_response",
	)
	register(scopeTool,
		"type", "name", "model", "function", "parameters", "input_schema",
	)
	register(scopeToolFunction, "name", "parameters", "arguments")
	register(scopeToolChoice, "type", "name", "function")
	register(scopeBatchItem,
		"prompt", "model", "size", "quality", "output_format", "background",
	)
	return scopes
}
