package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// sessionWindowMockRepo 记录窗口写入，非预期健康操作保持失败。
type sessionWindowMockRepo struct {
	// 捕获实际写入。
	sessionWindowCalls []swCall
	updateExtraCalls   []ueCall
	clearRateLimitIDs  []int64
}

var _ accountcore.SessionWindowStore = (*sessionWindowMockRepo)(nil)

type swCall struct {
	ID     int64
	Start  *time.Time
	End    *time.Time
	Status string
}

type ueCall struct {
	ID      int64
	Updates map[string]any
}

func (m *sessionWindowMockRepo) UpdateSessionWindow(_ context.Context, id int64, start, end *time.Time, status string) error {
	m.sessionWindowCalls = append(m.sessionWindowCalls, swCall{ID: id, Start: start, End: end, Status: status})
	return nil
}
func (m *sessionWindowMockRepo) UpdateSessionWindowEnd(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (m *sessionWindowMockRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	m.updateExtraCalls = append(m.updateExtraCalls, ueCall{ID: id, Updates: updates})
	return nil
}
func (m *sessionWindowMockRepo) ClearRateLimit(_ context.Context, id int64) error {
	m.clearRateLimitIDs = append(m.clearRateLimitIDs, id)
	return nil
}
func (m *sessionWindowMockRepo) ClearAntigravityQuotaScopes(_ context.Context, _ int64) error {
	return nil
}
func (m *sessionWindowMockRepo) ClearModelRateLimits(_ context.Context, _ int64) error {
	return nil
}
func (m *sessionWindowMockRepo) ClearTempUnschedulable(_ context.Context, _ int64) error {
	return nil
}

// 测试只装配窗口存储与原健康恢复，未使用的健康写入不得被意外调用。
func newSessionWindowService(repo *sessionWindowMockRepo) *accountcore.HealthService {
	recovery := accountcore.NewRecoveryService(repo, nil, accountcore.RecoveryOptions{})
	return accountcore.NewHealthService(repo, nil, accountcore.HealthOptions{SessionWindows: repo, ClearWindowRateLimit: recovery.ClearRateLimit})
}
func (m *sessionWindowMockRepo) GetByID(context.Context, int64) (*accountcore.Record, error) {
	panic("unexpected")
}
func (m *sessionWindowMockRepo) ClearError(context.Context, int64) error       { panic("unexpected") }
func (m *sessionWindowMockRepo) SetError(context.Context, int64, string) error { panic("unexpected") }
func (m *sessionWindowMockRepo) SetOverloaded(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (m *sessionWindowMockRepo) SetRateLimited(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (m *sessionWindowMockRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	panic("unexpected")
}
func (m *sessionWindowMockRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	panic("unexpected")
}

func TestUpdateSessionWindow_UsesResetHeader(t *testing.T) {
	// The reset header provides the real window end as a Unix timestamp.
	// UpdateSessionWindow should use it instead of the hour-truncated prediction.
	resetUnix := time.Now().Add(3 * time.Hour).Unix()
	wantEnd := time.Unix(resetUnix, 0)
	wantStart := wantEnd.Add(-5 * time.Hour)

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{ID: 42} // no existing window → needInitWindow=true
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", resetUnix))

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	if call.ID != 42 {
		t.Errorf("expected account ID 42, got %d", call.ID)
	}
	if call.End == nil || !call.End.Equal(wantEnd) {
		t.Errorf("expected window end %v, got %v", wantEnd, call.End)
	}
	if call.Start == nil || !call.Start.Equal(wantStart) {
		t.Errorf("expected window start %v, got %v", wantStart, call.Start)
	}
	if call.Status != "allowed" {
		t.Errorf("expected status 'allowed', got %q", call.Status)
	}
}

func TestUpdateSessionWindow_FallbackPredictionWhenNoResetHeader(t *testing.T) {
	// When the reset header is absent, should fall back to hour-truncated prediction.
	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{ID: 10} // no existing window
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed_warning")
	// No anthropic-ratelimit-unified-5h-reset header

	// Capture now before the call to avoid hour-boundary races
	now := time.Now()
	expectedStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
	expectedEnd := expectedStart.Add(5 * time.Hour)

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	if call.End == nil {
		t.Fatal("expected window end to be set (fallback prediction)")
	}
	// Fallback: start = current hour truncated, end = start + 5h

	if !call.End.Equal(expectedEnd) {
		t.Errorf("expected fallback end %v, got %v", expectedEnd, *call.End)
	}
	if call.Start == nil || !call.Start.Equal(expectedStart) {
		t.Errorf("expected fallback start %v, got %v", expectedStart, call.Start)
	}
}

func TestUpdateSessionWindow_CorrectsStalePrediction(t *testing.T) {
	// When the stored SessionWindowEnd is wrong (from a previous prediction),
	// and the reset header provides the real time, it should update the window.
	staleEnd := time.Now().Add(2 * time.Hour)             // existing prediction: 2h from now
	realResetUnix := time.Now().Add(4 * time.Hour).Unix() // real reset: 4h from now
	wantEnd := time.Unix(realResetUnix, 0)

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{
		ID:               55,
		SessionWindowEnd: &staleEnd,
	}
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", realResetUnix))

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	if call.End == nil || !call.End.Equal(wantEnd) {
		t.Errorf("expected corrected end %v, got %v", wantEnd, call.End)
	}
}

func TestUpdateSessionWindow_NoUpdateWhenHeaderMatchesStored(t *testing.T) {
	// If the reset header matches the stored SessionWindowEnd, no window update needed.
	futureUnix := time.Now().Add(3 * time.Hour).Unix()
	existingEnd := time.Unix(futureUnix, 0)

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{
		ID:               77,
		SessionWindowEnd: &existingEnd,
	}
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", futureUnix)) // same as stored

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	if len(repo.sessionWindowCalls) != 1 {
		t.Fatalf("expected 1 UpdateSessionWindow call, got %d", len(repo.sessionWindowCalls))
	}

	call := repo.sessionWindowCalls[0]
	// windowStart and windowEnd should be nil (no update needed)
	if call.Start != nil || call.End != nil {
		t.Errorf("expected nil start/end (no window change needed), got start=%v end=%v", call.Start, call.End)
	}
	// Status is still updated
	if call.Status != "allowed" {
		t.Errorf("expected status 'allowed', got %q", call.Status)
	}
}

func TestUpdateSessionWindow_ClearsUtilizationOnWindowReset(t *testing.T) {
	// When needInitWindow=true and window is set, utilization should be cleared.
	resetUnix := time.Now().Add(3 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{ID: 33} // no existing window → needInitWindow=true
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", resetUnix))
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.15")

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	// Should have 2 UpdateExtra calls: one to clear utilization, one to store new utilization
	if len(repo.updateExtraCalls) != 2 {
		t.Fatalf("expected 2 UpdateExtra calls, got %d", len(repo.updateExtraCalls))
	}

	// First call: clear utilization (nil value)
	clearCall := repo.updateExtraCalls[0]
	if clearCall.Updates["session_window_utilization"] != nil {
		t.Errorf("expected utilization cleared to nil, got %v", clearCall.Updates["session_window_utilization"])
	}

	// Second call: store new utilization
	storeCall := repo.updateExtraCalls[1]
	if val, ok := storeCall.Updates["session_window_utilization"].(float64); !ok || val != 0.15 {
		t.Errorf("expected utilization stored as 0.15, got %v", storeCall.Updates["session_window_utilization"])
	}
}

func TestUpdateSessionWindow_NoClearUtilizationOnCorrection(t *testing.T) {
	// When correcting a stale prediction (needInitWindow=false), utilization should NOT be cleared.
	staleEnd := time.Now().Add(2 * time.Hour)
	realResetUnix := time.Now().Add(4 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{
		ID:               66,
		SessionWindowEnd: &staleEnd,
	}
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", realResetUnix))
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.30")

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	// Only 1 UpdateExtra call (store utilization), no clear call
	if len(repo.updateExtraCalls) != 1 {
		t.Fatalf("expected 1 UpdateExtra call (no clear), got %d", len(repo.updateExtraCalls))
	}

	if val, ok := repo.updateExtraCalls[0].Updates["session_window_utilization"].(float64); !ok || val != 0.30 {
		t.Errorf("expected utilization 0.30, got %v", repo.updateExtraCalls[0].Updates["session_window_utilization"])
	}
}

func TestUpdateSessionWindow_SamplesFable7dOiHeaders(t *testing.T) {
	// 被动采样应收集 7d_oi（Fable 专属 7d 窗口）的 utilization 和 reset。
	existingEnd := time.Now().Add(3 * time.Hour)
	resetOIUnix := time.Now().Add(80 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{ID: 90, SessionWindowEnd: &existingEnd} // 复用现有窗口，不触发初始化
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d_oi-utilization", "0.87")
	headers.Set("anthropic-ratelimit-unified-7d_oi-reset", fmt.Sprintf("%d", resetOIUnix))

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	if len(repo.updateExtraCalls) != 1 {
		t.Fatalf("expected 1 UpdateExtra call, got %d", len(repo.updateExtraCalls))
	}
	updates := repo.updateExtraCalls[0].Updates
	if val, ok := updates["passive_usage_7d_oi_utilization"].(float64); !ok || val != 0.87 {
		t.Errorf("expected passive_usage_7d_oi_utilization=0.87, got %v", updates["passive_usage_7d_oi_utilization"])
	}
	if val, ok := updates["passive_usage_7d_oi_reset"].(int64); !ok || val != resetOIUnix {
		t.Errorf("expected passive_usage_7d_oi_reset=%d, got %v", resetOIUnix, updates["passive_usage_7d_oi_reset"])
	}
}

func TestUpdateSessionWindow_ClearsFable7dOiOnWindowReset(t *testing.T) {
	// 5h 窗口重置时应连同清除 7d_oi 被动采样数据，与 7d 行为一致。
	resetUnix := time.Now().Add(3 * time.Hour).Unix()

	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{ID: 91} // 无现有窗口，需要执行初始化
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-reset", fmt.Sprintf("%d", resetUnix))

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(headers))

	if len(repo.updateExtraCalls) != 1 {
		t.Fatalf("expected 1 UpdateExtra (clear) call, got %d", len(repo.updateExtraCalls))
	}
	clearUpdates := repo.updateExtraCalls[0].Updates
	for _, key := range []string{"passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset"} {
		if val, present := clearUpdates[key]; !present || val != nil {
			t.Errorf("expected %s cleared to nil on window reset, got present=%v val=%v", key, present, val)
		}
	}
}

func TestUpdateSessionWindow_NoStatusHeader(t *testing.T) {
	// Should return immediately if no status header.
	repo := &sessionWindowMockRepo{}
	svc := newSessionWindowService(repo)

	account := &accountcore.Record{ID: 1}

	svc.UpdateSessionWindow(context.Background(), account, SessionWindowObservation(http.Header{}))

	if len(repo.sessionWindowCalls) != 0 {
		t.Errorf("expected no calls when status header absent, got %d", len(repo.sessionWindowCalls))
	}
}
