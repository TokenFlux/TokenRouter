// 聚合状态按写入职责更新，避免手工回填与实时水位互相覆盖。
package usage

import (
	"context"
	"errors"
	"time"
)

// AnalyticsChangeKind 是现有聚合状态的闭合更新种类。
type AnalyticsChangeKind uint8

const (
	AnalyticsManualRequest AnalyticsChangeKind = iota + 1
	AnalyticsRunStarted
	AnalyticsLiveSuccess
	AnalyticsRunFailed
	AnalyticsAutomaticProgress
	AnalyticsManualProgress
)

// AnalyticsStateChange 只传递一次状态更新的意图，不暴露数据库事务。
type AnalyticsStateChange struct {
	Kind                                      AnalyticsChangeKind
	State                                     UsageAnalyticsAggregationState
	ExpectedManualStart, ExpectedManualCursor *time.Time
}

var ErrAnalyticsRequestSuperseded = errors.New("manual backfill request superseded")

// ApplyAnalyticsStateChange 在存储锁定的最新行上应用本次拥有的字段。
// @project-doc docs/operations/pre_aggregation.md#usage_state_ownership
func ApplyAnalyticsStateChange(current UsageAnalyticsAggregationState, change AnalyticsStateChange) (UsageAnalyticsAggregationState, error) {
	next := current
	desired := change.State
	setOutcome := func() {
		next.Phase = desired.Phase
		next.LastSuccessAt = desired.LastSuccessAt
		next.LastError = desired.LastError
		next.LastErrorAt = desired.LastErrorAt
		next.LastDurationMS = desired.LastDurationMS
	}
	switch change.Kind {
	case AnalyticsManualRequest:
		next.ManualBackfillStart = desired.ManualBackfillStart
		next.ManualBackfillCursor = desired.ManualBackfillCursor
		next.Phase = "backfill"
	case AnalyticsRunStarted:
		next.Phase = desired.Phase
		next.LastRunAt = desired.LastRunAt
	case AnalyticsLiveSuccess:
		next.LiveWatermark = desired.LiveWatermark
		if next.CoverageStart == nil {
			next.CoverageStart = desired.CoverageStart
			next.BackfillCursor = desired.BackfillCursor
		}
		setOutcome()
	case AnalyticsRunFailed:
		next.Phase = desired.Phase
		next.LastError = desired.LastError
		next.LastErrorAt = desired.LastErrorAt
		next.LastDurationMS = desired.LastDurationMS
	case AnalyticsAutomaticProgress:
		next.CoverageStart = desired.CoverageStart
		next.BackfillCursor = desired.BackfillCursor
		next.SourceOldestAt = desired.SourceOldestAt
		setOutcome()
	case AnalyticsManualProgress:
		if !sameAnalyticsTime(current.ManualBackfillStart, change.ExpectedManualStart) || !sameAnalyticsTime(current.ManualBackfillCursor, change.ExpectedManualCursor) {
			return current, ErrAnalyticsRequestSuperseded
		}
		next.ManualBackfillStart = desired.ManualBackfillStart
		next.ManualBackfillCursor = desired.ManualBackfillCursor
		next.CoverageStart = desired.CoverageStart
		next.BackfillCursor = desired.BackfillCursor
		setOutcome()
	default:
		return current, errors.New("unknown analytics state change")
	}
	return next, nil
}
func sameAnalyticsTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (s *DashboardAggregationService) readAnalyticsState(ctx context.Context) (*UsageAnalyticsAggregationState, error) {
	state, err := s.analyticsRepo.GetUsageAnalyticsAggregationState(ctx)
	if err == nil && state != nil {
		state.observedManualStart = state.ManualBackfillStart
		state.observedManualCursor = state.ManualBackfillCursor
	}
	return state, err
}
func (s *DashboardAggregationService) updateAnalyticsState(ctx context.Context, state *UsageAnalyticsAggregationState, kind AnalyticsChangeKind) error {
	next, err := s.analyticsRepo.ApplyUsageAnalyticsState(ctx, AnalyticsStateChange{Kind: kind, State: *state, ExpectedManualStart: state.observedManualStart, ExpectedManualCursor: state.observedManualCursor})
	if err == nil && next != nil {
		*state = *next
		state.observedManualStart = state.ManualBackfillStart
		state.observedManualCursor = state.ManualBackfillCursor
	}
	return err
}
