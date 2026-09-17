package bootstrap

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	ip "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
)

// ensureSimpleModeAdminConcurrency 保留原初始化顺序及升级标记。
func ensureSimpleModeAdminConcurrency(ctx context.Context, client *dbent.Client) error {
	return ip.EnsureSimpleModeAdminConcurrency(ctx, client)
}
