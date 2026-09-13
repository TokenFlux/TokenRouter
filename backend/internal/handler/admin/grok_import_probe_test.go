//go:build unit

package admin

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

type grokImportProbeStub struct {
	mu           sync.Mutex
	calls        map[int64]int
	failures     map[int64]error
	active       int
	maxActive    int
	deadlineSeen bool
	block        <-chan struct{}
	started      chan int64
	done         chan int64
}

func newGrokImportProbeStub(buffer int) *grokImportProbeStub {
	return &grokImportProbeStub{
		calls:    make(map[int64]int),
		failures: make(map[int64]error),
		started:  make(chan int64, buffer),
		done:     make(chan int64, buffer),
	}
}

func (s *grokImportProbeStub) QueryQuota(ctx context.Context, accountID int64) (*service.GrokQuotaProbeResult, error) {
	_, deadlineSeen := ctx.Deadline()
	s.mu.Lock()
	s.calls[accountID]++
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.deadlineSeen = s.deadlineSeen || deadlineSeen
	s.mu.Unlock()

	s.started <- accountID
	var ctxErr error
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			ctxErr = ctx.Err()
		}
	}

	s.mu.Lock()
	s.active--
	failure := s.failures[accountID]
	s.mu.Unlock()
	s.done <- accountID
	if ctxErr != nil {
		return nil, ctxErr
	}
	if failure != nil {
		return nil, failure
	}
	return &service.GrokQuotaProbeResult{
		Source:         "hybrid_probe",
		Model:          "grok-4.5",
		StatusCode:     200,
		ResetSupported: false,
	}, nil
}

func (s *grokImportProbeStub) snapshot() (map[int64]int, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make(map[int64]int, len(s.calls))
	for id, count := range s.calls {
		calls[id] = count
	}
	return calls, s.maxActive, s.deadlineSeen
}

func awaitGrokProbeSignal(t *testing.T, signals <-chan int64) int64 {
	t.Helper()
	select {
	case id := <-signals:
		return id
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Grok import probe")
		return 0
	}
}
