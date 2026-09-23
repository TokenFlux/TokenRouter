// 旧网关搜索入口只投影账号和请求，工具规则与合成输出由 gateway/searchtools 唯一实现。
package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/search"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/gin-gonic/gin"
)

// SearchToolsRuntime 是供 app 固定绑定的无状态工厂，引用唯一 search 注册表与动态设置。
func (s *GatewayService) SearchToolsRuntime() *searchtools.Emulator {
	if s.searchToolsRuntime != nil {
		return s.searchToolsRuntime
	}
	var runtime *search.ConfigService
	if s.settingService != nil {
		runtime = s.settingService.Search
	}
	return gatewayprovider.NewSearchTools(runtime, s.channelService)
}
func (s *GatewayService) shouldEmulateWebSearch(ctx context.Context, account *gatewayprovider.ExecutionAccount, groupID *int64, body []byte) bool {
	return s.SearchToolsRuntime().ShouldEmulate(ctx, searchtools.PolicyInput{Body: body, Mode: gatewayprovider.SearchAccountMode(&searchtools.AccountPolicy{ID: account.Record.ID, Platform: account.Record.Platform, Type: account.Record.Type, Extra: account.Record.Extra}), Platform: account.Record.Platform, GroupID: groupID})
}
func (s *GatewayService) handleWebSearchEmulation(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, parsed *requeststate.ParsedRequest) (*forwardcore.MessagesResult, error) {
	result, err := s.SearchToolsRuntime().Execute(ctx, searchtools.Request{
		Body:        parsed.Body.Bytes(),
		Model:       parsed.Model,
		Stream:      parsed.Stream,
		AccountID:   account.Record.ID,
		AccountName: account.Record.Name,
		ProxyURL:    resolveAccountProxyURL(account),
		OnAccepted:  parsed.OnUpstreamAccepted,
	}, gatewayhttp.SearchOutput{Context: c})
	if err != nil {
		var proxy *searchtools.ProxyFailure
		if errors.As(err, &proxy) {
			return nil, &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(err.Error())}
		}
		return nil, err
	}
	return &forwardcore.MessagesResult{Model: result.Model, Duration: result.Duration, Usage: result.Usage}, nil
}

// 该投影仍被 Live 传输使用；不改变代理缺失时的原返回值。
func resolveAccountProxyURL(account *gatewayprovider.ExecutionAccount) string {
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		return account.Record.Proxy.URL()
	}
	return ""
}

// BindSearchToolsRuntime 仅在开放请求前由 app 绑定，之后保持同一无状态编排器。
func (s *GatewayService) BindSearchToolsRuntime(runtime *searchtools.Emulator) {
	s.searchToolsRuntime = runtime
}
