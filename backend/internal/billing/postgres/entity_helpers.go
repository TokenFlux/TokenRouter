// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package postgres

import (
	context "context"
	sql "database/sql"
	errors "errors"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// clientFromContext 从 context 中获取事务 client，如果不存在则返回默认 client。
//
// 这个辅助函数支持 repository 方法在事务上下文中工作：
// - 如果 context 中存在事务（通过 ent.NewTxContext 设置），返回事务的 client
// - 否则返回传入的默认 client
//
// 使用示例：
//
//	func (r *someRepo) SomeMethod(ctx context.Context) error {
//	    client := clientFromContext(ctx, r.client)
//	    return client.SomeEntity.Create().Save(ctx)
//	}
func clientFromContext(ctx context.Context, defaultClient *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return defaultClient
}

// translatePersistenceError 将数据库层错误翻译为业务层错误。
//
// 这是 Repository 层的核心错误处理函数，确保数据库细节不会泄露到业务层。
// 通过统一的错误翻译，业务层可以使用语义明确的错误类型（如 ErrUserNotFound）
// 而不是依赖于特定数据库的错误（如 sql.ErrNoRows）。
//
// 参数：
//   - err: 原始数据库错误
//   - notFound: 当记录不存在时返回的业务错误（可为 nil 表示不处理）
//   - conflict: 当违反唯一约束时返回的业务错误（可为 nil 表示不处理）
//
// 返回：
//   - 翻译后的业务错误，或原始错误（如果不匹配任何规则）
//
// 示例：
//
//	err := translatePersistenceError(dbErr, service.ErrUserNotFound, service.ErrEmailExists)
func translatePersistenceError(err error, notFound, conflict *apperror.ApplicationError) error {
	if err == nil {
		return nil
	}

	// 兼容 Ent ORM 和标准 database/sql 的 NotFound 行为。
	// Ent 使用自定义的 NotFoundError，而标准库使用 sql.ErrNoRows。
	// 这里同时处理两种情况，保持业务错误映射一致。
	if notFound != nil && (errors.Is(err, sql.ErrNoRows) || dbent.IsNotFound(err)) {
		return notFound.WithCause(err)
	}

	// 处理唯一约束冲突（如邮箱已存在、名称重复等）
	if conflict != nil && postgresinfra.IsUniqueConstraintViolation(err) {
		return conflict.WithCause(err)
	}

	// 未匹配任何规则，返回原始错误
	return err
}

func uniquePositiveInt64s(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sqlExecutor 只用于存储适配层内部，不暴露给业务核心。
type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// userSummaryFromEntity 保留原权益 eager-load 的浅层用户投影与空值。
func userSummaryFromEntity(u *dbent.User) *billing.UserSummary {
	if u == nil {
		return nil
	}
	out := &billing.UserSummary{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		LastActiveAt:               u.LastActiveAt,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		TotalRecharged:             u.TotalRecharged,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt, RPMLimit: u.RpmLimit}
	if u.BalanceNotifyExtraEmails != "" && u.BalanceNotifyExtraEmails != "[]" {
		out.BalanceNotifyExtraEmails = billing.ParseNotifyEmails(u.BalanceNotifyExtraEmails)
	}
	return out
}

func paginationResultFromTotal(total int64, params pagination.PaginationParams) *pagination.PaginationResult {
	return pagination.ResultFromTotal(total, params)
}

// isUniqueConstraintViolation 复用 PostgreSQL 技术层的约束错误判断。
func isUniqueConstraintViolation(err error) bool {
	return postgresinfra.IsUniqueConstraintViolation(err)
}
