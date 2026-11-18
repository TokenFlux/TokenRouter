package openai

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// rejected_field_retry.go repairs explicitly rejected Responses fields and
// coordinates body novelty with a shared retry budget.
// Responses compatibility repair follows explicit upstream 400 evidence instead
// of removing optional request fields before they fail. Recognized rejection
// codes, indexed parameters, and message paths select narrow repairs;
// inconsistent parameter evidence leaves the body ineligible for retry.
//
// Each request state remembers body hashes while a separate budget can be
// shared across derived states. Allow checks novelty and consumes one of six
// tickets under ordered locks, preventing repeated bodies and separate
// compatibility paths from independently resetting the retry allowance. Field
// repairs retain the request outside their selected paths. Same-type status
// cleanup can repair several indexed items in one body, while null reasoning
// content and message content use different replacements. The caller owns
// sending the eligible replacement and applying health/retry policy.

// MaxResponsesRejectedFieldRetries caps admitted field repairs across states sharing
// one budget, preventing separate request-normalization paths from restarting their own retry allowance.
const MaxResponsesRejectedFieldRetries = 6

// Rejection patterns recognize supported upstream parameter paths and precise
// message evidence before the selector attempts compatibility repair of the original request.
var (
	openAIResponsesRejectedNamespaceParamPattern  = regexp.MustCompile(`(?i)^input\[(\d+)\]\.namespace$`)
	openAIResponsesRejectedStatusParamPattern     = regexp.MustCompile(`(?i)^input\[(\d+)\]\.status$`)
	openAIResponsesRejectedContentParamPattern    = regexp.MustCompile(`(?i)^input\[(\d+)\]\.content$`)
	openAIResponsesRejectedCacheParamPattern      = regexp.MustCompile(`(?i)^input\[(\d+)\]\.prompt_cache_breakpoint$`)
	openAIResponsesRejectedMessageParamPattern    = regexp.MustCompile(`(?i)(?:unknown|unsupported)[ _-]+parameter\s*(?::|=|is)?\s*["']?(max_output_tokens|truncation|input\[\d+\]\.(?:namespace|status))(?:["']|\b)`)
	openAIResponsesInvalidTypeMessageParamPattern = regexp.MustCompile(`(?i)invalid[ _-]+type\s+for\s+["']?(input\[\d+\]\.content)(?:["']|\b)[^\n]*\b(?:got|received)\s+null\b`)
	openAIResponsesMaxZeroContentMessagePattern   = regexp.MustCompile(`(?i)invalid\s+["']?(input\[\d+\]\.content)["']?\s*:\s*array too long\.[^\n]*maximum length 0\b`)
	openAIResponsesCacheModelRejectionPattern     = regexp.MustCompile(`(?i)["']?(prompt_cache_breakpoint|input\[\d+\]\.prompt_cache_breakpoint)["']?\s+is\s+not\s+supported\s+on\s+this\s+model\b`)
	openAIResponsesToolParametersParamPattern     = regexp.MustCompile(`(?i)^(?:tools|input)\[\d+\](?:\.tools\[\d+\])*(?:\.function)?\.parameters$`)
	openAIResponsesMissingSchemaTypePattern       = regexp.MustCompile(`(?i)\bgot\s+["']?type\s*:\s*["']?none["']?`)
)

// ResponsesRejectedFieldRetryState records body hashes against an optionally
// shared field-repair budget.
type ResponsesRejectedFieldRetryState struct {
	// mu protects seenBodyHashes, and Allow acquires budget.mu afterward to
	// consume a shared ticket. budget can participate in several states whose
	// local body histories differ while their retry allowance remains combined.
	mu             sync.Mutex                         // retry state synchronization mutex
	budget         *ResponsesRejectedFieldRetryBudget // shared field-repair admission budget
	seenBodyHashes map[[sha256.Size]byte]struct{}     // remembered attempted body hashes
}

// ResponsesRejectedFieldRetryBudget counts admitted field-repair retries across
// participating request states.
type ResponsesRejectedFieldRetryBudget struct {
	// mu protects attempts across all states sharing this handle. Allow checks
	// the cap and increments attempts while holding that lock after its state lock.
	mu       sync.Mutex // retry state synchronization mutex
	attempts int        // admitted shared repair count
}

// NewOpenAIResponsesRejectedFieldRetryState creates a ready state with its own
// six-retry budget. A nonempty initial body is remembered before later Allow
// calls, preventing repair from repeating that already-attempted payload.
func NewOpenAIResponsesRejectedFieldRetryState(initialBody []byte) *ResponsesRejectedFieldRetryState {
	return NewOpenAIResponsesRejectedFieldRetryStateWithBudget(initialBody, &ResponsesRejectedFieldRetryBudget{})
}

// NewOpenAIResponsesRejectedFieldRetryStateWithBudget creates a state sharing
// the supplied budget and records a nonempty initial body. Nil budget retains
// hash tracking but disables Allow; callers reuse a budget across derived
// request states when retries share one allowance.
func NewOpenAIResponsesRejectedFieldRetryStateWithBudget(initialBody []byte, budget *ResponsesRejectedFieldRetryBudget) *ResponsesRejectedFieldRetryState {
	state := &ResponsesRejectedFieldRetryState{
		budget:         budget,
		seenBodyHashes: make(map[[sha256.Size]byte]struct{}, MaxResponsesRejectedFieldRetries+1),
	}
	state.Remember(initialBody)
	return state
}

// Allow records a novel nonempty body and consumes a shared retry ticket before
// returning true. Use a constructed state so its hash map is initialized, and
// keep nextBody immutable while hashing. Nil states, nil budgets, empty bodies,
// remembered hashes, and exhausted budgets return false.
//
// The state mutex precedes the budget mutex, keeping duplicate detection and
// shared allowance consumption in one admitted operation. A repeated body
// consumes no ticket, while two different states sharing a budget still count
// against the same total. Callers send only after true and retain the
// reservation even if that later transport attempt fails.
func (s *ResponsesRejectedFieldRetryState) Allow(nextBody []byte) bool {
	if s == nil || s.budget == nil || len(nextBody) == 0 {
		return false
	}
	// Acquire the state lock before the shared budget lock so hash novelty
	// and ticket consumption publish together, while other states retain the same lock ordering.
	s.mu.Lock()
	defer s.mu.Unlock()
	bodyHash := sha256.Sum256(nextBody)
	if _, seen := s.seenBodyHashes[bodyHash]; seen {
		return false
	}
	s.budget.mu.Lock()
	defer s.budget.mu.Unlock()
	if s.budget.attempts >= MaxResponsesRejectedFieldRetries {
		return false
	}
	s.seenBodyHashes[bodyHash] = struct{}{}
	s.budget.attempts++
	return true
}

// Remember records a nonempty body hash without consuming a retry ticket. Nil
// states and empty bodies are ignored; it initializes missing hash storage
// under the state mutex so later duplicate checking can reuse that observation.
func (s *ResponsesRejectedFieldRetryState) Remember(body []byte) {
	if s == nil || len(body) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rememberLocked(body)
}

// rememberLocked records body in the state hash map, initializing storage when
// absent. Callers hold s.mu and keep body immutable while hashing; this
// operation records history without changing the shared retry count.
func (s *ResponsesRejectedFieldRetryState) rememberLocked(body []byte) {
	if s.seenBodyHashes == nil {
		s.seenBodyHashes = make(map[[sha256.Size]byte]struct{}, MaxResponsesRejectedFieldRetries+1)
	}
	s.seenBodyHashes[sha256.Sum256(body)] = struct{}{}
}

// NormalizeOpenAIResponsesRejectedFieldRetryBody returns a repaired request,
// reason, and eligibility after explicit upstream 400 evidence. Other statuses,
// missing bodies, unsupported rejections, or already-compatible target fields
// return nil bytes with false. Callers check eligibility before using returned
// bytes as a retry request.
//
// Tool-schema, cache-breakpoint, namespace/status, null-content, and
// reasoning-length rejections use distinct evidence rules. A provided parameter
// and captured message path must agree where that rule requires both,
// preventing one item's failure from rewriting another path. Repairs apply to
// recognized field shapes and return errors on failed splicing; shared
// retry-state admission remains separate so the caller can enforce body novelty
// and the overall ticket budget.
func NormalizeOpenAIResponsesRejectedFieldRetryBody(statusCode int, body, responseBody []byte) ([]byte, string, bool, error) {
	if statusCode != http.StatusBadRequest || len(body) == 0 || len(responseBody) == 0 {
		return nil, "", false, nil
	}

	code := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorCode(responseBody)))
	message := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)))
	param := strings.ToLower(strings.TrimSpace(gjson.GetBytes(responseBody, "error.param").String()))
	if code == "invalid_function_parameters" &&
		openAIResponsesToolParametersParamPattern.MatchString(param) &&
		openAIResponsesMissingSchemaTypePattern.MatchString(message) {
		retryBody, changed, err := wire.SanitizeToolParameterTypes(body)
		if err != nil {
			return nil, "", false, fmt.Errorf("repair rejected tool parameter root type: %w", err)
		}
		if changed {
			return retryBody, "tool parameter root type rejection", true, nil
		}
	}
	cacheMessageParam := OpenAIResponsesCacheModelRejectionParamFromMessage(message)
	cacheParam := param
	if cacheParam == "" {
		cacheParam = cacheMessageParam
	}
	cacheParamMatchesMessage := cacheMessageParam == "" || cacheParam == cacheMessageParam
	cacheModelRejection := code == "invalid_parameter" || cacheMessageParam != ""
	if cacheParam != "" && cacheParamMatchesMessage && cacheModelRejection {
		if cacheParam == "prompt_cache_breakpoint" && gjson.GetBytes(body, cacheParam).Exists() &&
			!hasOpenAIExplicitCacheTTL(body, cacheParam) {
			retryBody, err := sjson.DeleteBytes(body, cacheParam)
			if err != nil {
				return nil, "", false, fmt.Errorf("delete rejected prompt_cache_breakpoint: %w", err)
			}
			return retryBody, "prompt_cache_breakpoint parameter rejection", true, nil
		}
		if index, ok := OpenAIResponsesRejectedCacheIndex(cacheParam); ok {
			return RemoveOpenAIResponsesRejectedCacheAtIndex(body, index)
		}
	}
	if IsExplicitOpenAIResponsesFieldRejection(code, message) {
		messageParam := OpenAIResponsesRejectedParamFromMessage(message)
		if param != "" && messageParam != "" && param != messageParam {
			return nil, "", false, nil
		}
		if param == "" {
			param = messageParam
		}
		if index, ok := OpenAIResponsesRejectedNamespaceIndex(param); ok {
			// Missing-identity diagnostics require retaining the call's namespace. An
			// inconsistent compatibility code cannot authorize removing the very field
			// that the upstream explicitly requires for dispatch.
			if strings.Contains(message, "missing namespace") {
				return nil, "", false, nil
			}
			return RemoveOpenAIResponsesRejectedNamespaceAtIndex(body, index)
		}
		if index, ok := OpenAIResponsesRejectedStatusIndex(param); ok {
			return RemoveOpenAIResponsesRejectedStatusAtIndex(body, index)
		}
		if param == "max_output_tokens" && gjson.GetBytes(body, "max_output_tokens").Exists() {
			retryBody, err := sjson.DeleteBytes(body, "max_output_tokens")
			if err != nil {
				return nil, "", false, fmt.Errorf("delete rejected max_output_tokens: %w", err)
			}
			return retryBody, "max_output_tokens parameter rejection", true, nil
		}
		if param == "truncation" && gjson.GetBytes(body, "truncation").Exists() {
			retryBody, err := sjson.DeleteBytes(body, "truncation")
			if err != nil {
				return nil, "", false, fmt.Errorf("delete rejected truncation: %w", err)
			}
			return retryBody, "truncation parameter rejection", true, nil
		}
	}

	messageContentParam := OpenAIResponsesInvalidTypeParamFromMessage(message)
	contentParam := param
	if contentParam == "" {
		contentParam = messageContentParam
	}
	if index, ok := OpenAIResponsesRejectedContentIndex(contentParam); ok &&
		contentParam == messageContentParam && IsExplicitOpenAIResponsesNullContentRejection(code, message) {
		return NormalizeOpenAIResponsesRejectedNullContentAtIndex(body, index)
	}
	maxZeroContentParam := OpenAIResponsesMaxZeroContentParamFromMessage(message)
	if index, ok := OpenAIResponsesRejectedContentIndex(param); ok &&
		param == maxZeroContentParam && code == "array_above_max_length" {
		return RemoveOpenAIResponsesRejectedReasoningContentAtIndex(body, index)
	}
	return nil, "", false, nil
}

// IsExplicitOpenAIResponsesFieldRejection recognizes supported parameter codes
// or known message phrases. Callers provide normalized lowercase evidence; code
// whitespace is trimmed here, and unrecognized wording alone cannot select an
// indexed field repair.
func IsExplicitOpenAIResponsesFieldRejection(code, message string) bool {
	switch strings.TrimSpace(code) {
	case "unknown_parameter", "unsupported_parameter":
		return true
	}
	return strings.Contains(message, "unknown parameter") ||
		strings.Contains(message, "unsupported parameter")
}

// OpenAIResponsesRejectedParamFromMessage extracts a supported field path from
// unknown/unsupported parameter wording. Matching ignores case and returns
// trimmed lowercase text; unrelated paths or unusable capture shapes return
// empty text.
func OpenAIResponsesRejectedParamFromMessage(message string) string {
	match := openAIResponsesRejectedMessageParamPattern.FindStringSubmatch(strings.TrimSpace(message))
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

// OpenAIResponsesMaxZeroContentParamFromMessage extracts an indexed content
// path from an array-too-long/zero-maximum message. Matching ignores case and
// returns lowercase text; absent matching evidence returns empty for the repair
// selector.
func OpenAIResponsesMaxZeroContentParamFromMessage(message string) string {
	match := openAIResponsesMaxZeroContentMessagePattern.FindStringSubmatch(strings.TrimSpace(message))
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

// OpenAIResponsesInvalidTypeParamFromMessage extracts an indexed content path
// rejected as null. Matching ignores case and returns lowercase text; unrelated
// invalid-type messages produce no path for null-content repair.
func OpenAIResponsesInvalidTypeParamFromMessage(message string) string {
	match := openAIResponsesInvalidTypeMessageParamPattern.FindStringSubmatch(strings.TrimSpace(message))
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

// OpenAIResponsesCacheModelRejectionParamFromMessage extracts a
// model-unsupported cache-breakpoint path. It accepts top-level or indexed
// input spellings case-insensitively and returns normalized text, while
// unrelated model rejection wording returns empty.
func OpenAIResponsesCacheModelRejectionParamFromMessage(message string) string {
	match := openAIResponsesCacheModelRejectionPattern.FindStringSubmatch(strings.TrimSpace(message))
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

// IsExplicitOpenAIResponsesNullContentRejection recognizes normalized
// invalid-type, invalid-request, or empty-code evidence with explicit
// null-content wording. Other codes or unmatched messages return false,
// preventing a broad content rewrite from generic validation errors.
func IsExplicitOpenAIResponsesNullContentRejection(code, message string) bool {
	code = strings.TrimSpace(code)
	return (code == "invalid_type" || code == "invalid_request_error" || code == "") &&
		openAIResponsesInvalidTypeMessageParamPattern.MatchString(strings.TrimSpace(message))
}

// OpenAIResponsesRejectedNamespaceIndex parses an input[N].namespace parameter
// with the shared index validator. Surrounding whitespace and case are
// accepted; false distinguishes unmatched or overflowed paths from a valid
// index zero.
func OpenAIResponsesRejectedNamespaceIndex(param string) (int, bool) {
	return OpenAIResponsesRejectedInputIndex(openAIResponsesRejectedNamespaceParamPattern, param)
}

// OpenAIResponsesRejectedStatusIndex parses an input[N].status parameter with
// the shared index validator. Surrounding whitespace and case are accepted;
// false distinguishes unmatched or overflowed paths from a valid index zero.
func OpenAIResponsesRejectedStatusIndex(param string) (int, bool) {
	return OpenAIResponsesRejectedInputIndex(openAIResponsesRejectedStatusParamPattern, param)
}

// OpenAIResponsesRejectedContentIndex parses an input[N].content parameter with
// the shared index validator. Surrounding whitespace and case are accepted;
// false distinguishes unmatched or overflowed paths from a valid index zero.
func OpenAIResponsesRejectedContentIndex(param string) (int, bool) {
	return OpenAIResponsesRejectedInputIndex(openAIResponsesRejectedContentParamPattern, param)
}

// OpenAIResponsesRejectedCacheIndex parses an input[N].prompt_cache_breakpoint
// parameter with the shared index validator. Surrounding whitespace and case
// are accepted; false distinguishes unmatched or overflowed paths from a valid
// index zero.
func OpenAIResponsesRejectedCacheIndex(param string) (int, bool) {
	return OpenAIResponsesRejectedInputIndex(openAIResponsesRejectedCacheParamPattern, param)
}

// OpenAIResponsesRejectedInputIndex parses a nonnegative index from one capture
// in the supplied pattern. The pattern is required, param is trimmed, and
// shape/overflow failures return zero with false so callers do not accidentally
// target index zero.
func OpenAIResponsesRejectedInputIndex(pattern *regexp.Regexp, param string) (int, bool) {
	match := pattern.FindStringSubmatch(strings.TrimSpace(param))
	if len(match) != 2 {
		return 0, false
	}
	index, err := strconv.Atoi(match[1])
	if err == nil && index >= 0 {
		return index, true
	}
	return 0, false
}

// RemoveOpenAIResponsesRejectedStatusAtIndex returns a request with rejected
// status fields removed and a repair reason. A missing object or status at
// index returns nil bytes with false. The original body supplies the array
// positions used for selection.
//
// When the rejected object has a nonblank type, all object items with that same
// trimmed type lose status together, keeping repeated indexed failures within
// one repair attempt. Field deletion preserves array positions and other types
// retain their status. If no same-type cleanup occurs, only the named item is
// repaired; failed splicing reports an error before publishing an eligible
// replacement.
func RemoveOpenAIResponsesRejectedStatusAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	itemPath := fmt.Sprintf("input.%d", index)
	rejected := gjson.GetBytes(body, itemPath)
	if !rejected.IsObject() {
		return nil, "", false, nil
	}
	if !gjson.GetBytes(body, itemPath+".status").Exists() {
		return nil, "", false, nil
	}

	retryBody := body
	cleared := 0
	rejectedType := strings.TrimSpace(rejected.Get("type").String())
	if input := gjson.GetBytes(body, "input"); rejectedType != "" && input.IsArray() {
		// Field deletion retains input positions, so the original array can identify
		// every same-type status path while the progressively repaired body supplies field existence.
		for itemIndex, item := range input.Array() {
			if !item.IsObject() || strings.TrimSpace(item.Get("type").String()) != rejectedType {
				continue
			}
			statusPath := fmt.Sprintf("input.%d.status", itemIndex)
			if !gjson.GetBytes(retryBody, statusPath).Exists() {
				continue
			}
			next, err := sjson.DeleteBytes(retryBody, statusPath)
			if err != nil {
				return nil, "", false, fmt.Errorf("delete rejected status at input[%d]: %w", itemIndex, err)
			}
			retryBody = next
			cleared++
		}
	}
	if cleared == 0 {
		// Missing type evidence cannot justify broad cleanup. Repair the named index
		// alone so unrelated item statuses remain available to their upstream protocol consumers.
		next, err := sjson.DeleteBytes(retryBody, itemPath+".status")
		if err != nil {
			return nil, "", false, fmt.Errorf("delete rejected status at input[%d]: %w", index, err)
		}
		retryBody = next
	}
	return retryBody, "indexed status parameter rejection", true, nil
}

// RemoveOpenAIResponsesRejectedCacheAtIndex deletes one existing
// cache-breakpoint field from an object input item. Missing or incompatible
// shapes return nil/false; successful deletion returns a repair reason and
// keeps other request fields for retry.
func RemoveOpenAIResponsesRejectedCacheAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	itemPath := fmt.Sprintf("input.%d", index)
	if !gjson.GetBytes(body, itemPath).IsObject() {
		return nil, "", false, nil
	}
	cachePath := itemPath + ".prompt_cache_breakpoint"
	if !gjson.GetBytes(body, cachePath).Exists() || hasOpenAIExplicitCacheTTL(body, cachePath) {
		return nil, "", false, nil
	}
	retryBody, err := sjson.DeleteBytes(body, cachePath)
	if err != nil {
		return nil, "", false, fmt.Errorf("delete rejected prompt_cache_breakpoint at input[%d]: %w", index, err)
	}
	return retryBody, "indexed prompt_cache_breakpoint parameter rejection", true, nil
}

// hasOpenAIExplicitCacheTTL reports whether a legacy repair would alter paid
// cache retention. An explicit per-breakpoint or inherited request TTL keeps
// the vendor rejection visible to the caller.
func hasOpenAIExplicitCacheTTL(body []byte, breakpointPath string) bool {
	return gjson.GetBytes(body, "prompt_cache_options.ttl").Exists() ||
		gjson.GetBytes(body, breakpointPath+".ttl").Exists()
}

// NormalizeOpenAIResponsesRejectedNullContentAtIndex repairs only an object
// whose content is explicitly null. Reasoning loses content, while messages or
// role-bearing items receive an empty string; other item kinds return nil/false
// for the retry selector.
func NormalizeOpenAIResponsesRejectedNullContentAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	itemPath := fmt.Sprintf("input.%d", index)
	item := gjson.GetBytes(body, itemPath)
	content := gjson.GetBytes(body, itemPath+".content")
	if !item.IsObject() || !content.Exists() || content.Type != gjson.Null {
		return nil, "", false, nil
	}

	itemType := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	role := strings.TrimSpace(item.Get("role").String())
	contentPath := itemPath + ".content"
	switch {
	case itemType == "reasoning":
		// Null reasoning content can be omitted while its summary and ciphertext
		// remain available; deleting this field keeps the rest of the replay item intact.
		retryBody, err := sjson.DeleteBytes(body, contentPath)
		if err != nil {
			return nil, "", false, fmt.Errorf("delete rejected null content at input[%d]: %w", index, err)
		}
		return retryBody, "indexed reasoning null content rejection", true, nil
	case itemType == "message" || role != "":
		// Message-shaped items retain an explicit content string after null rejection,
		// so their role and neighboring replay fields remain usable in the retried request.
		retryBody, err := sjson.SetBytes(body, contentPath, "")
		if err != nil {
			return nil, "", false, fmt.Errorf("normalize rejected null content at input[%d]: %w", index, err)
		}
		return retryBody, "indexed message null content rejection", true, nil
	default:
		return nil, "", false, nil
	}
}

// RemoveOpenAIResponsesRejectedReasoningContentAtIndex deletes nonempty array
// content from a reasoning item. Missing, empty, or differently shaped fields
// return nil/false. Call after NormalizeOpenAIResponsesRejectedFieldRetryBody
// has matched the upstream zero-maximum rejection; this helper checks the
// selected item shape rather than inspecting error evidence.
func RemoveOpenAIResponsesRejectedReasoningContentAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	itemPath := fmt.Sprintf("input.%d", index)
	item := gjson.GetBytes(body, itemPath)
	content := item.Get("content")
	if !item.IsObject() || strings.TrimSpace(item.Get("type").String()) != "reasoning" || !content.IsArray() || len(content.Array()) == 0 {
		return nil, "", false, nil
	}
	retryBody, err := sjson.DeleteBytes(body, itemPath+".content")
	if err != nil {
		return nil, "", false, fmt.Errorf("delete rejected reasoning content at input[%d]: %w", index, err)
	}
	return retryBody, "indexed reasoning content maximum-length rejection", true, nil
}

// RemoveOpenAIResponsesRejectedNamespaceAtIndex deletes namespace from
// recognized historical tool-call kinds. Missing namespace or other item types
// return nil/false; an eligible result includes the reason and keeps unrelated
// request fields.
func RemoveOpenAIResponsesRejectedNamespaceAtIndex(body []byte, index int) ([]byte, string, bool, error) {
	itemPath := fmt.Sprintf("input.%d", index)
	itemType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, itemPath+".type").String()))
	// Namespace stripping is defined for these historical tool-call shapes. Keep
	// other item fields intact because their meanings are not established by this tool compatibility repair.
	switch itemType {
	case "function_call", "tool_call", "custom_tool_call", "mcp_tool_call":
	default:
		return nil, "", false, nil
	}

	namespacePath := itemPath + ".namespace"
	if !gjson.GetBytes(body, namespacePath).Exists() {
		return nil, "", false, nil
	}
	retryBody, err := sjson.DeleteBytes(body, namespacePath)
	if err != nil {
		return nil, "", false, fmt.Errorf("delete rejected namespace at input[%d]: %w", index, err)
	}
	return retryBody, "indexed namespace parameter rejection", true, nil
}

// Budget returns this state shared retry-budget pointer or nil for nil states.
// Callers pass the handle into another constructed state to share admission;
// budget counters and mutex access remain internal.
func (s *ResponsesRejectedFieldRetryState) Budget() *ResponsesRejectedFieldRetryBudget {
	if s == nil {
		return nil
	}
	return s.budget
}
