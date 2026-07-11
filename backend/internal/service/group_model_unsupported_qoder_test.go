package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

func TestDefaultRequestModelIDsForPlatformQoder(t *testing.T) {
	require.Equal(t, qoder.DefaultRequestModelIDs(), defaultRequestModelIDsForPlatform(PlatformQoder))
}
