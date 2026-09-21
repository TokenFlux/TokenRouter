package forward

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamFailoverErrorNextAccountActionPreservesLegacyRetry(t *testing.T) {
	t.Parallel()

	require.True(t, (&UpstreamFailoverError{}).ShouldRetryNextAccount())
	require.True(t, (&UpstreamFailoverError{NextAccountAction: NextAccountRetry}).ShouldRetryNextAccount())
	require.False(t, (&UpstreamFailoverError{NextAccountAction: NextAccountStop}).ShouldRetryNextAccount())
}
