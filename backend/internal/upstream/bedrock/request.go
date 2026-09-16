package bedrock

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	claude "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const DefaultBedrockRegion = "us-east-1"

// FeatureKeyBedrockCCCompat 是 Channel.FeaturesConfig 中 Bedrock CC 兼容开关的配置键。
const FeatureKeyBedrockCCCompat = "bedrock_cc_compat"

var BedrockCrossRegionPrefixes = []string{"us.", "eu.", "apac.", "jp.", "au.", "us-gov.", "global."}

func BedrockRuntimeRegion(account *RouteInput) string {
	if account == nil {
		return DefaultBedrockRegion
	}
	if region := strings.TrimSpace(account.Region); region != "" {
		return region
	}
	return DefaultBedrockRegion
}

func ShouldForceBedrockGlobal(account *RouteInput) bool {
	return account != nil && account.ForceGlobal
}

func IsRegionalBedrockModelID(modelID string) bool {
	for _, prefix := range BedrockCrossRegionPrefixes {
		if strings.HasPrefix(modelID, prefix) {
			return true
		}
	}
	return false
}

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

// ResolveBedrockModelID 为调度与模型目录提供同一条区域解析边界。
func ResolveBedrockModelID(account *RouteInput, requestedModel string) (string, bool) {
	route, err := ResolveBedrockModelRoute(account, requestedModel)
	return route.ModelID, err == nil
}

// BuildBedrockURL 构建 Bedrock InvokeModel 的 URL
// stream=true 时使用 invoke-with-response-stream 端点
// modelID 中的特殊字符会被 URL 编码（与 litellm 的 urllib.parse.quote(safe="") 对齐）
func BuildBedrockURL(region, modelID string, stream bool) string {
	if region == "" {
		region = DefaultBedrockRegion
	}
	encodedModelID := url.PathEscape(modelID)
	// url.PathEscape 不编码冒号（RFC 允许 path 中出现 ":"），
	// 但 AWS Bedrock 期望模型 ID 中的冒号被编码为 %3A
	encodedModelID = strings.ReplaceAll(encodedModelID, ":", "%3A")
	if stream {
		return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke-with-response-stream", region, encodedModelID)
	}
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke", region, encodedModelID)
}

// PrepareBedrockRequestBody 处理请求体以适配 Bedrock API
//  1. 注入 anthropic_version
//  2. 注入 anthropic_beta（从客户端 anthropic-beta 头解析）
//  3. 移除 Bedrock 不支持的字段（model, stream, output_format, output_config）
//  4. 移除工具定义中的 custom 字段（Claude Code 会发送 custom: {defer_loading: true}）
//  5. 清理 cache_control 中 Bedrock 不支持的字段（scope, ttl）
//  6. 修复 thinking 字段兼容性（Opus 4.7 仅支持 adaptive，enabled 需要 budget_tokens）
//  7. 清理 tool_use.id / tool_use_id 中 Bedrock 不接受的字符
//  8. 根据最终 Bedrock beta tokens 剥离不再支持的 beta 字段
func PrepareBedrockRequestBody(body []byte, modelID string, betaHeader string) ([]byte, error) {
	betaTokens := ResolveBedrockBetaTokens(betaHeader, body, modelID)
	return PrepareBedrockRequestBodyWithTokens(body, modelID, betaTokens, false)
}

// PrepareBedrockRequestBodyWithTokens 使用预解析的 beta token 准备 Bedrock 请求体。
// ccCompat 启用 CC 兼容模式时额外处理 thinking 类型转换和 tool_use.id 清理。
func PrepareBedrockRequestBodyWithTokens(body []byte, modelID string, betaTokens []string, ccCompat bool) ([]byte, error) {
	var err error

	betaTokens = FilterBedrockBetaTokens(betaTokens)
	body = SanitizeBedrockFieldsForBetaTokens(body, betaTokens)

	// 注入 anthropic_version（Bedrock 要求）
	body, err = sjson.SetBytes(body, "anthropic_version", "bedrock-2023-05-31")
	if err != nil {
		return nil, fmt.Errorf("inject anthropic_version: %w", err)
	}

	// 注入 anthropic_beta（Bedrock Invoke 通过请求体传递 beta 头，而非 HTTP 头）
	// 1. 从客户端 anthropic-beta header 解析
	// 2. 根据请求体内容自动补齐必要的 beta token
	//    参考 litellm: AnthropicModelInfo.get_anthropic_beta_list() + _get_tool_search_beta_header_for_bedrock()
	if len(betaTokens) > 0 {
		body, err = sjson.SetBytes(body, "anthropic_beta", betaTokens)
		if err != nil {
			return nil, fmt.Errorf("inject anthropic_beta: %w", err)
		}
		logger.LegacyPrintf("service.gateway", "[Bedrock] Injected beta tokens: %v (model=%s ccCompat=%v)", betaTokens, modelID, ccCompat)
	} else {
		body, _ = sjson.DeleteBytes(body, "anthropic_beta")
	}

	// 移除 Bedrock 不支持的 Anthropic 直连 API 专有顶层字段
	body, _ = sjson.DeleteBytes(body, "provider")
	body, _ = sjson.DeleteBytes(body, "metadata")

	// 移除 model 字段（Bedrock 通过 URL 指定模型）
	body, err = sjson.DeleteBytes(body, "model")
	if err != nil {
		return nil, fmt.Errorf("remove model field: %w", err)
	}

	// 移除 stream 字段（Bedrock 通过不同端点控制流式，不接受请求体中的 stream 字段）
	body, err = sjson.DeleteBytes(body, "stream")
	if err != nil {
		return nil, fmt.Errorf("remove stream field: %w", err)
	}

	// 转换 output_format（Bedrock Invoke 不支持此字段，但可将 schema 内联到最后一条 user message）
	// 参考 litellm: _convert_output_format_to_inline_schema()
	body = ConvertOutputFormatToInlineSchema(body)

	// 移除 output_config 字段（Bedrock Invoke 不支持）
	body, err = sjson.DeleteBytes(body, "output_config")
	if err != nil {
		return nil, fmt.Errorf("remove output_config field: %w", err)
	}

	// 移除工具定义中的 custom 字段
	// Claude Code (v2.1.69+) 在 tool 定义中发送 custom: {defer_loading: true}，
	// Anthropic API 接受但 Bedrock 会拒绝并报 "Extra inputs are not permitted"
	body = RemoveCustomFieldFromTools(body)

	// 清理 cache_control 中 Bedrock 不支持的字段
	body = SanitizeBedrockCacheControl(body, modelID)

	// CC 兼容模式：修复 CC 发送的 Bedrock 不兼容字段
	if ccCompat {
		body = SanitizeBedrockThinking(body, modelID)
		body = SanitizeBedrockToolUseIDs(body)
	}

	return body, nil
}

// ResolveBedrockBetaTokens computes the final Bedrock beta token list before policy filtering.
func ResolveBedrockBetaTokens(betaHeader string, body []byte, modelID string) []string {
	betaTokens := ParseAnthropicBetaHeader(betaHeader)
	betaTokens = AutoInjectBedrockBetaTokens(betaTokens, body, modelID)
	return FilterBedrockBetaTokens(betaTokens)
}

// ConvertOutputFormatToInlineSchema 将 output_format 中的 JSON schema 内联到最后一条 user message
// Bedrock Invoke 不支持 output_format 参数，litellm 的做法是将 schema 追加到用户消息中
// 参考: litellm AmazonAnthropicClaudeMessagesConfig._convert_output_format_to_inline_schema()
func ConvertOutputFormatToInlineSchema(body []byte) []byte {
	outputFormat := gjson.GetBytes(body, "output_format")
	if !outputFormat.Exists() || !outputFormat.IsObject() {
		return body
	}

	// 先从请求体中移除 output_format
	body, _ = sjson.DeleteBytes(body, "output_format")

	schema := outputFormat.Get("schema")
	if !schema.Exists() {
		return body
	}

	// 找到最后一条 user message
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}
	msgArr := messages.Array()
	lastUserIdx := -1
	for i := len(msgArr) - 1; i >= 0; i-- {
		if msgArr[i].Get("role").String() == "user" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return body
	}

	// 将 schema 序列化为 JSON 文本追加到该 message 的 content 数组
	schemaJSON, err := json.Marshal(json.RawMessage(schema.Raw))
	if err != nil {
		return body
	}

	content := msgArr[lastUserIdx].Get("content")
	basePath := fmt.Sprintf("messages.%d.content", lastUserIdx)

	if content.IsArray() {
		// 追加一个 text block 到 content 数组末尾
		idx := len(content.Array())
		body, _ = sjson.SetBytes(body, fmt.Sprintf("%s.%d.type", basePath, idx), "text")
		body, _ = sjson.SetBytes(body, fmt.Sprintf("%s.%d.text", basePath, idx), string(schemaJSON))
	} else if content.Type == gjson.String {
		// content 是纯字符串，转换为数组格式
		originalText := content.String()
		body, _ = sjson.SetBytes(body, basePath, []map[string]string{
			{"type": "text", "text": originalText},
			{"type": "text", "text": string(schemaJSON)},
		})
	}

	return body
}

// RemoveCustomFieldFromTools 移除 tools 数组中每个工具定义的 custom 字段
func RemoveCustomFieldFromTools(body []byte) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body
	}
	var err error
	for i := range tools.Array() {
		body, err = sjson.DeleteBytes(body, fmt.Sprintf("tools.%d.custom", i))
		if err != nil {
			// 删除失败不影响整体流程，跳过
			continue
		}
	}
	return body
}

// ClaudeVersionRe 匹配 Claude 模型 ID 中的版本号部分
// 支持 claude-{tier}-{major}、claude-{tier}-{major}-{minor} 和 claude-{tier}-{major}.{minor} 格式；
// 省略 minor 时按 0 处理，以兼容 Claude 5 这类主版本模型 ID。
var ClaudeVersionRe = regexp.MustCompile(`claude-(?:haiku|sonnet|opus)-(\d+)(?:[-.](\d+))?`)

// IsBedrockClaude45OrNewer 判断 Bedrock 模型 ID 是否为 Claude 4.5 或更新版本
// Claude 4.5+ 支持 cache_control 中的 ttl 字段（"5m" 和 "1h"）
func IsBedrockClaude45OrNewer(modelID string) bool {
	lower := strings.ToLower(modelID)
	if IsBedrockFable5(lower) {
		return true
	}
	matches := ClaudeVersionRe.FindStringSubmatch(lower)
	if matches == nil {
		return false
	}
	major, _ := strconv.Atoi(matches[1])
	minor := 0
	if len(matches) > 2 && matches[2] != "" {
		minor, _ = strconv.Atoi(matches[2])
	}
	return major > 4 || (major == 4 && minor >= 5)
}

// SanitizeBedrockCacheControl 清理 system 和 messages 中 cache_control 里
// Bedrock 不支持的字段：
//   - scope：Bedrock 不支持（如 "global" 跨请求缓存）
//   - ttl：仅 Claude 4.5+ 支持 "5m" 和 "1h"，旧模型需要移除
func SanitizeBedrockCacheControl(body []byte, modelID string) []byte {
	isClaude45 := IsBedrockClaude45OrNewer(modelID)

	// 清理 system 数组中的 cache_control
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

	// 清理 messages 中的 cache_control
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

// DeleteCacheControlUnsupportedFields 删除给定 cache_control 路径下 Bedrock 不支持的字段
func DeleteCacheControlUnsupportedFields(body []byte, basePath string, cc gjson.Result, isClaude45 bool) []byte {
	// Bedrock 不支持 scope（如 "global"）
	if cc.Get("scope").Exists() {
		body, _ = sjson.DeleteBytes(body, basePath+".scope")
	}

	// ttl：仅 Claude 4.5+ 支持 "5m" 和 "1h"，其余情况移除
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

func ParseAnthropicBetaHeader(header string) []string { return claude.ParseAnthropicBetaHeader(header) }

// BedrockSupportedBetaTokens 是 Bedrock Invoke 支持的 beta 头白名单
// 参考: AWS Bedrock 官方文档 + litellm anthropic_beta_headers_config.json
// 更新策略: 当 AWS Bedrock 新增支持的 beta token 时需同步更新此白名单
var BedrockSupportedBetaTokens = map[string]bool{
	"computer-use-2025-01-24":                true,
	"computer-use-2025-11-24":                true,
	"context-1m-2025-08-07":                  true,
	"context-management-2025-06-27":          true, // 支持压缩与 clear_thinking，AWS 文档已支持
	"compact-2026-01-12":                     true, // 官方支持，仅 InvokeModel API（Opus 4.6+）
	"fine-grained-tool-streaming-2025-05-14": true, // AWS 工具调用文档已支持
	// "interleaved-thinking-2025-05-14": false, // 无官方文档支持
	"tool-search-tool-2025-10-19": true,
	"tool-examples-2025-10-29":    true,
}

const BedrockContextManagementBetaToken = "context-management-2025-06-27"

// BedrockBetaTokenTransforms 定义 Bedrock Invoke 特有的 beta 头转换规则
// Anthropic 直接 API 使用通用头，Bedrock Invoke 需要特定的替代头
var BedrockBetaTokenTransforms = map[string]string{
	"advanced-tool-use-2025-11-20": "tool-search-tool-2025-10-19",
}

// AutoInjectBedrockBetaTokens 根据请求体内容自动补齐必要的 beta token
// 参考 litellm: AnthropicModelInfo.get_anthropic_beta_list() 和
// AmazonAnthropicClaudeMessagesConfig._get_tool_search_beta_header_for_bedrock()
//
// 客户端（特别是非 Claude Code 客户端）可能只在 body 中启用了功能而不在 header 中带对应 beta token，
// 这里通过检测请求体特征自动补齐，确保 Bedrock Invoke 不会因缺少必要 beta 头而 400。
func AutoInjectBedrockBetaTokens(tokens []string, body []byte, modelID string) []string {
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

	// 注意：thinking 字段不再自动注入 interleaved-thinking-2025-05-14
	// 因为该 beta token 未在 AWS Bedrock 官方文档中确认支持

	// 检测 computer_use 工具
	// tools 中有 type="computer_20xxxxxx" 的工具 → 需要 computer-use beta
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
			// programmatic tool calling 和 input examples 需要 advanced-tool-use，
			// 后续 FilterBedrockBetaTokens 会将其转换为 Bedrock 特定的 tool-search-tool
			inject("advanced-tool-use-2025-11-20")
		}
		if toolSearchUsed && BedrockModelSupportsToolSearch(modelID) {
			// 纯 tool search（无 programmatic/inputExamples）时直接注入 Bedrock 特定头，
			// 跳过 advanced-tool-use → tool-search-tool 的转换步骤（与 litellm 对齐）
			if !programmaticToolCallingUsed && !inputExamplesUsed {
				inject("tool-search-tool-2025-10-19")
			} else {
				inject("advanced-tool-use-2025-11-20")
			}
		}
	}

	return tokens
}

func IsBedrockToolSearchType(toolType string) bool {
	return toolType == "tool_search_tool_regex_20251119" || toolType == "tool_search_tool_bm25_20251119"
}

func HasCodeExecutionAllowedCallers(tool gjson.Result) bool {
	allowedCallers := tool.Get("allowed_callers")
	if ContainsStringInJSONArray(allowedCallers, "code_execution_20250825") {
		return true
	}
	return ContainsStringInJSONArray(tool.Get("function.allowed_callers"), "code_execution_20250825")
}

func HasInputExamples(tool gjson.Result) bool {
	if arr := tool.Get("input_examples"); arr.Exists() && arr.IsArray() && len(arr.Array()) > 0 {
		return true
	}
	arr := tool.Get("function.input_examples")
	return arr.Exists() && arr.IsArray() && len(arr.Array()) > 0
}

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

// BedrockModelSupportsToolSearch 判断 Bedrock 模型是否支持 tool search
// 目前仅 Claude Opus/Sonnet 4.5+ 支持，Haiku 不支持
func BedrockModelSupportsToolSearch(modelID string) bool {
	lower := strings.ToLower(modelID)
	matches := ClaudeVersionRe.FindStringSubmatch(lower)
	if matches == nil {
		return false
	}
	// Haiku 不支持 tool search
	if strings.Contains(lower, "haiku") {
		return false
	}
	major, _ := strconv.Atoi(matches[1])
	minor := 0
	if len(matches) > 2 && matches[2] != "" {
		minor, _ = strconv.Atoi(matches[2])
	}
	return major > 4 || (major == 4 && minor >= 5)
}

// FilterBedrockBetaTokens 过滤并转换 beta token 列表，仅保留 Bedrock Invoke 支持的 token
// 1. 应用转换规则（如 advanced-tool-use → tool-search-tool）
// 2. 过滤掉 Bedrock 不支持的 token（如 output-128k, files-api, structured-outputs 等）
// 3. 自动关联 tool-examples（当 tool-search-tool 存在时）
func FilterBedrockBetaTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	var result []string

	for _, t := range tokens {
		// 应用转换规则
		if replacement, ok := BedrockBetaTokenTransforms[t]; ok {
			t = replacement
		}
		// 只保留白名单中的 token，且去重
		if BedrockSupportedBetaTokens[t] && !seen[t] {
			result = append(result, t)
			seen[t] = true
		}
	}

	// 自动关联: tool-search-tool 存在时，确保 tool-examples 也存在
	if seen["tool-search-tool-2025-10-19"] && !seen["tool-examples-2025-10-29"] {
		result = append(result, "tool-examples-2025-10-29")
	}

	return result
}

// SanitizeBedrockFieldsForBetaTokens 根据最终保留的 beta token 删除 Bedrock 不支持的请求字段。
func SanitizeBedrockFieldsForBetaTokens(body []byte, betaTokens []string) []byte {
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

// ContainsBedrockBetaToken 判断最终 beta token 列表中是否包含指定能力开关。
func ContainsBedrockBetaToken(tokens []string, target string) bool {
	for _, token := range tokens {
		if token == target {
			return true
		}
	}
	return false
}

// ContainsAnyBedrockBetaToken 判断 tokens 是否包含 targets 中的任意一个 token。
func ContainsAnyBedrockBetaToken(tokens []string, targets ...string) bool {
	for _, target := range targets {
		if ContainsBedrockBetaToken(tokens, target) {
			return true
		}
	}
	return false
}

// BedrockToolUseIDRe 匹配 Bedrock 允许的 tool_use ID 字符（字母、数字、下划线、连字符）
var BedrockToolUseIDRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// IsBedrockOpus47OrNewer 判断 Bedrock 模型 ID 是否为 Claude Opus 4.7 或更新版本
// Opus 4.7 仅支持 thinking.type: "adaptive"，不支持 "enabled"
func IsBedrockOpus47OrNewer(modelID string) bool {
	lower := strings.ToLower(modelID)
	if !strings.Contains(lower, "opus") {
		return false
	}
	matches := ClaudeVersionRe.FindStringSubmatch(lower)
	if matches == nil {
		return false
	}
	major, _ := strconv.Atoi(matches[1])
	minor := 0
	if len(matches) > 2 && matches[2] != "" {
		minor, _ = strconv.Atoi(matches[2])
	}
	return major > 4 || (major == 4 && minor >= 7)
}

func IsBedrockFable5(modelID string) bool {
	return strings.Contains(strings.ToLower(modelID), "claude-fable-5")
}

const DefaultThinkingBudgetTokens = 10000

// SanitizeBedrockThinking 修复 thinking 字段的 Bedrock 兼容性问题：
//   - Fable 5: 仅使用 always-on adaptive thinking，不支持手动 budget_tokens
//   - Opus 4.7+: 仅支持 "adaptive"，将 "enabled" 转换为 "adaptive" 并移除 budget_tokens
//   - 其他模型: "enabled" 必须带 budget_tokens，缺失时补充默认值
func SanitizeBedrockThinking(body []byte, modelID string) []byte {
	thinking := gjson.GetBytes(body, "thinking")
	if !thinking.Exists() || !thinking.IsObject() {
		return body
	}

	thinkingType := thinking.Get("type").String()
	if thinkingType == "" {
		return body
	}

	if IsBedrockFable5(modelID) {
		if thinkingType == "enabled" {
			body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
		}
		if thinkingType == "enabled" || thinkingType == "adaptive" {
			body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
		}
		return body
	}

	if IsBedrockOpus47OrNewer(modelID) {
		if thinkingType == "enabled" {
			body, _ = sjson.SetBytes(body, "thinking.type", "adaptive")
			body, _ = sjson.DeleteBytes(body, "thinking.budget_tokens")
		}
		return body
	}

	if thinkingType == "enabled" && !thinking.Get("budget_tokens").Exists() {
		body, _ = sjson.SetBytes(body, "thinking.budget_tokens", DefaultThinkingBudgetTokens)
	}

	return body
}

// SanitizeBedrockToolUseIDs 清理 messages 中 tool_use.id 和 tool_result.tool_use_id
// 的非法字符。Bedrock 要求 ID 匹配 '^[a-zA-Z0-9_-]+$'。
func SanitizeBedrockToolUseIDs(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}
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

func SanitizeIDField(body []byte, id, path string) []byte {
	if id == "" {
		return body
	}
	sanitized := BedrockToolUseIDRe.ReplaceAllString(id, "_")
	if sanitized != id {
		body, _ = sjson.SetBytes(body, path, sanitized)
	}
	return body
}

const DefaultCCMaxTokens = 81920

// SanitizeBedrockCCFields 处理 Claude Code 发送的 Bedrock 不兼容字段：
//   - 移除 service_tier（Anthropic API 专有，Bedrock 不支持）
//   - 移除 interface_geo（Anthropic API 专有，Bedrock 不支持）
//   - 移除 context_management（Anthropic API 专有，Bedrock 不支持，CC v2.1.87+ 默认携带）
//   - 无条件移除 fallbacks / fallback_credit_token（server-side refusal fallback，
//     Anthropic 直连 beta API 专有；Bedrock Invoke 无对应 beta，永不支持）
//   - 注入 max_tokens 默认值 81920（CC 可能省略，Bedrock 要求必须提供）
//   - 注入 anthropic_version（CC 通过 HTTP 头发送，Bedrock 需要放在请求体中）
func SanitizeBedrockCCFields(body []byte) []byte {
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

// SanitizeBedrockCCBetaTokens 清理请求体中的 anthropic_beta 字段，只保留 Bedrock 支持的 beta token
// CC 可能在请求体中注入了 Bedrock 不支持的 beta token（如 prompt-caching 等），导致 ValidationException
func SanitizeBedrockCCBetaTokens(body []byte, modelID string) []byte {
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

	originalTokens := append([]string(nil), tokens...) // 保存原始 tokens 用于日志

	// 复用现有的 Bedrock beta token 过滤逻辑（自动注入 + 白名单过滤 + 转换）
	// 即使 tokens 为空，也要执行自动注入（根据 body 内容补充必要的 beta token）
	tokens = AutoInjectBedrockBetaTokens(tokens, body, modelID)
	tokens = FilterBedrockBetaTokens(tokens)

	if len(tokens) == 0 {
		// 所有 token 都被过滤掉，删除 anthropic_beta 字段
		body, _ = sjson.DeleteBytes(body, "anthropic_beta")
		logger.LegacyPrintf("service.gateway", "[Bedrock CC Compat] Removed all beta tokens: original=%v", originalTokens)
	} else {
		// 更新为过滤后的 token 列表
		body, _ = sjson.SetBytes(body, "anthropic_beta", tokens)
		if len(originalTokens) > 0 {
			logger.LegacyPrintf("service.gateway", "[Bedrock CC Compat] Filtered beta tokens: original=%v final=%v", originalTokens, tokens)
		} else {
			logger.LegacyPrintf("service.gateway", "[Bedrock CC Compat] Auto-injected beta tokens: %v", tokens)
		}
	}

	return body
}
