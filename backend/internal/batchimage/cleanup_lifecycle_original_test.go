//go:build unit

package batchimage_test

import (
	"context"

	"runtime"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

type s13CleanupRepo struct {
	batchimage.BatchImageRepository
}

func (*s13CleanupRepo) ListBatchImageJobsDueForInputCleanup(ctx context.Context, _ time.Time, _ int) ([]*batchimage.BatchImageJob, error) {
	return nil, ctx.Err()
}

func (*s13CleanupRepo) ListBatchImageJobsDueForOutputCleanup(ctx context.Context, _ time.Time, _ int) ([]*batchimage.BatchImageJob, error) {
	return nil, ctx.Err()
}

func TestS13CleanupImmediateStop(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	core := &batchimage.Cleanup{Repo: &s13CleanupRepo{}}
	s := batchimage.NewRuntime("batch image cleanup", true, core.Run)
	s.Start()
	s.Stop()
}
