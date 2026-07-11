//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/antigravity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type settingUpdateRepoStub struct {
	updates map[string]string
	values  map[string]string
}

func (s *settingUpdateRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *settingUpdateRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.values != nil {
		if value, ok := s.values[key]; ok {
			return value, nil
		}
	}
	return "", ErrSettingNotFound
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
	return nil
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

func (s *settingGetAllRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
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

type settingAntigravityUARepoStub struct {
	values map[string]string
}

func (s *settingAntigravityUARepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *settingAntigravityUARepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
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
	byID  map[int64]*SubscriptionPlan
	errBy map[int64]error
	calls []int64
}

func (s *defaultSubPlanReaderStub) GetByID(ctx context.Context, id int64) (*SubscriptionPlan, error) {
	s.calls = append(s.calls, id)
	if err, ok := s.errBy[id]; ok {
		return nil, err
	}
	if plan, ok := s.byID[id]; ok {
		return plan, nil
	}
	return nil, ErrSubscriptionNotFound
}

func TestSettingService_UpdateSettings_DefaultSubscriptions_ValidPlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		byID: map[int64]*SubscriptionPlan{
			11: {ID: 11, Name: "Monthly"},
		},
	}
	svc := NewSettingService(repo, &config.Config{})
	svc.SetDefaultSubscriptionPlanReader(planReader)

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DefaultSubscriptions: []DefaultSubscriptionSetting{
			{PlanID: 11},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{11}, planReader.calls)

	raw, ok := repo.updates[SettingKeyDefaultSubscriptions]
	require.True(t, ok)

	var got []DefaultSubscriptionSetting
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Equal(t, []DefaultSubscriptionSetting{
		{PlanID: 11},
	}, got)
}

func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsMissingPlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		errBy: map[int64]error{
			12: ErrSubscriptionNotFound,
		},
	}
	svc := NewSettingService(repo, &config.Config{})
	svc.SetDefaultSubscriptionPlanReader(planReader)

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DefaultSubscriptions: []DefaultSubscriptionSetting{
			{PlanID: 12},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_INVALID", infraerrors.Reason(err))
	require.Nil(t, repo.updates)
}

func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsNotFoundPlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		errBy: map[int64]error{
			13: infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found"),
		},
	}
	svc := NewSettingService(repo, &config.Config{})
	svc.SetDefaultSubscriptionPlanReader(planReader)

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DefaultSubscriptions: []DefaultSubscriptionSetting{
			{PlanID: 13},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_INVALID", infraerrors.Reason(err))
	require.Equal(t, "13", infraerrors.FromError(err).Metadata["plan_id"])
	require.Nil(t, repo.updates)
}

func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsDuplicatePlan(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	planReader := &defaultSubPlanReaderStub{
		byID: map[int64]*SubscriptionPlan{
			11: {ID: 11, Name: "Monthly"},
		},
	}
	svc := NewSettingService(repo, &config.Config{})
	svc.SetDefaultSubscriptionPlanReader(planReader)

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DefaultSubscriptions: []DefaultSubscriptionSetting{
			{PlanID: 11},
			{PlanID: 11},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_DUPLICATE", infraerrors.Reason(err))
	require.Equal(t, "11", infraerrors.FromError(err).Metadata["plan_id"])
	require.Nil(t, repo.updates)
}

func TestSettingService_UpdateSettings_DefaultSubscriptions_RejectsDuplicatePlanWithoutReader(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		DefaultSubscriptions: []DefaultSubscriptionSetting{
			{PlanID: 11},
			{PlanID: 11},
		},
	})
	require.Error(t, err)
	require.Equal(t, "DEFAULT_SUBSCRIPTION_PLAN_DUPLICATE", infraerrors.Reason(err))
	require.Equal(t, "11", infraerrors.FromError(err).Metadata["plan_id"])
	require.Nil(t, repo.updates)
}

func TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Normalized(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		RegistrationEmailSuffixWhitelist: []string{"example.com", "@EXAMPLE.com", " @foo.bar ", "*.EDU.CN"},
	})
	require.NoError(t, err)
	require.Equal(t, `["@example.com","@foo.bar","*.edu.cn"]`, repo.updates[SettingKeyRegistrationEmailSuffixWhitelist])
}

func TestSettingService_UpdateSettings_RegistrationEmailSuffixWhitelist_Invalid(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		RegistrationEmailSuffixWhitelist: []string{"@invalid_domain"},
	})
	require.Error(t, err)
	require.Equal(t, "INVALID_REGISTRATION_EMAIL_SUFFIX_WHITELIST", infraerrors.Reason(err))
}

func TestParseDefaultSubscriptions_NormalizesValues(t *testing.T) {
	got := parseDefaultSubscriptions(`[{"plan_id":11},{"plan_id":11},{"plan_id":0},{"plan_id":12}]`)
	require.Equal(t, []DefaultSubscriptionSetting{
		{PlanID: 11},
		{PlanID: 11},
		{PlanID: 12},
	}, got)
}

func TestSettingService_UpdateSettings_TablePreferences(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		TableDefaultPageSize: 50,
		TablePageSizeOptions: []int{20, 50, 100},
	})
	require.NoError(t, err)
	require.Equal(t, "50", repo.updates[SettingKeyTableDefaultPageSize])
	require.Equal(t, "[20,50,100]", repo.updates[SettingKeyTablePageSizeOptions])

	err = svc.UpdateSettings(context.Background(), &SystemSettings{
		TableDefaultPageSize: 1000,
		TablePageSizeOptions: []int{20, 100},
	})
	require.NoError(t, err)
	require.Equal(t, "1000", repo.updates[SettingKeyTableDefaultPageSize])
	require.Equal(t, "[20,100]", repo.updates[SettingKeyTablePageSizeOptions])
}

func TestSettingService_UpdateSettings_PaymentVisibleMethodsAndAdvancedScheduler(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		PaymentVisibleMethodAlipaySource:                   "alipay",
		PaymentVisibleMethodWxpaySource:                    "easypay",
		PaymentVisibleMethodAlipayEnabled:                  true,
		PaymentVisibleMethodWxpayEnabled:                   false,
		OpenAIAdvancedSchedulerEnabled:                     true,
		OpenAIAdvancedSchedulerStickyWeightedEnabled:       true,
		OpenAIAdvancedSchedulerSubscriptionPriorityEnabled: true,
		OpenAIAdvancedSchedulerLBTopK:                      " 3 ",
		OpenAIAdvancedSchedulerWeightPriority:              "2.50",
		OpenAIAdvancedSchedulerWeightLoad:                  "0",
		OpenAIAdvancedSchedulerWeightQueue:                 "0.75",
		OpenAIAdvancedSchedulerWeightErrorRate:             "1.25",
		OpenAIAdvancedSchedulerWeightTTFT:                  "0.5",
		OpenAIAdvancedSchedulerWeightReset:                 "",
		OpenAIAdvancedSchedulerWeightQuotaHeadroom:         "0.2",
		OpenAIAdvancedSchedulerWeightPreviousResponse:      "8",
		OpenAIAdvancedSchedulerWeightSessionSticky:         "4",
	})
	require.NoError(t, err)
	require.Equal(t, VisibleMethodSourceOfficialAlipay, repo.updates[SettingPaymentVisibleMethodAlipaySource])
	require.Equal(t, VisibleMethodSourceEasyPayWechat, repo.updates[SettingPaymentVisibleMethodWxpaySource])
	require.Equal(t, "true", repo.updates[SettingPaymentVisibleMethodAlipayEnabled])
	require.Equal(t, "false", repo.updates[SettingPaymentVisibleMethodWxpayEnabled])
	require.Equal(t, "true", repo.updates[openAIAdvancedSchedulerSettingKey])
	require.Equal(t, "true", repo.updates[SettingKeyOpenAIAdvancedSchedulerStickyWeightedEnabled])
	require.Equal(t, "true", repo.updates[SettingKeyOpenAIAdvancedSchedulerSubscriptionPriorityEnabled])
	require.Equal(t, "3", repo.updates[SettingKeyOpenAIAdvancedSchedulerLBTopK])
	require.Equal(t, "2.5", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightPriority])
	require.Equal(t, "0", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightLoad])
	require.Equal(t, "0.75", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightQueue])
	require.Equal(t, "1.25", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightErrorRate])
	require.Equal(t, "0.5", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightTTFT])
	require.Equal(t, "", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightReset])
	require.Equal(t, "0.2", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightQuotaHeadroom])
	require.Equal(t, "8", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightPreviousResponse])
	require.Equal(t, "4", repo.updates[SettingKeyOpenAIAdvancedSchedulerWeightSessionSticky])
}

func TestSettingService_GetAllSettings_OpenAIAdvancedSchedulerEffectiveValuesUseConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 13
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights = config.GatewayOpenAIWSSchedulerScoreWeights{
		Priority:         2,
		Load:             3,
		Queue:            4,
		ErrorRate:        5,
		TTFT:             6,
		Reset:            7,
		QuotaHeadroom:    8,
		PreviousResponse: 9,
		SessionSticky:    10,
	}
	svc := NewSettingService(&settingGetAllRepoStub{values: map[string]string{
		SettingKeyOpenAIAdvancedSchedulerLBTopK:              "3",
		SettingKeyOpenAIAdvancedSchedulerWeightPriority:      "99",
		SettingKeyOpenAIAdvancedSchedulerWeightSessionSticky: "88",
	}}, cfg)

	settings, err := svc.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "3", settings.OpenAIAdvancedSchedulerLBTopK)
	require.Equal(t, "99", settings.OpenAIAdvancedSchedulerWeightPriority)
	require.Equal(t, "88", settings.OpenAIAdvancedSchedulerWeightSessionSticky)
	require.Equal(t, "13", settings.OpenAIAdvancedSchedulerEffectiveLBTopK)
	require.Equal(t, "2", settings.OpenAIAdvancedSchedulerEffectiveWeightPriority)
	require.Equal(t, "3", settings.OpenAIAdvancedSchedulerEffectiveWeightLoad)
	require.Equal(t, "10", settings.OpenAIAdvancedSchedulerEffectiveWeightSessionSticky)
}

func TestSettingService_UpdateSettings_OpenAIQuotaAutoPauseMergesOpsAdvancedSettings(t *testing.T) {
	repo := &settingUpdateRepoStub{
		values: map[string]string{
			SettingKeyOpsAdvancedSettings: `{"data_retention":{"cleanup_enabled":true,"cleanup_schedule":"0 3 * * *","error_log_retention_days":14,"minute_metrics_retention_days":7,"hourly_metrics_retention_days":30},"aggregation":{"aggregation_enabled":true},"openai_account_quota_auto_pause":{"default_threshold_5h":0.8,"default_threshold_7d":0.75},"ignore_count_tokens_errors":false,"ignore_context_canceled":true,"ignore_no_available_accounts":true,"ignore_invalid_api_key_errors":true,"ignore_insufficient_balance_errors":false,"display_openai_token_stats":true,"display_alert_events":false,"auto_refresh_enabled":true,"auto_refresh_interval_seconds":45}`,
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		OpenAIQuotaAutoPauseSettingsSet: true,
		OpenAIQuotaAutoPauseSettings: OpsOpenAIAccountQuotaAutoPauseSettings{
			DefaultThreshold5h: 0.95,
			DefaultThreshold7d: 1.2,
		},
	})
	require.NoError(t, err)

	var got OpsAdvancedSettings
	require.NoError(t, json.Unmarshal([]byte(repo.updates[SettingKeyOpsAdvancedSettings]), &got))
	require.Equal(t, true, got.DataRetention.CleanupEnabled)
	require.Equal(t, "0 3 * * *", got.DataRetention.CleanupSchedule)
	require.Equal(t, 14, got.DataRetention.ErrorLogRetentionDays)
	require.True(t, got.Aggregation.AggregationEnabled)
	require.False(t, got.IgnoreCountTokensErrors)
	require.True(t, got.DisplayOpenAITokenStats)
	require.False(t, got.DisplayAlertEvents)
	require.True(t, got.AutoRefreshEnabled)
	require.Equal(t, 45, got.AutoRefreshIntervalSec)
	require.Equal(t, 0.95, got.OpenAIAccountQuotaAutoPause.DefaultThreshold5h)
	require.Equal(t, 1.0, got.OpenAIAccountQuotaAutoPause.DefaultThreshold7d)
	require.Equal(t, got.OpenAIAccountQuotaAutoPause, svc.GetOpenAIQuotaAutoPauseSettings(context.Background()))
}

func TestSettingService_ParseSettings_OpenAIQuotaAutoPauseFromOpsAdvancedSettings(t *testing.T) {
	svc := NewSettingService(&settingUpdateRepoStub{}, &config.Config{})

	got := svc.parseSettings(map[string]string{
		SettingKeyOpsAdvancedSettings: `{"openai_account_quota_auto_pause":{"default_threshold_5h":0.7,"default_threshold_7d":1.3}}`,
	})

	require.Equal(t, 0.7, got.OpenAIQuotaAutoPauseSettings.DefaultThreshold5h)
	require.Equal(t, 1.0, got.OpenAIQuotaAutoPauseSettings.DefaultThreshold7d)
}

func TestSettingService_UpdateSettings_AntigravityUserAgentVersion(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		AntigravityUserAgentVersion: "1.23.2",
	})
	require.NoError(t, err)
	require.Equal(t, "1.23.2", repo.updates[SettingKeyAntigravityUserAgentVersion])
}

func TestSettingService_UpdateSettings_OpenAICodexUserAgent(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		OpenAICodexUserAgent: " codex-tui/9.9.9 test-terminal ",
	})
	require.NoError(t, err)
	require.Equal(t, "codex-tui/9.9.9 test-terminal", repo.updates[SettingKeyOpenAICodexUserAgent])
}

func TestSettingService_UpdateSettings_APIKeyACLTrustForwardedIPRefreshesConfig(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	cfg := &config.Config{}
	svc := NewSettingService(repo, cfg)

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		APIKeyACLTrustForwardedIP: true,
	})
	require.NoError(t, err)
	require.Equal(t, "true", repo.updates[SettingKeyAPIKeyACLTrustForwardedIP])
	require.True(t, cfg.Security.TrustForwardedIPForAPIKeyACL)
	require.True(t, cfg.TrustForwardedIPForAPIKeyACL())
}

func TestSettingService_ParseSettings_APIKeyACLTrustForwardedIPFallsBackToConfigWhenMissing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.TrustForwardedIPForAPIKeyACL = true
	svc := NewSettingService(&settingUpdateRepoStub{}, cfg)

	got := svc.parseSettings(map[string]string{})

	require.True(t, got.APIKeyACLTrustForwardedIP)
}

func TestSettingService_GetAntigravityUserAgentVersion_Precedence(t *testing.T) {
	t.Run("后台设置优先", func(t *testing.T) {
		svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
			SettingKeyAntigravityUserAgentVersion: "1.24.0",
		}}, &config.Config{})

		require.Equal(t, "1.24.0", svc.GetAntigravityUserAgentVersion(context.Background()))
	})

	t.Run("空值回退配置默认值", func(t *testing.T) {
		svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
			SettingKeyAntigravityUserAgentVersion: "",
		}}, &config.Config{})

		require.Equal(t, antigravity.GetDefaultUserAgentVersion(), svc.GetAntigravityUserAgentVersion(context.Background()))
	})

	t.Run("缺失回退配置默认值", func(t *testing.T) {
		svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{}}, &config.Config{})

		require.Equal(t, antigravity.GetDefaultUserAgentVersion(), svc.GetAntigravityUserAgentVersion(context.Background()))
	})
}

func TestSettingService_GetOpenAICodexUserAgent_Precedence(t *testing.T) {
	t.Run("后台设置优先", func(t *testing.T) {
		svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
			SettingKeyOpenAICodexUserAgent: "codex-tui/9.9.9 test-terminal",
		}}, &config.Config{})

		require.Equal(t, "codex-tui/9.9.9 test-terminal", svc.GetOpenAICodexUserAgent(context.Background()))
	})

	t.Run("空值回退内置默认值", func(t *testing.T) {
		svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{
			SettingKeyOpenAICodexUserAgent: "",
		}}, &config.Config{})

		require.Equal(t, DefaultOpenAICodexUserAgent, svc.GetOpenAICodexUserAgent(context.Background()))
	})

	t.Run("缺失回退内置默认值", func(t *testing.T) {
		svc := NewSettingService(&settingAntigravityUARepoStub{values: map[string]string{}}, &config.Config{})

		require.Equal(t, DefaultOpenAICodexUserAgent, svc.GetOpenAICodexUserAgent(context.Background()))
	})
}

func TestSettingService_UpdateSettings_RejectsInvalidPaymentVisibleMethodSource(t *testing.T) {
	repo := &settingUpdateRepoStub{}
	svc := NewSettingService(repo, &config.Config{})

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		PaymentVisibleMethodAlipaySource: "not-a-provider",
	})
	require.Error(t, err)
	require.Equal(t, "INVALID_PAYMENT_VISIBLE_METHOD_SOURCE", infraerrors.Reason(err))
	require.Nil(t, repo.updates)
}
