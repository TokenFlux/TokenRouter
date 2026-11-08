//go:build integration

package billing_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativepg "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	"github.com/TokenFlux/TokenRouter/internal/creative/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// creative_gif_scenario_integration_test.go exercises creative result delivery,
// saved-frame settlement, and billing transactions with PostgreSQL and Redis.
// Real GIF bytes establish actual counts independently of requested frames.
// The scenarios retain original bytes, reconcile wallet and Key amounts, and
// check persisted usage after both excess-frame and reduced-frame results.
//
// A task participant can fail after changing the allowance flag, forcing the
// surrounding capture transaction to undo its wallet, quota, and dedup writes.
// Recovery reloads the same task and output snapshots before completing funds
// and delivery. Repeated capture must reconstruct the committed actual charge
// without touching another task's frozen balance or charging this GIF twice.

// gifCaptureProjection injects failure after the task's capture metadata write.
type gifCaptureProjection struct {
	billingpg.TaskProjection // transaction-bound task persistence delegate
	// rejectCapture fails completion after the delegate writes its flag, allowing
	// the caller to observe rollback of preceding funds and allowance changes.
	rejectCapture bool // reject completed allowance updates
}

// SetAllowanceReserved persists the requested flag before optionally failing
// capture. Reservation always delegates normally, allowing the test to verify
// rollback after wallet and Key updates have already executed in the transaction.
func (p gifCaptureProjection) SetAllowanceReserved(ctx context.Context, reserved bool) error {
	if err := p.TaskProjection.SetAllowanceReserved(ctx, reserved); err != nil {
		return err
	}
	if !reserved && p.rejectCapture {
		return errors.New("injected GIF capture projection failure")
	}
	return nil
}

// TestCreativeGIFActualFramesSettlement accepts GIFs with different requested
// and encoded frame counts, including GIFs returned for PNG requests. It verifies
// durable recovery, original Redis bytes, actual usage records, quota reconciliation,
// and one capture after replay.
func TestCreativeGIFActualFramesSettlement(t *testing.T) {
	rdb := rediscontainer.New(t)
	for _, test := range []struct {
		name           string  // scenario display name
		requested      int     // requested animation frames
		actual         int     // encoded animation frames
		initialBalance float64 // available wallet before reservation
		unitPrice      float64 // base dollars per frame
		balanceRate    float64 // saved wallet price factor
		subscription   float64 // reserved subscription dollar limit
		wantCost       float64 // combined actual charge
		wantBalance    float64 // final available wallet
		rollback       bool    // inject first capture failure
	}{
		{name: "png_request_receives_gif", requested: 0, actual: 9, initialBalance: 100, unitPrice: 4, balanceRate: 1, wantCost: 36, wantBalance: 64, rollback: true},
		{name: "png_request_gif_wallet_debt", requested: 0, actual: 9, initialBalance: 4, unitPrice: 4, balanceRate: 1, wantCost: 36, wantBalance: -32},
		{name: "png_request_one_frame", requested: 0, actual: 1, initialBalance: 100, unitPrice: 4, balanceRate: 1, wantCost: 4, wantBalance: 96},
		{name: "png_request_free_gif", requested: 0, actual: 9, balanceRate: 1},
		{name: "png_request_zero_rate_replay", requested: 0, actual: 9, initialBalance: 100, unitPrice: 4, wantBalance: 100},
		{name: "fewer_frames", requested: 9, actual: 3, initialBalance: 100, unitPrice: 4, balanceRate: 1, wantCost: 12, wantBalance: 88},
		{name: "equal_frames", requested: 3, actual: 3, initialBalance: 100, unitPrice: 4, balanceRate: 1, wantCost: 12, wantBalance: 88},
		{name: "one_returned_frame", requested: 3, actual: 1, initialBalance: 100, unitPrice: 4, balanceRate: 1, wantCost: 4, wantBalance: 96},
		{name: "above_request_frame_limit", requested: 3, actual: 17, initialBalance: 100, unitPrice: 4, balanceRate: 1, wantCost: 68, wantBalance: 32},
		{name: "more_frames_and_rollback", requested: 3, actual: 9, initialBalance: 100, unitPrice: 4, balanceRate: 1, wantCost: 36, wantBalance: 64, rollback: true},
		{name: "more_frames_wallet_debt", requested: 3, actual: 9, initialBalance: 12, unitPrice: 4, balanceRate: 1, wantCost: 36, wantBalance: -24},
		{name: "fractional_frame_price", requested: 3, actual: 9, initialBalance: 100, unitPrice: 0.125000001, balanceRate: 1, wantCost: 1.125000009, wantBalance: 98.87499999},
		{name: "mixed_source_rates", requested: 3, actual: 9, initialBalance: 100, unitPrice: 4, balanceRate: 2, subscription: 4, wantCost: 60, wantBalance: 44},
		{name: "subscription_only_hold", requested: 3, actual: 9, initialBalance: 100, unitPrice: 4, balanceRate: 2, subscription: 6, wantCost: 54, wantBalance: 52},
		{name: "free_gif", requested: 3, actual: 9, balanceRate: 1},
		{name: "zero_rate_historical_capture", requested: 3, actual: 9, initialBalance: 100, unitPrice: 4, wantBalance: 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			client := committedEntitlementClient(t)
			user := mustCreateUser(t, client, &identity.User{Email: uuid.NewString() + "@example.com", Balance: test.initialBalance})
			group := mustCreateGroup(t, client, &routing.Group{Name: "gif-" + uuid.NewString()})
			key := mustCreateApiKey(t, client, &apikey.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-" + uuid.NewString(), Name: "gif"})
			upstream := mustCreateProvider(t, client, &provider.Record{Name: "gif-" + uuid.NewString(), Platform: "openai", Type: "apikey"})
			if test.subscription > 0 {
				plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
					Name: "gif-" + uuid.NewString(), GroupIDs: []int64{group.ID},
					GroupRateMultipliers: map[int64]float64{group.ID: 0.5},
					DailyLimitUSD:        float64Ptr(test.subscription),
				})
				mustCreateSubscription(t, client, &billing.UserSubscription{
					UserID: user.ID, PlanID: plan.ID, DailyLimitUSD: float64Ptr(test.subscription),
				})
			}
			repo := creativepg.NewCreativeRunRepository(client)
			estimate := float64(max(1, test.requested)) * test.unitPrice * test.balanceRate
			params := creative.CreateCreativeRunParams{
				RunID: "crun_" + uuid.NewString(), UserID: user.ID, GroupID: group.ID, APIKeyID: key.ID,
				Model: "gpt-image-2.6-flare", RequestedModel: "gpt-image-2.6-flare", Operation: creative.CreativeOperationGenerate,
				RequestedOutputCount: 1, ImageSize: "1K", ResponseMIMEType: "image/gif", FrameCount: test.requested, FrameDurationMS: 200,
				PromptHash: "hash", RequestFingerprint: "gif-snapshot", EstimatedCost: estimate, HoldAmount: estimate,
				BaseUnitPrice: test.unitPrice, SubscriptionRateMultiplier: 1, BalanceRateMultiplier: test.balanceRate, PlanGroupRateEnabled: true,
			}
			if test.requested == 0 {
				params.ResponseMIMEType = "image/png"
				params.FrameDurationMS = 0
			}
			run, err := repo.CreateCreativeRun(ctx, params)
			require.NoError(t, err)
			rejectCapture := test.rollback
			store := billingpg.NewSettlementStore(integrationDB, timezone.NewCalendar(time.UTC), nil,
				billingpg.TaskProjectionFactories{creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpg.TaskProjection {
					return gifCaptureProjection{TaskProjection: creativepg.NewFundingParticipant(tx, ref.ID), rejectCapture: rejectCapture}
				}})
			funding := creative.Funding{Store: billing.NewFunds(store)}
			require.NoError(t, funding.Reserve(ctx, run))
			otherHold := 0.0
			if test.rollback {
				otherParams := params
				otherParams.RunID = "crun_" + uuid.NewString()
				otherParams.FrameCount = 0
				otherParams.FrameDurationMS = 0
				otherParams.ResponseMIMEType = "image/png"
				otherParams.EstimatedCost = test.unitPrice * test.balanceRate
				otherParams.HoldAmount = otherParams.EstimatedCost
				other, err := repo.CreateCreativeRun(ctx, otherParams)
				require.NoError(t, err)
				require.NoError(t, funding.Reserve(ctx, other))
				otherHold = *other.HoldAmount
			}
			require.NoError(t, repo.MarkCreativeRunRunning(ctx, run.RunID, upstream.ID, time.Now()))
			transient := rediscache.NewCreativeTransientStore(rdb, &rediscache.TransientOptions{TransientTTLSeconds: 60})
			usageStore := usagepg.NewUsageLogRepositoryWithSQL(client, directUsageExecutor{integrationDB}, timezone.NewCalendar(time.UTC))
			results := &creative.Results{
				Repo: repo, Funding: funding, TransientStore: transient, TransientTTL: time.Minute,
				RecordUsage: func(ctx context.Context, record *usage.UsageLog) {
					_, err := usageStore.Create(ctx, record)
					require.NoError(t, err)
				},
			}
			data := creativeGIFBytes(t, test.actual)
			require.NoError(t, results.CreativeDelivery().Record(ctx, run.RunID, upstream.ID, []creative.ProviderOutput{{Index: 0, Success: true, Mime: "image/gif", Bytes: data}}))
			output, err := repo.GetCreativeRunOutput(ctx, run.RunID, 0)
			require.NoError(t, err)
			require.Equal(t, test.actual, output.FrameCount)
			if test.unitPrice > 0 && test.balanceRate == 0 {
				// Earlier workers could capture extra frames at a zero wallet rate.
				// Reload their persisted snapshot: PostgreSQL stores microseconds, while
				// the creation timestamp can retain nanoseconds in the caller's copy.
				legacyRun, err := repo.GetCreativeRunByRunID(ctx, run.RunID)
				require.NoError(t, err)
				legacy, err := creative.BuildHoldCommand(legacyRun, creative.CreativeCaptureRequestID(run.RunID), float64(test.actual)*test.unitPrice)
				require.NoError(t, err)
				_, err = funding.Store.Capture(ctx, legacy)
				require.NoError(t, err)
			}
			if test.rollback {
				// The injected participant failure follows wallet and Key updates. Verify
				// that all financial writes and the capture claim roll back together.
				require.ErrorContains(t, results.SettleRun(ctx, run.RunID), "injected GIF capture")
				var balance, frozen, quota float64
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id=$1`, user.ID).Scan(&balance, &frozen))
				require.Equal(t, test.initialBalance-estimate-otherHold, balance)
				require.Equal(t, estimate+otherHold, frozen)
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used FROM api_keys WHERE id=$1`, key.ID).Scan(&quota))
				require.Equal(t, estimate+otherHold, quota)
				var claims int
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id=$1`, creative.CreativeCaptureRequestID(run.RunID)).Scan(&claims))
				require.Zero(t, claims)
				rejectCapture = false
				// Concurrent recovery uses separate reloaded commands and one capture ID.
				// Wait for both attempts before checking which transaction applied funds.
				applied := make(chan bool, 2)
				failures := make(chan error, 2)
				for range 2 {
					go func() {
						reloaded, err := repo.GetCreativeRunByRunID(ctx, run.RunID)
						if err != nil {
							failures <- err
							applied <- false
							return
						}
						reloaded.ActualFrameCount = output.FrameCount
						result, err := funding.Capture(ctx, reloaded, 1)
						failures <- err
						applied <- result != nil && result.Applied
					}()
				}
				count := 0
				for range 2 {
					err := <-failures
					wasApplied := <-applied
					if err != nil {
						t.Error(err)
					}
					if wasApplied {
						count++
					}
				}
				require.Equal(t, 1, count)
			}
			// Recreate the result service to discard its caller state before recovery.
			// The database's requested controls and actual output count own settlement.
			recovered := *results
			recovered.Repo = creativepg.NewCreativeRunRepository(client)
			require.NoError(t, recovered.SettleRun(ctx, run.RunID))
			require.NoError(t, recovered.SettleRun(ctx, run.RunID))
			restored, err := repo.GetCreativeRunByRunID(ctx, run.RunID)
			require.NoError(t, err)
			require.Equal(t, creative.CreativeRunStatusSucceeded, restored.Status)
			require.Equal(t, test.requested, restored.FrameCount)
			require.NotNil(t, restored.ActualCost)
			require.InDelta(t, test.wantCost, *restored.ActualCost, 1e-10)
			restored.ActualFrameCount = output.FrameCount
			replay, err := funding.Capture(ctx, restored, 1)
			require.NoError(t, err)
			require.False(t, replay.Applied)
			require.InDelta(t, test.wantCost, replay.ActualAmountUSD, 1e-10)
			var balance, frozen, quota, usage5h, usage1d, usage7d float64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id=$1`, user.ID).Scan(&balance, &frozen))
			require.InDelta(t, test.wantBalance-otherHold, balance, 1e-8)
			require.Equal(t, otherHold, frozen)
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h, usage_1d, usage_7d FROM api_keys WHERE id=$1`, key.ID).Scan(&quota, &usage5h, &usage1d, &usage7d))
			// Wallet and Key counters store eight fractional dollars; usage records
			// below retain the more precise calculated charge for billing details.
			wantQuota := billing.QuantizeUsageBillingAmount(test.wantCost + otherHold)
			for _, amount := range []float64{quota, usage5h, usage1d, usage7d} {
				require.InDelta(t, wantQuota, amount, 1e-10)
			}
			if test.subscription > 0 {
				var subscriptionUsage float64
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE user_id=$1`, user.ID).Scan(&subscriptionUsage))
				require.Equal(t, test.subscription, subscriptionUsage)
			}
			var files, frames, count int
			var cost float64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*), MAX(image_count), MAX(image_frame_count), MAX(actual_cost) FROM usage_logs WHERE billing_key=$1`, creative.CreativeSettlementRequestID(run.RunID)).Scan(&count, &files, &frames, &cost))
			require.Equal(t, 1, count)
			require.Equal(t, 1, files)
			require.Equal(t, test.actual, frames)
			require.InDelta(t, test.wantCost, cost, 1e-10)
			storedBytes, err := transient.LoadOutput(ctx, run.RunID, 0)
			require.NoError(t, err)
			require.Equal(t, data, storedBytes)
		})
	}
}

// creativeGIFBytes encodes the requested number of small, valid fixture frames.
// It uses the standard GIF encoder so result parsing observes actual container
// descriptors instead of trusting a supplied count or fabricated metadata.
func creativeGIFBytes(t *testing.T, frames int) []byte {
	t.Helper()
	animation := &gif.GIF{}
	for range frames {
		animation.Image = append(animation.Image, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White}))
		// GIF stores delays in centiseconds; twenty represents 200 milliseconds.
		animation.Delay = append(animation.Delay, 20)
	}
	var buffer bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buffer, animation))
	return buffer.Bytes()
}
