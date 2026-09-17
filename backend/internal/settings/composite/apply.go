package composite

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// Application 指定所属模块对已提交快照的应用，不携带待保存请求。
type Application struct {
	Module string
	Apply  func(context.Context, *Snapshot) error
}

// ApplicationsForUpdate 给一次更新创建独立的回读状态；未成功回读时不发布其它模块。
func ApplicationsForUpdate(load func(context.Context) (*Snapshot, error), steps []Application) []settings.PreparedChange {
	var snapshot *Snapshot
	changes := []settings.PreparedChange{{Module: "system", Apply: func(ctx context.Context) error { var err error; snapshot, err = load(ctx); return err }}}
	for _, step := range steps {
		step := step
		changes = append(changes, settings.PreparedChange{Module: step.Module, Apply: func(ctx context.Context) error {
			if snapshot == nil {
				return nil
			}
			return step.Apply(ctx, snapshot)
		}})
	}
	return changes
}
