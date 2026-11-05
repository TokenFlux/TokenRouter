package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingskit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// Composite settings preparation normalizes values before their repository
// batch reaches runtime applications. Map-backed cases inspect captured keys
// for registration defaults, creative switches, payment display sources,
// scheduler choices, Ops quota settings, User-Agent strings, and
// forwarded-client-IP policy; preparation errors are checked before any batch
// is captured.
//
// The fixtures distinguish write failure from publication:
// settingUpdateRepoStub can retain attempted map changes despite returning an
// error, while forwardedIPMigrationRepoStub rejects its configured failed write
// before mutating maps. Runtime assertions therefore concern selected callbacks
// and published config, not PostgreSQL rollback. Stored administrator scheduler
// text and fixture process-derived effective values are inspected separately,
// and historical default-subscription parsing retains duplicates that Save
// rejects.

// forwardedIPMigrationRepoStub simulates forwarded-IP reads and captures
// successful migration write batches.
type forwardedIPMigrationRepoStub struct {
	// values retains setting state and updates records the latest successful
	// batch. SetMultiple requires values initialized for nonempty writes and
	// returns injected failure before either map changes.
	values  map[string]string // retained migration setting values
	updates map[string]string // latest successful write batch
	// getMultipleErr takes precedence over configured data and returns nil batch
	// results, letting load cases observe the module's runtime read-failure
	// policy.
	getMultipleErr error
	// setMultipleErr rejects a write before fixture mutation. Nil permits the
	// supplied values to merge into retained state and be captured in updates.
	setMultipleErr error
}

// settingAntigravityUARepoStub supplies stored User-Agent values through its
// supported single-value reads.
type settingAntigravityUARepoStub struct {
	// values is borrowed fixture storage read by exact key. Nil means every key is
	// missing; callers serialize changes while this reader returns its immutable
	// strings.
	values map[string]string
}

// defaultSubPlanReaderStub records plan lookup attempts and supplies configured
// plan or error results.
type defaultSubPlanReaderStub struct {
	// byID and errBy define lookup precedence: a present errBy entry returns
	// first, including nil errors; otherwise a byID entry returns its pointer
	// unchanged before missing-plan failure. byID contains borrowed plan result
	// pointers returned directly by GetByID. Mutating a returned plan therefore
	// also changes this configured fixture object.
	byID  map[int64]*billing.SubscriptionPlan // borrowed plan pointers
	errBy map[int64]error                     // configured lookup error entries
	// calls appends each requested ID before any result is selected. This fixture
	// has no lock, so tests serialize calls and direct changes to its maps and
	// observations.
	calls []int64
}

// Get panics on an unsupported single-record read during forwarded-IP migration
// fixtures.
func (s *forwardedIPMigrationRepoStub) Get(context.Context, string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue returns the configured string for a present key, including an empty
// value, and ignores ctx. Missing keys return ErrSettingNotFound so the
// forwarded-IP reader can select its missing-setting behavior.
func (s *forwardedIPMigrationRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return value, nil
}

// Set panics on an unsupported individual setting write during forwarded-IP
// migration fixtures.
func (s *forwardedIPMigrationRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

// GetMultiple returns the configured batch-read error before copying values, or
// a new map of present requested strings. Missing keys are omitted, returned
// storage is independent, and ctx is ignored.
func (s *forwardedIPMigrationRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if s.getMultipleErr != nil {
		return nil, s.getMultipleErr
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

// SetMultiple returns its configured write failure before changing either map.
// Success replaces updates and merges supplied entries into values; callers
// initialize values for nonempty writes and serialize access to this fixture.
func (s *forwardedIPMigrationRepoStub) SetMultiple(_ context.Context, values map[string]string) error {
	if s.setMultipleErr != nil {
		return s.setMultipleErr
	}
	s.updates = make(map[string]string, len(values))
	for key, value := range values {
		s.values[key] = value
		s.updates[key] = value
	}
	return nil
}

// GetAll panics on an unsupported complete-map read in forwarded-IP migration
// fixtures.
func (s *forwardedIPMigrationRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete panics on unsupported setting removal in the forwarded-IP migration
// fixture.
func (s *forwardedIPMigrationRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

// Get panics when the user-agent reader unexpectedly requests a complete
// setting record.
func (s *settingAntigravityUARepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue returns a configured user-agent string for its exact setting key and
// ignores ctx. Missing keys return ErrSettingNotFound, while a present empty
// value is returned with no error.
func (s *settingAntigravityUARepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", settingscore.ErrSettingNotFound
}

// Set panics on an unexpected individual write through the user-agent read
// fixture.
func (s *settingAntigravityUARepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple panics on an unsupported batch read through the single-value
// user-agent fixture.
func (s *settingAntigravityUARepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

// SetMultiple panics on unexpected batch persistence through the user-agent
// read fixture.
func (s *settingAntigravityUARepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

// GetAll panics on an unsupported whole-map request through the user-agent read
// fixture.
func (s *settingAntigravityUARepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete panics on unexpected setting removal through the user-agent read
// fixture.
func (s *settingAntigravityUARepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// TestSettingService_AffiliateAdminRechargeSetting compares a missing and
// enabled administrator recharge-rebate flag, then saves enabled state. Parsing
// selects the expected booleans and the captured repository batch contains true
// text.
func TestSettingService_AffiliateAdminRechargeSetting(t *testing.T) {
	t.Run("missing value defaults to disabled", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingGetAllRepoStub{values: map[string]string{}}, &config.Config{})

		settings, err := svc.Runtime.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.False(t, settings.AdminRechargeRebateEnabled)
	})

	t.Run("explicit value is parsed", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingGetAllRepoStub{values: map[string]string{
			promotion.SettingKeyAffiliateAdminRechargeEnabled: "true",
		}}, &config.Config{})

		settings, err := svc.Runtime.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.True(t, settings.AdminRechargeRebateEnabled)
	})

	t.Run("value is persisted", func(t *testing.T) {
		repo := &settingUpdateRepoStub{}
		svc := settingskit.NewComposite(repo, &config.Config{})

		err := svc.Save(context.Background(), &composite.Snapshot{
			AdminRechargeRebateEnabled: true,
		})
		require.NoError(t, err)
		require.Equal(t, "true", repo.updates[promotion.SettingKeyAffiliateAdminRechargeEnabled])
	})
}

// TestSettingService_PageFeatureFlagsArePersisted saves enabled team features
// and disabled creative features in one snapshot. The capturing repository
// records separate true and false values under their respective setting keys.
func TestSettingService_PageFeatureFlagsArePersisted(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		TeamEnabled:     true,
		CreativeEnabled: false,
	})
	require.NoError(t, err)
	require.Equal(t, "true", repo.updates[team.SettingKeyTeamEnabled])
	require.Equal(t, "false", repo.updates[creative.SettingKeyCreativeEnabled])
}

// TestSettingService_CreativeWorkerCountIsPersisted saves a worker count of
// seven through the composite fixture. The repository's captured batch includes
// that count as text under the creative worker setting key.
func TestSettingService_CreativeWorkerCountIsPersisted(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{CreativeWorkerCount: 7})
	require.NoError(t, err)
	require.Equal(t, "7", repo.updates[creative.SettingKeyCreativeWorkerCount])
}

// TestSettingService_CreativeWorkerCountCallbackOnlyAfterWrite saves a worker
// count and then injects a repository write error. The successful call
// publishes nine once, while the later failed Save invokes no additional
// callback.
func TestSettingService_CreativeWorkerCountCallbackOnlyAfterWrite(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})
	var callbackValue int
	callbackCalls := 0
	svc.WorkerChanged = func(value int) {
		callbackValue = value
		callbackCalls++
	}

	require.NoError(t, svc.Save(context.Background(), &composite.Snapshot{CreativeWorkerCount: 9}))
	require.Equal(t, 9, callbackValue)
	require.Equal(t, 1, callbackCalls)

	// Return a fixture write error on the later Save; its attempted maps may still
	// change, while the callback count exposes whether runtime publication follows
	// that failure.
	repo.setMultipleErr = errors.New("database unavailable")
	require.Error(t, svc.Save(context.Background(), &composite.Snapshot{CreativeWorkerCount: 11}))
	require.Equal(t, 1, callbackCalls)
}

// GetByID records id before returning a configured error entry, borrowed plan
// pointer, or SubscriptionNotFound. Presence in errBy takes precedence even for
// nil errors; mapped plan pointers, including nil, return unchanged.
func (s *defaultSubPlanReaderStub) GetByID(ctx context.Context, id int64) (*billing.SubscriptionPlan, error) {
	s.calls = append(s.calls, id)
	if err, ok := s.errBy[id]; ok {
		return nil, err
	}
	if plan, ok := s.byID[id]; ok {
		return plan, nil
	}
	return nil, billing.ErrSubscriptionNotFound
}

// TestSettingService_UpdateSettings_DefaultSubscriptions_ValidPlan supplies a
// default grant referencing an available plan fixture. Save performs the
// asserted plan lookup and captures equivalent default-subscription JSON in its
// write batch.
func TestSettingService_UpdateSettings_DefaultSubscriptions_ValidPlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		byID: map[int64]*billing.SubscriptionPlan{
			11: {ID: 11, Name: "Monthly"},
		},
	}
	svc := settingskit.NewComposite(repo, &config.Config{})
	svc.Plans = planReader

	err := svc.Save(context.Background(), &composite.Snapshot{
		DefaultSubscriptions: []identity.DefaultSubscriptionSetting{
			{PlanID: 11},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{11}, planReader.calls)

	raw, ok := repo.updates[billing.SettingKeyDefaultSubscriptions]
	require.True(t, ok)

	var got []identity.DefaultSubscriptionSetting
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Equal(t, []identity.DefaultSubscriptionSetting{
		{PlanID: 11},
	}, got)
}

// TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsMissingPlan
// injects SubscriptionNotFound for the requested default plan. Save reports
// DEFAULT_SUBSCRIPTION_PLAN_INVALID and the capturing repository receives no
// write batch for that snapshot.
func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsMissingPlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		errBy: map[int64]error{
			12: billing.ErrSubscriptionNotFound,
		},
	}
	svc := settingskit.NewComposite(repo, &config.Config{})
	svc.Plans = planReader

	err := svc.Save(context.Background(), &composite.Snapshot{
		DefaultSubscriptions: []identity.DefaultSubscriptionSetting{
			{PlanID: 12},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_INVALID", apperror.Reason(err))
	require.Nil(t, repo.updates)
}

// TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsNotFoundPlan
// injects an application NotFound result for one plan. Save reports the
// default-plan error with the selected plan ID and leaves the captured updates
// nil.
func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsNotFoundPlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		errBy: map[int64]error{
			13: apperror.NotFound("PLAN_NOT_FOUND", "subscription plan not found"),
		},
	}
	svc := settingskit.NewComposite(repo, &config.Config{})
	svc.Plans = planReader

	err := svc.Save(context.Background(), &composite.Snapshot{
		DefaultSubscriptions: []identity.DefaultSubscriptionSetting{
			{PlanID: 13},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_INVALID", apperror.Reason(err))
	require.Equal(t, "13", apperror.FromError(err).Metadata["plan_id"])
	require.Nil(t, repo.updates)
}

// TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsDuplicatePlan
// submits two grants for the same available plan ID. Save returns the
// duplicate-plan reason with ID metadata before a repository batch is captured.
func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsDuplicatePlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		byID: map[int64]*billing.SubscriptionPlan{
			11: {ID: 11, Name: "Monthly"},
		},
	}
	svc := settingskit.NewComposite(repo, &config.Config{})
	svc.Plans = planReader

	err := svc.Save(context.Background(), &composite.Snapshot{
		DefaultSubscriptions: []identity.DefaultSubscriptionSetting{
			{PlanID: 11},
			{PlanID: 11},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_DUPLICATE", apperror.Reason(err))
	require.Equal(t, "11", apperror.FromError(err).Metadata["plan_id"])
	require.Nil(t, repo.updates)
}

// TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsDuplicatePlanWithoutReader
// repeats a default plan without configuring plan lookup. Duplicate validation
// still returns the expected reason and ID metadata while the repository
// receives no batch.
func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsDuplicatePlanWithoutReader(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		DefaultSubscriptions: []identity.DefaultSubscriptionSetting{
			{PlanID: 11},
			{PlanID: 11},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_DUPLICATE", apperror.Reason(err))
	require.Equal(t, "11", apperror.FromError(err).Metadata["plan_id"])
	require.Nil(t, repo.updates)
}

// TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Normalized
// saves mixed-case, spaced, repeated, and wildcard domain entries. The captured
// JSON normalizes prefixes and case, trims whitespace, and retains one copy of
// the repeated domain.
func TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Normalized(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		RegistrationEmailSuffixWhitelist: []string{"example.com", "@EXAMPLE.com", " @foo.bar ", "*.EDU.CN"},
	})
	require.NoError(t, err)
	require.Equal(t, `["@example.com","@foo.bar","*.edu.cn"]`, repo.updates[identity.SettingKeyRegistrationEmailSuffixWhitelist])
}

// TestSettingService_UpdateSettings_PersistsUserEmailChangeSwitch saves an
// enabled user email-change switch through the composite fixture. The
// repository captures true text under the identity setting that governs this
// option.
func TestSettingService_UpdateSettings_PersistsUserEmailChangeSwitch(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{UserEmailChangeEnabled: true})
	require.NoError(t, err)
	require.Equal(t, "true", repo.updates[identity.SettingKeyUserEmailChangeEnabled])
}

// TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Invalid
// supplies a registration domain containing an invalid underscore. Save returns
// an error with INVALID_REGISTRATION_EMAIL_SUFFIX_WHITELIST as its reason for
// that configured entry.
func TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Invalid(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		RegistrationEmailSuffixWhitelist: []string{"@invalid_domain"},
	})
	require.Error(t, err)
	require.Equal(t, "INVALID_REGISTRATION_EMAIL_SUFFIX_WHITELIST", apperror.Reason(err))
}

// TestParseDefaultSubscriptions_NormalizesValues parses historical grant JSON
// containing repeated positive plans and a zero plan. Positive entries retain
// their order and duplicate while the zero entry is omitted.
func TestParseDefaultSubscriptions_NormalizesValues(t *testing.T) {
	got := identity.ParseDefaultSubscriptions(`[{"plan_id":11},{"plan_id":11},{"plan_id":0},{"plan_id":12}]`)
	require.Equal(t, []identity.DefaultSubscriptionSetting{
		{PlanID: 11},
		{PlanID: 11},
		{PlanID: 12},
	}, got)
}

// TestSettingService_UpdateSettings_TablePreferences saves table page size and
// option arrays through the capturing fixture. Both initial and later snapshots
// produce the asserted string and JSON values, including the larger default
// size.
func TestSettingService_UpdateSettings_TablePreferences(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		TableDefaultPageSize: 50,
		TablePageSizeOptions: []int{20, 50, 100},
	})
	require.NoError(t, err)
	require.Equal(t, "50", repo.updates[site.SettingKeyTableDefaultPageSize])
	require.Equal(t, "[20,50,100]", repo.updates[site.SettingKeyTablePageSizeOptions])

	err = svc.Save(context.Background(), &composite.Snapshot{
		TableDefaultPageSize: 1000,
		TablePageSizeOptions: []int{20, 100},
	})
	require.NoError(t, err)
	require.Equal(t, "1000", repo.updates[site.SettingKeyTableDefaultPageSize])
	require.Equal(t, "[20,100]", repo.updates[site.SettingKeyTablePageSizeOptions])
}

// TestSettingService_UpdateSettings_PaymentVisibleMethodsAndAdvancedScheduler
// supplies payment aliases and scheduler flags, alphas, thresholds, and
// weights. Captured updates normalize provider aliases and numeric text while
// retaining the selected independent settings.
func TestSettingService_UpdateSettings_PaymentVisibleMethodsAndAdvancedScheduler(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		PaymentVisibleMethodAlipaySource:             "alipay",
		PaymentVisibleMethodWxpaySource:              "easypay",
		PaymentVisibleMethodAlipayEnabled:            true,
		PaymentVisibleMethodWxpayEnabled:             false,
		AdvancedSchedulerStickyWeightedEnabled:       true,
		AdvancedSchedulerSubscriptionPriorityEnabled: true,
		AdvancedSchedulerEWMAErrorRateAlpha:          "0.4",
		AdvancedSchedulerEWMATTFTAlpha:               "0.7",
		AdvancedSchedulerStickyEscapeEnabled:         false,
		AdvancedSchedulerStickyEscapeEnabledSet:      true,
		AdvancedSchedulerStickyEscapeTTFTMs:          "9000",
		AdvancedSchedulerStickyEscapeErrorRate:       "0.25",
		AdvancedSchedulerLBTopK:                      " 3 ",
		AdvancedSchedulerWeightPriority:              "2.50",
		AdvancedSchedulerWeightLoad:                  "0",
		AdvancedSchedulerWeightQueue:                 "0.75",
		AdvancedSchedulerWeightErrorRate:             "1.25",
		AdvancedSchedulerWeightTTFT:                  "0.5",
		AdvancedSchedulerWeightReset:                 "",
		AdvancedSchedulerWeightQuotaHeadroom:         "0.2",
		AdvancedSchedulerWeightPreviousResponse:      "8",
		AdvancedSchedulerWeightSessionSticky:         "4",
	})
	require.NoError(t, err)
	require.Equal(t, payment.VisibleMethodSourceOfficialAlipay, repo.updates[payment.SettingPaymentVisibleMethodAlipaySource])
	require.Equal(t, payment.VisibleMethodSourceEasyPayWechat, repo.updates[payment.SettingPaymentVisibleMethodWxpaySource])
	require.Equal(t, "true", repo.updates[payment.SettingPaymentVisibleMethodAlipayEnabled])
	require.Equal(t, "false", repo.updates[payment.SettingPaymentVisibleMethodWxpayEnabled])
	require.Equal(t, "true", repo.updates[scheduler.SettingKeyAdvancedSchedulerStickyWeightedEnabled])
	require.Equal(t, "true", repo.updates[scheduler.SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled])
	require.Equal(t, "0.4", repo.updates[scheduler.SettingKeyAdvancedSchedulerEWMAErrorRateAlpha])
	require.Equal(t, "0.7", repo.updates[scheduler.SettingKeyAdvancedSchedulerEWMATTFTAlpha])
	require.Equal(t, "false", repo.updates[scheduler.SettingKeyAdvancedSchedulerStickyEscapeEnabled])
	require.Equal(t, "9000", repo.updates[scheduler.SettingKeyAdvancedSchedulerStickyEscapeTTFTMs])
	require.Equal(t, "0.25", repo.updates[scheduler.SettingKeyAdvancedSchedulerStickyEscapeErrorRate])
	require.Equal(t, "3", repo.updates[scheduler.SettingKeyAdvancedSchedulerLBTopK])
	require.Equal(t, "2.5", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightPriority])
	require.Equal(t, "0", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightLoad])
	require.Equal(t, "0.75", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightQueue])
	require.Equal(t, "1.25", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightErrorRate])
	require.Equal(t, "0.5", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightTTFT])
	require.Equal(t, "", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightReset])
	require.Equal(t, "0.2", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightQuotaHeadroom])
	require.Equal(t, "8", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightPreviousResponse])
	require.Equal(t, "4", repo.updates[scheduler.SettingKeyAdvancedSchedulerWeightSessionSticky])
}

// TestSettingServiceRefreshCachedSettingsKeepsIndependentEWMAProcessDefaults
// publishes empty administrator scheduler values with distinct process alphas.
// Runtime loading retains the configured error-rate and latency alpha values
// within the comparison tolerance.
func TestSettingServiceRefreshCachedSettingsKeepsIndependentEWMAProcessDefaults(t *testing.T) {
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.EWMAErrorRateAlpha = 0.35
	cfg.Scheduling.Advanced.EWMATTFTAlpha = 0.8
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, cfg)
	svc.Scheduler.Store(scheduler.RuntimeSettingsFromAdmin(scheduler.AdminSettings{}, svc.Defaults))

	runtime := svc.Scheduler.Load(context.Background(), nil, svc.Defaults.Process)
	// A one-millionth comparison tolerance distinguishes the independent process
	// alpha values.
	require.InDelta(t, 0.35, runtime.EwmaErrorRateAlpha, 0.000001)
	require.InDelta(t, 0.8, runtime.EwmaTTFTAlpha, 0.000001)
}

// TestSettingService_UpdateSettings_AdvancedSchedulerWeightSums supplies
// reset-only weights and individually finite weights whose sums overflow. Save
// accepts the reset-only case and rejects both overflowing base or sticky
// totals.
func TestSettingService_UpdateSettings_AdvancedSchedulerWeightSums(t *testing.T) {
	// Supply individually finite weights using MaxFloat64 text so summing two
	// values exposes overflow in either base scoring or the combined sticky
	// scoring configuration.
	maxFloat := strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64)
	tests := []struct {
		name    string             // scheduler weight input case
		weights composite.Snapshot // submitted scheduler weight settings
		wantErr bool               // expected sum validation failure
	}{
		{
			name: "reset only base is valid",
			weights: composite.Snapshot{
				AdvancedSchedulerWeightPriority:         "0",
				AdvancedSchedulerWeightLoad:             "0",
				AdvancedSchedulerWeightQueue:            "0",
				AdvancedSchedulerWeightErrorRate:        "0",
				AdvancedSchedulerWeightTTFT:             "0",
				AdvancedSchedulerWeightReset:            "1",
				AdvancedSchedulerWeightQuotaHeadroom:    "0",
				AdvancedSchedulerWeightPreviousResponse: "0",
				AdvancedSchedulerWeightSessionSticky:    "0",
			},
		},
		{
			name: "base sum overflow is rejected",
			weights: composite.Snapshot{
				AdvancedSchedulerWeightPriority: maxFloat,
				AdvancedSchedulerWeightLoad:     maxFloat,
			},
			wantErr: true,
		},
		{
			name: "sticky total sum overflow is rejected",
			weights: composite.Snapshot{
				AdvancedSchedulerWeightPriority:         maxFloat,
				AdvancedSchedulerWeightPreviousResponse: maxFloat,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := settingskit.NewComposite(&settingUpdateRepoStub{}, &config.Config{})
			err := svc.Save(context.Background(), &tt.weights)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestSettingService_GetAllSettings_AdvancedSchedulerEffectiveValuesUseConfig
// supplies stored scheduler text that differs from fixture process defaults.
// The runtime snapshot reports those raw strings alongside effective values
// from the configured process settings.
func TestSettingService_GetAllSettings_AdvancedSchedulerEffectiveValuesUseConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Scheduling.Advanced.LBTopK = 13
	cfg.Scheduling.Advanced.ScoreWeights = config.GatewayAdvancedSchedulerScoreWeights{
		Priority:         2,
		Load:             3,
		Queue:            4,
		ErrorRate:        5,
		TTFT:             6,
		Reset:            7,
		QuotaHeadroom:    8,
		PreviousResponse: 10,
		SessionSticky:    11,
	}
	svc := settingskit.NewComposite(&settingGetAllRepoStub{values: map[string]string{
		scheduler.SettingKeyAdvancedSchedulerLBTopK:              "3",
		scheduler.SettingKeyAdvancedSchedulerWeightPriority:      "99",
		scheduler.SettingKeyAdvancedSchedulerWeightSessionSticky: "88",
	}}, cfg)

	settings, err := svc.Runtime.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "3", settings.AdvancedSchedulerLBTopK)
	require.Equal(t, "99", settings.AdvancedSchedulerWeightPriority)
	require.Equal(t, "88", settings.AdvancedSchedulerWeightSessionSticky)
	require.Equal(t, "13", settings.AdvancedSchedulerEffectiveLBTopK)
	require.Equal(t, "2", settings.AdvancedSchedulerEffectiveWeightPriority)
	require.Equal(t, "3", settings.AdvancedSchedulerEffectiveWeightLoad)
	require.Equal(t, "11", settings.AdvancedSchedulerEffectiveWeightSessionSticky)
}

// TestSettingService_UpdateSettings_OpenAIQuotaAutoPauseMergesOpsAdvancedSettings
// replaces pause thresholds inside existing Ops JSON. Captured values retain
// asserted operational settings, remove aggregation, clamp the oversized
// threshold, and match the quota reader's published settings.
func TestSettingService_UpdateSettings_OpenAIQuotaAutoPauseMergesOpsAdvancedSettings(t *testing.T) {
	repo := &settingUpdateRepoStub{
		values: map[string]string{
			ops.SettingKeyOpsAdvancedSettings: `{"data_retention":{"cleanup_enabled":true,"cleanup_schedule":"0 3 * * *","error_log_retention_days":14,"minute_metrics_retention_days":7,"hourly_metrics_retention_days":30},"aggregation":{"aggregation_enabled":true},"openai_provider_quota_auto_pause":{"default_threshold_5h":0.8,"default_threshold_7d":0.75},"ignore_count_tokens_errors":false,"ignore_context_canceled":true,"ignore_no_available_providers":true,"ignore_invalid_api_key_errors":true,"ignore_insufficient_balance_errors":false,"display_openai_token_stats":true,"display_alert_events":false,"auto_refresh_enabled":true,"auto_refresh_interval_seconds":45}`,
		},
	}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		OpenAIQuotaAutoPauseSettingsSet: true,
		OpenAIQuotaAutoPauseSettings: ops.OpsOpenAIProviderQuotaAutoPauseSettings{
			DefaultThreshold5h: 0.95,
			DefaultThreshold7d: 1.2,
		},
	})
	require.NoError(t, err)

	var got ops.OpsAdvancedSettings
	require.NoError(t, json.Unmarshal([]byte(repo.updates[ops.SettingKeyOpsAdvancedSettings]), &got))
	require.Equal(t, true, got.DataRetention.CleanupEnabled)
	require.Equal(t, "0 3 * * *", got.DataRetention.CleanupSchedule)
	require.Equal(t, 14, got.DataRetention.ErrorLogRetentionDays)
	require.NotContains(t, repo.updates[ops.SettingKeyOpsAdvancedSettings], `"aggregation"`)
	require.False(t, got.IgnoreCountTokensErrors)
	require.True(t, got.DisplayOpenAITokenStats)
	require.False(t, got.DisplayAlertEvents)
	require.True(t, got.AutoRefreshEnabled)
	require.Equal(t, 45, got.AutoRefreshIntervalSec)
	require.Equal(t, 0.95, got.OpenAIProviderQuotaAutoPause.DefaultThreshold5h)
	require.Equal(t, 1.0, got.OpenAIProviderQuotaAutoPause.DefaultThreshold7d)
	require.Equal(t, got.OpenAIProviderQuotaAutoPause, svc.Quota.GetOpenAIQuotaAutoPauseSettings(context.Background()))
}

// TestSettingService_ParseSettings_OpenAIQuotaAutoPauseFromOpsAdvancedSettings
// reads pause thresholds from stored Ops JSON. Composite parsing retains the
// valid five-hour threshold and caps the oversized seven-day value at one.
func TestSettingService_ParseSettings_OpenAIQuotaAutoPauseFromOpsAdvancedSettings(t *testing.T) {
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, &config.Config{})

	got := composite.Parse(map[string]string{
		ops.SettingKeyOpsAdvancedSettings: `{"openai_provider_quota_auto_pause":{"default_threshold_5h":0.7,"default_threshold_7d":1.3}}`,
	}, svc.Read)

	require.Equal(t, 0.7, got.OpenAIQuotaAutoPauseSettings.DefaultThreshold5h)
	require.Equal(t, 1.0, got.OpenAIQuotaAutoPauseSettings.DefaultThreshold7d)
}

// TestSettingService_UpdateSettings_AntigravityUserAgentVersion saves a
// configured Antigravity version through the composite fixture. The captured
// repository batch contains the supplied version text under its gateway setting
// key.
func TestSettingService_UpdateSettings_AntigravityUserAgentVersion(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		AntigravityUserAgentVersion: "1.23.2",
	})
	require.NoError(t, err)
	require.Equal(t, "1.23.2", repo.updates[gateway.SettingKeyAntigravityUserAgentVersion])
}

// TestSettingService_UpdateSettings_OpenAICodexUserAgent saves a Codex
// User-Agent surrounded by whitespace through the composite fixture. The
// captured value contains the supplied agent and terminal text with outer
// spaces removed.
func TestSettingService_UpdateSettings_OpenAICodexUserAgent(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		OpenAICodexUserAgent: " codex-tui/9.9.9 test-terminal ",
	})
	require.NoError(t, err)
	require.Equal(t, "codex-tui/9.9.9 test-terminal", repo.updates[gateway.SettingKeyOpenAICodexUserAgent])
}

// TestSettingService_InitializeDefaultSettingsPersistsConfiguredForwardedClientIPHeaders
// loads forwarded-IP settings from an empty fixture repository with configured
// headers. Migration writes the configured header list into the retained
// fixture values.
func TestSettingService_InitializeDefaultSettingsPersistsConfiguredForwardedClientIPHeaders(t *testing.T) {
	repo := &forwardedIPMigrationRepoStub{values: map[string]string{}}
	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(true, []string{"X-Cdn-Ip", "True-Client-Ip"})
	svc := settingskit.NewComposite(repo, cfg)

	require.NoError(t, svc.Forwarded.LoadForwardedClientIPSettings(context.Background()))
	require.JSONEq(t, `["X-Cdn-Ip","True-Client-Ip"]`, repo.values[runtimeconfig.SettingKeyForwardedClientIPHeaders])
}

// TestSettingService_UpdateSettings_APIKeyACLTrustForwardedIPRefreshesConfig
// saves enabled forwarded trust with repeated and mixed-case header names.
// Captured headers are normalized, runtime config updates, and mutating its
// returned slice does not alter a later read.
func TestSettingService_UpdateSettings_APIKeyACLTrustForwardedIPRefreshesConfig(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	cfg := &config.Config{}
	svc := settingskit.NewComposite(repo, cfg)

	err := svc.Save(context.Background(), &composite.Snapshot{
		APIKeyACLTrustForwardedIP: true,
		ForwardedClientIPHeaders:  []string{" x-cdn-ip ", "X-CDN-IP", "true-client-ip"},
	})
	require.NoError(t, err)
	require.Equal(t, "true", repo.updates[runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP])
	require.JSONEq(t, `["X-Cdn-Ip","True-Client-Ip"]`, repo.updates[runtimeconfig.SettingKeyForwardedClientIPHeaders])
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.True(t, runtimeSettings.TrustForwardedIP)
	require.Equal(t, []string{"X-Cdn-Ip", "True-Client-Ip"}, runtimeSettings.Headers)

	runtimeSettings.Headers[0] = "X-Mutated"
	require.Equal(t, []string{"X-Cdn-Ip", "True-Client-Ip"}, cfg.ForwardedClientIPSettings().Headers)
}

// TestSettingService_UpdateSettings_RejectsInvalidForwardedClientIPHeadersWithoutRefreshing
// supplies a header containing whitespace inside its name. Save fails before
// capturing updates, and existing runtime trust plus header values remain
// unchanged.
func TestSettingService_UpdateSettings_RejectsInvalidForwardedClientIPHeadersWithoutRefreshing(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(true, []string{"X-Existing-IP"})
	svc := settingskit.NewComposite(repo, cfg)

	err := svc.Save(context.Background(), &composite.Snapshot{
		ForwardedClientIPHeaders: []string{"X Invalid"},
	})

	require.Error(t, err)
	require.Nil(t, repo.updates)
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.True(t, runtimeSettings.TrustForwardedIP)
	require.Equal(t, []string{"X-Existing-IP"}, runtimeSettings.Headers)
}

// TestSettingService_UpdateSettings_WriteFailureDoesNotRefreshForwardedIPRuntime
// injects a repository failure while requesting new trust and headers. Save
// returns that failure and runtime config retains its preceding disabled policy
// and existing headers.
func TestSettingService_UpdateSettings_WriteFailureDoesNotRefreshForwardedIPRuntime(t *testing.T) {
	repo := &settingUpdateRepoStub{setMultipleErr: errors.New("database unavailable")}
	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(false, []string{"X-Existing-IP"})
	svc := settingskit.NewComposite(repo, cfg)

	err := svc.Save(context.Background(), &composite.Snapshot{
		APIKeyACLTrustForwardedIP: true,
		ForwardedClientIPHeaders:  []string{"X-New-IP"},
	})

	require.ErrorContains(t, err, "database unavailable")
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.False(t, runtimeSettings.TrustForwardedIP)
	require.Equal(t, []string{"X-Existing-IP"}, runtimeSettings.Headers)
}

// TestSettingService_ParseSettings_APIKeyACLTrustForwardedIPFallsBackToConfigWhenMissing
// supplies no stored trust choice with an enabled process default. Composite
// parsing reports enabled trust from that configured starting value.
func TestSettingService_ParseSettings_APIKeyACLTrustForwardedIPFallsBackToConfigWhenMissing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.ClientIP.TrustForwardedHeaders = true
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, cfg)

	got := composite.Parse(map[string]string{}, svc.Read)

	require.True(t, got.APIKeyACLTrustForwardedIP)
}

// TestSettingService_ParseSettings_APIKeyACLTrustForwardedIPUsesStoredValue
// supplies a stored false choice beside an enabled process default. Composite
// parsing reports disabled trust for the supplied stored setting value.
func TestSettingService_ParseSettings_APIKeyACLTrustForwardedIPUsesStoredValue(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetTrustForwardedHeaders(true)
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, cfg)

	got := composite.Parse(map[string]string{runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "false"}, svc.Read)

	require.False(t, got.APIKeyACLTrustForwardedIP)
}

// TestSettingService_ParseSettings_CreativeEnabledDefaultsTrue compares missing
// creative enablement with stored false text through composite parsing. Missing
// state enables creative features, while the supplied false setting disables
// them.
func TestSettingService_ParseSettings_CreativeEnabledDefaultsTrue(t *testing.T) {
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, &config.Config{})

	got := composite.Parse(map[string]string{}, svc.Read)
	require.True(t, got.CreativeEnabled)

	got = composite.Parse(map[string]string{creative.SettingKeyCreativeEnabled: "false"}, svc.Read)
	require.False(t, got.CreativeEnabled)
}

// TestSettingService_ParseSettings_CreativeWorkerCount parses missing, valid,
// zero, and malformed worker text. A valid seven is retained while the other
// exercised cases return the creative worker default.
func TestSettingService_ParseSettings_CreativeWorkerCount(t *testing.T) {
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, &config.Config{})

	got := composite.Parse(map[string]string{}, svc.Read)
	require.Equal(t, creative.DefaultCreativeWorkerCount, got.CreativeWorkerCount)
	got = composite.Parse(map[string]string{creative.SettingKeyCreativeWorkerCount: "7"}, svc.Read)
	require.Equal(t, 7, got.CreativeWorkerCount)
	got = composite.Parse(map[string]string{creative.SettingKeyCreativeWorkerCount: "0"}, svc.Read)
	require.Equal(t, creative.DefaultCreativeWorkerCount, got.CreativeWorkerCount)
	got = composite.Parse(map[string]string{creative.SettingKeyCreativeWorkerCount: "invalid"}, svc.Read)
	require.Equal(t, creative.DefaultCreativeWorkerCount, got.CreativeWorkerCount)
}

// TestSettingService_ParseSettings_ForwardedClientIPHeaders compares valid
// stored header JSON, absent data, and malformed object data. Parsing
// normalizes headers, uses configured headers when missing, and disables trust
// for the malformed input.
func TestSettingService_ParseSettings_ForwardedClientIPHeaders(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(true, []string{"X-Config-IP"})
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, cfg)

	t.Run("stored value is normalized", func(t *testing.T) {
		got := composite.Parse(map[string]string{
			runtimeconfig.SettingKeyForwardedClientIPHeaders: `[" x-cdn-ip ","X-CDN-IP","true-client-ip"]`,
		}, svc.Read)
		require.Equal(t, []string{"X-Cdn-Ip", "True-Client-Ip"}, got.ForwardedClientIPHeaders)
	})

	t.Run("missing value falls back to config", func(t *testing.T) {
		got := composite.Parse(map[string]string{}, svc.Read)
		require.Equal(t, []string{"X-Config-IP"}, got.ForwardedClientIPHeaders)
	})

	t.Run("malformed value disables forwarded trust", func(t *testing.T) {
		got := composite.Parse(map[string]string{
			runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "true",
			runtimeconfig.SettingKeyForwardedClientIPHeaders:  `{"not":"an array"}`,
		}, svc.Read)
		require.False(t, got.APIKeyACLTrustForwardedIP)
		require.Empty(t, got.ForwardedClientIPHeaders)
	})
}

// TestSettingService_LoadForwardedClientIPSettingsMigration varies legacy trust
// text, process defaults, proxy configuration, and migration completion.
// Loading selects the asserted runtime policy, captures expected compatibility
// writes, and updates the marker when required.
func TestSettingService_LoadForwardedClientIPSettingsMigration(t *testing.T) {
	tests := []struct {
		name                   string            // migration case name
		values                 map[string]string // input settings map
		trustedProxiesSet      bool              // proxy configuration supplied
		configDefault          bool              // process trust default
		wantEnabled            bool              // expected trust mode
		wantForwardedIPUpdate  string            // captured trust rewrite
		wantMigrationMarkerSet bool              // marker write expected
	}{
		{
			name:                   "missing setting follows configured default",
			values:                 map[string]string{},
			configDefault:          true,
			wantEnabled:            true,
			wantMigrationMarkerSet: true,
		},
		{
			name:                   "legacy false without proxy config migrates to compatibility",
			values:                 map[string]string{runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "false"},
			wantEnabled:            true,
			wantForwardedIPUpdate:  "true",
			wantMigrationMarkerSet: true,
		},
		{
			name:                   "legacy false with explicit proxy config stays secure",
			values:                 map[string]string{runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "false"},
			trustedProxiesSet:      true,
			wantEnabled:            false,
			wantMigrationMarkerSet: true,
		},
		{
			name: "completed migration preserves later false choice",
			values: map[string]string{
				runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "false",
				runtimeconfig.SettingKeyForwardedClientIPModeV2:   "true",
			},
			wantEnabled: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &forwardedIPMigrationRepoStub{values: test.values}
			cfg := &config.Config{Server: config.ServerConfig{ClientIP: config.ServerClientIPConfig{TrustedProxiesConfigured: test.trustedProxiesSet}}}
			cfg.Server.ClientIP.TrustForwardedHeaders = test.configDefault
			svc := settingskit.NewComposite(repo, cfg)

			require.NoError(t, svc.Forwarded.LoadForwardedClientIPSettings(context.Background()))
			require.Equal(t, test.wantEnabled, cfg.TrustForwardedHeaders())
			require.Equal(t, test.wantForwardedIPUpdate, repo.updates[runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP])
			require.JSONEq(t, `[]`, repo.updates[runtimeconfig.SettingKeyForwardedClientIPHeaders])
			if test.wantMigrationMarkerSet {
				require.Equal(t, "true", repo.updates[runtimeconfig.SettingKeyForwardedClientIPModeV2])
			} else {
				require.NotContains(t, repo.updates, runtimeconfig.SettingKeyForwardedClientIPModeV2)
			}
		})
	}
}

// TestSettingService_LoadForwardedClientIPSettingsLoadsHeaders supplies
// normalized-mode settings and valid stored header JSON. Loading updates
// runtime trust and canonical header names while capturing no additional
// migration write batch.
func TestSettingService_LoadForwardedClientIPSettingsLoadsHeaders(t *testing.T) {
	repo := &forwardedIPMigrationRepoStub{values: map[string]string{
		runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "true",
		runtimeconfig.SettingKeyForwardedClientIPHeaders:  `[" x-cdn-ip ","true-client-ip"]`,
		runtimeconfig.SettingKeyForwardedClientIPModeV2:   "true",
	}}
	cfg := &config.Config{}
	svc := settingskit.NewComposite(repo, cfg)

	require.NoError(t, svc.Forwarded.LoadForwardedClientIPSettings(context.Background()))
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.True(t, runtimeSettings.TrustForwardedIP)
	require.Equal(t, []string{"X-Cdn-Ip", "True-Client-Ip"}, runtimeSettings.Headers)
	require.Nil(t, repo.updates)
}

// TestSettingService_LoadForwardedClientIPSettingsMalformedHeadersDisablesCustomTrust
// supplies an invalid stored header name. Loading reports the header error,
// disables trust and headers, and still captures the migration marker without
// rewriting the stored trust choice.
func TestSettingService_LoadForwardedClientIPSettingsMalformedHeadersDisablesCustomTrust(t *testing.T) {
	repo := &forwardedIPMigrationRepoStub{values: map[string]string{
		runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "true",
		runtimeconfig.SettingKeyForwardedClientIPHeaders:  `["X Invalid"]`,
	}}
	cfg := &config.Config{}
	svc := settingskit.NewComposite(repo, cfg)

	err := svc.Forwarded.LoadForwardedClientIPSettings(context.Background())

	require.ErrorContains(t, err, "load forwarded client ip headers")
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.False(t, runtimeSettings.TrustForwardedIP)
	require.Empty(t, runtimeSettings.Headers)
	require.Equal(t, "true", repo.updates[runtimeconfig.SettingKeyForwardedClientIPModeV2])
	require.NotContains(t, repo.updates, runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP)
}

// TestSettingService_LoadForwardedClientIPSettingsBackfillsConfigHeaders
// supplies a completed marker with no stored headers and an existing configured
// header. Loading backfills that header in the captured batch and retains it in
// runtime config.
func TestSettingService_LoadForwardedClientIPSettingsBackfillsConfigHeaders(t *testing.T) {
	repo := &forwardedIPMigrationRepoStub{values: map[string]string{
		runtimeconfig.SettingKeyForwardedClientIPModeV2: "true",
	}}
	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(false, []string{"X-Config-IP"})
	svc := settingskit.NewComposite(repo, cfg)

	require.NoError(t, svc.Forwarded.LoadForwardedClientIPSettings(context.Background()))
	require.JSONEq(t, `["X-Config-IP"]`, repo.updates[runtimeconfig.SettingKeyForwardedClientIPHeaders])
	require.Equal(t, []string{"X-Config-IP"}, cfg.ForwardedClientIPSettings().Headers)
}

// TestSettingService_LoadForwardedClientIPSettingsReadFailureFailsClosed
// injects a batch-read failure while runtime trust starts enabled. Loading
// reports the read error, disables trust, and clears the runtime custom header
// list.
func TestSettingService_LoadForwardedClientIPSettingsReadFailureFailsClosed(t *testing.T) {
	repo := &forwardedIPMigrationRepoStub{
		getMultipleErr: errors.New("database unavailable"),
	}
	cfg := &config.Config{}
	cfg.SetTrustForwardedHeaders(true)
	svc := settingskit.NewComposite(repo, cfg)

	err := svc.Forwarded.LoadForwardedClientIPSettings(context.Background())

	require.ErrorContains(t, err, "get forwarded client ip settings")
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.False(t, runtimeSettings.TrustForwardedIP)
	require.Empty(t, runtimeSettings.Headers)
}

// TestSettingService_LoadForwardedClientIPSettingsWriteFailureUsesComputedMode
// injects migration write failure with or without configured proxies. Loading
// reports the write error while runtime trust follows the respective computed
// compatibility or proxy policy.
func TestSettingService_LoadForwardedClientIPSettingsWriteFailureUsesComputedMode(t *testing.T) {
	tests := []struct {
		name              string // failed migration write case
		trustedProxiesSet bool   // proxy configuration supplied
		wantEnabled       bool   // expected computed runtime trust choice
	}{
		{name: "compatibility migration remains effective", wantEnabled: true},
		{name: "explicit proxy policy remains secure", trustedProxiesSet: true, wantEnabled: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &forwardedIPMigrationRepoStub{
				values:         map[string]string{runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "false"},
				setMultipleErr: errors.New("database unavailable"),
			}
			cfg := &config.Config{Server: config.ServerConfig{ClientIP: config.ServerClientIPConfig{TrustedProxiesConfigured: test.trustedProxiesSet}}}
			svc := settingskit.NewComposite(repo, cfg)

			err := svc.Forwarded.LoadForwardedClientIPSettings(context.Background())

			require.ErrorContains(t, err, "migrate forwarded client ip setting")
			require.Equal(t, test.wantEnabled, cfg.TrustForwardedHeaders())
		})
	}
}

// TestSettingService_GetAntigravityUserAgentVersion_Precedence reads supplied,
// empty, and absent stored version text. The configured version wins, while
// empty or missing values return the Antigravity default selected by the
// fixture.
func TestSettingService_GetAntigravityUserAgentVersion_Precedence(t *testing.T) {
	t.Run("后台设置优先", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingAntigravityUARepoStub{values: map[string]string{
			gateway.SettingKeyAntigravityUserAgentVersion: "1.24.0",
		}}, &config.Config{})

		require.Equal(t, "1.24.0", svc.Gateway.GetAntigravityUserAgentVersion(context.Background()))
	})

	t.Run("空值回退配置默认值", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingAntigravityUARepoStub{values: map[string]string{
			gateway.SettingKeyAntigravityUserAgentVersion: "",
		}}, &config.Config{})

		require.Equal(t, antigravity.GetDefaultUserAgentVersion(), svc.Gateway.GetAntigravityUserAgentVersion(context.Background()))
	})

	t.Run("缺失回退配置默认值", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingAntigravityUARepoStub{values: map[string]string{}}, &config.Config{})

		require.Equal(t, antigravity.GetDefaultUserAgentVersion(), svc.Gateway.GetAntigravityUserAgentVersion(context.Background()))
	})
}

// TestSettingService_GetOpenAICodexUserAgent_Precedence reads supplied, empty,
// and absent Codex User-Agent text. The configured value wins, while empty or
// missing values return the gateway's built-in default string.
func TestSettingService_GetOpenAICodexUserAgent_Precedence(t *testing.T) {
	t.Run("后台设置优先", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingAntigravityUARepoStub{values: map[string]string{
			gateway.SettingKeyOpenAICodexUserAgent: "codex-tui/9.9.9 test-terminal",
		}}, &config.Config{})

		require.Equal(t, "codex-tui/9.9.9 test-terminal", svc.Gateway.GetOpenAICodexUserAgent(context.Background()))
	})

	t.Run("空值回退内置默认值", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingAntigravityUARepoStub{values: map[string]string{
			gateway.SettingKeyOpenAICodexUserAgent: "",
		}}, &config.Config{})

		require.Equal(t, gateway.DefaultOpenAICodexUserAgent, svc.Gateway.GetOpenAICodexUserAgent(context.Background()))
	})

	t.Run("缺失回退内置默认值", func(t *testing.T) {
		svc := settingskit.NewComposite(&settingAntigravityUARepoStub{values: map[string]string{}}, &config.Config{})

		require.Equal(t, gateway.DefaultOpenAICodexUserAgent, svc.Gateway.GetOpenAICodexUserAgent(context.Background()))
	})
}

// TestSettingService_UpdateSettings_RejectsInvalidPaymentVisibleMethodSource
// supplies an unsupported payment-visible provider value. Save returns
// INVALID_PAYMENT_VISIBLE_METHOD_SOURCE and the capturing repository receives
// no prepared update batch for that input.
func TestSettingService_UpdateSettings_RejectsInvalidPaymentVisibleMethodSource(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		PaymentVisibleMethodAlipaySource: "not-a-provider",
	})
	require.Error(t, err)
	require.Equal(t, "INVALID_PAYMENT_VISIBLE_METHOD_SOURCE", apperror.Reason(err))
	require.Nil(t, repo.updates)
}
