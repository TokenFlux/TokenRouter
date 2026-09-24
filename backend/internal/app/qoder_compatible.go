package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
)

// qoderCompatibleExecution 只连接现有选择、刷新和完成投影，不持有循环或缓存。
type qoderCompatibleExecution struct {
	choices *selection.Generic
	*gatewayprovider.RoutePlanner
	runtime *gatewayprovider.QoderRuntime
	refresh *accountprovider.QoderRequestRefresh
	keys    *apikey.APIKeyService
}

func (p *qoderCompatibleExecution) Select(ctx context.Context, id *int64, hash, model string, excluded map[int64]struct{}, userID int64) (gatewayhttp.QoderCompatibleSelection, error) {
	value, err := p.choices.SelectAccountWithLoadAwareness(ctx, id, hash, model, excluded, "", userID)
	if err != nil {
		return nil, err
	}
	return &qoderCompatibleSelection{owner: p, value: value}, nil
}
func (p *qoderCompatibleExecution) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	return p.PlanKey(ctx, key, model)
}

type qoderCompatibleSelection struct {
	owner *qoderCompatibleExecution
	value *gatewayprovider.SelectionResult
}

func (s *qoderCompatibleSelection) Target() gatewayhttp.QoderCompatibleTarget {
	return &qoderCompatibleTarget{owner: s.owner, value: s.value.Account}
}
func (s *qoderCompatibleSelection) Acquired() bool                       { return s.value.Acquired }
func (s *qoderCompatibleSelection) ReleaseFunc() func()                  { return s.value.ReleaseFunc }
func (s *qoderCompatibleSelection) WaitPlan() *scheduler.AccountWaitPlan { return s.value.WaitPlan }
func (s *qoderCompatibleSelection) Report(id int64, ok bool, result *forward.MessagesResult) {
	s.owner.choices.ReportAdvancedAccountScheduleResult(s.value, id, ok, result)
}
func (s *qoderCompatibleSelection) Switched() { s.owner.choices.RecordAdvancedAccountSwitch(s.value) }

// qoderCompatibleTarget 把旧执行账号限制在单次调用目标内；HTTP 只取得原生快照。
type qoderCompatibleTarget struct {
	owner *qoderCompatibleExecution
	value *gatewayprovider.ExecutionAccount
}

func (t *qoderCompatibleTarget) Snapshot() account.AccountSnapshot {
	return gatewayprovider.ExecutionSnapshot(t.value)
}
func (t *qoderCompatibleTarget) Forward(ctx context.Context, c *gin.Context, body []byte, wire protocol.ProtocolID, model string) (*forward.MessagesResult, error) {
	return gatewayhttp.ForwardQoderAttempt(ctx, c, t.owner.runtime, gatewayprovider.ExecutionRecord(t.value), body, wire, model)
}
func (t *qoderCompatibleTarget) Refresh(ctx context.Context) (gatewayhttp.QoderCompatibleTarget, error) {
	value, err := t.owner.refresh.RefreshAccountSession(ctx, gatewayprovider.ExecutionRecord(t.value))
	if value == nil {
		return nil, err
	}
	return &qoderCompatibleTarget{owner: t.owner, value: gatewayprovider.NewExecutionAccount(value)}, err
}
func (t *qoderCompatibleTarget) Completion(ctx context.Context, capture gatewayhttp.QoderCompletionCapture) *completion.Input {
	return gatewayprovider.CaptureMessages(ctx, &gatewayprovider.MessagesCapture{
		Result: capture.Result, QuotaPlatform: capture.QuotaPlatform, APIKey: capture.Key, User: capture.Key.User, Account: gatewayprovider.ExecutionCompletionRecord(t.value),
		Subscription: capture.Subscription, InboundEndpoint: capture.InboundEndpoint, UpstreamEndpoint: capture.UpstreamEndpoint,
		UserAgent: capture.UserAgent, IPAddress: capture.ClientIP, RequestPayloadHash: capture.PayloadHash, RequestBody: capture.Body,
		APIKeyService: t.owner.keys, ChannelUsageFields: capture.Channel,
	})
}

// provideQoderCompatibleHTTP 直接构造原生固定端口，共享原池、计费、刷新及活动屏障。
func provideQoderCompatibleHTTP(source *gatewayprovider.RoutePlanner, runtime *gatewayprovider.QoderRuntime, refresh *accountprovider.QoderRequestRefresh, concurrency *scheduler.ConcurrencyService, funding *admission.FundingAdmission, keys *apikey.APIKeyService, rules *errorpolicy.ErrorPassthroughService, pool *completion.UsageRecordWorkerPool, recorders GatewayCompletionRecorders, activity *gatewayRequestActivity, qoderActivity *qoderRequestActivity, choices *selection.Generic) *gatewayhttp.QoderCompatibleHandler {
	slots := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatComment, 0)
	var matcher gatewayhttp.ErrorRuleMatcher
	if rules != nil {
		matcher = rules
	}
	options := gatewayhttp.QoderCompatibleOptions{
		Recorder: recorders.Forward, Pool: pool, Slots: slots, Rules: rules,
		ReadAccess: keyhttp.GetAPIKeyFromContext, PlatformAvailable: runtime != nil,
		MayRefresh: qoder.MayRefreshAttempt, MaySwitch: qoder.MaySwitchCompatibleAttempt,
		Errors: gatewayhttp.QoderErrorPresenter{Rules: matcher, Describe: gatewayprovider.DescribeQoderError, ReadAccess: keyhttp.GetAPIKeyFromContext, Catalogue: gatewayprovider.ModelDisplayCatalogue{}},
	}
	if source != nil {
		options.Execution = &qoderCompatibleExecution{RoutePlanner: source, choices: choices, runtime: runtime, refresh: refresh, keys: keys}
	}
	if funding != nil {
		options.Funding = funding
	}
	if qoderActivity != nil {
		options.Enter = qoderActivity.Enter
	}
	result := gatewayhttp.NewQoderCompatibleHandler(gatewayhttp.NewQoderCompatibleRuntime(options), slots, 3)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}

// BindStickySession 仅将原 HTTP 成功后的绑定意图交给同一选择器。
func (p *qoderCompatibleExecution) BindStickySession(ctx context.Context, group *int64, hash string, id int64) error {
	return p.choices.BindStickySession(ctx, group, hash, id)
}
