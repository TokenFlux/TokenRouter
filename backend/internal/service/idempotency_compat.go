// 本文件仅保留旧入口；S15/S16 随消费者迁移删除。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"time"
)

type IdempotencyRecord = idempotency.IdempotencyRecord
type IdempotencyRepository = idempotency.IdempotencyRepository
type IdempotencyConfig = idempotency.IdempotencyConfig
type IdempotencyExecuteOptions = idempotency.IdempotencyExecuteOptions
type IdempotencyExecuteResult = idempotency.IdempotencyExecuteResult
type IdempotencyCoordinator = idempotency.IdempotencyCoordinator
type IdempotencyCleanupService = idempotency.IdempotencyCleanupService
type IdempotencyMetricsSnapshot = idempotency.IdempotencyMetricsSnapshot

const IdempotencyStatusProcessing = idempotency.IdempotencyStatusProcessing
const IdempotencyStatusSucceeded = idempotency.IdempotencyStatusSucceeded
const IdempotencyStatusFailedRetryable = idempotency.IdempotencyStatusFailedRetryable

var ErrIdempotencyKeyRequired = idempotency.ErrIdempotencyKeyRequired
var ErrIdempotencyKeyInvalid = idempotency.ErrIdempotencyKeyInvalid
var ErrIdempotencyKeyConflict = idempotency.ErrIdempotencyKeyConflict
var ErrIdempotencyInProgress = idempotency.ErrIdempotencyInProgress
var ErrIdempotencyRetryBackoff = idempotency.ErrIdempotencyRetryBackoff
var ErrIdempotencyStoreUnavail = idempotency.ErrIdempotencyStoreUnavail
var ErrIdempotencyInvalidPayload = idempotency.ErrIdempotencyInvalidPayload

func DefaultIdempotencyConfig() IdempotencyConfig { return idempotency.DefaultIdempotencyConfig() }
func SetDefaultIdempotencyCoordinator(svc *IdempotencyCoordinator) {
	idempotency.SetDefaultIdempotencyCoordinator(svc)
}
func DefaultIdempotencyCoordinator() *IdempotencyCoordinator {
	return idempotency.DefaultIdempotencyCoordinator()
}
func DefaultWriteIdempotencyTTL() time.Duration { return idempotency.DefaultWriteIdempotencyTTL() }
func DefaultSystemOperationIdempotencyTTL() time.Duration {
	return idempotency.DefaultSystemOperationIdempotencyTTL()
}
func NewIdempotencyCoordinator(repo IdempotencyRepository, cfg IdempotencyConfig) *IdempotencyCoordinator {
	return idempotency.NewIdempotencyCoordinator(repo, cfg)
}
func NormalizeIdempotencyKey(raw string) (string, error) {
	return idempotency.NormalizeIdempotencyKey(raw)
}
func HashIdempotencyKey(key string) string { return idempotency.HashIdempotencyKey(key) }
func BuildIdempotencyFingerprint(method, route, actorScope string, payload any) (string, error) {
	return idempotency.BuildIdempotencyFingerprint(method, route, actorScope, payload)
}
func RetryAfterSecondsFromError(err error) int { return idempotency.RetryAfterSecondsFromError(err) }
func GetIdempotencyMetricsSnapshot() IdempotencyMetricsSnapshot {
	return idempotency.GetIdempotencyMetricsSnapshot()
}
func RecordIdempotencyStoreUnavailable(endpoint, scope, strategy string) {
	idempotency.RecordIdempotencyStoreUnavailable(endpoint, scope, strategy)
}
func NewIdempotencyCleanupService(repo IdempotencyRepository, cfg *config.Config) *IdempotencyCleanupService {
	opts := idempotency.CleanupOptions{}
	if cfg != nil {
		opts.Interval = time.Duration(cfg.Idempotency.CleanupIntervalSeconds) * time.Second
		opts.Batch = cfg.Idempotency.CleanupBatchSize
	}
	return idempotency.NewIdempotencyCleanupService(repo, opts)
}
