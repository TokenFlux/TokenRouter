package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
)

// provideSelectionSnapshots 发布原快照适配器的受控读取，缺省来源保持真正的 nil。
func provideSelectionSnapshots(source *scheduler.SnapshotService) selection.Snapshots {
	if source == nil {
		return nil
	}
	return schedulerredis.NewSnapshotReader(source)
}
