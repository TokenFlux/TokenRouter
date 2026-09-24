package provider

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"go.uber.org/zap"
)

// TransportHealth 复用账号运行阻断与存储，处理已经分类的持久传输故障。
type TransportHealth struct {
	Runtime  *account.RuntimeBlockState
	Deferred *account.DeferredService
	Store    interface {
		SetTempUnschedulable(context.Context, int64, time.Time, string) error
	}
}

// Attempt 只记录实际进入网络传输的 Ollama Cloud 活动。
func (s *TransportHealth) Attempt(value *account.Record) {
	if s == nil || s.Deferred == nil || value == nil || !account.IsOllamaCloudUsageAccount(value) {
		return
	}
	s.Deferred.ScheduleLastUsedUpdate(value.ID)
}

// Persistent 先阻断进程内调度，再用原五秒独立预算保存十分钟暂停。
func (s *TransportHealth) Persistent(ctx context.Context, value *account.Record, safeError string) {
	if s == nil || value == nil {
		return
	}
	until := time.Now().Add(10 * time.Minute)
	reason := "upstream transport error (proxy/network): " + safeError
	s.Runtime.BlockAccountScheduling(value, until, "transport_error")
	if s.Store == nil {
		logging.L().With(zap.String("component", "service.openai_gateway")).Warn("openai.account_temp_unscheduled_transport_memory_only", zap.Int64("account_id", value.ID), zap.String("account_name", value.Name), zap.String("platform", value.Platform), zap.Time("until", until), zap.String("reason", reason))
		return
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.Store.SetTempUnschedulable(stateCtx, value.ID, until, reason); err != nil {
		logging.L().With(zap.String("component", "service.openai_gateway")).Warn("openai.account_temp_unscheduled_transport_failed", zap.Int64("account_id", value.ID), zap.Error(err))
		return
	}
	logging.L().With(zap.String("component", "service.openai_gateway")).Warn("openai.account_temp_unscheduled_transport", zap.Int64("account_id", value.ID), zap.String("account_name", value.Name), zap.String("platform", value.Platform), zap.Time("until", until), zap.String("reason", reason))
}
