package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CompletionSubmission 在同步 HTTP 边界冻结请求，不缓存 Gin Context 或新建工作池。
type CompletionSubmission struct {
	pool    *completion.UsageRecordWorkerPool
	options completion.SubmissionOptions
}

// NewCompletionSubmission 保留 Messages 与 OpenAI 入口原有的日志来源。
func NewCompletionSubmission(pool *completion.UsageRecordWorkerPool, openAI bool) CompletionSubmission {
	prefix, component := "gateway", "handler.gateway.messages"
	if openAI {
		prefix, component = "openai", "handler.openai_gateway.responses"
	}
	return CompletionSubmission{pool: pool, options: completion.SubmissionOptions{
		FallbackWhenStopped: true,
		RecoverPanic:        true,
		Observe: func(event completion.SubmissionEvent) {
			name, source := prefix+".usage_record_task_stopped_sync_fallback", component
			if event.Mandatory {
				name, source = prefix+".usage_record_task_mandatory_sync_fallback", "handler.gateway.usage"
			}
			if event.Mandatory && openAI {
				source = "handler.openai_gateway.usage"
			}
			logger := logging.L().With(zap.String("component", source))
			if event.Panic != nil {
				logger.With(zap.Any("panic", event.Panic)).Error(prefix + ".usage_record_task_panic_recovered")
				return
			}
			logger.Warn(name)
		},
	}}
}

// NewQoderCompletionSubmission 保留池拒绝不内联、无池时脱离取消的原独立策略。
func NewQoderCompletionSubmission(pool *completion.UsageRecordWorkerPool) CompletionSubmission {
	return CompletionSubmission{pool: pool, options: completion.SubmissionOptions{PreserveSourceValues: true}}
}

// CompletionContext 仅固化原关联字段和模型链，供同步构造完成输入使用。
func CompletionContext(c *gin.Context) context.Context {
	return completion.SnapshotContext(completionSource(c))
}
func completionSource(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return c.Request.Context()
}
func (s CompletionSubmission) Submit(c *gin.Context, task completion.UsageRecordTask) {
	completion.SubmitTask(s.pool, completionSource(c), task, false, s.options)
}
func (s CompletionSubmission) SubmitMandatory(c *gin.Context, task completion.UsageRecordTask) {
	completion.SubmitTask(s.pool, completionSource(c), task, true, s.options)
}

// SubmitImages 只接收已经确认的产出数量，不读取或重新估算用量。
func (s CompletionSubmission) SubmitImages(c *gin.Context, images int, task completion.UsageRecordTask) {
	completion.SubmitTask(s.pool, completionSource(c), task, images > 0, s.options)
}
