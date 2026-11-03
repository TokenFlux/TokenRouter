package textattempt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// helpers_test.go exercises provider admission through real HTTP timing middleware
// and scheduler waits for the text attempt adapters. Shared scenarios distinguish
// accepted capacity from failed acquisition, check that later work cannot extend
// initial admission, and retain each adapter's normal error and release behavior.

// admissionTimingAttempt exposes acquisition and its current release for shared assertions.
type admissionTimingAttempt struct {
	acquire func() bool // adapter acquisition operation
	// release points to the adapter's current cleanup callback. Acquisition can
	// replace it on retries, so tests read it after each successful call.
	release *func()
}

// testProviderAdmissionTiming checks acquisition, retry classification and failed
// admission for one adapter factory. The factory receives selected request state
// and returns the adapter's actual Acquire method without inserting timing hooks.
func testProviderAdmissionTiming(t *testing.T, path string, build func(messageAttemptBridge) admissionTimingAttempt) {
	t.Helper()
	for _, scenario := range []struct {
		name     string // acquisition scenario name
		selected bool   // scheduler already acquired capacity
		wait     bool   // selection supplies wait plan
		queued   bool   // first capacity check fails
		timeout  bool   // all capacity checks fail
		canceled bool   // request canceled during waiting
		storeErr bool   // capacity store returns error
		admitted bool   // expected acquisition success
	}{
		{name: "selected", selected: true, admitted: true},
		{name: "wait_immediate", wait: true, admitted: true},
		{name: "wait_queued", wait: true, queued: true, admitted: true},
		{name: "missing_plan"},
		{name: "wait_timeout", wait: true, timeout: true},
		{name: "wait_canceled", wait: true, canceled: true},
		{name: "wait_store_error", wait: true, storeErr: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := gin.New()
				router.Use(httpapi.TextRequestTiming())
				var stages *timing.Stages
				router.POST(path, func(c *gin.Context) {
					stages = timing.StagesFromContext(c.Request.Context())
					require.NotNil(t, stages)
					ctx, cancel := context.WithCancel(c.Request.Context())
					defer cancel()
					c.Request = c.Request.WithContext(ctx)
					cache := &testkit.ConcurrencySequence{ProviderSeq: []bool{true}}
					if scenario.queued {
						cache.ProviderSeq = []bool{false, true}
					}
					if scenario.timeout || scenario.canceled {
						cache.ProviderSeq = nil
					}
					var store scheduler.ConcurrencyCache = cache
					if scenario.storeErr {
						store = &testkit.ConcurrencySequenceWithError{Err: errors.New("capacity store unavailable")}
					}
					concurrency := scheduler.NewConcurrencyService(store)
					// Stop after adapter cleanup so no owned slot keeps the scheduler's
					// shutdown barrier waiting after this request has finished its assertions.
					defer func() { require.NoError(t, concurrency.StopContext(context.Background())) }()
					helper := httpapi.NewConcurrencyHelper(concurrency, httpapi.SSEPingFormatNone, 0)
					runtime := New(Bindings{
						Concurrency: helper,
						Selection: SelectionPorts{BindSticky: func(context.Context, *int64, string, int64) error {
							before := stages.Snapshot()
							require.Equal(t, int64(1), before["timing_admitted"])
							require.Contains(t, before, "provider_slot_acquired_ms")
							// A slow failed affinity write retains usable capacity. Its elapsed
							// time must not become part of the already completed admission.
							time.Sleep(time.Second)
							require.Equal(t, before, stages.Snapshot())
							return errors.New("sticky store unavailable")
						}},
					})
					key := &apikey.APIKey{}
					target := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 7})
					selection := &gatewayprovider.SelectionResult{Provider: target, Acquired: scenario.selected}
					var selectionReleases atomic.Int32
					if scenario.selected {
						selection.ReleaseFunc = func() { selectionReleases.Add(1) }
					}
					plan := &scheduler.ProviderWaitPlan{
						ProviderID:     7,
						MaxConcurrency: 1,
						MaxWaiting:     2,
						Timeout:        time.Second,
					}
					if scenario.wait {
						selection.WaitPlan = plan
					}
					started := false
					attempt := build(messageAttemptBridge{
						fixed:           runtime.dependencies,
						c:               c,
						apiKey:          key,
						currentAPIKey:   key,
						provider:        target,
						selection:       selection,
						reqLog:          zap.NewNop(),
						streamStarted:   &started,
						reqStream:       stages.Streaming(),
						sessionAttempts: scheduler.NewSessionAttempts(nil, scheduler.Diagnostics{}),
					})
					// Keep cleanup active even when an assertion stops the handler; its
					// mutable pointer follows the release installed by a later retry.
					defer func() {
						if *attempt.release != nil {
							(*attempt.release)()
						}
					}()
					// Advance virtual ingress work so an admission offset cannot pass
					// merely because a missing map entry also reads as numeric zero.
					time.Sleep(2 * time.Millisecond)
					if scenario.canceled {
						// Cancel while capacity remains busy so the real scheduler records
						// the interrupted queue interval without granting this request a slot.
						time.AfterFunc(10*time.Millisecond, cancel)
					}
					require.Equal(t, scenario.admitted, attempt.acquire())
					initial := stages.Snapshot()
					if !scenario.admitted {
						require.Zero(t, initial["timing_admitted"])
						require.NotContains(t, initial, "provider_slot_acquired_ms")
						if scenario.timeout || scenario.canceled {
							require.Positive(t, initial["provider_slot_wait_ms"])
						}
						return
					}
					require.Equal(t, int64(1), initial["timing_admitted"])
					require.Contains(t, initial, "provider_slot_acquired_ms")
					require.Positive(t, initial["provider_slot_acquired_ms"])
					require.Equal(t, initial["provider_slot_acquired_ms"],
						initial["request_body_read_ms"]+initial["preprocess_ms"]+
							initial["user_slot_wait_ms"]+initial["provider_slot_wait_ms"])
					if scenario.queued {
						require.Positive(t, initial["provider_slot_wait_ms"])
					} else {
						require.Zero(t, initial["provider_slot_wait_ms"])
					}
					require.Zero(t, initial["retry_provider_slot_wait_ms"])
					// Model upstream execution after acquisition. It must not inflate
					// preprocessing or move the first accepted provider's timestamp forward.
					time.Sleep(2 * time.Second)
					require.Equal(t, initial, stages.Snapshot())
					(*attempt.release)()
					(*attempt.release)()
					require.Equal(t, int32(1), selectionReleases.Load()+int32(cache.ProviderReleaseCalls))

					// Retry with another provider after releasing the first attempt.
					// Its real queue interval belongs only to retry waiting, regardless
					// of whether the initial selection acquired immediately or queued.
					target.Record.ID = 8
					selection.Acquired = false
					selection.ReleaseFunc = nil
					selection.WaitPlan = plan
					plan.ProviderID = 8
					cache.ProviderSeq = []bool{false, true}
					require.True(t, attempt.acquire())
					afterRetry := stages.Snapshot()
					for _, field := range []string{
						"provider_slot_acquired_ms",
						"request_body_read_ms",
						"preprocess_ms",
						"user_slot_wait_ms",
						"provider_slot_wait_ms",
						"timing_admitted",
					} {
						require.Equal(t, initial[field], afterRetry[field], field)
					}
					require.Positive(t, afterRetry["retry_provider_slot_wait_ms"])
					(*attempt.release)()
					(*attempt.release)()
					require.Equal(t, int32(2), selectionReleases.Load()+int32(cache.ProviderReleaseCalls))
				})
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")).WithContext(t.Context())
				router.ServeHTTP(httptest.NewRecorder(), request)
				final := stages.Snapshot()
				if scenario.admitted {
					require.Equal(t, int64(1), final["timing_admitted"])
					require.Contains(t, final, "provider_slot_acquired_ms")
				} else {
					require.Zero(t, final["timing_admitted"])
					require.NotContains(t, final, "provider_slot_acquired_ms")
				}
			})
		})
	}
}
