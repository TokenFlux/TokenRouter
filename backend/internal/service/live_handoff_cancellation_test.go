package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	coderws "github.com/coder/websocket"
)

// 把取消固定在已经取得控制权、尚未查询账号的交接点。
type s11PlanningLiveStore struct {
	liveTestStore
	cancel context.CancelFunc
}

func (s *s11PlanningLiveStore) ClaimLiveController(ctx context.Context, hash, controller, owner string) (bool, error) {
	ok, err := s.liveTestStore.ClaimLiveController(ctx, hash, controller, owner)
	s.cancel()
	return ok, err
}

type s11PlanningLiveAccounts struct {
	AccountRepository
	reads atomic.Int32
}

func (r *s11PlanningLiveAccounts) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.reads.Add(1)
	return nil, ctx.Err()
}
func TestLiveHandoffCancellationStopsBeforeAccountLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	record := &session.LiveCallRecord{CallHash: "planning", Controller: session.LiveControllerPending, AccountID: 7, ExpiresAt: time.Now().Add(time.Minute)}
	store := &s11PlanningLiveStore{liveTestStore: liveTestStore{record: record}, cancel: cancel}
	accounts := &s11PlanningLiveAccounts{}
	s := &OpenAIGatewayService{cache: store, accountRepo: accounts, liveObserverStopped: true}
	start := time.Now()
	err := s.ProxyLiveSideband(ctx, record, &coderws.Conn{})
	if err != context.Canceled {
		t.Errorf("expected cancellation, got %v", err)
	}
	if time.Since(start) >= 100*time.Millisecond {
		t.Error("cancelled Live handoff still slept for the observer interval")
	}
	if accounts.reads.Load() != 0 {
		t.Errorf("cancelled Live handoff still queried execution account: %d", accounts.reads.Load())
	}
}
