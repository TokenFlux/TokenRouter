//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type settingUpdateRepoStub struct {
	updates map[string]string
}

func (s *settingUpdateRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *settingUpdateRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
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
	}
	return nil
}

func (s *settingUpdateRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *settingUpdateRepoStub) Delete(ctx context.Context, key string) error {
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
		RegistrationEmailSuffixWhitelist: []string{"example.com", "@EXAMPLE.com", " @foo.bar "},
	})
	require.NoError(t, err)
	require.Equal(t, `["@example.com","@foo.bar"]`, repo.updates[SettingKeyRegistrationEmailSuffixWhitelist])
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
