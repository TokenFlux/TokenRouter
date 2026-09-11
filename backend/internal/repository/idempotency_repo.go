package repository

import (
	"database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	idempotencypostgres "github.com/TokenFlux/TokenRouter/internal/idempotency/postgres"
)

// NewIdempotencyRepository 兼容旧图和集成测试，S15/S16 清理。
func NewIdempotencyRepository(_ *dbent.Client, db *sql.DB) idempotency.IdempotencyRepository {
	return idempotencypostgres.NewIdempotencyRepository(db)
}
