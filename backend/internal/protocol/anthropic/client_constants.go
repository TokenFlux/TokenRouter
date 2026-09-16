package anthropic

// ClaudeCodeBillingHeaderPrefix 是协议报文中的计费归因文本前缀，不包含平台选择策略。
const ClaudeCodeBillingHeaderPrefix = "x-anthropic-billing-header"

// ClaudeCodeEntrypointMarker 保留入口归因字段的既有 wire 名称。
const ClaudeCodeEntrypointMarker = "cc_entrypoint="
