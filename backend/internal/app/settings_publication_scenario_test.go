package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/bstats"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
)

// A backend-mode load started before an administrator update can finish after
// the new value has been published. The scenario holds that earlier single-key
// read until combined settings saves true, then checks both persisted settings
// and subsequent cached admission retain the administrator value.
//
// The delayed fixture announces entry and returns a fixed false value only when
// the test releases it. Administrator reads use its embedded batch repository
// and continue while that single-key call is blocked. Joining the load before
// the final assertion makes this order explicit, exposing whether an older
// result can replace the later publication.

// delayedBackendReadFixture delays one backend-mode read while allowing batch
// settings access.
type delayedBackendReadFixture struct {
	*settingHandlerRepoStub // batch settings fixture
	// entered marks the blocked old read, while release is closed by the test
	// after publication. Both are initialized before loading, and the old-read branch owns closing entered once.
	entered, release chan struct{} // entered: old-read start signal; release: old-read continuation signal
}

// GetValue announces and blocks the backend-mode key before returning the old
// false value. Initialize fresh entered/release channels and invoke that branch
// once; the test closes release after publishing newer settings.
//
// The blocked branch ignores ctx cancellation so the older result can complete
// after the update. Other keys delegate to the embedded repository, letting
// normal settings access retain that fixture's values and missing-key behavior.
func (r *delayedBackendReadFixture) GetValue(ctx context.Context, key string) (string, error) {
	if key == admission.BackendModeKey {
		// Announce the old load before blocking so the test can publish true
		// through batch settings first, then release false to exercise stale-read rejection deterministically.
		close(r.entered)
		<-r.release
		return "false", nil
	}
	return r.settingHandlerRepoStub.GetValue(ctx, key)
}

// TestSettingUpdateLateBackendModeLoad delays an earlier backend read while
// combined settings publishes true. It checks successful HTTP persistence, the
// stored true value, and a later Enabled read that retains the publication
// after the old load finishes.
func TestSettingUpdateLateBackendModeLoad(t *testing.T) {
	repo := &delayedBackendReadFixture{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{}}, entered: make(chan struct{}), release: make(chan struct{})}
	store := settings.New(repo)
	cfg := &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}}
	backend := provideBackendMode(store)
	grants := provideGrantSettings(store, cfg, nil)
	defaults := provideSchedulerAdminDefaults(cfg)
	rules := provideGatewayAdminRules()
	gatewaySettings := provideGatewaySettings(store)
	shared := &schedulerSharedState{Settings: scheduler.NewSettingsRuntime(scheduler.Diagnostics{})}
	source := provideCompositeRuntime(store, cfg, provideCompositeReadOptions(cfg, provideOAuthSettings(store, cfg), rules, defaults, nil), grants, gatewaySettings, rules, defaults, nil, backend, provideProviderSettings(store), provideQuotaSettings(store), provideForwardedSettings(store, cfg), shared, nil, nil, bstats.New(store, nil, nil))
	participants := staticSettingsParticipants(&payment.Runtime{}, grants, defaults, rules)
	participants[len(participants)-1] = payment.SettingsParticipant(nil)
	registry, err := settings.NewRegistry(participants...)
	require.NoError(t, err)
	handler := settingshttp.NewHandler(settingshttp.HandlerOptions{Settings: source, Participants: registry})
	done := make(chan struct{})
	// Run the old load independently and join it after settings publication.
	// The entered/release signals select the ordering while done protects the final cached-value assertion.
	go func() { backend.Enabled(context.Background()); close(done) }()
	<-repo.entered
	response := doUpdateSettings(t, handler, map[string]any{"backend_mode_enabled": true}, nil)
	close(repo.release)
	<-done
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "true", repo.values[admission.BackendModeKey])
	require.True(t, backend.Enabled(context.Background()), "旧回源覆盖管理员刚开启的 backend mode")
}
