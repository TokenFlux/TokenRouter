package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// provider_scoring.go resolves administrator scoring inputs from deployment
// defaults, runtime settings, and eligible group overrides. Administrator score
// snapshots share the same feedback, runtime settings, and parameter defaults
// as provider selection. Group overrides are read only for advanced groups,
// keeping basic group display independent of retained advanced settings.
//
// Shared Parameters are reused when already constructed; otherwise a Parameters
// instance is built from schedulerParameterDefaults and the supplied settings
// store. A nil callback context becomes Background so runtime settings reads
// always receive a usable context.

// providerScoreOptions binds administrative scoring to shared feedback and
// effective scheduler parameters. Shared Parameters are reused when present and
// otherwise constructed from schedulerParameterDefaults; group overrides apply
// only to advanced groups, and nil callback contexts become Background.
func providerScoreOptions(concurrency *scheduler.ConcurrencyService, shared *schedulerSharedState, store *settings.Store, cfg *config.Config) provider.SchedulerScoreOptions {
	var parameters *scheduler.Parameters
	if shared != nil && shared.Parameters != nil {
		parameters = shared.Parameters
	} else {
		var settingsRuntime *scheduler.SettingsRuntime
		if shared != nil {
			settingsRuntime = shared.Settings
		}
		var source scheduler.RuntimeSettingSource
		if store != nil {
			source = store
		}
		parameters = scheduler.NewParameters(settingsRuntime, source, schedulerParameterDefaults(cfg))
	}
	var feedback *scheduler.RuntimeStats
	if shared != nil {
		feedback = shared.Feedback
	}
	return provideradapter.SchedulerScoreOptions(concurrency, feedback, func(ctx context.Context, group *accessview.GroupConfig) policy.EffectiveSettings {
		if ctx == nil {
			ctx = context.Background()
		}
		var overrides policy.GroupAdvancedSchedulerOverrides
		// Basic groups can retain old advanced configuration. Admit overrides
		// only for an advanced group so those retained fields cannot alter its basic score display.
		if group != nil && group.SchedulerType == "advanced" {
			overrides = group.AdvancedSchedulerOverrides
		}
		return parameters.Effective(ctx, overrides)
	})
}
