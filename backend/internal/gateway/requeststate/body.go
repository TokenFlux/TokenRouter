package requeststate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// body.go parses gateway request fields and maintains their raw byte ranges
// across body replacement.
// Gateway request parsing keeps forwarding bytes alongside scalar fields and
// raw ranges into the same body. Body replacement can change field offsets, so
// parsed state refreshes those ranges before system, messages, or Responses
// input is read again.
//
// RequestBodyRef retains caller storage, and raw accessors return slices of
// that storage. CloneForBody creates another body reference but retains request
// identity pointers; callers keep shared data stable and replace bytes through
// ParsedRequest when derived fields are needed. Anthropic parsing removes
// trailing Claude Code context selectors before recording ranges. Session
// hashing consumes captured source values and normalizes User-Agent products
// separately, preventing version spelling from becoming the whole affinity
// identity.

// claudeCodeLongContextModelSuffix identifies the trailing client context
// selector removed during Anthropic model parsing before routing.
const claudeCodeLongContextModelSuffix = "[1m]"

// These patterns extract User-Agent product names and remove standalone version
// strings so session affinity can survive client version updates.
var (
	sessionUserAgentProductPattern = regexp.MustCompile(`([A-Za-z0-9._-]+)/[A-Za-z0-9._-]+`)
	sessionUserAgentVersionPattern = regexp.MustCompile(`\bv?\d+(?:\.\d+){1,3}\b`)
)

// SessionContext carries request-source values mixed into content-derived
// session affinity. Callers keep this object unchanged while parsed requests or
// attempt clones retain its pointer.
type SessionContext struct {
	// ClientIP, UserAgent, and APIKeyID come from the request entry. Content-based
	// session hashing mixes these source factors into affinity evidence so
	// identical messages from different callers can use separate sessions.
	ClientIP  string // request source IP spelling
	UserAgent string // request User-Agent header value
	APIKeyID  int64  // authenticated API-key identifier
}

// jsonRange identifies one JSON value inside the current request body. Missing
// ranges use negative offsets; raw accessors return the captured byte interval
// after checking its end against the current slice.
type jsonRange struct {
	// start and end describe the current body's half-open byte interval; kind
	// records its parsed value type. Replacement requires new offsets before
	// these ranges are used against the updated storage.
	start int        // inclusive body byte offset
	end   int        // exclusive body byte offset
	kind  gjson.Type // captured gjson value type
}

// RequestBodyRef retains forwarding bytes shared with its request owner.
// Callers keep the supplied backing storage stable and use
// ParsedRequest.ReplaceBody when parsed fields or ranges also need refreshing.
type RequestBodyRef struct {
	// data retains the caller's backing storage. Replace changes this reference;
	// parsed range and scalar refresh belongs to ParsedRequest.ReplaceBody.
	data []byte // retained forwarding body bytes
}

// ParsedRequest holds scalar request fields and raw views used by forwarding
// and session selection. Its Body and identity pointers can be shared with an
// attempt clone; callers serialize replacement and leave retained input storage
// unchanged.
type ParsedRequest struct {
	// Body supplies bytes for every raw range. Use ReplaceBody when replacing a
	// parsed request so ranges and derived fields are refreshed together.
	Body            *RequestBodyRef // current forwarding body reference
	Model           string          // model captured from current body
	Stream          bool            // requested streaming response mode
	MetadataUserID  string          // metadata user identifier for affinity
	HasSystem       bool            // system presence including JSON null
	ThinkingEnabled bool            // enabled or adaptive thinking flag
	OutputEffort    string          // trimmed Claude output-config effort
	MaxTokens       int             // float64-derived max_tokens probe value
	// SessionContext is retained by CloneForBody as the same pointer. Callers
	// leave its fields unchanged while either parsed request uses them.
	SessionContext *SessionContext // request-source session identity factors

	// protocol chooses field paths when refreshing systemRange, messagesRange,
	// and inputRange. Each range refers to Body's current bytes and is cleared
	// when a failed replacement makes those offsets unusable.
	protocol      string    // protocol selected for range refresh
	parsedData    []byte    // last successfully parsed body slice
	parsedValid   bool      // scalars and ranges match parsedData
	systemRange   jsonRange // system or Gemini parts range
	messagesRange jsonRange // messages or Gemini contents range
	inputRange    jsonRange // Responses input value range

	// GroupID is supplied by the authenticated entry and retained by CloneForBody.
	// Callers needing independent identity storage replace it with their own
	// copied pointer.
	GroupID *int64 // selected request group identifier
	// OnUpstreamAccepted releases the entry's serial queue when execution declares
	// acceptance. Native streaming paths call after successful headers; emulated
	// search calls before searching. CloneForBody clears it for another attempt.
	OnUpstreamAccepted func() // optional serial-queue release callback
}

// scannedGatewayRequestFields holds first-occurrence top-level members and byte
// ranges collected in one traversal of a validated JSON object.
type scannedGatewayRequestFields struct {
	hasModel       bool         // model member encountered
	model          gjson.Result // first model value
	hasStream      bool         // stream member encountered
	stream         gjson.Result // first stream value
	metadataUserID string       // first metadata.user_id string
	thinkingType   string       // first thinking.type string
	outputEffort   string       // trimmed first output_config.effort
	hasMaxTokens   bool         // max_tokens member encountered
	maxTokens      gjson.Result // first max_tokens value
	hasSystem      bool         // system member presence for non-Gemini
	systemRange    jsonRange    // system or systemInstruction.parts range
	messagesRange  jsonRange    // messages or contents array range
	inputRange     jsonRange    // Responses input range
}

// NewRequestBodyRef retains data as a forwarding body reference. It copies no
// bytes, so callers keep the backing storage unchanged while consumers use it
// and supply replacement storage when content changes.
func NewRequestBodyRef(data []byte) *RequestBodyRef {
	return &RequestBodyRef{data: data}
}

// Bytes returns the currently retained body slice, or nil for a nil reference.
// The slice shares backing storage with its owner, so callers treat it as
// read-only while forwarding or raw views use it.
func (b *RequestBodyRef) Bytes() []byte {
	if b == nil {
		return nil
	}
	return b.data
}

// Len returns the byte length of the retained forwarding body. A nil reference
// reports zero, allowing callers to handle missing and empty bodies through the
// same length check.
func (b *RequestBodyRef) Len() int {
	if b == nil {
		return 0
	}
	return len(b.data)
}

// Replace installs data as the retained body slice and ignores a nil reference.
// It retains caller storage and refreshes no ParsedRequest fields; consumers
// needing derived data use ParsedRequest.ReplaceBody instead.
func (b *RequestBodyRef) Replace(data []byte) {
	if b == nil {
		return
	}
	b.data = data
}

// missingJSONRange returns negative offsets identifying an absent request
// value.
func missingJSONRange() jsonRange {
	return jsonRange{start: -1, end: -1}
}

// rangeFromBorrowedResult records the byte interval of r.Raw inside jsonStr.
// When r.Raw does not point within jsonStr's backing bytes, it falls back to
// r.Index; out-of-bounds or empty results produce a missing range.
func rangeFromBorrowedResult(jsonStr string, r gjson.Result) jsonRange {
	if r.Raw == "" || jsonStr == "" {
		return missingJSONRange()
	}
	basePtr := uintptr(unsafe.Pointer(unsafe.StringData(jsonStr)))
	rawPtr := uintptr(unsafe.Pointer(unsafe.StringData(r.Raw)))
	if rawPtr >= basePtr {
		offset := rawPtr - basePtr
		if offset <= uintptr(len(jsonStr)) && len(r.Raw) <= len(jsonStr)-int(offset) {
			start := int(offset)
			return jsonRange{start: start, end: start + len(r.Raw), kind: r.Type}
		}
	}
	return rangeFromResult(r)
}

// rangeFromResult records the raw value's positive start offset, length, and
// gjson type. Empty raw values, nonpositive indexes, or overflowing end offsets
// produce a missing range that raw accessors decline to read.
func rangeFromResult(r gjson.Result) jsonRange {
	if r.Raw == "" || r.Index <= 0 {
		return missingJSONRange()
	}
	end := r.Index + len(r.Raw)
	if end < r.Index {
		// An overflowed end cannot identify a safe slice of the current request.
		// Mark it absent so raw readers decline this malformed parser result.
		return missingJSONRange()
	}
	return jsonRange{start: r.Index, end: end, kind: r.Type}
}

// exists reports whether the range has ordered nonnegative byte offsets.
func (r jsonRange) exists() bool {
	return r.start >= 0 && r.end >= r.start
}

// sameNonEmptyByteSlice reports whether a and b share non-empty backing bytes
// with identical length. Callers keep parsed body storage immutable in place,
// so matching slice headers establish identical JSON bytes.
func sameNonEmptyByteSlice(a, b []byte) bool {
	return len(a) > 0 && len(a) == len(b) && unsafe.SliceData(a) == unsafe.SliceData(b)
}

// clearGatewayRequestDerivedState resets body-derived scalars and raw ranges
// before valid-body parsing proceeds. Nil requests are ignored; the body
// reference, protocol and caller-supplied identity or acceptance state are
// retained.
func clearGatewayRequestDerivedState(parsed *ParsedRequest) {
	if parsed == nil {
		return
	}
	parsed.parsedData = nil
	parsed.parsedValid = false
	parsed.Model = ""
	parsed.Stream = false
	parsed.MetadataUserID = ""
	parsed.HasSystem = false
	parsed.ThinkingEnabled = false
	parsed.OutputEffort = ""
	parsed.MaxTokens = 0
	parsed.systemRange = missingJSONRange()
	parsed.messagesRange = missingJSONRange()
	parsed.inputRange = missingJSONRange()
}

// clearGatewayRequestRanges clears raw ranges and system presence after
// replacement parsing fails. Other scalar values remain as parsing left them,
// and a nil request is ignored for caller cleanup.
func clearGatewayRequestRanges(parsed *ParsedRequest) {
	if parsed == nil {
		return
	}
	parsed.parsedData = nil
	parsed.parsedValid = false
	parsed.HasSystem = false
	parsed.systemRange = missingJSONRange()
	parsed.messagesRange = missingJSONRange()
	parsed.inputRange = missingJSONRange()
}

// normalizeClaudeCodeLongContextModel removes repeated trailing Claude Code
// context selectors case-insensitively. A name consisting solely of the
// selector stays unchanged, and selector text in the middle of a name remains
// part of that model.
func normalizeClaudeCodeLongContextModel(model string) string {
	// A selector alone remains a model spelling, so repeated stripping stops
	// before an attempt would forward an empty model name to routing.
	for len(model) > len(claudeCodeLongContextModelSuffix) &&
		strings.EqualFold(model[len(model)-len(claudeCodeLongContextModelSuffix):], claudeCodeLongContextModelSuffix) {
		model = model[:len(model)-len(claudeCodeLongContextModelSuffix)]
	}
	return model
}

// parseGatewayRequestCurrentBody validates current body bytes, extracts
// scalars, and records raw ranges for the selected protocol. Callers provide a
// body reference and serialize access to its retained storage during parsing
// and subsequent raw reads.
//
// Invalid JSON fails before scalar reset. Once JSON is valid, earlier derived
// values are cleared, then model and stream type checks can return with
// partially refreshed scalars. One top-level object traversal collects first-
// occurrence fields and ranges so large message arrays are skipped once rather
// than on every field lookup. Anthropic model normalization replaces bytes and
// refreshes ranges for the rewritten storage; max_tokens uses finite integral
// float64 values compared against converted int bounds. A successful parse does
// not require a top-level object, and caller-owned identity fields remain
// attached to this request.
func parseGatewayRequestCurrentBody(parsed *ParsedRequest, protocol string) error {
	if parsed == nil || parsed.Body == nil {
		return fmt.Errorf("empty request body")
	}

	bodyBytes := parsed.Body.Bytes()
	if !gjson.ValidBytes(bodyBytes) {
		return DescribeInvalidJSON(bodyBytes)
	}

	clearGatewayRequestDerivedState(parsed)
	parsed.protocol = protocol

	// gjson offsets refer to this retained byte storage. Keep it unchanged during
	// parsing and rebind after an Anthropic model selector rewrite replaces bytes.
	jsonStr := unsafe.String(unsafe.SliceData(bodyBytes), len(bodyBytes))
	fields, isObject := scanGatewayRequestTopLevelFields(jsonStr, protocol)
	if !isObject {
		parsed.parsedData = bodyBytes
		parsed.parsedValid = true
		return nil
	}

	if fields.hasModel {
		if fields.model.Type != gjson.String {
			return fmt.Errorf("invalid model field type")
		}
		parsed.Model = fields.model.String()
		if protocol == capability.PlatformAnthropic {
			normalizedModel := normalizeClaudeCodeLongContextModel(parsed.Model)
			if normalizedModel != parsed.Model {
				normalizedBody, err := sjson.SetBytes(bodyBytes, "model", normalizedModel)
				if err != nil {
					return fmt.Errorf("normalize model field: %w", err)
				}
				// Routing uses the base Anthropic model, while raw range offsets must refer
				// to the rewritten bytes rather than positions in the client's selector spelling.
				parsed.Body.Replace(normalizedBody)
				bodyBytes = normalizedBody
				jsonStr = unsafe.String(unsafe.SliceData(bodyBytes), len(bodyBytes))
				fields, _ = scanGatewayRequestTopLevelFields(jsonStr, protocol)
				parsed.Model = normalizedModel
			}
		}
	}

	if fields.hasStream {
		if fields.stream.Type != gjson.True && fields.stream.Type != gjson.False {
			return fmt.Errorf("invalid stream field type")
		}
		parsed.Stream = fields.stream.Bool()
	}

	parsed.MetadataUserID = fields.metadataUserID
	parsed.ThinkingEnabled = fields.thinkingType == "enabled" || fields.thinkingType == "adaptive"
	parsed.OutputEffort = fields.outputEffort

	if fields.hasMaxTokens && fields.maxTokens.Type == gjson.Number {
		// Probe identification uses an integer extracted through gjson's float64
		// path, so large source integers can already be rounded before conversion.
		f := fields.maxTokens.Float()
		if !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) &&
			f <= float64(math.MaxInt) && f >= float64(math.MinInt) {
			parsed.MaxTokens = int(f)
		}
	}

	parsed.HasSystem = fields.hasSystem
	parsed.systemRange = fields.systemRange
	parsed.messagesRange = fields.messagesRange
	parsed.inputRange = fields.inputRange
	parsed.parsedData = bodyBytes
	parsed.parsedValid = true
	return nil
}

// scanGatewayRequestTopLevelFields visits top-level members once, keeping the
// first occurrence of each recognized scalar and protocol range. Non-object
// roots return false so callers retain zero-valued fields without error.
func scanGatewayRequestTopLevelFields(jsonStr, protocol string) (scannedGatewayRequestFields, bool) {
	out := scannedGatewayRequestFields{
		systemRange:   missingJSONRange(),
		messagesRange: missingJSONRange(),
		inputRange:    missingJSONRange(),
	}
	root := gjson.Parse(jsonStr)
	if !root.IsObject() {
		return out, false
	}

	const (
		seenModel uint16 = 1 << iota
		seenStream
		seenMetadata
		seenThinking
		seenOutputConfig
		seenMaxTokens
		seenSystem
		seenMessages
		seenInput
	)
	wantMask := seenModel | seenStream | seenMetadata | seenThinking | seenOutputConfig | seenMaxTokens | seenSystem | seenMessages
	if protocol == "responses" {
		wantMask |= seenInput
	}
	var seen uint16

	root.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "model":
			if seen&seenModel == 0 {
				out.hasModel = true
				out.model = value
				seen |= seenModel
			}
		case "stream":
			if seen&seenStream == 0 {
				out.hasStream = true
				out.stream = value
				seen |= seenStream
			}
		case "metadata":
			if seen&seenMetadata == 0 {
				out.metadataUserID = value.Get("user_id").String()
				seen |= seenMetadata
			}
		case "thinking":
			if seen&seenThinking == 0 {
				out.thinkingType = value.Get("type").String()
				seen |= seenThinking
			}
		case "output_config":
			if seen&seenOutputConfig == 0 {
				out.outputEffort = strings.TrimSpace(value.Get("effort").String())
				seen |= seenOutputConfig
			}
		case "max_tokens":
			if seen&seenMaxTokens == 0 {
				out.hasMaxTokens = true
				out.maxTokens = value
				seen |= seenMaxTokens
			}
		case "systemInstruction":
			if protocol == capability.PlatformGemini && seen&seenSystem == 0 {
				if sysParts := value.Get("parts"); sysParts.Exists() && sysParts.IsArray() {
					out.systemRange = rangeFromBorrowedResult(jsonStr, sysParts)
				}
				seen |= seenSystem
			}
		case "contents":
			if protocol == capability.PlatformGemini && seen&seenMessages == 0 {
				if value.IsArray() {
					out.messagesRange = rangeFromBorrowedResult(jsonStr, value)
				}
				seen |= seenMessages
			}
		case "system":
			if protocol != capability.PlatformGemini && seen&seenSystem == 0 {
				out.hasSystem = true
				out.systemRange = rangeFromBorrowedResult(jsonStr, value)
				seen |= seenSystem
			}
		case "messages":
			if protocol != capability.PlatformGemini && seen&seenMessages == 0 {
				if value.IsArray() {
					out.messagesRange = rangeFromBorrowedResult(jsonStr, value)
				}
				seen |= seenMessages
			}
		case "input":
			if protocol == "responses" && seen&seenInput == 0 {
				out.inputRange = rangeFromBorrowedResult(jsonStr, value)
				seen |= seenInput
			}
		}
		return seen != wantMask
	})
	return out, true
}

// refreshGatewayRequestRanges reparses scalars and ranges from the current body
// using protocol. It propagates parsing errors with their partial-state
// behavior; callers decide whether to clear raw views or discard the request.
func refreshGatewayRequestRanges(parsed *ParsedRequest, protocol string) error {
	return parseGatewayRequestCurrentBody(parsed, protocol)
}

// DescribeInvalidJSON returns diagnostic JSON failure information containing
// body length and available syntax offset. It uses encoding/json after
// rejection by the primary parser; an accepted diagnostic parse still returns a
// length-only failure and request contents are not embedded.
func DescribeInvalidJSON(body []byte) error {
	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		if syntaxErr, ok := errors.AsType[*json.SyntaxError](err); ok {
			return fmt.Errorf("invalid json (len=%d, offset=%d): %s", len(body), syntaxErr.Offset, syntaxErr.Error())
		}
		return fmt.Errorf("invalid json (len=%d): %w", len(body), err)
	}
	// The diagnostic decoder can accept an input rejected by the primary parser.
	// Keep the original rejection visible with length alone when it supplies no syntax error.
	return fmt.Errorf("invalid json (len=%d)", len(body))
}

// NormalizeSessionUserAgent returns a stable affinity label derived from a
// User-Agent. Blank input returns empty, and product/version tokens produce
// sorted, lowercased, unique product names joined by plus signs.
//
// Versions and incidental surrounding text are discarded when recognized
// products exist, so a client update can keep the same content-session seed.
// With no recognized products it uses the normalized text after standalone
// version removal. The resulting label groups those spellings for session
// hashing; it is request-source evidence and does not authenticate the client
// or distinguish every original header.
func NormalizeSessionUserAgent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	matches := sessionUserAgentProductPattern.FindAllStringSubmatch(raw, -1)
	// Headers lacking Product/Version tokens still need usable affinity evidence.
	// Reduce their whitespace and standalone versions through the textual path.
	if len(matches) == 0 {
		return normalizeSessionUserAgentFallback(raw)
	}

	products := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		product := strings.ToLower(strings.TrimSpace(match[1]))
		if product == "" {
			continue
		}
		if _, exists := seen[product]; exists {
			continue
		}
		seen[product] = struct{}{}
		products = append(products, product)
	}
	// If every captured product was unusable, textual normalization supplies the
	// affinity evidence that an empty product join would otherwise discard.
	if len(products) == 0 {
		return normalizeSessionUserAgentFallback(raw)
	}
	sort.Strings(products)
	return strings.Join(products, "+")
}

// normalizeSessionUserAgentFallback lowercases and collapses whitespace before
// removing standalone version spellings. It collapses whitespace again
// afterward, returning the remaining text for session hashing when
// product/version token extraction found no usable products.
func normalizeSessionUserAgentFallback(raw string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	normalized = sessionUserAgentVersionPattern.ReplaceAllString(normalized, "")
	return strings.Join(strings.Fields(normalized), " ")
}

// ParseGatewayRequest returns parsed scalar fields and raw ranges around the
// supplied shared body reference after rejecting non-object, duplicate-key, or
// case-ambiguous JSON inputs. The selected protocol controls Gemini, Messages,
// or Responses field paths; Anthropic also normalizes trailing context
// selectors in that reference. Errors return no parsed object, while a
// successful caller retains responsibility for stable body storage and later
// replacement through ParsedRequest.
func ParseGatewayRequest(body *RequestBodyRef, protocol string) (*ParsedRequest, error) {
	if body == nil {
		return nil, fmt.Errorf("empty request body")
	}
	raw := body.Bytes()
	if !gjson.ValidBytes(raw) {
		return nil, DescribeInvalidJSON(raw)
	}
	if err := wirejson.ValidateRequestObject(raw); err != nil {
		return nil, err
	}
	parsed := &ParsedRequest{Body: body}
	if err := parseGatewayRequestCurrentBody(parsed, protocol); err != nil {
		return nil, err
	}
	return parsed, nil
}

// raw returns a shared slice for a present range that fits the current body.
// Missing requests, body references or ranges return nil, as does an end beyond
// the current byte length; callers keep the storage unchanged while reading.
func (p *ParsedRequest) raw(r jsonRange) []byte {
	if p == nil || p.Body == nil || !r.exists() {
		return nil
	}
	body := p.Body.Bytes()
	if r.end > len(body) {
		return nil
	}
	return body[r.start:r.end]
}

// SystemRaw returns the shared system value or Gemini systemInstruction.parts
// byte range. Missing or invalidated ranges return nil; callers keep the
// current body storage unchanged while using the returned slice.
func (p *ParsedRequest) SystemRaw() []byte {
	return p.raw(p.systemRange)
}

// MessagesRaw returns the shared messages or Gemini contents array byte range.
// Missing or invalidated ranges return nil, and callers keep the current body
// storage unchanged while that slice remains in use.
func (p *ParsedRequest) MessagesRaw() []byte {
	return p.raw(p.messagesRange)
}

// InputRaw returns the shared Responses input value byte range. Other protocols
// or missing and invalidated ranges return nil; callers keep the backing body
// bytes unchanged while using a returned view.
func (p *ParsedRequest) InputRaw() []byte {
	return p.raw(p.inputRange)
}

// SystemValue decodes the captured system range with encoding/json and reports
// success, including for JSON null. Missing or undecodable views return false;
// decoded interface-held numbers follow float64 decoding and can lose original
// integer precision.
func (p *ParsedRequest) SystemValue() (any, bool) {
	raw := p.SystemRaw()
	if len(raw) == 0 {
		return nil, false
	}
	var system any
	if err := json.Unmarshal(raw, &system); err != nil {
		// A raw range can remain readable after direct body replacement even when
		// its bytes no longer encode a system value. Report unavailable decoded input.
		return nil, false
	}
	return system, true
}

// CloneForBody copies parsed state around the supplied body bytes for another
// attempt, reparsing only when body differs from the already validated slice.
// A nil receiver or parsing failure returns an error, and the new body
// reference retains caller storage.
//
// The copy retains SessionContext and GroupID pointers, while scalar and range
// fields match the supplied bytes. OnUpstreamAccepted is cleared so the earlier
// attempt's queue release callback cannot be carried into this one. Callers
// keep retained identity objects and byte storage unchanged or supply separate
// copies before mutation; the clone creates independent parsed fields, not an
// independent copy of every referenced value.
func (p *ParsedRequest) CloneForBody(body []byte) (*ParsedRequest, error) {
	if p == nil {
		return nil, fmt.Errorf("parse request: empty request")
	}
	clone := *p
	clone.Body = NewRequestBodyRef(body)
	// Queue acceptance belongs to the preceding attempt. Clear that callback
	// before reparsing so another attempt installs its own resource-release decision.
	clone.OnUpstreamAccepted = nil
	if p.parsedValid && p.Body != nil && sameNonEmptyByteSlice(p.parsedData, body) && sameNonEmptyByteSlice(p.Body.Bytes(), body) {
		return &clone, nil
	}
	if err := refreshGatewayRequestRanges(&clone, clone.protocol); err != nil {
		return nil, err
	}
	return &clone, nil
}

// ReplaceBody installs supplied bytes and reparses this request's scalars and
// ranges in place when the slice differs from the already parsed body. A nil
// receiver fails, and the installed body retains caller storage after either
// success or error.
//
// This operation does not roll back a failed replacement. Invalid JSON can
// retain earlier scalar values, while later type errors can leave partially
// refreshed values; either failure clears raw ranges and HasSystem before
// returning. Callers discard or handle failed parsed state instead of treating
// those scalars as a successful new parse. Successful replacement keeps
// caller-owned identity and acceptance fields attached.
func (p *ParsedRequest) ReplaceBody(data []byte) error {
	if p == nil {
		return fmt.Errorf("parse request: empty request")
	}
	if p.parsedValid && p.Body != nil && sameNonEmptyByteSlice(p.parsedData, data) && sameNonEmptyByteSlice(p.Body.Bytes(), data) {
		return nil
	}
	if p.Body == nil {
		p.Body = NewRequestBodyRef(data)
	} else {
		p.Body.Replace(data)
	}
	if err := refreshGatewayRequestRanges(p, p.protocol); err != nil {
		// Failed replacement leaves new body storage installed. Invalidate every raw
		// view so earlier offsets cannot expose unrelated bytes from that replacement.
		clearGatewayRequestRanges(p)
		return err
	}
	return nil
}

// FirstNonEmpty returns the first trimmed nonblank string from values. It
// returns empty when every value is blank or the list is empty, letting callers
// supply ordered identity or model evidence.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
