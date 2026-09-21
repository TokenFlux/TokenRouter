package codec

import (
	"fmt"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// recordSnapshot 的凭据仅能由 Adapter 解包；调度核心接口只暴露重建元数据。
type recordSnapshot struct{ value *account.Record }

func (s recordSnapshot) SnapshotMetadata() scheduler.SnapshotMetadata {
	return scheduler.SnapshotMetadata{
		ID: s.value.ID, Name: s.value.Name, Platform: s.value.Platform,
		GroupIDs: slices.Clone(s.value.GroupIDs), MixedScheduling: s.value.IsMixedSchedulingEnabled(),
	}
}

func WrapRecord(value *account.Record) scheduler.SnapshotAccount {
	if value == nil {
		return nil
	}
	return recordSnapshot{value: value}
}

// RecordValue 仅供受控账号读取和缓存编码使用，不向评分核心输出凭据。
func RecordValue(value scheduler.SnapshotAccount) (*account.Record, error) {
	if value == nil {
		return nil, nil
	}
	stored, ok := value.(recordSnapshot)
	if !ok {
		return nil, fmt.Errorf("unexpected legacy scheduler snapshot data %T", value)
	}
	return stored.value, nil
}
