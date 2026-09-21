//go:build integration

package repository

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

type sqlQueryer = infra.Queryer

func scanSingleRow(ctx context.Context, q sqlQueryer, query string, args []any, dest ...any) error {
	return infra.ScanSingleRow(ctx, q, query, args, dest...)
}
func newUsageLogRepositoryWithSQL(client *dbent.Client, q infra.Executor) *usagepg.Store {
	return usagepg.NewUsageLogRepositoryWithSQL(client, q, timezone.NewCalendar(time.Local))
}
