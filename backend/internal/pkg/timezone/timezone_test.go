package timezone

import (
	"testing"
	"time"
)

// testCalendar 为原日期契约提供独立时区，不改动其他测试的进程状态。
func testCalendar(t *testing.T, name string) Calendar {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return NewCalendar(location)
}

func TestToday(t *testing.T) {
	calendar := testCalendar(t, "Asia/Shanghai")
	today := calendar.Today()
	now := calendar.Now()
	if today.Hour() != 0 || today.Minute() != 0 || today.Second() != 0 {
		t.Errorf("Today() not at start of day: %v", today)
	}
	if today.Year() != now.Year() || today.Month() != now.Month() || today.Day() != now.Day() {
		t.Errorf("Today() date mismatch: today=%v, now=%v", today, now)
	}
}

func TestStartOfDay(t *testing.T) {
	calendar := testCalendar(t, "Asia/Shanghai")
	input := time.Date(2024, 6, 15, 15, 30, 45, 123456789, calendar.Location())
	start := calendar.StartOfDay(input)
	want := time.Date(2024, 6, 15, 0, 0, 0, 0, calendar.Location())
	if !start.Equal(want) {
		t.Errorf("StartOfDay: got %v, want %v", start, want)
	}
}

func TestTruncateVsStartOfDay(t *testing.T) {
	calendar := testCalendar(t, "Asia/Shanghai")
	now := calendar.Now()
	truncated := now.Truncate(24 * time.Hour)
	start := calendar.StartOfDay(now)
	t.Logf("Now: %v, Truncate(24h): %v, StartOfDay: %v", now, truncated, start)
	if start.Hour() != 0 {
		t.Errorf("StartOfDay should be at hour 0, got %d", start.Hour())
	}
}

func TestDSTAwareness(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York timezone not available: %v", err)
	}
	calendar := NewCalendar(location)
	_ = calendar.Today()
	_ = calendar.Now()
	_ = calendar.StartOfDay(calendar.Now())
}

func TestStartOfWeek_Boundaries(t *testing.T) {
	calendar := testCalendar(t, "Asia/Shanghai")
	location := calendar.Location()
	want := time.Date(2026, 5, 18, 0, 0, 0, 0, location)
	cases := []struct {
		name string
		in   time.Time
	}{
		{"friday", time.Date(2026, 5, 22, 14, 30, 0, 0, location)},
		{"sunday", time.Date(2026, 5, 24, 10, 0, 0, 0, location)},
		{"monday-self", time.Date(2026, 5, 18, 9, 15, 30, 0, location)},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := calendar.StartOfWeek(item.in); !got.Equal(want) {
				t.Errorf("StartOfWeek(%v) = %v, want %v", item.in, got, want)
			}
		})
	}
}
