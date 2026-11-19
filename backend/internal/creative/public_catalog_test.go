package creative

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCreativePublicListChecksConfiguredSettings lists one exact saved model
// through a counting provider. The response contains that model, records its
// support check, and leaves candidate inventory reads at zero.
func TestCreativePublicListChecksConfiguredSettings(t *testing.T) {
	fixture := &targetQueryFixture{}
	result, err := newTargetQueryPublic(fixture).ListModels(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	require.Equal(t, "gpt-image-2", result.Data[0].Model)
	require.Equal(t, []string{"gpt-image-2"}, fixture.checked)
	require.Zero(t, fixture.inventoryReads)
}
