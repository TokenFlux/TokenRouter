//go:build unit

package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

// bmRepoStub 仅提供准入开关读取，并记录原回源次数。
type bmRepoStub struct {
	getValueFn func(ctx context.Context, key string) (string, error)
	calls      int
}

func (s *bmRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	s.calls++
	if s.getValueFn == nil {
		panic("unexpected GetValue call")
	}
	return s.getValueFn(ctx, key)
}

func TestIsBackendModeEnabled_ReturnsTrue(t *testing.T) {

	repo := &bmRepoStub{
		getValueFn: func(ctx context.Context, key string) (string, error) {
			require.Equal(t, BackendModeKey, key)
			return "true", nil
		},
	}
	svc := NewBackendMode(repo, nil)

	require.True(t, svc.IsBackendModeEnabled(context.Background()))
	require.Equal(t, 1, repo.calls)
}

func TestIsBackendModeEnabled_ReturnsFalse(t *testing.T) {

	repo := &bmRepoStub{
		getValueFn: func(ctx context.Context, key string) (string, error) {
			require.Equal(t, BackendModeKey, key)
			return "false", nil
		},
	}
	svc := NewBackendMode(repo, nil)

	require.False(t, svc.IsBackendModeEnabled(context.Background()))
	require.Equal(t, 1, repo.calls)
}

func TestIsBackendModeEnabled_ReturnsFalseOnNotFound(t *testing.T) {

	repo := &bmRepoStub{
		getValueFn: func(ctx context.Context, key string) (string, error) {
			require.Equal(t, BackendModeKey, key)
			return "", settings.ErrSettingNotFound
		},
	}
	svc := NewBackendMode(repo, nil)

	require.False(t, svc.IsBackendModeEnabled(context.Background()))
	require.Equal(t, 1, repo.calls)
}

func TestIsBackendModeEnabled_ReturnsFalseOnDBError(t *testing.T) {

	repo := &bmRepoStub{
		getValueFn: func(ctx context.Context, key string) (string, error) {
			require.Equal(t, BackendModeKey, key)
			return "", errors.New("db down")
		},
	}
	svc := NewBackendMode(repo, nil)

	require.False(t, svc.IsBackendModeEnabled(context.Background()))
	require.Equal(t, 1, repo.calls)
}

func TestIsBackendModeEnabled_CachesResult(t *testing.T) {

	repo := &bmRepoStub{
		getValueFn: func(ctx context.Context, key string) (string, error) {
			require.Equal(t, BackendModeKey, key)
			return "true", nil
		},
	}
	svc := NewBackendMode(repo, nil)

	require.True(t, svc.IsBackendModeEnabled(context.Background()))
	require.True(t, svc.IsBackendModeEnabled(context.Background()))
	require.Equal(t, 1, repo.calls)
}
