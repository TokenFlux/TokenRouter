package media

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

type embeddingPortsStub struct {
	outcomes                                                    []EmbeddingOutcome
	selected, acquired, released, reported, completed, switches int
	events                                                      []string
	bodies                                                      [][]byte
	exclusions                                                  []map[int64]struct{}
	gone, denyAcquire                                           bool
	selectErr                                                   error
}

func (p *embeddingPortsStub) SelectEmbedding(_ context.Context, excluded map[int64]struct{}) (account.AccountSnapshot, bool, error) {
	p.selected++
	copySet := make(map[int64]struct{}, len(excluded))
	for id := range excluded {
		copySet[id] = struct{}{}
	}
	p.exclusions = append(p.exclusions, copySet)
	if p.selectErr != nil {
		return account.AccountSnapshot{}, false, p.selectErr
	}
	return account.AccountSnapshot{ID: int64(p.selected)}, true, nil
}
func (p *embeddingPortsStub) AcquireEmbedding(context.Context, account.AccountSnapshot) (func(), bool) {
	if p.denyAcquire {
		return nil, false
	}
	p.acquired++
	return func() { p.released++; p.events = append(p.events, "release") }, true
}
func (p *embeddingPortsStub) ForwardEmbedding(_ context.Context, _ account.AccountSnapshot, body []byte) EmbeddingOutcome {
	p.bodies = append(p.bodies, append([]byte(nil), body...))
	p.events = append(p.events, "forward")
	return p.outcomes[p.selected-1]
}
func (p *embeddingPortsStub) ReportEmbedding(context.Context, account.AccountSnapshot, *EmbeddingResult, bool, error) {
	p.reported++
	p.events = append(p.events, "report")
}
func (p *embeddingPortsStub) CompleteEmbedding(context.Context, account.AccountSnapshot, *EmbeddingResult) {
	p.completed++
	p.events = append(p.events, "complete")
}
func (p *embeddingPortsStub) SwitchEmbedding(account.AccountSnapshot) { p.switches++ }
func (p *embeddingPortsStub) ObserveEmbedding(EmbeddingEvent)         {}
func (p *embeddingPortsStub) ClientGone() bool                        { return p.gone }

func TestEmbeddingsRetryReleasesBeforeFeedbackAndCompletesOnce(t *testing.T) {
	failure := errors.New("switch")
	ports := &embeddingPortsStub{outcomes: []EmbeddingOutcome{{Err: failure, Failover: true}, {Result: &EmbeddingResult{RequestID: "ok"}}}}
	body := []byte(`{"model":"alias","input":["a","b"]}`)
	require.Nil(t, RunEmbeddings(context.Background(), body, 1, ports))
	require.Equal(t, 2, ports.selected)
	require.Equal(t, 2, ports.released)
	require.Equal(t, 1, ports.completed)
	require.Equal(t, 1, ports.switches)
	require.Equal(t, []string{"forward", "release", "report", "forward", "release", "report", "complete"}, ports.events)
	require.Contains(t, ports.exclusions[1], int64(1))
	require.Equal(t, body, ports.bodies[0])
	require.Equal(t, body, ports.bodies[1])
}

func TestEmbeddingsOutputClosesRetryWindow(t *testing.T) {
	ports := &embeddingPortsStub{outcomes: []EmbeddingOutcome{{Err: errors.New("partial"), Failover: true, OutputChanged: true}}}
	failure := RunEmbeddings(context.Background(), nil, 3, ports)
	require.NotNil(t, failure)
	require.Equal(t, "exhausted", failure.Stage)
	require.True(t, failure.Outcome.OutputChanged)
	require.Equal(t, 1, ports.released)
	require.Zero(t, ports.reported)
	require.Zero(t, ports.completed)
	require.Zero(t, ports.switches)
}

func TestEmbeddingsWaitCancellationAndExhaustion(t *testing.T) {
	t.Run("wait", func(t *testing.T) {
		ports := &embeddingPortsStub{denyAcquire: true}
		require.Nil(t, RunEmbeddings(context.Background(), nil, 1, ports))
		require.Zero(t, ports.acquired)
		require.Empty(t, ports.bodies)
	})
	t.Run("canceled after failure", func(t *testing.T) {
		ports := &embeddingPortsStub{gone: true, outcomes: []EmbeddingOutcome{{Err: context.Canceled, Failover: true}}}
		require.Nil(t, RunEmbeddings(context.Background(), nil, 3, ports))
		require.Equal(t, 1, ports.released)
		require.Zero(t, ports.switches)
	})
	t.Run("limit", func(t *testing.T) {
		failure := EmbeddingOutcome{Err: errors.New("switch"), Failover: true}
		ports := &embeddingPortsStub{outcomes: []EmbeddingOutcome{failure, failure}}
		result := RunEmbeddings(context.Background(), nil, 1, ports)
		require.Equal(t, "exhausted", result.Stage)
		require.Equal(t, 2, ports.selected)
		require.Equal(t, 2, ports.released)
		require.Zero(t, ports.completed)
	})
}
