package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/coder/websocket"
)

// liveHTTPExecution 仅把已有 Live 用例的结果投影给 HTTP，不复制会话、租约或 observer。
type liveHTTPExecution struct{ source *service.OpenAIGatewayService }

func (p liveHTTPExecution) Create(ctx context.Context, request *session.LiveCallRequest, identity session.LiveCallIdentity, limit int) (*gatewaylive.Created, error) {
	created, err := p.source.CreateLiveCall(ctx, request, identity, limit)
	if err != nil {
		return nil, err
	}
	out := &gatewaylive.Created{SDP: created.SDP, CallID: created.CallID, Location: created.Location}
	if created.Account != nil {
		out.AccountID = created.Account.Record.ID
	}
	return out, nil
}

func (p liveHTTPExecution) Lookup(ctx context.Context, id string, identity session.LiveCallIdentity) (*session.LiveCallRecord, error) {
	return p.source.GetLiveCallForIdentity(ctx, id, identity)
}

func (p liveHTTPExecution) Proxy(ctx context.Context, record *session.LiveCallRecord, connection *websocket.Conn) error {
	return p.source.ProxyLiveSideband(ctx, record, connection)
}

// provideLiveHTTP 直接构造原生 Handler，共享原资金、并发和审核实例。
func provideLiveHTTP(source *service.OpenAIGatewayService, funding *admission.FundingAdmission, concurrency *scheduler.ConcurrencyService, moderator *moderation.ContentModerationService, activity *gatewayRequestActivity) *gatewayhttp.LiveHandler {
	ports := gatewayhttp.LivePorts{Execution: liveHTTPExecution{source}, Slots: concurrency}
	if funding != nil {
		ports.Funding = funding
	}
	if moderator != nil {
		ports.Moderation = moderator
	}
	result := gatewayhttp.NewLiveHandler(ports)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}
