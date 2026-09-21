package service

import (
	"context"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// liveCreatePorts 仅投影账号与平台能力；selected 只用于旧返回值兼容，不参与核心规则。
type liveCreatePorts struct {
	service  *OpenAIGatewayService
	selected *Account
}

func (p *liveCreatePorts) PrepareAttestation(ctx context.Context) (string, string, error) {
	return p.service.prepareLiveAttestation(ctx)
}
func (p *liveCreatePorts) Select(ctx context.Context, groupID *int64, model string, excluded map[int64]struct{}) (*gatewaylive.Candidate, error) {
	selection, _, err := p.service.SelectAccountWithSchedulerForCapability(ctx, groupID, "", uuid.NewString(), model, excluded, egress.OpenAIUpstreamTransportHTTPSSE, accountcore.OpenAIEndpointCapabilityLive, false, false)
	if err != nil || selection == nil {
		return nil, err
	}
	result := &gatewaylive.Candidate{Acquired: selection.Acquired, ReleaseFunc: selection.ReleaseFunc}
	if selection.Account != nil {
		p.selected = selection.Account
		result.ID = selection.Account.ID
		result.Concurrency = selection.Account.Concurrency
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
	plan := p.service.PlanRoute(ctx, nil, groupID, model)
	mapping := ChannelMappingFromRoutePlan(plan)
	return requested, mapping.BuildModelMappingChain(model, upstream)
}
func (p *liveCreatePorts) NewLeaseID() string { return scheduler.GenerateRequestID() }
func (p *liveCreatePorts) ShouldFailover(err error) bool {
	return p.service.shouldFailoverLiveCreateError(err)
}
func (p *liveCreatePorts) Observe(record *session.LiveCallRecord) {
	RunBackgroundTask("service/openai_live.go:CreateLiveCall", BackgroundCall1(p.service.observeLiveCall, record))
}

// liveCreateTarget 保存本次选择取得的凭据视图，避免迁移引入第二次账号查询。
type liveCreateTarget struct {
	service *OpenAIGatewayService
	account *Account
	groupID *int64
	router  egress.TLSFingerprintRouterMatchResult
}

func (t *liveCreateTarget) ResolveModel(ctx context.Context, model string) (string, string, error) {
	routing, err := t.service.ResolveOpenAIWSRoutingModelForAccount(ctx, t.groupID, t.account, model, accountcore.OpenAIEndpointCapabilityLive)
	if err != nil {
		return "", "", err
	}
	return routing, resolveOpenAIAccountUpstreamModelForRequest(t.account, routing, false, false), nil
}
func (t *liveCreateTarget) AllowsClient(ctx context.Context, identity session.LiveCallIdentity) bool {
	t.router = t.service.matchLiveTLSFingerprintRouter(t.account, identity.UserAgent)
	result := t.service.liveClientPolicyResult(ctx, t.account, identity, t.router)
	if result.Enabled && !result.Matched {
		logging.FromContext(ctx).Warn("OpenAI Live 客户端策略拒绝候选账号", zap.Int64("account_id", t.account.ID), zap.String("policy", result.Policy), zap.String("reason", result.Reason))
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
