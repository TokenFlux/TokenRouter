package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// tool_schema.go repairs supported tool-schema patterns and parameter types
// through bounded edits to request JSON bytes.
// Tool schemas can need small compatibility repairs while request numeric
// literals, key ordering, and unrelated data keep their existing spelling. A
// structural parser records byte spans rather than decoding and rebuilding the
// complete request. Its contexts distinguish declaration roots, nested schema
// keywords, and instance values, so a pattern in message metadata or a default
// object is retained.
//
// Edits remain deferred until bounded parsing and full JSON validation finish.
// Null-type replacement rejects duplicate type members, and missing-type
// insertion uses a separate bounded decoded union check. Patching merges
// overlapping deletions but rejects overlapping replacements. Key and pattern
// decoding uses Go quoted-literal rules, so unsupported JSON escape forms can
// remain unmatched even when the document itself is valid.

const (
	// Traversal roles distinguish declarations, schemas, and ordinary instance data
	// so familiar keys in a message cannot select schema editing by themselves.
	openAIResponsesToolSchemaSkip        openAIResponsesToolSchemaContext = iota // parse values with editing skipped
	openAIResponsesToolSchemaDocument                                            // inspect request tools and input
	openAIResponsesToolSchemaTools                                               // inspect an array of tool declarations
	openAIResponsesToolSchemaTool                                                // inspect one tool declaration object
	openAIResponsesToolSchemaInput                                               // inspect request input item containers
	openAIResponsesToolSchemaInputItem                                           // probe an input item discriminator
	openAIResponsesToolSchemaToolCarrier                                         // inspect additional-tools carrier declarations
	openAIResponsesToolSchemaTypeProbe                                           // retain decoded string type fields
	openAIResponsesToolSchemaFunction                                            // inspect a wrapped function declaration
	openAIResponsesToolSchema                                                    // inspect one parameter schema object
	openAIResponsesToolSchemaArray                                               // inspect an array of schemas
	openAIResponsesToolSchemaMap                                                 // inspect named child schema values
	openAIResponsesToolSchemaOrArray                                             // inspect one schema or a schema array

	// Body, traversal-depth, and edit limits stop the entire compatibility pass
	// and retain the source body. Union byte/depth limits decline type insertion
	// for that root, while other recorded schema repairs can still be published.
	openAIResponsesToolSchemaMaxBodySize = 256 << 20 // maximum source body bytes
	openAIResponsesToolSchemaMaxDepth    = 128       // maximum parser nesting depth
	openAIResponsesToolSchemaMaxEdits    = 1 << 20   // maximum recorded edit count
	openAIResponsesObjectUnionMaxSize    = 1 << 20   // maximum union-root byte count
	openAIResponsesObjectUnionMaxDepth   = 32        // maximum union classification depth

	// openAIResponsesToolSchemaFallbackType supplies the raw JSON object label
	// for replacing an explicit null type at a supported parameter root.
	openAIResponsesToolSchemaFallbackType = `"object"`
)

var (
	// errOpenAIResponsesToolSchemaLimit distinguishes bounded-work exhaustion
	// from malformed input so the outer sanitizer can retain the source body.
	errOpenAIResponsesToolSchemaLimit = errors.New("OpenAI Responses tool schema safety limit exceeded")

	// Union needles avoid map decoding for ordinary roots; the escape needle
	// also admits roots whose oneOf or anyOf key uses escaped JSON spelling.
	openAIResponsesJSONOneOfKey       = []byte(`"oneOf"`)
	openAIResponsesJSONAnyOfKey       = []byte(`"anyOf"`)
	openAIResponsesJSONEscapeNeedle   = []byte{'\\'}
	openAIResponsesUnicodeKeyNeedle   = []byte(`\u`)
	openAIResponsesPatternFieldNeedle = []byte("pattern")
)

// ToolSchemaSanitizeOptions selects which schema compatibility repairs run
// together during a single structural traversal.
type ToolSchemaSanitizeOptions struct {
	// RepairParameterTypes enables explicit-null root replacement and bounded
	// object-only union root type insertion.
	RepairParameterTypes bool // repair parameter root type fields
	// RemoveLookaroundPatterns deletes schema pattern members containing regex
	// lookaround assertions.
	RemoveLookaroundPatterns bool // remove lookaround pattern constraints
}

// openAIResponsesToolSchemaOptions selects independent compatibility edits for
// the scoped schema parser.
type openAIResponsesToolSchemaOptions struct {
	// removeLookaroundPatterns selects recognized lookaround member deletion
	// after traversal establishes a schema context; instance-valued keywords remain skipped.
	removeLookaroundPatterns bool // remove lookaround schema constraints
	// replaceNullParameterTypes replaces null only when a parameter root has
	// one type member, retaining duplicate-key roots and nested property types.
	replaceNullParameterTypes bool // replace explicit null root types
	// injectObjectUnionRootObjectType selects missing-type insertion after one
	// parameters member passes the bounded decoded union classifier for its root.
	injectObjectUnionRootObjectType bool // insert classified union root type
}

// openAIResponsesToolSchemaContext identifies the structural role used to
// choose editable child values.
type openAIResponsesToolSchemaContext uint8

// openAIResponsesToolSchemaEdit replaces a half-open byte span in the
// unchanged source body.
type openAIResponsesToolSchemaEdit struct {
	// start and end use offsets in the unmodified body, with end excluded.
	// Equal offsets insert replacement text; an empty replacement deletes the
	// selected span when the patch builder publishes a candidate.
	start       int    // first replaced byte offset
	end         int    // excluded replacement end offset
	replacement string // inserted bytes or empty deletion
}

// openAIResponsesToolSchemaParser scans shared source bytes and records
// deferred compatibility edits. Its caller keeps the source stable during
// parsing and serializes operations on this mutable parser.
type openAIResponsesToolSchemaParser struct {
	// body is shared source storage. pos and edits use its unchanged byte
	// offsets; methods advance the scan and defer all source replacement until
	// the outer sanitizer has validated the complete input.
	body    []byte                           // shared source body bytes
	pos     int                              // current scan byte offset
	options openAIResponsesToolSchemaOptions // requested compatibility edit switches
	edits   []openAIResponsesToolSchemaEdit  // deferred source replacement spans
	// probeType retains the most recent successfully decoded string type in a
	// temporary input-item probe. Nonstring type values leave that earlier result available.
	probeType string // last decoded input type string
}

// SanitizeToolSchemas runs the requested parameter-type and pattern repairs in
// a single structural pass over body. Disabled options or empty input return
// body unchanged; when only pattern removal is requested and the conservative
// lookaround screen finds no candidate, source bytes pass through directly.
func SanitizeToolSchemas(body []byte, options ToolSchemaSanitizeOptions) ([]byte, bool, error) {
	if len(body) == 0 || (!options.RepairParameterTypes && !options.RemoveLookaroundPatterns) {
		return body, false, nil
	}
	removePatterns := options.RemoveLookaroundPatterns && openAIResponsesBodyMayContainRegexLookaround(body)
	if !options.RepairParameterTypes && !removePatterns {
		return body, false, nil
	}
	return sanitizeOpenAIResponsesToolSchemas(body, openAIResponsesToolSchemaOptions{
		removeLookaroundPatterns:        removePatterns,
		replaceNullParameterTypes:       options.RepairParameterTypes,
		injectObjectUnionRootObjectType: options.RepairParameterTypes,
	})
}

// SanitizeToolSchemaPatterns removes recognized lookaround pattern members
// from tool parameter schemas and supported nested schema keywords.
// Instance-valued keywords remain outside schema traversal. Empty input or a
// failed literal precheck returns source bytes without JSON validation;
// decoder failures can leave a pattern unmatched.
func SanitizeToolSchemaPatterns(body []byte) ([]byte, bool, error) {
	return SanitizeToolSchemas(body, ToolSchemaSanitizeOptions{RemoveLookaroundPatterns: true})
}

// SanitizeToolParameterTypes replaces a single explicit null type at a
// parameter root and inserts a missing object type for a qualifying decoded
// root union. Ordinary missing types and nested property types stay unchanged.
// Empty input and parser safety limits pass through source bytes; malformed
// parsed input returns an error.
func SanitizeToolParameterTypes(body []byte) ([]byte, bool, error) {
	return SanitizeToolSchemas(body, ToolSchemaSanitizeOptions{RepairParameterTypes: true})
}

// openAIResponsesBodyMayContainRegexLookaround screens for an opening
// parenthesis (literal or Unicode-escaped) alongside a literal or escaped
// pattern member key. A match allows scoped parsing while avoiding full-body
// scans when ordinary message code contains parentheses without pattern keys.
func openAIResponsesBodyMayContainRegexLookaround(body []byte) bool {
	// Both a parenthesis and a literal or Unicode-escaped pattern key must appear
	// before any schema pattern member can decode to a lookaround assertion.
	hasParen := bytes.IndexByte(body, '(') >= 0 || bytes.Contains(body, []byte(`\u0028`))
	if !hasParen {
		return false
	}
	return bytes.Contains(body, openAIResponsesPatternFieldNeedle) ||
		bytes.Contains(body, openAIResponsesUnicodeKeyNeedle)
}

// hasRegexLookaround reports any of the four lookahead/lookbehind marker
// substrings in a pattern. It does not validate full regular-expression
// syntax, so a matching literal substring is sufficient for this compatibility
// selection.
func hasRegexLookaround(pattern string) bool {
	return strings.Contains(pattern, "(?=") || strings.Contains(pattern, "(?!") ||
		strings.Contains(pattern, "(?<=") || strings.Contains(pattern, "(?<!")
}

// sanitizeOpenAIResponsesToolSchemas parses one JSON value and applies
// selected schema byte edits. Oversized bodies and depth/edit exhaustion
// return the original bytes with false and no error; those paths can stop
// before complete validation. Other invalid UTF-8, syntax, trailing-data, or
// patch failures return an error.
//
// Parsing records edits without changing source storage, then checks full JSON
// validity before publication. An empty edit list keeps the input slice. A
// successful changed candidate comes from the patch builder, which copies
// untouched ranges and validates the rebuilt document. This ordering prevents
// a partial repair from replacing a request when a later member fails.
func sanitizeOpenAIResponsesToolSchemas(
	body []byte, options openAIResponsesToolSchemaOptions,
) ([]byte, bool, error) {
	// The compatibility pass has its own body cap even when ingress allows
	// larger requests. Skip all parsing here instead of retaining a partial repair.
	if len(body) > openAIResponsesToolSchemaMaxBodySize {
		return body, false, nil
	}
	if !utf8.Valid(body) {
		return nil, false, fmt.Errorf("sanitize OpenAI Responses tool schemas: invalid UTF-8")
	}
	p := openAIResponsesToolSchemaParser{body: body, options: options}
	if err := p.parseValue(openAIResponsesToolSchemaDocument, false, 0); err != nil {
		// A safety stop can occur after edits were collected. Return the source
		// body so bounded-work exhaustion cannot publish only a prefix of repairs.
		if errors.Is(err, errOpenAIResponsesToolSchemaLimit) {
			return body, false, nil
		}
		return nil, false, err
	}
	p.skipWhitespace()
	if p.pos != len(body) {
		return nil, false, fmt.Errorf("sanitize OpenAI Responses tool schemas: trailing data at byte %d", p.pos)
	}
	if !json.Valid(body) {
		return nil, false, fmt.Errorf("sanitize OpenAI Responses tool schemas: invalid JSON")
	}
	if len(p.edits) == 0 {
		return body, false, nil
	}
	return applyOpenAIResponsesToolSchemaEdits(body, p.edits)
}

// parseValue consumes one value at the current offset using its structural
// context. It advances pos and can record edits, but leaves complete-document
// and trailing-data validation to the outer sanitizer. Excess depth produces
// the shared limit error rather than continuing recursion.
//
// Object input items are first probed for their most recent decoded string
// type so tool declarations and additional_tools carriers receive appropriate
// traversal. Object and array schema contexts select different child roles,
// while skipped values are still parsed for syntax. That separation keeps
// message data outside schema editing even when it contains familiar parameter
// or pattern keys.
func (p *openAIResponsesToolSchemaParser) parseValue(
	context openAIResponsesToolSchemaContext, schemaRoot bool, depth int,
) error {
	// Cap value traversal before descending through another object or array.
	if depth > openAIResponsesToolSchemaMaxDepth {
		return errOpenAIResponsesToolSchemaLimit
	}
	p.skipWhitespace()
	if p.pos >= len(p.body) {
		return p.syntaxError("expected value")
	}
	if p.body[p.pos] == '{' {
		switch context {
		case openAIResponsesToolSchemaInput:
			context = openAIResponsesToolSchemaInputItem
		case openAIResponsesToolSchemaOrArray, openAIResponsesToolSchemaArray:
			context = openAIResponsesToolSchema
		}
	}
	switch p.body[p.pos] {
	case '{':
		return p.parseObject(context, schemaRoot, depth+1)
	case '[':
		return p.parseArray(context, depth+1)
	case '"':
		_, _, err := p.parseString()
		return err
	case 't':
		return p.parseKeyword("true")
	case 'f':
		return p.parseKeyword("false")
	case 'n':
		return p.parseKeyword("null")
	default:
		return p.parseNumber()
	}
}

// parseObject consumes members and records edits allowed by the current
// schema/declaration context. It keeps source offsets for member runs and
// parameter roots rather than reconstructing decoded objects. Syntax errors or
// edit limits return before the outer sanitizer can publish any recorded
// changes.
//
// Input-item objects resolve their discriminator lazily only when they contain
// a parameters, function, or tools member, keeping ordinary conversation items
// single-pass. Pattern deletion spans include the appropriate adjacent comma
// while retaining the next member's preceding whitespace. Root null repair
// requires one type member, since duplicate effective values are ambiguous.
// Missing-type union insertion requires one parameters member and a successful
// bounded classification of that root. Nested deletions can overlap; the final
// patch builder resolves deletion overlap and rejects conflicting
// replacements.
func (p *openAIResponsesToolSchemaParser) parseObject(
	context openAIResponsesToolSchemaContext, schemaRoot bool, depth int,
) error {
	objectStart := p.pos
	p.pos++
	p.skipWhitespace()
	if p.consume('}') {
		return nil
	}
	previousComma := -1
	deleteRunStart := -1
	deleteRunPreviousComma := -1
	rootTypeCount := 0
	nullRootTypeStart := -1
	nullRootTypeEnd := -1
	parameterCount := 0
	parameterValueStart := -1
	parameterValueEnd := -1
	for {
		p.skipWhitespace()
		memberStart := p.pos
		keyStart, keyEnd, err := p.parseString()
		if err != nil {
			return err
		}
		key := p.body[keyStart:keyEnd]
		p.skipWhitespace()
		if !p.consume(':') {
			return p.syntaxError("expected colon")
		}
		p.skipWhitespace()
		valueStart := p.pos

		if context == openAIResponsesToolSchemaInputItem &&
			openAIResponsesJSONStringMatchesAny(key, "parameters", "function", "tools") {
			// Type can appear after parameters or repeat later in an input item.
			// Probe the full object once on demand so ordinary messages avoid a second pass.
			probe := openAIResponsesToolSchemaParser{body: p.body, pos: objectStart}
			if err := probe.parseValue(openAIResponsesToolSchemaTypeProbe, false, depth-1); err != nil {
				return err
			}
			switch probe.probeType {
			case "function", "custom", "tool_search", "namespace":
				context = openAIResponsesToolSchemaTool
			case "additional_tools":
				context = openAIResponsesToolSchemaToolCarrier
			default:
				context = openAIResponsesToolSchemaSkip
			}
		}
		childContext, childSchemaRoot := openAIResponsesToolSchemaChildContext(context, key)
		if err := p.parseValue(childContext, childSchemaRoot, depth); err != nil {
			return err
		}
		valueEnd := p.pos
		p.skipWhitespace()

		deleteMember := false
		if context == openAIResponsesToolSchemaTypeProbe && openAIResponsesJSONStringEquals(key, "type") {
			if value, ok := decodeOpenAIResponsesJSONStringValue(p.body[valueStart:valueEnd]); ok {
				p.probeType = strings.TrimSpace(value)
			}
		}
		if (context == openAIResponsesToolSchemaTool || context == openAIResponsesToolSchemaFunction) &&
			openAIResponsesJSONStringEquals(key, "parameters") {
			parameterCount++
			parameterValueStart = valueStart
			parameterValueEnd = valueEnd
		}
		// A pattern key is editable only in schema context. Keeping instance
		// values in skipped contexts prevents deleting lookaround-like user data.
		if context == openAIResponsesToolSchema && openAIResponsesJSONStringEquals(key, "pattern") && p.options.removeLookaroundPatterns {
			if pattern, ok := decodeOpenAIResponsesJSONStringValue(p.body[valueStart:valueEnd]); ok && hasRegexLookaround(pattern) {
				deleteMember = true
			}
		}
		if context == openAIResponsesToolSchema && schemaRoot && openAIResponsesJSONStringEquals(key, "type") {
			// Duplicate JSON keys have parser-dependent effective values. Repair the
			// root type when it occurs once; choosing among duplicates could
			// change the client's effective schema.
			rootTypeCount++
			if bytes.Equal(p.body[valueStart:valueEnd], []byte("null")) {
				nullRootTypeStart = valueStart
				nullRootTypeEnd = valueEnd
			}
		}

		if p.pos >= len(p.body) {
			return p.syntaxError("unterminated object")
		}
		if deleteMember && deleteRunStart < 0 {
			deleteRunStart = memberStart
			deleteRunPreviousComma = previousComma
		}
		if !deleteMember && deleteRunStart >= 0 {
			// Remove through the delimiter after the deleted run, but retain the
			// whitespace that originally preceded the next member.
			if err := p.addEdit(deleteRunStart, previousComma+1, ""); err != nil {
				return err
			}
			deleteRunStart = -1
			deleteRunPreviousComma = -1
		}
		if p.body[p.pos] == ',' {
			comma := p.pos
			p.pos++
			previousComma = comma
			continue
		}
		if p.body[p.pos] != '}' {
			return p.syntaxError("expected comma or closing brace")
		}
		if deleteRunStart >= 0 {
			start := deleteRunStart
			if deleteRunPreviousComma >= 0 {
				start = deleteRunPreviousComma
			}
			if err := p.addEdit(start, p.pos, ""); err != nil {
				return err
			}
		}
		// A unique root type has one interpretation for this compatibility edit.
		// Duplicate roots keep their spelling because effective-value selection can differ.
		if p.options.replaceNullParameterTypes && rootTypeCount == 1 && nullRootTypeStart >= 0 {
			if err := p.addEdit(nullRootTypeStart, nullRootTypeEnd, openAIResponsesToolSchemaFallbackType); err != nil {
				return err
			}
		}
		// Probe one complete parameters root before inserting a type. Multiple
		// parameters members are ambiguous and remain outside this insertion rule.
		if p.options.injectObjectUnionRootObjectType &&
			(context == openAIResponsesToolSchemaTool || context == openAIResponsesToolSchemaFunction) &&
			parameterCount == 1 && parameterValueStart >= 0 {
			if edit, ok := openAIMissingRootObjectUnionTypeEdit(
				p.body[parameterValueStart:parameterValueEnd], parameterValueStart,
			); ok {
				if err := p.addEdit(edit.start, edit.end, edit.replacement); err != nil {
					return err
				}
			}
		}
		p.pos++
		return nil
	}
}

// openAIMissingRootObjectUnionTypeEdit returns an insertion at a parameter
// object's opening brace when its decoded union qualifies and type is absent.
// Oversized, nonobject, invalid, or unqualified roots return false. The
// absoluteStart offset places the edit in the original complete body.
//
// The byte precheck includes an escape needle so escaped union keys can reach
// map decoding. Decoded map keys follow encoding/json's duplicate-key
// handling. Qualification examines explicit object labels or nested
// oneOf/anyOf branches; it is not a complete JSON Schema satisfiability check.
// The returned zero-width edit adds a member while retaining the root's
// existing bytes.
func openAIMissingRootObjectUnionTypeEdit(raw []byte, absoluteStart int) (openAIResponsesToolSchemaEdit, bool) {
	// Bound the extra decoded-map probe independently of the streaming parser,
	// retaining the root when classification would exceed its own byte budget.
	if len(raw) > openAIResponsesObjectUnionMaxSize {
		return openAIResponsesToolSchemaEdit{}, false
	}
	if !bytes.Contains(raw, openAIResponsesJSONOneOfKey) && !bytes.Contains(raw, openAIResponsesJSONAnyOfKey) &&
		!bytes.Contains(raw, openAIResponsesJSONEscapeNeedle) {
		return openAIResponsesToolSchemaEdit{}, false
	}
	// Only a decoded object can justify inserting a parameter-root member.
	// Unsupported or invalid values leave their raw representation available to the request.
	var schema map[string]json.RawMessage
	if len(raw) < 2 || raw[0] != '{' || json.Unmarshal(raw, &schema) != nil {
		return openAIResponsesToolSchemaEdit{}, false
	}
	if _, hasType := schema["type"]; hasType || !openAIResponsesSchemaHasObjectOnlyUnion(schema, 0) {
		return openAIResponsesToolSchemaEdit{}, false
	}
	return openAIResponsesToolSchemaEdit{
		start:       absoluteStart + 1,
		end:         absoluteStart + 1,
		replacement: `"type":"object",`,
	}, true
}

// openAIResponsesSchemaHasObjectOnlyUnion classifies a decoded root by its
// oneOf and anyOf arrays. At least one such keyword must exist, and every
// encountered array must be nonempty with all branches accepted by the bounded
// object classifier. Depth exhaustion or failed array decoding returns false.
//
// Both union families participate when present, so one unqualified branch
// rejects the root even if the other union contains explicit objects. Other
// schema keywords are outside this classification. The result authorizes a
// compatibility type insertion based on these declared shapes; it does not
// prove that every combination of constraints admits an object.
func openAIResponsesSchemaHasObjectOnlyUnion(schema map[string]json.RawMessage, depth int) bool {
	// An exhausted recursive classification cannot justify a root-type edit.
	// Return false so the original union remains available to the upstream.
	if depth > openAIResponsesObjectUnionMaxDepth {
		return false
	}
	found := false
	for _, keyword := range []string{"oneOf", "anyOf"} {
		raw, ok := schema[keyword]
		if !ok {
			continue
		}
		found = true
		var branches []json.RawMessage
		// An unparsed or empty branch list supplies no positive object evidence.
		// Keep the root unchanged instead of inferring a type from the union key alone.
		if json.Unmarshal(raw, &branches) != nil || len(branches) == 0 {
			return false
		}
		for _, branch := range branches {
			if !openAIResponsesSchemaIsObjectOnly(branch, depth+1) {
				return false
			}
		}
	}
	return found
}

// openAIResponsesSchemaIsObjectOnly classifies one raw branch using an
// explicit type or a nested decoded union. Depth exhaustion and decoding
// failures return false. A present type is authoritative here: it must decode
// as the exact object string, and other constraints are not examined on that
// path.
//
// When type is absent, the helper delegates both supported union families with
// an increased depth. Properties alone do not establish object identity. This
// ordering gives the insertion probe a bounded structural rule while leaving
// unsupported schema constraints and reference resolution outside its
// decision.
func openAIResponsesSchemaIsObjectOnly(raw json.RawMessage, depth int) bool {
	// An exhausted recursive classification cannot justify a root-type edit.
	// Return false so the original union remains available to the upstream.
	if depth > openAIResponsesObjectUnionMaxDepth {
		return false
	}
	// Nonobject or invalid branches cannot supply a declared object shape.
	// Treat their failed map decode as unqualified rather than applying a root insertion.
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil {
		return false
	}
	// Explicit type controls this branch's classification. A different or
	// unsupported type cannot be rescued by another union in the same object.
	if rawType, ok := schema["type"]; ok {
		var schemaType string
		return json.Unmarshal(rawType, &schemaType) == nil && schemaType == "object"
	}
	return openAIResponsesSchemaHasObjectOnlyUnion(schema, depth+1)
}

// parseArray consumes array values using the child role selected by its
// current context. It advances pos and propagates partial-parse errors;
// declaration, input, and schema arrays use distinct roles, while other arrays
// are parsed with editing skipped.
func (p *openAIResponsesToolSchemaParser) parseArray(
	context openAIResponsesToolSchemaContext, depth int,
) error {
	p.pos++
	p.skipWhitespace()
	if p.consume(']') {
		return nil
	}
	childContext := openAIResponsesToolSchemaSkip
	switch context {
	case openAIResponsesToolSchemaTools:
		childContext = openAIResponsesToolSchemaTool
	case openAIResponsesToolSchemaInput:
		childContext = openAIResponsesToolSchemaInputItem
	case openAIResponsesToolSchemaArray, openAIResponsesToolSchemaOrArray:
		childContext = openAIResponsesToolSchema
	}
	for {
		if err := p.parseValue(childContext, false, depth); err != nil {
			return err
		}
		p.skipWhitespace()
		if p.consume(',') {
			continue
		}
		if !p.consume(']') {
			return p.syntaxError("expected comma or closing bracket")
		}
		return nil
	}
}

// openAIResponsesToolSchemaChildContext selects traversal roles from the
// parent context and a raw member key. It also marks a declaration's
// parameters member as a schema root, which limits null-type repair to that
// location. Unrecognized keys enter the skip context.
//
// Schema-valued keywords, arrays of schemas, and maps of schemas need
// different child rules. Instance-valued default/example/const/enum fields
// therefore remain parsed data rather than editable schemas. Escaped key
// matching uses the quoted-string helper; a decode failure means the key does
// not select a recognized role.
func openAIResponsesToolSchemaChildContext(
	context openAIResponsesToolSchemaContext, key []byte,
) (openAIResponsesToolSchemaContext, bool) {
	switch context {
	case openAIResponsesToolSchemaDocument:
		switch {
		case openAIResponsesJSONStringEquals(key, "tools"):
			return openAIResponsesToolSchemaTools, false
		case openAIResponsesJSONStringEquals(key, "input"):
			return openAIResponsesToolSchemaInput, false
		}
	case openAIResponsesToolSchemaTool:
		switch {
		case openAIResponsesJSONStringEquals(key, "parameters"):
			return openAIResponsesToolSchema, true
		case openAIResponsesJSONStringEquals(key, "function"):
			return openAIResponsesToolSchemaFunction, false
		case openAIResponsesJSONStringEquals(key, "tools"):
			return openAIResponsesToolSchemaTools, false
		}
	case openAIResponsesToolSchemaToolCarrier:
		if openAIResponsesJSONStringEquals(key, "tools") {
			return openAIResponsesToolSchemaTools, false
		}
	case openAIResponsesToolSchemaFunction:
		switch {
		case openAIResponsesJSONStringEquals(key, "parameters"):
			return openAIResponsesToolSchema, true
		case openAIResponsesJSONStringEquals(key, "tools"):
			return openAIResponsesToolSchemaTools, false
		}
	case openAIResponsesToolSchema:
		switch {
		case openAIResponsesJSONStringMatchesAny(key,
			"additionalProperties", "additionalItems", "contains", "not", "if", "then", "else",
			"propertyNames", "unevaluatedProperties", "unevaluatedItems"):
			return openAIResponsesToolSchema, false
		case openAIResponsesJSONStringEquals(key, "items"):
			return openAIResponsesToolSchemaOrArray, false
		case openAIResponsesJSONStringMatchesAny(key, "anyOf", "oneOf", "allOf", "prefixItems"):
			return openAIResponsesToolSchemaArray, false
		case openAIResponsesJSONStringMatchesAny(key,
			"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies"):
			return openAIResponsesToolSchemaMap, false
		}
	case openAIResponsesToolSchemaMap:
		return openAIResponsesToolSchema, false
	}
	return openAIResponsesToolSchemaSkip, false
}

// parseString returns the half-open quoted byte span of a scanned string and
// advances pos. It checks JSON escape spelling, hexadecimal digits, and raw
// control bytes without decoding the value. Errors may leave pos advanced; the
// outer sanitizer separately checks UTF-8 and full JSON validity.
func (p *openAIResponsesToolSchemaParser) parseString() (int, int, error) {
	body := p.body
	if p.pos >= len(body) || body[p.pos] != '"' {
		return 0, 0, p.syntaxError("expected string")
	}
	start := p.pos
	i := start + 1
	for i < len(body) {
		// Advance across unescaped non-control bytes in registers before handling
		// quotes, escapes, or invalid control characters.
		for i < len(body) {
			b := body[i]
			if b == '"' {
				p.pos = i + 1
				return start, i + 1, nil
			}
			if b == '\\' || b < 0x20 {
				break
			}
			i++
		}
		if i >= len(body) {
			break
		}
		if body[i] < 0x20 {
			// Raw control bytes cannot remain inside a JSON quoted string.
			p.pos = i
			return 0, 0, p.syntaxError("control character in string")
		}
		i++
		if i >= len(body) {
			p.pos = i
			return 0, 0, p.syntaxError("unterminated string escape")
		}
		switch body[i] {
		case 'u':
			if i+4 >= len(body) {
				p.pos = i
				return 0, 0, p.syntaxError("short unicode escape")
			}
			for _, digit := range body[i+1 : i+5] {
				if !isOpenAIResponsesJSONHexDigit(digit) {
					p.pos = i
					return 0, 0, p.syntaxError("invalid unicode escape")
				}
			}
			// Skip the u marker and its four hexadecimal digits.
			i += 5
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			i++
		default:
			p.pos = i
			return 0, 0, p.syntaxError("invalid string escape")
		}
	}
	p.pos = len(body)
	return 0, 0, p.syntaxError("unterminated string")
}

// isOpenAIResponsesJSONHexDigit reports whether a byte is an ASCII hexadecimal
// digit.
func isOpenAIResponsesJSONHexDigit(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

// parseKeyword consumes the expected JSON literal at the current parser
// offset. A mismatched or short literal returns a syntax error without
// advancing pos; successful matching advances by that literal's byte length.
func (p *openAIResponsesToolSchemaParser) parseKeyword(keyword string) error {
	if len(p.body)-p.pos < len(keyword) || string(p.body[p.pos:p.pos+len(keyword)]) != keyword {
		return p.syntaxError("invalid literal")
	}
	p.pos += len(keyword)
	return nil
}

// parseNumber consumes JSON numeric spelling without converting it to a
// numeric value. It checks sign, leading zero, fraction, and exponent syntax,
// leaving delimiter checking to its caller. Syntax failures can leave the
// parser offset advanced.
func (p *openAIResponsesToolSchemaParser) parseNumber() error {
	start := p.pos
	if p.consume('-') && p.pos >= len(p.body) {
		return p.syntaxError("invalid number")
	}
	if p.consume('0') {
		if p.pos < len(p.body) && p.body[p.pos] >= '0' && p.body[p.pos] <= '9' {
			return p.syntaxError("invalid leading zero")
		}
	} else {
		if p.pos >= len(p.body) || p.body[p.pos] < '1' || p.body[p.pos] > '9' {
			return p.syntaxError("invalid number")
		}
		for p.pos < len(p.body) && p.body[p.pos] >= '0' && p.body[p.pos] <= '9' {
			p.pos++
		}
	}
	if p.consume('.') {
		fractionStart := p.pos
		for p.pos < len(p.body) && p.body[p.pos] >= '0' && p.body[p.pos] <= '9' {
			p.pos++
		}
		if p.pos == fractionStart {
			return p.syntaxError("invalid fraction")
		}
	}
	if p.pos < len(p.body) && (p.body[p.pos] == 'e' || p.body[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.body) && (p.body[p.pos] == '+' || p.body[p.pos] == '-') {
			p.pos++
		}
		exponentStart := p.pos
		for p.pos < len(p.body) && p.body[p.pos] >= '0' && p.body[p.pos] <= '9' {
			p.pos++
		}
		if p.pos == exponentStart {
			return p.syntaxError("invalid exponent")
		}
	}
	if p.pos == start {
		return p.syntaxError("invalid value")
	}
	return nil
}

// addEdit appends a replacement for a valid half-open body span, including
// zero-width insertions. Invalid spans return a syntax error, and reaching the
// edit cap returns the shared limit error before appending another change.
func (p *openAIResponsesToolSchemaParser) addEdit(start, end int, replacement string) error {
	if start < 0 || end < start || end > len(p.body) {
		return p.syntaxError("invalid patch span")
	}
	// Bound retained patch storage before appending another replacement span.
	if len(p.edits) >= openAIResponsesToolSchemaMaxEdits {
		return errOpenAIResponsesToolSchemaLimit
	}
	p.edits = append(p.edits, openAIResponsesToolSchemaEdit{start: start, end: end, replacement: replacement})
	return nil
}

// skipWhitespace advances pos across JSON's four permitted whitespace byte
// values.
func (p *openAIResponsesToolSchemaParser) skipWhitespace() {
	for p.pos < len(p.body) {
		switch p.body[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// consume advances pos by one when the current byte matches want. A different
// byte or end of input returns false and leaves the offset unchanged, allowing
// the caller to test delimiters in order.
func (p *openAIResponsesToolSchemaParser) consume(want byte) bool {
	if p.pos < len(p.body) && p.body[p.pos] == want {
		p.pos++
		return true
	}
	return false
}

// syntaxError annotates a schema-parser failure with its current zero-based
// byte offset.
func (p *openAIResponsesToolSchemaParser) syntaxError(message string) error {
	return fmt.Errorf("sanitize OpenAI Responses tool schemas: %s at byte %d", message, p.pos)
}

// decodeOpenAIResponsesJSONString uses strconv.Unquote to decode the supplied
// quoted literal. It returns the dependency error for unsupported escape
// forms, including JSON-only escaped slash or surrogate pairs; callers treat
// failed decoding as an unmatched key or value.
func decodeOpenAIResponsesJSONString(raw []byte) (string, error) {
	decoded, err := strconv.Unquote(string(raw))
	if err != nil {
		return "", err
	}
	return decoded, nil
}

// openAIResponsesJSONStringEquals compares a quoted key with want, using raw
// contents when no escape is present. Escaped keys use the quoted-literal
// decoder; missing quotes or a decoding error returns false rather than
// selecting a schema role.
func openAIResponsesJSONStringEquals(raw []byte, want string) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	if !bytes.Contains(raw, []byte{'\\'}) {
		return bytes.Equal(raw[1:len(raw)-1], []byte(want))
	}
	// Escaped keys require decoding before role matching. Unsupported quoted
	// forms select no role, while the outer scan still establishes JSON validity.
	decoded, err := decodeOpenAIResponsesJSONString(raw)
	return err == nil && decoded == want
}

// openAIResponsesJSONStringMatchesAny reports whether the quoted key matches
// any supplied candidate label.
func openAIResponsesJSONStringMatchesAny(raw []byte, candidates ...string) bool {
	for _, candidate := range candidates {
		if openAIResponsesJSONStringEquals(raw, candidate) {
			return true
		}
	}
	return false
}

// decodeOpenAIResponsesJSONStringValue returns decoded text from a slice whose
// first and last bytes are quotes. Nonquoted values or quoted-literal errors
// return empty text and false, so schema decisions can skip an unsupported
// value.
func decodeOpenAIResponsesJSONStringValue(raw []byte) (string, bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", false
	}
	// A failed quoted-literal decode supplies no pattern or discriminator text.
	// Other recognized members remain eligible for their own edits in the same schema.
	decoded, err := decodeOpenAIResponsesJSONString(raw)
	return decoded, err == nil
}

// applyOpenAIResponsesToolSchemaEdits sorts and merges validated body spans,
// then copies a candidate with their replacements. Callers provide in-range
// spans from addEdit. The edit slice and its backing storage are reordered and
// compacted; source body bytes remain unchanged.
//
// Overlapping deletion spans merge because either deletion removes the covered
// bytes. An overlap involving replacement text returns an error, since
// choosing one could change the intended member edit. Untouched ranges retain
// their raw spelling, and invalid rebuilt JSON is rejected. Success returns
// true even for an empty edit list, so the outer sanitizer avoids calling this
// builder when no edits were recorded.
func applyOpenAIResponsesToolSchemaEdits(
	body []byte, edits []openAIResponsesToolSchemaEdit,
) ([]byte, bool, error) {
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end < edits[j].end
	})

	// Nested removals can cover the same bytes. Merge deletion spans while
	// retaining distinct replacement positions, whose overlap would have ambiguous output.
	merged := edits[:0]
	for _, edit := range edits {
		if len(merged) == 0 || edit.start >= merged[len(merged)-1].end {
			merged = append(merged, edit)
			continue
		}
		previous := &merged[len(merged)-1]
		if previous.replacement != "" || edit.replacement != "" {
			return nil, false, fmt.Errorf("sanitize OpenAI Responses tool schemas: overlapping patches")
		}
		if edit.end > previous.end {
			previous.end = edit.end
		}
	}

	delta := 0
	for _, edit := range merged {
		delta += len(edit.replacement) - (edit.end - edit.start)
	}
	sanitized := make([]byte, 0, len(body)+delta)
	cursor := 0
	for _, edit := range merged {
		sanitized = append(sanitized, body[cursor:edit.start]...)
		sanitized = append(sanitized, edit.replacement...)
		cursor = edit.end
	}
	sanitized = append(sanitized, body[cursor:]...)
	if !json.Valid(sanitized) {
		return nil, false, fmt.Errorf("sanitize OpenAI Responses tool schemas: patch produced invalid JSON")
	}
	return sanitized, true, nil
}
