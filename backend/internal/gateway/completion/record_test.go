package completion

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// recordStore counts settlement calls to detect duplicate settlement during log retries.
type recordStore struct {
	calls   int                          // financial apply invocation count
	err     error                        // configured settlement failure
	command *billing.UsageBillingCommand // last supplied command
	events  *[]string                    // shared serial event trace
}

// recordWriter retains attempted row pointers and write outcomes for ordering
// assertions. rows, contexts, and events are updated serially by the tests; events
// shares the same trace used by the funds and effects fixtures.
type recordWriter struct {
	rows []*usage.UsageLog // retained attempted row pointers
	// bestErr: best-effort configured error. errorSync: synchronous configured error.
	bestErr, errorSync error
	events             *[]string // shared write event trace
	contexts           []error   // observed write context errors
}

// recordEffects appends committed-effect observations to the shared serial test trace.
type recordEffects struct {
	events *[]string // shared committed event trace
}

// recordModels selects the supplied primary model for recorder pricing assertions.
type recordModels struct{}

// recordPriceCatalog supplies known prices for settlement and log-order tests.
type recordPriceCatalog struct{}

// recordTTLStats retains usage supplied to differential cache-lifetime pricing.
type recordTTLStats struct {
	tokens pricing.UsageTokens // observed disjoint pricing buckets
}

// recordFundingReader traces preliminary subscription and balance-rate reads for
// the same payer/group pair. Tests use one serial trace shared with settlement
// to detect duplicate reads or a change in their order.
type recordFundingReader struct {
	events *[]string // shared serial lookup trace
	// subscription is returned with subscriptionErr so callers can demonstrate
	// that a failed preliminary read discards even nonnil subscription evidence.
	subscription    *billing.UserSubscription // returned preliminary eligibility evidence
	subscriptionErr error                     // configured subscription lookup failure
	rate            float64                   // configured balance user multiplier
}

// Apply counts and retains one financial command before returning the configured
// fixture result. A cancelled context returns its error; otherwise the synthetic
// result carries Applied=true and the command's balance amount alongside the configured error.
func (s *recordStore) Apply(ctx context.Context, c *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error) {
	s.calls++
	s.command = c
	*s.events = append(*s.events, "funds")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &billing.UsageBillingApplyResult{Applied: true, BalanceAmountUSD: c.BillableAmountUSD}, s.err
}

// CreateBestEffort records the event, current context error, and supplied row
// pointer before returning its configured queue error. This serial fixture performs
// no persistence and lets tests inspect recorder ordering and retry inputs.
func (w *recordWriter) CreateBestEffort(ctx context.Context, r *usage.UsageLog) error {
	*w.events = append(*w.events, "best")
	w.contexts = append(w.contexts, ctx.Err())
	w.rows = append(w.rows, r)
	return w.bestErr
}

// Create records the synchronous event, context error, and supplied row pointer.
// It returns true with the configured error, allowing tests to verify that a failed
// analysis retry follows the funds result without another Apply invocation.
func (w *recordWriter) Create(ctx context.Context, r *usage.UsageLog) (bool, error) {
	*w.events = append(*w.events, "sync")
	w.contexts = append(w.contexts, ctx.Err())
	w.rows = append(w.rows, r)
	return true, w.errorSync
}

// ProviderUsed appends the provider-activity event to the serial shared trace.
func (e recordEffects) ProviderUsed(int64) { *e.events = append(*e.events, "used") }

// InvalidateAuth appends the authentication-expiry event to the serial shared trace.
func (e recordEffects) InvalidateAuth(context.Context, string) { *e.events = append(*e.events, "auth") }

// Settled appends the committed-effects event to the serial shared trace.
func (e recordEffects) Settled(SettlementInput, *billing.UsageBillingApplyResult) {
	*e.events = append(*e.events, "effects")
}

// Candidates returns the primary model as the recorder test pricing candidate.
func (recordModels) Candidates(model string, _ ...string) []string { return []string{model} }

// recordFixture builds serial funds, analysis, and effects spies around a fixed
// native usage input. The returned event slice is shared by those spies; callers
// configure failures and inspect completed recording calls through the retained objects.
func recordFixture() (*Recorder, *recordStore, *recordWriter, *Input, *[]string) {
	events := []string{}
	funds := &recordStore{events: &events}
	logs := &recordWriter{events: &events}
	calculator := billing.NewCalculator(recordPriceCatalog{}, billing.CalculatorOptions{})
	recorder := NewRecorder(Dependencies{Calculator: calculator, Funds: funds, Models: recordModels{}, Logs: logs, Effects: recordEffects{&events}}, RecorderOptions{DefaultMultiplier: 1})
	input := &Input{
		Result:    &Result{Model: "claude-sonnet-4", Usage: TokenUsage{InputTokens: 10, OutputTokens: 2, CacheReadInputTokens: 3, CacheCreationInputTokens: 1}},
		RequestID: "fixed",
		APIKey:    &KeySnapshot{ID: 2, BillingMode: billing.APIKeyBillingModeBalance, ActorUserID: 8},
		User:      &PayerSnapshot{ID: 1},
		Provider:  &ProviderSnapshot{ID: 3, RateMultiplier: 1},
	}
	return recorder, funds, logs, input, &events
}

// TestRecordSettlementFailureRetainsUnsettledFact records native and OpenAI usage
// from a cancelled source while funds returns an error. It checks one funds call,
// actor/payer attribution, protocol-specific input buckets, and a zero-ActualCost
// row after settlement.
func TestRecordSettlementFailureRetainsUnsettledFact(t *testing.T) {
	for _, openAI := range []bool{false, true} {
		t.Run(map[bool]string{false: "anthropic", true: "openai"}[openAI], func(t *testing.T) {
			core, funds, logs, in, events := recordFixture()
			funds.err = errors.New("settlement failed")
			// Cancel before recording to distinguish the live detached funds/write budgets
			// from the request context that has already ended in this failure scenario.
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

// TestRecordLogFailureDoesNotResettle makes both analysis write attempts fail
// after an applied funds result. It checks the funds/effects/write event order,
// one financial invocation, and a retained positive charge in the attempted retry row.
func TestRecordLogFailureDoesNotResettle(t *testing.T) {
	core, funds, logs, in, events := recordFixture()
	logs.bestErr = errors.New("queue full")
	logs.errorSync = errors.New("database unavailable")
	require.NoError(t, core.Record(context.Background(), in, true))
	require.Equal(t, 1, funds.calls)
	require.Equal(t, []string{"funds", "effects", "best", "sync"}, *events)
	require.Greater(t, logs.rows[1].ActualCost, 0.0)
}

// TestSnapshotIsolatesQueueInputs captures mutable scalar pointers, image
// collections, notification data, and plan rates before changing their sources.
// It checks the submitted values and fixed pricing instant retained by the snapshot.
func TestSnapshotIsolatesQueueInputs(t *testing.T) {
	threshold := 2.0
	group := int64(7)
	tier := "priority"
	at := time.Date(2026, 9, 16, 3, 4, 5, 0, time.UTC)
	in := &Input{
		PricingAt:    at,
		APIKey:       &KeySnapshot{GroupID: &group, Group: &GroupSnapshot{AudioPrice: &pricing.AudioPriceConfig{RealtimePerMin: &threshold}}},
		User:         &PayerSnapshot{Notification: &billing.UserSummary{BalanceNotifyThreshold: &threshold}},
		Result:       &Result{ServiceTier: &tier, ImageOutputSizes: []string{"1K"}, ImageSizeBreakdown: map[string]int{"1K": 1}},
		Subscription: &billing.UserSubscription{ID: 1, Plan: &billing.SubscriptionPlan{GroupIDs: []int64{7}, GroupRateMultipliers: map[int64]float64{7: 2}}},
	}
	// Mutate shared scalar fixtures, image collections, and the plan-rate map after
	// Snapshot to expose storage sharing in the fields asserted below.
	frozen := Snapshot(in)
	threshold = 99
	group = 8
	tier = "flex"
	in.Result.ImageOutputSizes[0] = "4K"
	in.Result.ImageSizeBreakdown["1K"] = 9
	in.Subscription.Plan.GroupRateMultipliers[7] = 8
	require.Equal(t, at, frozen.PricingAt)
	require.Equal(t, int64(7), *frozen.APIKey.GroupID)
	require.Equal(t, "priority", *frozen.Result.ServiceTier)
	require.Equal(t, 2.0, *frozen.User.Notification.BalanceNotifyThreshold)
	require.Equal(t, 2.0, *frozen.APIKey.Group.AudioPrice.RealtimePerMin)
	require.Equal(t, []string{"1K"}, frozen.Result.ImageOutputSizes)
	require.Equal(t, 1, frozen.Result.ImageSizeBreakdown["1K"])
	require.Equal(t, 2.0, frozen.Subscription.Plan.GroupRateMultipliers[7])
}

// GetModelPricing returns fixed input/output token prices for claude-sonnet-4.
// Other names return nil, giving ordering tests a stable priced model and a
// clear missing-price result without consulting a live catalogue.
func (recordPriceCatalog) GetModelPricing(model string) *pricing.CatalogModelPricing {
	if model != "claude-sonnet-4" {
		return nil
	}
	return &pricing.CatalogModelPricing{InputCostPerToken: 3e-6, OutputCostPerToken: 15e-6}
}

// ForceUpdate accepts the catalogue refresh interface while leaving fixed fixture prices unchanged.
func (recordPriceCatalog) ForceUpdate() error { return nil }

// TestRecordCyberRetainsFailureFactAndDoesNotResettleLogFailure records Cyber
// classification with either failed funds or failed analysis writes. It checks one
// financial call, retained rows, zero charge on settlement failure, and the unchanged source flag.
func TestRecordCyberRetainsFailureFactAndDoesNotResettleLogFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		core, funds, logs, in, _ := recordFixture()
		if failed {
			funds.err = errors.New("settlement failed")
		} else {
			logs.bestErr = errors.New("queue full")
			logs.errorSync = errors.New("write failed")
		}
		core.RecordCyber(context.Background(), in)
		require.Equal(t, 1, funds.calls)
		require.NotEmpty(t, logs.rows)
		for _, row := range logs.rows {
			require.Equal(t, RequestTypeCyberBlocked, row.RequestType)
			if failed {
				require.Zero(t, row.ActualCost)
			}
		}
		require.False(t, in.CyberBlocked)
	}
}

// TestRecordCyberPreservesZeroUsage supplies an eligible model with empty measured
// usage to the Cyber recorder. It checks a zero financial command, a retained
// zero-cost analysis row, and the Cyber request classification.
func TestRecordCyberPreservesZeroUsage(t *testing.T) {
	core, funds, logs, in, _ := recordFixture()
	in.Result.Usage = TokenUsage{}
	core.RecordCyber(context.Background(), in)
	require.Equal(t, 1, funds.calls)
	require.Zero(t, funds.command.BillableAmountUSD)
	require.NotEmpty(t, logs.rows)
	require.Zero(t, logs.rows[0].ActualCost)
	require.Equal(t, RequestTypeCyberBlocked, logs.rows[0].RequestType)
}

// ResolveProviderStats captures token evidence and applies distinct fixture TTL
// rates. A returned cost pointer selects provider statistics for settlement;
// calls run serially so tests can inspect the retained buckets after recording.
func (s *recordTTLStats) ResolveProviderStats(_ context.Context, input billing.ProviderStatsCostInput) *float64 {
	s.tokens = input.Tokens
	cost := pricing.ComputeCacheCreationCost(&pricing.ModelPricing{SupportsCacheBreakdown: true, CacheCreation5mPrice: 1e-6, CacheCreation1hPrice: 2e-6}, input.Tokens, 0, 1)
	return &cost
}

// TestRecordProviderStatisticsRetainCacheLifetimes submits a native Messages
// observation with separate cache-write lifetimes to the recorder. Provider
// statistics must price those disjoint buckets, and the persisted row must
// retain the lifetimes with their distinct TTL cost.
func TestRecordProviderStatisticsRetainCacheLifetimes(t *testing.T) {
	recorder, _, logs, input, _ := recordFixture()
	stats := &recordTTLStats{}
	recorder.stats = stats
	groupID := int64(1)
	input.APIKey.GroupID = &groupID
	input.Result.NativeUsage = true
	input.Result.Usage = TokenUsage{InputTokens: 100, OutputTokens: 10, CacheReadInputTokens: 30, CacheCreationInputTokens: 12, CacheCreation5mTokens: 5, CacheCreation1hTokens: 7}
	require.NoError(t, recorder.Record(t.Context(), input, true))
	require.Equal(t, pricing.UsageTokens{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 30, CacheCreationTokens: 12, CacheCreation5mTokens: 5, CacheCreation1hTokens: 7}, stats.tokens)
	require.Len(t, logs.rows, 1)
	row := logs.rows[0]
	require.Equal(t, 100, row.InputTokens)
	require.Equal(t, 5, row.CacheCreation5mTokens)
	require.Equal(t, 7, row.CacheCreation1hTokens)
	require.NotNil(t, row.ProviderStatsCost)
	// Five tokens at one and seven at two microdollars cost nineteen microdollars.
	require.InDelta(t, 19e-6, *row.ProviderStatsCost, 1e-12)
}

// ResolveUsableSubscriptionForGroup traces the payer and group before returning
// configured eligibility evidence. Tests can supply both a subscription and an
// error to verify how the public recorder handles a failed preliminary read.
func (r *recordFundingReader) ResolveUsableSubscriptionForGroup(_ context.Context, userID, groupID int64) (*billing.UserSubscription, error) {
	*r.events = append(*r.events, fmt.Sprintf("subscription(%d,%d)", userID, groupID))
	return r.subscription, r.subscriptionErr
}

// Resolve traces the payer, group, and supplied group fallback before returning
// the configured user multiplier. The shared trace lets tests observe whether
// subscription selection suppresses or precedes this balance lookup.
func (r *recordFundingReader) Resolve(_ context.Context, userID, groupID int64, fallback float64) float64 {
	*r.events = append(*r.events, fmt.Sprintf("rate(%d,%d,%g)", userID, groupID, fallback))
	return r.rate
}

// TestRecordPreliminaryFunding characterizes both protocol entrypoints with
// captured or inferred subscriptions and missing or failed readers. It protects
// preliminary prices, command factors, captured funding mode, and lookup order.
func TestRecordPreliminaryFunding(t *testing.T) {
	groupID := int64(7)
	subscriptionID := int64(99)
	planSubscription := &billing.UserSubscription{
		ID: subscriptionID,
		Plan: &billing.SubscriptionPlan{
			GroupIDs:             []int64{groupID},
			GroupRateMultipliers: map[int64]float64{groupID: 0.5},
		},
	}
	groupSubscription := &billing.UserSubscription{
		ID: subscriptionID,
		Plan: &billing.SubscriptionPlan{
			GroupIDs: []int64{groupID},
		},
	}
	excludedSubscription := &billing.UserSubscription{
		ID: subscriptionID,
		Plan: &billing.SubscriptionPlan{
			GroupIDs: []int64{8},
		},
	}
	lookupErr := errors.New("subscription lookup failed")
	settlementErr := errors.New("settlement rejected")
	cases := []struct {
		name               string                    // descriptive funding fixture name
		mode               string                    // captured key funding mode
		current            *billing.UserSubscription // captured preliminary subscription evidence
		resolved           *billing.UserSubscription // reader supplied subscription evidence
		resolveErr         error                     // preliminary eligibility lookup failure
		omitGroup          bool                      // missing captured group snapshot
		omitGroupID        bool                      // missing selected group identifier
		omitRates          bool                      // absent balance override reader
		omitSubscriptions  bool                      // absent automatic eligibility reader
		settlementFails    bool                      // rejected financial command fixture
		wantBase           float64                   // expected preliminary pricing factor
		wantSubscription   float64                   // expected subscription allocation factor
		wantBalance        float64                   // expected balance allocation factor
		wantSubscriptionID *int64                    // expected preliminary subscription identifier
		wantReads          []string                  // expected ordered preliminary lookups
	}{
		{
			name: "system default without group", omitGroup: true, omitGroupID: true,
			wantBase: 1.25, wantSubscription: 1.25, wantBalance: 1.25,
		},
		{
			name: "group snapshot without group ID", omitGroupID: true,
			wantBase: 1.25, wantSubscription: 1.25, wantBalance: 1.25,
		},
		{
			name: "inferred subscription without group snapshot", omitGroup: true, resolved: planSubscription,
			wantBase: 1.25, wantSubscription: 1.25, wantBalance: 1.25,
			wantSubscriptionID: &subscriptionID, wantReads: []string{"subscription(1,7)"},
		},
		{
			name: "balance ignores captured subscription", mode: billing.APIKeyBillingModeBalance, current: planSubscription,
			wantBase: 1.8, wantSubscription: 1.4, wantBalance: 1.8,
			wantReads: []string{"rate(1,7,1.4)"},
		},
		{
			name:     "automatic balance override",
			wantBase: 1.8, wantSubscription: 1.4, wantBalance: 1.8,
			wantReads: []string{"subscription(1,7)", "rate(1,7,1.4)"},
		},
		{
			name: "captured subscription plan rate", current: planSubscription,
			wantBase: 0.5, wantSubscription: 1.4, wantBalance: 1.4, wantSubscriptionID: &subscriptionID,
		},
		{
			name: "inferred subscription plan rate", resolved: planSubscription,
			wantBase: 0.5, wantSubscription: 1.4, wantBalance: 1.4,
			wantSubscriptionID: &subscriptionID, wantReads: []string{"subscription(1,7)"},
		},
		{
			name: "locked subscription retains captured plan", mode: billing.APIKeyBillingModeSubscription,
			current: planSubscription, resolved: excludedSubscription, settlementFails: true,
			wantBase: 0.5, wantSubscription: 1.4, wantBalance: 1.4, wantSubscriptionID: &subscriptionID,
		},
		{
			name: "locked missing subscription does not infer", mode: billing.APIKeyBillingModeSubscription, resolved: planSubscription,
			wantBase: 1.8, wantSubscription: 1.4, wantBalance: 1.8,
			wantReads: []string{"rate(1,7,1.4)"},
		},
		{
			name: "covered subscription uses group rate", current: groupSubscription,
			wantBase: 1.4, wantSubscription: 1.4, wantBalance: 1.4, wantSubscriptionID: &subscriptionID,
		},
		{
			name: "excluded subscription keeps system default", current: excludedSubscription,
			wantBase: 1.25, wantSubscription: 1.4, wantBalance: 1.4, wantSubscriptionID: &subscriptionID,
		},
		{
			name: "lookup error discards returned subscription", resolved: planSubscription, resolveErr: lookupErr,
			wantBase: 1.8, wantSubscription: 1.4, wantBalance: 1.8,
			wantReads: []string{"subscription(1,7)", "rate(1,7,1.4)"},
		},
		{
			name: "missing rate reader uses group rate", omitRates: true,
			wantBase: 1.4, wantSubscription: 1.4, wantBalance: 1.4,
			wantReads: []string{"subscription(1,7)"},
		},
		{
			name: "missing subscription reader uses balance", omitSubscriptions: true,
			wantBase: 1.8, wantSubscription: 1.4, wantBalance: 1.8,
			wantReads: []string{"rate(1,7,1.4)"},
		},
	}
	for _, protocol := range []string{"anthropic", "openai"} {
		t.Run(protocol, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					recorder, funds, logs, input, events := recordFixture()
					recorder.defaultMultiplier = 1.25
					reader := &recordFundingReader{
						events: events, subscription: tc.resolved, subscriptionErr: tc.resolveErr, rate: 1.8,
					}
					if !tc.omitSubscriptions {
						recorder.subscriptions = reader
					}
					if !tc.omitRates {
						recorder.rates = reader
					}
					recorder.now = func() time.Time {
						*events = append(*events, "clock")
						return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
					}
					input.APIKey.BillingMode = tc.mode
					if !tc.omitGroupID {
						input.APIKey.GroupID = &groupID
					}
					if !tc.omitGroup {
						input.APIKey.Group = &GroupSnapshot{ID: groupID, RateMultiplier: 1.4}
					}
					if tc.mode == billing.APIKeyBillingModeSubscription {
						input.APIKey.PreferredSubscriptionID = &subscriptionID
					}
					input.Subscription = tc.current
					if tc.settlementFails {
						funds.err = settlementErr
					}
					var err error
					if protocol == "anthropic" {
						err = recorder.RecordAnthropic(t.Context(), input, nil)
					} else {
						err = recorder.RecordOpenAI(t.Context(), input)
					}
					wantEvents := append([]string{}, tc.wantReads...)
					wantEvents = append(wantEvents, "clock", "funds")
					if tc.settlementFails {
						require.ErrorIs(t, err, settlementErr)
					} else {
						require.NoError(t, err)
						wantEvents = append(wantEvents, "effects")
					}
					wantEvents = append(wantEvents, "best")
					require.Equal(t, wantEvents, *events)
					require.Equal(t, 1, funds.calls)
					require.Len(t, logs.rows, 1)
					row := logs.rows[0]
					require.Equal(t, tc.wantBase, row.RateMultiplier)
					require.Equal(t, tc.wantSubscription, funds.command.SubscriptionRateMultiplier)
					require.Equal(t, tc.wantBalance, funds.command.BalanceRateMultiplier)
					require.Equal(t, 1.0, funds.command.SubscriptionRateMultiplierScale)
					wantMode := tc.mode
					if wantMode == "" {
						wantMode = billing.APIKeyBillingModeAuto
					}
					require.Equal(t, wantMode, funds.command.APIKeyBillingMode)
					require.Equal(t, input.APIKey.PreferredSubscriptionID, funds.command.PreferredSubscriptionID)
					wantType := BillingTypeBalance
					if tc.wantSubscriptionID != nil {
						wantType = BillingTypeSubscription
					}
					require.Equal(t, wantType, funds.command.BillingType)
					// The fixture catalog prices ordinary input at three and output at fifteen
					// microdollars; only the OpenAI entry subtracts the overlapping cache buckets.
					wantInput := 10
					if protocol == "openai" {
						wantInput = 6
					}
					require.Equal(t, wantInput, row.InputTokens)
					wantTotal := float64(wantInput)*3e-6 + 2*15e-6
					// Allow floating-point rounding from token pricing and multiplier arithmetic.
					require.InDelta(t, wantTotal, row.TotalCost, 1e-12)
					require.InDelta(t, wantTotal*tc.wantBase, funds.command.BillableAmountUSD, 1e-12)
					if tc.settlementFails {
						require.Zero(t, row.ActualCost)
						require.Equal(t, tc.wantSubscriptionID, row.SubscriptionID)
					} else {
						require.InDelta(t, wantTotal*tc.wantBase, row.ActualCost, 1e-12)
					}
				})
			}
		})
	}
}

// TestRecordCacheWriteSettlementAndAnomalies exercises Anthropic partial and
// duplicate breakdowns alongside OpenAI untagged and missing-total cache writes.
// Persisted usage rows must always partition cache_creation_tokens into concrete
// TTL buckets and record cache_write_anomaly only when upstream counts disagree.
func TestRecordCacheWriteSettlementAndAnomalies(t *testing.T) {
	t.Run("anthropic partial breakdown assigns remainder to 5m and logs anomaly", func(t *testing.T) {
		recorder, _, logs, input, _ := recordFixture()
		input.Result.Usage = TokenUsage{
			InputTokens:              100,
			OutputTokens:             10,
			CacheCreationInputTokens: 311,
			CacheCreation5mTokens:    211,
			CacheCreation1hTokens:    0,
		}
		require.NoError(t, recorder.RecordAnthropic(t.Context(), input, nil))
		require.Len(t, logs.rows, 1)
		row := logs.rows[0]
		require.Equal(t, 311, row.CacheCreationTokens)
		require.Equal(t, 311, row.CacheCreation5mTokens)
		require.Zero(t, row.CacheCreation1hTokens)
		require.NotNil(t, row.CacheWriteAnomaly)
		require.Equal(t, pricing.CacheWriteAnomalyPartialBreakdown, row.CacheWriteAnomaly.Kind)
		require.Equal(t, 311, row.CacheWriteAnomaly.ReportedTotal)
		require.Equal(t, 211, row.CacheWriteAnomaly.Reported5m)
	})

	t.Run("openai untagged and missing-total cache writes settle into 30m/12h", func(t *testing.T) {
		recorder, _, logs, input, _ := recordFixture()
		input.Provider.Platform = "openai"
		input.Provider.OpenAI = true
		input.Result.Usage = TokenUsage{
			InputTokens:              1000,
			OutputTokens:             20,
			CacheCreationInputTokens: 311,
		}
		require.NoError(t, recorder.RecordOpenAI(t.Context(), input))
		require.Len(t, logs.rows, 1)
		untagged := logs.rows[0]
		require.Equal(t, 689, untagged.InputTokens)
		require.Equal(t, 311, untagged.CacheCreationTokens)
		require.NotNil(t, untagged.CacheCreation30mTokens)
		require.Equal(t, 311, *untagged.CacheCreation30mTokens)
		require.NotNil(t, untagged.CacheCreation12hTokens)
		require.Zero(t, *untagged.CacheCreation12hTokens)
		require.Nil(t, untagged.CacheWriteAnomaly)

		recorder2, _, logs2, input2, _ := recordFixture()
		input2.Provider.Platform = "openai"
		input2.Provider.OpenAI = true
		input2.Result.Usage = TokenUsage{
			InputTokens:              1000,
			OutputTokens:             20,
			CacheCreationInputTokens: 0,
			CacheCreation30mTokens:   new(200),
			CacheCreation12hTokens:   new(100),
		}
		require.NoError(t, recorder2.RecordOpenAI(t.Context(), input2))
		require.Len(t, logs2.rows, 1)
		missingTotal := logs2.rows[0]
		require.Equal(t, 700, missingTotal.InputTokens)
		require.Equal(t, 300, missingTotal.CacheCreationTokens)
		require.NotNil(t, missingTotal.CacheCreation30mTokens)
		require.Equal(t, 200, *missingTotal.CacheCreation30mTokens)
		require.NotNil(t, missingTotal.CacheCreation12hTokens)
		require.Equal(t, 100, *missingTotal.CacheCreation12hTokens)
		require.NotNil(t, missingTotal.CacheWriteAnomaly)
		require.Equal(t, pricing.CacheWriteAnomalyMissingTotal, missingTotal.CacheWriteAnomaly.Kind)
		require.Zero(t, missingTotal.CacheWriteAnomaly.ReportedTotal)
	})
}
