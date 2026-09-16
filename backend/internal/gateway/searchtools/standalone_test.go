package searchtools

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
	"github.com/stretchr/testify/require"
)

type standaloneStub struct {
	selected       int
	events         []string
	failures       int
	selectionError bool
	acquireError   bool
}

func (s *standaloneStub) Select(_ context.Context, _ string, excluded map[int64]struct{}) (Selection, bool, error) {
	s.selected++
	s.events = append(s.events, "select")
	if s.selectionError {
		return Selection{}, false, errors.New("selection failed")
	}
	if s.selected > 1 {
		if _, ok := excluded[int64(s.selected-1)]; !ok {
			panic("previous account not excluded")
		}
	}
	return Selection{AccountID: int64(s.selected)}, true, nil
}
func (s *standaloneStub) Acquire(context.Context, Selection) (func(), bool, error) {
	if s.acquireError {
		return nil, false, errors.New("slot failed")
	}
	s.events = append(s.events, "acquire")
	return func() { s.events = append(s.events, "release") }, true, nil
}
func (s *standaloneStub) Execute(context.Context, int64, StandaloneRequest, string, int) (*contract.SearchResponse, string, error) {
	s.events = append(s.events, "execute")
	if s.selected <= s.failures {
		return nil, "", errors.New("upstream failed")
	}
	return &contract.SearchResponse{}, "grok-native", nil
}
func (s *standaloneStub) CanSwitch(error) bool { return true }
func TestStandaloneLeaseSpansResponseAndReleasesBeforeSwitch(t *testing.T) {
	ports := &standaloneStub{failures: 1}
	lease := scheduler.NewLease(context.Background(), scheduler.ReleaseOnCompletion)
	result, err := RunStandalone(context.Background(), StandaloneRequest{Query: "q"}, "model", 5, ports, lease)
	require.NoError(t, err)
	require.Equal(t, int64(2), result.AccountID)
	require.Equal(t, []string{"select", "acquire", "execute", "release", "select", "acquire", "execute"}, ports.events)
	lease.Release()
	lease.Release()
	require.Equal(t, "release", ports.events[len(ports.events)-1])
	require.Len(t, ports.events, 8)
}
func TestStandaloneFourAttemptsAndFirstFailureKinds(t *testing.T) {
	t.Run("four attempts", func(t *testing.T) {
		p := &standaloneStub{failures: 4}
		lease := scheduler.NewLease(context.Background(), scheduler.ReleaseOnCompletion)
		defer lease.Release()
		_, err := RunStandalone(context.Background(), StandaloneRequest{}, "model", 5, p, lease)
		require.Error(t, err)
		require.Equal(t, 4, p.selected)
	})
	for _, stage := range []string{"selection", "concurrency"} {
		t.Run(stage, func(t *testing.T) {
			p := &standaloneStub{selectionError: stage == "selection", acquireError: stage == "concurrency"}
			lease := scheduler.NewLease(context.Background(), scheduler.ReleaseOnCompletion)
			defer lease.Release()
			_, err := RunStandalone(context.Background(), StandaloneRequest{}, "model", 5, p, lease)
			var failure *StandaloneFailure
			require.ErrorAs(t, err, &failure)
			require.Equal(t, stage, failure.Stage)
			require.Equal(t, 1, p.selected)
		})
	}
}
