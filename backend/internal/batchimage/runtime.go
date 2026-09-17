// Runtime 拥有本模块后台循环，停止后不能再次启动。
package batchimage

import (
	"context"
	"fmt"
	"sync"
)

type Runtime struct {
	mu       sync.Mutex
	enabled  bool
	name     string
	loops    []func(context.Context)
	cancel   context.CancelFunc
	done     chan struct{}
	stopped  bool
	stopDone chan struct{}
	stopErr  error
}

func NewRuntime(name string, enabled bool, loops ...func(context.Context)) *Runtime {
	return &Runtime{name: name, enabled: enabled, loops: loops}
}
func (r *Runtime) Start() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enabled || r.stopped || r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	done := make(chan struct{})
	r.done = done
	var wg sync.WaitGroup
	for _, loop := range r.loops {
		if loop != nil {
			wg.Add(1)
			go func(run func(context.Context)) { defer wg.Done(); run(ctx) }(loop)
		}
	}
	go func() { wg.Wait(); close(done) }()
}
func (r *Runtime) Running() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel != nil && !r.stopped
}
func (r *Runtime) Stop() { _ = r.StopContext(context.Background()) }

// StopContext 共享首次停止结果，忽略取消的任务必须报告未完成。
func (r *Runtime) StopContext(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.stopped {
		done := r.stopDone
		r.mu.Unlock()
		select {
		case <-done:
			return r.stopErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.stopped = true
	r.stopDone = make(chan struct{})
	cancel := r.cancel
	done := r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var err error
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			err = fmt.Errorf("%s work remains unfinished: %w", r.name, ctx.Err())
		}
	}
	r.mu.Lock()
	r.stopErr = err
	close(r.stopDone)
	r.mu.Unlock()
	return err
}
