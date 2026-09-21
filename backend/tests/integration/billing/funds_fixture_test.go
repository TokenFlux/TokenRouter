//go:build integration

package billing_test

import (
	"database/sql"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchpostgres "github.com/TokenFlux/TokenRouter/internal/batchimage/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativepostgres "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
)

// newTaskFundsFixture 复用原数据库连接、时区及两个任务参与工厂，不建立另一套资金规则。
func newTaskFundsFixture(db *sql.DB) *billing.Funds {
	return billing.NewFunds(newSettlementFixture(db))
}

// newSettlementFixture 直接构造普通结算与任务资金共用的原生事务存储。
func newSettlementFixture(db *sql.DB) *billingpostgres.SettlementStore {
	store := billingpostgres.NewSettlementStore(db, timezone.NewCalendar(time.Local), schedulerpostgres.EnqueueAccountQuotaChangedInTx, billingpostgres.TaskProjectionFactories{
		creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpostgres.TaskProjection {
			return creativepostgres.NewFundingParticipant(tx, ref.ID)
		},
		batchimage.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpostgres.TaskProjection {
			return batchpostgres.NewFundingParticipant(tx, ref.ID)
		},
	})
	return store
}
func testEntClient(t *testing.T) *dbent.Client { t.Helper(); return integrationEntClient }
