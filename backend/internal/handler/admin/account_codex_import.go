// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	time "time"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	openai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	gin "github.com/gin-gonic/gin"
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
