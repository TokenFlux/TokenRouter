package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingstestkit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const (
	// openAI403HTMLBody represents HTML forbidden output for endpoint-level
	// exemption tests, including the OpenAI and Kimi, Zhipu, and DeepSeek
	// provider paths exercised below.
	openAI403HTMLBody = "<!DOCTYPE html>\n<html><head><title>403 Forbidden</title></head>" +
		"<body><h1>403 Forbidden</h1></body></html>"

	// teamLinkedDeactivatedBody identifies workspace deactivation for same-team
	// fanout assertions, contrasting it with ordinary payment-required response text.
	teamLinkedDeactivatedBody = `{"detail":{"code":"deactivated_workspace","message":"This workspace has been deactivated."}}`

	// These Grok fixture constants name the saved quota observation and expected
	// probe, repeat, sustained, maximum, quiet-period, and spending-limit cooldowns.
	grokQuotaSnapshotExtraKey        = "grok_usage_snapshot"
	grokRateLimitFallbackCooldown    = 2 * time.Minute
	grokRateLimitRepeatCooldown      = 10 * time.Minute
	grokRateLimitSustainedCooldown   = 30 * time.Minute
	grokRateLimitMaxAdaptiveCooldown = time.Hour
	grokRateLimitBackoffQuietPeriod  = time.Hour
	grokSpendingLimitProbeCooldown   = 10 * time.Minute
)

// dbFallbackRepoStub supplies an optional matching provider record for health
// cache-miss tests.
type dbFallbackRepoStub struct {
	gatewaytestkit.ErrorPolicyStore // unused error policy storage methods

	// optional matching database provider
	dbProvider *gatewayprovider.ExecutionProvider
}

// countingOpenAI403CounterCache counts delegated forbidden-counter updates for
// provider penalty assertions.
type countingOpenAI403CounterCache struct {
	// configured forbidden counter responses
	gatewaytestkit.ForbiddenCounter

	increments int // delegated forbidden counter calls
}

// openAI403TestHarness combines provider, health, persistence, counter, and
// runtime-block fixture observations.
type openAI403TestHarness struct {
	svc *provideradapter.UpstreamHealth // bound fixture health observer

	// provider penalty write recorder
	repo *gatewaytestkit.HealthStoreRecorder
	// forbidden counter invocation spy
	counter *countingOpenAI403CounterCache
	// runtime scheduling block recorder
	blocker *gatewaytestkit.RuntimeBlockRecorder
	// current health observation target
	provider *gatewayprovider.ExecutionProvider
}

// anthropicWindowLimitRepo records provider, model, and session-window writes
// during Anthropic health tests.
type anthropicWindowLimitRepo struct {
	// unused health storage methods
	gatewaytestkit.HealthStoreBase
	// global rate-limit write count
	rateLimitCalls          int
	tempUnschedCalls        int       // temporary pause write count
	lastRateLimitReset      time.Time // last global reset deadline
	modelRateLimitCalls     int       // model scope write count
	lastModelRateLimitScope string    // last model limit scope
	lastModelRateLimitReset time.Time // last model reset deadline
	sessionWindowCalls      int       // session window write count
	// lastWindowStart and lastWindowEnd retain UpdateSessionWindow pointers.
	// Tests leave those timestamp values stable through assertions; the recorder
	// captures status and invocation count alongside this same window observation.
	lastWindowStart  *time.Time // borrowed session start observation
	lastWindowEnd    *time.Time // borrowed session end observation
	lastWindowStatus string     // last session status observation
	// lastExtraUpdates retains the caller map by reference for later assertions;
	// test code keeps its entries stable after UpdateExtra records the observation.
	lastExtraUpdates map[string]any // borrowed passive observation updates
}

// teamLinkedProviderRepoStub records filtered team lookup and selectively
// failing error writes.
type teamLinkedProviderRepoStub struct {
	// unused health storage methods
	gatewaytestkit.HealthStoreBase
	// configured team candidate records
	teamProviders []gatewayprovider.ExecutionProvider
	// configured candidate lookup error
	listErr error
	// observed team lookup count
	listCalls int
	// successfully written provider IDs
	setErrorIDs []int64
	// successful error messages by ID
	setErrorMsgs map[int64]string
	// configured per-provider write errors
	failSetError map[int64]error
}

// fableSchedulingThresholdRepoStub records Fable model cooldown metadata
// alongside ordinary health writes.
type fableSchedulingThresholdRepoStub struct {
	gatewaytestkit.HealthStoreRecorder // ordinary provider write recorder

	modelCalls      int       // Fable scope write count
	lastModelScope  string    // last Fable model scope
	lastModelReset  time.Time // last Fable reset deadline
	lastModelReason string    // first supplied Fable reason
}

// grokQuotaProviderRepo records Grok quota updates, cooldown writes, and
// conditional recovery observations.
type grokQuotaProviderRepo struct {
	// unused health storage methods
	gatewaytestkit.HealthStoreBase
	// providersByID and updates retain borrowed test targets and supplied update
	// maps. UpdateExtra records each map and merges its keys into a found target;
	// tests serialize their observation and assertion phases while those values
	// remain shared.
	//
	// borrowed targets by provider ID
	providersByID map[int64]*gatewayprovider.ExecutionProvider
	// borrowed update maps by ID
	updates map[int64]map[string]any
	// quota snapshot update count
	updateCalls int
	// global rate-limit write count
	rateLimitedCalls int
	// last rate-limited provider ID
	lastRateLimitedID int64
	// last global reset deadline
	lastRateLimitResetAt time.Time
	// temporary pause write count
	tempUnschedCalls int
	// last temporarily paused provider ID
	lastTempUnschedID int64
	// last temporary pause deadline
	lastTempUnschedUntil time.Time
	// last temporary pause reason
	lastTempUnschedReason string
	// conditional cooldown clear calls
	recoveryClearCalls int
	// supplied observed limit timestamp
	recoveryObservedAt time.Time
	// supplied observed reset timestamp
	recoveryObservedReset time.Time
	// configured recovery match result
	recoveryClearResult bool
}

// grokPoolPolicyProviderRepo extends quota recording with explicit pool policy
// write observations.
type grokPoolPolicyProviderRepo struct {
	*grokQuotaProviderRepo          // embedded quota write recorder
	setErrorCalls            int    // explicit pool error writes
	overloadedCalls          int    // explicit pool overload writes
	modelRateLimitCalls      int    // explicit model scope writes
	lastModelRateLimitScope  string // last model cooldown scope
	lastModelRateLimitReason string // first supplied model reason
}

// grokHealthTestClock supplies an atomic clock override for runtime block
// tests.
type grokHealthTestClock struct {
	nanos atomic.Int64 /* zero selects wall time */
}

// TestHandle403_OtherPlatformWithKimiConcurrencyMessageUsesNormalPolicy
// supplies the Kimi concurrency phrase on a Zhipu API Key target. Normal 403
// handling consumes the permanent counter and writes an error, with no Kimi
// temporary pause.
func TestHandle403_OtherPlatformWithKimiConcurrencyMessageUsesNormalPolicy(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{providercore.OpenAI403DisableThresholdDefault}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{ForbiddenCounter: counter, Block: func(v *providercore.Record, until time.Time, reason string) {
		blocker.BlockProviderScheduling(gatewayprovider.NewExecutionProvider(v), until, reason)
	}}, nil)

	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 405, Platform: capability.PlatformZhipu, Type: capability.ProviderTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls, "Zhipu must use the normal permanent-error policy")
	require.Equal(t, 0, repo.TempCalls)
	require.Empty(t, counter.Counts, "Zhipu forbidden handling must consume the counter result")
	require.Equal(t, []string{"auth_error"}, blocker.Reasons, "the Kimi-specific runtime block must not apply")
}

// TestHandle403_KimiConcurrencyLimitAlwaysUsesTemporaryCooldown supplies the
// exact concurrency-limit phrase on Kimi. Health records a temporary pause and
// runtime block while leaving the permanent-error counter unused.
func TestHandle403_KimiConcurrencyLimitAlwaysUsesTemporaryCooldown(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{providercore.OpenAI403DisableThresholdDefault}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{ForbiddenCounter: counter, Block: func(v *providercore.Record, until time.Time, reason string) {
		blocker.BlockProviderScheduling(gatewayprovider.NewExecutionProvider(v), until, reason)
	}}, nil)

	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 403, Platform: capability.PlatformKimi, Type: capability.ProviderTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable, "the request must still fail over to another provider")
	require.Equal(t, 0, repo.SetErrorCalls)
	require.Equal(t, 1, repo.TempCalls)
	require.Contains(t, repo.LastTempReason, providercore.KimiConcurrencyLimitReason)
	require.Equal(t, []int64{providercore.OpenAI403DisableThresholdDefault}, counter.Counts, "transient concurrency 403 must bypass the permanent-error counter")
	require.Len(t, blocker.Providers, 1)
	require.Equal(t, providercore.KimiConcurrencyLimitReason, blocker.Reasons[0])
	require.True(t, blocker.Until[0].After(time.Now()))
}

// TestHandle403_KimiConcurrencyLimitRepositoryFailureKeepsRuntimeBlock makes
// temporary pause storage fail for the Kimi concurrency rejection. The runtime
// block and failover decision remain, with no permanent-error write or counter
// consumption.
func TestHandle403_KimiConcurrencyLimitRepositoryFailureKeepsRuntimeBlock(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{TempErr: errors.New("repository unavailable")}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{providercore.OpenAI403DisableThresholdDefault}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{ForbiddenCounter: counter, Block: func(v *providercore.Record, until time.Time, reason string) {
		blocker.BlockProviderScheduling(gatewayprovider.NewExecutionProvider(v), until, reason)
	}}, nil)

	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 406, Platform: capability.PlatformKimi, Type: capability.ProviderTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again."}}`), nil)).StopScheduling

	require.True(t, shouldDisable, "the current request must fail over even when persistence fails")
	require.Equal(t, 1, repo.TempCalls, "the temporary cooldown should still be persisted when possible")
	require.Equal(t, 0, repo.SetErrorCalls, "persistence failure must not fall back to permanent provider error")
	require.Equal(t, []int64{providercore.OpenAI403DisableThresholdDefault}, counter.Counts, "persistence failure must not enter the permanent-error counter path")
	require.Len(t, blocker.Providers, 1, "the in-memory runtime block must survive repository failure")
	// Compare business fields and routes after excluding clock functions from the assertion.
	expectedProvider, observedProvider := *provider, *blocker.Providers[0]
	expectedProvider.Record.Now, observedProvider.Record.Now = nil, nil
	expectedProvider.Record.LoadLocation, observedProvider.Record.LoadLocation = nil, nil
	require.Equal(t, expectedProvider, observedProvider)
	require.Equal(t, providercore.KimiConcurrencyLimitReason, blocker.Reasons[0])
	require.True(t, blocker.Until[0].After(time.Now()))
}

// TestHandle403_KimiNearMatchRetainsNormalPermanentErrorPolicy changes
// the ending of the Kimi concurrency message so it is a near match. Health
// retains the permanent-error write and produces no temporary pause.
func TestHandle403_KimiNearMatchRetainsNormalPermanentErrorPolicy(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{providercore.OpenAI403DisableThresholdDefault}}
	service := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{ForbiddenCounter: counter}, nil)

	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 404, Platform: capability.PlatformKimi, Type: capability.ProviderTypeAPIKey}}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"You've reached your concurrent request limit. Please contact support."}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls, "non-exact 403 must retain existing permission/auth protection")
	require.Equal(t, 0, repo.TempCalls)
}

// GetByID returns the configured provider when its ID matches the lookup.
// Absent or mismatched fixtures return nil with no error, so these tests
// distinguish a missing database record from a read failure.
func (r *dbFallbackRepoStub) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	if r.dbProvider != nil && r.dbProvider.Record.ID == id {
		return r.dbProvider, nil
	}
	return nil, nil // not found, no error
}

// TestCheckErrorPolicy_401_DBFallback_Escalates supplies a cached record with
// no prior reason and a database record showing 401. Gemini returns the
// escalation policy result, while Antigravity returns temporary-unscheduling
// policy.
func TestCheckErrorPolicy_401_DBFallback_Escalates(t *testing.T) {
	// The stored record supplies an earlier 401 reason missing from the captured
	// target. Returned policy distinguishes Gemini's escalation path from
	// Antigravity's temporary rule; these assertions do not inspect a state write.
	t.Run("gemini_escalates", func(t *testing.T) {
		repo := &dbFallbackRepoStub{
			dbProvider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 20,
					TempUnschedulableReason: `{"status_code":401,"until_unix":1735689600}`,
				},
			},
		}
		svc := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{}, nil)

		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 20,
				Type:                    capability.ProviderTypeOAuth,
				Platform:                capability.PlatformGemini,
				TempUnschedulableReason: "",
				Credentials: map[string]any{
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(401),
							"keywords":         []any{"unauthorized"},
							"duration_minutes": float64(10),
						},
					},
				},
			},
		}

		result := svc.CheckErrorPolicy(context.Background(), gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusUnauthorized, nil, []byte(`unauthorized`), nil))
		require.Equal(t, providercore.ErrorPolicyNone, result, "gemini 401 with DB fallback showing previous 401 should escalate")
	})

	t.Run("antigravity_stays_temp", func(t *testing.T) {
		repo := &dbFallbackRepoStub{
			dbProvider: &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 20,
					TempUnschedulableReason: `{"status_code":401,"until_unix":1735689600}`,
				},
			},
		}
		svc := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{}, nil)

		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 20,
				Type:                    capability.ProviderTypeOAuth,
				Platform:                capability.PlatformAntigravity,
				TempUnschedulableReason: "",
				Credentials: map[string]any{
					"temp_unschedulable_enabled": true,
					"temp_unschedulable_rules": []any{
						map[string]any{
							"error_code":       float64(401),
							"keywords":         []any{"unauthorized"},
							"duration_minutes": float64(10),
						},
					},
				},
			},
		}

		result := svc.CheckErrorPolicy(context.Background(), gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusUnauthorized, nil, []byte(`unauthorized`), nil))
		require.Equal(t, providercore.ErrorPolicyTempUnscheduled, result, "antigravity 401 skips escalation, stays temp-unscheduled")
	})
}

// TestCheckErrorPolicy_401_DBFallback_NoDBRecord_FirstHit uses Antigravity
// with an empty prior reason in both captured and stored records. The first
// matching 401 rule returns temporary unscheduling.
func TestCheckErrorPolicy_401_DBFallback_NoDBRecord_FirstHit(t *testing.T) {
	// With no earlier 401 in either source, the first hit selects temporary policy.
	repo := &dbFallbackRepoStub{
		dbProvider: &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 21,
				TempUnschedulableReason: "",
			}, // DB also empty
		},
	}
	svc := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 21,
			Type:                    capability.ProviderTypeOAuth,
			Platform:                capability.PlatformAntigravity,
			TempUnschedulableReason: "",
			Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(401),
						"keywords":         []any{"unauthorized"},
						"duration_minutes": float64(10),
					},
				},
			},
		},
	}

	result := svc.CheckErrorPolicy(context.Background(), gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusUnauthorized, nil, []byte(`unauthorized`), nil))
	require.Equal(t, providercore.ErrorPolicyTempUnscheduled, result, "401 first hit with no DB record should temp-unschedule")
}

// TestCheckErrorPolicy_401_DBFallback_DBError_FirstHit uses an Antigravity
// lookup fixture returning nil with no error. The missing record is handled as
// a first matching 401, producing temporary unscheduling.
func TestCheckErrorPolicy_401_DBFallback_DBError_FirstHit(t *testing.T) {
	// A missing cached reason and absent database record are treated as the first hit.
	repo := &dbFallbackRepoStub{
		dbProvider: nil, // GetByID returns nil, nil
	}
	svc := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 22,
			Type:                    capability.ProviderTypeOAuth,
			Platform:                capability.PlatformAntigravity,
			TempUnschedulableReason: "",
			Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(401),
						"keywords":         []any{"unauthorized"},
						"duration_minutes": float64(10),
					},
				},
			},
		},
	}

	result := svc.CheckErrorPolicy(context.Background(), gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusUnauthorized, nil, []byte(`unauthorized`), nil))
	require.Equal(t, providercore.ErrorPolicyTempUnscheduled, result, "401 first hit with DB not found should temp-unschedule")
}

// TestRateLimitService_HandleUpstreamError_OpenAIOAuth403UsesTempUnschedulable
// configures seven-minute OpenAI OAuth 403 cooldown and a first counter value.
// Health writes a temporary pause with counter text and the measured deadline,
// avoiding permanent error.
func TestRateLimitService_HandleUpstreamError_OpenAIOAuth403UsesTempUnschedulable(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{1}}
	settingRepo := settingstestkit.NewMemory()
	data, err := json.Marshal(providercore.OpenAI403CooldownSettings{
		Enabled:                 true,
		CooldownMinutes:         7,
		ErrorOnThresholdEnabled: true,
		ThresholdCount:          providercore.OpenAI403DisableThresholdDefault,
		ThresholdWindowMinutes:  providercore.OpenAI403CounterWindowMinutesDefault,
	})
	require.NoError(t, err)
	settingRepo.Data[providercore.SettingKeyOpenAI403CooldownSettings] = string(data)
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{ForbiddenCounter: counter},

		newExecutionReadersFixture(settingRepo, &config.Config{}))
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 104,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		},
	}

	before := time.Now()
	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.SetErrorCalls)
	require.Equal(t, 1, repo.TempCalls)
	require.WithinDuration(t, before.Add(7*time.Minute), repo.LastTempUntil, 2*time.Second)
	require.Contains(t, repo.LastTempReason, "temporary forbidden")
	require.Contains(t, repo.LastTempReason, "(1/3)")
}

// TestRateLimitService_HandleUpstreamError_OpenAIOAuth403DisabledUsesSetError
// disables the OpenAI OAuth temporary 403 cooldown. A forbidden response
// writes one provider error containing the upstream message and records no
// temporary pause.
func TestRateLimitService_HandleUpstreamError_OpenAIOAuth403DisabledUsesSetError(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{1}}
	settingRepo := settingstestkit.NewMemory()
	data, err := json.Marshal(providercore.OpenAI403CooldownSettings{Enabled: false, CooldownMinutes: 7})
	require.NoError(t, err)
	settingRepo.Data[providercore.SettingKeyOpenAI403CooldownSettings] = string(data)
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{ForbiddenCounter: counter},

		newExecutionReadersFixture(settingRepo, &config.Config{}))
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 105,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls)
	require.Equal(t, 0, repo.TempCalls)
	require.Contains(t, repo.LastErrorMsg, "temporary forbidden")
}

// TestRateLimitService_HandleUpstreamError_OpenAIOAuth403WithoutCounterUsesSetError
// omits the 403 counter dependency for an OAuth target. Forbidden handling
// records one permanent provider error and no temporary pause.
func TestRateLimitService_HandleUpstreamError_OpenAIOAuth403WithoutCounterUsesSetError(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 106,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls)
	require.Equal(t, 0, repo.TempCalls)
	require.Contains(t, repo.LastErrorMsg, "temporary forbidden")
}

// TestRateLimitService_HandleUpstreamError_NonOpenAIOAuth403UsesSetError
// supplies an Anthropic OAuth target with a forbidden response. Ordinary
// platform handling records a permanent error containing the 403 explanation.
func TestRateLimitService_HandleUpstreamError_NonOpenAIOAuth403UsesSetError(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 107,
			Platform: capability.PlatformAnthropic,
			Type:     capability.ProviderTypeOAuth,
		},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"forbidden"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls)
	require.Equal(t, 0, repo.TempCalls)
	require.Contains(t, repo.LastErrorMsg, "Access forbidden (403)")
}

// IncrementOpenAI403Count increments the spy count before delegating to the
// embedded counter fixture.
func (s *countingOpenAI403CounterCache) IncrementOpenAI403Count(ctx context.Context, providerID int64, window int) (int64, error) {
	s.increments++
	return s.ForbiddenCounter.IncrementOpenAI403Count(ctx, providerID, window)
}

// newOpenAI403TestHarness constructs OAuth 403 handling with supplied counter
// results and a runtime-block recorder. Returned persistence and counter spies
// expose which penalty path was invoked for each body.
func newOpenAI403TestHarness(t *testing.T, providerID int64, counts ...int64) *openAI403TestHarness {
	t.Helper()
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &countingOpenAI403CounterCache{ForbiddenCounter: gatewaytestkit.ForbiddenCounter{Counts: counts}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	svc := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{ForbiddenCounter: counter, Block: func(v *providercore.Record, until time.Time, reason string) {
		blocker.BlockProviderScheduling(gatewayprovider.NewExecutionProvider(v), until, reason)
	}}, nil)

	return &openAI403TestHarness{
		svc:      svc,
		repo:     repo,
		counter:  counter,
		blocker:  blocker,
		provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: providerID, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}},
	}
}

// handle observes an HTTP 403 body on the harness provider and returns
// StopScheduling.
func (h *openAI403TestHarness) handle(body string) bool {
	return gatewayprovider.ApplyExecutionHealth(context.Background(), h.svc, h.provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(body), nil)).StopScheduling
}

// requireNoProviderPenalty checks that error writes, temporary pause, runtime
// blocks, and counter increments stay unused. It marks itself as a helper so
// failures point to the calling scenario.
func (h *openAI403TestHarness) requireNoProviderPenalty(t *testing.T) {
	t.Helper()
	require.Equal(t, 0, h.repo.SetErrorCalls, "端点级 403 不得永久禁用提供商")
	require.Equal(t, 0, h.repo.TempCalls, "端点级 403 不得把提供商设为临时不可调度")
	require.Empty(t, h.blocker.Providers, "端点级 403 不得触发调度阻断通知")
	require.Equal(t, 0, h.counter.increments, "端点级 403 不得递增连续 403 计数")
}

// TestHandleUpstreamError_OpenAIHTML403DoesNotPenalizeProvider supplies
// doctype, bare HTML, and padded uppercase HTML 403 bodies. Each leaves
// permanent errors, temporary pauses, runtime blocking, and counter increments
// untouched.
func TestHandleUpstreamError_OpenAIHTML403DoesNotPenalizeProvider(t *testing.T) {
	cases := []struct {
		name string // HTML response scenario label
		body string // upstream forbidden response body
	}{
		{"doctype_prefixed", openAI403HTMLBody},
		{"bare_html_tag", "<html><body>403 Forbidden</body></html>"},
		{"leading_whitespace_and_uppercase", "\n\t  <!DOCTYPE HTML><html><body>Forbidden</body></html>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 501, 1)

			shouldDisable := h.handle(tc.body)

			require.False(t, shouldDisable, "HTML 403 不得判定提供商应下线")
			h.requireNoProviderPenalty(t)
		})
	}
}

// TestHandleUpstreamErrorKimiZhipuDeepSeekHTML403DoesNotPenalizeProvider
// supplies the HTML 403 fixture to Kimi, Zhipu, and DeepSeek API Key targets.
// Those compatible paths avoid provider penalties and counter increments.
func TestHandleUpstreamErrorKimiZhipuDeepSeekHTML403DoesNotPenalizeProvider(t *testing.T) {
	for _, platform := range []string{capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 507, 1)
			h.provider.Record.Platform = platform
			h.provider.Record.Type = capability.ProviderTypeAPIKey

			require.False(t, h.handle(openAI403HTMLBody))
			h.requireNoProviderPenalty(t)
		})
	}
}

// TestHandleUpstreamErrorKimiZhipuDeepSeekStructured403UsesCumulativeCooldown
// supplies structured forbidden errors to three Kimi, Zhipu, and DeepSeek
// platforms with a first counter value. Each increments the counter and writes
// temporary cooldown rather than permanent error.
func TestHandleUpstreamErrorKimiZhipuDeepSeekStructured403UsesCumulativeCooldown(t *testing.T) {
	for _, platform := range []string{capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 508, 1)
			h.provider.Record.Platform = platform
			h.provider.Record.Type = capability.ProviderTypeAPIKey

			require.True(t, h.handle(`{"error":{"message":"forbidden"}}`))
			require.Equal(t, 1, h.counter.increments)
			require.Equal(t, 1, h.repo.TempCalls)
			require.Zero(t, h.repo.SetErrorCalls)
			require.Contains(t, h.repo.LastTempReason, "(1/3)")
		})
	}
}

// TestHandleUpstreamError_OpenAIHTML403RepeatedNeverEscalates repeats HTML 403
// beyond the configured disable threshold. All calls return no scheduling-stop
// decision and leave the stored counter and provider-penalty observations
// unused.
func TestHandleUpstreamError_OpenAIHTML403RepeatedNeverEscalates(t *testing.T) {
	h := newOpenAI403TestHarness(t, 502, 1, 2, 3, 4, 5)

	for i := range providercore.OpenAI403DisableThresholdDefault + 2 {
		require.False(t, h.handle(openAI403HTMLBody), "第 %d 次 HTML 403 仍不得判定提供商应下线", i+1)
	}

	h.requireNoProviderPenalty(t)
}

// TestHandleUpstreamError_OpenAIStructured403StillPenalizes compares
// first-hit, threshold, and plain-text forbidden failures on OpenAI OAuth.
// Counter-backed structured failures pause or disable according to count, and
// plain text still receives ordinary cooldown.
func TestHandleUpstreamError_OpenAIStructured403StillPenalizes(t *testing.T) {
	t.Run("first_hit_temp_unschedulable", func(t *testing.T) {
		h := newOpenAI403TestHarness(t, 503, 1)

		require.True(t, h.handle(`{"error":{"message":"Your provider is not authorized"}}`))
		require.Equal(t, 1, h.counter.increments)
		require.Equal(t, 1, h.repo.TempCalls)
		require.Equal(t, 0, h.repo.SetErrorCalls)
		require.Contains(t, h.repo.LastTempReason, "Your provider is not authorized")
		require.Len(t, h.blocker.Providers, 1)
	})

	t.Run("threshold_disables", func(t *testing.T) {
		h := newOpenAI403TestHarness(t, 504, int64(providercore.OpenAI403DisableThresholdDefault))

		require.True(t, h.handle(`{"error":{"message":"workspace forbidden by policy"}}`))
		require.Equal(t, 1, h.repo.SetErrorCalls)
		require.Contains(t, h.repo.LastErrorMsg, "workspace forbidden by policy")
	})

	t.Run("plain_text_body_unchanged", func(t *testing.T) {
		h := newOpenAI403TestHarness(t, 505, 1)

		require.True(t, h.handle("Forbidden"))
		require.Equal(t, 1, h.repo.TempCalls)
	})
}

// TestHandleUpstreamError_HTML403OnOtherPlatformsUnchanged supplies HTML
// forbidden bodies to Anthropic and Gemini API Key targets. Each records a
// permanent error and stops scheduling for the observed failure.
func TestHandleUpstreamError_HTML403OnOtherPlatformsUnchanged(t *testing.T) {
	for _, platform := range []string{capability.PlatformAnthropic, capability.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			repo := &gatewaytestkit.HealthStoreRecorder{}
			svc := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{}, nil)

			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 506, Platform: platform, Type: capability.ProviderTypeAPIKey}}

			shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), svc, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(openAI403HTMLBody), nil)).StopScheduling

			require.True(t, shouldDisable)
			require.Equal(t, 1, repo.SetErrorCalls, "其他平台保持原有 SetError 行为")
		})
	}
}

// TestRateLimitService_HandleUpstreamError_OpenAI403FirstHitTempUnschedulable
// uses a first 403 counter result with a structured edge rejection. Temporary
// persistence and runtime block retain the provider ID and message, while
// permanent error stays unused.
func TestRateLimitService_HandleUpstreamError_OpenAI403FirstHitTempUnschedulable(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{1}}
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{ForbiddenCounter: counter, Block: func(v *providercore.Record, until time.Time, reason string) {
		blocker.BlockProviderScheduling(gatewayprovider.NewExecutionProvider(v), until, reason)
	}}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 301,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"temporary edge rejection"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.SetErrorCalls)
	require.Equal(t, 1, repo.TempCalls)
	require.Contains(t, repo.LastTempReason, "temporary edge rejection")
	require.Contains(t, repo.LastTempReason, "(1/3)")
	require.Len(t, blocker.Providers, 1)
	require.Equal(t, provider.Record.ID, blocker.Providers[0].Record.ID)
	require.Equal(t, "openai_403_temp", blocker.Reasons[0])
	require.True(t, blocker.Until[0].After(time.Now()))
}

// TestRateLimitService_HandleUpstreamError_OpenAI403ThresholdDisables returns
// a threshold 403 count for a forbidden workspace response. Health records one
// permanent provider error, retains counter/message diagnostics, and creates
// no temporary pause.
func TestRateLimitService_HandleUpstreamError_OpenAI403ThresholdDisables(t *testing.T) {
	repo := &gatewaytestkit.HealthStoreRecorder{}
	counter := &gatewaytestkit.ForbiddenCounter{Counts: []int64{3}}
	service := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{ForbiddenCounter: counter}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 302,
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), service, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusForbidden, http.Header{}, []byte(`{"error":{"message":"workspace forbidden by policy"}}`), nil)).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.SetErrorCalls)
	require.Equal(t, 0, repo.TempCalls)
	require.Contains(t, repo.LastErrorMsg, "workspace forbidden by policy")
	require.Contains(t, repo.LastErrorMsg, "consecutive_403=3/3")
}

// SetRateLimited records the requested reset deadline and increments the
// fixture's global-limit count.
func (r *anthropicWindowLimitRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.rateLimitCalls++
	r.lastRateLimitReset = resetAt
	return nil
}

// SetTempUnschedulable increments the fixture count for provider
// temporary-pause observations.
func (r *anthropicWindowLimitRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.tempUnschedCalls++
	return nil
}

// SetModelRateLimit records the scope and reset deadline with its model-limit
// invocation count.
func (r *anthropicWindowLimitRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, resetAt time.Time, _ ...string) error {
	r.modelRateLimitCalls++
	r.lastModelRateLimitScope = scope
	r.lastModelRateLimitReset = resetAt
	return nil
}

// UpdateSessionWindow records borrowed start/end pointers plus status and call
// count. Test callers keep the referenced timestamps stable until assertions
// finish reading the captured observation.
func (r *anthropicWindowLimitRepo) UpdateSessionWindow(_ context.Context, _ int64, start, end *time.Time, status string) error {
	r.sessionWindowCalls++
	r.lastWindowStart = start
	r.lastWindowEnd = end
	r.lastWindowStatus = status
	return nil
}

// UpdateExtra retains the supplied update map for passive quota assertions.
// The fixture does not clone entries, so callers keep those values stable
// while checking the observation.
func (r *anthropicWindowLimitRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.lastExtraUpdates = updates
	return nil
}

// TestHandleUpstreamError_AnthropicWindowLimitPreemptsTempUnschedRule reports
// exhausted official five-hour utilization alongside a shorter local 429 rule.
// The official reset governs provider cooldown and rejected session-window
// times, with no local temporary pause.
func TestHandleUpstreamError_AnthropicWindowLimitPreemptsTempUnschedRule(t *testing.T) {
	resetAt := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.02")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(resetAt.Unix(), 10))

	repo := &anthropicWindowLimitRepo{}
	svc := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 42,
			Type:     capability.ProviderTypeOAuth,
			Platform: capability.PlatformAnthropic,
			Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(http.StatusTooManyRequests),
						"keywords":         []any{"rate limit"},
						"duration_minutes": float64(10),
					},
				},
			},
		},
	}

	gatewayprovider.ApplyExecutionHealth(context.Background(), svc, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, headers, []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your provider's rate limit. Please try again later."}}`), nil))

	require.Zero(t, repo.tempUnschedCalls, "官方 Anthropic 窗口限流不应被本地临时不可调度规则缩短")
	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, resetAt, repo.lastRateLimitReset)
	require.Equal(t, 1, repo.sessionWindowCalls)
	require.NotNil(t, repo.lastWindowStart)
	require.NotNil(t, repo.lastWindowEnd)
	require.Equal(t, resetAt, *repo.lastWindowEnd)
	require.Equal(t, resetAt.Add(-5*time.Hour), *repo.lastWindowStart)
	require.Equal(t, "rejected", repo.lastWindowStatus)
}

// fable429Headers creates a rejected Fable-specific window alongside allowed
// five-hour and seven-day windows. Supplied timestamps are encoded as Unix
// seconds, and the fixture includes overage plus passive observation fields.
func fable429Headers(reset5h, resetOI time.Time) http.Header {
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset5h.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.41")
	headers.Set("anthropic-ratelimit-unified-7d-reset", strconv.FormatInt(resetOI.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-7d-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d-utilization", "0.56")
	headers.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(resetOI.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-7d_oi-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-7d_oi-surpassed-threshold", "1.0")
	headers.Set("anthropic-ratelimit-unified-7d_oi-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-fallback-percentage", "0.5")
	headers.Set("anthropic-ratelimit-unified-overage-disabled-reason", "org_level_disabled")
	headers.Set("anthropic-ratelimit-unified-overage-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-representative-claim", "seven_day_overage_included")
	headers.Set("anthropic-ratelimit-unified-reset", strconv.FormatInt(resetOI.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-status", "rejected")
	return headers
}

// TestHandleUpstreamError_Anthropic7dOiOnlyMarksModelRateLimit rejects the
// Fable-specific window while ordinary provider windows remain allowed. Health
// records the Fable shared model limit and passive observations, leaving
// provider and five-hour session penalties unused.
func TestHandleUpstreamError_Anthropic7dOiOnlyMarksModelRateLimit(t *testing.T) {
	now := time.Now()
	reset5h := now.Add(2 * time.Hour).Truncate(time.Second)
	resetOI := now.Add(80 * time.Hour).Truncate(time.Second)
	headers := fable429Headers(reset5h, resetOI)

	repo := &anthropicWindowLimitRepo{}
	svc := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 42,
			Type:     capability.ProviderTypeOAuth,
			Platform: capability.PlatformAnthropic,
			Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(http.StatusTooManyRequests),
						"keywords":         []any{"rate limit"},
						"duration_minutes": float64(10),
					},
				},
			},
		},
	}

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), svc, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, headers, []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your provider's rate limit. Please try again later."}}`), []string{"claude-fable-5"})).StopScheduling

	require.False(t, shouldDisable)
	require.Zero(t, repo.rateLimitCalls, "7d_oi (Fable-only) window must not mark the whole provider rate limited")
	require.Zero(t, repo.tempUnschedCalls, "7d_oi window must not trigger local temp-unsched rules")
	require.Zero(t, repo.sessionWindowCalls, "7d_oi window must not rewrite the 5h session window as rejected")
	require.Equal(t, 1, repo.modelRateLimitCalls)
	require.Equal(t, providercore.AnthropicFableRateLimitKey, repo.lastModelRateLimitScope)
	require.Equal(t, resetOI, repo.lastModelRateLimitReset)

	// Passive sampling still updates Fable usage while the response reports a rate limit.
	require.NotNil(t, repo.lastExtraUpdates)
	require.Equal(t, 1.0, repo.lastExtraUpdates["passive_usage_7d_oi_utilization"])
	require.Equal(t, resetOI.Unix(), repo.lastExtraUpdates["passive_usage_7d_oi_reset"])
	require.Equal(t, 0.41, repo.lastExtraUpdates["session_window_utilization"])
}

// TestHandleUpstreamError_Anthropic5hWindowStillWinsOver7dOi rejects both
// five-hour and Fable windows in the same observation. Provider cooldown uses
// the five-hour reset and the Fable model scope is also recorded.
func TestHandleUpstreamError_Anthropic5hWindowStillWinsOver7dOi(t *testing.T) {
	// A rejected five-hour window sets the provider cooldown using its reset
	// and also records the Fable model limit.
	now := time.Now()
	reset5h := now.Add(2 * time.Hour).Truncate(time.Second)
	resetOI := now.Add(80 * time.Hour).Truncate(time.Second)
	headers := fable429Headers(reset5h, resetOI)
	headers.Set("anthropic-ratelimit-unified-5h-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")

	repo := &anthropicWindowLimitRepo{}
	svc := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 42, Type: capability.ProviderTypeOAuth, Platform: capability.PlatformAnthropic}}

	gatewayprovider.ApplyExecutionHealth(context.Background(), svc, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, headers, nil, []string{"claude-fable-5"}))

	require.Equal(t, 1, repo.rateLimitCalls, "exhausted 5h window must still rate limit the provider")
	require.Equal(t, reset5h, repo.lastRateLimitReset)
	require.Equal(t, 1, repo.modelRateLimitCalls)
	require.Equal(t, providercore.AnthropicFableRateLimitKey, repo.lastModelRateLimitScope)
}

// TestHandleUpstreamError_AnthropicProviderWindowStillWinsOver7dOi rejects the
// ordinary seven-day and Fable-specific windows together. Provider cooldown
// uses the seven-day reset while retaining the Fable model-limit write.
func TestHandleUpstreamError_AnthropicProviderWindowStillWinsOver7dOi(t *testing.T) {
	// A rejected seven-day provider window sets provider and Fable model cooldowns.
	now := time.Now()
	reset5h := now.Add(2 * time.Hour).Truncate(time.Second)
	resetOI := now.Add(80 * time.Hour).Truncate(time.Second)
	headers := fable429Headers(reset5h, resetOI)
	headers.Set("anthropic-ratelimit-unified-7d-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-7d-utilization", "1.02")

	repo := &anthropicWindowLimitRepo{}
	svc := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 42, Type: capability.ProviderTypeOAuth, Platform: capability.PlatformAnthropic}}

	gatewayprovider.ApplyExecutionHealth(context.Background(), svc, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, headers, nil, []string{"claude-fable-5"}))

	require.Equal(t, 1, repo.rateLimitCalls, "exhausted 7d window must still rate limit the provider")
	require.Equal(t, resetOI, repo.lastRateLimitReset)
	require.Equal(t, 1, repo.modelRateLimitCalls, "Fable model rate limit should also be recorded")
	require.Equal(t, providercore.AnthropicFableRateLimitKey, repo.lastModelRateLimitScope)
}

// TestHandleUpstreamError_Anthropic429Without7dOiKeepsLegacyBehavior supplies
// 429 with allowed ordinary windows and no Fable signal. The earlier reset
// sets provider cooldown, session-window observation runs, and no model limit
// is recorded.
func TestHandleUpstreamError_Anthropic429Without7dOiKeepsLegacyBehavior(t *testing.T) {
	// Without 7d_oi or a rejected provider window, a 429 uses the earlier reset
	// for the provider cooldown.
	now := time.Now()
	reset5h := now.Add(2 * time.Hour).Truncate(time.Second)
	reset7d := now.Add(80 * time.Hour).Truncate(time.Second)

	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset5h.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.41")
	headers.Set("anthropic-ratelimit-unified-7d-reset", strconv.FormatInt(reset7d.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-7d-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d-utilization", "0.56")

	repo := &anthropicWindowLimitRepo{}
	svc := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)

	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 42, Type: capability.ProviderTypeOAuth, Platform: capability.PlatformAnthropic}}

	gatewayprovider.ApplyExecutionHealth(context.Background(), svc, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusTooManyRequests, headers, nil, []string{"claude-fable-5"}))

	require.Zero(t, repo.modelRateLimitCalls, "no 7d_oi signal → no model rate limit")
	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, reset5h, repo.lastRateLimitReset, "legacy path picks the sooner reset")
	require.Equal(t, 1, repo.sessionWindowCalls)
}

// newUpstreamHealthForTest binds the production observer to fixture storage,
// cache, settings, and health options. A supplied config replaces
// unauthorized, overload, and usage-monitor interval options before gatewaytestkit
// creates the shared components.
func newUpstreamHealthForTest(store gatewayprovider.ExecutionProviderStore, cfg *config.Config, cache providercore.TempUnschedCache, options providercore.HealthOptions, readers *gatewayprovider.RuntimeReaders) *provideradapter.UpstreamHealth {
	if cfg != nil {
		options.UnauthorizedCooldownMinutes = cfg.Provider.Cooldown.OAuth401Minutes
		options.OverloadMinutes = cfg.Provider.Cooldown.OverloadMinutes
		options.UsageMonitorIntervalMinutes = cfg.Provider.UsageMonitor.IntervalMinutes
	}
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Cache: cache, Options: options, Readers: readers})
}

// newExecutionReadersFixture binds runtime readers after wrapping a nonnil
// repository in the settings cache. Nil repository remains nil, and the
// compatibility config parameter is currently unused.
func newExecutionReadersFixture(repo settings.Repository, _ *config.Config) *gatewayprovider.RuntimeReaders {
	if repo != nil {
		repo = settings.New(repo)
	}
	return gatewaytestkit.RuntimeReaders(repo)
}

// TestRateLimitService_TempUnschedulableContextPreservesModelForPoolDependency
// places the cooldown model in request hints while omitting explicit model
// arguments for a pool target. Health writes that model scope and leaves
// provider-wide temporary cooldown unused.
func TestRateLimitService_TempUnschedulableContextPreservesModelForPoolDependency(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)

	provider := gatewaytestkit.ModelNotFoundProvider()
	provider.Record.Credentials["pool_mode"] = true
	ctx := requeststate.WithHealthModel(context.Background(), []string{"gpt-5.4"})

	// With no model argument, pool health observation uses the contextual
	// canonical model for cooldown scope.
	observation := gatewayprovider.HealthObservationFromContext(ctx, http.StatusNotFound, http.Header{},
		[]byte(`{"error":{"message":"endpoint not found"}}`), nil)
	handled := gatewayprovider.ApplyExecutionHealth(ctx, svc, provider, observation).StopScheduling

	require.True(t, handled)
	require.Zero(t, repo.TempCalls)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.4", repo.ModelRateLimitCalls[0].Scope)
}

// TestRateLimitService_ModelTempUnschedulableIsolatesSchedulerByModel reports
// a final upstream model when provider mappings contain another possible hop.
// The stored scope stays at that final identity, rejecting its public alias
// while another model stays schedulable.
func TestRateLimitService_ModelTempUnschedulableIsolatesSchedulerByModel(t *testing.T) {
	repo := &gatewaytestkit.ModelHealthStore{}
	svc := newUpstreamHealthForTest(repo, nil, nil, providercore.HealthOptions{}, nil)

	provider := gatewaytestkit.ModelNotFoundProvider()
	provider.Record.Credentials["model_mapping"] = map[string]any{
		"public-a":   "upstream-a",
		"upstream-a": "upstream-b",
	}

	handled := gatewayprovider.ApplyExecutionHealth(context.Background(), svc, provider, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":"endpoint not found"}}`), []string{"upstream-a"})).StopScheduling

	require.True(t, handled)
	require.Len(t, repo.ModelRateLimitCalls, 1)
	call := repo.ModelRateLimitCalls[0]
	require.Equal(t, "upstream-a", call.Scope, "canonical upstream model must not be mapped a second time")

	provider.Record.Extra = map[string]any{
		"model_rate_limits": map[string]any{
			call.Scope: map[string]any{
				"rate_limit_reset_at": call.ResetAt.UTC().Format(time.RFC3339),
			},
		},
	}

	require.False(t, gatewayprovider.ExecutionModelPolicy(provider).Schedulable(context.Background(), "public-a"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Schedulable(context.Background(), "gpt-5.6-sol"))
}

// ListByPlatform records a lookup and returns active fixtures for the supplied
// platform, or its configured error. The result slice is new, while nested
// provider data remains borrowed from teamProviders.
func (r *teamLinkedProviderRepoStub) ListByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]gatewayprovider.ExecutionProvider, 0, len(r.teamProviders))
	for _, acc := range r.teamProviders {
		if acc.Record.Platform == platform && acc.Record.Status == billing.StatusActive {
			out = append(out, acc)
		}
	}
	return out, nil
}

// SetError returns the configured per-ID error before recording a successful
// write. Successful calls append the ID and save the message, leaving failed
// IDs absent from both observations.
func (r *teamLinkedProviderRepoStub) SetError(ctx context.Context, id int64, errorMsg string) error {
	if err, ok := r.failSetError[id]; ok {
		return err
	}
	r.setErrorIDs = append(r.setErrorIDs, id)
	if r.setErrorMsgs == nil {
		r.setErrorMsgs = make(map[int64]string)
	}
	r.setErrorMsgs[id] = errorMsg
	return nil
}

// newTeamLinkedProvider constructs an active OpenAI OAuth fixture with the
// supplied team account identity.
func newTeamLinkedProvider(id int64, teamID string) gatewayprovider.ExecutionProvider {
	return gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Credentials: map[string]any{"chatgpt_account_id": teamID},
		},
	}
}

// newTeamLinkedFixture creates trigger, matching teammates, another team, API
// Key, shadow, and errored records for fanout. Fresh credential maps let each
// scenario modify provider classification before calling health.
func newTeamLinkedFixture() []gatewayprovider.ExecutionProvider {
	parentID := int64(1)
	shadow := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 5,
			Platform:         capability.PlatformOpenAI,
			Type:             capability.ProviderTypeOAuth,
			Status:           billing.StatusActive,
			ParentProviderID: &parentID,
		},
	}
	apikey := newTeamLinkedProvider(4, "team-A")
	apikey.Record.Type = capability.ProviderTypeAPIKey
	erroredSibling := newTeamLinkedProvider(7, "team-A")
	erroredSibling.Record.Status = providercore.StatusError
	return []gatewayprovider.ExecutionProvider{
		newTeamLinkedProvider(1, "team-A"),
		newTeamLinkedProvider(2, "team-A"),
		newTeamLinkedProvider(3, "team-B"),
		apikey,
		shadow,
		newTeamLinkedProvider(6, "team-A"),
		erroredSibling,
	}
}

// newTeamLinkedTestService constructs a health observer and its runtime-block
// recorder for team scenarios.
func newTeamLinkedTestService(repo *teamLinkedProviderRepoStub) (*provideradapter.UpstreamHealth,
	*gatewaytestkit.RuntimeBlockRecorder,
) {
	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	rl := newUpstreamHealthForTest(repo, &config.Config{}, nil, providercore.HealthOptions{Block: func(v *providercore.Record, until time.Time, reason string) {
		blocker.BlockProviderScheduling(gatewayprovider.NewExecutionProvider(v), until, reason)
	}}, nil)

	return rl, blocker
}

// TestTeamLinkedError_FanoutMarksSameTeamProviders supplies workspace
// deactivation on a trigger among mixed team/provider records. Matching
// teammates are written before the trigger, and recorded runtime-block order
// separates team fanout from ordinary authentication handling.
func TestTeamLinkedError_FanoutMarksSameTeamProviders(t *testing.T) {
	repo := &teamLinkedProviderRepoStub{teamProviders: newTeamLinkedFixture()}
	rl, blocker := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedProvider(1, "team-A")

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &trigger, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil)).StopScheduling

	require.True(t, shouldDisable)
	// Fan-out marks teammates 2 and 6 first; ordinary 402 handling then marks trigger 1.
	require.Equal(t, []int64{2, 6, 1}, repo.setErrorIDs)
	require.Contains(t, repo.setErrorMsgs[2], "team-linked error triggered by provider #1")
	require.Contains(t, repo.setErrorMsgs[6], "team-linked error triggered by provider #1")
	require.Contains(t, repo.setErrorMsgs[1], "Workspace deactivated (402)")
	require.NotContains(t, repo.setErrorMsgs[1], "team-linked")
	// Recorded fanout blocks use the team-linked reason; the trigger uses auth_error.
	require.Equal(t, []string{providercore.OpenAITeamLinkedErrorBlockReason, providercore.OpenAITeamLinkedErrorBlockReason, "auth_error"}, blocker.Reasons)
	require.Equal(t, int64(2), blocker.Providers[0].Record.ID)
	require.Equal(t, int64(6), blocker.Providers[1].Record.ID)
	require.Equal(t, int64(1), blocker.Providers[2].Record.ID)
}

// TestTeamLinkedError_GenericPaymentErrorDoesNotFanout supplies ordinary
// insufficient-balance text on a team OAuth target. Health writes the trigger
// alone and performs no team-provider list query.
func TestTeamLinkedError_GenericPaymentErrorDoesNotFanout(t *testing.T) {
	repo := &teamLinkedProviderRepoStub{teamProviders: newTeamLinkedFixture()}
	rl, _ := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedProvider(1, "team-A")

	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &trigger, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(`{"error":{"message":"insufficient balance"}}`), nil))

	require.Equal(t, []int64{1}, repo.setErrorIDs)
	require.Contains(t, repo.setErrorMsgs[1], "Payment required (402)")
	require.Zero(t, repo.listCalls)
}

// TestTeamLinkedError_DedupWithinTTL reports workspace deactivation on two
// teammates through the same observer. The team list is read once, repeated
// fanout is suppressed, and ordinary error handling still writes the second
// trigger.
func TestTeamLinkedError_DedupWithinTTL(t *testing.T) {
	repo := &teamLinkedProviderRepoStub{teamProviders: newTeamLinkedFixture()}
	rl, _ := newTeamLinkedTestService(repo)
	first := newTeamLinkedProvider(1, "team-A")
	second := newTeamLinkedProvider(2, "team-A")

	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &first, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil))
	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &second, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil))

	// Deduplication suppresses repeated fan-out; ordinary 402 handling still marks trigger 2.
	require.Equal(t, []int64{2, 6, 1, 2}, repo.setErrorIDs)
	require.Equal(t, 1, repo.listCalls)
}

// TestTeamLinkedError_APIKeyTriggerDoesNotFanout reports workspace
// deactivation on an API Key target carrying the team account ID. Only that
// trigger receives an error, and no team enumeration occurs.
func TestTeamLinkedError_APIKeyTriggerDoesNotFanout(t *testing.T) {
	repo := &teamLinkedProviderRepoStub{teamProviders: newTeamLinkedFixture()}
	rl, _ := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedProvider(4, "team-A")
	trigger.Record.Type = capability.ProviderTypeAPIKey

	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &trigger, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil))

	require.Equal(t, []int64{4}, repo.setErrorIDs)
	require.Zero(t, repo.listCalls)
}

// TestRateLimitService_ApplyProviderSchedulingThreshold_SetsTempUnschedulable
// sets an 80 percent OpenAI threshold with a 91.5 percent window observation.
// Threshold handling writes the reset deadline and detailed reason, then
// copies pause state to the target.
func TestRateLimitService_ApplyProviderSchedulingThreshold_SetsTempUnschedulable(t *testing.T) {
	settingsRepo := settingstestkit.NewMemory()
	settingsRepo.Data[providercore.SettingKeyProviderSchedulingThresholds] = `{"openai":80}`

	providerRepo := &gatewaytestkit.HealthStoreRecorder{}
	rl := newUpstreamHealthForTest(providerRepo, &config.Config{}, nil, providercore.HealthOptions{},

		newExecutionReadersFixture(settingsRepo, &config.Config{}))

	until := time.Now().UTC().Add(6 * time.Hour)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1001,
			Platform:    capability.PlatformOpenAI,
			Status:      billing.StatusActive,
			Schedulable: true,
			Extra: map[string]any{
				"codex_7d_used_percent": 91.5,
				"codex_7d_reset_at":     until.Format(time.RFC3339),
			},
		},
	}

	blocked := gatewayprovider.ApplyExecutionSchedulingThreshold(context.Background(), rl, provider)

	require.True(t, blocked)
	require.Equal(t, 1, providerRepo.TempCalls)
	require.NotNil(t, provider.Record.TempUnschedulableUntil)
	require.WithinDuration(t, until, *provider.Record.TempUnschedulableUntil, time.Second)
	require.True(t, providercore.IsProviderSchedulingThresholdReason(providerRepo.LastTempReason))

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(providerRepo.LastTempReason), &payload))
	require.Equal(t, capability.PlatformOpenAI, payload["platform"])
	require.Equal(t, "7d", payload["window"])
	require.Equal(t, float64(80), payload["threshold_percent"])
	require.Equal(t, float64(91.5), payload["used_percent"])
	require.Contains(t, payload["error_message"], "91.5% used >= 80%")
}

// TestRateLimitService_ApplyProviderSchedulingThreshold_UsesProviderOverrideInReason
// combines a 90 percent global threshold with an 80 percent provider override.
// An 85.5 percent observation pauses and records the provider override in
// diagnostic JSON.
func TestRateLimitService_ApplyProviderSchedulingThreshold_UsesProviderOverrideInReason(t *testing.T) {
	settingsRepo := settingstestkit.NewMemory()
	settingsRepo.Data[providercore.SettingKeyProviderSchedulingThresholds] = `{"openai":90}`

	providerRepo := &gatewaytestkit.HealthStoreRecorder{}
	rl := newUpstreamHealthForTest(providerRepo, &config.Config{}, nil, providercore.HealthOptions{},

		newExecutionReadersFixture(settingsRepo, &config.Config{}))

	until := time.Now().UTC().Add(6 * time.Hour)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1003,
			Platform:    capability.PlatformOpenAI,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"provider_scheduling_threshold": 80,
			},
			Extra: map[string]any{
				"codex_7d_used_percent": 85.5,
				"codex_7d_reset_at":     until.Format(time.RFC3339),
			},
		},
	}

	blocked := gatewayprovider.ApplyExecutionSchedulingThreshold(context.Background(), rl, provider)

	require.True(t, blocked)
	require.Equal(t, 1, providerRepo.TempCalls)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(providerRepo.LastTempReason), &payload))
	require.Equal(t, float64(80), payload["threshold_percent"])
	require.Equal(t, float64(85.5), payload["used_percent"])
	require.Contains(t, payload["error_message"], "85.5% used >= 80%")
}

// SetModelRateLimit records scope, reset, count, and the first optional
// reason. With no reason argument, its previous recorded reason remains
// available, matching the fixture's capture behavior.
func (r *fableSchedulingThresholdRepoStub) SetModelRateLimit(_ context.Context, _ int64, scope string, resetAt time.Time, reason ...string) error {
	r.modelCalls++
	r.lastModelScope = scope
	r.lastModelReset = resetAt
	if len(reason) > 0 {
		r.lastModelReason = reason[0]
	}
	return nil
}

// TestRateLimitService_ApplyProviderSchedulingThreshold_FableOnlyLimitsFableModels
// exceeds a provider override in the Fable-specific window while ordinary
// usage remains lower. The shared Fable model cooldown leaves other Claude
// models schedulable, and repeated evaluation avoids a duplicate write.
func TestRateLimitService_ApplyProviderSchedulingThreshold_FableOnlyLimitsFableModels(t *testing.T) {
	settingsRepo := settingstestkit.NewMemory()
	settingsRepo.Data[providercore.SettingKeyProviderSchedulingThresholds] = `{"anthropic":100}`

	providerRepo := &fableSchedulingThresholdRepoStub{}
	rl := newUpstreamHealthForTest(providerRepo, &config.Config{}, nil, providercore.HealthOptions{},

		newExecutionReadersFixture(settingsRepo, &config.Config{}))

	until := time.Now().UTC().Add(4 * 24 * time.Hour).Truncate(time.Second)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1004,
			Platform:    capability.PlatformAnthropic,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"provider_scheduling_threshold": 60,
			},
			Extra: map[string]any{
				"passive_usage_7d_utilization":    0.40,
				"passive_usage_7d_reset":          float64(time.Now().UTC().Add(3 * 24 * time.Hour).Unix()),
				"passive_usage_7d_oi_utilization": 0.61,
				"passive_usage_7d_oi_reset":       float64(until.Unix()),
			},
		},
	}

	blocked := gatewayprovider.ApplyExecutionSchedulingThreshold(context.Background(), rl, provider)

	require.False(t, blocked, "the Fable-only window must not pause the whole provider")
	require.Zero(t, providerRepo.TempCalls)
	require.Equal(t, 1, providerRepo.modelCalls)
	require.Equal(t, providercore.AnthropicFableRateLimitKey, providerRepo.lastModelScope)
	require.WithinDuration(t, until, providerRepo.lastModelReset, time.Second)
	require.True(t, providercore.IsProviderSchedulingThresholdReason(providerRepo.lastModelReason))
	require.False(t, gatewayprovider.ExecutionModelPolicy(provider).Schedulable(context.Background(), "claude-fable-5"))
	require.False(t, gatewayprovider.ExecutionModelPolicy(provider).Schedulable(context.Background(), "claude-fable-5[1m]"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Schedulable(context.Background(), "claude-opus-4-8"))
	require.True(t, gatewayprovider.ExecutionModelPolicy(provider).Schedulable(context.Background(), "claude-sonnet-4-6"))

	blocked = gatewayprovider.ApplyExecutionSchedulingThreshold(context.Background(), rl, provider)

	require.False(t, blocked)
	require.Equal(t, 1, providerRepo.modelCalls, "an active model limit should not be persisted twice")
}

// TestRateLimitService_ApplyProviderSchedulingThreshold_SkipsDuplicateTempUnschedulable
// supplies an active matching threshold pause and unchanged usage/reset
// evidence. Evaluation remains blocked while preserving reason/deadline and
// recording no additional temporary write.
func TestRateLimitService_ApplyProviderSchedulingThreshold_SkipsDuplicateTempUnschedulable(t *testing.T) {
	settingsRepo := settingstestkit.NewMemory()
	settingsRepo.Data[providercore.SettingKeyProviderSchedulingThresholds] = `{"openai":80}`

	providerRepo := &gatewaytestkit.HealthStoreRecorder{}
	rl := newUpstreamHealthForTest(providerRepo, &config.Config{}, nil, providercore.HealthOptions{},

		newExecutionReadersFixture(settingsRepo, &config.Config{}))

	until := time.Now().UTC().Add(6 * time.Hour).Truncate(time.Second)
	existingReason := providercore.BuildDetailedProviderSchedulingThresholdReason(providercore.ProviderSchedulingThresholdReasonInput{
		Platform:         capability.PlatformOpenAI,
		Window:           "7d",
		ThresholdPercent: 80,
		UsedPercent:      91.5,
		Until:            until,
		Now:              until.Add(-time.Hour),
	})
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 1002,
			Platform:                capability.PlatformOpenAI,
			Status:                  billing.StatusActive,
			Schedulable:             true,
			TempUnschedulableUntil:  &until,
			TempUnschedulableReason: existingReason,
			Extra: map[string]any{
				"codex_7d_used_percent": 91.5,
				"codex_7d_reset_at":     until.Format(time.RFC3339),
			},
		},
	}

	blocked := gatewayprovider.ApplyExecutionSchedulingThreshold(context.Background(), rl, provider)

	require.True(t, blocked)
	require.Equal(t, 0, providerRepo.TempCalls)
	require.Equal(t, existingReason, provider.Record.TempUnschedulableReason)
	require.NotNil(t, provider.Record.TempUnschedulableUntil)
	require.True(t, until.Equal(*provider.Record.TempUnschedulableUntil))
}

// TestRateLimitService_ApplyProviderSchedulingThreshold_UnsupportedPlatformDoesNotBlock
// supplies high utilization-like extras on an unsupported platform. Threshold
// evaluation returns false, records no temporary write, and leaves pause
// fields absent.
func TestRateLimitService_ApplyProviderSchedulingThreshold_UnsupportedPlatformDoesNotBlock(t *testing.T) {
	settingsRepo := settingstestkit.NewMemory()
	settingsRepo.Data[providercore.SettingKeyProviderSchedulingThresholds] = `{"openai":80}`

	providerRepo := &gatewaytestkit.HealthStoreRecorder{}
	rl := newUpstreamHealthForTest(providerRepo, &config.Config{}, nil, providercore.HealthOptions{},

		newExecutionReadersFixture(settingsRepo, &config.Config{}))

	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 2002,
			Platform:    "kiro",
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"provider_scheduling_threshold": 1,
			},
			Extra: map[string]any{
				"kiro_sched_utilization": 99.0,
				"kiro_sched_reset_at":    time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			},
		},
	}

	blocked := gatewayprovider.ApplyExecutionSchedulingThreshold(context.Background(), rl, provider)

	require.False(t, blocked)
	require.Equal(t, 0, providerRepo.TempCalls)
	require.Nil(t, provider.Record.TempUnschedulableUntil)
	require.Empty(t, provider.Record.TempUnschedulableReason)
}

// UpdateExtra records a borrowed update map and merges entries into a
// configured target when present. Missing target IDs still retain the update
// observation and return success; no persistence transaction is simulated.
func (r *grokQuotaProviderRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updateCalls++
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	if r.providersByID != nil {
		provider := r.providersByID[id]
		if provider == nil {
			return nil
		}
		if provider.Record.Extra == nil {
			provider.Record.Extra = make(map[string]any)
		}
		maps.Copy(provider.Record.Extra, updates)
	}
	return nil
}

// SetRateLimited records provider ID, reset deadline, and the global-limit
// invocation count.
func (r *grokQuotaProviderRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastRateLimitResetAt = resetAt
	return nil
}

// SetRateLimitedIfLater delegates to the recording setter for fixture
// assertions. It performs no conditional deadline comparison, so tests here
// observe supplied values rather than store-level extension atomicity.
func (r *grokQuotaProviderRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.SetRateLimited(ctx, id, resetAt)
}

// ClearRateLimitIfObserved records comparison timestamps and returns the
// fixture's configured match result. It performs no stored-state comparison,
// letting tests inspect the health consumer's recovery request.
func (r *grokQuotaProviderRepo) ClearRateLimitIfObserved(_ context.Context, _ int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	r.recoveryClearCalls++
	r.recoveryObservedAt = observedLimitedAt
	r.recoveryObservedReset = observedResetAt
	return r.recoveryClearResult, nil
}

// SetTempUnschedulable records the provider, deadline, reason, and fixture
// invocation count.
func (r *grokQuotaProviderRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedID = id
	r.lastTempUnschedUntil = until
	r.lastTempUnschedReason = reason
	return nil
}

// SetError increments the fixture's explicit pool-policy error count and
// returns success.
func (r *grokPoolPolicyProviderRepo) SetError(_ context.Context, _ int64, _ string) error {
	r.setErrorCalls++
	return nil
}

// SetOverloaded increments the fixture's pool-policy overload write count and
// returns success.
func (r *grokPoolPolicyProviderRepo) SetOverloaded(_ context.Context, _ int64, _ time.Time) error {
	r.overloadedCalls++
	return nil
}

// SetModelRateLimit records model scope and count, plus the first reason when
// supplied. An absent reason leaves the previous reason intact for this
// recording fixture.
func (r *grokPoolPolicyProviderRepo) SetModelRateLimit(_ context.Context, _ int64, scope string, _ time.Time, reason ...string) error {
	r.modelRateLimitCalls++
	r.lastModelRateLimitScope = scope
	if len(reason) > 0 {
		r.lastModelRateLimitReason = reason[0]
	}
	return nil
}

// newGrokPoolProvider constructs an active Grok API Key fixture with pool mode
// enabled.
func newGrokPoolProvider(id int64) *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id,
			Platform:    capability.PlatformGrok,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"pool_mode": true},
		},
	}
}

// newGrokHealthForTest binds storage, optional throttle, runtime blocks,
// transient model state, and production model normalization. Each call creates
// fresh runtime/model state for provider health assertions in that scenario.
func newGrokHealthForTest(store provideradapter.GrokHealthStore, throttle *providercore.WriteThrottle) *provideradapter.GrokHealth {
	return &provideradapter.GrokHealth{Store: store, Throttle: throttle, Runtime: providercore.NewRuntimeBlockState(time.Now), ModelTransient: providercore.NewModelTransientState(0), NormalizeModel: func(value *providercore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
}

// newGrokPoolHealthForTest binds a provider-ID-indexed recording store and
// shared runtime blocking to Grok health. It adds the production generic
// observer so explicit custom and temporary rules can be asserted.
func newGrokPoolHealthForTest(value *gatewayprovider.ExecutionProvider) (*provideradapter.GrokHealth, *grokPoolPolicyProviderRepo) {
	repo := &grokPoolPolicyProviderRepo{grokQuotaProviderRepo: &grokQuotaProviderRepo{providersByID: map[int64]*gatewayprovider.ExecutionProvider{value.Record.ID: value}}}
	health := newGrokHealthForTest(repo, nil)
	health.Health = gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: repo, Options: providercore.HealthOptions{Block: health.Runtime.BlockProviderScheduling}})
	return health, repo
}

// GetByID returns the configured target pointer for id, or nil with no error
// when absent. Returned target data stays borrowed for this scenario's later
// health writes and assertions.
func (r *grokQuotaProviderRepo) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	return r.providersByID[id], nil
}

// handleGrokHealthForTest observes a Grok failure with no team-model quota
// hint and returns StopScheduling.
func handleGrokHealthForTest(health *provideradapter.GrokHealth, ctx context.Context, value *gatewayprovider.ExecutionProvider, status int, headers http.Header, body []byte, models ...string) bool {
	return gatewayprovider.ApplyGrokExecutionHealth(ctx, health, value, status, headers, body, "", models...).StopScheduling
}

// handleGrokHealthWithTeamForTest observes a Grok failure with the supplied
// team-model hint and returns StopScheduling.
func handleGrokHealthWithTeamForTest(health *provideradapter.GrokHealth, team string, ctx context.Context, value *gatewayprovider.ExecutionProvider, status int, headers http.Header, body []byte, models ...string) bool {
	return gatewayprovider.ApplyGrokExecutionHealth(ctx, health, value, status, headers, body, team, models...).StopScheduling
}

// Now returns the atomic nanosecond override when nonzero, otherwise the
// current wall time. Clearing the override restores real-time expiry checks
// for fixtures that installed an earlier block.
func (c *grokHealthTestClock) Now() time.Time {
	if n := c.nanos.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Now()
}

// Set publishes the supplied Unix nanoseconds for subsequent fixture clock
// reads.
func (c *grokHealthTestClock) Set(now time.Time) { c.nanos.Store(now.UnixNano()) }

// bindGrokHealthClockForTest replaces the health runtime with a fresh block
// state using the returned clock. Tests set its time before evaluating block
// expiry and retain it for later assertions.
func bindGrokHealthClockForTest(health *provideradapter.GrokHealth) *grokHealthTestClock {
	clock := &grokHealthTestClock{}
	health.Runtime = providercore.NewRuntimeBlockState(clock.Now)
	return clock
}

// bindExpiredGrokHealthForTest creates a block while the fixture clock
// precedes its deadline, then restores real time. This permits the runtime to
// retain an entry that is already expired when the test observes it.
func bindExpiredGrokHealthForTest(health *provideradapter.GrokHealth, id int64, expired time.Time) {
	clock := bindGrokHealthClockForTest(health)
	// Install the block before its deadline using the controlled clock, then
	// clear the override so real wall time exposes the expired entry to assertions.
	clock.Set(expired.Add(-time.Minute))
	health.Runtime.Block(id, expired, "原过期快照夹具")
	clock.nanos.Store(0)
}

// grokInt64PtrForTest returns separately allocated pointer storage for a quota
// integer fixture.
func grokInt64PtrForTest(v int64) *int64 { return &v }

// TestHandleGrokProviderUpstreamErrorPoolModeSkipsDefaultLocalState observes
// authentication, payment, rate, server, and overload statuses on pool API Key
// targets. Default state penalties stay unused, while 429 still writes the
// Retry-After quota observation.
func TestHandleGrokProviderUpstreamErrorPoolModeSkipsDefaultLocalState(t *testing.T) {
	tests := []struct {
		name       string      // pool failure scenario label
		statusCode int         // upstream failure status code
		headers    http.Header // upstream quota response headers
		body       []byte      // upstream failure payload bytes
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized},
		{name: "payment required", statusCode: http.StatusPaymentRequired},
		{name: "forbidden", statusCode: http.StatusForbidden, body: []byte(`{"error":{"message":"access denied"}}`)},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, headers: http.Header{"Retry-After": []string{"60"}}},
		{name: "server error", statusCode: http.StatusInternalServerError},
		{name: "overloaded", statusCode: 529},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newGrokPoolProvider(int64(620 + index))
			svc, repo := newGrokPoolHealthForTest(provider)

			shouldDisable := handleGrokHealthForTest(svc,
				context.Background(),
				provider,
				tt.statusCode,
				tt.headers,
				tt.body,
				"grok-4.5",
			)

			require.False(t, shouldDisable)
			require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
			require.Zero(t, repo.rateLimitedCalls)
			require.Zero(t, repo.tempUnschedCalls)
			require.Zero(t, repo.setErrorCalls)
			require.Zero(t, repo.overloadedCalls)
			require.Zero(t, repo.modelRateLimitCalls)
			require.Nil(t, provider.Record.RateLimitResetAt)
			require.Nil(t, provider.Record.TempUnschedulableUntil)

			if tt.statusCode == http.StatusTooManyRequests {
				require.Equal(t, 1, repo.updateCalls)
				stored, ok := provider.Record.Extra[grokQuotaSnapshotExtraKey].(*grok.QuotaSnapshot)
				require.True(t, ok)
				require.NotNil(t, stored.RetryAfterSeconds)
				require.Equal(t, 60, *stored.RetryAfterSeconds)
			}
		})
	}
}

// TestUpdateGrokUsageSnapshotPoolModeExhaustedSuccessIsObservationOnly reports
// a successful but exhausted request window on a pool target. The quota
// snapshot is stored while rate-limit persistence and runtime blocking remain
// unused.
func TestUpdateGrokUsageSnapshotPoolModeExhaustedSuccessIsObservationOnly(t *testing.T) {
	provider := newGrokPoolProvider(626)
	svc, repo := newGrokPoolHealthForTest(provider)
	resetAt := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	headers := http.Header{
		"X-Ratelimit-Limit-Requests":     []string{"10"},
		"X-Ratelimit-Remaining-Requests": []string{"0"},
		"X-Ratelimit-Reset-Requests":     []string{fmt.Sprintf("%d", resetAt.Unix())},
	}

	svc.ObserveResponse(context.Background(), provider.View(), headers, http.StatusOK, "")

	require.Equal(t, 1, repo.updateCalls)
	require.Zero(t, repo.rateLimitedCalls)
	require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	stored, ok := provider.Record.Extra[grokQuotaSnapshotExtraKey].(*grok.QuotaSnapshot)
	require.True(t, ok)
	require.NotNil(t, stored.Requests)
	require.NotNil(t, stored.Requests.Remaining)
	require.Zero(t, *stored.Requests.Remaining)
}

// TestHandleGrokProviderUpstreamErrorPoolModeKeepsExplicitPolicies combines
// pool mode with custom error codes and matching or unmatched temporary rules.
// Explicit codes disable and matching rules limit the requested model, while
// unmatched rules retain ordinary pool behavior.
func TestHandleGrokProviderUpstreamErrorPoolModeKeepsExplicitPolicies(t *testing.T) {
	t.Run("custom error code still disables provider", func(t *testing.T) {
		provider := newGrokPoolProvider(627)
		provider.Record.Credentials["custom_error_codes_enabled"] = true
		provider.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnauthorized)}
		svc, repo := newGrokPoolHealthForTest(provider)

		shouldDisable := handleGrokHealthForTest(svc,
			context.Background(),
			provider,
			http.StatusUnauthorized,
			nil,
			[]byte(`{"error":{"message":"invalid api key"}}`),
			"grok-4.5",
		)

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrorCalls)
		require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	})

	t.Run("custom non-failover code still disables provider", func(t *testing.T) {
		provider := newGrokPoolProvider(633)
		provider.Record.Credentials["custom_error_codes_enabled"] = true
		provider.Record.Credentials["custom_error_codes"] = []any{float64(http.StatusUnprocessableEntity)}
		svc, repo := newGrokPoolHealthForTest(provider)

		decision := gatewayprovider.ApplyGrokExecutionHealth(context.Background(), svc, provider, http.StatusUnprocessableEntity, nil, []byte(`{"error":{"message":"configured"}}`), "", "grok-4.5")

		require.Equal(t, providercore.ErrorPolicyCustomMatched, decision.Policy)
		require.True(t, decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), http.StatusUnprocessableEntity, false))
		require.False(t, decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), http.StatusUnprocessableEntity))
		require.Equal(t, 1, repo.setErrorCalls)
		require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	})

	t.Run("matching temporary rule only pauses requested model", func(t *testing.T) {
		provider := newGrokPoolProvider(628)
		provider.Record.Credentials["temp_unschedulable_enabled"] = true
		provider.Record.Credentials["temp_unschedulable_rules"] = []any{
			map[string]any{
				"error_code":       float64(http.StatusServiceUnavailable),
				"keywords":         []any{"maintenance"},
				"duration_minutes": float64(30),
			},
		}
		svc, repo := newGrokPoolHealthForTest(provider)

		shouldDisable := handleGrokHealthForTest(svc,
			context.Background(),
			provider,
			http.StatusServiceUnavailable,
			nil,
			[]byte(`{"error":{"message":"maintenance in progress"}}`),
			"grok-4.5",
		)

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.modelRateLimitCalls)
		require.Equal(t, "grok-4.5", repo.lastModelRateLimitScope)
		require.Zero(t, repo.tempUnschedCalls)
		require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	})

	t.Run("unmatched temporary rule keeps default pool behavior", func(t *testing.T) {
		provider := newGrokPoolProvider(629)
		provider.Record.Credentials["temp_unschedulable_enabled"] = true
		provider.Record.Credentials["temp_unschedulable_rules"] = []any{
			map[string]any{
				"error_code":       float64(http.StatusServiceUnavailable),
				"keywords":         []any{"maintenance"},
				"duration_minutes": float64(30),
			},
		}
		svc, repo := newGrokPoolHealthForTest(provider)

		shouldDisable := handleGrokHealthForTest(svc,
			context.Background(),
			provider,
			http.StatusServiceUnavailable,
			nil,
			[]byte(`{"error":{"message":"temporary outage"}}`),
			"grok-4.5",
		)

		require.False(t, shouldDisable)
		require.Zero(t, repo.modelRateLimitCalls)
		require.Zero(t, repo.tempUnschedCalls)
		require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	})
}

// TestHandleGrokProviderUpstreamErrorTempUnschedulesNonRateLimitStates
// supplies OAuth authentication, entitlement, payment, method, and server
// failures. Each produces its configured temporary reason/deadline and runtime
// block, with no rate-limit write.
func TestHandleGrokProviderUpstreamErrorTempUnschedulesNonRateLimitStates(t *testing.T) {
	tests := []struct {
		// temporary cooldown scenario label
		name   string
		status int // observed upstream status code
		// observed response header evidence
		headers         http.Header
		wantReason      string        // expected temporary pause reason
		wantMinCooldown time.Duration // minimum asserted pause duration
		wantMaxCooldown time.Duration // maximum asserted pause duration
	}{
		{
			name:            "unauthorized reauth",
			status:          http.StatusUnauthorized,
			wantReason:      "grok credentials unauthorized",
			wantMinCooldown: 10*time.Minute - time.Second,
			wantMaxCooldown: 10*time.Minute + time.Second,
		},
		{
			name:            "forbidden entitlement",
			status:          http.StatusForbidden,
			wantReason:      "grok access or entitlement denied",
			wantMinCooldown: 30*time.Minute - time.Second,
			wantMaxCooldown: 30*time.Minute + time.Second,
		},
		{
			name:            "payment required",
			status:          http.StatusPaymentRequired,
			wantReason:      "grok payment required",
			wantMinCooldown: 30*time.Minute - time.Second,
			wantMaxCooldown: 30*time.Minute + time.Second,
		},
		{
			name:            "method not allowed",
			status:          http.StatusMethodNotAllowed,
			wantReason:      "grok endpoint not supported (405)",
			wantMinCooldown: 30*time.Minute - time.Second,
			wantMaxCooldown: 30*time.Minute + time.Second,
		},
		{
			name:            "upstream temporary error",
			status:          http.StatusInternalServerError,
			wantReason:      "grok upstream temporary error",
			wantMinCooldown: 2*time.Minute - time.Second,
			wantMaxCooldown: 2*time.Minute + time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 61, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
			repo := &grokQuotaProviderRepo{}
			svc := newGrokHealthForTest(repo, nil)
			before := time.Now()

			handleGrokHealthForTest(svc, context.Background(), provider, tt.status, tt.headers, nil)

			require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
			require.Equal(t, 1, repo.tempUnschedCalls)
			require.Zero(t, repo.rateLimitedCalls)
			require.Equal(t, provider.Record.ID, repo.lastTempUnschedID)
			require.Equal(t, tt.wantReason, repo.lastTempUnschedReason)
			require.True(t, repo.lastTempUnschedUntil.After(before.Add(tt.wantMinCooldown)))
			require.True(t, repo.lastTempUnschedUntil.Before(before.Add(tt.wantMaxCooldown)))
		})
	}
}

// TestHandleGrokProviderUpstreamErrorSpendingLimit403RateLimits supplies the
// spending-limit marker under HTTP 403. Health records the probe cooldown as a
// rate limit and runtime block, leaving generic temporary pause unused.
func TestHandleGrokProviderUpstreamErrorSpendingLimit403RateLimits(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 614, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	before := time.Now()
	body := []byte(`{"code":"personal-team-blocked:spending-limit","error":"You have run out of credits"}`)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusForbidden, nil, body)

	require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, provider.Record.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, before.Add(grokSpendingLimitProbeCooldown), repo.lastRateLimitResetAt, 2*time.Second)
	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, grok.IsSpendingLimitError(body))
}

// TestHandleGrokProviderUpstreamError5xxRespectsPoolMode compares a pool API
// Key target with ordinary API Key handling for 502. Pool state remains
// available, while ordinary handling receives a two-minute temporary cooldown.
func TestHandleGrokProviderUpstreamError5xxRespectsPoolMode(t *testing.T) {
	t.Run("pool mode keeps scheduling state", func(t *testing.T) {
		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 611,
				Platform: capability.PlatformGrok,
				Type:     capability.ProviderTypeAPIKey,
				Credentials: map[string]any{
					"pool_mode": true,
				},
			},
		}
		repo := &grokQuotaProviderRepo{}
		svc := newGrokHealthForTest(repo, nil)

		handleGrokHealthForTest(svc, context.Background(), provider, http.StatusBadGateway, nil, nil)

		require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
		require.Zero(t, repo.tempUnschedCalls)
		require.Nil(t, provider.Record.TempUnschedulableUntil)
		require.Empty(t, provider.Record.TempUnschedulableReason)
	})

	t.Run("non-pool mode keeps two minute cooldown", func(t *testing.T) {
		provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 612, Platform: capability.PlatformGrok, Type: capability.ProviderTypeAPIKey}}
		repo := &grokQuotaProviderRepo{}
		svc := newGrokHealthForTest(repo, nil)
		before := time.Now()

		handleGrokHealthForTest(svc, context.Background(), provider, http.StatusBadGateway, nil, nil)

		require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
		require.Equal(t, 1, repo.tempUnschedCalls)
		require.Equal(t, provider.Record.ID, repo.lastTempUnschedID)
		require.Equal(t, "grok upstream temporary error", repo.lastTempUnschedReason)
		require.WithinDuration(t, before.Add(2*time.Minute), repo.lastTempUnschedUntil, time.Second)
	})
}

// TestHandleGrokProviderUpstreamError405RespectsPoolMode supplies HTTP 405 to
// a pool API Key target. Runtime blocking and temporary-pause persistence
// remain unused for that default pool policy.
func TestHandleGrokProviderUpstreamError405RespectsPoolMode(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 613,
			Platform: capability.PlatformGrok,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"pool_mode": true,
			},
		},
	}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusMethodNotAllowed, nil, nil)

	require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }), "公共池提供商应跳过 405 默认冷却")
	require.Zero(t, repo.tempUnschedCalls)
	require.Nil(t, provider.Record.TempUnschedulableUntil)
}

// TestHandleGrokProviderUpstreamError429SetsRateLimitedFromRetryAfter observes
// a Grok OAuth 429 with Retry-After=45 and no body. Health records the
// measured 45-second global reset and runtime block while temporary
// unscheduling stays unused.
func TestHandleGrokProviderUpstreamError429SetsRateLimitedFromRetryAfter(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 61, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	before := time.Now()

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"45"}}, nil)

	require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, provider.Record.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, before.Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
}

// TestHandleGrokProviderUpstreamError402RecoversAfterCooldownExpiry creates a
// payment-required temporary cooldown, then supplies an expired runtime/target
// observation. Blocking clears and the provider's scheduling predicate becomes
// true.
func TestHandleGrokProviderUpstreamError402RecoversAfterCooldownExpiry(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 610, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth,
			Status: billing.StatusActive, Schedulable: true,
		},
	}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusPaymentRequired, nil, nil)
	require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	require.Equal(t, 1, repo.tempUnschedCalls)

	expired := time.Now().Add(-time.Second)
	provider.Record.TempUnschedulableUntil = &expired
	bindExpiredGrokHealthForTest(svc, provider.Record.ID, expired)

	require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	require.True(t, provider.View().IsSchedulable())
}

// TestHandleGrokProviderUpstreamError429UsesLatestExhaustedWindowReset reports
// exhausted request and token windows with different future resets. Health
// records the later token reset as the provider rate-limit deadline.
func TestHandleGrokProviderUpstreamError429UsesLatestExhaustedWindowReset(t *testing.T) {
	now := time.Now()
	requestReset := now.Add(10 * time.Minute).Truncate(time.Second)
	tokenReset := now.Add(20 * time.Minute).Truncate(time.Second)
	headers := http.Header{
		"X-Ratelimit-Limit-Requests":     []string{"10"},
		"X-Ratelimit-Remaining-Requests": []string{"0"},
		"X-Ratelimit-Reset-Requests":     []string{fmt.Sprintf("%d", requestReset.Unix())},
		"X-Ratelimit-Limit-Tokens":       []string{"1000"},
		"X-Ratelimit-Remaining-Tokens":   []string{"0"},
		"X-Ratelimit-Reset-Tokens":       []string{fmt.Sprintf("%d", tokenReset.Unix())},
	}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 62, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusTooManyRequests, headers, nil)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, tokenReset, repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
}

// TestHandleGrokProviderUpstreamError429UsesFallbackReset supplies OAuth 429
// with no window or Retry-After evidence. Health records the fallback cooldown
// deadline and leaves temporary pause unused.
func TestHandleGrokProviderUpstreamError429UsesFallbackReset(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 63, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	before := time.Now()

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusTooManyRequests, nil, nil)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(grokRateLimitFallbackCooldown), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
}

// TestGrokRateLimitResetAtForProviderEscalatesRepeated429s combines recent
// prior OAuth cooldown lengths with another short Retry-After snapshot.
// Expected resets progress through repeat and sustained cooldowns to the
// configured adaptive cap.
func TestGrokRateLimitResetAtForProviderEscalatesRepeated429s(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	retryAfter := 45
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		RetryAfterSeconds: &retryAfter,
		UpdatedAt:         now.Format(time.RFC3339),
	}
	tests := []struct {
		// adaptive cooldown scenario label
		name string
		// previous observed cooldown duration
		previousCooldown time.Duration
		// expected adaptive cooldown duration
		wantCooldown time.Duration
	}{
		{name: "repeat after short boundary", previousCooldown: 45 * time.Second, wantCooldown: grokRateLimitRepeatCooldown},
		{name: "sustained repeat", previousCooldown: grokRateLimitRepeatCooldown, wantCooldown: grokRateLimitSustainedCooldown},
		{name: "capped repeat", previousCooldown: grokRateLimitSustainedCooldown, wantCooldown: grokRateLimitMaxAdaptiveCooldown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previousReset := now.Add(-time.Second)
			previousLimited := previousReset.Add(-tt.previousCooldown)
			provider := &gatewayprovider.ExecutionProvider{
				Record: providercore.Record{
					LoadLocation: time.LoadLocation, ID: 630,
					Platform:         capability.PlatformGrok,
					Type:             capability.ProviderTypeOAuth,
					RateLimitedAt:    &previousLimited,
					RateLimitResetAt: &previousReset,
				},
			}

			resetAt, limited := providercore.GrokRateLimitResetAtForProvider(provider.View(), snapshot, now)

			require.True(t, limited)
			require.WithinDuration(t, now.Add(tt.wantCooldown), resetAt, time.Second)
		})
	}
}

// TestGrokRateLimitResetAtForProviderPreservesAuthoritativeAndQuietRecovery
// compares an old quiet-period cooldown with a recent repeated failure plus an
// exhausted window. Quiet recovery uses Retry-After, while the authoritative
// future window governs the later reset.
func TestGrokRateLimitResetAtForProviderPreservesAuthoritativeAndQuietRecovery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	retryAfter := 45
	previousReset := now.Add(-grokRateLimitBackoffQuietPeriod - time.Second)
	previousLimited := previousReset.Add(-grokRateLimitSustainedCooldown)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 631,
			Platform:         capability.PlatformGrok,
			Type:             capability.ProviderTypeOAuth,
			RateLimitedAt:    &previousLimited,
			RateLimitResetAt: &previousReset,
		},
	}
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		RetryAfterSeconds: &retryAfter,
		UpdatedAt:         now.Format(time.RFC3339),
	}

	resetAt, limited := providercore.GrokRateLimitResetAtForProvider(provider.View(), snapshot, now)
	require.True(t, limited)
	require.WithinDuration(t, now.Add(45*time.Second), resetAt, time.Second)

	authoritativeReset := now.Add(2 * time.Hour)
	remaining := int64(0)
	snapshot.Requests = &grok.QuotaWindow{Remaining: &remaining, ResetUnix: grokInt64PtrForTest(authoritativeReset.Unix())}
	recentReset := now.Add(-time.Second)
	recentLimited := recentReset.Add(-grokRateLimitSustainedCooldown)
	provider.Record.RateLimitResetAt = &recentReset
	provider.Record.RateLimitedAt = &recentLimited

	resetAt, limited = providercore.GrokRateLimitResetAtForProvider(provider.View(), snapshot, now)
	require.True(t, limited)
	require.WithinDuration(t, authoritativeReset, resetAt, time.Second)
}

// TestGrokRateLimitResetAtForProviderLeavesAPIKey429PolicyUnchanged gives an
// API Key target a recent sustained cooldown and a short Retry-After
// observation. Its reset remains the short header-based value rather than
// OAuth adaptive escalation.
func TestGrokRateLimitResetAtForProviderLeavesAPIKey429PolicyUnchanged(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	retryAfter := 45
	previousReset := now.Add(-time.Second)
	previousLimited := previousReset.Add(-grokRateLimitSustainedCooldown)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 632,
			Platform:         capability.PlatformGrok,
			Type:             capability.ProviderTypeAPIKey,
			RateLimitedAt:    &previousLimited,
			RateLimitResetAt: &previousReset,
		},
	}
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		RetryAfterSeconds: &retryAfter,
		UpdatedAt:         now.Format(time.RFC3339),
	}

	resetAt, limited := providercore.GrokRateLimitResetAtForProvider(provider.View(), snapshot, now)
	require.True(t, limited)
	require.WithinDuration(t, now.Add(45*time.Second), resetAt, time.Second)
}

// TestGrokRateLimitResetAtUsesFutureWindowAfterRetryAfterExpires supplies an
// old 429 snapshot whose Retry-After elapsed but exhausted request window
// resets later. Rate-limit evaluation retains that future window reset.
func TestGrokRateLimitResetAtUsesFutureWindowAfterRetryAfterExpires(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	observedAt := now.Add(-2 * time.Minute)
	windowReset := now.Add(15 * time.Minute)
	retryAfter := 30
	snapshot := &grok.QuotaSnapshot{
		StatusCode:        http.StatusTooManyRequests,
		UpdatedAt:         observedAt.Format(time.RFC3339),
		RetryAfterSeconds: &retryAfter,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(0),
			ResetUnix: grokInt64PtrForTest(windowReset.Unix()),
		},
	}

	resetAt, limited := providercore.GrokRateLimitResetAt(snapshot, now)

	require.True(t, limited)
	require.WithinDuration(t, windowReset, resetAt, time.Second)
}

// TestHandleGrokProviderUpstreamError429DoesNotShortenExistingPause combines a
// fifteen-minute pause with a new 45-second Retry-After failure. Rate-limit
// persistence records the short reset, while the controlled runtime clock
// confirms the longer pause governs availability.
func TestHandleGrokProviderUpstreamError429DoesNotShortenExistingPause(t *testing.T) {
	existingUntil := time.Now().Add(15 * time.Minute)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 64,
			Platform:                capability.PlatformGrok,
			Type:                    capability.ProviderTypeOAuth,
			TempUnschedulableUntil:  &existingUntil,
			TempUnschedulableReason: "existing pause",
		},
	}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	clock := bindGrokHealthClockForTest(svc)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"45"}}, nil)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, time.Now().Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
	clock.Set(existingUntil.Add(-time.Second))
	require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
	clock.Set(existingUntil.Add(time.Second))
	require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
}

// TestUpdateGrokUsageSnapshotExhaustedSuccessBypassesThrottleAndSetsRateLimited
// consumes ordinary snapshot allowance before storing an exhausted success.
// Both observations are written, and the exhausted window sets rate-limit
// state plus runtime blocking despite the throttle.
func TestUpdateGrokUsageSnapshotExhaustedSuccessBypassesThrottleAndSetsRateLimited(t *testing.T) {
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 65, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, providercore.NewWriteThrottle(time.Hour))
	now := time.Now()

	// Consume the ordinary snapshot write allowance first.
	svc.StoreSnapshot(context.Background(), provider.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(9),
		},
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}, true, "")
	resetAt := now.Add(30 * time.Minute).Truncate(time.Second)
	svc.StoreSnapshot(context.Background(), provider.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(0),
			ResetUnix: grokInt64PtrForTest(resetAt.Unix()),
			ResetAt:   resetAt.UTC().Format(time.RFC3339),
		},
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}, true, "")

	require.Equal(t, 2, repo.updateCalls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, provider.Record.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, resetAt, repo.lastRateLimitResetAt, time.Second)
	require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
}

// TestUpdateGrokUsageSnapshotAvailableSuccessDoesNotSetRateLimited stores a
// successful request-window snapshot with positive remaining quota for a Grok
// OAuth target. The snapshot write occurs once and global rate-limit
// persistence remains unused.
func TestUpdateGrokUsageSnapshotAvailableSuccessDoesNotSetRateLimited(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 66, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}

	svc.StoreSnapshot(context.Background(), provider.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Requests: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(10),
			Remaining: grokInt64PtrForTest(1),
		},
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}, true, "")

	require.Equal(t, 1, repo.updateCalls)
	require.Zero(t, repo.rateLimitedCalls)
}

// TestUpdateGrokUsageFromResponseHeaderlessSuccessClearsObservedCooldown
// observes headerless OAuth success after an expired cooldown. Conditional
// recovery receives the observed timestamps, leaves informative quota storage
// untouched, and retains the shared target reset pointer.
func TestUpdateGrokUsageFromResponseHeaderlessSuccessClearsObservedCooldown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	limitedAt := now.Add(-grokRateLimitRepeatCooldown)
	observedResetAt := now.Add(-time.Second)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 660,
			Platform:         capability.PlatformGrok,
			Type:             capability.ProviderTypeOAuth,
			RateLimitedAt:    &limitedAt,
			RateLimitResetAt: &observedResetAt,
		},
	}
	repo := &grokQuotaProviderRepo{recoveryClearResult: true}
	svc := newGrokHealthForTest(repo, providercore.NewWriteThrottle(time.Hour))

	svc.ObserveResponse(context.Background(), provider.View(), nil, http.StatusOK, "")

	require.Zero(t, repo.updateCalls, "headerless success must not overwrite an informative quota snapshot")
	require.Equal(t, 1, repo.recoveryClearCalls)
	require.Equal(t, limitedAt, repo.recoveryObservedAt)
	require.Equal(t, observedResetAt, repo.recoveryObservedReset)
	require.Same(t, &observedResetAt, provider.Record.RateLimitResetAt, "shared provider snapshots must not be mutated in place")
}

// TestUpdateGrokUsageFromResponseRecoveryRespectsCancellationAndAPIKeyBoundary
// compares an already cancelled OAuth success observation with API Key success
// carrying expired cooldown timestamps. Neither invokes the OAuth conditional
// cooldown recovery writer.
func TestUpdateGrokUsageFromResponseRecoveryRespectsCancellationAndAPIKeyBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	observedResetAt := now.Add(-time.Second)
	observedLimitedAt := observedResetAt.Add(-grokRateLimitRepeatCooldown)

	t.Run("parent cancellation does not mutate provider state", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 661,
				Platform:         capability.PlatformGrok,
				Type:             capability.ProviderTypeOAuth,
				RateLimitedAt:    &observedLimitedAt,
				RateLimitResetAt: &observedResetAt,
			},
		}
		repo := &grokQuotaProviderRepo{recoveryClearResult: true}
		svc := newGrokHealthForTest(repo, nil)

		svc.ObserveResponse(ctx, provider.View(), nil, http.StatusOK, "")

		require.Zero(t, repo.recoveryClearCalls)
	})

	t.Run("API key success does not alter OAuth cooldown state", func(t *testing.T) {
		provider := &gatewayprovider.ExecutionProvider{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 662,
				Platform:         capability.PlatformGrok,
				Type:             capability.ProviderTypeAPIKey,
				RateLimitedAt:    &observedLimitedAt,
				RateLimitResetAt: &observedResetAt,
			},
		}
		repo := &grokQuotaProviderRepo{recoveryClearResult: true}
		svc := newGrokHealthForTest(repo, nil)

		svc.ObserveResponse(context.Background(), provider.View(), nil, http.StatusOK, "")

		require.Zero(t, repo.recoveryClearCalls)
	})
}

// TestUpdateGrokUsageSnapshotExhaustedSuccessWithoutResetUsesFallback stores
// exhausted successful token quota lacking a reset. A fallback reset is
// persisted and added to the snapshot, producing pause before that deadline
// and no pause afterward.
func TestUpdateGrokUsageSnapshotExhaustedSuccessWithoutResetUsesFallback(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 67, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	before := time.Now()

	svc.StoreSnapshot(context.Background(), provider.View(), &grok.QuotaSnapshot{
		StatusCode: http.StatusOK,
		Tokens: &grok.QuotaWindow{
			Limit:     grokInt64PtrForTest(2_000_000),
			Remaining: grokInt64PtrForTest(0),
		},
		UpdatedAt: before.UTC().Format(time.RFC3339),
	}, true, "")

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(grokRateLimitFallbackCooldown), repo.lastRateLimitResetAt, time.Second)
	stored, ok := repo.updates[provider.Record.ID][grokQuotaSnapshotExtraKey].(*grok.QuotaSnapshot)
	require.True(t, ok)
	require.NotNil(t, stored.Tokens.ResetUnix)
	paused, _ := providercore.GrokQuotaWindowAutoPause("tokens", stored.Tokens, before.Add(time.Second))
	require.True(t, paused)
	paused, _ = providercore.GrokQuotaWindowAutoPause("tokens", stored.Tokens, repo.lastRateLimitResetAt.Add(time.Second))
	require.False(t, paused)
}

// TestHandleGrokProviderUpstreamErrorEntitlement403KeepsDefaultCooldown
// supplies subscription-required text under OAuth 403. Health records the
// entitlement reason and a temporary deadline inside the tested thirty-minute
// range.
func TestHandleGrokProviderUpstreamErrorEntitlement403KeepsDefaultCooldown(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 4716, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), provider, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok access or entitlement denied", repo.lastTempUnschedReason)
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(29*time.Minute))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(31*time.Minute))
}

// TestHandleGrokProviderUpstreamError403UsesConfiguredRule matches
// subscription-required body text against a seven-minute temporary rule on a
// Grok OAuth target. Health writes one pause inside the asserted
// six-to-eight-minute timing range.
func TestHandleGrokProviderUpstreamError403UsesConfiguredRule(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 4717,
			Platform: capability.PlatformGrok,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(http.StatusForbidden),
						"keywords":         []any{"subscription"},
						"duration_minutes": float64(7),
					},
				},
			},
		},
	}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), provider, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(6*time.Minute))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(8*time.Minute))
}

// TestHandleGrokProviderUpstreamError403ConfiguredUnmatchedKeepsDefaultCooldown
// configures a 403 rule for unrelated text before a subscription rejection.
// The ordinary entitlement reason and runtime block remain selected.
func TestHandleGrokProviderUpstreamError403ConfiguredUnmatchedKeepsDefaultCooldown(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 4718,
			Platform: capability.PlatformGrok,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(http.StatusForbidden),
						"keywords":         []any{"different failure"},
						"duration_minutes": float64(7),
					},
				},
			},
		},
	}

	handleGrokHealthForTest(svc,
		context.Background(), provider, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok access or entitlement denied", repo.lastTempUnschedReason)
	require.True(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
}

// TestHandleGrokProviderUpstreamError_FreeUsageBodyCoolsProvider supplies
// Free-usage exhaustion under HTTP 400 with no absolute reset. The resulting
// temporary pause uses the short Free-usage probe duration.
func TestHandleGrokProviderUpstreamError_FreeUsageBodyCoolsProvider(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9101, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	before := time.Now()
	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"You've used all the included free usage. Usage resets over a rolling 24-hour window."}}`)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusBadRequest, nil, body)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok free usage exhausted", repo.lastTempUnschedReason)
	// Without an absolute upstream reset, exhausted rolling quota uses a short probe cooldown.
	// It must not impose a 24-hour lockout.
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(grok.GrokFreeUsageProbeCooldown-time.Second))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(grok.GrokFreeUsageProbeCooldown+time.Second))
}

// TestHandleGrokProviderUpstreamError_FreeUsageUsesUpstreamReset reports
// Free-usage exhaustion under 429 with an hour Retry-After. Rate-limit reset
// follows the upstream duration, and no generic temporary pause is written.
func TestHandleGrokProviderUpstreamError_FreeUsageUsesUpstreamReset(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9102, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"free usage exhausted; rolling 24-hour window"}}`)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusTooManyRequests,
		http.Header{"Retry-After": []string{"3600"}}, body)

	require.Zero(t, repo.tempUnschedCalls)
	require.WithinDuration(t, time.Now().Add(time.Hour), repo.lastRateLimitResetAt, 2*time.Second)
}

// TestHandleGrokProviderUpstreamError_EmptyOutputCoolsProvider supplies an
// empty-output failure under HTTP 502 for a Grok OAuth target. Health records
// one empty-output pause with a measured four-minute temporary deadline.
func TestHandleGrokProviderUpstreamError_EmptyOutputCoolsProvider(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9102, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), provider, http.StatusBadGateway, nil,
		[]byte(`empty model output: no content/tool_calls`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok empty model output", repo.lastTempUnschedReason)
	require.WithinDuration(t, before.Add(4*time.Minute), repo.lastTempUnschedUntil, time.Second)
}

// TestHandleGrokProviderUpstreamError_MultiAgentCapacityBlocksOnlyThatModel
// supplies engine_overloaded for a multi-agent team model. The specific model
// quota state is blocked while another Grok model and provider-wide temporary
// writes remain unaffected.
func TestHandleGrokProviderUpstreamError_MultiAgentCapacityBlocksOnlyThatModel(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9120, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	ctx := context.Background()

	handleGrokHealthWithTeamForTest(svc, "grok-4.20-multi-agent-0309",
		ctx, provider, http.StatusBadGateway, nil,
		[]byte(`{"error":{"message":"engine_overloaded"}}`),
	)

	require.Zero(t, repo.tempUnschedCalls)
	require.True(t, providercore.IsGrokModelQuotaBlocked(provider.Record.ID, "grok-4.20-multi-agent-0309", time.Now()))
	require.False(t, providercore.IsGrokModelQuotaBlocked(provider.Record.ID, "grok-4.5", time.Now()))
}

// TestHandleGrokProviderUpstreamError_CapacityNeverCoolsProvider reports model
// capacity under HTTP 429 for ordinary Grok text. The tested failure creates
// no provider temporary write or runtime block.
func TestHandleGrokProviderUpstreamError_CapacityNeverCoolsProvider(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9121, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	ctx := context.Background()

	handleGrokHealthWithTeamForTest(svc, "grok-4.6", ctx, provider, http.StatusTooManyRequests, nil,
		[]byte(`{"error":{"message":"The model is currently at capacity due to high demand"}}`))

	require.Zero(t, repo.tempUnschedCalls)
	require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
}

// TestHandleGrokProviderUpstreamError_FreeUsageDoesNotCoolPoolMode supplies
// Free-usage exhaustion under HTTP 400 to a pool API Key target. The tested
// path uses neither temporary pause persistence nor a runtime block.
func TestHandleGrokProviderUpstreamError_FreeUsageDoesNotCoolPoolMode(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9103,
			Platform: capability.PlatformGrok,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"pool_mode": true,
			},
		},
	}
	body := []byte(`{"error":{"code":"subscription:free-usage-exhausted","message":"free usage exhausted"}}`)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusBadRequest, nil, body)

	require.Zero(t, repo.tempUnschedCalls)
	require.False(t, svc.Runtime.Blocked(provider.Record.ID, func() string { return providercore.RefreshCredentialIdentity(provider.View()) }))
}

// TestHandleGrokProviderUpstreamError_ContentPolicyStillNoMutation reports a
// new_sensitive content-policy marker under Grok OAuth HTTP 403. The recorder
// observes zero temporary-unscheduling writes for that request-content
// failure.
func TestHandleGrokProviderUpstreamError_ContentPolicyStillNoMutation(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9104, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	body := []byte(`{"error":{"code":"new_sensitive","message":"text is sensitive"}}`)

	handleGrokHealthForTest(svc, context.Background(), provider, http.StatusForbidden, nil, body)

	require.Zero(t, repo.tempUnschedCalls)
}

// TestHandleGrokProviderUpstreamError_Entitlement403Unchanged reports an
// ordinary OAuth subscription-required 403. Health retains the entitlement
// reason and a temporary cooldown within the asserted thirty-minute interval.
func TestHandleGrokProviderUpstreamError_Entitlement403Unchanged(t *testing.T) {
	repo := &grokQuotaProviderRepo{}
	svc := newGrokHealthForTest(repo, nil)
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 9105, Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}}
	before := time.Now()

	handleGrokHealthForTest(svc,
		context.Background(), provider, http.StatusForbidden, nil,
		[]byte(`{"error":{"message":"subscription required"}}`),
	)

	require.Equal(t, 1, repo.tempUnschedCalls)
	require.Equal(t, "grok access or entitlement denied", repo.lastTempUnschedReason)
	require.Greater(t, repo.lastTempUnschedUntil, before.Add(29*time.Minute))
	require.Less(t, repo.lastTempUnschedUntil, before.Add(31*time.Minute))
}
