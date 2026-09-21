package service

import (
	"context"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/gin-gonic/gin"
)

// ForwardGemini 转发 Gemini 协议请求
//
// 限流处理流程:
//
//	请求 → antigravityRetryLoop → 预检查(remaining>0? → 切换账号) → 发送上游
//	  ├─ 成功 → 正常返回
//	  └─ 429/503 → handleSmartRetry
//	      ├─ retryDelay >= 7s → 设置模型限流 + 清除粘性绑定 → 切换账号
//	      └─ retryDelay <  7s → 等待后重试 1 次
//	          ├─ 成功 → 正常返回
//	          └─ 失败 → 设置模型限流 + 清除粘性绑定 → 切换账号
type ForwardGeminiOption func(*forwardGeminiOptions)

type forwardGeminiOptions struct {
	groupID     int64
	sessionHash string
}

func WithForwardGeminiSession(groupID int64, sessionHash string) ForwardGeminiOption {
	return func(opts *forwardGeminiOptions) {
		opts.groupID = groupID
		opts.sessionHash = sessionHash
	}
}

func (s *AntigravityGatewayService) ForwardGemini(ctx context.Context, c *gin.Context, account *Account, originalModel string, action string, stream bool, body []byte, isStickySession bool, options ...ForwardGeminiOption) (*forwardcore.MessagesResult, error) {
	start := time.Now()
	opts := forwardGeminiOptions{}
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	adapter := &geminiExecutionAdapter{s: s, c: c, account: account}
	input := forwardcore.GeminiInput{StartedAt: start, Model: originalModel, Action: action, Stream: stream, Body: body, Sticky: isStickySession, GroupID: opts.groupID, SessionHash: opts.sessionHash, AccountID: account.ID, AccountName: account.Name, Platform: account.Platform, TokenAvailable: s.tokenProvider != nil, Prefix: logPrefix(getSessionID(c), account.Name)}
	adapter.prefix = input.Prefix
	result, err := forwardcore.Gemini(ctx, adapter, input)
	return legacyForwardExecutionResult(result), err
}
