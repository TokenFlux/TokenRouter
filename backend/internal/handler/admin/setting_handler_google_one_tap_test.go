package admin

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDiffSettingsTracksGoogleOneTapSwitch(t *testing.T) {
	before := &service.SystemSettings{GoogleOneTapEnabled: false}
	after := &service.SystemSettings{GoogleOneTapEnabled: true}

	changed := diffSettings(before, after, nil, nil, UpdateSettingsRequest{})

	require.Contains(t, changed, "google_one_tap_enabled")
}
