package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"log"
	"time"
)

// provideAccountDeferred 固定同一个账号存储与时间轮，构造无定时任务副作用。
func provideAccountDeferred(store *accountpostgres.AccountStore, wheel *timingwheel.Wheel) *account.DeferredService {
	return account.NewDeferredService(store, wheel, account.DeferredOptions{Interval: 10 * time.Second, Now: time.Now, Observe: log.Printf})
}
