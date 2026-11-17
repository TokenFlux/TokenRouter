package bedrock

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	claude "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// request.go adapts Anthropic request bodies and model URLs for Bedrock Invoke
// operations.
// Bedrock Invoke bodies differ from direct Anthropic messages in version
// placement, beta encoding, and URL-carried model/stream selection.
// Preparation resolves and filters beta tokens before removing fields whose
// enabling beta is absent, then adapts output schemas, tool extensions, and
// cache controls.
//
// Claude Code compatibility adds thinking and tool-ID normalization after the
// general preparation. Model rules come from the first supplied override or
// the catalogue, while some Fable-specific handling still checks the catalogue
// predicate. Byte-level JSON edits keep unrelated source text when those
// operations succeed; several cleanup helpers discard modification errors and
// therefore do not provide an atomic transformation guarantee. Tool IDs use
// one lossy alphabet replacement across calls and results, leaving collision
// avoidance to callers.

const (
	// DefaultBedrockRegion supplies the absent source region for runtime routing
	// and Invoke URL construction, while credentials use the resolved signing
	// region.
	DefaultBedrockRegion = "us-east-1"

	// FeatureKeyBedrockCCCompat selects the group setting that enables the extra
	// Claude Code request adaptations before a prepared Invoke body is built.
	FeatureKeyBedrockCCCompat = "bedrock_cc_compat"

	// BedrockContextManagementBetaToken gates context edits;
	// DefaultThinkingBudgetTokens and DefaultCCMaxTokens supply missing budget
	// and output values for their separate compatibility paths.
	BedrockContextManagementBetaToken = "context-management-2025-06-27"
	DefaultThinkingBudgetTokens       = 10000
	DefaultCCMaxTokens                = 81920
)

var (
	// BedrockCrossRegionPrefixes identifies known inference-profile prefixes for
	// base-ID lookup and vendor-ID recognition; date and version suffixes remain
	// on the resulting identity.
	BedrockCrossRegionPrefixes = []string{"us.", "eu.", "apac.", "jp.", "au.", "us-gov.", "global."}

	// BedrockSupportedBetaTokens lists beta tokens allowed in Invoke requests.
	// Sources: AWS Bedrock documentation and litellm anthropic_beta_headers_config.json.
	// Verify upstream support when updating this allowlist.
	BedrockSupportedBetaTokens = map[string]bool{
		"computer-use-2025-01-24":                true,
		"computer-use-2025-11-24":                true,
		"context-1m-2025-08-07":                  true,
		"context-management-2025-06-27":          true, // context compaction and clear_thinking
		"compact-2026-01-12":                     true, // InvokeModel compaction for supported Opus models
		"fine-grained-tool-streaming-2025-05-14": true, // fine-grained tool streaming
		// interleaved-thinking-2025-05-14 is excluded pending documented Bedrock support.
		"tool-search-tool-2025-10-19": true,
		"tool-examples-2025-10-29":    true,
	}

	// BedrockBetaTokenTransforms selects local Invoke beta replacements before
	// allowlist membership and deduplication, keeping advanced tool use aligned
	// with the tool-search spelling.
	BedrockBetaTokenTransforms = map[string]string{
		"advanced-tool-use-2025-11-20": "tool-search-tool-2025-10-19",
	}

	// BedrockToolUseIDRe finds characters replaced in call and result
	// identifiers; the shared alphabet keeps the same rewritten spelling for
	// matching source IDs.
	BedrockToolUseIDRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
)

// BedrockRuntimeRegion returns the trimmed provider source region or
// DefaultBedrockRegion when absent. A nil provider uses the same default,
// keeping runtime endpoint selection consistent with an omitted region.
func BedrockRuntimeRegion(provider *RouteInput) string {
	if provider == nil {
		return DefaultBedrockRegion
	}
	if region := strings.TrimSpace(provider.Region); region != "" {
		return region
	}
	return DefaultBedrockRegion
}

// ShouldForceBedrockGlobal reports the provider global-inference flag for
// route selection. A nil provider returns false, allowing callers to inspect
// optional routing input before a later resolver reports its absence.
func ShouldForceBedrockGlobal(provider *RouteInput) bool {
	return provider != nil && provider.ForceGlobal
}

// IsRegionalBedrockModelID reports whether a supplied model starts with a
// known inference prefix.
func IsRegionalBedrockModelID(modelID string) bool {
	for _, prefix := range BedrockCrossRegionPrefixes {
		if strings.HasPrefix(modelID, prefix) {
			return true
		}
	}
	return false
}

// IsLikelyBedrockModelID recognizes trimmed, case-folded vendor prefixes,
// ARNs, or regional inference prefixes. It is a shape classifier for
// passthrough selection; the upstream service validates actual resource
// existence and permissions.
func IsLikelyBedrockModelID(modelID string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "arn:") {
		return true
	}
	for _, prefix := range []string{
		"anthropic.",
		"amazon.",
		"meta.",
		"mistral.",
		"cohere.",
		"ai21.",
		"deepseek.",
		"stability.",
		"writer.",
		"nova.",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return IsRegionalBedrockModelID(lower)
}

// ResolveBedrockModelID returns the selected inference ID and a success flag
// from route resolution. Failure returns the route partial value with false;
// callers requiring diagnostics use ResolveBedrockModelRoute directly.
func ResolveBedrockModelID(provider *RouteInput, requestedModel string) (string, bool) {
	route, err := ResolveBedrockModelRoute(provider, requestedModel)
	return route.ModelID, err == nil
}

// BuildBedrockURL returns the regional Invoke action for modelID and stream.
// An exactly empty region selects DefaultBedrockRegion, model text is
// path-escaped with colons encoded separately, and callers supply a valid
// source region.
func BuildBedrockURL(region, modelID string, stream bool) string {
	if region == "" {
		region = DefaultBedrockRegion
	}
	encodedModelID := url.PathEscape(modelID)
	// Encode the colon separately because Go path escaping retains it, while this
	// Invoke builder uses encoded version separators inside the model resource
	// segment.
	encodedModelID = strings.ReplaceAll(encodedModelID, ":", "%3A")
	if stream {
		return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke-with-response-stream", region, encodedModelID)
	}
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke", region, encodedModelID)
}

// PrepareBedrockRequestBody resolves header/body beta requirements and
// delegates ordinary Invoke adaptation. It leaves the additional thinking and
// tool-ID compatibility steps disabled; callers requesting them use
// PrepareBedrockRequestBodyWithTokens.
func PrepareBedrockRequestBody(body []byte, modelID string, betaHeader string) ([]byte, error) {
	betaTokens := ResolveBedrockBetaTokens(betaHeader, body, modelID)
	return PrepareBedrockRequestBodyWithTokens(body, modelID, betaTokens, false)
}

// PrepareBedrockRequestBodyWithTokens adapts an Anthropic body to Invoke using
// precomputed beta tokens and optional model-rule overrides. Callers provide
// valid request JSON and use the returned bytes; explicit version, model,
// stream, and output-config edit failures return errors.
//
// Beta filtering precedes dependent-field cleanup, then body version and beta
// values are placed before model/stream removal and schema/tool/cache
// adaptation. ccCompat additionally normalizes thinking and tool IDs. Cleanup
// helpers can discard edit errors, so a successful result does not certify
// every optional cleanup succeeded. The first supplied model rule controls
// catalogue-driven helpers, and callers retain responsibility for matching
// those rules with the selected model.
func PrepareBedrockRequestBodyWithTokens(body []byte, modelID string, betaTokens []string, ccCompat bool, rules ...modelrules.ExecutionRules) ([]byte, error) {
	var err error

	betaTokens = FilterBedrockBetaTokens(betaTokens)
	body = SanitizeBedrockFieldsForBetaTokens(body, betaTokens)

	// Place the Invoke version in the body because this request dialect carries
	// protocol selection with JSON rather than the direct Anthropic version
	// header.
	body, err = sjson.SetBytes(body, "anthropic_version", "bedrock-2023-05-31")
	if err != nil {
		return nil, fmt.Errorf("inject anthropic_version: %w", err)
	}

	// Write the final beta list after dependent-field cleanup so the body
	// advertises the same feature set used to decide which optional request
	// fields survive.
	if len(betaTokens) > 0 {
		body, err = sjson.SetBytes(body, "anthropic_beta", betaTokens)
		if err != nil {
			return nil, fmt.Errorf("inject anthropic_beta: %w", err)
		}
		logger.LegacyPrintf("service.gateway", "[Bedrock] Injected beta tokens: %v (model=%s ccCompat=%v)", betaTokens, modelID, ccCompat)
	} else {
		// An empty accepted list removes the beta field so the body cannot continue
		// advertising rejected features; a deletion error is discarded here.
		body, _ = sjson.DeleteBytes(body, "anthropic_beta")
	}

	// Drop direct-request provider and metadata objects during Invoke adaptation;
	// these cleanup edits discard errors and leave later required-field handling
	// to report its own failures.
	body, _ = sjson.DeleteBytes(body, "provider")
	body, _ = sjson.DeleteBytes(body, "metadata")

	// Remove body model identity after route selection because the Invoke URL
	// carries the selected vendor resource, keeping body and endpoint model
	// selection from competing.
	body, err = sjson.DeleteBytes(body, "model")
	if err != nil {
		return nil, fmt.Errorf("remove model field: %w", err)
	}

	// Remove the direct stream flag because the Invoke action path selects
	// response framing; callers align the chosen action with their output
	// adapter.
	body, err = sjson.DeleteBytes(body, "stream")
	if err != nil {
		return nil, fmt.Errorf("remove stream field: %w", err)
	}

	// Adapt structured-output schema to user text before dropping output_config,
	// retaining an available schema prompt when the last user content accepts
	// text insertion.
	body = ConvertOutputFormatToInlineSchema(body)

	body, err = sjson.DeleteBytes(body, "output_config")
	if err != nil {
		return nil, fmt.Errorf("remove output_config field: %w", err)
	}

	// Remove custom tool extension data from the Invoke definitions, including
	// defer_loading, while retaining ordinary tool names and schemas for the
	// selected model.
	body = RemoveCustomFieldFromTools(body)

	body = SanitizeBedrockCacheControl(body, modelID, rules...)

	if ccCompat {
		body = SanitizeBedrockThinking(body, modelID, rules...)
		body = SanitizeBedrockToolUseIDs(body)
	}

	return body, nil
}

// ResolveBedrockBetaTokens combines parsed header tokens with body-feature
// injection and filters the resulting list. The first supplied execution rule
// overrides catalogue capability for feature detection; the returned list uses
// the Invoke allowlist and translations.
func ResolveBedrockBetaTokens(betaHeader string, body []byte, modelID string, rules ...modelrules.ExecutionRules) []string {
	betaTokens := ParseAnthropicBetaHeader(betaHeader)
	betaTokens = AutoInjectBedrockBetaTokens(betaTokens, body, modelID, rules...)
	return FilterBedrockBetaTokens(betaTokens)
}

// ConvertOutputFormatToInlineSchema removes an object-valued output_format and
// appends its schema to the last user message. Missing schema or suitable
// content returns the body after that removal; failed byte edits are
// discarded.
//
// Array content receives another text block, while string content becomes its
// original text followed by schema text. Earlier user messages are bypassed
// once the last user is selected, even when that content cannot receive the
// schema. Schema bytes are revalidated through RawMessage marshaling before
// insertion, and malformed schema leaves the already removed format without
// additional text.
func ConvertOutputFormatToInlineSchema(body []byte) []byte {
	outputFormat := gjson.GetBytes(body, "output_format")
	if !outputFormat.Exists() || !outputFormat.IsObject() {
		return body
	}

	// Remove the direct format object before finding an insertion target; later
	// missing schema or user content leaves this cleanup applied without schema
	// text.
	body, _ = sjson.DeleteBytes(body, "output_format")

	schema := outputFormat.Get("schema")
	if !schema.Exists() {
		return body
	}

	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}
	msgArr := messages.Array()
	lastUserIdx := -1
	for i, m := range slices.Backward(msgArr) {
		if m.Get("role").String() == "user" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return body
	}

	// Revalidate extracted raw JSON before adding it as user text; serialization
	// failure returns the already cleaned body and omits the schema prompt.
	schemaJSON, err := json.Marshal(json.RawMessage(schema.Raw))
	if err != nil {
		return body
	}

	content := msgArr[lastUserIdx].Get("content")
	basePath := fmt.Sprintf("messages.%d.content", lastUserIdx)

	if content.IsArray() {
		// Append schema text after the selected user parts so their existing content
		// stays ahead of the output instruction in that message.
		idx := len(content.Array())
		body, _ = sjson.SetBytes(body, fmt.Sprintf("%s.%d.type", basePath, idx), "text")
		body, _ = sjson.SetBytes(body, fmt.Sprintf("%s.%d.text", basePath, idx), string(schemaJSON))
	} else if content.Type == gjson.String {
		// Convert string content to two text blocks so the original user text
		// remains first and the requested schema follows it in the same message.
		originalText := content.String()
		body, _ = sjson.SetBytes(body, basePath, []map[string]string{
			{"type": "text", "text": originalText},
			{"type": "text", "text": string(schemaJSON)},
		})
	}

	return body
}

// RemoveCustomFieldFromTools deletes the custom extension from each
// tools-array entry. Missing or nonarray tools returns the supplied body;
// deletion errors continue with the byte result returned by sjson rather than
// rolling back earlier work.
func RemoveCustomFieldFromTools(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body
	}
	var err error
	// Remove the direct custom extension while leaving each tool definition in
	// place, so ordinary names and schemas still reach later Invoke preparation.
	for i := range tools.Array() {
		body, err = sjson.DeleteBytes(body, fmt.Sprintf("tools.%d.custom", i))
		if err != nil {
			// Continue through later tools after a failed deletion using the returned
			// byte result; this cleanup helper cannot restore earlier source bytes on
			// that path.
			continue
		}
	}
	return body
}

// IsBedrockClaude45OrNewer reports whether the resolved catalogue rules
// contain cache TTL entries.
func IsBedrockClaude45OrNewer(modelID string) bool {
	return len(modelrules.BedrockExecutionRules(modelID).CacheTTL) > 0
}

// SanitizeBedrockCacheControl removes scope and disallowed TTL fields from
// object cache controls at top level and in system, tool, and message blocks.
// The first rule override or catalogue determines whether 5m/1h can remain;
// cleanup edit errors are discarded.
func SanitizeBedrockCacheControl(body []byte, modelID string, rules ...modelrules.ExecutionRules) []byte {
	isClaude45 := len(bedrockExecutionRules(modelID, rules).CacheTTL) > 0

	if topCC := gjson.GetBytes(body, "cache_control"); topCC.Exists() && topCC.IsObject() {
		body = DeleteCacheControlUnsupportedFields(body, "cache_control", topCC, isClaude45)
	}

	systemArr := gjson.GetBytes(body, "system")
	if systemArr.Exists() && systemArr.IsArray() {
		for i, item := range systemArr.Array() {
			if !item.IsObject() {
				continue
			}
			cc := item.Get("cache_control")
			if !cc.Exists() || !cc.IsObject() {
				continue
			}
			body = DeleteCacheControlUnsupportedFields(body, fmt.Sprintf("system.%d.cache_control", i), cc, isClaude45)
		}
	}

	toolsArr := gjson.GetBytes(body, "tools")
	if toolsArr.Exists() && toolsArr.IsArray() {
		for i, item := range toolsArr.Array() {
			if !item.IsObject() {
				continue
			}
			cc := item.Get("cache_control")
			if !cc.Exists() || !cc.IsObject() {
				continue
			}
			body = DeleteCacheControlUnsupportedFields(body, fmt.Sprintf("tools.%d.cache_control", i), cc, isClaude45)
		}
	}

	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}
	for mi, msg := range messages.Array() {
		if !msg.IsObject() {
			continue
		}
		content := msg.Get("content")
		if !content.Exists() || !content.IsArray() {
			continue
		}
		for ci, block := range content.Array() {
			if !block.IsObject() {
				continue
			}
			cc := block.Get("cache_control")
			if !cc.Exists() || !cc.IsObject() {
				continue
			}
			body = DeleteCacheControlUnsupportedFields(body, fmt.Sprintf("messages.%d.content.%d.cache_control", mi, ci), cc, isClaude45)
		}
	}

	return body
}

// DeleteCacheControlUnsupportedFields removes scope at basePath and retains
// TTL only for the enabled capability and exact 5m/1h values. It uses cc for
// source-field presence, returns edited bytes, and discards deletion errors.
func DeleteCacheControlUnsupportedFields(body []byte, basePath string, cc gjson.Result, isClaude45 bool) []byte {
	// Strip scope from selected cache objects while keeping their remaining
	// fields; this independent cleanup also applies when a supported TTL survives.
	if cc.Get("scope").Exists() {
		body, _ = sjson.DeleteBytes(body, basePath+".scope")
	}

	// Use capability and exact TTL spelling together because catalogue support
	// alone does not validate an arbitrary duration text sent in cache_control.
	ttl := cc.Get("ttl")
	if ttl.Exists() {
		shouldRemove := true
		if isClaude45 {
			v := ttl.String()
			if v == "5m" || v == "1h" {
				shouldRemove = false
			}
		}
		if shouldRemove {
			body, _ = sjson.DeleteBytes(body, basePath+".ttl")
		}
	}

	return body
}

// ParseAnthropicBetaHeader delegates comma-list and JSON-array parsing to the
// shared Anthropic parser.
func ParseAnthropicBetaHeader(header string) []string { return claude.ParseAnthropicBetaHeader(header) }

// AutoInjectBedrockBetaTokens adds missing feature-required beta values to
// tokens before caller filtering. It can reuse the supplied slice storage,
// retains existing duplicates, and uses the first execution-rule override when
// present.
//
// Computer tools select the configured computer-use token. Programmatic
// callers or examples add advanced-tool-use, while eligible pure tool search
// selects its Invoke token directly. Detection recognizes both top-level and
// nested function feature fields; thinking alone adds no beta. The caller
// subsequently translates, allowlists, and deduplicates the combined values.
func AutoInjectBedrockBetaTokens(tokens []string, body []byte, modelID string, rules ...modelrules.ExecutionRules) []string {
	seen := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		seen[t] = true
	}

	inject := func(token string) {
		if !seen[token] {
			tokens = append(tokens, token)
			seen[token] = true
		}
	}

	// Infer beta requirements from tool features rather than thinking alone;
	// computer, programmatic, example, and search paths feed the caller final
	// translation and allowlist step.
	tools := gjson.GetBytes(body, "tools")
	if tools.Exists() && tools.IsArray() {
		toolSearchUsed := false
		programmaticToolCallingUsed := false
		inputExamplesUsed := false
		for _, tool := range tools.Array() {
			toolType := tool.Get("type").String()
			if strings.HasPrefix(toolType, "computer_20") {
				inject("computer-use-2025-11-24")
			}
			if IsBedrockToolSearchType(toolType) {
				toolSearchUsed = true
			}
			if HasCodeExecutionAllowedCallers(tool) {
				programmaticToolCallingUsed = true
			}
			if HasInputExamples(tool) {
				inputExamplesUsed = true
			}
		}
		if programmaticToolCallingUsed || inputExamplesUsed {
			// Programmatic callers and examples select advanced-tool-use here.
			// FilterBedrockBetaTokens later translates it to the Bedrock tool-search token.
			inject("advanced-tool-use-2025-11-20")
		}
		rule := bedrockExecutionRules(modelID, rules)
		if toolSearchUsed && rule.ToolSearch != nil && *rule.ToolSearch {
			// For an eligible pure search tool, use the Invoke token directly; mixed
			// programmatic/example features keep their advanced token until the shared
			// translation step.
			if !programmaticToolCallingUsed && !inputExamplesUsed {
				inject("tool-search-tool-2025-10-19")
			} else {
				inject("advanced-tool-use-2025-11-20")
			}
		}
	}

	return tokens
}

// IsBedrockToolSearchType recognizes the exact regex and BM25 tool-search type
// spellings.
func IsBedrockToolSearchType(toolType string) bool {
	return toolType == "tool_search_tool_regex_20251119" || toolType == "tool_search_tool_bm25_20251119"
}

// HasCodeExecutionAllowedCallers reports the code-execution caller marker in
// top-level or nested function arrays.
func HasCodeExecutionAllowedCallers(tool gjson.Result) bool {
	allowedCallers := tool.Get("allowed_callers")
	if ContainsStringInJSONArray(allowedCallers, "code_execution_20250825") {
		return true
	}
	return ContainsStringInJSONArray(tool.Get("function.allowed_callers"), "code_execution_20250825")
}

// HasInputExamples recognizes a nonempty input_examples array at either
// supported tool location.
func HasInputExamples(tool gjson.Result) bool {
	if arr := tool.Get("input_examples"); arr.Exists() && arr.IsArray() && len(arr.Array()) > 0 {
		return true
	}
	arr := tool.Get("function.input_examples")
	return arr.Exists() && arr.IsArray() && len(arr.Array()) > 0
}

// ContainsStringInJSONArray compares converted item text with target in a JSON
// array. Absent or nonarray results return false, and gjson item String
// conversion governs nonstring values.
func ContainsStringInJSONArray(result gjson.Result, target string) bool {
	if !result.Exists() || !result.IsArray() {
		return false
	}
	for _, item := range result.Array() {
		if item.String() == target {
			return true
		}
	}
	return false
}

// BedrockModelSupportsToolSearch reports a present true catalogue tool-search
// capability for modelID.
func BedrockModelSupportsToolSearch(modelID string) bool {
	rules := modelrules.BedrockExecutionRules(modelID)
	return rules.ToolSearch != nil && *rules.ToolSearch
}

// FilterBedrockBetaTokens translates, allowlists, and deduplicates tokens in
// encounter order. A selected tool-search token appends tool-examples if
// absent; an empty accepted set returns nil, and caller input is read without
// rewriting its elements.
//
// Translation happens before membership and deduplication, so advanced-tool-use
// and its direct search spelling select one accepted identity. The companion
// examples token is then added only when missing, giving both input forms the
// same final pair without moving earlier accepted tokens.
func FilterBedrockBetaTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	var result []string

	for _, t := range tokens {
		if replacement, ok := BedrockBetaTokenTransforms[t]; ok {
			t = replacement
		}
		if BedrockSupportedBetaTokens[t] && !seen[t] {
			result = append(result, t)
			seen[t] = true
		}
	}

	// Append the local companion tool-examples beta when tool search is selected,
	// so both direct and translated search tokens receive the same final feature
	// set.
	if seen["tool-search-tool-2025-10-19"] && !seen["tool-examples-2025-10-29"] {
		result = append(result, "tool-examples-2025-10-29")
	}

	return result
}

// SanitizeBedrockFieldsForBetaTokens removes context-management and fallback
// fields when their required tokens are absent. Callers supply the final
// filtered list; byte deletion errors are discarded, so this helper returns
// the resulting cleanup bytes.
func SanitizeBedrockFieldsForBetaTokens(body []byte, betaTokens []string) []byte {
	// Remove beta-dependent body fields against the same final token list used
	// for Invoke encoding, so filtered capabilities cannot leave their associated
	// direct-API options active.
	if !ContainsBedrockBetaToken(betaTokens, BedrockContextManagementBetaToken) && gjson.GetBytes(body, "context_management").Exists() {
		body, _ = sjson.DeleteBytes(body, "context_management")
	}
	if !ContainsBedrockBetaToken(betaTokens, claude.BetaServerSideFallback) && gjson.GetBytes(body, "fallbacks").Exists() {
		body, _ = sjson.DeleteBytes(body, "fallbacks")
	}
	if !ContainsAnyBedrockBetaToken(betaTokens, claude.BetaServerSideFallback, claude.BetaFallbackCredit, claude.BetaFallbackCreditLegacy) &&
		gjson.GetBytes(body, "fallback_credit_token").Exists() {
		body, _ = sjson.DeleteBytes(body, "fallback_credit_token")
	}
	return body
}

// ContainsBedrockBetaToken reports exact target membership in the supplied
// beta token slice.
func ContainsBedrockBetaToken(tokens []string, target string) bool {
	return slices.Contains(tokens, target)
}

// ContainsAnyBedrockBetaToken reports whether any supplied target occurs in
// the beta slice.
func ContainsAnyBedrockBetaToken(tokens []string, targets ...string) bool {
	for _, target := range targets {
		if ContainsBedrockBetaToken(tokens, target) {
			return true
		}
	}
	return false
}

// IsBedrockOpus47OrNewer combines catalogue adaptive-thinking mode with an
// Opus substring in modelID.
func IsBedrockOpus47OrNewer(modelID string) bool {
	return modelrules.BedrockExecutionRules(modelID).ThinkingMode == "adaptive" && strings.Contains(strings.ToLower(modelID), "opus")
}

// IsBedrockFable5 combines catalogue adaptive-thinking mode with a Fable
// substring in modelID.
func IsBedrockFable5(modelID string) bool {
	return modelrules.BedrockExecutionRules(modelID).ThinkingMode == "adaptive" && strings.Contains(strings.ToLower(modelID), "fable")
}

// SanitizeBedrockThinking adjusts existing object-valued thinking for the
// selected model rules and returns the edited bytes. Missing or empty type
// preserves the body, and cleanup modification errors are discarded.
//
// Adaptive modes convert enabled to adaptive and remove its explicit budget.
// Already adaptive Fable input also loses budget through the
// catalogue-specific predicate; other already adaptive input keeps it.
// Remaining enabled modes receive the default budget only when its field is
// absent, so an explicit zero remains present. The first supplied rule
// overrides the main thinking-mode lookup, while the Fable predicate still
// uses catalogue resolution.
func SanitizeBedrockThinking(body []byte, modelID string, rules ...modelrules.ExecutionRules) []byte {
	thinking := gjson.GetBytes(body, "thinking")
	if !thinking.Exists() || !thinking.IsObject() {
		return body
	}

	thinkingType := thinking.Get("type").String()
	if thinkingType == "" {
		return body
	}

	mode := bedrockExecutionRules(modelID, rules).ThinkingMode
	if mode == "adaptive" || mode == "adaptive_between_tools" {
		// Convert enabled input to the resolved adaptive dialect before dropping its
		// budget, so callers can supply one thinking shape to models with different
		// catalogue rules.
		if thinkingType == "enabled" {
			body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
		}
		if thinkingType == "enabled" || thinkingType == "adaptive" && IsBedrockFable5(modelID) {
			body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
		}
		return body
	}

	if thinkingType == "enabled" && !thinking.Get("budget_tokens").Exists() {
		body, _ = sjson.SetBytes(body, "thinking.budget_tokens", DefaultThinkingBudgetTokens)
	}

	return body
}

// SanitizeBedrockToolUseIDs applies the same allowed alphabet to top-level
// message tool_use.id and tool_result.tool_use_id values. String or absent
// content is skipped; distinct source IDs can collide after replacement, and
// callers keep identity compatibility in mind.
func SanitizeBedrockToolUseIDs(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}
	// Use identical character replacement for calls and results within message
	// content so a matching source tool ID still has the same rewritten spelling
	// on both sides.
	for mi, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.Exists() || !content.IsArray() {
			continue
		}
		for ci, block := range content.Array() {
			switch block.Get("type").String() {
			case "tool_use":
				body = SanitizeIDField(body, block.Get("id").String(), fmt.Sprintf("messages.%d.content.%d.id", mi, ci))
			case "tool_result":
				body = SanitizeIDField(body, block.Get("tool_use_id").String(), fmt.Sprintf("messages.%d.content.%d.tool_use_id", mi, ci))
			}
		}
	}
	return body
}

// SanitizeIDField replaces characters outside the shared tool-use alphabet
// with underscores at path. Empty or unchanged IDs retain body bytes, and
// byte-setting errors are discarded; replacement is lossy and does not prevent
// collisions.
func SanitizeIDField(body []byte, id, path string) []byte {
	if id == "" {
		return body
	}
	sanitized := BedrockToolUseIDRe.ReplaceAllString(id, "_")
	if sanitized != id {
		// Store the same lossy spelling selected for calls and results; setting
		// errors retain the returned byte result instead of restoring earlier edits.
		body, _ = sjson.SetBytes(body, path, sanitized)
	}
	return body
}

// SanitizeBedrockCCFields removes the listed unsupported Claude Code fields
// and supplies absent max_tokens/version defaults. Presence checks retain
// explicit values including null or zero, and byte edit errors are discarded
// while later cleanup continues.
func SanitizeBedrockCCFields(body []byte) []byte {
	// Apply this explicit Claude Code cleanup independently of beta admission
	// because its listed direct-API fields are removed before the ordinary Invoke
	// preparation and missing defaults are supplied.
	if gjson.GetBytes(body, "service_tier").Exists() {
		body, _ = sjson.DeleteBytes(body, "service_tier")
	}
	if gjson.GetBytes(body, "interface_geo").Exists() {
		body, _ = sjson.DeleteBytes(body, "interface_geo")
	}
	if gjson.GetBytes(body, "context_management").Exists() {
		body, _ = sjson.DeleteBytes(body, "context_management")
	}
	if gjson.GetBytes(body, "fallbacks").Exists() {
		body, _ = sjson.DeleteBytes(body, "fallbacks")
	}
	if gjson.GetBytes(body, "fallback_credit_token").Exists() {
		body, _ = sjson.DeleteBytes(body, "fallback_credit_token")
	}
	if !gjson.GetBytes(body, "max_tokens").Exists() {
		body, _ = sjson.SetBytes(body, "max_tokens", DefaultCCMaxTokens)
	}
	if !gjson.GetBytes(body, "anthropic_version").Exists() {
		body, _ = sjson.SetBytes(body, "anthropic_version", "bedrock-2023-05-31")
	}
	return body
}

// SanitizeBedrockCCBetaTokens normalizes an existing anthropic_beta field and
// adds inferred body-feature tokens before filtering. An absent field returns
// the supplied body without injection; a present nonarray field starts from an
// empty token list.
//
// String array entries are collected for diagnostics before feature injection
// can reuse their storage. Filtering translates and deduplicates accepted
// values, then an empty result removes the body field and a nonempty result
// replaces it. Those byte-edit errors are discarded, and the first supplied
// execution rule controls feature capability lookup.
func SanitizeBedrockCCBetaTokens(body []byte, modelID string, rules ...modelrules.ExecutionRules) []byte {
	betaField := gjson.GetBytes(body, "anthropic_beta")
	if !betaField.Exists() {
		return body
	}

	var tokens []string
	if betaField.IsArray() {
		for _, t := range betaField.Array() {
			if t.Type == gjson.String {
				tokens = append(tokens, t.String())
			}
		}
	}

	originalTokens := append([]string(nil), tokens...) // retained for diagnostics
	// Inspect features after retaining original tokens for diagnostics; an
	// existing empty beta field still allows required body-feature tokens to
	// reach the final allowlist step.

	tokens = AutoInjectBedrockBetaTokens(tokens, body, modelID, rules...)
	tokens = FilterBedrockBetaTokens(tokens)

	// Publish the accepted feature set after injection and filtering; an empty
	// list removes the field, while edit errors leave the returned byte result.
	if len(tokens) == 0 {
		body, _ = sjson.DeleteBytes(body, "anthropic_beta")
		logger.LegacyPrintf("service.gateway", "[Bedrock CC Compat] Removed all beta tokens: original=%v", originalTokens)
	} else {
		body, _ = sjson.SetBytes(body, "anthropic_beta", tokens)
		if len(originalTokens) > 0 {
			logger.LegacyPrintf("service.gateway", "[Bedrock CC Compat] Filtered beta tokens: original=%v final=%v", originalTokens, tokens)
		} else {
			logger.LegacyPrintf("service.gateway", "[Bedrock CC Compat] Auto-injected beta tokens: %v", tokens)
		}
	}

	return body
}

// bedrockExecutionRules returns the first supplied rule override, even when
// zero-valued. An absent override resolves catalogue rules for model; later
// slice entries are ignored so callers choose one authoritative rule
// observation.
func bedrockExecutionRules(model string, rules []modelrules.ExecutionRules) modelrules.ExecutionRules {
	if len(rules) > 0 {
		return rules[0]
	}
	return modelrules.BedrockExecutionRules(model)
}
