package handler

import (
	"github.com/BrandonVee/TokenRouter/internal/service"
	"go.uber.org/zap"
)

// openAIPassthroughFailoverState 记录本次账号尝试是否经过 OpenAI 透传账号。
// 一旦经过透传，后续切换到非透传账号时必须清理上游私有的加密 reasoning。
type openAIPassthroughFailoverState struct {
	passthroughSeen bool
}

// deriveOpenAIForwardAttemptBody 从不可变的 canonical 请求体派生当前账号的尝试请求体。
// 只有在已经尝试过透传账号、且当前账号不是透传账号时，才删除带 encrypted_content 的
// reasoning 项；同类重试和之后的非透传账号会持续使用清理后的派生体，canonical 本身不变。
func (h *OpenAIGatewayHandler) deriveOpenAIForwardAttemptBody(
	reqLog *zap.Logger,
	canonicalBody []byte,
	account *service.Account,
	state *openAIPassthroughFailoverState,
) []byte {
	currentPassthrough := account.IsOpenAIPassthroughEnabled()
	if currentPassthrough {
		state.passthroughSeen = true
		return canonicalBody
	}
	if !state.passthroughSeen {
		return canonicalBody
	}

	sanitized, changed, err := service.SanitizeOpenAICrossModeFailoverReasoning(canonicalBody)
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("openai.failover_cross_mode_reasoning_sanitize_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
		}
		return canonicalBody
	}
	if !changed {
		return canonicalBody
	}
	if reqLog != nil {
		reqLog.Info("openai.failover_cross_mode_reasoning_stripped",
			zap.Int64("account_id", account.ID),
			zap.Bool("account_passthrough", currentPassthrough),
			zap.Bool("passthrough_seen", true),
		)
	}
	return sanitized
}
