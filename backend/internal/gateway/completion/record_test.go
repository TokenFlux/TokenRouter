package completion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

// recordStore 只记录真正结算调用，日志兜底不能再次经过资金端口。
type recordStore struct {
	calls   int
	err     error
	command *billing.UsageBillingCommand
	events  *[]string
}

func (s *recordStore) Apply(ctx context.Context, c *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error) {
	s.calls++
	s.command = c
	*s.events = append(*s.events, "funds")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &billing.UsageBillingApplyResult{Applied: true, BalanceAmountUSD: c.BillableAmountUSD}, s.err
}

type recordWriter struct {
	rows               []*usage.UsageLog
	bestErr, errorSync error
	events             *[]string
	contexts           []error
}

func (w *recordWriter) CreateBestEffort(ctx context.Context, r *usage.UsageLog) error {
	*w.events = append(*w.events, "best")
	w.contexts = append(w.contexts, ctx.Err())
	w.rows = append(w.rows, r)
	return w.bestErr
}

func (w *recordWriter) Create(ctx context.Context, r *usage.UsageLog) (bool, error) {
	*w.events = append(*w.events, "sync")
	w.contexts = append(w.contexts, ctx.Err())
	w.rows = append(w.rows, r)
	return true, w.errorSync
}

type recordEffects struct{ events *[]string }

func (e recordEffects) AccountUsed(int64) { *e.events = append(*e.events, "used") }

func (e recordEffects) InvalidateAuth(context.Context, string) { *e.events = append(*e.events, "auth") }

func (e recordEffects) Settled(SettlementInput, *billing.UsageBillingApplyResult) {
	*e.events = append(*e.events, "effects")
}

type recordModels struct{}

func (recordModels) Candidates(model string, _ ...string) []string { return []string{model} }
func recordFixture(simple bool) (*Recorder, *recordStore, *recordWriter, *Input, *[]string) {
	events := []string{}
	funds := &recordStore{events: &events}
	logs := &recordWriter{events: &events}
	calculator := billing.NewCalculator(nil, billing.CalculatorOptions{DefaultRateMultiplier: 1})
	recorder := NewRecorder(Dependencies{Calculator: calculator, Funds: funds, Models: recordModels{}, Logs: logs, Effects: recordEffects{&events}}, RecorderOptions{Simple: simple, DefaultMultiplier: 1})
	input := &Input{
		Result:    &Result{Model: "claude-sonnet-4-5", Usage: TokenUsage{InputTokens: 10, OutputTokens: 2, CacheReadInputTokens: 3, CacheCreationInputTokens: 1}},
		RequestID: "fixed",
		APIKey:    &KeySnapshot{ID: 2, BillingMode: billing.APIKeyBillingModeBalance, ActorUserID: 8},
		User:      &PayerSnapshot{ID: 1},
		Account:   &AccountSnapshot{ID: 3, RateMultiplier: 1},
	}
	return recorder, funds, logs, input, &events
}

func TestRecordSettlementFailureRetainsUnsettledFact(t *testing.T) {
	for _, openAI := range []bool{false, true} {
		t.Run(map[bool]string{false: "anthropic", true: "openai"}[openAI], func(t *testing.T) {
			core, funds, logs, in, events := recordFixture(false)
			funds.err = errors.New("settlement failed")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := core.Record(ctx, in, openAI)
			require.ErrorIs(t, err, funds.err)
			require.Equal(t, 1, funds.calls)
			require.Equal(t, []string{"funds", "best"}, *events)
			require.Greater(t, logs.rows[0].TotalCost, 0.0)
			require.Zero(t, logs.rows[0].ActualCost)
			require.Equal(t, int64(8), logs.rows[0].UserID)
			require.Equal(t, int64(1), logs.rows[0].BillingUserID)
			require.Equal(t, []error{nil}, logs.contexts)
			if openAI {
				require.Equal(t, 6, logs.rows[0].InputTokens)
			} else {
				require.Equal(t, 10, logs.rows[0].InputTokens)
			}
		})
	}
}

func TestRecordLogFailureDoesNotResettle(t *testing.T) {
	core, funds, logs, in, events := recordFixture(false)
	logs.bestErr = errors.New("queue full")
	logs.errorSync = errors.New("database unavailable")
	require.NoError(t, core.Record(context.Background(), in, true))
	require.Equal(t, 1, funds.calls)
	require.Equal(t, []string{"funds", "effects", "best", "sync"}, *events)
	require.Greater(t, logs.rows[1].ActualCost, 0.0)
}

func TestRecordSimpleOnlyWritesAndUpdatesActivity(t *testing.T) {
	core, funds, _, in, events := recordFixture(true)
	require.NoError(t, core.Record(context.Background(), in, false))
	require.Zero(t, funds.calls)
	require.Equal(t, []string{"best", "used"}, *events)
}

func TestSnapshotIsolatesQueueInputs(t *testing.T) {
	threshold := 2.0
	group := int64(7)
	tier := "priority"
	at := time.Date(2026, 9, 16, 3, 4, 5, 0, time.UTC)
	in := &Input{
		PricingAt:    at,
		APIKey:       &KeySnapshot{GroupID: &group, Group: &GroupSnapshot{Price: &billing.PriceGroup{ModelPricing: []pricing.ModelPricingEntry{{Models: []string{"model"}}}}, AudioPrice: &pricing.AudioPriceConfig{RealtimePerMin: &threshold}}},
		User:         &PayerSnapshot{Notification: &billing.UserSummary{BalanceNotifyThreshold: &threshold}},
		Result:       &Result{ServiceTier: &tier, ImageOutputSizes: []string{"1K"}, ImageSizeBreakdown: map[string]int{"1K": 1}},
		Subscription: &billing.UserSubscription{ID: 1, Plan: &billing.SubscriptionPlan{GroupIDs: []int64{7}, GroupRateMultipliers: map[int64]float64{7: 2}}},
	}
	frozen := Snapshot(in)
	threshold = 99
	group = 8
	tier = "flex"
	in.Result.ImageOutputSizes[0] = "4K"
	in.Result.ImageSizeBreakdown["1K"] = 9
	in.Subscription.Plan.GroupRateMultipliers[7] = 8
	in.APIKey.Group.Price.ModelPricing[0].Models[0] = "other"
	require.Equal(t, at, frozen.PricingAt)
	require.Equal(t, int64(7), *frozen.APIKey.GroupID)
	require.Equal(t, "priority", *frozen.Result.ServiceTier)
	require.Equal(t, 2.0, *frozen.User.Notification.BalanceNotifyThreshold)
	require.Equal(t, 2.0, *frozen.APIKey.Group.AudioPrice.RealtimePerMin)
	require.Equal(t, []string{"1K"}, frozen.Result.ImageOutputSizes)
	require.Equal(t, 1, frozen.Result.ImageSizeBreakdown["1K"])
	require.Equal(t, 2.0, frozen.Subscription.Plan.GroupRateMultipliers[7])
	require.Equal(t, "model", frozen.APIKey.Group.Price.ModelPricing[0].Models[0])
}
