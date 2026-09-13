package scheduler

import (
	"context"
	"sync"
)

// ReleaseMode 由执行入口明确选择；完成释放不会因客户端断开提前归还上游容量。
type ReleaseMode uint8

const (
	ReleaseOnCompletion ReleaseMode = iota
	ReleaseOnCancel
)

// Lease 持有本次请求实际取得的资源，按取得顺序的逆序清理。
// @project-doc docs/architecture/gateway_request_lifecycle.md#account_selection_and_failover
type Lease struct {
	mu        sync.Mutex
	closed    bool
	resources []func()
	stop      func() bool
	once      sync.Once
}

// NewLease 先登记资源再关联取消，保证已取消输入也不会遗失刚取得的资源。
func NewLease(ctx context.Context, mode ReleaseMode, resources ...func()) *Lease {
	l := &Lease{}
	for _, release := range resources {
		l.Own(release)
	}
	if mode == ReleaseOnCancel && ctx != nil {
		stop := context.AfterFunc(ctx, l.Release)
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			stop()
		} else {
			l.stop = stop
			l.mu.Unlock()
		}
	}
	return l
}

// Own 转移一个已取得资源的释放责任；租约已结束时立即归还并返回 false。
func (l *Lease) Own(release func()) bool {
	if release == nil {
		return true
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		release()
		return false
	}
	l.resources = append(l.resources, release)
	l.mu.Unlock()
	return true
}

// Release 在取消、错误补偿和显式完成并发发生时只清理一次；重复调用等待相同清理完成。
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.mu.Lock()
		l.closed = true
		resources, stop := l.resources, l.stop
		l.resources, l.stop = nil, nil
		l.mu.Unlock()
		if stop != nil {
			stop()
		}
		for i := len(resources) - 1; i >= 0; i-- {
			resources[i]()
		}
	})
}

// WrapRelease 保留旧函数形状，所有取消和幂等语义委托 Lease。
func WrapRelease(ctx context.Context, mode ReleaseMode, release func()) func() {
	if release == nil {
		return nil
	}
	return NewLease(ctx, mode, release).Release
}

// AttemptOutcome 中 Served 包括既有可结算部分结果；它决定空闲会话是否继续保留。
type AttemptOutcome struct {
	Served bool
}

// AttemptLease 独立管理本次账号尝试，父请求仍可在其结束后按旧规则尝试其他账号。
type AttemptLease struct {
	resources *Lease
	finish    func(AttemptOutcome)
	once      sync.Once
}

// NewAttemptLease 注册到请求拥有者；没有完成结果的异常退出按失败清理。
func NewAttemptLease(parent *Lease, finish func(AttemptOutcome), resources ...func()) *AttemptLease {
	a := &AttemptLease{resources: NewLease(context.Background(), ReleaseOnCompletion, resources...), finish: finish}
	if parent != nil {
		parent.Own(a.Release)
	}
	return a
}

// Finish 先处理会话完成语义，再释放本次资源；父租约兜底不会重复执行。
func (a *AttemptLease) Finish(outcome AttemptOutcome) {
	if a == nil {
		return
	}
	a.once.Do(func() {
		defer a.resources.Release()
		if a.finish != nil {
			a.finish(outcome)
		}
	})
}

// Release 为尚未明确完成的尝试提供失败兜底。
func (a *AttemptLease) Release() { a.Finish(AttemptOutcome{}) }
