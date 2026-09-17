// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package legacybridge

import (
	context "context"
	sql "database/sql"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func BillingMaintenanceLease(ctx context.Context, cache service.LeaderLockCache, db *sql.DB, key, owner string, ttl time.Duration) (func(), bool) {
	return service.AcquireLegacySingletonLease(ctx, cache, db, key, owner, ttl)
}
