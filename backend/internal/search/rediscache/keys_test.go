package rediscache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuotaRedisKey_Format(t *testing.T) {
	key := quotaRedisKey("brave")
	require.Equal(t, "websearch:quota:brave", key)
}
