package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// public_test.go checks the public settings API against the HTML injection path.
// Controlled display values and injected calendars verify that both transports
// expose the same feature decisions and timezone metadata before browsers render
// account controls.

// calendarPublicSource supplies a display switch alongside injected calendar settings.
type calendarPublicSource struct {
	showUserID string // Stored display-switch value.
}

// TestPublicCalendarMatchesAPIAndInjection supplies calendars with distinct zone names and display labels.
// It verifies that API and HTML injection return the configured label and the
// current UTC offset derived from the same injected calendar.
func TestPublicCalendarMatchesAPIAndInjection(t *testing.T) {
	for _, item := range []struct {
		location, name string // IANA calendar location and displayed timezone label
	}{
		{"Asia/Shanghai", "Asia/Shanghai"},
		{"America/New_York", "America/New_York"},
		{"America/New_York", "Local"},
	} {
		t.Run(item.name, func(t *testing.T) {
			location, err := time.LoadLocation(item.location)
			require.NoError(t, err)
			calendar := timezone.NewCalendar(location)
			service := site.NewPublicService(calendarPublicSource{}, calendar, item.name)
			handler := NewPublicHandler(service, "calendar-version")
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/settings/public", nil)
			handler.GetPublicSettings(ctx)
			require.Equal(t, 200, response.Code)
			var envelope struct {
				Data map[string]any `json:"data"` // public settings response
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			injection, err := service.GetPublicSettingsForInjection(context.Background())
			require.NoError(t, err)
			encoded, err := json.Marshal(injection)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(encoded, &fields))
			for _, values := range []map[string]any{envelope.Data, fields} {
				require.Equal(t, item.name, values["server_timezone"])
				require.Equal(t, calendar.UTCOffset(time.Now()), values["server_utc_offset"])
			}
		})
	}
}

// LoadSitePublicInputs supplies display-switch values with empty auth and usage inputs.
// Each call allocates a fresh values map, keeping fixture data independent of
// the response service's language and navigation transformations.
func (source calendarPublicSource) LoadSitePublicInputs(context.Context) (site.PublicInputs, error) {
	return site.PublicInputs{Values: map[string]string{site.SettingKeyShowUserID: source.showUserID}}, nil
}

// PublicVersion returns the fixed source build label used by injection fixtures.
func (calendarPublicSource) PublicVersion() string { return "calendar-contract" }

// LoadSiteShareImage leaves this text-only fixture without an uploaded cover.
func (calendarPublicSource) LoadSiteShareImage(context.Context) (string, error) { return "", nil }

// TestPublicUserIDSwitchMatchesAPIAndInjection supplies missing, disabled, enabled,
// and invalid stored values. Both public transports must agree so initial HTML
// rendering and subsequent API refreshes expose the same account display policy.
func TestPublicUserIDSwitchMatchesAPIAndInjection(t *testing.T) {
	for _, value := range []string{"", "false", "true", "invalid"} {
		t.Run(value, func(t *testing.T) {
			service := site.NewPublicService(calendarPublicSource{showUserID: value}, timezone.NewCalendar(time.UTC), "UTC")
			handler := NewPublicHandler(service, "test")
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)
			handler.GetPublicSettings(ctx)
			require.Equal(t, http.StatusOK, response.Code)
			var envelope struct {
				Data map[string]any `json:"data"` // Public settings response.
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Equal(t, value == "true", envelope.Data["show_user_id"])

			injection, err := service.GetPublicSettingsForInjection(context.Background())
			require.NoError(t, err)
			encoded, err := json.Marshal(injection)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(encoded, &fields))
			require.Equal(t, value == "true", fields["show_user_id"])
		})
	}
}
