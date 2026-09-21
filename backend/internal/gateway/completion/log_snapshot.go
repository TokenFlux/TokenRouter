package completion

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

// SnapshotLogWriter 隔离存储调用的可变记录；同步兜底再次取得同一输入的独立副本。
func SnapshotLogWriter(repo LogWriter) LogWriter {
	if repo == nil {
		return nil
	}
	writer := snapshotLogWriter{repo: repo}
	if best, ok := repo.(BestEffortLogWriter); ok {
		return snapshotBestEffortLogWriter{snapshotLogWriter: writer, best: best}
	}
	return writer
}

type snapshotLogWriter struct{ repo LogWriter }

func (w snapshotLogWriter) Create(ctx context.Context, row *UsageLog) (bool, error) {
	return w.repo.Create(ctx, querycache.Clone(row))
}

type snapshotBestEffortLogWriter struct {
	snapshotLogWriter
	best BestEffortLogWriter
}

func (w snapshotBestEffortLogWriter) CreateBestEffort(ctx context.Context, row *UsageLog) error {
	return w.best.CreateBestEffort(ctx, querycache.Clone(row))
}
