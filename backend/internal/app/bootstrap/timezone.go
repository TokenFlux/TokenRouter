package bootstrap

import (
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// timezone.go initializes the process timezone used by application calendars.
// Application calendars capture the process timezone after bootstrap. Loading
// the configured location before publishing it leaves time.Local unchanged on
// invalid names, while an empty setting selects Asia/Shanghai for subsequent
// calendar construction and time reporting.

// InitTimezone loads a location and publishes it through time.Local during
// bootstrap. Empty names select Asia/Shanghai; invalid names return an error
// before changing the process timezone, so call it before concurrent calendar
// consumers start.
func InitTimezone(name string) error {
	if name == "" {
		name = "Asia/Shanghai"
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", name, err)
	}
	time.Local = location
	logging.LegacyPrintf("bootstrap.timezone", "Timezone initialized: %s (UTC offset: %s)", name, timezone.NewCalendar(location).UTCOffset(time.Now()))
	return nil
}
