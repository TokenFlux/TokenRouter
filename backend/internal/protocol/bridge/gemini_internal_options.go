package bridge

import (
	"fmt"
	"strings"
)

func InternalHasWebSearchTool(tools []ClaudeTool) bool {
	for _, tool := range tools {
		if InternalIsWebSearchTool(tool) {
			return true
		}
	}
	return false
}

func InternalIsWebSearchTool(tool ClaudeTool) bool {
	if strings.HasPrefix(tool.Type, "web_search") || tool.Type == "google_search" {
		return true
	}

	name := strings.TrimSpace(tool.Name)
	switch name {
	case "web_search", "google_search", "web_search_20250305":
		return true
	default:
		return false
	}
}

func InternalIsCodeExecutionTool(tool ClaudeTool) bool {
	return strings.TrimSpace(tool.Type) == "code_execution"
}

// InternalHasMixedToolInvocations 判断构建后的工具声明是否同时包含函数声明与内置工具
// （googleSearch/codeExecution）。仅在两者并存时需要开启 includeServerSideToolInvocations。
func InternalHasMixedToolInvocations(declarations []GeminiToolDeclaration) bool {
	hasFunctions, hasBuiltin := false, false
	for _, declaration := range declarations {
		if len(declaration.FunctionDeclarations) > 0 {
			hasFunctions = true
		}
		if declaration.GoogleSearch != nil || declaration.CodeExecution != nil {
			hasBuiltin = true
		}
	}
	return hasFunctions && hasBuiltin
}

// buildTools 构建 tools
func BuildInternalGeminiTools(tools []ClaudeTool) ([]GeminiToolDeclaration, []string) {
	diagnostics := []string{}
	diagnostic := func(format string, args ...any) { diagnostics = append(diagnostics, fmt.Sprintf(format, args...)) }
	if len(tools) == 0 {
		return nil, diagnostics
	}

	hasWebSearch := InternalHasWebSearchTool(tools)
	hasCodeExecution := false
	for _, tool := range tools {
		if InternalIsCodeExecutionTool(tool) {
			hasCodeExecution = true
			break
		}
	}

	// 普通工具
	var funcDecls []GeminiFunctionDecl
	for _, tool := range tools {
		if InternalIsWebSearchTool(tool) || InternalIsCodeExecutionTool(tool) {
			continue
		}
		// 跳过无效工具名称
		if strings.TrimSpace(tool.Name) == "" {
			diagnostic("Warning: skipping tool with empty name")
			continue
		}

		var description string
		var inputSchema map[string]any

		// 检查是否为 custom 类型工具 (MCP)
		if tool.Type == "custom" {
			if tool.Custom == nil || tool.Custom.InputSchema == nil {
				diagnostic("[Warning] Skipping invalid custom tool '%s': missing custom spec or input_schema", tool.Name)
				continue
			}
			description = tool.Custom.Description
			inputSchema = tool.Custom.InputSchema

		} else {
			// 标准格式: 从顶层字段获取
			description = tool.Description
			inputSchema = tool.InputSchema
		}

		// 清理 JSON Schema
		// 1. 深度清理 [undefined] 值
		DeepCleanUndefined(inputSchema)
		// 2. 转换为符合 Gemini v1internal 的 schema
		params := CleanJSONSchema(inputSchema)
		// 为 nil schema 提供默认值
		if params == nil {
			params = map[string]any{
				"type":       "object", // lowercase type
				"properties": map[string]any{},
			}
		}

		funcDecls = append(funcDecls, GeminiFunctionDecl{
			Name:        tool.Name,
			Description: description,
			Parameters:  params,
		})
	}

	var declarations []GeminiToolDeclaration
	if len(funcDecls) > 0 {
		declarations = append(declarations, GeminiToolDeclaration{
			FunctionDeclarations: funcDecls,
		})
	}
	if hasWebSearch {
		declarations = append(declarations, GeminiToolDeclaration{
			GoogleSearch: &GeminiGoogleSearch{
				EnhancedContent: &GeminiEnhancedContent{
					ImageSearch: &GeminiImageSearch{
						MaxResultCount: 5,
					},
				},
			},
		})
	}
	if hasCodeExecution {
		declarations = append(declarations, GeminiToolDeclaration{
			CodeExecution: &GeminiCodeExecution{},
		})
	}
	if len(declarations) == 0 {
		return nil, diagnostics
	}

	return declarations, diagnostics
}

func BuildInternalGeminiGenerationConfig(req *ClaudeRequest, options InternalGeminiGenerationOptions) (*GeminiGenerationConfig, []string) {
	diagnostics := []string{}
	diagnostic := func(format string, args ...any) { diagnostics = append(diagnostics, fmt.Sprintf(format, args...)) }
	maxLimit := options.MaxOutputTokens
	config := &GeminiGenerationConfig{
		MaxOutputTokens: options.DefaultOutputTokens, // 默认最大输出
	}

	isReasoning := options.ReasoningModel
	if !isReasoning {
		config.StopSequences = options.StopSequences
	}

	// 如果请求中指定了 MaxTokens，使用请求值
	if req.MaxTokens > 0 {
		config.MaxOutputTokens = req.MaxTokens
	}

	// Thinking 配置
	if req.Thinking != nil && (req.Thinking.Type == "enabled" || req.Thinking.Type == "adaptive") {
		config.ThinkingConfig = &GeminiThinkingConfig{
			IncludeThoughts: true,
		}

		// - thinking.type=enabled：budget_tokens>0 用显式预算
		// - thinking.type=adaptive：在 Antigravity 的高阶 Opus（4.6+）上覆写为 （24576）
		budget := -1
		if req.Thinking.BudgetTokens > 0 {
			budget = req.Thinking.BudgetTokens
		}
		if req.Thinking.Type == "adaptive" && options.AdaptiveThinkingBudget > 0 {
			budget = options.AdaptiveThinkingBudget
		}

		// 正预算需要做上限与 max_tokens 约束；动态预算（-1）直接透传给上游。
		if budget > 0 {
			// gemini-2.5-flash 上限
			if options.ThinkingBudgetLimit > 0 && budget > options.ThinkingBudgetLimit {
				budget = options.ThinkingBudgetLimit
			}

			// 自动修正：max_tokens 必须大于 budget_tokens（Claude 上游要求）
			if adjusted, ok := EnsureMaxTokensGreaterThanBudget(config.MaxOutputTokens, budget, options.BudgetPadding); ok {
				diagnostic("[Antigravity] Auto-adjusted max_tokens from %d to %d (must be > budget_tokens=%d)",
					config.MaxOutputTokens, adjusted, budget)
				config.MaxOutputTokens = adjusted
			}
		}
		config.ThinkingConfig.ThinkingBudget = budget
	}

	if config.MaxOutputTokens > maxLimit {
		config.MaxOutputTokens = maxLimit
	}

	// 其他参数
	if !isReasoning {
		if req.Temperature != nil {
			config.Temperature = req.Temperature
		}
		if req.TopP != nil {
			config.TopP = req.TopP
		}
		if req.TopK != nil {
			config.TopK = req.TopK
		}
	}

	return config, diagnostics
}

// ensureMaxTokensGreaterThanBudget 确保 max_tokens > budget_tokens
// Claude API 要求启用 thinking 时，max_tokens 必须大于 thinking.budget_tokens
// 返回调整后的 maxTokens 和是否进行了调整
func EnsureMaxTokensGreaterThanBudget(maxTokens, budgetTokens, padding int) (int, bool) {
	if budgetTokens > 0 && maxTokens <= budgetTokens {
		return budgetTokens + padding, true
	}
	return maxTokens, false
}

// InternalGeminiGenerationOptions 是平台已确定的生成约束，转换不识别模型名称。
type InternalGeminiGenerationOptions struct {
	DefaultOutputTokens    int
	MaxOutputTokens        int
	ReasoningModel         bool
	StopSequences          []string
	AdaptiveThinkingBudget int
	ThinkingBudgetLimit    int
	BudgetPadding          int
}
