package httpapi

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// liveCreatePorts 只连接原生选择器、模型轨迹和供应商单次创建能力。
type liveCreatePorts struct {
	service *OpenAILiveExecutor
}

func (p *liveCreatePorts) PrepareAttestation(ctx context.Context) (string, string, error) {
	return p.service.prepareLiveAttestation(ctx)
}
func (p *liveCreatePorts) Select(ctx context.Context, groupID *int64, model string, excluded map[int64]struct{}) (*gatewaylive.Candidate, error) {
	selection, _, err := p.service.Selection.SelectAccountWithSchedulerForCapability(ctx, groupID, "", uuid.NewString(), model, excluded, egress.OpenAIUpstreamTransportHTTPSSE, accountcore.OpenAIEndpointCapabilityLive, false, false)
	if err != nil || selection == nil {
		return nil, err
	}
	result := &gatewaylive.Candidate{Acquired: selection.Acquired, ReleaseFunc: selection.ReleaseFunc}
	if selection.Account != nil {
		result.ID = selection.Account.Record.ID
		result.Concurrency = selection.Account.Record.Concurrency
		result.Target = &liveCreateTarget{service: p.service, account: selection.Account, groupID: groupID}
	}
	return result, nil
}
func (p *liveCreatePorts) TraceModels(ctx context.Context, routing, upstream string) {
	modeltrace.RegisterStage(ctx, routing)
	modeltrace.RegisterStage(ctx, upstream)
}
func (p *liveCreatePorts) ModelTrace(ctx context.Context, groupID *int64, model, upstream string) (string, string) {
	requested := model
	if trace, ok := modeltrace.FromContext(ctx); ok && strings.TrimSpace(trace.ClientModel) != "" {
		requested = trace.ClientModel
	}
	plan := p.service.Routes.PlanRoute(ctx, nil, groupID, model)
	mapping := routing.ChannelMappingResult(plan.Mapping())
	return requested, mapping.BuildModelMappingChain(model, upstream)
}
func (p *liveCreatePorts) NewLeaseID() string { return scheduler.GenerateRequestID() }
func (p *liveCreatePorts) ShouldFailover(err error) bool {
	return p.service.shouldFailoverLiveCreateError(err)
}
func (p *liveCreatePorts) Observe(record *session.LiveCallRecord) {
	p.service.Background("service/openai_live.go:CreateLiveCall", func() {
		p.service.observeLiveCall(record)
	})
}

// liveCreateTarget 保存本次选择取得的凭据视图，后续执行不再查询账号。
type liveCreateTarget struct {
	service *OpenAILiveExecutor
	account *gatewayprovider.ExecutionAccount
	groupID *int64
	router  egress.TLSFingerprintRouterMatchResult
}

func (t *liveCreateTarget) ResolveModel(ctx context.Context, model string) (string, string, error) {
	routing, err := t.service.Selection.ResolveOpenAIWSRoutingModelForAccount(ctx, t.groupID, t.account, model, accountcore.OpenAIEndpointCapabilityLive)
	if err != nil {
		return "", "", err
	}
	return routing, gatewayprovider.ExecutionModelPolicy(t.account).OpenAIUpstream(routing, false, false), nil
}
func (t *liveCreateTarget) AllowsClient(ctx context.Context, identity session.LiveCallIdentity) bool {
	t.router = t.service.matchLiveTLSFingerprintRouter(t.account, identity.UserAgent)
	result := t.service.liveClientPolicyResult(ctx, t.account, identity, t.router)
	if result.Enabled && !result.Matched {
		logging.FromContext(ctx).Warn("OpenAI Live 客户端策略拒绝候选账号", zap.Int64("account_id", t.account.Record.ID), zap.String("policy", result.Policy), zap.String("reason", result.Reason))
		return false
	}
	return true
}
func (t *liveCreateTarget) Create(ctx context.Context, request *session.LiveCallRequest, attestation string) (*gatewaylive.Created, error) {
	created, err := t.service.createUpstreamLiveCall(ctx, t.account, request, attestation, t.router)
	if err != nil {
		return nil, err
	}
	return &gatewaylive.Created{SDP: created.SDP, CallID: created.CallID, Location: created.Location}, nil
}
