package bootstrap

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	rp "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
)

// EnsureSimpleModeDefaultGroups 只装配初始化能力，不触发普通分组管理副作用。
func EnsureSimpleModeDefaultGroups(ctx context.Context, client *dbent.Client) error {
	return rp.EnsureSimpleModeDefaultGroups(ctx, client)
}
