package requeststate

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// codex_bootstrap.go recognizes Codex bootstrap tool-output items and rewrites
// them as user input messages.
// Codex can inject delegation or automation bootstrap text using a
// tool-output-shaped item without a call identifier. Recognized bootstrap items
// are reclassified as user messages at their existing input positions,
// retaining the output text as the user input.
//
// Conservative byte and zero-copy input screening skips full uniqueness and map
// decoding when no missing-ID bootstrap candidate can exist. When a candidate
// is present, the normalizer rejects duplicate JSON members before decoding an
// object so conflicting discriminators or identities cannot silently collapse
// during a rewrite. Candidate items need absent or blank string call IDs;
// delegation permits other identified history and a string previous response ID,
// while automation permits only bootstrap context. Narrow text/XML recognition
// decides which items qualify and json.Number retains unrelated numeric
// spelling during whole-object encoding. Declined requests return the supplied
// bytes; recognition establishes a supported format, leaving authorization and
// source trust with the caller.

var (
	// These byte markers screen raw JSON before structural candidate inspection;
	// a Unicode escape marker keeps escaped ASCII spellings eligible for probing.
	codexBootstrapUnicodeEscapeNeedle = []byte(`\u`)
	codexBootstrapOutputTypeNeedle    = []byte("function_call_output")
	codexBootstrapAppNamespaceNeedle  = []byte("codex_app")
	codexBootstrapTUINamespaceNeedle  = []byte("codex_tui")
)

// NormalizeCodexBootstraps evaluates automation and delegation bootstrap
// repairs in sequence while validating member uniqueness and decoding the
// request object at most once. It returns the resulting bytes alongside
// separate change flags for automation and delegation; declined requests return
// the supplied slice and false flags.
func NormalizeCodexBootstraps(body []byte) ([]byte, bool, bool) {
	if !mayContainCodexBootstrapCandidate(body, true, true) {
		return body, false, false
	}
	// A map rewrite would collapse duplicate members and choose one identity or
	// discriminator. Decline before decoding so the supplied ambiguity remains visible.
	if !HasUniqueJSONMembers(body) {
		return body, false, false
	}
	request, ok := decodeCodexBootstrapRequest(body)
	if !ok {
		return body, false, false
	}
	automationChanged := applyCodexCallOutputBootstrap(request, IsCodexAutomationCandidate, false)
	delegationChanged := applyCodexCallOutputBootstrap(request, IsCodexDelegationCandidate, true)
	if !automationChanged && !delegationChanged {
		return body, false, false
	}
	normalized, err := json.Marshal(request)
	if err != nil {
		// Reencoding failed after temporary object changes. Retain the original
		// request so callers cannot adopt a partial bootstrap conversion.
		return body, false, false
	}
	return normalized, automationChanged, delegationChanged
}

// NormalizeCodexDelegationBootstrap reclassifies recognized missing-ID
// delegation output items as user input. It permits other identified historical
// calls or item references and string previous-response evidence; declined
// input retains the supplied slice and false.
func NormalizeCodexDelegationBootstrap(body []byte) ([]byte, bool) {
	if !mayContainCodexBootstrapCandidate(body, false, true) {
		return body, false
	}
	// A resumed delegation can accompany identified call history. Permit that
	// context while still rejecting a call ID on the injected candidate itself.
	return NormalizeCodexCallOutputBootstrap(body, IsCodexDelegationCandidate, true)
}

// NormalizeCodexAutomationBootstrap reclassifies recognized automation text or
// heartbeat items as user input. Nonblank previous-response evidence or other
// call/reference context declines bootstrap conversion, returning the supplied
// bytes and false.
func NormalizeCodexAutomationBootstrap(body []byte) ([]byte, bool) {
	if !mayContainCodexBootstrapCandidate(body, true, false) {
		return body, false
	}
	return NormalizeCodexCallOutputBootstrap(body, IsCodexAutomationCandidate, false)
}

// NormalizeCodexCallOutputBootstrap returns rewritten bytes when supported
// missing-ID bootstrap items are converted to user messages. Callers supply a
// stable, nonnil candidate predicate; it is evaluated during both validation
// and conversion and must leave decoded items unchanged.
//
// Unique-member validation precedes decoding so a conflicting field cannot
// disappear when the object is rebuilt. Candidate call_id values may be absent
// or blank strings; ordinary identified history is admitted only when
// allowHistoricalContext is true. Rewritten items retain their output text and
// input order while losing their tool-output wrapper. The whole object is
// reencoded with json.Number values, and any rejection or lack of conversion
// returns the original slice with false.
func NormalizeCodexCallOutputBootstrap(body []byte, isCandidate func(map[string]any) bool, allowHistoricalContext bool) ([]byte, bool) {
	// A map rewrite would collapse duplicate members and choose one identity or
	// discriminator. Decline before decoding so the supplied ambiguity remains visible.
	if !HasUniqueJSONMembers(body) {
		return body, false
	}
	request, ok := decodeCodexBootstrapRequest(body)
	if !ok {
		return body, false
	}
	if !applyCodexCallOutputBootstrap(request, isCandidate, allowHistoricalContext) {
		return body, false
	}
	normalized, err := json.Marshal(request)
	if err != nil {
		// Reencoding failed after temporary object changes. Retain the original
		// request so callers cannot adopt a partial bootstrap conversion.
		return body, false
	}
	return normalized, true
}

// mayContainCodexBootstrapCandidate conservatively screens body for any
// automation or delegation bootstrap item before full uniqueness and map
// decoding. False guarantees that neither selected normalizer can rewrite body;
// escaped JSON keys conservatively return true so full decoding decides.
func mayContainCodexBootstrapCandidate(body []byte, checkAutomation, checkDelegation bool) bool {
	if len(body) == 0 || (!checkAutomation && !checkDelegation) {
		return false
	}
	hasUnicodeEscape := bytes.Contains(body, codexBootstrapUnicodeEscapeNeedle)
	if !hasUnicodeEscape {
		if !bytes.Contains(body, codexBootstrapOutputTypeNeedle) {
			return false
		}
		hasApp := bytes.Contains(body, codexBootstrapAppNamespaceNeedle)
		hasTUI := checkDelegation && bytes.Contains(body, codexBootstrapTUINamespaceNeedle)
		if !hasApp && !hasTUI {
			return false
		}
	}

	root := wirejson.ParseView(body)
	if !root.IsObject() {
		return false
	}
	foundCandidate := false
	root.ForEach(func(key, value gjson.Result) bool {
		// Escaped root keys can alias input or previous_response_id; let the full
		// uniqueness and map decoder evaluate those rare representations.
		if strings.Contains(key.Raw, `\u`) {
			foundCandidate = true
			return false
		}
		if key.Str != "input" || !value.IsArray() {
			return true
		}
		value.ForEach(func(_, item gjson.Result) bool {
			if !item.IsObject() {
				return true
			}
			var typ, namespace, name string
			var hasOutput, disallowedCallID, escapedKey bool
			item.ForEach(func(itemKey, itemVal gjson.Result) bool {
				if strings.Contains(itemKey.Raw, `\u`) {
					escapedKey = true
					return false
				}
				switch itemKey.Str {
				case "type":
					if itemVal.Type == gjson.String {
						typ = itemVal.Str
					}
				case "namespace":
					if itemVal.Type == gjson.String {
						namespace = itemVal.Str
					}
				case "name":
					if itemVal.Type == gjson.String {
						name = itemVal.Str
					}
				case "output":
					if itemVal.Type == gjson.String {
						hasOutput = true
					}
				case "call_id":
					if itemVal.Type != gjson.String || strings.TrimSpace(itemVal.Str) != "" {
						disallowedCallID = true
					}
				}
				return true
			})
			if escapedKey {
				foundCandidate = true
				return false
			}
			if typ != "function_call_output" || !hasOutput || disallowedCallID {
				return true
			}
			if checkAutomation && namespace == "codex_app" && name == "automation_update" {
				foundCandidate = true
				return false
			}
			if checkDelegation && IsCodexDelegationTool(namespace, name) {
				foundCandidate = true
				return false
			}
			return true
		})
		return !foundCandidate
	})
	return foundCandidate
}

// decodeCodexBootstrapRequest decodes body into a number-preserving top-level
// object map. Non-object roots or syntax failures return false so callers
// retain their original bytes.
func decodeCodexBootstrapRequest(body []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var request map[string]any
	// This optional repair needs an object before it can classify input items.
	// Keep the complete supplied representation when object decoding cannot prepare one.
	if err := decoder.Decode(&request); err != nil || request == nil {
		return nil, false
	}
	return request, true
}

// applyCodexCallOutputBootstrap validates context and rewrites qualifying
// missing-ID bootstrap items inside a decoded request object. All validation
// completes before input mutation, so a false result leaves request unchanged.
func applyCodexCallOutputBootstrap(request map[string]any, isCandidate func(map[string]any) bool, allowHistoricalContext bool) bool {
	if previousResponseID, exists := request["previous_response_id"]; exists {
		value, ok := previousResponseID.(string)
		if !ok || (!allowHistoricalContext && strings.TrimSpace(value) != "") {
			return false
		}
	}
	input, ok := request["input"].([]any)
	if !ok {
		return false
	}

	// Validate surrounding call identities before adopting any conversion.
	// Delegation can retain identified history, while automation stays in its
	// bootstrap-only context and declines other call or reference shapes.
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := StringField(item, "type")
		if isCandidate(item) {
			callIDValue, exists := item["call_id"]
			callID, isString := callIDValue.(string)
			if exists && (!isString || strings.TrimSpace(callID) != "") {
				return false
			}
			continue
		}
		if typ == "item_reference" {
			if allowHistoricalContext && strings.TrimSpace(StringField(item, "id")) != "" {
				continue
			}
			return false
		}
		if strings.HasSuffix(typ, "_call") || IsResponsesCallOutputType(typ) {
			if allowHistoricalContext && strings.TrimSpace(StringField(item, "call_id")) != "" {
				continue
			}
			return false
		}
	}

	changed := false
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || !isCandidate(item) {
			continue
		}
		output, ok := item["output"].(string)
		if !ok {
			continue
		}
		// The candidate lacks a call ID, so its tool-output wrapper cannot name a
		// prior invocation. Give its text a user-message wrapper at the same position.
		input[i] = map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": output,
			}},
		}
		changed = true
	}
	return changed
}

// HasUniqueJSONMembers reports whether body contains one complete JSON value
// with unique members in every object. Scalar and array roots are allowed by
// this format check, while object decoding is performed separately by its
// caller.
//
// Recursive token reading tracks each object's decoded member names, so
// escaped-equivalent names also collide before a map rewrite can choose one.
// After consuming the value, EOF is required to reject another value or a
// malformed suffix. The decoder uses its default numeric token conversion here,
// so a number it cannot read also declines validation. False leaves callers
// responsible for retaining or rejecting their original request.
func HasUniqueJSONMembers(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !ConsumeUniqueJSONValue(decoder) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

// ConsumeUniqueJSONValue reads one JSON value from a nonnil decoder and reports
// complete structure with no duplicate object members. The decoder's numeric
// configuration controls token reading, and failure can leave it partially
// consumed.
//
// Each object owns a set of decoded keys and recursively validates its values
// before accepting the closing delimiter. Arrays recursively validate each
// entry; scalars succeed after their token is read. This helper does not check
// trailing EOF, so callers such as HasUniqueJSONMembers perform that additional
// check before treating the complete body as a safe candidate for object
// rewriting.
func ConsumeUniqueJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}

	switch delim {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, duplicate := members[key]; duplicate {
				return false
			}
			members[key] = struct{}{}
			if !ConsumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !ConsumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

// IsResponsesCallOutputType recognizes the _call_output suffix and the
// tool_search_output spelling. It classifies item shape for bootstrap
// validation, while the caller separately checks call identifiers and
// historical-context eligibility.
func IsResponsesCallOutputType(typ string) bool {
	return strings.HasSuffix(typ, "_call_output") || typ == "tool_search_output"
}

// IsCodexDelegationCandidate recognizes exact function_call_output tool
// metadata with a valid delegation XML envelope. Call-ID and
// surrounding-history checks belong to the normalizer, so a true result alone
// does not authorize conversion.
func IsCodexDelegationCandidate(item map[string]any) bool {
	if StringField(item, "type") != "function_call_output" ||
		!IsCodexDelegationTool(StringField(item, "namespace"), StringField(item, "name")) {
		return false
	}
	output, ok := item["output"].(string)
	return ok && ValidCodexDelegationEnvelope(output)
}

// IsCodexAutomationCandidate recognizes exact codex_app automation_update
// output carrying supported bootstrap text or heartbeat XML. Call-ID and
// surrounding-context checks remain with the normalizer before this formatted
// output becomes user input.
func IsCodexAutomationCandidate(item map[string]any) bool {
	if StringField(item, "type") != "function_call_output" ||
		StringField(item, "namespace") != "codex_app" ||
		StringField(item, "name") != "automation_update" {
		return false
	}
	output, ok := item["output"].(string)
	return ok && (ValidCodexAutomationBootstrap(output) || ValidCodexAutomationHeartbeat(output))
}

// StringField returns a string member or empty for missing and nonstring
// values. The candidate predicates use this exact spelling, so the helper
// leaves whitespace and case normalization to their individual rules.
func StringField(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return value
}

// IsCodexDelegationTool recognizes create_thread or send_message_to_thread
// under codex_app or codex_tui. Names and namespaces use exact spelling, while
// output envelope validation determines whether a recognized tool item contains
// delegation bootstrap text.
func IsCodexDelegationTool(namespace, name string) bool {
	return (namespace == "codex_app" || namespace == "codex_tui") &&
		(name == "create_thread" || name == "send_message_to_thread")
}

// ValidCodexAutomationBootstrap recognizes the fixed automation header block
// followed by a nonblank prompt. CRLF is normalized for parsing, isolated
// carriage returns are rejected, and the memory path must contain the same
// validated automation ID.
//
// The last-run field accepts never or an RFC3339Nano timestamp paired with its
// matching epoch milliseconds. A blank line separates that metadata from the
// prompt, and header values require unpadded nonempty text. Format recognition
// reads supplied text alone; the memory-path spelling is checked without
// accessing that path or establishing who created the bootstrap.
func ValidCodexAutomationBootstrap(value string) bool {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	if strings.ContainsRune(normalized, '\r') {
		return false
	}
	lines := strings.Split(normalized, "\n")
	// Headers need a blank separator and at least one following prompt line.
	if len(lines) < 6 {
		return false
	}
	if _, ok := CodexAutomationHeaderValue(lines[0], "Automation: "); !ok {
		return false
	}
	automationID, ok := CodexAutomationHeaderValue(lines[1], "Automation ID: ")
	if !ok || !ValidCodexAutomationID(automationID) {
		return false
	}
	expectedMemory := "Automation memory: $CODEX_HOME/automations/" + automationID + "/memory.md"
	if lines[2] != expectedMemory {
		return false
	}
	lastRun, ok := CodexAutomationHeaderValue(lines[3], "Last run: ")
	if !ok || !ValidCodexAutomationLastRun(lastRun) || lines[4] != "" {
		return false
	}
	return strings.TrimSpace(strings.Join(lines[5:], "\n")) != ""
}

// CodexAutomationHeaderValue reads an exact prefix followed by nonempty
// unpadded text. A wrong prefix or surrounding whitespace reports false,
// allowing the bootstrap parser to reject ambiguous header framing before
// inspecting prompt content.
func CodexAutomationHeaderValue(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	value := strings.TrimPrefix(line, prefix)
	return value, value != "" && strings.TrimSpace(value) == value
}

// ValidCodexAutomationID recognizes a nonempty ASCII automation identifier of
// at most 128 bytes. Letters, digits, hyphen, underscore and dot are accepted,
// while dot/dot-dot alone and other bytes fail the relative memory-path
// identity check.
func ValidCodexAutomationID(value string) bool {
	// Bound the identifier used inside the relative automation memory path.
	if value == "" || len(value) > 128 || value == "." || value == ".." {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

// ValidCodexAutomationLastRun checks whether the header names one last-run
// instant. The literal never is accepted; other input needs an RFC3339Nano
// timestamp followed by a space and parenthesized signed decimal milliseconds.
//
// The timestamp is reduced with UnixMilli before comparison, so submillisecond
// digits can differ while representing the same accepted millisecond. A
// malformed timestamp, integer, or suffix returns false. This agreement checks
// the two representations supplied in the header; it does not establish that
// the run occurred or how recent it was.
func ValidCodexAutomationLastRun(value string) bool {
	if value == "never" {
		return true
	}
	separator := strings.LastIndex(value, " (")
	if separator <= 0 || !strings.HasSuffix(value, ")") {
		return false
	}
	// The timestamp and parenthesized integer describe the same last-run moment.
	// Compare milliseconds so fractional timestamp precision follows the bootstrap header format.
	runAt, err := time.Parse(time.RFC3339Nano, value[:separator])
	if err != nil {
		return false
	}
	epochMillis, err := strconv.ParseInt(value[separator+2:len(value)-1], 10, 64)
	return err == nil && runAt.UnixMilli() == epochMillis
}

// ValidCodexAutomationHeartbeat recognizes one unadorned heartbeat root
// containing one automation_id with a valid unpadded identifier. Whitespace
// outside the identifier is tolerated, while extra/nested elements, attributes,
// namespaces and comments or directives fail.
//
// The parser tracks root presence, child presence and depth until EOF so a
// fragment or additional structure cannot be treated as a complete heartbeat.
// Identifier character data is joined across decoder tokens and checked after
// the tree closes. This exact shape supports bootstrap classification without
// promoting arbitrary XML tool output to user input or proving its origin.
func ValidCodexAutomationHeartbeat(value string) bool {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var rootSeen, automationIDSeen bool
	var automationID bytes.Buffer
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			id := automationID.String()
			return rootSeen && automationIDSeen && depth == 0 &&
				strings.TrimSpace(id) == id && ValidCodexAutomationID(id)
		}
		if err != nil {
			return false
		}
		switch current := token.(type) {
		case xml.StartElement:
			depth++
			if current.Name.Space != "" || len(current.Attr) != 0 || depth > 2 {
				return false
			}
			if depth == 1 {
				if rootSeen || current.Name.Local != "heartbeat" {
					return false
				}
				rootSeen = true
			} else if automationIDSeen || current.Name.Local != "automation_id" {
				return false
			}
			automationIDSeen = depth == 2
		case xml.EndElement:
			if current.Name.Space != "" {
				return false
			}
			depth--
			if depth < 0 {
				return false
			}
		case xml.CharData:
			if depth == 2 {
				_, _ = automationID.Write(current)
			} else if len(bytes.TrimSpace(current)) != 0 {
				return false
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			return false
		}
	}
}

// ValidCodexDelegationEnvelope recognizes one codex_delegation root containing
// exactly one nonblank source_thread_id and one nonblank input. Either child
// order is accepted, while attributes, namespaces, nested elements, comments,
// directives and extra outside text fail.
//
// The parser accumulates character data until each child closes, then records
// that child once. Duplicate children or an incomplete root fail before EOF can
// establish a complete envelope. This preserves a narrow delegation-text shape
// for candidate classification; it validates supplied structure and does not
// verify thread existence, authorization, or the authenticity of its source.
func ValidCodexDelegationEnvelope(value string) bool {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var rootSeen, sourceSeen, inputSeen bool
	var childName string
	var childText bytes.Buffer
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return rootSeen && depth == 0 && sourceSeen && inputSeen
		}
		if err != nil {
			return false
		}
		switch current := token.(type) {
		case xml.StartElement:
			depth++
			if current.Name.Space != "" || len(current.Attr) != 0 || (depth == 1 && current.Name.Local != "codex_delegation") || depth > 2 {
				return false
			}
			if depth == 1 {
				if rootSeen {
					return false
				}
				rootSeen = true
				continue
			}
			if current.Name.Local != "source_thread_id" && current.Name.Local != "input" {
				return false
			}
			childName = current.Name.Local
			childText.Reset()
		case xml.EndElement:
			if current.Name.Space != "" {
				return false
			}
			if depth == 2 {
				if current.Name.Local != childName || strings.TrimSpace(childText.String()) == "" {
					return false
				}
				if childName == "source_thread_id" {
					if sourceSeen {
						return false
					}
					sourceSeen = true
				} else {
					if inputSeen {
						return false
					}
					inputSeen = true
				}
				childName = ""
			}
			depth--
			if depth < 0 {
				return false
			}
		case xml.CharData:
			if depth == 2 {
				_, _ = childText.Write(current)
			} else if len(bytes.TrimSpace(current)) != 0 {
				return false
			}
		case xml.Comment:
			return false
		case xml.ProcInst, xml.Directive:
			return false
		}
	}
}
