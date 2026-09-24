package messageforward

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"go.uber.org/zap"
)

// requestTLS 在原发送位置读取配置 ID，TLS 策略与缓存由 egress 唯一拥有。
func (r *Runtime) requestTLS(target *provider.ExecutionAccount) *tlsfingerprint.Profile {
	selection := egress.TLSSelection{}
	if target != nil {
		selection.Enabled = target.View().IsTLSFingerprintEnabled()
		selection.DirectProfileID = target.View().GetTLSFingerprintProfileID()
	}
	return r.dependencies.TLS.ResolveRequestTLS(selection)
}

func (r *Runtime) scheduleActivity(target *provider.ExecutionAccount) {
	if r.dependencies.Deferred != nil && target != nil && account.IsOllamaCloudUsageAccount(provider.ExecutionRecord(target)) {
		r.dependencies.Deferred.ScheduleLastUsedUpdate(target.Record.ID)
	}
}

// transportError 保留取消不切号、不停调，以及持久故障先观察再记录停调的顺序。
func (r *Runtime) transportError(ctx context.Context, output HTTPBoundary, target *provider.ExecutionAccount, err error, notice forward.Notice) error {
	safe := logredact.SanitizeUpstreamQueries(err.Error())
	output.SetError(0, safe, "")
	notice.Platform = target.Record.Platform
	notice.AccountID = target.Record.ID
	notice.AccountName = target.Record.Name
	notice.UpstreamStatusCode = 0
	notice.Kind = "request_error"
	notice.Message = safe
	output.Observe(notice)
	if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return err
	}
	r.scheduleActivity(target)
	if httpclient.ClassifyTransportFailure(err).Persistent {
		r.tempUnscheduleTransport(ctx, target, safe)
	}
	return &forward.UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`{"type":"error","error":{"type":"upstream_error","message":"Upstream request failed"}}`),
	}
}

func (r *Runtime) tempUnscheduleTransport(ctx context.Context, target *provider.ExecutionAccount, safe string) {
	if target == nil || r.dependencies.AccountState == nil {
		return
	}
	until := time.Now().Add(10 * time.Minute)
	reason := "upstream transport error (proxy/network): " + safe
	updateContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.dependencies.AccountState.SetTempUnschedulable(updateContext, target.Record.ID, until, reason); err != nil {
		logging.L().With(zap.String("component", "service.gateway")).Warn("gateway.account_temp_unschedule_transport_failed", zap.Int64("account_id", target.Record.ID), zap.Error(err))
		return
	}
	logging.L().With(zap.String("component", "service.gateway")).Warn("gateway.account_temp_unscheduled_transport",
		zap.Int64("account_id", target.Record.ID), zap.String("account_name", target.Record.Name),
		zap.String("platform", target.Record.Platform), zap.Time("until", until), zap.String("reason", reason))
}
