//go:build unit

package httpapi

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"time"
)

// quotaTestUsers 只替换用户存在性读取，不依赖旧 AdminService。
type quotaTestUsers struct{ getUserErr error }

func newStubAdminService() *quotaTestUsers { return &quotaTestUsers{} }
func (u *quotaTestUsers) GetByID(_ context.Context, id int64) (*billing.UserSummary, error) {
	return &billing.UserSummary{ID: id}, u.getUserErr
}
func newTestQuotaHandler(repo billing.UserPlatformQuotaRepository, cache billing.BillingCache, users *quotaTestUsers) *QuotaHandler {
	if users == nil {
		users = newStubAdminService()
	}
	service := billing.NewPlatformQuotas(repo, cache, users, billing.NewQuotaCoordinator(), time.Now, nil)
	return NewQuotaHandler(service, timezone.NewCalendar(timezone.Location()), time.Now)
}
