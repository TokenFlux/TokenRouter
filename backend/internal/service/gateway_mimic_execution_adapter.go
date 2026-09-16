package service

import "context"

// mimicExecutionAdapter 的 systemRaw 仅兼容原 JSON string/array 输入，不携带业务实体。
type mimicExecutionAdapter struct {
	*messageExecutionAdapter
	systemRaw any
}

func (a *mimicExecutionAdapter) RewriteMimicSystem(body []byte, model, prompt, blocks string) []byte {
	blocks = claudeOAuthSystemPromptBlocksForModel(model, blocks)
	return rewriteSystemForNonClaudeCodeWithPromptBlocks(body, normalizeSystemParam(a.systemRaw), prompt, blocks)
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
		_, mimic, _ = a.s.settingService.GetGatewayForwardingSettings(ctx)
	}
	if mimic {
		return ""
	}
	return a.s.buildOAuthMetadataUserIDFromBody(ctx, a.account, fp, body)
}
