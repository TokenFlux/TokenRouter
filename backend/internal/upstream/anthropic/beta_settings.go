// Beta 配置值保留原 JSON 与默认规则顺序。
package anthropic

// Beta Policy 策略常量
const (
	BetaPolicyActionPass   = "pass"   // 透传，不做任何处理
	BetaPolicyActionFilter = "filter" // 过滤，从 beta header 中移除该 token
	BetaPolicyActionBlock  = "block"  // 拦截，直接返回错误

	BetaPolicyScopeAll     = "all"     // 所有账号类型
	BetaPolicyScopeOAuth   = "oauth"   // 仅 OAuth 账号
	BetaPolicyScopeAPIKey  = "apikey"  // 仅 API Key 账号
	BetaPolicyScopeBedrock = "bedrock" // 仅 AWS Bedrock 账号
)

// BetaPolicyRule 单条 Beta 策略规则
type BetaPolicyRule struct {
	BetaToken            string   `json:"beta_token"`                       // beta token 值
	Action               string   `json:"action"`                           // "pass" | "filter" | "block"
	Scope                string   `json:"scope"`                            // "all" | "oauth" | "apikey" | "bedrock"
	ErrorMessage         string   `json:"error_message,omitempty"`          // 自定义错误消息 (action=block 时生效)
	ModelWhitelist       []string `json:"model_whitelist,omitempty"`        // 模型匹配模式列表（为空=对所有模型生效）
	FallbackAction       string   `json:"fallback_action,omitempty"`        // 未匹配白名单的模型的处理方式
	FallbackErrorMessage string   `json:"fallback_error_message,omitempty"` // 未匹配白名单时的自定义错误消息 (fallback_action=block 时生效)
}

// BetaPolicySettings Beta 策略配置
type BetaPolicySettings struct {
	Rules []BetaPolicyRule `json:"rules"`
}

// DefaultBetaPolicySettings 返回默认的 Beta 策略配置
//
// context-1m-2025-08-07 的默认策略：
//   - 仅 claude-sonnet-5 及后续版本（如 claude-sonnet-5-*）在上游默认支持 1M 上下文。
//   - Sonnet 4.x 及以下、Opus、Haiku 上游都不支持该 beta，透传上去会被上游 400 或降级。
//   - 因此默认对 sonnet-5 系列放行、其余全部过滤，与上游能力保持一致。
//
// 白名单需要覆盖每个上游路径的模型 ID 变形：
//   - 直连 Anthropic API：模型保持客户端原样，如 "claude-sonnet-5"、日期后缀或 thinking 后缀。
//   - Vertex AI：normalizeVertexAnthropicModelID 会把 "-YYYYMMDD" 后缀转成 "@YYYYMMDD"。
//   - AWS Bedrock：ResolveBedrockModelID 会输出带跨区域前缀的模型 ID。
//
// 白名单只支持精确匹配和末尾通配符；这里用精确、"-*"、"@*"、"-v*" 等有分隔符的前缀，
// 避免误放行未来可能出现的 "claude-sonnet-50" 或 "claude-sonnet-5.1" 等意外命名。
func DefaultBetaPolicySettings() *BetaPolicySettings {
	return &BetaPolicySettings{
		Rules: []BetaPolicyRule{
			{
				BetaToken: "fast-mode-2026-02-01",
				Action:    BetaPolicyActionFilter,
				Scope:     BetaPolicyScopeAll,
			},
			{
				BetaToken: "context-1m-2025-08-07",
				Action:    BetaPolicyActionPass,
				Scope:     BetaPolicyScopeAll,
				ModelWhitelist: []string{
					// 直连 Anthropic API（客户端请求 model 原样）
					"claude-sonnet-5",
					"claude-sonnet-5-*",
					// Vertex AI 走 normalizeVertexAnthropicModelID 后为 "@YYYYMMDD" 格式
					"claude-sonnet-5@*",
					// AWS Bedrock cross-region inference profile
					// 当前模型 ID 不带版本后缀，精确项不能只由 -* 或 -v* 覆盖。
					"us.anthropic.claude-sonnet-5",
					"us.anthropic.claude-sonnet-5-v*",
					"us.anthropic.claude-sonnet-5-*",
					"eu.anthropic.claude-sonnet-5",
					"eu.anthropic.claude-sonnet-5-v*",
					"eu.anthropic.claude-sonnet-5-*",
					"apac.anthropic.claude-sonnet-5-v*",
					"apac.anthropic.claude-sonnet-5-*",
					"jp.anthropic.claude-sonnet-5-v*",
					"jp.anthropic.claude-sonnet-5-*",
					"au.anthropic.claude-sonnet-5",
					"au.anthropic.claude-sonnet-5-v*",
					"au.anthropic.claude-sonnet-5-*",
					"us-gov.anthropic.claude-sonnet-5-v*",
					"us-gov.anthropic.claude-sonnet-5-*",
					"global.anthropic.claude-sonnet-5",
					"global.anthropic.claude-sonnet-5-v*",
					"global.anthropic.claude-sonnet-5-*",
					// AWS Bedrock 无 cross-region 前缀
					"anthropic.claude-sonnet-5",
					"anthropic.claude-sonnet-5-v*",
					"anthropic.claude-sonnet-5-*",
				},
				FallbackAction: BetaPolicyActionFilter,
			},
		},
	}
}
