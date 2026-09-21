package usageview_test

import (
	"testing"

	usageview "github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
	"github.com/stretchr/testify/require"
)

func TestValidateNormalizedUsageRejectsUnknownMode(t *testing.T) {
	err := usageview.ValidateNormalizedUsage(&usageview.UpstreamUsageInfo{Provider: "test", Mode: "window"})
	require.Error(t, err)
	err = usageview.ValidateNormalizedUsage(&usageview.UpstreamUsageInfo{Provider: "test", Mode: "balance"})
	require.Error(t, err)
}
