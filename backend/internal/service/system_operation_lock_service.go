package service

import (
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
)

type SystemOperationLock = maintenance.SystemOperationLock
type SystemOperationLockService = maintenance.SystemOperationLockService

var ErrSystemOperationBusy = maintenance.ErrSystemOperationBusy

// NewSystemOperationLockService 兼容旧装配；专用存储不退回无所有者比较的接口。
func NewSystemOperationLockService(repo IdempotencyRepository, cfg IdempotencyConfig) *SystemOperationLockService {
	store, _ := repo.(idempotency.OperationLeaseStore)
	return maintenance.NewSystemOperationLockService(store, maintenance.Options{Log: logging.LegacyPrintf, ProcessingTimeout: cfg.ProcessingTimeout, SystemOperationTTL: cfg.SystemOperationTTL})
}
