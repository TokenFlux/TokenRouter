package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// provideGrokHealth 与 OpenAI 快照写入共享节流器，与选号共享运行阻断及模型冷却。
func provideGrokHealth(store *accountpostgres.AccountStore, health *accountprovider.UpstreamHealth, blocks *account.RuntimeBlockState, models *account.ModelTransientState) *accountprovider.GrokHealth {
	return &accountprovider.GrokHealth{
		Store: store, Health: health, Runtime: blocks, ModelTransient: models,
		Throttle: account.NewWriteThrottle(30 * time.Second),
		NormalizeModel: func(value *account.Record, model string) string {
			return (provider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
		},
	}
}
