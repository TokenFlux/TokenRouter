package notification

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	mailtest "github.com/TokenFlux/TokenRouter/internal/notification/testkit"

	"github.com/TokenFlux/TokenRouter/internal/notification/smtp"
	"github.com/stretchr/testify/require"
)

// 并发夹具验证投递去重和退订初始化，不要求两个调用同时完成去重读取。
func TestS10ConcurrentNotificationDelivery(t *testing.T) {
	repo := mailtest.NewMemorySettings()
	server := mailtest.StartSMTPServer(t)
	require.NoError(t, repo.SetMultiple(context.Background(), server.Settings()))
	n := NewNotificationEmailService(repo, NewMailer(repo, smtp.New()))
	input := SendRequest{Event: NotificationEmailEventSubscriptionExpiryReminder, RecipientEmail: "fixture@example.com", SourceType: "subscription", SourceID: "1", ReminderKey: "7d"}
	start := make(chan struct{})
	errs := make(chan error, 16)
	for range 16 {
		go func() { <-start; errs <- n.Send(context.Background(), input) }()
	}
	close(start)
	for range 16 {
		require.NoError(t, <-errs)
	}
	require.Equal(t, int64(1), server.MessageCount())
	require.Empty(t, n.locks.entries)
}
func TestS10ConcurrentFirstUnsubscribeSecret(t *testing.T) {
	n := NewNotificationEmailService(mailtest.NewMemorySettings(), nil)
	start := make(chan struct{})
	tokens := make(chan string, 16)
	errs := make(chan error, 16)
	for range 16 {
		go func() {
			<-start
			token, err := n.createUnsubscribeToken(context.Background(), "fixture@example.com", NotificationEmailEventBalanceLow)
			tokens <- token
			errs <- err
		}()
	}
	close(start)
	for range 16 {
		token := <-tokens
		require.NoError(t, <-errs)
		_, err := n.parseUnsubscribeToken(context.Background(), token)
		require.NoError(t, err)
	}
	require.Empty(t, n.locks.entries)
}
func TestS10NotificationLockCancellationAndIsolation(t *testing.T) {
	var locks keyCoordinator
	release, err := locks.acquire(context.Background(), "one")
	require.NoError(t, err)
	other, err := locks.acquire(context.Background(), "two")
	require.NoError(t, err)
	other()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := locks.acquire(ctx, "one"); done <- err }()
	require.Eventually(t, func() bool { locks.mu.Lock(); defer locks.mu.Unlock(); return locks.entries["one"].refs == 2 }, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	release()
	release()
	require.Empty(t, locks.entries)
}
func TestS10SMTPContextCancellation(t *testing.T) {
	t.Run("before-send", func(t *testing.T) {
		repo := mailtest.NewMemorySettings()
		server := mailtest.StartSMTPServer(t)
		require.NoError(t, repo.SetMultiple(context.Background(), server.Settings()))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, NewMailer(repo, smtp.New()).SendEmail(ctx, "fixture@example.com", "fixture", "fixture"), context.Canceled)
		require.Zero(t, server.MessageCount())
	})
	t.Run("in-flight", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()
		accepted := make(chan net.Conn, 1)
		go func() {
			conn, e := ln.Accept()
			if e == nil {
				accepted <- conn
			}
		}()
		repo := mailtest.NewMemorySettings()
		address, ok := ln.Addr().(*net.TCPAddr)
		require.True(t, ok)
		require.NoError(t, repo.SetMultiple(context.Background(), map[string]string{SettingKeySMTPHost: "127.0.0.1", SettingKeySMTPPort: fmt.Sprint(address.Port), SettingKeySMTPFrom: "sender@example.com", SettingKeySMTPUsername: "fixture", SettingKeySMTPPassword: "fixture"}))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- NewMailer(repo, smtp.New()).SendEmail(ctx, "fixture@example.com", "fixture", "fixture")
		}()
		conn := <-accepted
		defer func() { _ = conn.Close() }()
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("SMTP 未响应取消")
		}
	})
}

type blockingMailTask struct {
	started chan struct{}
	once    sync.Once
}

func (p *blockingMailTask) SendVerifyCode(ctx context.Context, _, _ string, _ ...string) error {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return ctx.Err()
}
func (p *blockingMailTask) SendPasswordResetEmailWithCooldown(ctx context.Context, a, b, c string, d ...string) error {
	return p.SendVerifyCode(ctx, a, b, d...)
}
func TestS10MailQueueBoundedDrain(t *testing.T) {
	p := &blockingMailTask{started: make(chan struct{})}
	q := NewEmailQueueService(p, 1)
	require.NoError(t, q.EnqueueVerifyCode("fixture@example.com", "fixture"))
	require.False(t, q.started)
	q.Start()
	q.Start()
	<-p.started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := q.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Error(t, q.EnqueueVerifyCode("fixture@example.com", "fixture"))
	require.ErrorIs(t, q.StopContext(context.Background()), context.DeadlineExceeded)
	q.Start()
	require.True(t, q.stopped)
}

func TestS10MailQueueStopBeforeStartReportsPending(t *testing.T) {
	queue := NewEmailQueueService(nil, 1)
	require.NoError(t, queue.EnqueueVerifyCode("fixture@example.com", "fixture"))
	require.ErrorContains(t, queue.StopContext(context.Background()), "1 tasks")
	queue.Start()
	require.False(t, queue.started)
}
