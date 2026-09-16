package legacybridge

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// QoderChat 只绑定尚未迁完的请求上下文、平台资格和完成队列，不持有缓存、锁或尝试状态。
type QoderChat struct {
	Gateway     *service.GatewayService
	Qoder       *service.QoderGatewayService
	Billing     *service.BillingCacheService
	Concurrency *scheduler.ConcurrencyService
	Keys        *service.APIKeyService
	HTTP        *handler.QoderGatewayHandler
	ErrorRules  *service.ErrorPassthroughService
}

func (b QoderChat) Preflight(c *gin.Context) error {
	if _, ok := middleware.GetAPIKeyFromContext(c); !ok {
		return &gatewayhttp.HTTPFailure{Status: 401, Type: "authentication_error", Message: "Invalid API key"}
	}
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
		return &gatewayhttp.HTTPFailure{Status: 500, Type: "api_error", Message: "User context not found"}
	}
	return nil
}
func (b QoderChat) Prepare(c *gin.Context, parsed gatewayhttp.ParsedRequest) (gateway.Request, gateway.RequestPorts, error) {
	if err := b.Preflight(c); err != nil {
		return gateway.Request{}, gateway.RequestPorts{}, err
	}
	key, _ := middleware.GetAPIKeyFromContext(c)
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if b.ErrorRules != nil {
		service.BindErrorPassthroughService(c, b.ErrorRules)
	}
	handler.ObserveQoderRequest(c, parsed.Model, parsed.Stream)
	plan := b.Gateway.PlanRoute(c.Request.Context(), service.APIKeyRouteGroup(key), key.GroupID, parsed.Model).WithClientProtocol(protocol.ProtocolOpenAIChatCompletions)
	mapping := service.ChannelMappingFromRoutePlan(plan)
	c.Request = c.Request.WithContext(service.WithRoutePlan(c.Request.Context(), plan))
	body := parsed.Body
	if mapping.Mapped {
		body = b.Gateway.ReplaceModelInBody(body, mapping.MappedModel)
	}
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(parsed.StartedAt).Milliseconds())
	var access *apikey.AccessSnapshot
	if value, ok := c.Get("apikey_access_snapshot"); ok {
		access, _ = value.(*apikey.AccessSnapshot)
	}
	request := gateway.Request{Access: access, Route: plan, UserID: subject.UserID, Concurrency: subject.Concurrency, Stream: parsed.Stream, Body: parsed.Body, Model: parsed.Model}
	hash := b.HTTP.QoderRequestSessionHash(c, parsed.Body, key.ID)
	started := false
	ports := gateway.RequestPorts{Concurrency: b.Concurrency, CanRefresh: b.HTTP.QoderMayRefresh, CanFailover: handler.QoderMayFailover, RefreshPending: func(err error) bool { return errors.Is(err, service.ErrQoderRefreshInProgress) }, WaitObserver: func(string) scheduler.WaitObserver { return b.HTTP.QoderWaitObserver(c, parsed.Stream, &started) }, QueueFailure: func(kind string, err error) {
		logger.LegacyPrintf("handler.qoder_gateway", "%s wait counter failed: %v", kind, err)
	}}
	if b.Billing != nil {
		ports.Check = func(ctx context.Context, afterWait bool) error {
			if !afterWait {
				return b.Billing.CheckBillingEligibility(ctx, key.User, key, key.Group, subscription, service.QuotaPlatform(ctx, key))
			}
			var group *billing.GroupSnapshot
			if key.Group != nil {
				group = &billing.GroupSnapshot{ID: key.Group.ID}
			}
			return b.Billing.Check(ctx, billing.CheckInput{Payer: service.BillingUserSummary(key.User), Key: service.BillingKeySnapshot(key), Group: group, Subscription: subscription, Platform: service.QuotaPlatform(ctx, key)})
		}
	}
	var project func(*service.AccountSelectionResult, *service.Account, bool) *gateway.Selection
	project = func(selection *service.AccountSelectionResult, account *service.Account, refresh bool) *gateway.Selection {
		executor, input := b.Qoder.PrepareQoderAttempt(c, account, body, protocol.ProtocolOpenAIChatCompletions, parsed.Model)
		snapshot := service.AccountSnapshotView(account)
		candidate, _ := plan.ResolveCandidate(snapshot)
		selected := &gateway.Selection{Snapshot: snapshot, Plan: candidate, Acquired: selection.Acquired, Release: selection.ReleaseFunc, WaitPlan: selection.WaitPlan, Executor: executor, Input: input}
		if refresh {
			selected.Acquired = false
			selected.Release = nil
			if selected.WaitPlan == nil {
				selected.WaitWithoutCounter = true
				selected.WaitPlan = &scheduler.AccountWaitPlan{AccountID: account.ID, MaxConcurrency: account.Concurrency, Timeout: 30 * time.Second, MaxWaiting: 0}
			}
		}
		selected.Observe = func(result upstream.AttemptResult, err error) {
			b.Qoder.ObserveQoderFailure(c.Request.Context(), account, err)
			var legacy *service.ForwardResult
			if err == nil || result.Served && result.HasUsage {
				legacy = service.ForwardResultFromAttempt(result)
			}
			b.Gateway.ReportAdvancedAccountScheduleResult(selection, account.ID, err == nil, legacy)
		}
		selected.Switched = func() { b.Gateway.RecordAdvancedAccountSwitch(selection) }
		selected.Refresh = func(ctx context.Context) (*gateway.Selection, error) {
			updated, err := b.Qoder.RefreshAccountSession(ctx, account)
			if err != nil || updated == nil {
				return nil, err
			}
			handler.ObserveQoderSelection(c, updated.ID, updated.Platform)
			return project(selection, updated, true), nil
		}
		selected.Bind = func(ctx context.Context, _ upstream.AttemptResult) {
			b.HTTP.BindQoderSuccess(ctx, key.GroupID, hash, account.ID)
		}
		selected.Complete = func(_ context.Context, result upstream.AttemptResult) {
			userAgent, clientIP := c.GetHeader("User-Agent"), ip.GetClientIP(c)
			inbound, outbound := handler.QoderEndpoints(c, account.Platform)
			quotaPlatform := service.QuotaPlatform(c.Request.Context(), key)
			legacyResult := service.ForwardResultFromAttempt(result)
			payloadHash := service.HashUsageRequestPayload(parsed.Body)
			copiedBody := append([]byte(nil), parsed.Body...)
			b.HTTP.SubmitQoderCompletion(c, func(ctx context.Context) {
				if err := b.Gateway.RecordUsage(ctx, &service.RecordUsageInput{Result: legacyResult, QuotaPlatform: quotaPlatform, APIKey: key, User: key.User, Account: account, Subscription: subscription, InboundEndpoint: inbound, UpstreamEndpoint: outbound, UserAgent: userAgent, IPAddress: clientIP, RequestPayloadHash: payloadHash, RequestBody: copiedBody, APIKeyService: b.Keys, ChannelUsageFields: mapping.ToUsageFields(parsed.Model, result.UpstreamModel)}); err != nil {
					logger.LegacyPrintf("handler.qoder_gateway", "record usage failed account=%d: %v", account.ID, err)
				}
			})
		}
		return selected
	}
	ports.Select = func(ctx context.Context, excluded map[int64]struct{}) (*gateway.Selection, error) {
		selection, err := b.Gateway.SelectAccountWithLoadAwareness(ctx, key.GroupID, hash, parsed.Model, excluded, "", subject.UserID)
		if err != nil {
			return nil, err
		}
		handler.ObserveQoderSelection(c, selection.Account.ID, selection.Account.Platform)
		return project(selection, selection.Account, false), nil
	}
	return request, ports, nil
}
