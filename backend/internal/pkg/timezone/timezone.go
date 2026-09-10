// Package timezone provides global timezone management for the application.
// Similar to PHP's date_default_timezone_set, this package allows setting
// a global timezone that affects all time.Now() calls.
package timezone

import (
	"fmt"
	"log"
	"time"
)

var (
	// location is the global timezone location
	location *time.Location
	// tzName stores the timezone name for logging/debugging
	tzName string
)

// Init initializes the global timezone setting.
// This should be called once at application startup.
// Example timezone values: "Asia/Shanghai", "America/New_York", "UTC"
func Init(tz string) error {
	if tz == "" {
		tz = "Asia/Shanghai" // Default timezone
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}

	// Set the global Go time.Local to our timezone
	// This affects time.Now() throughout the application
	time.Local = loc
	location = loc
	tzName = tz

	log.Printf("Timezone initialized: %s (UTC offset: %s)", tz, getUTCOffset(loc))
	return nil
}

// getUTCOffset returns the current UTC offset for a location
func getUTCOffset(loc *time.Location) string {
	_, offset := time.Now().In(loc).Zone()
	hours := offset / 3600
	minutes := (offset % 3600) / 60
	if minutes < 0 {
		minutes = -minutes
	}
	sign := "+"
	if hours < 0 {
		sign = "-"
		hours = -hours
	}
	return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
}

// Now returns the current time in the configured timezone.
// This is equivalent to time.Now() after Init() is called,
// but provided for explicit timezone-aware code.
func Now() time.Time {
	return NewCalendar(location).Now()
}

// Location returns the configured timezone location.
func Location() *time.Location {
	if location == nil {
		return time.Local
	}
	return location
}

// Name returns the configured timezone name.
func Name() string {
	if tzName == "" {
		return "Local"
	}
	return tzName
}

// UTCOffset 返回当前配置时区相对 UTC 的偏移，例如 "+08:00"。
func UTCOffset() string {
	return getUTCOffset(Location())
}

// StartOfDay returns the start of the given day (00:00:00) in the configured timezone.
func StartOfDay(t time.Time) time.Time {
	return NewCalendar(Location()).StartOfDay(t)
}

// Today returns the start of today (00:00:00) in the configured timezone.
func Today() time.Time {
	return NewCalendar(Location()).Today()
}

// EndOfDay returns the end of the given day (23:59:59.999999999) in the configured timezone.
func EndOfDay(t time.Time) time.Time {
	return NewCalendar(Location()).EndOfDay(t)
}

// StartOfWeek returns the start of the week (Monday 00:00:00) for the given time.
func StartOfWeek(t time.Time) time.Time {
	return NewCalendar(Location()).StartOfWeek(t)
}

// StartOfMonth returns the start of the month (1st day 00:00:00) for the given time.
func StartOfMonth(t time.Time) time.Time {
	return NewCalendar(Location()).StartOfMonth(t)
}

// ParseInLocation parses a time string in the configured timezone.
func ParseInLocation(layout, value string) (time.Time, error) {
	return NewCalendar(Location()).ParseInLocation(layout, value)
}

// ParseInUserLocation parses a time string in the user's timezone.
// If userTZ is empty or invalid, falls back to the configured server timezone.
func ParseInUserLocation(layout, value, userTZ string) (time.Time, error) {
	return NewCalendar(Location()).ParseInUserLocation(layout, value, userTZ)
}

// ParseDateTimeInUserLocation 解析用户时区下的日期或日期时间。
// 返回值 dateOnly 表示输入是否为 YYYY-MM-DD，调用方可据此决定结束边界是否需要顺延一天。
func ParseDateTimeInUserLocation(value, userTZ string) (parsed time.Time, dateOnly bool, err error) {
	return NewCalendar(Location()).ParseDateTimeInUserLocation(value, userTZ)
}

// NowInUserLocation returns the current time in the user's timezone.
// If userTZ is empty or invalid, falls back to the configured server timezone.
func NowInUserLocation(userTZ string) time.Time {
	return NewCalendar(location).NowInUserLocation(userTZ)
}

// StartOfDayInUserLocation returns the start of the given day in the user's timezone.
// If userTZ is empty or invalid, falls back to the configured server timezone.
func StartOfDayInUserLocation(t time.Time, userTZ string) time.Time {
	return NewCalendar(Location()).StartOfDayInUserLocation(t, userTZ)
}
