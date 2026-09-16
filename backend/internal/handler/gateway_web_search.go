// HTTP 旧入口只绑定查询、平台执行和同步完成快照，不拥有搜索尝试循环。
package handler

import (
	"context"
	"errors"
	"strings"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SearchHTTPHandler 供 app 直接绑定新 HTTP Adapter，旧路由也委托此入口。
func (h *GatewayHandler) SearchHTTPHandler() *gatewayhttp.SearchHandler {
	return gatewayhttp.NewSearchHandler(gatewaySearchHTTPPorts{h})
}
func (h *GatewayHandler) WebSearch(c *gin.Context) { h.SearchHTTPHandler().WebSearch(c) }

type gatewaySearchHTTPPorts struct{ h *GatewayHandler }

func (p gatewaySearchHTTPPorts) DefaultModel() string { return resolveGrokStandaloneSearchModel() }
func (p gatewaySearchHTTPPorts) NormalizeMaxResults(n int) int {
	return xai.NormalizeGrokWebSearchMaxResults(n)
}
func (p gatewaySearchHTTPPorts) Access(c *gin.Context) (gatewayhttp.SearchAccess, bool) {
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		return gatewayhttp.SearchAccess{}, false
	}
	out := gatewayhttp.SearchAccess{GroupPresent: key.Group != nil, GroupID: key.GroupID}
	if key.Group != nil {
		out.Platform = key.Group.Platform
	}
	return out, true
}
func (p gatewaySearchHTTPPorts) Billing(c *gin.Context) *gatewayhttp.SearchHTTPFailure {
	key, _ := middleware2.GetAPIKeyFromContext(c)
	sub, _ := middleware2.GetSubscriptionFromContext(c)
	if err := p.h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, sub, service.QuotaPlatform(c.Request.Context(), key)); err != nil {
		status, code, message, retry := billingErrorDetails(err)
		return &gatewayhttp.SearchHTTPFailure{Status: status, Code: code, Message: message, RetryAfter: retry}
	}
	return nil
}
func (p gatewaySearchHTTPPorts) Moderate(c *gin.Context, model string, body []byte) *gatewayhttp.SearchHTTPFailure {
	key, _ := middleware2.GetAPIKeyFromContext(c)
	subject, _ := middleware2.GetAuthSubjectFromContext(c)
	decision := p.h.checkContentModeration(c, requestLogger(c, "handler.gateway.web_search"), key, subject, service.ContentModerationProtocolOpenAIChat, model, body)
	if decision != nil && decision.Blocked {
		return &gatewayhttp.SearchHTTPFailure{Status: contentModerationStatus(decision), Code: contentModerationErrorCode(decision), Message: decision.Message}
	}
	return nil
}
func (p gatewaySearchHTTPPorts) Run(c *gin.Context, groupID int64, isX bool) gatewayhttp.SearchHTTPRun {
	return &gatewayStandaloneSearchRun{h: p.h, c: c, groupID: groupID, isX: isX}
}
func (p gatewaySearchHTTPPorts) ConcurrencyError(c *gin.Context, err error) {
	p.h.handleConcurrencyError(c, err, "account", false)
}

// 当前账号仅在当前 HTTP Adapter 内用于执行和同步快照，不进入核心或后台闭包。
type gatewayStandaloneSearchRun struct {
	h       *GatewayHandler
	c       *gin.Context
	groupID int64
	isX     bool
	account *service.Account
}

func (r *gatewayStandaloneSearchRun) Select(ctx context.Context, model string, excluded map[int64]struct{}) (searchtools.Selection, bool, error) {
	selected, _, err := r.h.openAIGatewayService.SelectAccountWithSchedulerForCapability(ctx, &r.groupID, "", "", model, excluded, service.OpenAIUpstreamTransportHTTPSSE, service.OpenAIEndpointCapabilityTextGeneration, false, false, service.PlatformGrok)
	if err != nil {
		return searchtools.Selection{}, false, err
	}
	if selected == nil || selected.Account == nil {
		return searchtools.Selection{}, false, nil
	}
	r.account = selected.Account
	return searchtools.Selection{AccountID: selected.Account.ID, Acquired: selected.Acquired, Release: selected.ReleaseFunc, WaitPlan: selected.WaitPlan}, true, nil
}
func (r *gatewayStandaloneSearchRun) CanSwitch(err error) bool {
	var failure *service.UpstreamFailoverError
	return errors.As(err, &failure) && failure.ShouldRetryNextAccount()
}
func (r *gatewayStandaloneSearchRun) Execute(ctx context.Context, _ int64, request searchtools.StandaloneRequest, model string, maxResults int) (*contract.SearchResponse, string, error) {
	var body []byte
	if r.isX {
		var err error
		body, err = xai.BuildGrokXSearchResponsesBody(projectNativeStandaloneSearch(request), model)
		if err != nil {
			return nil, "", err
		}
	} else {
		body = xai.BuildGrokWebSearchResponsesBody(request.Query, maxResults, model)
	}
	response, err := r.h.gatewayService.DoGrokNativeResponsesJSON(ctx, r.account, body)
	if err != nil {
		return nil, "", err
	}
	return &contract.SearchResponse{Query: request.Query, Results: projectStandaloneSearchResults(xai.ExtractGrokWebSearchSources(response, maxResults))}, "grok-native", nil
}
func (r *gatewayStandaloneSearchRun) Acquire(ctx context.Context, selected searchtools.Selection) (func(), bool, error) {
	if selected.Acquired {
		return selected.Release, true, nil
	}
	if selected.WaitPlan == nil || r.h.concurrencyHelper == nil {
		return nil, false, nil
	}
	counted := false
	wait, err := r.h.concurrencyHelper.EnterAccountWait(ctx, selected.AccountID, selected.WaitPlan.MaxWaiting)
	if err != nil {
		logger.L().Warn("gateway.web_search.account_wait_counter_increment_failed", zap.Int64("account_id", selected.AccountID), zap.Error(err))
	} else if !wait.Allowed {
		return nil, false, nil
	} else {
		counted = true
	}
	streamStarted := false
	release, err := r.h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(r.c, selected.AccountID, selected.WaitPlan.MaxConcurrency, selected.WaitPlan.Timeout, false, &streamStarted)
	if counted {
		wait.Release()
	}
	if err != nil {
		return nil, false, err
	}
	return release, true, nil
}
func (r *gatewayStandaloneSearchRun) Complete(c *gin.Context, req searchtools.StandaloneRequest, _ searchtools.StandaloneResult, isXSearch bool) {
	h := r.h
	account := r.account
	apiKey, _ := middleware2.GetAPIKeyFromContext(c)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	searchLabel := "web_search"
	if isXSearch {
		searchLabel = "x_search"
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	requestPayloadHash := service.HashUsageRequestPayload([]byte(req.Query))
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	// request ID 是结算幂等键，必须按调用唯一；查询、IP 或 UA 哈希会错误合并重复搜索。
	searchRequestID := searchLabel + ":" + uuid.NewString()
	if apiKey.Group != nil {
		if p := apiKey.Group.GetSearchPricePer1k(); p != nil && *p == 0 {
			logger.L().With(
				zap.String("component", "handler.gateway.web_search"),
				zap.Int64("group_id", apiKey.Group.ID),
			).Info("gateway.web_search.search_price_per_1k_explicit_free")
		}
	}
	// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
	completionInput := service.CompletionForwardInput(usageRecordContextFromGin(c), &service.RecordUsageInput{
		Result: &service.ForwardResult{
			RequestID:   searchRequestID,
			Model:       "grok-" + strings.ReplaceAll(searchLabel, "_", "-"),
			SearchCount: 1,
			Duration:    0,
		},
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            account,
		Subscription:       subscription,
		InboundEndpoint:    inboundEndpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		UserAgent:          userAgent,
		IPAddress:          clientIP,
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      quotaPlatform,
	})
	completionRuntime := h.completionRuntime()
	h.submitMandatoryUsageRecordTask(c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			logger.L().With(
				zap.String("component", "handler.gateway.web_search"),
				zap.Int64("user_id", completionInput.User.ID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Int64("account_id", completionInput.Account.ID),
			).Error("gateway.web_search.record_usage_failed", zap.Error(err))
		}
	})

}
func projectNativeStandaloneSearch(v searchtools.StandaloneRequest) xai.StandaloneSearchRequest {
	return xai.StandaloneSearchRequest{
		Query:                    v.Query,
		Input:                    v.Input,
		MaxResults:               v.MaxResults,
		AllowedXHandles:          v.AllowedXHandles,
		ExcludedXHandles:         v.ExcludedXHandles,
		FromDate:                 v.FromDate,
		ToDate:                   v.ToDate,
		EnableImageUnderstanding: v.EnableImageUnderstanding,
		EnableVideoUnderstanding: v.EnableVideoUnderstanding,
	}
}
func projectStandaloneSearchResults(in []xai.StandaloneSearchResult) []contract.SearchResult {
	if in == nil {
		return nil
	}
	out := make([]contract.SearchResult, len(in))
	for i, v := range in {
		out[i] = contract.SearchResult{URL: v.URL, Title: v.Title, Snippet: v.Snippet, PageAge: v.PageAge}
	}
	return out
}
