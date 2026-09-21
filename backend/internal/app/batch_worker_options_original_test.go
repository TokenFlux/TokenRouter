//go:build unit

package app

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"

	"github.com/stretchr/testify/require"
)

func TestNewBatchImageWorkerOptionsFromConfig_UsesFiniteReserveTimeout(t *testing.T) {
	opts := batchWorkerOptions(nil)
	require.Equal(t, batchimage.DefaultBatchImageWorkerReserveBlockTimeout, opts.ReserveBlockTimeout)
	require.Positive(t, opts.ReserveBlockTimeout)
}
