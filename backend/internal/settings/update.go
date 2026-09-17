package settings

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// PreparedChange 只包含已经校验的持久值和提交后应用，不持有数据库事务。
type PreparedChange struct {
	Module string
	Values map[string]string
	Apply  func(context.Context) error
}

// Updates 统一同实例综合更新的读取、准备、提交和运行发布。
// @project-doc docs/interfaces/configuration.md#runtime_settings
type Updates struct {
	repo   Repository
	gate   chan struct{}
	mu     sync.Mutex
	sealed bool
	active int
	done   chan struct{}
	run    context.Context
	cancel context.CancelFunc
}

func newUpdates(repo Repository) *Updates {
	ctx, cancel := context.WithCancel(context.Background())
	return &Updates{repo: repo, gate: make(chan struct{}, 1), done: make(chan struct{}), run: ctx, cancel: cancel}
}

// UpdateSession 的保护从读取旧值前开始，一直覆盖提交后的应用。
// 调用者必须 Close；它不会把已提交的数据回滚。
type UpdateSession struct {
	owner     *Updates
	ctx       context.Context
	cancel    context.CancelFunc
	stop      func() bool
	once      sync.Once
	committed bool
}

func (u *Updates) Begin(ctx context.Context) (*UpdateSession, error) {
	u.mu.Lock()
	if u.sealed {
		u.mu.Unlock()
		return nil, apperror.ServiceUnavailable("SETTINGS_STOPPED", "settings updates are stopped")
	}
	u.active++
	u.mu.Unlock()
	updateCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(u.run, cancel)
	s := &UpdateSession{owner: u, ctx: updateCtx, cancel: cancel, stop: stop}
	select {
	case u.gate <- struct{}{}:
		if err := updateCtx.Err(); err != nil {
			s.Close()
			return nil, err
		}
		return s, nil
	case <-updateCtx.Done():
		stop()
		cancel()
		u.finish()
		return nil, updateCtx.Err()
	}
}

func (s *UpdateSession) Context() context.Context { return s.ctx }

// Commit 合并已准备的键，只写入一次；应用失败保留数据库事实并返回明确标识。
func (s *UpdateSession) Commit(changes ...PreparedChange) error {
	if s.committed {
		return errors.New("settings update already committed")
	}
	values := make(map[string]string)
	for _, change := range changes {
		if change.Module == "" {
			return errors.New("settings change has no owner")
		}
		for key := range change.Values {
			if _, exists := values[key]; exists {
				return fmt.Errorf("duplicate settings key: %s", key)
			}
		}
		maps.Copy(values, change.Values)
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.owner.repo.SetMultiple(s.ctx, values); err != nil {
		return err
	}
	s.committed = true
	var failures []error
	var modules []string
	for _, change := range changes {
		if change.Apply == nil {
			continue
		}
		if err := change.Apply(s.ctx); err != nil {
			modules = append(modules, change.Module)
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return apperror.InternalServer("SETTINGS_APPLY_FAILED", "settings persisted but runtime application failed").
			WithMetadata(map[string]string{"persisted": "true", "modules": strings.Join(modules, ",")}).WithCause(errors.Join(failures...))
	}
	return nil
}

func (s *UpdateSession) Close() {
	s.once.Do(func() {
		s.stop()
		s.cancel()
		<-s.owner.gate
		s.owner.finish()
	})
}
func (u *Updates) finish() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.active--
	if u.sealed && u.active == 0 {
		close(u.done)
	}
}

// Seal 在 HTTP 等待前拒绝新更新，并取消现有准备和应用。
func (u *Updates) Seal() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.sealed {
		return
	}
	u.sealed = true
	u.cancel()
	if u.active == 0 {
		close(u.done)
	}
}

// Stop 的预算只限制等待，未完成的更新仍持有依赖且不会报告成功。
func (u *Updates) Stop(ctx context.Context) error {
	u.Seal()
	select {
	case <-u.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("settings updates unfinished: %w", ctx.Err())
	}
}
