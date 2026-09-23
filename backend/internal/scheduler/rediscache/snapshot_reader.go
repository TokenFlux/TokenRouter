package rediscache

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
)

// SnapshotReader 衔接唯一快照服务及格式解码，不拥有缓存、预算或另一条数据库回源路径。
type SnapshotReader struct{ source *scheduler.SnapshotService }

func NewSnapshotReader(source *scheduler.SnapshotService) *SnapshotReader {
	return &SnapshotReader{source: source}
}

func (s *SnapshotReader) GetAccount(ctx context.Context, id int64) (*account.Record, error) {
	value, err := s.source.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	return codec.RecordValue(value)
}
func (s *SnapshotReader) ListAccounts(ctx context.Context, group *int64, platform string, forced bool) ([]account.Record, bool, error) {
	values, mixed, err := s.source.ListSchedulableAccounts(ctx, group, platform, forced)
	if err != nil {
		return nil, mixed, err
	}
	if values == nil {
		return nil, mixed, nil
	}
	out := make([]account.Record, 0, len(values))
	for _, value := range values {
		record, err := codec.RecordValue(value)
		if err != nil {
			return nil, mixed, err
		}
		if record != nil {
			out = append(out, *record)
		}
	}
	return out, mixed, nil
}
