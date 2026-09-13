// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	"time"
)

// provideGroupCapacity 直接读取唯一账号存储，复用原并发/会话/RPM 实例和动态设置读取时机。
func provideGroupCapacity(accounts *accountpostgres.AccountStore, groups *routingpostgres.GroupStore, concurrency *service.ConcurrencyService, sessions service.SessionLimitCache, rpm service.RPMCache, settings service.OpenAIQuotaAutoPauseSettingsReader) *routing.CapacityService {
	return routing.NewCapacityService(capacityAccounts{Store: accounts, Settings: legacybridge.CapacitySettings(settings)}, groups, concurrency, sessions, rpm)
}

// capacityAccounts 只把已有存储行投影给路由；不持有账号缓存或执行供应商规则。
type capacityAccounts struct {
	Store    *accountpostgres.AccountStore
	Settings func(context.Context) account.QuotaAutoPauseSettings
}

func (r capacityAccounts) ListSchedulableByGroupID(ctx context.Context, id int64) ([]account.CapacitySnapshot, error) {
	values, err := r.Store.ListSchedulableByGroupID(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	settings := r.Settings(ctx)
	out := make([]account.CapacitySnapshot, len(values))
	for i, v := range values {
		out[i] = account.ProjectObservedCapacity(account.GroupAccountCapacityRow{AccountID: v.ID, Platform: v.Platform, Concurrency: v.Concurrency, Extra: v.Extra, SessionWindowStart: v.SessionWindowStart, SessionWindowEnd: v.SessionWindowEnd}, settings, time.Now())
	}
	return out, nil
}
func (r capacityAccounts) ListSchedulableCapacityByGroupIDs(ctx context.Context, ids []int64) ([]routing.CapacityAccountRow, error) {
	values, err := r.Store.ListSchedulableCapacityByGroupIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	settings := r.Settings(ctx)
	out := make([]routing.CapacityAccountRow, len(values))
	for i, v := range values {
		out[i] = routing.CapacityAccountRow{GroupID: v.GroupID, Account: account.ProjectObservedCapacity(v, settings, time.Now())}
	}
	return out, nil
}
