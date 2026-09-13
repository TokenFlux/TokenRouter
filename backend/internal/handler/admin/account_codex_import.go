// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	openai "github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
	time "time"
)

// 旧入口只投影依赖，算法、索引和状态都由账号模块持有。
func (h *AccountHandler) ImportCodexSession(c *gin.Context) {
	accounthttp.NewCodexImportHandler(h.legacyCodexImporter()).ImportCodexSession(c)
}
func (h *AccountHandler) legacyCodexImporter() *account.CodexImporter {
	options := legacyCodexImportOptions()
	if h.tokenCacheInvalidator != nil {
		options.Invalidate = func(ctx context.Context, v *account.Record) error {
			return h.tokenCacheInvalidator.InvalidateToken(ctx, service.AccountFromRecord(v))
		}
	}
	return account.NewCodexImporter(legacyCodexAccounts{legacyArchiveAccounts{h.adminService}}, h.legacyArchive(), options)
}
func legacyCodexImportOptions() account.CodexImportOptions {
	return account.CodexImportOptions{Now: time.Now, OAuthClientID: openai.ClientID, ValidatePrivateKey: service.ValidateOpenAIAgentIdentityPrivateKey}
}

type legacyCodexAccounts struct{ legacyArchiveAccounts }

func (s legacyCodexAccounts) UpdateAccount(ctx context.Context, id int64, input *account.UpdateAccountInput) (*account.Record, error) {
	v, err := s.source.UpdateAccount(ctx, id, input)
	return service.AccountRecordView(v), err
}

type CodexSessionImportRequest = account.CodexSessionImportRequest
type CodexSessionImportResult = account.CodexSessionImportResult
type CodexSessionImportItem = account.CodexSessionImportItem
type CodexSessionImportMessage = account.CodexSessionImportMessage

// mergeCodexImportMap 为待迁供应商入口委托账号纯规则。
func mergeCodexImportMap(existing, incoming map[string]any) map[string]any {
	return account.MergeCodexImportMap(existing, incoming)
}

// sanitizeCodexImportCredentialExtras 为待迁供应商入口委托账号纯规则。
func sanitizeCodexImportCredentialExtras(input map[string]any) map[string]any {
	return account.SanitizeCodexImportCredentialExtras(input)
}

// codexTokenFingerprint 为待迁供应商入口委托账号纯规则。
func codexTokenFingerprint(token string) string { return account.CodexTokenFingerprint(token) }
