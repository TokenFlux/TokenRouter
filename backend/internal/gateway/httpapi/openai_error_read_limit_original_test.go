package httpapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIUpstreamErrorBodyReadLimitForConfig_RespectsDiagnosticLimit(t *testing.T) {
	output := &OpenAIResponseOutput{Options: OpenAIResponseOptions{
		LogUpstreamErrorBody:         true,
		LogUpstreamErrorBodyMaxBytes: int((512 << 10)) + 1024,
	}}

	require.Equal(t, int64(output.Options.LogUpstreamErrorBodyMaxBytes), output.errorBodyReadLimit())
}
