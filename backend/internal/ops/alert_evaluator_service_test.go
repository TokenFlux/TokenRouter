package ops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// alert_evaluator_service_test.go checks group percentages, provider predicates,
// and rule metric selection against supplied observations. Availability cases
// distinguish query failures from usable empty results, check required group
// selectors, and count reads across repeated rule evaluations with mixed
// provider states and temporary-unschedulable deadlines.

// The interface assertion checks the dashboard stub against the narrowed
// evaluator repository port rather than the full OpsRepository surface.
var _ AlertEvaluatorRepository = (*stubOpsRepo)(nil)

// stubOpsRepo supplies dashboard results while other evaluator repository
// methods remain promoted from its embedded interface.
type stubOpsRepo struct {
	// AlertEvaluatorRepository holds promoted methods that these selected metric
	// fixtures leave nil. Tests keep their calls on the overridden or
	// independent metric paths.
	AlertEvaluatorRepository
	// overview is returned by reference when present. Consumers share the
	// fixture pointer and coordinate any mutations while parallel readers
	// use it.
	// overview and err select the dashboard response: err takes precedence,
	// overview is returned when supplied, and absence of both allocates a
	// new empty result.
	overview *OpsDashboardOverview // supplied dashboard result
	err      error                 // forced dashboard read failure
}

// GetDashboardOverview returns the configured error before inspecting its
// supplied overview pointer. Context and filter are ignored; a present overview
// is shared, while an absent overview allocates a new empty value.
func (s *stubOpsRepo) GetDashboardOverview(ctx context.Context, filter *OpsDashboardFilter) (*OpsDashboardOverview, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.overview != nil {
		return s.overview, nil
	}
	return &OpsDashboardOverview{}, nil
}

// TestComputeGroupAvailableRatio supplies normal, zero-total, and
// zero-available group counts in parallel cases. It checks eighty percent for
// eight of ten and zero for the two supplied empty-count cases.
func TestComputeGroupAvailableRatio(t *testing.T) {
	t.Parallel()

	t.Run("正常情况: 10个提供商, 8个可用 = 80%", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalProviders: 10,
			AvailableCount: 8,
		})
		// Allow a small absolute tolerance for percentage arithmetic.
		require.InDelta(t, 80.0, got, 0.0001)
	})

	t.Run("边界情况: TotalProviders = 0 应返回 0", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalProviders: 0,
			AvailableCount: 8,
		})
		require.Equal(t, 0.0, got)
	})

	t.Run("边界情况: AvailableCount = 0 应返回 0%", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalProviders: 10,
			AvailableCount: 0,
		})
		require.Equal(t, 0.0, got)
	})
}

// TestCountProvidersByCondition supplies rate-limited providers, error
// providers with a temporary exclusion, and an empty map. Parallel cases check
// each selected predicate's count against its supplied fixture.
func TestCountProvidersByCondition(t *testing.T) {
	t.Parallel()

	t.Run("测试限流提供商统计: acc.IsRateLimited", func(t *testing.T) {
		t.Parallel()

		providers := map[int64]*ProviderAvailability{
			1: {IsRateLimited: true},
			2: {IsRateLimited: false},
			3: {IsRateLimited: true},
		}

		got := countProvidersByCondition(providers, func(acc *ProviderAvailability) bool {
			return acc.IsRateLimited
		})
		require.Equal(t, int64(2), got)
	})

	t.Run("测试错误提供商统计（排除临时不可调度）: acc.HasError && acc.TempUnschedulableUntil == nil", func(t *testing.T) {
		t.Parallel()

		until := time.Now().UTC().Add(5 * time.Minute)
		providers := map[int64]*ProviderAvailability{
			1: {HasError: true},
			2: {HasError: true, TempUnschedulableUntil: &until},
			3: {HasError: false},
		}

		got := countProvidersByCondition(providers, func(acc *ProviderAvailability) bool {
			return acc.HasError && acc.TempUnschedulableUntil == nil
		})
		require.Equal(t, int64(1), got)
	})

	t.Run("边界情况: 空 map 应返回 0", func(t *testing.T) {
		t.Parallel()

		got := countProvidersByCondition(map[int64]*ProviderAvailability{}, func(acc *ProviderAvailability) bool {
			return acc.IsRateLimited
		})
		require.Equal(t, int64(0), got)
	})
}

// TestComputeRuleMetricNewIndicators supplies disk and all seven availability
// indicators with mixed provider states. Each availability rule reads once,
// including repeated evaluations, while invalid group rules make no query.
func TestComputeRuleMetricNewIndicators(t *testing.T) {
	t.Parallel()

	groupID := int64(101)
	platform := "openai"

	availability := &OpsProviderAvailability{
		Group: &GroupAvailability{
			GroupID:        groupID,
			TotalProviders: 10,
			AvailableCount: 8,
			RateLimitCount: 3,
		},
		Providers: map[int64]*ProviderAvailability{
			1: {IsRateLimited: true},
			2: {IsRateLimited: true},
			3: {HasError: true},
			4: {HasError: true, TempUnschedulableUntil: timePtr(time.Now().UTC().Add(2 * time.Minute))},
			5: {HasError: false, IsRateLimited: false},
			6: nil,
			7: {HasError: true, TempUnschedulableUntil: timePtr(time.Now().UTC().Add(-time.Minute))},
			8: {IsOverloaded: true},
		},
	}

	start := time.Now().UTC().Add(-5 * time.Minute)
	end := time.Now().UTC()
	ctx := context.Background()

	tests := []struct {
		name       string // subtest display name
		metricType string // selected indicator kind
		// system supplies sample-based indicators and is read by
		// computeRuleMetric. Parallel cases keep the pointed fixture
		// values unchanged throughout their evaluation.
		system *OpsSystemMetricsSnapshot // system sample pointer
		// groupID is passed unchanged to metric selection. Nil rejects group
		// indicators and leaves provider indicators unfiltered; supplied group
		// selectors use the positive fixture identity.
		groupID *int64 // optional group selector
		// wantValue and wantOK describe the observation together. Unavailable
		// observations return zero with false, while usable zero values still
		// participate in the caller's threshold comparison.
		wantValue float64 // expected usable value
		wantOK    bool    // expected usable flag
		wantCalls int     // reads per evaluation
	}{
		{
			name:       "disk_usage_percent",
			metricType: "disk_usage_percent",
			system: &OpsSystemMetricsSnapshot{
				DiskUsagePercent: float64Ptr(92.5),
			},
			groupID:   nil,
			wantValue: 92.5,
			wantOK:    true,
		},
		{
			name:       "group_available_providers",
			metricType: "group_available_providers",
			groupID:    &groupID,
			wantValue:  8,
			wantOK:     true,
			wantCalls:  1,
		},
		{
			name:       "group_available_ratio",
			metricType: "group_available_ratio",
			groupID:    &groupID,
			wantValue:  80.0,
			wantOK:     true,
			wantCalls:  1,
		},
		{
			name:       "provider_rate_limited_count",
			metricType: "provider_rate_limited_count",
			groupID:    nil,
			wantValue:  2,
			wantOK:     true,
			wantCalls:  1,
		},
		{
			name:       "provider_error_count",
			metricType: "provider_error_count",
			groupID:    nil,
			wantValue:  1,
			wantOK:     true,
			wantCalls:  1,
		},
		{
			name:       "group_rate_limit_ratio",
			metricType: "group_rate_limit_ratio",
			groupID:    &groupID,
			wantValue:  30,
			wantOK:     true,
			wantCalls:  1,
		},
		{
			name:       "provider_error_ratio",
			metricType: "provider_error_ratio",
			wantValue:  12.5,
			wantOK:     true,
			wantCalls:  1,
		},
		{
			name:       "overload_provider_count",
			metricType: "overload_provider_count",
			wantValue:  1,
			wantOK:     true,
			wantCalls:  1,
		},
		{
			name:       "group_available_providers without group_id returns false",
			metricType: "group_available_providers",
			groupID:    nil,
			wantValue:  0,
			wantOK:     false,
		},
		{
			name:       "group_rate_limit_ratio without group_id returns false",
			metricType: "group_rate_limit_ratio",
			wantOK:     false,
		},
		{
			name:       "group_available_ratio without group_id returns false",
			metricType: "group_available_ratio",
			groupID:    nil,
			wantValue:  0,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			svc := &OpsAlertEvaluatorService{
				opsService: &OpsService{
					getProviderAvailability: func(gotCtx context.Context, gotPlatform string, gotGroupID *int64) (*OpsProviderAvailability, error) {
						calls++
						require.Equal(t, ctx, gotCtx)
						require.Equal(t, platform, gotPlatform)
						require.Same(t, tt.groupID, gotGroupID)
						return availability, nil
					},
				},
			}
			for evaluation := 1; evaluation <= 2; evaluation++ {
				rule := &OpsAlertRule{ID: int64(evaluation), MetricType: " " + tt.metricType + " "}
				gotValue, gotOK := svc.computeRuleMetric(ctx, rule, tt.system, start, end, platform, tt.groupID)
				require.Equal(t, tt.wantOK, gotOK)
				require.Equal(t, evaluation*tt.wantCalls, calls)
				// Use a small absolute tolerance for returned measurements.
				require.InDelta(t, tt.wantValue, gotValue, 0.0001)
			}
		})
	}
}

// TestComputeRuleMetricAvailabilityInputs distinguishes unavailable reads from
// usable empty observations for every availability indicator. It also checks
// group validation before queries and dependency failures before any read.
func TestComputeRuleMetricAvailabilityInputs(t *testing.T) {
	t.Parallel()

	groupID := int64(101)
	zeroGroupID := int64(0)
	negativeGroupID := int64(-1)
	queryErr := errors.New("availability unavailable")
	metrics := []string{
		"group_available_providers",
		"group_available_ratio",
		"provider_rate_limited_count",
		"provider_error_count",
		"group_rate_limit_ratio",
		"provider_error_ratio",
		"overload_provider_count",
	}
	for _, metric := range metrics {
		t.Run(metric, func(t *testing.T) {
			t.Parallel()

			requiresGroup := metric == "group_available_providers" || metric == "group_available_ratio" || metric == "group_rate_limit_ratio"
			tests := []struct {
				name    string // subtest display name
				groupID *int64 // supplied group selector
				// availability and queryErr are returned together by the injected
				// query. A nonnil error makes the observation unusable even when
				// availability contains a result.
				availability *OpsProviderAvailability // supplied query result
				queryErr     error                    // supplied query failure
				// missingEvaluator and missingService select absent dependencies.
				// The evaluator is omitted first; otherwise the service is bound
				// unless missingService requests the nil-service path.
				missingEvaluator bool // omit evaluator instance
				missingService   bool // omit availability service
				wantOK           bool // expected usable result
				wantCalls        int  // expected availability reads
			}{
				{
					name:             "nil evaluator",
					groupID:          &groupID,
					missingEvaluator: true,
				},
				{
					name:           "nil service",
					groupID:        &groupID,
					missingService: true,
				},
				{
					name:      "query error",
					groupID:   &groupID,
					queryErr:  queryErr,
					wantCalls: 1,
				},
				{
					name:         "query error with data",
					groupID:      &groupID,
					availability: &OpsProviderAvailability{},
					queryErr:     queryErr,
					wantCalls:    1,
				},
				{
					name:      "nil observation",
					groupID:   &groupID,
					wantCalls: 1,
				},
				{
					name:         "nil group and providers",
					groupID:      &groupID,
					availability: &OpsProviderAvailability{},
					wantOK:       true,
					wantCalls:    1,
				},
				{
					name:    "empty group and providers",
					groupID: &groupID,
					availability: &OpsProviderAvailability{
						Group:     &GroupAvailability{},
						Providers: map[int64]*ProviderAvailability{},
					},
					wantOK:    true,
					wantCalls: 1,
				},
				{
					name:    "negative group total",
					groupID: &groupID,
					availability: &OpsProviderAvailability{
						Group: &GroupAvailability{TotalProviders: -1},
					},
					wantOK:    true,
					wantCalls: 1,
				},
				{
					name:    "nil provider entry",
					groupID: &groupID,
					availability: &OpsProviderAvailability{
						Providers: map[int64]*ProviderAvailability{1: nil},
					},
					wantOK:    true,
					wantCalls: 1,
				},
				{
					name:         "missing group selector",
					availability: &OpsProviderAvailability{},
					wantOK:       !requiresGroup,
					wantCalls:    1,
				},
				{
					name:         "zero group selector",
					groupID:      &zeroGroupID,
					availability: &OpsProviderAvailability{},
					wantOK:       !requiresGroup,
					wantCalls:    1,
				},
				{
					name:         "negative group selector",
					groupID:      &negativeGroupID,
					availability: &OpsProviderAvailability{},
					wantOK:       !requiresGroup,
					wantCalls:    1,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					calls := 0
					svc := &OpsAlertEvaluatorService{}
					if tt.missingEvaluator {
						svc = nil
					} else if !tt.missingService {
						svc.opsService = &OpsService{
							getProviderAvailability: func(_ context.Context, _ string, gotGroupID *int64) (*OpsProviderAvailability, error) {
								calls++
								require.Same(t, tt.groupID, gotGroupID)
								return tt.availability, tt.queryErr
							},
						}
					}
					rule := &OpsAlertRule{MetricType: metric}
					gotValue, gotOK := svc.computeRuleMetric(context.Background(), rule, nil, time.Time{}, time.Time{}, "openai", tt.groupID)
					require.Equal(t, tt.wantOK, gotOK)
					require.Zero(t, gotValue)
					wantCalls := tt.wantCalls
					if requiresGroup && (tt.groupID == nil || *tt.groupID <= 0) {
						wantCalls = 0
					}
					require.Equal(t, wantCalls, calls)
				})
			}
		})
	}
}

// TestComputeRuleMetricDirectEvaluatorRepository evaluates dashboard ratio
// rules through AlertEvaluatorRepository when OpsService is absent. It verifies
// scaled percentage values and rejects invalid windows without calling
// unrelated system-metric or heartbeat methods on the stub.
func TestComputeRuleMetricDirectEvaluatorRepository(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 15, 11, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	svc := &OpsAlertEvaluatorService{
		opsRepo: &stubOpsRepo{
			overview: &OpsDashboardOverview{
				RequestCountSLA:   100,
				SLA:               0.985,
				ErrorRate:         0.015,
				UpstreamErrorRate: 0.005,
			},
		},
	}

	val, ok := svc.computeRuleMetric(context.Background(), &OpsAlertRule{MetricType: "success_rate"}, nil, start, end, "openai", nil)
	require.True(t, ok)
	require.InDelta(t, 98.5, val, 0.0001)

	val, ok = svc.computeRuleMetric(context.Background(), &OpsAlertRule{MetricType: "error_rate"}, nil, start, end, "openai", nil)
	require.True(t, ok)
	require.InDelta(t, 1.5, val, 0.0001)

	val, ok = svc.computeRuleMetric(context.Background(), &OpsAlertRule{MetricType: "upstream_error_rate"}, nil, end, start, "openai", nil)
	require.False(t, ok)
	require.Zero(t, val)
}
