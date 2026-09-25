package provider

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 手动查询也必须属于资源拥有者：停止时取消并等待，迟到响应不能继续写快照。
func TestOllamaUsageStopOwnsManualRefresh(t *testing.T) {
	value := ollamaUsageAccount(901)
	value.Extra[account.OllamaCloudUsageSessionExtraKey] = "cipher:wos-session=fixture"
	repo := &ollamaUsageTestRepo{ollamaUsageRows: &ollamaUsageRows{accounts: map[int64]*account.Record{value.ID: value}}}
	entered, cancelled := make(chan struct{}), make(chan struct{})
	upstream := &ollamaUsageHTTPStub{body: ollamaUsageFixture(t), beforeResponse: func(req *http.Request) { close(entered); <-req.Context().Done(); close(cancelled) }}
	svc := newOllamaUsageTestService(t, repo, upstream, &ollamaUsageSettings{}, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := svc.Refresh(ctx, value.ID); done <- err }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("手动查询未退出")
		}
	})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("手动查询未进入 HTTP")
	}
	svc.Stop()
	select {
	case <-cancelled:
	default:
		t.Error("停止没有取消手动查询")
	}
	require.NotContains(t, value.Extra, account.OllamaCloudUsageSnapshotExtraKey)
}
