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

type searchChannelPolicy struct{ channels *routing.ChannelService }

func (p searchChannelPolicy) Enabled(ctx context.Context, groupID int64, platform string) (bool, error) {
	channel, err := p.channels.GetChannelForGroup(ctx, groupID)
	if err != nil || channel == nil {
		return false, err
	}
	return channel.IsWebSearchEmulationEnabled(platform), nil
}

// NewSearchTools 只装配同步工具编排；配置、渠道缓存、Manager 与配额仍由原拥有者持有。
func NewSearchTools(runtime *search.ConfigService, channels *routing.ChannelService) *searchtools.Emulator {
	var registry *search.Registry
	var settings searchtools.Settings
	if runtime != nil {
		registry = runtime.Registry()
		settings = runtime
	}
	var policy searchtools.ChannelPolicy
	if channels != nil {
		policy = searchChannelPolicy{channels}
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
