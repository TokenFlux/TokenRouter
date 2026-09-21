package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// mimicExecutionAdapter 的 systemRaw 仅兼容原 JSON string/array 输入，不携带业务实体。
type mimicExecutionAdapter struct {
	*messageExecutionAdapter
	systemRaw any
}

func (a *mimicExecutionAdapter) RewriteMimicSystem(body []byte, model, prompt, blocks string) []byte {
	blocks = anthropic.ClaudeOAuthSystemPromptBlocksForModel(model, blocks)
	return anthropic.RewriteSystemForNonClaudeCodeWithPromptBlocks(body, anthropic.NormalizeSystemParam(a.systemRaw), prompt, blocks)
}
func (a *mimicExecutionAdapter) MimicMetadata(ctx context.Context, body []byte) string {
	if a.s.identityService == nil || a.c == nil || a.c.Request == nil {
		return ""
	}
	fp, err := a.s.identityService.GetOrCreateFingerprint(ctx, a.account.ID, a.c.Request.Header)
	if err != nil || fp == nil {
		return ""
	}
	mimic := false
	if a.s.settingService != nil {
		_, mimic, _ = a.s.settingService.Gateway.GetGatewayForwardingSettings(ctx)
	}
	if mimic {
		return ""
	}
	return a.s.buildOAuthMetadataUserIDFromBody(ctx, a.account, fp, body)
}
