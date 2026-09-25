// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"fmt"
	"sync"
)

type operationActivity struct {
	mu       sync.Mutex
	stopped  bool
	next     uint64
	active   map[uint64]context.CancelFunc
	idle     chan struct{}
	stopDone chan struct{}
	stopErr  error
}

// beginRefresh 将排队与交换纳入同一生命周期，正常运行时仍沿用调用方取消策略。
func (state *operationActivity) begin(parent context.Context, stoppedError error) (context.Context, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	state.mu.Lock()
	if state.stopped {
		state.mu.Unlock()
		return nil, nil, stoppedError
	}
	if len(state.active) == 0 {
		state.idle = make(chan struct{})
	}
	if state.active == nil {
		state.active = make(map[uint64]context.CancelFunc)
	}
	state.next++
	id := state.next
	ctx, cancel := context.WithCancel(parent)
	state.active[id] = cancel
	state.mu.Unlock()
	return ctx, func() {
		cancel()
		state.mu.Lock()
		delete(state.active, id)
		if len(state.active) == 0 {
			close(state.idle)
		}
		state.mu.Unlock()
	}, nil
}

// StopContext 停止新认领并取消等待和交换；本次停止结果固定，超时不能报告已排空。
func (state *operationActivity) stop(ctx context.Context, label string) error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	if state.stopped {
		done := state.stopDone
		state.mu.Unlock()
		select {
		case <-done:
			return state.stopErr
		default:
		}
		select {
		case <-done:
			return state.stopErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state.stopped = true
	state.stopDone = make(chan struct{})
	done := state.stopDone
	idle := state.idle
	cancels := make([]context.CancelFunc, 0, len(state.active))
	for _, cancel := range state.active {
		cancels = append(cancels, cancel)
	}
	state.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	var err error
	if idle != nil {
		select {
		case <-idle:
		default:
			select {
			case <-idle:
			case <-ctx.Done():
				err = fmt.Errorf("%s work remains unfinished: %w", label, ctx.Err())
			}
		}
	}
	state.mu.Lock()
	state.stopErr = err
	close(done)
	state.mu.Unlock()
	return err
}
