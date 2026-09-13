// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

// 旧断言调用新纯入口；只投影数据，不复制算法。
func (h *AccountHandler) importCodexSessions(ctx context.Context, req CodexSessionImportRequest, entries []codexImportEntry) (CodexSessionImportResult, error) {
	return h.legacyCodexImporter().Import(ctx, req, entries)
}

type codexImportEntry = account.CodexImportEntry
type codexImportAccount = account.CodexImportAccount

func parseCodexSessionImportEntries(req CodexSessionImportRequest) ([]codexImportEntry, error) {
	return account.ParseCodexSessionImportEntries(req)
}
func normalizeCodexImportEntry(entry codexImportEntry) (*codexImportAccount, error) {
	return account.NormalizeCodexImportEntry(entry, legacyCodexImportOptions())
}
func resolveCodexImportExpiry(req CodexSessionImportRequest, item *codexImportAccount) (*int64, *time.Time, *bool, []string, error) {
	return account.ResolveCodexImportExpiry(req, item, time.Now)
}
func buildCodexImportIdentityKeys(accountID, userID, email, accessToken, refreshToken string) []string {
	return account.BuildCodexImportIdentityKeys(accountID, userID, email, accessToken, refreshToken)
}
func buildCodexAgentIdentityKeys(accountID string) []string {
	return account.BuildCodexAgentIdentityKeys(accountID)
}
func buildCodexStoredIdentityKeys(accountID, userID, email, accessToken string) []string {
	return account.BuildCodexStoredIdentityKeys(accountID, userID, email, accessToken)
}

type codexSeenIdentity = account.CodexSeenIdentity

func firstSeenCodexIdentity(seen map[string]codexSeenIdentity, keys []string, userID string) (int, bool) {
	return account.FirstSeenCodexIdentity(seen, keys, userID)
}
func markCodexIdentitySeen(seen map[string]codexSeenIdentity, keys []string, index int, userID string) {
	account.MarkCodexIdentitySeen(seen, keys, index, userID)
}
func mergeCodexImportCredentials(existing, incoming map[string]any, item *codexImportAccount) map[string]any {
	return account.MergeCodexImportCredentials(existing, incoming, item)
}
func codexCredentialString(credentials map[string]any, key string) string {
	return account.CodexCredentialString(credentials, key)
}

type codexAccountIndex struct{ core *account.CodexAccountIndex }

func buildCodexAccountIndex(values []service.Account) *codexAccountIndex {
	return &codexAccountIndex{core: account.BuildCodexAccountIndex(service.AccountRecordsView(values))}
}
func (i *codexAccountIndex) Add(v service.Account) { i.core.Add(*service.AccountRecordView(&v)) }
func (i *codexAccountIndex) Find(keys []string, user string) (*service.Account, string) {
	v, key := i.core.Find(keys, user)
	return service.AccountFromRecord(v), key
}
