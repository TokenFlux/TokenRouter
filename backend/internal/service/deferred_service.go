package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"log"
	"time"
)

// DeferredService 保留旧调用形状，队列和算法只在 account 中实现。
type DeferredService = account.DeferredService

func NewDeferredService(repo AccountRepository, wheel *TimingWheelService, interval time.Duration) *DeferredService {
	return account.NewDeferredService(repo, wheel, account.DeferredOptions{Interval: interval, Now: time.Now, Observe: log.Printf})
}
