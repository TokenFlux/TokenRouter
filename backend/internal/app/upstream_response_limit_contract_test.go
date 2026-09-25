package app

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

func TestResolveUpstreamResponseReadLimit(t *testing.T) {
	t.Run("use default when config missing", func(t *testing.T) {
		require.Equal(t, config.DefaultUpstreamResponseReadMaxBytes, provideOpenAIResponseOutput(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).Options.ReadLimit)
	})

	t.Run("use configured value", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gateway.UpstreamResponseReadMaxBytes = 1234
		require.Equal(t, int64(1234), provideOpenAIResponseOutput(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).Options.ReadLimit)
	})
}
