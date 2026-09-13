package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"time"
)

// provideCodexImporter 绑定实际账号管理用例、唯一备份查询能力和当前平台端口。
func provideCodexImporter(admin *account.Admin, archive *account.Archive, invalidator service.TokenCacheInvalidator) *account.CodexImporter {
	options := legacybridge.CodexImportPlatformOptions(invalidator)
	options.Now = time.Now
	return account.NewCodexImporter(admin, archive, options)
}
