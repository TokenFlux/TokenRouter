// 公开用量直接接入新用量实现，身份与窗口只提供只读投影。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
)

func providePublicUsage(u *usage.UsageService, k *service.APIKeyService, users *identity.UserService, settings *service.SettingService) *usagehttp.PublicUsageHandler {
	return usagehttp.NewPublicUsageHandler(u, k.APIKeyService, usagehttp.PublicBalanceQuery(func(ctx context.Context, id int64) (*usagehttp.PublicUserBalance, error) {
		v, e := users.GetByID(ctx, id)
		if e != nil {
			return nil, e
		}
		return &usagehttp.PublicUserBalance{Balance: v.Balance}, nil
	}), settings, legacybridge.PublicUsageContext())
}
