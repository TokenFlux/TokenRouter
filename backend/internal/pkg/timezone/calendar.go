// 本文件拥有显式时区的日期计算，不负责进程初始化。
package timezone

import (
	"fmt"
	"strings"
	"time"
)

// Calendar 持有日期计算使用的时区，不修改进程全局状态。
type Calendar struct {
	location *time.Location
}

// NewCalendar 使用指定时区；nil 延续 Go 的本地时区语义。
func NewCalendar(loc *time.Location) Calendar {
	return Calendar{location: loc}
}

// Location 返回本对象的时区，零值使用 time.Local。
func (c Calendar) Location() *time.Location {
	if c.location == nil {
		return time.Local
	}
	return c.location
}
func (c Calendar) Now() time.Time {
	// 未指定时区时保留 time.Now 的单调时钟，兼容全局初始化前的调用。
	if c.location == nil {
		return time.Now()
	}
	return time.Now().In(c.Location())
}

// UTCOffset 保留原偏移展示格式，计算时刻由调用方提供。
func (c Calendar) UTCOffset(t time.Time) string {
	_, offset := t.In(c.Location()).Zone()
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

func (c Calendar) StartOfDay(t time.Time) time.Time {
	loc := c.Location()
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func (c Calendar) Today() time.Time {
	return c.StartOfDay(c.Now())
}

func (c Calendar) EndOfDay(t time.Time) time.Time {
	loc := c.Location()
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, loc)
}

func (c Calendar) StartOfWeek(t time.Time) time.Time {
	loc := c.Location()
	t = t.In(loc)
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday is day 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-weekday+1, 0, 0, 0, 0, loc)
}

func (c Calendar) StartOfMonth(t time.Time) time.Time {
	loc := c.Location()
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
}

func (c Calendar) ParseInLocation(layout, value string) (time.Time, error) {
	return time.ParseInLocation(layout, value, c.Location())
}

func (c Calendar) ParseInUserLocation(layout, value, userTZ string) (time.Time, error) {
	loc := c.Location() // default to server timezone
	if userTZ != "" {
		if userLoc, err := time.LoadLocation(userTZ); err == nil {
			loc = userLoc
		}
	}
	return time.ParseInLocation(layout, value, loc)
}

func (c Calendar) ParseDateTimeInUserLocation(value, userTZ string) (parsed time.Time, dateOnly bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false, fmt.Errorf("empty datetime")
	}

	if t, parseErr := time.Parse(time.RFC3339Nano, value); parseErr == nil {
		return t, false, nil
	}

	loc := c.Location()
	if userTZ != "" {
		if userLoc, loadErr := time.LoadLocation(userTZ); loadErr == nil {
			loc = userLoc
		}
	}

	layouts := []struct {
		layout   string
		dateOnly bool
	}{
		{layout: "2006-01-02", dateOnly: true},
		{layout: "2006-01-02T15:04:05", dateOnly: false},
		{layout: "2006-01-02T15:04", dateOnly: false},
		{layout: "2006-01-02 15:04:05", dateOnly: false},
		{layout: "2006-01-02 15:04", dateOnly: false},
	}
	for _, candidate := range layouts {
		if t, parseErr := time.ParseInLocation(candidate.layout, value, loc); parseErr == nil {
			return t, candidate.dateOnly, nil
		}
	}

	return time.Time{}, false, fmt.Errorf("invalid datetime %q", value)
}

func (c Calendar) NowInUserLocation(userTZ string) time.Time {
	if userTZ == "" {
		return c.Now()
	}
	if userLoc, err := time.LoadLocation(userTZ); err == nil {
		return time.Now().In(userLoc)
	}
	return c.Now()
}

func (c Calendar) StartOfDayInUserLocation(t time.Time, userTZ string) time.Time {
	loc := c.Location()
	if userTZ != "" {
		if userLoc, err := time.LoadLocation(userTZ); err == nil {
			loc = userLoc
		}
	}
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}
