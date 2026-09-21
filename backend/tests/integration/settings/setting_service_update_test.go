//go:build unit

package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"

	settingskit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"

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
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/stretchr/testify/require"
)

type settingUpdateRepoStub struct {
	updates        map[string]string
	values         map[string]string
	setMultipleErr error
}

func (s *settingUpdateRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *settingUpdateRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.values != nil {
		if value, ok := s.values[key]; ok {
			return value, nil
		}
	}
	return "", settingscore.ErrSettingNotFound
}

func (s *settingUpdateRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingUpdateRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *settingUpdateRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	s.updates = make(map[string]string, len(settings))
	for k, v := range settings {
		s.updates[k] = v
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[k] = v
	}
	return s.setMultipleErr
}

func (s *settingUpdateRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *settingUpdateRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

type settingGetAllRepoStub struct {
	values map[string]string
}

func (s *settingGetAllRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *settingGetAllRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

func (s *settingGetAllRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingGetAllRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *settingGetAllRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *settingGetAllRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *settingGetAllRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

type forwardedIPMigrationRepoStub struct {
	values         map[string]string
	updates        map[string]string
	getMultipleErr error
	setMultipleErr error
}

func (s *forwardedIPMigrationRepoStub) Get(context.Context, string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *forwardedIPMigrationRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return value, nil
}

func (s *forwardedIPMigrationRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

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

func (s *forwardedIPMigrationRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *forwardedIPMigrationRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

type settingAntigravityUARepoStub struct {
	values map[string]string
}

func (s *settingAntigravityUARepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *settingAntigravityUARepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", settingscore.ErrSettingNotFound
}

func (s *settingAntigravityUARepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingAntigravityUARepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *settingAntigravityUARepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *settingAntigravityUARepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *settingAntigravityUARepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

type defaultSubPlanReaderStub struct {
	byID  map[int64]*billing.SubscriptionPlan
	errBy map[int64]error
	calls []int64
}

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

// 页面功能开关必须独立持久化，避免保存其他设置时互相覆盖。
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

// TestSettingService_CreativeWorkerCountIsPersisted 验证 worker 数量随系统设置持久化。
func TestSettingService_CreativeWorkerCountIsPersisted(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{CreativeWorkerCount: 7})
	require.NoError(t, err)
	require.Equal(t, "7", repo.updates[creative.SettingKeyCreativeWorkerCount])
}

// TestSettingService_CreativeWorkerCountCallbackOnlyAfterWrite 验证失败写入不会改变运行时 worker 数量。
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

	repo.setMultipleErr = errors.New("database unavailable")
	require.Error(t, svc.Save(context.Background(), &composite.Snapshot{CreativeWorkerCount: 11}))
	require.Equal(t, 1, callbackCalls)
}

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

func TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Normalized(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		RegistrationEmailSuffixWhitelist: []string{"example.com", "@EXAMPLE.com", " @foo.bar ", "*.EDU.CN"},
	})
	require.NoError(t, err)
	require.Equal(t, `["@example.com","@foo.bar","*.edu.cn"]`, repo.updates[identity.SettingKeyRegistrationEmailSuffixWhitelist])
}

func TestSettingService_UpdateSettings_PersistsUserEmailChangeSwitch(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{UserEmailChangeEnabled: true})
	require.NoError(t, err)
	require.Equal(t, "true", repo.updates[identity.SettingKeyUserEmailChangeEnabled])
}

func TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Invalid(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		RegistrationEmailSuffixWhitelist: []string{"@invalid_domain"},
	})
	require.Error(t, err)
	require.Equal(t, "INVALID_REGISTRATION_EMAIL_SUFFIX_WHITELIST", apperror.Reason(err))
}

func TestParseDefaultSubscriptions_NormalizesValues(t *testing.T) {
	got := identity.ParseDefaultSubscriptions(`[{"plan_id":11},{"plan_id":11},{"plan_id":0},{"plan_id":12}]`)
	require.Equal(t, []identity.DefaultSubscriptionSetting{
		{PlanID: 11},
		{PlanID: 11},
		{PlanID: 12},
	}, got)
}

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

func TestSettingServiceRefreshCachedSettingsKeepsIndependentEWMAProcessDefaults(t *testing.T) {

	cfg := &config.Config{}
	cfg.Gateway.AdvancedScheduler.EWMAErrorRateAlpha = 0.35
	cfg.Gateway.AdvancedScheduler.EWMATTFTAlpha = 0.8
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, cfg)
	svc.Scheduler.Store(scheduler.RuntimeSettingsFromAdmin(scheduler.AdminSettings{}, svc.Defaults))

	runtime := svc.Scheduler.Load(context.Background(), nil, svc.Defaults.Process)
	require.InDelta(t, 0.35, runtime.EwmaErrorRateAlpha, 0.000001)
	require.InDelta(t, 0.8, runtime.EwmaTTFTAlpha, 0.000001)
}

func TestSettingService_UpdateSettings_AdvancedSchedulerWeightSums(t *testing.T) {
	maxFloat := strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64)
	tests := []struct {
		name    string
		weights composite.Snapshot
		wantErr bool
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

func TestSettingService_GetAllSettings_AdvancedSchedulerEffectiveValuesUseConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.AdvancedScheduler.LBTopK = 13
	cfg.Gateway.AdvancedScheduler.ScoreWeights = config.GatewayAdvancedSchedulerScoreWeights{
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

func TestSettingService_UpdateSettings_OpenAIQuotaAutoPauseMergesOpsAdvancedSettings(t *testing.T) {
	repo := &settingUpdateRepoStub{
		values: map[string]string{
			ops.SettingKeyOpsAdvancedSettings: `{"data_retention":{"cleanup_enabled":true,"cleanup_schedule":"0 3 * * *","error_log_retention_days":14,"minute_metrics_retention_days":7,"hourly_metrics_retention_days":30},"aggregation":{"aggregation_enabled":true},"openai_account_quota_auto_pause":{"default_threshold_5h":0.8,"default_threshold_7d":0.75},"ignore_count_tokens_errors":false,"ignore_context_canceled":true,"ignore_no_available_accounts":true,"ignore_invalid_api_key_errors":true,"ignore_insufficient_balance_errors":false,"display_openai_token_stats":true,"display_alert_events":false,"auto_refresh_enabled":true,"auto_refresh_interval_seconds":45}`,
		},
	}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		OpenAIQuotaAutoPauseSettingsSet: true,
		OpenAIQuotaAutoPauseSettings: ops.OpsOpenAIAccountQuotaAutoPauseSettings{
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
	require.Equal(t, 0.95, got.OpenAIAccountQuotaAutoPause.DefaultThreshold5h)
	require.Equal(t, 1.0, got.OpenAIAccountQuotaAutoPause.DefaultThreshold7d)
	require.Equal(t, got.OpenAIAccountQuotaAutoPause, svc.Quota.GetOpenAIQuotaAutoPauseSettings(context.Background()))
}

func TestSettingService_ParseSettings_OpenAIQuotaAutoPauseFromOpsAdvancedSettings(t *testing.T) {
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, &config.Config{})

	got := composite.Parse(map[string]string{
		ops.SettingKeyOpsAdvancedSettings: `{"openai_account_quota_auto_pause":{"default_threshold_5h":0.7,"default_threshold_7d":1.3}}`,
	}, svc.Read)

	require.Equal(t, 0.7, got.OpenAIQuotaAutoPauseSettings.DefaultThreshold5h)
	require.Equal(t, 1.0, got.OpenAIQuotaAutoPauseSettings.DefaultThreshold7d)
}

func TestSettingService_UpdateSettings_AntigravityUserAgentVersion(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		AntigravityUserAgentVersion: "1.23.2",
	})
	require.NoError(t, err)
	require.Equal(t, "1.23.2", repo.updates[gateway.SettingKeyAntigravityUserAgentVersion])
}

func TestSettingService_UpdateSettings_OpenAICodexUserAgent(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := settingskit.NewComposite(repo, &config.Config{})

	err := svc.Save(context.Background(), &composite.Snapshot{
		OpenAICodexUserAgent: " codex-tui/9.9.9 test-terminal ",
	})
	require.NoError(t, err)
	require.Equal(t, "codex-tui/9.9.9 test-terminal", repo.updates[gateway.SettingKeyOpenAICodexUserAgent])
}

func TestSettingService_InitializeDefaultSettingsPersistsConfiguredForwardedClientIPHeaders(t *testing.T) {
	repo := &forwardedIPMigrationRepoStub{values: map[string]string{}}
	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(true, []string{"X-Cdn-Ip", "True-Client-Ip"})
	svc := settingskit.NewComposite(repo, cfg)

	require.NoError(t, svc.Forwarded.LoadForwardedClientIPSettings(context.Background()))
	require.JSONEq(t, `["X-Cdn-Ip","True-Client-Ip"]`, repo.values[runtimeconfig.SettingKeyForwardedClientIPHeaders])
}

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

func TestSettingService_ParseSettings_APIKeyACLTrustForwardedIPFallsBackToConfigWhenMissing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.TrustForwardedIPForAPIKeyACL = true
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, cfg)

	got := composite.Parse(map[string]string{}, svc.Read)

	require.True(t, got.APIKeyACLTrustForwardedIP)
}

func TestSettingService_ParseSettings_APIKeyACLTrustForwardedIPUsesStoredValue(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetTrustForwardedIPForAPIKeyACL(true)
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, cfg)

	got := composite.Parse(map[string]string{runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "false"}, svc.Read)

	require.False(t, got.APIKeyACLTrustForwardedIP)
}

// 创作台开关与 TeamEnabled 同款"缺省 true"语义：键缺失时开启，显式 "false" 才关闭。
func TestSettingService_ParseSettings_CreativeEnabledDefaultsTrue(t *testing.T) {
	svc := settingskit.NewComposite(&settingUpdateRepoStub{}, &config.Config{})

	got := composite.Parse(map[string]string{}, svc.Read)
	require.True(t, got.CreativeEnabled)

	got = composite.Parse(map[string]string{creative.SettingKeyCreativeEnabled: "false"}, svc.Read)
	require.False(t, got.CreativeEnabled)
}

// TestSettingService_ParseSettings_CreativeWorkerCount 验证创作台 worker 设置的默认与脏值回退。
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

func TestSettingService_LoadForwardedClientIPSettingsMigration(t *testing.T) {
	tests := []struct {
		name                   string
		values                 map[string]string
		trustedProxiesSet      bool
		configDefault          bool
		wantEnabled            bool
		wantForwardedIPUpdate  string
		wantMigrationMarkerSet bool
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
			cfg := &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: test.trustedProxiesSet}}
			cfg.Security.TrustForwardedIPForAPIKeyACL = test.configDefault
			svc := settingskit.NewComposite(repo, cfg)

			require.NoError(t, svc.Forwarded.LoadForwardedClientIPSettings(context.Background()))
			require.Equal(t, test.wantEnabled, cfg.TrustForwardedIPForAPIKeyACL())
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

func TestSettingService_LoadForwardedClientIPSettingsReadFailureFailsClosed(t *testing.T) {
	repo := &forwardedIPMigrationRepoStub{
		getMultipleErr: errors.New("database unavailable"),
	}
	cfg := &config.Config{}
	cfg.SetTrustForwardedIPForAPIKeyACL(true)
	svc := settingskit.NewComposite(repo, cfg)

	err := svc.Forwarded.LoadForwardedClientIPSettings(context.Background())

	require.ErrorContains(t, err, "get forwarded client ip settings")
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.False(t, runtimeSettings.TrustForwardedIP)
	require.Empty(t, runtimeSettings.Headers)
}

func TestSettingService_LoadForwardedClientIPSettingsWriteFailureUsesComputedMode(t *testing.T) {
	tests := []struct {
		name              string
		trustedProxiesSet bool
		wantEnabled       bool
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
			cfg := &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: test.trustedProxiesSet}}
			svc := settingskit.NewComposite(repo, cfg)

			err := svc.Forwarded.LoadForwardedClientIPSettings(context.Background())

			require.ErrorContains(t, err, "migrate forwarded client ip setting")
			require.Equal(t, test.wantEnabled, cfg.TrustForwardedIPForAPIKeyACL())
		})
	}
}

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
