//go:build unit

package settings_test

import (
	"context"
	"maps"
	"testing"

	settingskit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/stretchr/testify/require"
)

type bmUpdateRepoStub struct {
	updates    map[string]string
	getValueFn func(ctx context.Context, key string) (string, error)
}

func (s *bmUpdateRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *bmUpdateRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.getValueFn == nil {
		panic("unexpected GetValue call")
	}
	return s.getValueFn(ctx, key)
}

func (s *bmUpdateRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *bmUpdateRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *bmUpdateRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	s.updates = make(map[string]string, len(settings))
	for k, v := range settings {
		s.updates[k] = v
	}
	return nil
}

func (s *bmUpdateRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	return maps.Clone(s.updates), nil
}

func (s *bmUpdateRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestUpdateSettings_InvalidatesBackendModeCache(t *testing.T) {

	repo := &bmUpdateRepoStub{
		getValueFn: func(ctx context.Context, key string) (string, error) {
			require.Equal(t, gateway.SettingKeyBackendModeEnabled, key)
			return "true", nil
		},
	}
	svc := settingskit.NewComposite(repo, &config.Config{})
	svc.Backend.Publish(true)

	err := svc.Save(context.Background(), &composite.Snapshot{
		BackendModeEnabled: false,
	})
	require.NoError(t, err)
	require.Equal(t, "false", repo.updates[gateway.SettingKeyBackendModeEnabled])
	require.False(t, svc.Backend.Enabled(context.Background()))
}
