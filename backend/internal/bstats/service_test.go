package bstats

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// service_test.go checks identity persistence and reporting lifetime with virtual
// time and an in-memory settings repository. Reporter callbacks replace network
// I/O, allowing complete official reporting intervals and cancellation races to
// be exercised without sending synthetic observations to the public service.

// memoryRepository provides the exact settings operations used by reporting.
type memoryRepository struct {
	settings.Repository                   // unused repository operations
	values              map[string]string // persisted fixture values
	readError           error             // injected read failure
	writeError          error             // injected write failure
	writes              int               // identity write attempts
}

// reporterFunc adapts a test callback to the reporting transport contract.
type reporterFunc func(context.Context, string, Config) error

// delayedRepository blocks one selected operation while preserving ordinary fixture storage.
type delayedRepository struct {
	*memoryRepository        // ordinary fixture persistence
	operation         string // selected delayed operation
}

// GetMultiple returns a copy or the fixture's configured read error.
func (r *memoryRepository) GetMultiple(context.Context, []string) (map[string]string, error) {
	return maps.Clone(r.values), r.readError
}

// GetValue distinguishes an absent identifier from a storage read failure.
func (r *memoryRepository) GetValue(_ context.Context, key string) (string, error) {
	if r.readError != nil {
		return "", r.readError
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", settings.ErrSettingNotFound
}

// Set records successful identity writes and preserves state on failure.
func (r *memoryRepository) Set(_ context.Context, key, value string) error {
	r.writes++
	if r.writeError != nil {
		return r.writeError
	}
	r.values[key] = value
	return nil
}

// Submit invokes the fixture callback with the actual reporting context.
func (f reporterFunc) Submit(ctx context.Context, id string, config Config) error {
	return f(ctx, id, config)
}

// newTestService supplies deterministic delays while retaining production worker coordination.
// Call it inside a synctest bubble, and stop the returned service before that
// bubble ends so all timer and submission goroutines can complete.
func newTestService(repo *memoryRepository, reporter reporterFunc) *Service {
	service := New(settings.New(repo), reporter, nil)
	service.delays = func() (time.Duration, time.Duration) { return 3 * time.Minute, 12 * time.Minute }
	return service
}

// TestServiceScheduleAndLoggingUpdate advances through both initial deadlines and
// the fixed-rate series. Logging publication and submission failures must leave
// those deadlines unchanged, and disabling must suppress all later submissions.
func TestServiceScheduleAndLoggingUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &memoryRepository{values: map[string]string{}}
		start := time.Now()
		var times []time.Duration
		var configs []Config
		service := newTestService(repo, func(_ context.Context, id string, config Config) error {
			require.Equal(t, repo.values[SettingKeyServerUUID], id)
			times = append(times, time.Since(start))
			configs = append(configs, config)
			return errors.New("offline")
		})
		require.NoError(t, service.Start(context.Background()))
		time.Sleep(3*time.Minute - time.Millisecond)
		synctest.Wait()
		require.Empty(t, times)
		time.Sleep(time.Millisecond)
		synctest.Wait()
		require.Equal(t, []time.Duration{3 * time.Minute}, times)
		require.NoError(t, service.Apply(context.Background(), Config{Enabled: true, LogSentData: true}))
		time.Sleep(12 * time.Minute)
		synctest.Wait()
		time.Sleep(30 * time.Minute)
		synctest.Wait()
		require.Equal(t, []time.Duration{3 * time.Minute, 15 * time.Minute, 45 * time.Minute}, times)
		require.False(t, configs[0].LogSentData)
		require.True(t, configs[1].LogSentData)
		require.NoError(t, service.Apply(context.Background(), Config{}))
		time.Sleep(time.Hour)
		synctest.Wait()
		require.Len(t, times, 3)
		require.NoError(t, service.Apply(context.Background(), Config{Enabled: true}))
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		require.Equal(t, 108*time.Minute, times[3])
		require.Equal(t, 1, repo.writes)
		require.NoError(t, service.Stop(context.Background()))
	})
}

// TestServiceCancellationAndRepeatedToggle holds an in-flight report until cancellation.
// A completed disable joins it, and rapid changes before the first deadline
// must leave only the final enabled generation able to submit.
func TestServiceCancellationAndRepeatedToggle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &memoryRepository{values: map[string]string{}}
		calls := 0
		completed := 0
		service := newTestService(repo, func(ctx context.Context, _ string, _ Config) error {
			calls++
			<-ctx.Done()
			completed++
			return ctx.Err()
		})
		require.NoError(t, service.Start(context.Background()))
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		require.Equal(t, 1, calls)
		require.NoError(t, service.Apply(context.Background(), Config{}))
		require.Equal(t, 1, completed)
		for range 20 {
			require.NoError(t, service.Apply(context.Background(), Config{Enabled: true}))
			require.NoError(t, service.Apply(context.Background(), Config{}))
		}
		require.NoError(t, service.Apply(context.Background(), Config{Enabled: true}))
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		require.Equal(t, 2, calls)
		require.NoError(t, service.Stop(context.Background()))
		require.Equal(t, 2, completed)
	})
}

// TestServiceIdentityAndStorageFailures recreates services against one settings
// repository and injects read or write failures. Only durable identities may
// reach the reporter, while later publication can recover paused initialization.
func TestServiceIdentityAndStorageFailures(t *testing.T) {
	for _, failure := range []string{"none", "read", "write", "invalid"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repo := &memoryRepository{values: map[string]string{}}
				if failure == "read" {
					repo.readError = errors.New("read failure")
				}
				if failure == "write" {
					repo.writeError = errors.New("write failure")
				}
				if failure == "invalid" {
					repo.values[SettingKeyServerUUID] = "broken"
				}
				var ids []string
				reporter := reporterFunc(func(_ context.Context, id string, _ Config) error {
					ids = append(ids, id)
					return nil
				})
				service := newTestService(repo, reporter)
				err := service.Start(context.Background())
				if failure != "none" {
					require.Error(t, err)
					time.Sleep(time.Hour)
					synctest.Wait()
					require.Empty(t, ids)
					repo.readError = nil
					repo.writeError = nil
					delete(repo.values, SettingKeyServerUUID)
					require.NoError(t, service.Apply(context.Background(), Config{Enabled: true}))
				} else {
					require.NoError(t, err)
				}
				time.Sleep(3 * time.Minute)
				synctest.Wait()
				require.Len(t, ids, 1)
				parsed, err := uuid.Parse(ids[0])
				require.NoError(t, err)
				require.Equal(t, uuid.Version(4), parsed.Version())
				require.NoError(t, service.Stop(context.Background()))
				service = newTestService(repo, reporter)
				require.NoError(t, service.Start(context.Background()))
				time.Sleep(3 * time.Minute)
				synctest.Wait()
				require.Equal(t, []string{ids[0], ids[0]}, ids)
				require.NoError(t, service.Stop(context.Background()))
			})
		})
	}
}

// TestServiceDisabledStartupAndInterruptedStop starts with reporting disabled,
// then interrupts a stop while a transport is still unwinding. Restart must
// join that cancelled generation before admitting the next scheduled worker.
func TestServiceDisabledStartupAndInterruptedStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &memoryRepository{values: map[string]string{SettingKeyEnabled: "false"}}
		release := make(chan struct{})
		calls := 0
		service := newTestService(repo, func(_ context.Context, _ string, _ Config) error {
			calls++
			<-release
			return nil
		})
		require.NoError(t, service.Start(context.Background()))
		time.Sleep(time.Hour)
		synctest.Wait()
		require.Zero(t, calls)
		require.Zero(t, repo.writes)
		require.NoError(t, service.Apply(context.Background(), Config{Enabled: true}))
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, service.Stop(ctx), context.Canceled)
		close(release)
		repo.values[SettingKeyEnabled] = "true"
		require.NoError(t, service.Start(context.Background()))
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		require.Equal(t, 2, calls)
		require.NoError(t, service.Stop(context.Background()))
	})
}

// TestSubmissionDelays verifies the upstream half-open ranges and millisecond
// precision. Random samples must never introduce a zero initial delay or
// fractional milliseconds into the schedule derived from Java's long conversion.
func TestSubmissionDelays(t *testing.T) {
	for range 100 {
		initial, second := submissionDelays()
		require.GreaterOrEqual(t, initial, 3*time.Minute)
		require.Less(t, initial, 6*time.Minute)
		require.GreaterOrEqual(t, second, time.Duration(0))
		require.Less(t, second, 30*time.Minute)
		require.Zero(t, initial%time.Millisecond)
		require.Zero(t, second%time.Millisecond)
	}
}

// SetMultiple applies complete fixture batches only when persistence succeeds.
func (r *memoryRepository) SetMultiple(_ context.Context, values map[string]string) error {
	if r.writeError != nil {
		return r.writeError
	}
	maps.Copy(r.values, values)
	return nil
}

// TestServicePublicationFollowsCommit rejects a settings batch before accepting
// the same enable request. Failed persistence must leave reporting disabled,
// whereas a committed update starts the normal delayed reporting sequence.
func TestServicePublicationFollowsCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &memoryRepository{values: map[string]string{SettingKeyEnabled: "false"}}
		calls := 0
		service := newTestService(repo, func(context.Context, string, Config) error {
			calls++
			return nil
		})
		require.NoError(t, service.Start(context.Background()))
		for _, fail := range []bool{true, false} {
			repo.writeError = nil
			if fail {
				repo.writeError = errors.New("persistence failed")
			}
			session, err := service.store.Updates().Begin(context.Background())
			require.NoError(t, err)
			err = session.Commit(settings.PreparedChange{Module: "bstats", Values: map[string]string{SettingKeyEnabled: "true"}, Apply: func(ctx context.Context) error {
				return service.Apply(ctx, Config{Enabled: true})
			}})
			session.Close()
			if fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			time.Sleep(3 * time.Minute)
			synctest.Wait()
			if fail {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		}
		require.NoError(t, service.Stop(context.Background()))
	})
}

// wait delays the selected storage operation until cancellation or a simulated
// twenty-second outage ends. The finite fallback lets the regression fail on
// missing deadlines without leaving the virtual-time test permanently blocked.
func (r *delayedRepository) wait(ctx context.Context, operation string) error {
	if r.operation != operation {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Second):
		return errors.New("storage remained blocked")
	}
}

// GetMultiple delays configuration loading when selected, otherwise returning
// the stored values. Cancellation during the selected delay returns the context
// error without invoking the ordinary repository read.
func (r *delayedRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if err := r.wait(ctx, "settings"); err != nil {
		return nil, err
	}
	return r.memoryRepository.GetMultiple(ctx, keys)
}

// GetValue delays identity lookup when selected, otherwise returning the stored
// identifier. Cancellation during the selected delay returns the context error
// without looking up or replacing the existing value.
func (r *delayedRepository) GetValue(ctx context.Context, key string) (string, error) {
	if err := r.wait(ctx, "identity-read"); err != nil {
		return "", err
	}
	return r.memoryRepository.GetValue(ctx, key)
}

// Set delays identity persistence when selected, otherwise writing through to
// fixture storage. Cancellation during the selected delay returns the context
// error without writing the candidate identifier into the repository.
func (r *delayedRepository) Set(ctx context.Context, key, value string) error {
	if err := r.wait(ctx, "identity-write"); err != nil {
		return err
	}
	return r.memoryRepository.Set(ctx, key, value)
}

// TestServiceInitializationDeadline bounds stalled settings and identity operations.
// Optional reporting must stop waiting within five seconds during startup or
// runtime enablement, and later publication must recover using a durable identity.
func TestServiceInitializationDeadline(t *testing.T) {
	for _, operation := range []string{"settings", "identity-read", "identity-write"} {
		for _, startup := range []bool{true, false} {
			if !startup && operation == "settings" {
				continue
			}
			t.Run(fmt.Sprintf("%s/startup=%t", operation, startup), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					repo := &delayedRepository{
						memoryRepository: &memoryRepository{values: map[string]string{}},
						operation:        operation,
					}
					calls := 0
					reporter := reporterFunc(func(context.Context, string, Config) error {
						calls++
						return nil
					})
					service := New(settings.New(repo), reporter, nil)
					service.delays = func() (time.Duration, time.Duration) {
						return 3 * time.Minute, 12 * time.Minute
					}
					if !startup {
						repo.values[SettingKeyEnabled] = "false"
						require.NoError(t, service.Start(context.Background()))
					}
					started := time.Now()
					var err error
					if startup {
						err = service.Start(context.Background())
					} else {
						err = service.Apply(context.Background(), Config{Enabled: true})
					}
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.Equal(t, 5*time.Second, time.Since(started))
					require.Zero(t, calls)
					require.Empty(t, repo.values[SettingKeyServerUUID])
					repo.operation = ""
					require.NoError(t, service.Apply(context.Background(), Config{Enabled: true}))
					time.Sleep(3 * time.Minute)
					synctest.Wait()
					require.Equal(t, 1, calls)
					require.NotEmpty(t, repo.values[SettingKeyServerUUID])
					require.NoError(t, service.Stop(context.Background()))
				})
			})
		}
	}
}
