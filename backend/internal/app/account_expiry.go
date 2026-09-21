package app

import (
	"log"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
)

// provideAccountExpiry 为原一分钟周期注入同一账号存储，启动由维护生命周期登记。
func provideAccountExpiry(store *accountpostgres.AccountStore) *account.ExpiryService {
	return account.NewExpiryService(store, account.ExpiryOptions{Interval: time.Minute, Now: time.Now, Observe: log.Printf})
}
