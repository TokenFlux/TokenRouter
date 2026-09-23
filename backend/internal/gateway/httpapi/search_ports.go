// 独立搜索 HTTP 绑定鉴权、资金、审核及完成快照，尝试循环由 searchtools 唯一拥有。
package httpapi

import (
	"context"
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// StandaloneSearchTarget 仅保留本次已选账号的受控执行与完成快照能力。
type StandaloneSearchTarget interface {
	CompletionRecord() *accountcore.Record
	Execute(context.Context, []byte) ([]byte, error)
}

// StandaloneSearchSelector 不向 HTTP 暴露旧账号实体或完整网关服务。
type StandaloneSearchSelector interface {
	Select(context.Context, int64, string, map[int64]struct{}) (StandaloneSearchTarget, searchtools.Selection, bool, error)
}

// SearchPorts 由组合根绑定唯一依赖；每个搜索只在 Run 中持有本次选择。
type SearchPorts struct {
	Selector    StandaloneSearchSelector
	Funding     *admission.FundingAdmission
	Moderation  ModerationPort
	Concurrency *ConcurrencyHelper
	Keys        gatewaycapture.QuotaUpdater
	Recorder    *completion.Recorder
	Workers     *completion.UsageRecordWorkerPool
}

func (p SearchPorts) DefaultModel() string { return gatewaycapture.GrokStandaloneSearchModel() }
func (p SearchPorts) NormalizeMaxResults(n int) int {
	return gatewaycapture.GrokStandaloneSearchMaxResults(n)
}
func (p SearchPorts) Access(c *gin.Context) (SearchAccess, bool) {
	key, ok := keyhttp.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		return SearchAccess{}, false
	}
	out := SearchAccess{GroupPresent: key.Group != nil, GroupID: key.GroupID}
	if key.Group != nil {
		out.Platform = key.Group.Platform
	}
	return out, true
}
func (p SearchPorts) Billing(c *gin.Context) *SearchHTTPFailure {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	sub, _ := SubscriptionFromContext(c)
	if err := p.Funding.CheckKey(c.Request.Context(), key, sub, admission.QuotaPlatform(c.Request.Context(), key), false); err != nil {
		status, code, message, retry := BillingErrorDetails(err)
		return &SearchHTTPFailure{Status: status, Code: code, Message: message, RetryAfter: retry}
	}
	return nil
}
func (p SearchPorts) Moderate(c *gin.Context, model string, body []byte) *SearchHTTPFailure {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subject, _ := authctx.GetAuthSubjectFromContext(c)
	decision := RunContentModeration(GatewayModerationEndpoints{}, c, RequestLogger(c, "handler.gateway.web_search"), p.Moderation, apikey.CopyAPIKey(key), subject, moderation.ContentModerationProtocolOpenAIChat, model, body)
	if decision != nil && decision.Blocked {
		return &SearchHTTPFailure{Status: ContentModerationStatus(decision), Code: ContentModerationErrorCode(decision), Message: decision.Message}
	}
	return nil
}
func (p SearchPorts) Run(c *gin.Context, groupID int64, isX bool) SearchHTTPRun {
	return &gatewayStandaloneSearchRun{ports: p, c: c, groupID: groupID, isX: isX}
}
func (p SearchPorts) ConcurrencyError(c *gin.Context, err error) {
	status, kind, code, message := ConcurrencyErrorResponse(err, "account")
	WriteAnthropicStreamError(c, status, kind, code, message, false, MarkOpsStreamError)
}

// 当前账号仅在当前 HTTP Adapter 内用于执行和同步快照，不进入核心或后台闭包。
type gatewayStandaloneSearchRun struct {
	ports   SearchPorts
	c       *gin.Context
	groupID int64
	isX     bool
	target  StandaloneSearchTarget
}

func (r *gatewayStandaloneSearchRun) Select(ctx context.Context, model string, excluded map[int64]struct{}) (searchtools.Selection, bool, error) {
	target, selected, found, err := r.ports.Selector.Select(ctx, r.groupID, model, excluded)
	if err != nil || !found {
		return selected, found, err
	}
	r.target = target
	return selected, true, nil
}
func (r *gatewayStandaloneSearchRun) CanSwitch(err error) bool {
	var failure *forwardcore.UpstreamFailoverError
	return errors.As(err, &failure) && failure.ShouldRetryNextAccount()
}
func (r *gatewayStandaloneSearchRun) Execute(ctx context.Context, _ int64, request searchtools.StandaloneRequest, model string, maxResults int) (*contract.SearchResponse, string, error) {
	body, err := gatewaycapture.GrokStandaloneSearchBody(request, model, maxResults, r.isX)
	if err != nil {
		return nil, "", err
	}
	response, err := r.target.Execute(ctx, body)
	if err != nil {
		return nil, "", err
	}
	return gatewaycapture.GrokStandaloneSearchResponse(request.Query, response, maxResults), "grok-native", nil
}
func (r *gatewayStandaloneSearchRun) Acquire(ctx context.Context, selected searchtools.Selection) (func(), bool, error) {
	if selected.Acquired {
		return selected.Release, true, nil
	}
	if selected.WaitPlan == nil || r.ports.Concurrency == nil {
		return nil, false, nil
	}
	counted := false
	wait, err := r.ports.Concurrency.EnterAccountWait(ctx, selected.AccountID, selected.WaitPlan.MaxWaiting)
	if err != nil {
		logging.L().Warn("gateway.web_search.account_wait_counter_increment_failed", zap.Int64("account_id", selected.AccountID), zap.Error(err))
	} else if !wait.Allowed {
		return nil, false, nil
	} else {
		counted = true
	}
	streamStarted := false
	release, err := r.ports.Concurrency.AcquireAccountSlotWithWaitTimeout(r.c, selected.AccountID, selected.WaitPlan.MaxConcurrency, selected.WaitPlan.Timeout, false, &streamStarted)
	if counted {
		wait.Release()
	}
	if err != nil {
		return nil, false, err
	}
	return release, true, nil
}
func (r *gatewayStandaloneSearchRun) Complete(c *gin.Context, req searchtools.StandaloneRequest, _ searchtools.StandaloneResult, isXSearch bool) {
	ports := r.ports
	account := r.target.CompletionRecord()
	apiKey, _ := keyhttp.GetAPIKeyFromContext(c)
	subscription, _ := SubscriptionFromContext(c)
	searchLabel := "web_search"
	if isXSearch {
		searchLabel = "x_search"
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := clientip.GetClientIP(c)
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	requestPayloadHash := billing.HashUsageRequestPayload([]byte(req.Query))
	quotaPlatform := admission.QuotaPlatform(c.Request.Context(), apiKey)
	// request ID 是结算幂等键，必须按调用唯一；查询、IP 或 UA 哈希会错误合并重复搜索。
	searchRequestID := searchLabel + ":" + uuid.NewString()
	if apiKey.Group != nil {
		if p := apiKey.Group.GetSearchPricePer1k(); p != nil && *p == 0 {
			logging.L().With(
				zap.String("component", "handler.gateway.web_search"),
				zap.Int64("group_id", apiKey.Group.ID),
			).Info("gateway.web_search.search_price_per_1k_explicit_free")
		}
	}
	// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
	completionInput := gatewaycapture.CaptureMessages(CompletionContext(c), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
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
		APIKeyService:      ports.Keys,
		QuotaPlatform:      quotaPlatform,
	})
	completionRuntime := ports.Recorder
	NewCompletionSubmission(ports.Workers, false).SubmitMandatory(c, func(ctx context.Context) {
		if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
			logging.L().With(
				zap.String("component", "handler.gateway.web_search"),
				zap.Int64("user_id", completionInput.User.ID),
				zap.Int64("api_key_id", completionInput.APIKey.ID),
				zap.Int64("account_id", completionInput.Account.ID),
			).Error("gateway.web_search.record_usage_failed", zap.Error(err))
		}
	})

}
