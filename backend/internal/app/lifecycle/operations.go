// 本文件跟踪同步执行的拥有关系，不创建 goroutine，也不改变平台请求取消策略。
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Operations 在关闭入口后拒绝新操作，等待已经进入的同步调用完成。
type Operations struct {
	mu       sync.Mutex
	active   int
	idle     chan struct{}
	closed   bool
	stopDone chan struct{}
	stopErr  error
	name     string
}

func NewOperations(name string) *Operations {
	idle := make(chan struct{})
	close(idle)
	return &Operations{name: name, idle: idle}
}
func (o *Operations) Enter() (func(), error) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil, errors.New(o.name + " is stopped")
	}
	if o.active == 0 {
		o.idle = make(chan struct{})
	}
	o.active++
	o.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			defer o.mu.Unlock()
			o.active--
			if o.active == 0 {
				close(o.idle)
			}
		})
	}, nil
}

// StopContext 固定一次停止结果；超时不能被后续调用改写为成功。
func (o *Operations) StopContext(ctx context.Context) error {
	o.mu.Lock()
	if o.closed {
		done := o.stopDone
		o.mu.Unlock()
		select {
		case <-done:
			return o.stopErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	o.closed = true
	o.stopDone = make(chan struct{})
	done, idle := o.stopDone, o.idle
	o.mu.Unlock()
	var err error
	select {
	case <-idle:
	default:
		select {
		case <-idle:
		case <-ctx.Done():
			o.mu.Lock()
			count := o.active
			o.mu.Unlock()
			err = fmt.Errorf("%s: %d operations unfinished: %w", o.name, count, ctx.Err())
		}
	}
	o.mu.Lock()
	o.stopErr = err
	close(done)
	o.mu.Unlock()
	return err
}
