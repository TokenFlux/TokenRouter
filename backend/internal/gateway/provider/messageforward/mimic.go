package messageforward

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// mimicAttempt 的 systemRaw 仅兼容原 JSON string/array 输入，不携带业务实体。
type mimicAttempt struct {
	*attempt
	systemRaw any
}

func (a *mimicAttempt) RewriteMimicSystem(body []byte, model, prompt, blocks string) []byte {
	blocks = anthropic.ClaudeOAuthSystemPromptBlocksForModel(model, blocks)
	return anthropic.RewriteSystemForNonClaudeCodeWithPromptBlocks(body, anthropic.NormalizeSystemParam(a.systemRaw), prompt, blocks)
}
func (a *mimicAttempt) MimicMetadata(ctx context.Context, body []byte) string {
	if a.s.dependencies.Fingerprint == nil || !a.c.RequestPresent() {
		return ""
	}
	fp, err := a.s.dependencies.Fingerprint.GetOrCreateFingerprint(ctx, a.account.Record.ID, a.c.RequestHeaders())
	if err != nil || fp == nil {
		return ""
	}
	mimic := false
	if a.s.dependencies.Settings != nil {
		_, mimic, _ = a.s.dependencies.Settings.GetGatewayForwardingSettings(ctx)
	}
	if mimic {
		return ""
	}
	return metadataUserIDFromBody(ctx, a.account, fp, body)
}

// mimic 将兼容协议的请求体交给已有伪装流程，状态与当前转换 attempt 共用。
func (r *Runtime) mimic(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, body []byte, system any, model string) []byte {
	a := newAttempt(r, output, target)
	a.state = state
	adapter := &mimicAttempt{attempt: a, systemRaw: system}
	return forward.Mimic(ctx, adapter, target != nil && target.View().IsOAuth(), body, model)
}
