package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// calendarPublicSource 保留一次公开输入读取，日期由实例注入，不经进程全局状态。
type calendarPublicSource struct{}

func (calendarPublicSource) LoadSitePublicInputs(context.Context) (site.PublicInputs, error) {
	return site.PublicInputs{Values: map[string]string{}}, nil
}
func (calendarPublicSource) PublicVersion() string { return "calendar-contract" }

func TestPublicCalendarMatchesAPIAndInjection(t *testing.T) {
	for _, item := range []struct{ location, name string }{
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
			ctx.Request = httptest.NewRequest("GET", "/settings/public", nil)
			handler.GetPublicSettings(ctx)
			require.Equal(t, 200, response.Code)
			var envelope struct {
				Data map[string]any `json:"data"`
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
