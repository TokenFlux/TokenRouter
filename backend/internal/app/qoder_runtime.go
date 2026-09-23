// Qoder 固定装配只做旧能力投影；尝试循环与完成事实分别由 gateway/completion 拥有。
package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoderRuntime 的字段在应用装配时固定，不持有 Gin 或逐请求状态。
type qoderRuntime struct {
	Gateway     *service.GatewayService
	Qoder       *gatewayprovider.QoderRuntime
	Refresh     *accountprovider.QoderRequestRefresh
	Billing     *admission.FundingAdmission
	Keys        *apikey.APIKeyService
	Completions *completion.UsageRecordWorkerPool
	Recorder    *completion.Recorder
}

func (b *qoderRuntime) Prepare(ctx context.Context, request gateway.Request) (gateway.Request, error) {
	key := apikey.CopyAPIKey(request.Funding.Key)
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
	key := apikey.CopyAPIKey(request.Funding.Key)
	return b.Billing.CheckKey(ctx, key, request.Funding.Subscription, request.Metadata.QuotaPlatform, afterWait)
}

func (b *qoderRuntime) Select(ctx context.Context, request gateway.Request, excluded map[int64]struct{}) (*gateway.Selection, error) {
	ctx = requeststate.WithRoutePlan(ctx, request.Route)
	key := apikey.CopyAPIKey(request.Funding.Key)
	plan := request.Route
	mapping := service.ChannelMappingFromRoutePlan(plan)
	body := request.AttemptBody
	var project func(*gatewayprovider.SelectionResult, *gatewayprovider.ExecutionAccount, bool) *gateway.Selection
	project = func(selection *gatewayprovider.SelectionResult, account *gatewayprovider.ExecutionAccount, refresh bool) *gateway.Selection {
		executor, input := b.Qoder.PrepareQoderTarget(qoder.RequestMetadata{APIKeyID: key.ID, ClaudeCode: request.Metadata.ClaudeCode, Headers: http.Header(request.Metadata.Headers).Clone()}, gatewayprovider.ExecutionRecord(account), body, protocol.ProtocolOpenAIChatCompletions, request.Model)
		snapshot := gatewayprovider.ExecutionSnapshot(account)
		candidate, _ := plan.ResolveCandidate(snapshot)
		selected := &gateway.Selection{Snapshot: snapshot, Plan: candidate, Acquired: selection.Acquired, Release: selection.ReleaseFunc, WaitPlan: selection.WaitPlan, Executor: executor, Input: input}
		if refresh {
			selected.Acquired = false
			selected.Release = nil
			if selected.WaitPlan == nil {
				selected.WaitWithoutCounter = true
				selected.WaitPlan = &scheduler.AccountWaitPlan{AccountID: account.Record.ID, MaxConcurrency: account.Record.Concurrency, Timeout: 30 * time.Second, MaxWaiting: 0}
			}
		}
		selected.Observe = func(result upstream.AttemptResult, err error) {
			b.Qoder.ObserveQoderFailure(ctx, gatewayprovider.ExecutionRecord(account), err)
			var legacy *forwardcore.MessagesResult
			if err == nil || result.Served && result.HasUsage {
				legacy = forwardcore.MessagesFromAttempt(result)
			}
			b.Gateway.ReportAdvancedAccountScheduleResult(selection, account.Record.ID, err == nil, legacy)
		}
		selected.Switched = func() { b.Gateway.RecordAdvancedAccountSwitch(selection) }
		selected.Refresh = func(ctx context.Context) (*gateway.Selection, error) {
			updated, err := b.Refresh.RefreshAccountSession(ctx, gatewayprovider.ExecutionRecord(account))
			if err != nil || updated == nil {
				return nil, err
			}
			return project(selection, gatewayprovider.NewExecutionAccount(updated), true), nil
		}
		selected.Bind = func(ctx context.Context, _ upstream.AttemptResult) {
			bindCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = b.Gateway.BindStickySession(bindCtx, key.GroupID, request.SessionHash, account.Record.ID)
		}

		selected.Complete = func(callCtx context.Context, result upstream.AttemptResult) {
			snapshot := gatewayprovider.CaptureMessages(callCtx, &gatewayprovider.MessagesCapture{
				Result: forwardcore.MessagesFromAttempt(result), QuotaPlatform: request.Metadata.QuotaPlatform,
				APIKey: key, User: key.User, Account: gatewayprovider.ExecutionCompletionRecord(account), Subscription: request.Funding.Subscription,
				InboundEndpoint: request.Metadata.InboundEndpoint, UpstreamEndpoint: request.Metadata.UpstreamEndpoint,
				UserAgent: request.Metadata.UserAgent, IPAddress: request.Metadata.ClientIP,
				RequestPayloadHash: billing.HashUsageRequestPayload(request.Body), RequestBody: request.Body,
				APIKeyService: b.Keys, ChannelUsageFields: mapping.ToUsageFields(request.Model, result.UpstreamModel),
			})
			task := func(workerCtx context.Context) {
				if err := b.Recorder.Record(workerCtx, snapshot, false); err != nil {
					logging.LegacyPrintf("handler.qoder_gateway", "record usage failed account=%d: %v", snapshot.Account.ID, err)
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
	return errors.Is(err, accountcore.ErrQoderRefreshInProgress)
}
func (b *qoderRuntime) QueueFailure(kind string, err error) {
	logging.LegacyPrintf("handler.qoder_gateway", "%s wait counter failed: %v", kind, err)
}
