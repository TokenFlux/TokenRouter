// Qoder 固定装配只做旧能力投影；尝试循环与完成事实分别由 gateway/completion 拥有。
package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoderRuntime 的字段在应用装配时固定，不持有 Gin 或逐请求状态。
type qoderRuntime struct {
	Gateway     *service.GatewayService
	Qoder       *service.QoderGatewayService
	Billing     *service.BillingCacheService
	Keys        *service.APIKeyService
	Completions *completion.UsageRecordWorkerPool
	Recorder    *completion.Recorder
}

func (b *qoderRuntime) Prepare(ctx context.Context, request gateway.Request) (gateway.Request, error) {
	key := service.APIKeyFromView(request.Funding.Key)
	request.Route = b.Gateway.PlanRoute(ctx, service.APIKeyRouteGroup(key), key.GroupID, request.Model).WithClientProtocol(protocol.ProtocolOpenAIChatCompletions)
	mapping := service.ChannelMappingFromRoutePlan(request.Route)
	request.AttemptBody = request.Body
	if mapping.Mapped {
		request.AttemptBody = b.Gateway.ReplaceModelInBody(request.Body, mapping.MappedModel)
	}
	return request, nil
}
func (b *qoderRuntime) Check(ctx context.Context, request gateway.Request, afterWait bool) error {
	if b.Billing == nil {
		return nil
	}
	key := service.APIKeyFromView(request.Funding.Key)
	if !afterWait {
		return b.Billing.CheckBillingEligibility(ctx, key.User, key, key.Group, request.Funding.Subscription, request.Metadata.QuotaPlatform)
	}
	var group *billing.GroupSnapshot
	if key.Group != nil {
		group = &billing.GroupSnapshot{ID: key.Group.ID}
	}
	return b.Billing.Check(ctx, billing.CheckInput{Payer: service.BillingUserSummary(key.User), Key: service.BillingKeySnapshot(key), Group: group, Subscription: request.Funding.Subscription, Platform: request.Metadata.QuotaPlatform})
}
func (b *qoderRuntime) Select(ctx context.Context, request gateway.Request, excluded map[int64]struct{}) (*gateway.Selection, error) {
	ctx = service.WithRoutePlan(ctx, request.Route)
	key := service.APIKeyFromView(request.Funding.Key)
	plan := request.Route
	mapping := service.ChannelMappingFromRoutePlan(plan)
	body := request.AttemptBody
	var project func(*service.AccountSelectionResult, *service.Account, bool) *gateway.Selection
	project = func(selection *service.AccountSelectionResult, account *service.Account, refresh bool) *gateway.Selection {
		executor, input := b.Qoder.PrepareQoderTarget(qoder.RequestMetadata{APIKeyID: key.ID, ClaudeCode: request.Metadata.ClaudeCode, Headers: http.Header(request.Metadata.Headers).Clone()}, account, body, protocol.ProtocolOpenAIChatCompletions, request.Model)
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
			b.Qoder.ObserveQoderFailure(ctx, account, err)
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
			return project(selection, updated, true), nil
		}
		selected.Bind = func(ctx context.Context, _ upstream.AttemptResult) {
			bindCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = b.Gateway.BindStickySession(bindCtx, key.GroupID, request.SessionHash, account.ID)
		}

		selected.Complete = func(callCtx context.Context, result upstream.AttemptResult) {
			snapshot := service.CompletionForwardInput(callCtx, &service.RecordUsageInput{
				Result: service.ForwardResultFromAttempt(result), QuotaPlatform: request.Metadata.QuotaPlatform,
				APIKey: key, User: key.User, Account: account, Subscription: request.Funding.Subscription,
				InboundEndpoint: request.Metadata.InboundEndpoint, UpstreamEndpoint: request.Metadata.UpstreamEndpoint,
				UserAgent: request.Metadata.UserAgent, IPAddress: request.Metadata.ClientIP,
				RequestPayloadHash: service.HashUsageRequestPayload(request.Body), RequestBody: request.Body,
				APIKeyService: b.Keys, ChannelUsageFields: mapping.ToUsageFields(request.Model, result.UpstreamModel),
			})
			task := func(workerCtx context.Context) {
				if err := b.Recorder.Record(workerCtx, snapshot, false); err != nil {
					logger.LegacyPrintf("handler.qoder_gateway", "record usage failed account=%d: %v", snapshot.Account.ID, err)
				}
			}
			if b.Completions != nil {
				b.Completions.Submit(task)
				return
			}
			completionCtx, cancel := context.WithTimeout(context.WithoutCancel(callCtx), 10*time.Second)
			defer cancel()
			task(completionCtx)
		}
		return selected
	}
	selection, err := b.Gateway.SelectAccountWithLoadAwareness(ctx, key.GroupID, request.SessionHash, request.Model, excluded, "", request.UserID)
	if err != nil {
		return nil, err
	}
	return project(selection, selection.Account, false), nil
}
func (b *qoderRuntime) CanRefresh(err error) bool  { return qoder.MayRefreshAttempt(err) }
func (b *qoderRuntime) CanFailover(err error) bool { return qoder.MaySwitchAttempt(err) }
func (b *qoderRuntime) RefreshPending(err error) bool {
	return errors.Is(err, service.ErrQoderRefreshInProgress)
}
func (b *qoderRuntime) QueueFailure(kind string, err error) {
	logger.LegacyPrintf("handler.qoder_gateway", "%s wait counter failed: %v", kind, err)
}
