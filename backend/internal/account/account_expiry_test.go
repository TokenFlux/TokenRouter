package account

import (
	"context"
	"testing"
	"time"
)

type s06AccountExpiryRepo struct {
	ExpiryRepository
	started chan struct{}
}

func (r *s06AccountExpiryRepo) AutoPauseExpiredAccounts(context.Context, time.Time) (int64, error) {
	close(r.started)
	return 0, nil
}

// 已停止的拥有者不能因重复 Start 再执行到期扫描。
func TestS06AccountExpiryCannotRestartAfterStop(t *testing.T) {
	repo := &s06AccountExpiryRepo{started: make(chan struct{})}
	svc := NewExpiryService(repo, ExpiryOptions{Interval: time.Hour})
	svc.Stop()
	svc.Start()
	defer svc.Stop()
	select {
	case <-repo.started:
		t.Fatal("stopped expiry worker executed another scan")
	case <-time.After(30 * time.Millisecond):
	}
}
