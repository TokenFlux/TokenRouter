// WorkGroup 跟踪当前及已替换配置的在途请求和额度清理，关闭后不再接受新搜索。
package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type WorkGroup struct {
	mu      sync.Mutex
	active  int
	stopped bool
	done    chan struct{}
}

func NewWorkGroup() *WorkGroup { return &WorkGroup{done: make(chan struct{})} }
func (g *WorkGroup) Begin() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return nil, errors.New("websearch: runtime stopped")
	}
	g.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.active--
			if g.stopped && g.active == 0 {
				close(g.done)
			}
		})
	}, nil
}
func (g *WorkGroup) Stop(ctx context.Context) error {
	g.mu.Lock()
	if !g.stopped {
		g.stopped = true
		if g.active == 0 {
			close(g.done)
		}
	}
	done := g.done
	g.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		g.mu.Lock()
		n := g.active
		g.mu.Unlock()
		return fmt.Errorf("websearch: %d unfinished operations: %w", n, ctx.Err())
	}
}
