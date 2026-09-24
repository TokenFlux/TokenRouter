package httpapi

import (
	"context"
	"errors"
	"net/http"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/gin-gonic/gin"
)

// UpstreamTransportFailure 保留无 HTTP 响应时的 Ops、取消和故障转移顺序。
var transportFailureBody = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)

type UpstreamTransportFailure struct {
	Health *accountprovider.TransportHealth
}

func (p *UpstreamTransportFailure) Handle(ctx context.Context, c *gin.Context, target *provider.ExecutionAccount, err error, passthrough bool) error {
	if err == nil {
		return nil
	}
	safe := logredact.SanitizeUpstreamQueries(err.Error())
	platform, name := "", ""
	var id int64
	if target != nil {
		platform, name, id = target.Record.Platform, target.Record.Name, target.Record.ID
	}
	SetOpsUpstreamError(c, 0, safe, "")
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: platform, AccountID: id, AccountName: name, UpstreamStatusCode: 0, Passthrough: passthrough, Kind: "request_error", Message: safe})
	if errors.Is(err, context.Canceled) {
		return err
	}
	if p != nil {
		p.Health.Attempt(provider.ExecutionRecord(target))
		if httpclient.ClassifyTransportFailure(err).Persistent {
			p.Health.Persistent(ctx, provider.ExecutionRecord(target), safe)
		}
	}
	return &forward.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: transportFailureBody}
}
