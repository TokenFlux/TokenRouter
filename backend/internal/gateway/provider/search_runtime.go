package provider

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/google/uuid"
)

// searchSource 引用配置运行时拥有的同一个注册表，避免另建全局 Manager 指针。
type searchSource struct{ registry *search.Registry }

func (s searchSource) Current() searchtools.Searcher {
	if s.registry == nil {
		return nil
	}
	manager := s.registry.Get()
	if manager == nil {
		return nil
	}
	return manager
}

type searchGroupPolicy struct{ groupPolicies *routing.PricingConfigService }

func (p searchGroupPolicy) Enabled(ctx context.Context, groupID int64, platform string) (bool, error) {
	policy, err := p.groupPolicies.GetGroupPolicy(ctx, groupID)
	if err != nil || policy == nil {
		return false, err
	}
	return policy.IsWebSearchEmulationEnabled(platform), nil
}

// NewSearchTools 只装配同步工具编排；分组策略、Manager 与配额分别从所属端口读取。
func NewSearchTools(runtime *search.ConfigService, groupPolicies *routing.PricingConfigService) *searchtools.Emulator {
	var registry *search.Registry
	var settings searchtools.Settings
	if runtime != nil {
		registry = runtime.Registry()
		settings = runtime
	}
	var policy searchtools.GroupPolicy
	if groupPolicies != nil {
		policy = searchGroupPolicy{groupPolicies}
	}
	return searchtools.NewEmulator(searchSource{registry}, settings, policy, time.Now, func() string { return uuid.New().String() }, observeSearch)
}

// SearchAccountMode 保留历史布尔输入的原诊断字段和级别。
func SearchAccountMode(value *searchtools.AccountPolicy) string {
	selection := searchtools.AccountMode(value)
	if selection.LegacyBool != nil {
		slog.Debug("legacy bool web_search_emulation value", "account_id", value.ID, "value", *selection.LegacyBool)
	}
	return selection.Mode
}

func observeSearch(e searchtools.Event) {
	switch e.Kind {
	case "executing":
		slog.Info("web search emulation: executing search", "account_id", e.AccountID, "account_name", e.AccountName, "query", e.Query)
	case "completed":
		slog.Info("web search emulation: search completed", "provider", e.Provider, "results_count", e.Results)
	case "search_failed":
		slog.Error("web search emulation: search failed", "error", e.Err)
	case "write_failed":
		slog.Warn("web search emulation: SSE write failed, stopping", "error", e.Err)
	}
}
