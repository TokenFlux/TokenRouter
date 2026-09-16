package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/stretchr/testify/require"
)

type generationStub struct {
	selected, activated, acquired, released, completed, switched, keepaliveStopped int
	outcomes                                                                       []GenerationOutcome
	reports                                                                        []bool
	terminal                                                                       *GenerationFailure
	events                                                                         []string
	rejected, gone                                                                 bool
}

func (p *generationStub) SelectGeneration(context.Context, map[int64]struct{}) (GenerationSelection, bool, error) {
	p.selected++
	return GenerationSelection{Account: account.AccountSnapshot{ID: 1}, RetryLimit: 1}, true, nil
}
func (p *generationStub) ActivateGeneration(GenerationSelection) { p.activated++ }
func (p *generationStub) GenerationEligible(context.Context, GenerationSelection) (bool, string, error) {
	return !p.rejected, "denied", nil
}
func (p *generationStub) AcquireGeneration(context.Context, GenerationSelection) (func(), bool) {
	p.acquired++
	return func() { p.released++; p.events = append(p.events, "release") }, true
}
func (p *generationStub) StartGenerationKeepalive() func() {
	return func() { p.keepaliveStopped++; p.events = append(p.events, "keepalive_stop") }
}
func (p *generationStub) ForwardGeneration(context.Context, GenerationSelection, []byte) GenerationOutcome {
	return p.outcomes[p.selected-1]
}
func (p *generationStub) ReportGeneration(_ context.Context, _ GenerationSelection, _ *GenerationResult, success bool, _ error) {
	p.reports = append(p.reports, success)
	p.events = append(p.events, "report")
}
func (p *generationStub) CompleteGeneration(context.Context, GenerationSelection, *GenerationResult) {
	p.completed++
	p.events = append(p.events, "complete")
}
func (p *generationStub) SwitchGeneration(GenerationSelection)                 { p.switched++ }
func (p *generationStub) StopGeneration429(GenerationSelection, int, int) bool { return false }
func (p *generationStub) GenerationClientGone() bool                           { return p.gone }
func (p *generationStub) ObserveGeneration(GenerationEvent)                    {}
func (p *generationStub) EndGeneration(f GenerationFailure) {
	p.terminal = &f
	p.events = append(p.events, "end")
}
func TestImagesLoopPartialOutputCompletesAndStopsKeepalive(t *testing.T) {
	p := &generationStub{outcomes: []GenerationOutcome{{Result: &GenerationResult{ImageCount: 2}, Err: errors.New("tail failure")}}}
	RunImages(context.Background(), GenerationRequest{RoutingStarted: time.Now()}, p)
	require.Equal(t, 1, p.released)
	require.Equal(t, 1, p.completed)
	require.Empty(t, p.reports)
	require.Nil(t, p.terminal)
	require.Equal(t, []string{"release", "complete", "keepalive_stop"}, p.events)
}
func TestImagesLoopStopsSwitchingAfterOutput(t *testing.T) {
	p := &generationStub{outcomes: []GenerationOutcome{{Err: errors.New("upstream"), Failure: &failover.FailureInfo{RetryNext: true}, OutputChanged: true}}}
	RunImages(context.Background(), GenerationRequest{MaxSwitches: 3, RoutingStarted: time.Now()}, p)
	require.Equal(t, 1, p.selected)
	require.Equal(t, []bool{false}, p.reports)
	require.Equal(t, "exhausted", p.terminal.Stage)
	require.Equal(t, []string{"release", "report", "end", "keepalive_stop"}, p.events)
}
func TestImagesLoopPreservesUserErrorAndSameAccountRetry(t *testing.T) {
	p := &generationStub{outcomes: []GenerationOutcome{{Err: errors.New("bad prompt"), ImageError: true}}}
	RunImages(context.Background(), GenerationRequest{Stream: true, RoutingStarted: time.Now()}, p)
	require.Equal(t, []bool{true}, p.reports)
	require.Zero(t, p.completed)
	require.Zero(t, p.switched)
	p = &generationStub{outcomes: []GenerationOutcome{{Err: errors.New("retry"), Failure: &failover.FailureInfo{RetryableOnSameAccount: true, RetryNext: true, SameAccountRetryDelay: time.Millisecond}}, {Result: &GenerationResult{ImageCount: 1}}}}
	RunImages(context.Background(), GenerationRequest{MaxSwitches: 1, RoutingStarted: time.Now()}, p)
	require.Equal(t, 2, p.released)
	require.Equal(t, 1, p.completed)
	require.Zero(t, p.switched)
}
func TestGrokLoopBoundLookupDoesNotSwitchOrProbeEligibility(t *testing.T) {
	p := &generationStub{}
	RunGrokMedia(context.Background(), GenerationRequest{VideoLookup: true, BoundAccountID: 8, RoutingStarted: time.Now()}, p)
	require.Equal(t, "bound_unavailable", p.terminal.Stage)
	require.Zero(t, p.activated)
	require.Zero(t, p.acquired)
	p = &generationStub{outcomes: []GenerationOutcome{{Err: errors.New("lookup"), Failure: &failover.FailureInfo{RetryNext: true}, ReportFailure: false}}}
	RunGrokMedia(context.Background(), GenerationRequest{VideoLookup: true, BoundAccountID: 1, RoutingStarted: time.Now()}, p)
	require.Equal(t, 1, p.released)
	require.Empty(t, p.reports)
	require.Zero(t, p.switched)
	require.Equal(t, "exhausted", p.terminal.Stage)
}
func TestGrokLoopEligibilityAndCancellationBudgets(t *testing.T) {
	p := &generationStub{rejected: true}
	RunGrokMedia(context.Background(), GenerationRequest{Generation: true, MaxSwitches: 1, RoutingStarted: time.Now()}, p)
	require.Equal(t, 2, p.selected)
	require.Zero(t, p.acquired)
	require.Equal(t, "ineligible", p.terminal.Stage)
	p = &generationStub{gone: true}
	RunGrokMedia(context.Background(), GenerationRequest{Generation: true}, p)
	require.Zero(t, p.selected)
	require.Nil(t, p.terminal)
}

func TestGenerationSnapshotIsolatesMutableObservedFields(t *testing.T) {
	first := 7
	original := &GenerationResult{FirstTokenMs: &first, ImageOutputSizes: []string{"1024x1024"}, ImageSizeBreakdown: map[string]int{"1K": 1}, Headers: map[string][]string{"X-Request-Id": {"old"}}, ResponseHeaders: map[string][]string{"Empty": {}}}
	snapshot := CloneGenerationResult(original)
	first = 99
	original.ImageOutputSizes[0] = "changed"
	original.ImageSizeBreakdown["1K"] = 4
	original.Headers["X-Request-Id"][0] = "changed"
	require.Equal(t, 7, *snapshot.FirstTokenMs)
	require.Equal(t, "1024x1024", snapshot.ImageOutputSizes[0])
	require.Equal(t, 1, snapshot.ImageSizeBreakdown["1K"])
	require.Equal(t, "old", snapshot.Headers["X-Request-Id"][0])
	require.NotNil(t, snapshot.ResponseHeaders["Empty"])
}
