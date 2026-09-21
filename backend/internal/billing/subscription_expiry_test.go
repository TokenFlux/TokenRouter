package billing

import (
	"bytes"
	"context"
	"errors"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionExpiryReminderSendTimeoutDoesNotPoisonNextPageList(t *testing.T) {
	now := time.Now()
	repo := &subscriptionExpiryRepoStub{
		pages: [][]UserSubscription{
			{
				{
					ID:        1,
					UserID:    10,
					PlanID:    100,
					StartsAt:  now.Add(-24 * time.Hour),
					ExpiresAt: now.Add(7 * SubscriptionDailyWindow),
					Status:    SubscriptionStatusActive,
					User:      &UserSummary{ID: 10, Email: "first@example.com", Username: "first"},
					Plan:      &SubscriptionPlan{Name: "Pro"},
				},
			},
			{
				{
					ID:        2,
					UserID:    20,
					PlanID:    100,
					StartsAt:  now.Add(-24 * time.Hour),
					ExpiresAt: now.Add(3 * SubscriptionDailyWindow),
					Status:    SubscriptionStatusActive,
					User:      &UserSummary{ID: 20, Email: "second@example.com", Username: "second"},
					Plan:      &SubscriptionPlan{Name: "Pro"},
				},
			},
		},
	}
	sender := &subscriptionExpiryBlockingSender{}
	svc := NewSubscriptionExpiryService(repo, ExpiryOptions{Interval: time.Minute, Observe: log.Printf})
	svc.notificationEmailService = sender
	svc.reminderSendTimeout = 10 * time.Millisecond
	svc.reminderListTimeout = time.Second

	svc.sendExpiryReminders(context.Background())

	require.Equal(t, 2, sender.calls)
	require.Equal(t, 2, repo.listCalls)
	require.Len(t, repo.listContextErrs, 2)
	require.NoError(t, repo.listContextErrs[0])
	require.NoError(t, repo.listContextErrs[1])
	require.ErrorIs(t, sender.errs[0], context.DeadlineExceeded)
}

type subscriptionExpiryRepoStub struct {
	UserSubscriptionRepository

	pages           [][]UserSubscription
	listCalls       int
	listContextErrs []error
}

func (r *subscriptionExpiryRepoStub) List(ctx context.Context, params pagination.PaginationParams, _userID, _planID *int64, status, _platform, sortBy, sortOrder string) ([]UserSubscription, *pagination.PaginationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	requireSubscriptionExpiryListParams(ctx, params, status, sortBy, sortOrder)
	r.listCalls++
	r.listContextErrs = append(r.listContextErrs, ctx.Err())
	pageIndex := params.Page - 1
	if pageIndex < 0 || pageIndex >= len(r.pages) {
		return nil, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize, Pages: len(r.pages)}, nil
	}
	return r.pages[pageIndex], &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize, Pages: len(r.pages)}, nil
}

func requireSubscriptionExpiryListParams(ctx context.Context, params pagination.PaginationParams, status, sortBy, sortOrder string) {
	if ctx == nil {
		panic("subscription expiry list ctx is nil")
	}
	if params.PageSize != 200 || status != SubscriptionStatusActive || sortBy != "expires_at" || sortOrder != "asc" {
		panic("unexpected subscription expiry list params")
	}
}

type subscriptionExpiryBlockingSender struct {
	readyErr error
	mu       sync.Mutex
	calls    int
	errs     []error
}

func (s *subscriptionExpiryBlockingSender) Send(ctx context.Context, input ExpiryReminder) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if input.DaysRemaining != 7 && input.DaysRemaining != 3 && input.DaysRemaining != 1 {
		return errors.New("unexpected event")
	}
	if call == 1 {
		<-ctx.Done()
		s.recordErr(ctx.Err())
		return ctx.Err()
	}
	s.recordErr(ctx.Err())
	return ctx.Err()
}

func (s *subscriptionExpiryBlockingSender) recordErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, err)
}

type subscriptionExpirySettingRepoStub struct {
	values   map[string]string
	err      error
	multiErr error
}

func (r *subscriptionExpirySettingRepoStub) Get(context.Context, string) (*settings.Setting, error) {
	return nil, settings.ErrSettingNotFound
}

func (r *subscriptionExpirySettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	value, ok := r.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

func (r *subscriptionExpirySettingRepoStub) Set(context.Context, string, string) error {
	return nil
}

func (r *subscriptionExpirySettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if r.multiErr != nil {
		return nil, r.multiErr
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r *subscriptionExpirySettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (r *subscriptionExpirySettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}

func (r *subscriptionExpirySettingRepoStub) Delete(context.Context, string) error {
	return nil
}

func TestSubscriptionExpiryService_ExpiryReminderEnabledDefaultsToTrue(t *testing.T) {
	svc := NewSubscriptionExpiryService(nil, ExpiryOptions{Interval: time.Minute, Observe: log.Printf})
	svc.settingRepo = &subscriptionExpirySettingRepoStub{values: map[string]string{}}

	require.True(t, svc.expiryReminderEnabled(context.Background()))
}

func TestSubscriptionExpiryService_ExpiryReminderDisabledSkipsSubscriptionScan(t *testing.T) {
	repo := &subscriptionExpiryRepoStub{}
	settingRepo := &subscriptionExpirySettingRepoStub{
		values: map[string]string{"subscription_expiry_notify_enabled": "false"},
	}
	svc := NewSubscriptionExpiryService(repo, ExpiryOptions{Interval: time.Minute, Observe: log.Printf})
	svc.settingRepo = settingRepo
	svc.notificationEmailService = &subscriptionExpiryBlockingSender{}

	svc.sendExpiryReminders(context.Background())

	require.Zero(t, repo.listCalls)
}

func TestSubscriptionExpiryService_ExpiryReminderSettingReadErrorFailsClosed(t *testing.T) {
	svc := NewSubscriptionExpiryService(nil, ExpiryOptions{Interval: time.Minute, Observe: log.Printf})
	svc.settingRepo = &subscriptionExpirySettingRepoStub{err: errors.New("db down")}

	require.False(t, svc.expiryReminderEnabled(context.Background()))
}

func TestSubscriptionExpiryService_ReminderSkipsScanWhenNotLeader(t *testing.T) {
	repo := &subscriptionExpiryRepoStub{}
	svc := NewSubscriptionExpiryService(repo, ExpiryOptions{Interval: time.Minute, Notifier: &subscriptionExpiryBlockingSender{}, Lease: func(context.Context, string, string, time.Duration) (func(), bool) { return nil, false }})
	svc.sendExpiryReminders(context.Background())
	require.Zero(t, repo.listCalls)
}
func TestSubscriptionExpiryService_ReminderRunsEveryCycleSingleInstance(t *testing.T) {
	for _, withLease := range []bool{false, true} {
		repo := &subscriptionExpiryRepoStub{}
		released := 0
		options := ExpiryOptions{Interval: time.Minute, Notifier: &subscriptionExpiryBlockingSender{}}
		if withLease {
			options.Lease = func(context.Context, string, string, time.Duration) (func(), bool) {
				return func() { released++ }, true
			}
		}
		svc := NewSubscriptionExpiryService(repo, options)
		for i := 0; i < 3; i++ {
			svc.sendExpiryReminders(context.Background())
		}
		require.Equal(t, 3, repo.listCalls)
		if withLease {
			require.Equal(t, 3, released)
		}
	}
}

func TestSubscriptionExpiryService_MissingSMTPSkipsReminderScanAndLogsOncePerInterval(t *testing.T) {
	repo := &subscriptionExpiryRepoStub{}
	settingRepo := &subscriptionExpirySettingRepoStub{values: map[string]string{}}

	svc := NewSubscriptionExpiryService(repo, ExpiryOptions{Interval: time.Minute, Observe: log.Printf})
	svc.settingRepo = settingRepo
	svc.notificationEmailService = &subscriptionExpiryBlockingSender{readyErr: ErrReminderTransportUnconfigured}

	var logs bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	svc.sendExpiryReminders(context.Background())
	svc.sendExpiryReminders(context.Background())

	require.Zero(t, repo.listCalls)
	require.Equal(t, 1, bytes.Count(logs.Bytes(), []byte("SMTP is not configured")))
}

func TestSubscriptionExpiryService_SMTPConfigReadErrorSkipsReminderScan(t *testing.T) {
	repo := &subscriptionExpiryRepoStub{}
	settingRepo := &subscriptionExpirySettingRepoStub{
		values:   map[string]string{},
		multiErr: errors.New("db down"),
	}

	svc := NewSubscriptionExpiryService(repo, ExpiryOptions{Interval: time.Minute, Observe: log.Printf})
	svc.settingRepo = settingRepo
	svc.notificationEmailService = &subscriptionExpiryBlockingSender{readyErr: errors.New("db down")}

	svc.sendExpiryReminders(context.Background())

	require.Zero(t, repo.listCalls)
}

func (s *subscriptionExpiryBlockingSender) Ready(context.Context) error { return s.readyErr }

// 可控存储证明停止会取消在途调用并等待退出，重复启动不会创建第二轮。
type expiryLifecycleRepo struct {
	UserSubscriptionRepository
	entered      chan struct{}
	release      chan struct{}
	ignoreCancel bool
}

func (r *expiryLifecycleRepo) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	close(r.entered)
	if r.ignoreCancel {
		<-r.release
	} else {
		<-ctx.Done()
	}
	return 0, ctx.Err()
}
func TestSubscriptionExpiryLifecycle(t *testing.T) {
	repo := &expiryLifecycleRepo{entered: make(chan struct{})}
	svc := NewSubscriptionExpiryService(repo, ExpiryOptions{Interval: time.Hour})
	select {
	case <-repo.entered:
		t.Fatal("constructor started work")
	default:
	}
	svc.Start()
	svc.Start()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, svc.StopContext(ctx))
	require.NoError(t, svc.StopContext(ctx))
	svc.Start()
}
func TestSubscriptionExpiryBlockedStopReturnsBudget(t *testing.T) {
	repo := &expiryLifecycleRepo{entered: make(chan struct{}), release: make(chan struct{}), ignoreCancel: true}
	svc := NewSubscriptionExpiryService(repo, ExpiryOptions{Interval: time.Hour})
	svc.Start()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, svc.StopContext(ctx), context.DeadlineExceeded)
	close(repo.release)
	require.NoError(t, svc.StopContext(context.Background()))
}
