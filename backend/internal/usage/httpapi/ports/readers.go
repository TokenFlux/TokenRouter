// Package ports 定义用量 HTTP 实际需要的只读协作面，不持有其他领域服务。
package ports

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type KeyReference struct {
	ID, UserID int64
	Name       string
}
type UserReference struct {
	ID        int64
	Email     string
	DeletedAt *time.Time
}
type UserListFilters struct {
	Search         string
	IncludeDeleted bool
}
type KeyReader interface {
	GetByID(context.Context, int64) (*KeyReference, error)
	VerifyOwnership(context.Context, int64, []int64) ([]int64, error)
	SearchAPIKeys(context.Context, int64, string, int) ([]KeyReference, error)
}
type UserReader interface {
	ListUsers(context.Context, int, int, UserListFilters, string, string) ([]UserReference, int64, error)
}
type UserErrors interface {
	ListUserErrorRequests(context.Context, int64, *ops.OpsErrorLogFilter) (*ops.UserErrorRequestList, error)
	GetUserErrorRequestDetail(context.Context, int64, int64) (*ops.UserErrorRequestDetail, error)
}
type Timings interface {
	LookupRequestTimings(context.Context, []string) (map[string]*ops.OpsRequestTiming, error)
}
type Settings interface {
	GetUsageRankingSettings(context.Context) (usage.UsageRankingSettings, error)
	IsUserErrorViewAllowed(context.Context) bool
}

// 函数投影只适配调用结果，不增加查询、缓存或权限规则。
type KeyQueries struct {
	Lookup    func(context.Context, int64) (*KeyReference, error)
	Ownership func(context.Context, int64, []int64) ([]int64, error)
	Search    func(context.Context, int64, string, int) ([]KeyReference, error)
}

func (q KeyQueries) GetByID(ctx context.Context, id int64) (*KeyReference, error) {
	return q.Lookup(ctx, id)
}
func (q KeyQueries) VerifyOwnership(ctx context.Context, id int64, keys []int64) ([]int64, error) {
	return q.Ownership(ctx, id, keys)
}
func (q KeyQueries) SearchAPIKeys(ctx context.Context, id int64, query string, limit int) ([]KeyReference, error) {
	return q.Search(ctx, id, query, limit)
}

type UserQueries func(context.Context, int, int, UserListFilters, string, string) ([]UserReference, int64, error)

func (q UserQueries) ListUsers(ctx context.Context, page, size int, f UserListFilters, sort, order string) ([]UserReference, int64, error) {
	return q(ctx, page, size, f, sort, order)
}
