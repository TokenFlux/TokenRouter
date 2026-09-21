package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/google/uuid"
)

// provideAccountAdmin 绑定唯一管理用例与原平台执行端口，构造不运行后台任务。
func provideAccountAdmin(store *accountpostgres.AccountStore, usage *billingpostgres.AccountUsageStore, blocker account.RuntimeUnblocker, privacy *account.PrivacyService, groups *routingpostgres.GroupStore, proxies *egresspostgres.ProxyStore, tasks *lifecycle.Tasks, upstream httpclient.UpstreamTransport, tls *provider.TLSProfiles) *account.Admin {
	return account.NewAdmin(store, account.AdminOptions{ShadowModels: accountprovider.DefaultSparkShadowModels, Duplicates: store, Quotas: usage, RuntimeBlocker: blocker, Privacy: privacy, Groups: accountGroupReferences{groups}, Proxies: proxies, Creation: account.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString}, Credentials: accountprovider.CreateCredentialHooks(upstream, tls), Background: tasks.Go, Error: slog.Error})
}

type accountGroupReferences struct{ store *routingpostgres.GroupStore }

func (g accountGroupReferences) DefaultGroup(ctx context.Context, platform string) (*account.GroupReference, error) {
	value, err := routing.FindPlatformDefaultGroup(ctx, g.store, platform)
	if value == nil {
		return nil, err
	}
	return &account.GroupReference{ID: value.ID, Name: value.Name, Platform: value.Platform, RequireOAuthOnly: value.RequireOAuthOnly}, err
}
func (g accountGroupReferences) GetGroup(ctx context.Context, id int64) (*account.GroupReference, error) {
	value, err := g.store.GetByID(ctx, id)
	if value == nil {
		return nil, err
	}
	return &account.GroupReference{ID: value.ID, Name: value.Name, Platform: value.Platform, RequireOAuthOnly: value.RequireOAuthOnly}, err
}

func (g accountGroupReferences) ActiveGroups(ctx context.Context, platform string) ([]account.GroupReference, error) {
	rows, err := g.store.ListActiveByPlatform(ctx, platform)
	if rows == nil {
		return nil, err
	}
	out := make([]account.GroupReference, len(rows))
	for i, v := range rows {
		out[i] = account.GroupReference{ID: v.ID, Name: v.Name, Platform: v.Platform, RequireOAuthOnly: v.RequireOAuthOnly}
	}
	return out, err
}
func (g accountGroupReferences) ValidateGroups(ctx context.Context, ids []int64) error {
	return routing.ValidateGroupIDs(ctx, g.store, ids)
}
