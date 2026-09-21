//go:build integration

package account_test

import (
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/stretchr/testify/suite"
)

type ProxyExpirySuite struct {
	suite.Suite
	ctx  context.Context
	tx   *dbent.Tx
	repo *postgres.ProxyStore
}

func (s *ProxyExpirySuite) SetupTest() {
	s.ctx = context.Background()
	s.tx = testEntTx(s.T())
	s.repo = newProxyStoreContract(s.tx.Client(), s.tx)
}
func TestProxyExpirySuite(t *testing.T) { suite.Run(t, new(ProxyExpirySuite)) }

func (s *ProxyExpirySuite) mkProxy(name, mode string, expiresAt *time.Time, backupID *int64) int64 {
	p := &egress.Proxy{Name: name, Protocol: "http", Host: "127.0.0.1", Port: 8080,
		Status: billing.StatusActive, FallbackMode: mode, ExpiryWarnDays: 7,
		ExpiresAt: expiresAt, BackupProxyID: backupID}
	s.Require().NoError(s.repo.Create(s.ctx, p))
	return p.ID
}

func (s *ProxyExpirySuite) mkAccountWithProxy(proxyID int64) int64 {
	var id int64
	err := postgresinfra.ScanSingleRow(s.ctx, s.tx, `
		INSERT INTO accounts (name, platform, type, credentials, extra, status, proxy_id, created_at, updated_at)
		VALUES ($1,'claude','api','{}','{}','active',$2,NOW(),NOW()) RETURNING id`,
		[]any{"acc-" + time.Now().Format("150405.000000"), proxyID}, &id)
	s.Require().NoError(err)
	return id
}

func (s *ProxyExpirySuite) accountProxyID(id int64) *int64 {
	var pid *int64
	err := postgresinfra.ScanSingleRow(s.ctx, s.tx, `SELECT proxy_id FROM accounts WHERE id=$1`, []any{id}, &pid)
	s.Require().NoError(err)
	return pid
}

func (s *ProxyExpirySuite) TestSweep_DirectMode() {
	past := time.Now().Add(-time.Hour)
	pid := s.mkProxy("p-direct", egress.FallbackModeDirect, &past, nil)
	aid := s.mkAccountWithProxy(pid)

	changed, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(changed, int64(1))

	got, _ := s.repo.GetByID(s.ctx, pid)
	s.Require().Equal(billing.StatusExpired, got.Status)
	s.Require().Nil(s.accountProxyID(aid))
	var origin *int64
	err = postgresinfra.ScanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{aid}, &origin)
	s.Require().NoError(err)
	s.Require().NotNil(origin)
	s.Require().Equal(pid, *origin)
}

func (s *ProxyExpirySuite) TestSweep_EnqueuesChangedAccountIDsWithoutFullRebuild() {
	past := time.Now().Add(-time.Hour)
	firstProxyID := s.mkProxy("p-bulk-first", egress.FallbackModeDirect, &past, nil)
	secondProxyID := s.mkProxy("p-bulk-second", egress.FallbackModeDirect, &past, nil)
	firstAccountID := s.mkAccountWithProxy(firstProxyID)
	secondAccountID := s.mkAccountWithProxy(secondProxyID)

	changed, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().EqualValues(2, changed)

	var payloadRaw []byte
	err = postgresinfra.ScanSingleRow(s.ctx, s.tx, `
		SELECT payload
		FROM scheduler_outbox
		WHERE event_type=$1
		ORDER BY id DESC
		LIMIT 1`, []any{scheduler.SchedulerOutboxEventAccountBulkChanged}, &payloadRaw)
	s.Require().NoError(err)

	var payload struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	s.Require().NoError(json.Unmarshal(payloadRaw, &payload))
	s.Require().Equal([]int64{firstAccountID, secondAccountID}, payload.AccountIDs)

	var fullRebuildCount int
	err = postgresinfra.ScanSingleRow(s.ctx, s.tx, `
		SELECT COUNT(*)
		FROM scheduler_outbox
		WHERE event_type=$1`, []any{scheduler.SchedulerOutboxEventFullRebuild}, &fullRebuildCount)
	s.Require().NoError(err)
	s.Require().Zero(fullRebuildCount)
}

func (s *ProxyExpirySuite) TestSweep_ProxyMode_Healthy() {
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-time.Hour)
	backup := s.mkProxy("p-backup", egress.FallbackModeNone, &future, nil)
	pid := s.mkProxy("p-main", egress.FallbackModeProxy, &past, &backup)
	aid := s.mkAccountWithProxy(pid)

	_, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	s.Require().Equal(backup, *s.accountProxyID(aid))
	var origin *int64
	err = postgresinfra.ScanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{aid}, &origin)
	s.Require().NoError(err)
	s.Require().NotNil(origin)
	s.Require().Equal(pid, *origin)
}

func (s *ProxyExpirySuite) TestSweep_NoneMode_KeepsAccount() {
	past := time.Now().Add(-time.Hour)
	pid := s.mkProxy("p-none", egress.FallbackModeNone, &past, nil)
	aid := s.mkAccountWithProxy(pid)

	_, err := s.repo.SweepExpiredProxies(s.ctx, time.Now())
	s.Require().NoError(err)
	got, _ := s.repo.GetByID(s.ctx, pid)
	s.Require().Equal(billing.StatusExpired, got.Status)
	s.Require().Equal(pid, *s.accountProxyID(aid))
	var origin *int64
	err = postgresinfra.ScanSingleRow(s.ctx, s.tx, `SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1`, []any{aid}, &origin)
	s.Require().NoError(err)
	s.Require().Nil(origin)
}
