// 旧网关搜索入口只投影账号和请求，工具规则与合成输出由 gateway/searchtools 唯一实现。
package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const featureKeyWebSearchEmulation = searchtools.FeatureKey

func SetWebSearchManager(m *search.Manager) { WebSearchRegistry().Set(m) }

type gatewaySearchSource struct{}

func (gatewaySearchSource) Current() searchtools.Searcher {
	manager := WebSearchRegistry().Get()
	if manager == nil {
		return nil
	}
	return manager
}

type gatewaySearchChannelPolicy struct{ service *ChannelService }

func (p gatewaySearchChannelPolicy) Enabled(ctx context.Context, groupID int64, platform string) (bool, error) {
	channel, err := p.service.GetChannelForGroup(ctx, groupID)
	if err != nil || channel == nil {
		return false, err
	}
	return channel.IsWebSearchEmulationEnabled(platform), nil
}
func observeGatewaySearch(e searchtools.Event) {
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

// SearchToolsRuntime 是供 app 固定绑定的无状态工厂，引用唯一 search 注册表与动态设置。
func (s *GatewayService) SearchToolsRuntime() *searchtools.Emulator {
	if s.searchToolsRuntime != nil {
		return s.searchToolsRuntime
	}
	var channels searchtools.ChannelPolicy
	if s.channelService != nil {
		channels = gatewaySearchChannelPolicy{s.channelService}
	}
	return searchtools.NewEmulator(gatewaySearchSource{}, s.settingService, channels, time.Now, func() string { return uuid.New().String() }, observeGatewaySearch)
}
func (s *GatewayService) shouldEmulateWebSearch(ctx context.Context, account *Account, groupID *int64, body []byte) bool {
	return s.SearchToolsRuntime().ShouldEmulate(ctx, searchtools.PolicyInput{Body: body, Mode: account.GetWebSearchEmulationMode(), Platform: account.Platform, GroupID: groupID})
}
func (s *GatewayService) handleWebSearchEmulation(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest) (*ForwardResult, error) {
	result, err := s.SearchToolsRuntime().Execute(ctx, searchtools.Request{
		Body:        parsed.Body.Bytes(),
		Model:       parsed.Model,
		Stream:      parsed.Stream,
		AccountID:   account.ID,
		AccountName: account.Name,
		ProxyURL:    resolveAccountProxyURL(account),
		OnAccepted:  parsed.OnUpstreamAccepted,
	}, gatewayhttp.SearchOutput{Context: c})
	if err != nil {
		var proxy *searchtools.ProxyFailure
		if errors.As(err, &proxy) {
			return nil, &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(err.Error())}
		}
		return nil, err
	}
	return &ForwardResult{Model: result.Model, Duration: result.Duration, Usage: result.Usage}, nil
}

// 该投影仍被 Live 传输使用；不改变代理缺失时的原返回值。
func resolveAccountProxyURL(account *Account) string {
	if account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

// BindSearchToolsRuntime 仅在开放请求前由 app 绑定，之后保持同一无状态编排器。
func (s *GatewayService) BindSearchToolsRuntime(runtime *searchtools.Emulator) {
	s.searchToolsRuntime = runtime
}
