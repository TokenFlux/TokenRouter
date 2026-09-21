//go:build unit

package forward

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyGrokCredentialFailureBillingExhaustionIsTransient(t *testing.T) {
	for _, message := range []string{
		"Grok OAuth refresh failed: spending limit reached",
		"included free usage exhausted",
		"credits exhausted",
	} {
		class := ClassifyGrokCredentialFailure(false, errors.New(message))
		require.Equal(t, GrokCredentialReasonRefreshTransient, class.Reason, message)
		require.True(t, class.Transient, message)
		require.False(t, class.Permanent, message)
	}
}
