package httpapi

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// 两个显式 HTTP 状态分别在原时点覆盖，沿用 Gin 的同步发布，不引入可变共享状态盒。
type codexIdentityState struct{ source *account.Record }
type codexFingerprintState struct{ ids *openai.FingerprintIDs }

const codexIdentityStateKey = "openai_codex_account_identity_source"
const codexFingerprintStateKey = "codex_fingerprint_ids"

// PrepareCodexIdentity 每个所选 attempt 重读影子资格，覆盖上次身份；命名空间仍延迟计算。
func PrepareCodexIdentity(ctx context.Context, c *gin.Context, reader provider.ExecutionAccountReader, value *provider.ExecutionAccount) (*account.Record, error) {
	source := value
	if value != nil && value.View().IsShadow() {
		resolved, err := provider.CredentialAccount(ctx, reader, value)
		if err != nil {
			return nil, err
		}
		source = resolved
	}
	record := source.View()
	if c != nil {
		c.Set(codexIdentityStateKey, codexIdentityState{source: record})
	}
	return record, nil
}
func CodexIdentityRecord(c *gin.Context, fallback *account.Record) *account.Record {
	if c != nil {
		if value, ok := c.Get(codexIdentityStateKey); ok {
			if state, ok := value.(codexIdentityState); ok && state.source != nil {
				return state.source
			}
		}
	}
	return fallback
}

// StageCodexFingerprintIDs 无条件覆盖，含 nil，避免 failover 沿用旧账号的收敛结果。
func StageCodexFingerprintIDs(c *gin.Context, ids *openai.FingerprintIDs) {
	if c != nil {
		c.Set(codexFingerprintStateKey, codexFingerprintState{ids: ids})
	}
}
func StagedCodexFingerprintIDs(c *gin.Context, value *account.Record) *openai.FingerprintIDs {
	if c == nil || value == nil || !value.UsesOpenAICodexProtocol() {
		return nil
	}
	stored, ok := c.Get(codexFingerprintStateKey)
	if !ok {
		return nil
	}
	state, ok := stored.(codexFingerprintState)
	if !ok || state.ids == nil {
		return nil
	}
	ids := state.ids
	if ids.AccountID != value.ID && (value.ParentAccountID == nil || *value.ParentAccountID != ids.AccountID) {
		return nil
	}
	return ids
}
func ApplyStagedCodexFingerprintHeaders(c *gin.Context, value *account.Record, h http.Header) {
	openai.ApplyCodexFingerprintHeaders(h, StagedCodexFingerprintIDs(c, value))
}
func ApplyStagedCodexFingerprintClientMetadata(c *gin.Context, value *account.Record, body map[string]any) bool {
	return openai.ApplyCodexFingerprintClientMetadata(body, StagedCodexFingerprintIDs(c, value))
}
