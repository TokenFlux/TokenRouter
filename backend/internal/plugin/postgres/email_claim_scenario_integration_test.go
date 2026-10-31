//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// email_claim_scenario_integration_test.go covers plugin/email.go, writes.go, and
// PostgreSQL ledger.go/email.go together. Real transactions verify that durable
// claims precede external delivery and remain authoritative across cancellation,
// concurrency, quota rejection, service reconstruction, and failed result persistence.

// claimEmailSender supplies controlled transport outcomes after observing committed claims.
type claimEmailSender struct {
	calls   atomic.Int64                                                           // observed transport invocation count
	deliver func(context.Context, plugin.EmailInput) (plugin.EmailDelivery, error) // configured delivery boundary behavior
}

// failedEmailResultStore rejects authoritative outcome persistence after an accepted claim.
type failedEmailResultStore struct{}

// Deliver counts calls before entering the configured test delivery boundary.
func (s *claimEmailSender) Deliver(ctx context.Context, _ plugin.Binding, input plugin.EmailInput) (plugin.EmailDelivery, error) {
	s.calls.Add(1)
	return s.deliver(ctx, input)
}

// FinalizeEmail injects a storage failure without changing the pending ledger row.
func (failedEmailResultStore) FinalizeEmail(context.Context, string, string, plugin.LedgerState, json.RawMessage) error {
	return errors.New("injected final result failure")
}

// TestEmailClaimsSerializeConcurrentDelivery persists a claim before blocking its sender and admitting thirty identical callers.
// A canceled waiter leaves the owner intact; all completed callers observe one delivery and one quota-bearing ledger row.
func TestEmailClaimsSerializeConcurrentDelivery(t *testing.T) {
	store, _, db := persistenceFixture(t)
	item := storedInstallation(t)
	require.NoError(t, store.Save(t.Context(), item))
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	sender := &claimEmailSender{deliver: func(ctx context.Context, input plugin.EmailInput) (plugin.EmailDelivery, error) {
		var state, amount string
		if err := db.QueryRowContext(ctx, `SELECT state,amount::text FROM plugin_ledger WHERE plugin_id=$1 AND idempotency_key=$2`, item.PluginID, input.Key).Scan(&state, &amount); err != nil {
			return plugin.EmailDelivery{}, err
		}
		if state != "pending" || amount != "1.00000000" {
			return plugin.EmailDelivery{}, fmt.Errorf("unexpected committed claim: %s %s", state, amount)
		}
		close(entered)
		<-release
		return plugin.EmailDelivery{Delivered: true, Attempted: true}, nil
	}}
	service := plugin.NewEmails(plugin.NewWrites(store, store, nil), store, sender, time.UTC, nil)
	binding := plugin.Binding{PluginID: item.PluginID, Permissions: []string{"notify.email"}}
	input := plugin.EmailInput{UserID: 7, Subject: "Reward", BodyText: "Hello", Key: "concurrent"}
	var completed, replayed atomic.Int64
	failures := make(chan error, 31)
	var callers sync.WaitGroup
	call := func() {
		result, err := service.Send(t.Context(), binding, input)
		if err != nil {
			failures <- err
			return
		}
		if !result.Delivered {
			failures <- errors.New("accepted delivery was lost")
			return
		}
		completed.Add(1)
		if result.Replayed {
			replayed.Add(1)
		}
	}
	callers.Go(call)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("delivery did not observe a committed pending claim")
	}
	for range 30 {
		callers.Go(call)
	}
	waiter, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := service.Send(waiter, binding, input)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int64(1), sender.calls.Load())
	releaseOnce.Do(func() { close(release) })
	callers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, int64(31), completed.Load())
	require.Equal(t, int64(30), replayed.Load())
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_ledger WHERE state='completed'").Scan(&count))
	require.Equal(t, 1, count)
	input.BodyText = "Different intent"
	_, err = service.Send(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrInvalidArgument)
	require.Equal(t, int64(1), sender.calls.Load())
}

// TestEmailTerminalOutcomesNeverResend reconstructs the coordinator after accepted, skipped, uncertain, and failed delivery outcomes.
// Every replay returns its stored decision, including a pending claim whose final persistence failed, without invoking transport again.
func TestEmailTerminalOutcomesNeverResend(t *testing.T) {
	for _, tc := range []struct {
		name           string               // named terminal outcome scenario
		delivery       plugin.EmailDelivery // configured transport acceptance observation
		deliveryError  error                // configured delivery return failure
		persistFailure bool                 // reject authoritative result persistence
		wantError      error                // expected safe capability error
		wantState      string               // expected final ledger state
	}{
		{name: "accepted", delivery: plugin.EmailDelivery{Delivered: true, Attempted: true}, wantState: "completed"},
		{
			name:          "accepted marker failed",
			delivery:      plugin.EmailDelivery{Delivered: true, Attempted: true},
			deliveryError: errors.New("marker failure"),
			wantState:     "completed",
		},
		{name: "skipped", wantState: "completed"},
		{
			name:          "SMTP uncertainty",
			delivery:      plugin.EmailDelivery{Attempted: true},
			deliveryError: errors.New("SMTP connection lost"),
			wantError:     plugin.ErrEmailDeliveryUnknown,
			wantState:     "unknown",
		},
		{name: "missing account", deliveryError: plugin.ErrHostUserNotFound, wantError: plugin.ErrHostUserNotFound, wantState: "failed"},
		{name: "pretransport failure", deliveryError: errors.New("template unavailable"), wantError: plugin.ErrHostUnavailable, wantState: "failed"},
		{
			name:           "result persistence failed",
			delivery:       plugin.EmailDelivery{Delivered: true, Attempted: true},
			persistFailure: true,
			wantError:      plugin.ErrEmailDeliveryUnknown,
			wantState:      "unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, db := persistenceFixture(t)
			item := storedInstallation(t)
			require.NoError(t, store.Save(t.Context(), item))
			sender := &claimEmailSender{deliver: func(context.Context, plugin.EmailInput) (plugin.EmailDelivery, error) {
				return tc.delivery, tc.deliveryError
			}}
			writes := plugin.NewWrites(store, store, nil)
			var results plugin.EmailResultStore = store
			if tc.persistFailure {
				results = failedEmailResultStore{}
			}
			service := plugin.NewEmails(writes, results, sender, time.UTC, nil)
			binding := plugin.Binding{PluginID: item.PluginID, Permissions: []string{"notify.email"}}
			input := plugin.EmailInput{UserID: 7, Subject: "Reward", BodyText: "Hello", Key: "outcome"}
			result, err := service.Send(t.Context(), binding, input)
			require.ErrorIs(t, err, tc.wantError)
			require.False(t, result.Replayed)
			service = plugin.NewEmails(writes, store, sender, time.UTC, nil)
			result, err = service.Send(t.Context(), binding, input)
			require.ErrorIs(t, err, tc.wantError)
			require.True(t, result.Replayed)
			require.Equal(t, tc.delivery.Delivered && !tc.persistFailure, result.Delivered)
			require.Equal(t, int64(1), sender.calls.Load())
			var state string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT state FROM plugin_ledger").Scan(&state))
			require.Equal(t, tc.wantState, state)
		})
	}
}

// TestEmailQuotasCountSkippedClaimsAndPermitReplay lowers limits after recording skipped messages for two recipients.
// Both daily dimensions reject new claims before delivery, while an existing result survives the later zero allowance.
func TestEmailQuotasCountSkippedClaimsAndPermitReplay(t *testing.T) {
	store, _, db := persistenceFixture(t)
	item := storedInstallation(t)
	item.Limits = json.RawMessage(`{"email":{"max_per_user_per_day":1,"max_per_day":2}}`)
	require.NoError(t, store.Save(t.Context(), item))
	sender := &claimEmailSender{deliver: func(context.Context, plugin.EmailInput) (plugin.EmailDelivery, error) {
		return plugin.EmailDelivery{}, nil
	}}
	service := plugin.NewEmails(plugin.NewWrites(store, store, nil), store, sender, time.UTC, nil)
	binding := plugin.Binding{PluginID: item.PluginID, Permissions: []string{"notify.email"}}
	input := plugin.EmailInput{UserID: 7, Subject: "Reward", BodyText: "Hello", Key: "first"}
	_, err := service.Send(t.Context(), binding, input)
	require.NoError(t, err)
	input.Key = "user-overflow"
	_, err = service.Send(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrLimitExceeded)
	input.UserID = 8
	input.Key = "second"
	_, err = service.Send(t.Context(), binding, input)
	require.NoError(t, err)
	input.UserID = 9
	input.Key = "global-overflow"
	_, err = service.Send(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrLimitExceeded)
	item.Limits = json.RawMessage(`{"email":{"max_per_user_per_day":0,"max_per_day":0}}`)
	require.NoError(t, store.Save(t.Context(), item))
	input.UserID = 7
	input.Key = "first"
	result, err := service.Send(t.Context(), binding, input)
	require.NoError(t, err)
	require.True(t, result.Replayed)
	require.False(t, result.Delivered)
	require.Equal(t, int64(2), sender.calls.Load())
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_ledger").Scan(&count))
	require.Equal(t, 2, count)
}
