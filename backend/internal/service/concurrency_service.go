// 并发、短期负载缓存和请求 ID 状态唯一位于 scheduler；旧消费者保持别名及构造委托。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"go.uber.org/zap"
)

type ConcurrencyService = scheduler.ConcurrencyService
type ConcurrencyCache = scheduler.ConcurrencyCache
type APIKeyConcurrencyCache = scheduler.APIKeyConcurrencyCache
type OpenAIWSIngressLeaseCache = scheduler.OpenAIWSIngressLeaseCache
type OpenAIWSIngressLease = scheduler.OpenAIWSIngressLease
type AcquireResult = scheduler.AcquireResult
type AccountWithConcurrency = scheduler.AccountWithConcurrency
type UserWithConcurrency = scheduler.UserWithConcurrency
type AccountLoadInfo = scheduler.AccountLoadInfo
type UserLoadInfo = scheduler.UserLoadInfo

var ErrOpenAIWSIngressLeaseLost = scheduler.ErrOpenAIWSIngressLeaseLost

func NewConcurrencyService(cache ConcurrencyCache) *ConcurrencyService {
	return scheduler.NewConcurrencyService(cache, LegacySchedulerDiagnostics())
}
func RequestIDPrefix() string              { return scheduler.RequestIDPrefix() }
func generateRequestID() string            { return scheduler.GenerateRequestID() }
func CalculateMaxWait(concurrency int) int { return scheduler.CalculateMaxWait(concurrency) }

// LegacySchedulerDiagnostics 只投影原日志后端；生产装配同样复用这一后端。
func LegacySchedulerDiagnostics() scheduler.Diagnostics {
	return scheduler.Diagnostics{Logf: logger.LegacyPrintf, Event: func(level, event string, fields ...any) {
		values := make([]zap.Field, 0, len(fields)/2)
		for i := 0; i+1 < len(fields); i += 2 {
			key, ok := fields[i].(string)
			if ok {
				values = append(values, zap.Any(key, fields[i+1]))
			}
		}
		switch level {
		case "error":
			logger.L().Error(event, values...)
		case "warn":
			logger.L().Warn(event, values...)
		default:
			logger.L().Debug(event, values...)
		}
	}}
}
