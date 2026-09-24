package provider

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/require"
)

type openAICodexSnapshotAsyncRepo struct {
	updateExtraCh chan map[string]any
	rateLimitCh   chan time.Time
}

func (r *openAICodexSnapshotAsyncRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	if r.rateLimitCh != nil {
		r.rateLimitCh <- resetAt
	}
	return nil
}
func (r *openAICodexSnapshotAsyncRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.updateExtraCh != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCh <- copied
	}
	return nil
}
func TestOpenAIGatewayService_UpdateCodexUsageSnapshot_ExhaustedSnapshotDoesNotSetRateLimit(t *testing.T) {
	repo := &openAICodexSnapshotAsyncRepo{
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := &CodexUsageObserver{Store: repo, Throttle: accountcore.NewWriteThrottle(30 * time.Second)}
	snapshot := &openai.OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         snapshotFloat(100),
		PrimaryResetAfterSeconds:   snapshotInt(3600),
		PrimaryWindowMinutes:       snapshotInt(10080),
		SecondaryUsedPercent:       snapshotFloat(12),
		SecondaryResetAfterSeconds: snapshotInt(1200),
		SecondaryWindowMinutes:     snapshotInt(300),
	}
	svc.Observe(context.Background(), 601, snapshot)

	select {
	case updates := <-repo.updateExtraCh:
		require.Equal(t, 100.0, updates["codex_7d_used_percent"])
	case <-time.After(2 * time.Second):
		t.Fatal("等待 codex 快照落库超时")
	}

	select {
	case resetAt := <-repo.rateLimitCh:
		t.Fatalf("不应因仅写入快照而生成运行时限流时间: %v", resetAt)
	case <-time.After(2 * time.Second):
	}
}
func TestOpenAIGatewayService_UpdateCodexUsageSnapshot_NonExhaustedSnapshotDoesNotSetRateLimit(t *testing.T) {
	repo := &openAICodexSnapshotAsyncRepo{
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := &CodexUsageObserver{Store: repo, Throttle: accountcore.NewWriteThrottle(30 * time.Second)}
	snapshot := &openai.OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         snapshotFloat(94),
		PrimaryResetAfterSeconds:   snapshotInt(3600),
		PrimaryWindowMinutes:       snapshotInt(10080),
		SecondaryUsedPercent:       snapshotFloat(22),
		SecondaryResetAfterSeconds: snapshotInt(1200),
		SecondaryWindowMinutes:     snapshotInt(300),
	}
	svc.Observe(context.Background(), 602, snapshot)

	select {
	case <-repo.updateExtraCh:
	case <-time.After(2 * time.Second):
		t.Fatal("等待 codex 快照落库超时")
	}

	select {
	case resetAt := <-repo.rateLimitCh:
		t.Fatalf("不应写入运行时限流时间: %v", resetAt)
	case <-time.After(200 * time.Millisecond):
	}
}
func TestOpenAIGatewayService_UpdateCodexUsageSnapshot_ThrottlesExtraWrites(t *testing.T) {
	repo := &openAICodexSnapshotAsyncRepo{
		updateExtraCh: make(chan map[string]any, 2),
	}
	svc := &CodexUsageObserver{Store: repo, Throttle: accountcore.NewWriteThrottle(time.Hour)}
	snapshot := &openai.OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         snapshotFloat(94),
		PrimaryResetAfterSeconds:   snapshotInt(3600),
		PrimaryWindowMinutes:       snapshotInt(10080),
		SecondaryUsedPercent:       snapshotFloat(22),
		SecondaryResetAfterSeconds: snapshotInt(1200),
		SecondaryWindowMinutes:     snapshotInt(300),
	}

	svc.Observe(context.Background(), 777, snapshot)
	svc.Observe(context.Background(), 777, snapshot)

	select {
	case <-repo.updateExtraCh:
	case <-time.After(2 * time.Second):
		t.Fatal("等待第一次 codex 快照落库超时")
	}

	select {
	case updates := <-repo.updateExtraCh:
		t.Fatalf("unexpected second codex snapshot write: %v", updates)
	case <-time.After(200 * time.Millisecond):
	}
}

type snapshotUpdateAccountRepo struct {
	updateExtraCalls chan map[string]any
}

func (r *snapshotUpdateAccountRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	if r.updateExtraCalls != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCalls <- copied
	}
	return nil
}
func TestOpenAIUpdateCodexUsageSnapshotFromHeaders(t *testing.T) {
	repo := &snapshotUpdateAccountRepo{updateExtraCalls: make(chan map[string]any, 1)}
	svc := &CodexUsageObserver{Store: repo, Throttle: accountcore.NewWriteThrottle(30 * time.Second)}
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "12")
	headers.Set("x-codex-secondary-used-percent", "34")
	headers.Set("x-codex-primary-window-minutes", "300")
	headers.Set("x-codex-secondary-window-minutes", "10080")
	headers.Set("x-codex-primary-reset-after-seconds", "600")
	headers.Set("x-codex-secondary-reset-after-seconds", "86400")

	svc.Headers(context.Background(), 123, headers)

	select {
	case updates := <-repo.updateExtraCalls:
		require.Equal(t, 12.0, updates["codex_5h_used_percent"])
		require.Equal(t, 34.0, updates["codex_7d_used_percent"])
		require.Equal(t, 600, updates["codex_5h_reset_after_seconds"])
		require.Equal(t, 86400, updates["codex_7d_reset_after_seconds"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected UpdateExtra to be called")
	}
}
func snapshotFloat(v float64) *float64 { return &v }
func snapshotInt(v int) *int           { return &v }
