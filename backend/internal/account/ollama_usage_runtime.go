package account

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrOllamaUsageStopped = errors.New("ollama cloud usage is stopped")

// OllamaUsageRuntime 同时拥有立即首轮、周期和手动查询；停止预算覆盖全部在途活动。
type OllamaUsageRuntime struct {
	mu               sync.Mutex
	started, stopped bool
	activity         operationActivity
	cycle            func(context.Context) error
	log              func(string, ...any)
}

func NewOllamaUsageRuntime(cycle func(context.Context) error, log func(string, ...any)) *OllamaUsageRuntime {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &OllamaUsageRuntime{cycle: cycle, log: log}
}
func (r *OllamaUsageRuntime) Begin(ctx context.Context) (context.Context, func(), error) {
	return r.activity.begin(ctx, ErrOllamaUsageStopped)
}
func (r *OllamaUsageRuntime) StartContext(parent context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.stopped {
		return nil
	}
	ctx, finish, err := r.Begin(parent)
	if err != nil {
		return err
	}
	r.started = true
	go func() {
		defer finish()
		if ctx.Err() != nil {
			return
		}
		_ = r.cycle(ctx)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				if err := r.cycle(ctx); err != nil {
					r.log("run_due_failed: err=%v", err)
				}
			}
		}
	}()
	return nil
}
func (r *OllamaUsageRuntime) StopContext(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	return r.activity.stop(ctx, "Ollama Cloud usage")
}
