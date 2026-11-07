//go:build integration

package billing_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativepg "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// task_funding_outcome_integration_test.go verifies creative outcome persistence and funding against PostgreSQL.
//
// Creative provider outcomes connect a run and its output state to a settlement
// outbox record. A PostgreSQL trigger rejects that outbox insert so the fixture
// can check that the run remains running, its output remains pending, and its
// outcome timestamp remains absent.
//
// Removing the trigger permits identical outcome metadata to be recorded twice
// while leaving one settlement operation. Completion then records lost delivery
// and actual cost. Separate funding cases delegate the task reservation write
// before injecting failure, inspect payer funds and request claims, and retry
// through a working task writer. Native capture replay checks an unapplied
// second result with one persisted charge.

// failingProjection injects reservation failure after its delegate persists
// task funding metadata.
type failingProjection struct {
	// TaskProjection uses the funding transaction for its writes;
	// SaveReservation injects failure after that delegate succeeds so its
	// caller can exercise rollback.
	billingpg.TaskProjection
}

// TestProviderOutcomeRollbackAndDeliveryLost rejects a creative outbox insert
// through a PostgreSQL trigger. Outcome state rolls back, retry creates one
// settle record, and completion records lost delivery with actual cost.
func TestProviderOutcomeRollbackAndDeliveryLost(t *testing.T) {
	ctx := context.Background()
	client := committedEntitlementClient(t)
	user := mustCreateUser(t, client, &identity.User{Email: "test-" + uuid.NewString() + "@example.com", Balance: 10})
	group := mustCreateGroup(t, client, &routing.Group{Name: "test-outcome-" + uuid.NewString()})
	key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: "sk-test-" + uuid.NewString(), Name: "test"})
	repo := creativepg.NewCreativeRunRepository(client)
	id := "crun_" + uuid.NewString()
	_, err := repo.CreateCreativeRun(ctx, creative.CreateCreativeRunParams{RunID: id, UserID: user.ID, GroupID: group.ID, APIKeyID: key.ID, Model: "image", RequestedModel: "image", Operation: creative.CreativeOperationGenerate, RequestedOutputCount: 1, ImageSize: "1K", ResponseMIMEType: "image/png", PromptHash: "hash", RequestFingerprint: "fingerprint"})
	require.NoError(t, err)
	require.NoError(t, repo.MarkCreativeRunRunning(ctx, id, 0, time.Now()))
	outcomes, ok := repo.(creative.ProviderOutcomeStore)
	require.True(t, ok)
	// Reject the outbox insert after outcome writes have begun so the
	// subsequent raw task reads can detect effects that escaped the
	// enclosing PostgreSQL transaction.
	_, err = integrationDB.ExecContext(ctx, `CREATE FUNCTION test_reject_outcome() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test outcome audit failure'; END $$;
 CREATE TRIGGER test_reject_outcome BEFORE INSERT ON creative_run_outbox FOR EACH ROW EXECUTE FUNCTION test_reject_outcome();`)
	require.NoError(t, err)
	cleanup := func() {
		_, err := integrationDB.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS test_reject_outcome ON creative_run_outbox; DROP FUNCTION IF EXISTS test_reject_outcome();`)
		require.NoError(t, err)
	}
	// Retain trigger removal for failing assertions, then also remove it
	// before the successful retry; IF EXISTS makes the final cleanup safe
	// to repeat.
	t.Cleanup(cleanup)
	now := time.Now()
	expires := now.Add(time.Minute)
	mime := "image/png"
	size := int64(3)
	code := creative.OutputDeliveryPending
	metadata := []creative.CreativeRunOutput{{OutputIndex: 0, Status: creative.CreativeRunOutputStatusSucceeded, MimeType: &mime, ByteSize: &size, TransientExpiresAt: &expires, ErrorCode: &code}}
	err = outcomes.RecordProviderOutcome(ctx, id, 0, metadata, now)
	require.ErrorContains(t, err, "test outcome audit failure")
	run, err := repo.GetCreativeRunByRunID(ctx, id)
	require.NoError(t, err)
	require.Nil(t, run.ProviderResultRecordedAt)
	require.Equal(t, creative.CreativeRunStatusRunning, run.Status)
	output, err := repo.GetCreativeRunOutput(ctx, id, 0)
	require.NoError(t, err)
	require.Equal(t, creative.CreativeRunOutputStatusPending, output.Status)
	// Remove the injected outbox failure before replaying identical
	// provider metadata; the two successful calls should leave a single
	// settlement outbox operation.
	cleanup()
	require.NoError(t, outcomes.RecordProviderOutcome(ctx, id, 0, metadata, now))
	require.NoError(t, outcomes.RecordProviderOutcome(ctx, id, 0, metadata, now))
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM creative_run_outbox WHERE run_id=$1 AND operation='settle'`, id).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, outcomes.CompleteProviderOutcome(ctx, id, 0.2, true, time.Now()))
	run, err = repo.GetCreativeRunByRunID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, creative.CreativeRunStatusResultLost, run.Status)
	require.NotNil(t, run.ActualCost)
	// Allow a small floating-point gap after reading persisted actual cost.
	require.InDelta(t, 0.2, *run.ActualCost, 1e-10)
}

// SaveReservation delegates task reservation metadata persistence and returns
// an injected error after successful writing. Delegate failures return
// directly, letting the funding transaction exercise rollback after the real
// task write.
func (p failingProjection) SaveReservation(ctx context.Context, balance float64, alloc []billing.BillingAllocation, hold, estimated float64) error {
	if err := p.TaskProjection.SaveReservation(ctx, balance, alloc, hold, estimated); err != nil {
		return err
	}
	return errors.New("test projection failure")
}

// TestTaskFundingProjectionRollback fails after task reservation metadata has
// been written in PostgreSQL. Payer balance, frozen funds, and request claims
// remain unchanged, and retry through a working writer applies.
func TestTaskFundingProjectionRollback(t *testing.T) {
	ctx := context.Background()
	client := committedEntitlementClient(t)
	user := mustCreateUser(t, client, &identity.User{Email: "test-funds-" + uuid.NewString() + "@example.com", Balance: 10})
	group := mustCreateGroup(t, client, &routing.Group{Name: "test-funds-" + uuid.NewString()})
	key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: "sk-test-funds-" + uuid.NewString(), Name: "test"})
	repo := creativepg.NewCreativeRunRepository(client)
	id := "crun_" + uuid.NewString()
	_, err := repo.CreateCreativeRun(ctx, creative.CreateCreativeRunParams{RunID: id, UserID: user.ID, GroupID: group.ID, APIKeyID: key.ID, Model: "image", RequestedModel: "image", Operation: creative.CreativeOperationGenerate, RequestedOutputCount: 1, ImageSize: "1K", ResponseMIMEType: "image/png", PromptHash: "hash", RequestFingerprint: "fingerprint", EstimatedCost: 0.2})
	require.NoError(t, err)
	store := billingpg.NewSettlementStore(integrationDB, timezone.NewCalendar(time.Local),

		nil, billingpg.TaskProjectionFactories{creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpg.TaskProjection {
			return failingProjection{creativepg.NewFundingParticipant(tx, ref.ID)}
		}})
	cmd := &billing.TaskFundsCommand{Task: creative.FundingReference(id), UserID: user.ID, APIKeyID: key.ID, RequestID: "creative_hold:" + id, HoldAmount: 0.2}
	// Let the task writer persist before its injected error aborts
	// reservation, then inspect payer balances and the request claim before
	// a working retry.
	_, err = store.Reserve(ctx, cmd)
	require.ErrorContains(t, err, "test projection failure")
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance,COALESCE(frozen_balance,0) FROM users WHERE id=$1`, user.ID).Scan(&balance, &frozen))
	require.Equal(t, 10.0, balance)
	require.Zero(t, frozen)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id=$1`, cmd.RequestID).Scan(&count))
	require.Zero(t, count)
	good := billingpg.NewSettlementStore(integrationDB, timezone.NewCalendar(time.Local),

		nil, billingpg.TaskProjectionFactories{creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpg.TaskProjection {
			return creativepg.NewFundingParticipant(tx, ref.ID)
		}})
	funds := billing.NewFunds(good)
	result, err := funds.Reserve(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied, fmt.Sprint(result))
}

// TestNativeCreativeFundingReplay reserves creative funding and captures the
// same request twice. The first applies, replay does not, balance is charged
// once, frozen funds clear, and one claim remains.
func TestNativeCreativeFundingReplay(t *testing.T) {
	ctx := context.Background()
	client := committedEntitlementClient(t)
	user := mustCreateUser(t, client, &identity.User{Email: "test-native-" + uuid.NewString() + "@example.com", Balance: 10})
	group := mustCreateGroup(t, client, &routing.Group{Name: "test-native-" + uuid.NewString()})
	key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: "sk-test-native-" + uuid.NewString(), Name: "native"})
	repo := creativepg.NewCreativeRunRepository(client)
	run, err := repo.CreateCreativeRun(ctx, creative.CreateCreativeRunParams{RunID: "crun_" + uuid.NewString(), UserID: user.ID, GroupID: group.ID, APIKeyID: key.ID, Model: "image", RequestedModel: "image", Operation: creative.CreativeOperationGenerate, RequestedOutputCount: 1, ImageSize: "1K", ResponseMIMEType: "image/png", PromptHash: "hash", RequestFingerprint: "fingerprint", EstimatedCost: 0.2, HoldAmount: 0.2, BaseUnitPrice: 0.2, SubscriptionRateMultiplier: 1, BalanceRateMultiplier: 1, PlanGroupRateEnabled: true})
	require.NoError(t, err)
	store := billingpg.NewSettlementStore(integrationDB, timezone.NewCalendar(time.Local),

		nil, billingpg.TaskProjectionFactories{creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpg.TaskProjection {
			return creativepg.NewFundingParticipant(tx, ref.ID)
		}})
	funding := creative.Funding{Store: billing.NewFunds(store)}
	require.NoError(t, funding.Reserve(ctx, run))
	first, err := funding.Capture(ctx, run, 1)
	require.NoError(t, err)
	require.True(t, first.Applied)
	// Keep the same task capture identity on retry so the stored request
	// claim can reject repeated charging while final payer balances expose
	// any duplicate effect.
	replay, err := funding.Capture(ctx, run, 1)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance,COALESCE(frozen_balance,0) FROM users WHERE id=$1`, user.ID).Scan(&balance, &frozen))
	// Compare the decimal wallet result with its floating-point scan value.
	require.InDelta(t, 9.8, balance, 1e-10)
	require.Zero(t, frozen)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id=$1`, creative.CreativeCaptureRequestID(run.RunID)).Scan(&count))
	require.Equal(t, 1, count)
}

// TestCreativeGIFFrameFunding reserves requested frames and captures actual frames.
// Reloaded PostgreSQL metadata preserves animation controls and provider output
// counts, while repeated capture charges once and releases the unused reservation.
func TestCreativeGIFFrameFunding(t *testing.T) {
	ctx := context.Background()
	client := committedEntitlementClient(t)
	user := mustCreateUser(t, client, &identity.User{Email: "test-gif-" + uuid.NewString() + "@example.com", Balance: 10})
	group := mustCreateGroup(t, client, &routing.Group{Name: "test-gif-" + uuid.NewString()})
	key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, Key: "sk-test-gif-" + uuid.NewString(), Name: "gif"})
	repo := creativepg.NewCreativeRunRepository(client)
	run, err := repo.CreateCreativeRun(ctx, creative.CreateCreativeRunParams{RunID: "crun_" + uuid.NewString(), UserID: user.ID, GroupID: group.ID, APIKeyID: key.ID, Model: "gpt-image-2.6-flare", RequestedModel: "gpt-image-2.6-flare", Operation: creative.CreativeOperationGenerate, RequestedOutputCount: 1, ImageSize: "1K", ResponseMIMEType: "image/gif", FrameCount: 4, FrameDurationMS: 200, PromptHash: "hash", RequestFingerprint: "gif-fingerprint", EstimatedCost: 1, HoldAmount: 1, BaseUnitPrice: 0.25, SubscriptionRateMultiplier: 1, BalanceRateMultiplier: 1, PlanGroupRateEnabled: true})
	require.NoError(t, err)
	store := billingpg.NewSettlementStore(integrationDB, timezone.NewCalendar(time.Local), nil, billingpg.TaskProjectionFactories{creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpg.TaskProjection {
		return creativepg.NewFundingParticipant(tx, ref.ID)
	}})
	funding := creative.Funding{Store: billing.NewFunds(store)}
	require.NoError(t, funding.Reserve(ctx, run))
	require.NoError(t, repo.MarkCreativeRunRunning(ctx, run.RunID, 0, time.Now()))
	mime := "image/gif"
	outcomes, ok := repo.(creative.ProviderOutcomeStore)
	require.True(t, ok)
	require.NoError(t, outcomes.RecordProviderOutcome(ctx, run.RunID, 0, []creative.CreativeRunOutput{{OutputIndex: 0, Status: creative.CreativeRunOutputStatusSucceeded, MimeType: &mime, FrameCount: 2}}, time.Now()))
	restored, err := repo.GetCreativeRunByRunID(ctx, run.RunID)
	require.NoError(t, err)
	require.Equal(t, 4, restored.FrameCount)
	require.Equal(t, 200, restored.FrameDurationMS)
	output, err := repo.GetCreativeRunOutput(ctx, run.RunID, 0)
	require.NoError(t, err)
	require.Equal(t, 2, output.FrameCount)
	restored.ActualFrameCount = output.FrameCount
	first, err := funding.Capture(ctx, restored, 1)
	require.NoError(t, err)
	require.True(t, first.Applied)
	replay, err := funding.Capture(ctx, restored, 1)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance,COALESCE(frozen_balance,0) FROM users WHERE id=$1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 9.5, balance, 1e-10)
	require.Zero(t, frozen)
}
