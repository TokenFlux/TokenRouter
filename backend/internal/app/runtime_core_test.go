package app

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
)

// runtime_core_test.go verifies reusable shutdown barriers and timeout isolation
// across core runtime phases. Virtual-time fixtures hold background side effects
// at each producer stage to confirm that reusable waits serialize phases without
// sealing later admission and stop advancing dependencies when a barrier times out.

// TestProvideCoreRuntimeBarriersDrainEachPhase creates tracked side effects at
// every reusable wait. Each barrier must finish its current task before the
// next producer runs, while leaving task admission available for later phases.
func TestProvideCoreRuntimeBarriersDrainEachPhase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tasks := lifecycle.NewTasks()
		manager := lifecycle.New()
		provideCoreRuntime(nil, nil, nil, nil, nil, nil, nil, manager, nil, nil, nil, tasks, nil, nil)

		// Numeric expectations identify each shutdown wait independently of the
		// production constants, so changing a stage cannot move this fixture with it.
		barriers := []int{16, 21, 26, 31, 41, 46, 49, 51, 56, 61, 66}
		entered := make([]bool, len(barriers))
		releases := make([]chan struct{}, len(barriers))
		for i, phase := range barriers {
			releases[i] = make(chan struct{})
			manager.Register(lifecycle.Hook{
				Name:      fmt.Sprintf("producer-before-%d", phase),
				StopOrder: phase - 1,
				Stop: func(context.Context) error {
					// Hold each producer's tracked effect after its stop returns, so
					// the following barrier must await that work before advancing shutdown.
					if !tasks.Go(fmt.Sprintf("effect-before-%d", phase), func() { <-releases[i] }) {
						t.Error("reusable barrier closed task admission")
					}
					entered[i] = true
					return nil
				},
			})
		}

		// Stop runs separately while virtual-time waits expose each blocked
		// task group. Releasing one group permits exactly the next producer to run.
		result := make(chan error, 1)
		go func() { result <- manager.Stop(context.Background()) }()
		for i, phase := range barriers {
			synctest.Wait()
			require.True(t, entered[i], "producer before barrier %d", phase)
			for later := i + 1; later < len(barriers); later++ {
				require.False(t, entered[later], "barrier %d admitted later producer %d", phase, barriers[later])
			}
			close(releases[i])
		}
		require.NoError(t, <-result)
		require.True(t, tasks.Go("after-reusable-barriers", func() {}))
		require.NoError(t, tasks.Stop(context.Background()))
	})
}

// TestProvideCoreRuntimeBarrierTimeoutKeepsDependencies holds a producer's
// tracked effect beyond the shutdown deadline. The first producer barrier must
// report incomplete cleanup and leave later dependency callbacks uncalled.
func TestProvideCoreRuntimeBarrierTimeoutKeepsDependencies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tasks := lifecycle.NewTasks()
		manager := lifecycle.New()
		provideCoreRuntime(nil, nil, nil, nil, nil, nil, nil, manager, nil, nil, nil, tasks, nil, nil)
		release := make(chan struct{})
		defer close(release)
		manager.Register(lifecycle.Hook{
			Name:      "producer",
			StopOrder: 20,
			Stop: func(context.Context) error {
				// Keep the producer's effect alive after its stop callback returns.
				// The barrier owns the timed wait, while shared dependencies remain available.
				tasks.Go("pending-effect", func() { <-release })
				return nil
			},
		})
		dependencyStopped := false
		manager.Register(lifecycle.Hook{
			Name:      "dependency",
			StopOrder: 25,
			Stop: func(context.Context) error {
				dependencyStopped = true
				return nil
			},
		})

		// A one-second virtual budget expires while the tracked effect remains blocked.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err := manager.Stop(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Contains(t, err.Error(), "BackgroundBarrier21")
		require.Contains(t, err.Error(), "dependency")
		require.False(t, dependencyStopped)
		require.Equal(t, err, manager.Stop(context.Background()))
	})
}
