package service

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"log"
)

// SubscriptionMaintenanceQueue 保留旧入口；当前无生产调用，S15 清理。
type SubscriptionMaintenanceQueue = billing.SubscriptionMaintenanceQueue

func NewSubscriptionMaintenanceQueue(workers, size int) *SubscriptionMaintenanceQueue {
	return billing.NewSubscriptionMaintenanceQueue(workers, size, log.Printf)
}
