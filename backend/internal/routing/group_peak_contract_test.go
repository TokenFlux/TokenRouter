package routing_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func TestPeakMultiplierAt_DisabledOrUnconfigured(t *testing.T) {
	cases := []struct {
		name string
		g    *routing.Group
	}{
		{"disabled", newPeakGroup(false, "14:00", "18:00", 3.0)},
		{"empty start", newPeakGroup(true, "", "18:00", 3.0)},
		{"empty end", newPeakGroup(true, "14:00", "", 3.0)},
		{"invalid start>=end", newPeakGroup(true, "18:00", "14:00", 3.0)},
		{"equal start==end", newPeakGroup(true, "14:00", "14:00", 3.0)},
		{"malformed start", newPeakGroup(true, "99:99", "18:00", 3.0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.g.PeakMultiplierAt(at(15, 0)); got != 1.0 {
				t.Fatalf("expect 1.0, got %v", got)
			}
		})
	}
}

func TestPeakMultiplierAt_NilReceiver(t *testing.T) {
	var g *routing.Group
	if got := g.PeakMultiplierAt(at(15, 0)); got != 1.0 {
		t.Fatalf("expect 1.0, got %v", got)
	}
}

func TestPeakMultiplierAt_Boundaries(t *testing.T) {
	g := newPeakGroup(true, "14:00", "18:00", 3.0)
	cases := []struct {
		t    time.Time
		want float64
	}{
		{at(13, 59), 1.0},
		{at(14, 0), 3.0},
		{at(15, 30), 3.0},
		{at(17, 59), 3.0},
		{at(18, 0), 1.0},
		{at(23, 0), 1.0},
	}
	for _, c := range cases {
		t.Run(c.t.Format("15:04"), func(t *testing.T) {
			if got := g.PeakMultiplierAt(c.t); got != c.want {
				t.Fatalf("at %s: expect %v, got %v", c.t.Format("15:04"), c.want, got)
			}
		})
	}
}

func TestPeakMultiplierAt_RespectsTimezoneLocation(t *testing.T) {
	// 装配显式使用 UTC。北京 15:00 = UTC 07:00，不在 [14:00,18:00)。
	nonUTC := time.Date(2026, 6, 29, 15, 0, 0, 0, mustLoad("Asia/Shanghai"))
	g := newPeakGroup(true, "14:00", "18:00", 3.0)
	if got := g.PeakMultiplierAt(nonUTC.In(time.UTC)); got != 1.0 {
		t.Fatalf("expect 1.0 (converted to UTC 07:00), got %v", got)
	}
}

func TestValidatePeakRateConfig(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		start   string
		end     string
		mult    float64
		wantErr bool
	}{
		{"disabled passes through", false, "", "", 0, false},
		{"enabled valid", true, "14:00", "18:00", 3.0, false},
		{"enabled valid single digit hour", true, "1:00", "2:00", 3.0, false},
		{"enabled empty start", true, "", "18:00", 1.0, true},
		{"enabled empty end", true, "14:00", "", 1.0, true},
		{"enabled malformed start", true, "99:99", "18:00", 1.0, true},
		{"enabled malformed end", true, "14:00", "25:00", 1.0, true},
		{"enabled equal start==end", true, "14:00", "14:00", 1.0, true},
		{"enabled cross-day rejected", true, "22:00", "02:00", 1.0, true},
		{"enabled negative multiplier", true, "14:00", "18:00", -0.5, true},
		{"enabled zero multiplier allowed", true, "14:00", "18:00", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := routing.ValidatePeakRateConfig(c.enabled, c.start, c.end, c.mult)
			if c.wantErr && err == nil {
				t.Fatalf("expect error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expect no error, got %v", err)
			}
		})
	}
}

func TestParseMinutesMatchesLegacyTimeParseShape(t *testing.T) {
	cases := []struct {
		value string
		want  int
		ok    bool
	}{
		{"0:00", 0, true},
		{"00:00", 0, true},
		{"1:30", 90, true},
		{"01:30", 90, true},
		{"23:59", 1439, true},
		{"001:30", 0, false},
		{"9:3", 0, false},
		{"24:00", 0, false},
		{"1:030", 0, false},
		{" 1:30", 0, false},
		{"1:30 ", 0, false},
	}

	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			got, ok := routing.ParseMinutes(c.value)
			if ok != c.ok {
				t.Fatalf("ok: got %v, want %v", ok, c.ok)
			}
			if ok && got != c.want {
				t.Fatalf("minutes: got %v, want %v", got, c.want)
			}
		})
	}
}

func TestNormalizePeakRateConfig(t *testing.T) {
	enabled, start, end, multiplier := routing.NormalizePeakRateConfig(false, "bad", "18:00", -2)
	if enabled || start != "" || end != "18:00" || multiplier != 1.0 {
		t.Fatalf("disabled cleanup mismatch: enabled=%v start=%q end=%q multiplier=%v", enabled, start, end, multiplier)
	}

	enabled, start, end, multiplier = routing.NormalizePeakRateConfig(false, "14:00", "18:00", 3)
	if enabled || start != "14:00" || end != "18:00" || multiplier != 3 {
		t.Fatalf("disabled valid config should be preserved: enabled=%v start=%q end=%q multiplier=%v", enabled, start, end, multiplier)
	}

	enabled, start, end, multiplier = routing.NormalizePeakRateConfig(true, "bad", "18:00", -2)
	if !enabled || start != "bad" || end != "18:00" || multiplier != -2 {
		t.Fatalf("enabled config should be left for validation: enabled=%v start=%q end=%q multiplier=%v", enabled, start, end, multiplier)
	}
}

func TestPeakMultiplierAt_EnabledGroupUsesConfiguredWindow(t *testing.T) {
	g := newPeakGroup(true, "14:00", "18:00", 3.0)
	if got := g.PeakMultiplierAt(at(15, 30)); got != 3.0 {
		t.Fatalf("enabled group peak multiplier: got %v, want 3.0", got)
	}
}

func newPeakGroup(enabled bool, start, end string, mult float64) *routing.Group {
	return &routing.Group{
		PeakRateEnabled:    enabled,
		PeakStart:          start,
		PeakEnd:            end,
		PeakRateMultiplier: mult,
	}
}

func at(hour, min int) time.Time {
	return time.Date(2026, 6, 29, hour, min, 0, 0, time.UTC)
}

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}
