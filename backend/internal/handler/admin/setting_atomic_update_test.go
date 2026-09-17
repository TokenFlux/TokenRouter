package admin

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 验证后段校验失败时前段配置是否已经写入。
func TestSettingsRejectedFastPolicyHasNoWrites(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeySiteName: "before"})
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "after", "openai_fast_policy_settings": map[string]any{"rules": []map[string]any{{"service_tier": "priority", "action": "bogus", "scope": "all"}}}}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "before", repo.values[service.SettingKeySiteName], "后段校验拒绝后不应保存站点名称")
}

// settingAtomicRepo 区分写入失败和提交后的回读失败，确保 HTTP 不掩盖持久化边界。
type settingAtomicRepo struct {
	*settingHandlerRepoStub
	writes    int
	failWrite bool
	failApply bool
}

func (r *settingAtomicRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	r.writes++
	if r.failWrite {
		return errors.New("settings write failed")
	}
	return r.settingHandlerRepoStub.SetMultiple(ctx, values)
}

func (r *settingAtomicRepo) GetAll(ctx context.Context) (map[string]string, error) {
	if r.failApply && r.writes > 0 {
		return nil, errors.New("settings reload failed")
	}
	return r.settingHandlerRepoStub.GetAll(ctx)
}

func TestSettingsCombinedUpdateCommitsOnce(t *testing.T) {
	repo := &settingAtomicRepo{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{service.SettingKeySiteName: "before"}}}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	payment := service.NewPaymentConfigService(nil, repo, nil)
	h := NewSettingHandler(svc, nil, nil, nil, payment, nil, nil)
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "after", "payment_enabled": true, "openai_fast_policy_settings": map[string]any{"rules": []any{}}}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, repo.writes)
	require.Equal(t, "after", repo.values[service.SettingKeySiteName])
	require.Equal(t, "true", repo.values[service.SettingPaymentEnabled])
	require.Equal(t, `{"rules":[]}`, repo.values[service.SettingKeyOpenAIFastPolicySettings])
}

func TestSettingsCombinedWriteAndApplyErrors(t *testing.T) {
	for _, scenario := range []string{"write", "apply"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &settingAtomicRepo{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{service.SettingKeySiteName: "before"}}, failWrite: scenario == "write", failApply: scenario == "apply"}
			svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
			h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
			notified := false
			svc.SetOnUpdateCallback(func() { notified = true })
			rec := doUpdateSettings(t, h, map[string]any{"site_name": "after"}, nil)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			require.False(t, notified)
			if scenario == "write" {
				require.Equal(t, "before", repo.values[service.SettingKeySiteName])
				return
			}
			require.Equal(t, "after", repo.values[service.SettingKeySiteName])
			require.Contains(t, rec.Body.String(), "SETTINGS_APPLY_FAILED")
			require.Contains(t, rec.Body.String(), "persisted")
		})
	}
}
