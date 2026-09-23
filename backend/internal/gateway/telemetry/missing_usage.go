package telemetry

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"go.uber.org/zap"
)

const openAIMissingUsageLogInterval = time.Minute

// openAIMissingUsageLogSampler 对缺失 usage 的诊断日志做低频采样，同时保留累计计数。
type openAIMissingUsageLogSampler struct {
	mu         sync.Mutex
	lastLogged time.Time
	total      uint64
	suppressed uint64
}

func (s *openAIMissingUsageLogSampler) sample(now time.Time) (logNow bool, total, suppressed uint64) {
	if s == nil {
		return false, 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	if s.lastLogged.IsZero() || !now.Before(s.lastLogged.Add(openAIMissingUsageLogInterval)) {
		s.lastLogged = now
		total = s.total
		suppressed = s.suppressed
		s.suppressed = 0
		return true, total, suppressed
	}
	s.suppressed++
	return false, s.total, 0
}

var openAIMissingUsageLogSamplerState openAIMissingUsageLogSampler
var openAIMissingUsageTotal atomic.Uint64

// SuccessMissingUsage 记录成功响应缺失 usage 的低频诊断，避免影响请求路径。
func SuccessMissingUsage(ctx context.Context, accountID int64, status int, usage *s09openai.ForwardUsage, terminalEvent string, clientDisconnected bool) {
	if status < 200 || status >= 300 || usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ImageOutputTokens > 0) {
		return
	}
	terminalEvent = strings.TrimSpace(terminalEvent)
	if terminalEvent != "response.completed" && terminalEvent != "response.done" && terminalEvent != "json" && terminalEvent != "[DONE]" {
		return
	}
	logNow, count, suppressed := openAIMissingUsageLogSamplerState.sample(time.Now())
	openAIMissingUsageTotal.Store(count)
	if !logNow {
		return
	}
	logging.FromContext(ctx).With(
		zap.Int64("account_id", accountID),
		zap.String("terminal_event", terminalEvent),
		zap.Bool("client_disconnected", clientDisconnected),
		zap.Uint64("missing_usage_total", count),
		zap.Uint64("missing_usage_suppressed", suppressed),
	).Debug("openai_usage.success_missing_usage")
}
