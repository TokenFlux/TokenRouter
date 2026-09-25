//go:build unit

package creative_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/stretchr/testify/require"
)

// 规划夹具仅放在仓库外，通过 overlay 验证原实现。
func TestS13StoppedRuntimesRejectStart(t *testing.T) {
	t.Run("creative", func(t *testing.T) {
		q := &parallelCreativeQueue{ready: make(chan string)}
		w := newCreativeWorkerFixtureForSources(q, nil, nil, nil, nil, creative.CreativeWorkerOptions{})
		r := newCreativeRuntimeFixture(w, nil, &config.Config{Creative: config.CreativeConfig{QueueEnabled: true}})
		r.SetWorkerCount(1)
		r.Stop()
		r.Start()
		defer r.Stop()
		require.False(t, r.Running(), "停止后的创作台 runtime 不应重开")
	})

}
