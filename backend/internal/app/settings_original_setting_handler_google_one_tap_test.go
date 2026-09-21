package app

import (
	"testing"

	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"

	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/stretchr/testify/require"
)

func TestDiffSettingsTracksGoogleOneTapSwitch(t *testing.T) {
	before := &composite.Snapshot{GoogleOneTapEnabled: false}
	after := &composite.Snapshot{GoogleOneTapEnabled: true}

	changed := settingshttp.DiffSettings(before, after, nil, nil, settingsdto.UpdateSettingsRequest{})

	require.Contains(t, changed, "google_one_tap_enabled")
}
