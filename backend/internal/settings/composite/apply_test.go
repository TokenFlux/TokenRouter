package composite

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplicationsUseOneCommittedSnapshot 保证全部应用读取同一次持久化回读，不使用原请求零值。
func TestApplicationsUseOneCommittedSnapshot(t *testing.T) {
	reads := 0
	var order []string
	value := &Snapshot{SiteName: "persisted", CreativeWorkerCount: 7}
	changes := ApplicationsForUpdate(func(context.Context) (*Snapshot, error) { reads++; return value, nil }, []Application{
		{Module: "gateway", Apply: func(_ context.Context, s *Snapshot) error {
			require.Same(t, value, s)
			order = append(order, "gateway")
			return nil
		}},
		{Module: "site", Apply: func(_ context.Context, s *Snapshot) error {
			require.Equal(t, "persisted", s.SiteName)
			order = append(order, "site")
			return nil
		}},
		{Module: "creative", Apply: func(_ context.Context, s *Snapshot) error {
			require.Equal(t, 7, s.CreativeWorkerCount)
			order = append(order, "creative")
			return nil
		}},
	})
	for _, change := range changes {
		require.NoError(t, change.Apply(context.Background()))
	}
	require.Equal(t, 1, reads)
	require.Equal(t, []string{"gateway", "site", "creative"}, order)
}

// TestApplicationsDoNotPublishAfterReadFailure 保持提交后读取失败不会发布零值；下一次更新不继承上次快照。
func TestApplicationsDoNotPublishAfterReadFailure(t *testing.T) {
	failed := errors.New("read failed")
	applied := false
	changes := ApplicationsForUpdate(func(context.Context) (*Snapshot, error) { return nil, failed }, []Application{{Module: "site", Apply: func(context.Context, *Snapshot) error { applied = true; return nil }}})
	require.ErrorIs(t, changes[0].Apply(context.Background()), failed)
	require.NoError(t, changes[1].Apply(context.Background()))
	require.False(t, applied)
}
