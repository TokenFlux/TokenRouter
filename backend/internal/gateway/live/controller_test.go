package live

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// cancelClaimStore 在成功取得控制权的边界取消，验证 B06 不执行后续账号查询。
type cancelClaimStore struct {
	session.LiveCallStore
	cancel   context.CancelFunc
	released atomic.Int32
}

func (s *cancelClaimStore) ClaimLiveController(context.Context, string, string, string) (bool, error) {
	s.cancel()
	return true, nil
}
func (s *cancelClaimStore) ReleaseLiveController(ctx context.Context, _ string, _ string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	s.released.Add(1)
	return true, nil
}

type controllerPorts struct {
	Ports
	store   session.LiveCallStore
	queries atomic.Int32
}

func (p *controllerPorts) Store() (session.LiveCallStore, error) { return p.store, nil }
func (p *controllerPorts) Target(context.Context, *session.LiveCallRecord) (Target, error) {
	p.queries.Add(1)
	return nil, errors.New("unexpected target read")
}
func (p *controllerPorts) BeginObserver(string) (context.Context, func(), bool) {
	return nil, nil, false
}

type idleFrames struct{}

func (idleFrames) ReadFrame(ctx context.Context) (int, []byte, error) {
	<-ctx.Done()
	return 0, nil, ctx.Err()
}
func (idleFrames) WriteFrame(context.Context, int, []byte) error { return nil }
func (idleFrames) SetReadLimit(int64)                            {}
func (idleFrames) Close() error                                  { return nil }

func TestSidebandCancelledAfterClaimReleasesWithoutTarget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &cancelClaimStore{cancel: cancel}
	ports := &controllerPorts{store: store}
	core := New(ports, time.Second, 1<<20)
	err := core.ProxyLiveSideband(ctx, &session.LiveCallRecord{CallHash: "call", ExpiresAt: time.Now().Add(time.Hour)}, idleFrames{})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(0), ports.queries.Load())
	require.Equal(t, int32(1), store.released.Load())
}

// 关闭认领成功一次只产生一条零费用事实，并释放原租约。
type finalizeStore struct {
	session.LiveCallStore
	closed atomic.Bool
}

func (s *finalizeStore) MarkLiveCallClosed(context.Context, string, time.Duration) (bool, error) {
	return s.closed.CompareAndSwap(false, true), nil
}

type finalLeases struct {
	scheduler.LiveConcurrencyCache
	released atomic.Int32
}

func (s *finalLeases) ReleaseLiveLease(context.Context, int64, int64, int64, string) error {
	s.released.Add(1)
	return nil
}

type finalizePorts struct {
	Ports
	store  *finalizeStore
	leases *finalLeases
	writes atomic.Int32
}

func (p *finalizePorts) Store() (session.LiveCallStore, error)           { return p.store, nil }
func (p *finalizePorts) Leases() (scheduler.LiveConcurrencyCache, error) { return p.leases, nil }
func (p *finalizePorts) RecordZeroUsage(context.Context, *session.LiveCallRecord, int) {
	p.writes.Add(1)
}
func TestFinalizeClaimsOnceBeforeLeaseAndZeroUsage(t *testing.T) {
	ports := &finalizePorts{store: &finalizeStore{}, leases: &finalLeases{}}
	core := New(ports, time.Second, 1<<20)
	record := &session.LiveCallRecord{CallHash: "call", AccountID: 1, UserID: 2, APIKeyID: 3, LeaseID: "lease", CreatedAt: time.Now()}
	core.Finalize(record)
	core.Finalize(record)
	require.Equal(t, int32(1), ports.writes.Load())
	require.Equal(t, int32(1), ports.leases.released.Load())
}
