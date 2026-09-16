package legacybridge

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// CodexImportPlatformOptions 只转接 S09 尚未迁移的私钥校验和缓存失效，不保留导入规则。
func CodexImportPlatformOptions(invalidator service.TokenCacheInvalidator) account.CodexImportOptions {
	options := account.CodexImportOptions{OAuthClientID: openai.ClientID, ValidatePrivateKey: service.ValidateOpenAIAgentIdentityPrivateKey}
	if invalidator != nil {
		options.Invalidate = func(ctx context.Context, v *account.Record) error {
			return invalidator.InvalidateToken(ctx, service.AccountFromRecord(v))
		}
	}
	return options
}
