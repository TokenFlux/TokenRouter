package httpapi

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/gin-gonic/gin"
)

// resolveOpenAITextProtocolForAttempt 解析当前账号的实际文本协议，并在转发前
// 覆盖 attempt 级端点元数据，避免故障转移后沿用上一账号的端点。
func resolveOpenAITextProtocolForAttempt(
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	preferred accountcore.TextProtocol,
) accountcore.TextProtocol {
	// OAuth 等专用账号始终保留既有 Responses 桥；只有 API Key 账号参与
	// “客户端首选协议 + 路由模式 + 探测状态”的普通文本协议解析。
	protocol := accountcore.TextProtocolResponses
	if account != nil && account.Record.Type == capability.AccountTypeAPIKey {
		protocol = accountcore.ResolveUpstreamTextProtocol(account.Record.Extra, preferred)
	}

	if account != nil && account.Route.Protocol() == protocolcore.ProtocolOpenAIChatCompletions {
		protocol = accountcore.TextProtocolChatCompletions
	}
	if account != nil && account.Route.Protocol() == protocolcore.ProtocolOpenAIResponses {
		protocol = accountcore.TextProtocolResponses
	}
	endpoint := "/v1/responses"
	if protocol == accountcore.TextProtocolChatCompletions {
		endpoint = "/v1/chat/completions"
	}
	SetActualOpenAIUpstreamEndpoint(c, endpoint)
	return protocol
}
