package openai

import (
	"context"
	"errors"
	"testing"
)

func BenchmarkOpenAIWSPoolAcquire(b *testing.B) {
	cfg := &WSPoolOptions{}
	cfg.MaxConnsPerAccount = 8
	cfg.MinIdlePerAccount = 1
	cfg.MaxIdlePerAccount = 4
	cfg.QueueLimitPerConn = 256
	cfg.DialTimeoutSeconds = 1

	pool := newStartedWSConnPoolForTest(cfg)
	pool.SetClientDialerForTest(&openAIWSCountingDialer{})

	account := &WSPoolAccount{ID: 1001, Type: "apikey"}
	req := WSAcquireRequest{
		Account: account,
		WSURL:   "wss://example.com/v1/responses",
	}
	ctx := context.Background()

	lease, err := pool.Acquire(ctx, req)
	if err != nil {
		b.Fatalf("warm acquire failed: %v", err)
	}
	lease.Release()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var (
				got        *WSConnLease
				acquireErr error
			)
			for retry := 0; retry < 3; retry++ {
				got, acquireErr = pool.Acquire(ctx, req)
				if acquireErr == nil {
					break
				}
				if !errors.Is(acquireErr, errOpenAIWSConnClosed) {
					break
				}
			}
			if acquireErr != nil {
				b.Fatalf("acquire failed: %v", acquireErr)
			}
			got.Release()
		}
	})
}
