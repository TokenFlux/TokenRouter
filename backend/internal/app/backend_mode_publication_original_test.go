//go:build unit

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	"github.com/stretchr/testify/require"
)

// delayedBackendReadFixture 只阻塞旧回源；综合管理读取仍通过原批量端口完成。
type delayedBackendReadFixture struct {
	*settingHandlerRepoStub
	entered, release chan struct{}
}

func (r *delayedBackendReadFixture) GetValue(ctx context.Context, key string) (string, error) {
	if key == admission.BackendModeKey {
		close(r.entered)
		<-r.release
		return "false", nil
	}
	return r.settingHandlerRepoStub.GetValue(ctx, key)
}

// TestSettingUpdateLateBackendModeLoad 使用真实综合 HTTP 和应用器，旧回源不能覆盖成功管理发布。
func TestSettingUpdateLateBackendModeLoad(t *testing.T) {
	repo := &delayedBackendReadFixture{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{}}, entered: make(chan struct{}), release: make(chan struct{})}
	store := settings.New(repo)
	cfg := &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}}
	backend := provideBackendMode(store)
	grants := provideGrantSettings(store, cfg, nil)
	defaults := provideSchedulerAdminDefaults(cfg)
	rules := provideGatewayAdminRules()
	gatewaySettings := provideGatewaySettings(store)
	shared := &schedulerSharedState{Settings: scheduler.NewSettingsRuntime(scheduler.Diagnostics{})}
	source := provideCompositeRuntime(store, cfg, provideCompositeReadOptions(cfg, provideOAuthSettings(store, cfg), rules, defaults), grants, gatewaySettings, rules, defaults, nil, backend, provideAccountSettings(store), provideQuotaSettings(store), provideForwardedSettings(store, cfg), shared, nil, nil)
	participants := staticSettingsParticipants(&payment.Runtime{}, grants, defaults, rules)
	participants[len(participants)-1] = payment.SettingsParticipant(nil)
	registry, err := settings.NewRegistry(participants...)
	require.NoError(t, err)
	handler := settingshttp.NewHandler(settingshttp.HandlerOptions{Settings: source, Participants: registry})
	done := make(chan struct{})
	go func() { backend.Enabled(context.Background()); close(done) }()
	<-repo.entered
	response := doUpdateSettings(t, handler, map[string]any{"backend_mode_enabled": true}, nil)
	close(repo.release)
	<-done
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "true", repo.values[admission.BackendModeKey])
	require.True(t, backend.Enabled(context.Background()), "旧回源覆盖管理员刚开启的 backend mode")
}
