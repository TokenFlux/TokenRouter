package app

import (
	"context"

	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// provideCodexImporter 绑定实际账号管理用例、唯一备份查询能力和当前平台端口。
func provideCodexImporter(admin *account.Admin, archive *account.Archive, invalidator account.TokenCacheInvalidator) *account.CodexImporter {
	options := account.CodexImportOptions{OAuthClientID: openai.ClientID, ValidatePrivateKey: func(value string) error { _, err := openai.ParseAgentIdentityPrivateKey(value); return err }}
	if invalidator != nil {
		options.Invalidate = func(ctx context.Context, value *account.Record) error {
			return invalidator.InvalidateToken(ctx, value)
		}
	}
	options.Now = time.Now
	return account.NewCodexImporter(admin, archive, options)
}
