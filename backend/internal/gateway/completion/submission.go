package completion

import (
	"context"
	"time"
)

// SubmissionEvent 只描述提交降级或 panic，日志后端由装配边界注入。
type SubmissionEvent struct {
	Mandatory bool
	Panic     any
}

// SubmissionOptions 保留各入口已经存在的停止池、取消和 panic 处理差异。
// 此对象不创建队列或后台任务，所有异步工作仍归唯一 WorkerPool。
type SubmissionOptions struct {
	FallbackWhenStopped  bool
	PreserveSourceValues bool
	RecoverPanic         bool
	Observe              func(SubmissionEvent)
}

// SubmitTask 在调用时冻结关联快照，异步闭包不持有可变 HTTP 请求。
// mandatory 仅覆盖明确要求同步兜底的任务，普通 drop/sample 仍保持丢弃。
func SubmitTask(pool *UsageRecordWorkerPool, source context.Context, task UsageRecordTask, mandatory bool, options SubmissionOptions) {
	if task == nil {
		return
	}
	task = WrapTaskContext(source, task)
	if pool != nil {
		mode := pool.Submit(task)
		fallback := mandatory && mode.Dropped() || options.FallbackWhenStopped && mode == UsageRecordSubmitModeDroppedStopped
		if !fallback {
			return
		}
		if options.Observe != nil {
			options.Observe(SubmissionEvent{Mandatory: mandatory})
		}
	}
	base := context.Background()
	if options.PreserveSourceValues && source != nil {
		base = context.WithoutCancel(source)
	}
	ctx, cancel := context.WithTimeout(base, 10*time.Second)
	defer cancel()
	if options.RecoverPanic {
		defer func() {
			if value := recover(); value != nil && options.Observe != nil {
				options.Observe(SubmissionEvent{Mandatory: mandatory, Panic: value})
			}
		}()
	}
	task(ctx)
}
