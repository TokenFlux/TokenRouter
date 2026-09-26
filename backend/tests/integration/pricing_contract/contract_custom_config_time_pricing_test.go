//go:build unit

package pricingcontract

import (
	"math"
	"strings"
	"testing"
	"time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func pricingTimePricingTestConfig(periods ...routing.TimePricingPeriod) *routing.TimePricingConfig {
	return &routing.TimePricingConfig{Timezone: "Asia/Shanghai", Periods: periods}
}

func TestValidateTimePricingConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *routing.TimePricingConfig
		wantErr string
	}{
		{name: "nil disabled"},
		{name: "empty disabled", config: pricingTimePricingTestConfig()},
		{name: "adjacent", config: pricingTimePricingTestConfig(
			routing.TimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 2},
			routing.TimePricingPeriod{StartTime: "12:00", EndTime: "14:00", Multiplier: 1.5},
		)},
		{name: "midnight split", config: pricingTimePricingTestConfig(
			routing.TimePricingPeriod{StartTime: "22:00", EndTime: "00:00", Multiplier: 2},
			routing.TimePricingPeriod{StartTime: "00:00", EndTime: "02:00", Multiplier: 2},
		)},
		{name: "empty timezone", config: &routing.TimePricingConfig{Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}, wantErr: "timezone"},
		{name: "invalid timezone", config: &routing.TimePricingConfig{Timezone: "UTC+8", Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}, wantErr: "timezone"},
		{name: "invalid format", config: pricingTimePricingTestConfig(routing.TimePricingPeriod{StartTime: "9:00", EndTime: "12:00", Multiplier: 2}), wantErr: "HH:mm"},
		{name: "cross midnight", config: pricingTimePricingTestConfig(routing.TimePricingPeriod{StartTime: "22:00", EndTime: "02:00", Multiplier: 2}), wantErr: "before"},
		{name: "overlap", config: pricingTimePricingTestConfig(
			routing.TimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 2},
			routing.TimePricingPeriod{StartTime: "11:59", EndTime: "14:00", Multiplier: 2},
		), wantErr: "overlap"},
		{name: "zero multiplier", config: pricingTimePricingTestConfig(routing.TimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 0}), wantErr: "greater than 0"},
		{name: "minimum multiplier", config: pricingTimePricingTestConfig(routing.TimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 0.01})},
		{name: "too many decimals", config: pricingTimePricingTestConfig(routing.TimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 1.001}), wantErr: "decimal"},
		{name: "overflow", config: pricingTimePricingTestConfig(routing.TimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: math.MaxFloat64}), wantErr: "finite"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (routing.PricingConfigValidation{LoadLocation: pricingprovider.LoadPricingLocation}).TimePricing(tt.config)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestTimePricingConfigMultiplierAt(t *testing.T) {
	config := pricingTimePricingTestConfig(routing.TimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 2})
	require.Equal(t, 1.0, config.MultiplierAt(time.Date(2026, 6, 29, 0, 59, 0, 0, time.UTC), contractTimeLocation(config)))
	require.Equal(t, 2.0, config.MultiplierAt(time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC), contractTimeLocation(config)))
	require.Equal(t, 1.0, config.MultiplierAt(time.Date(2026, 6, 29, 4, 0, 0, 0, time.UTC), contractTimeLocation(config)))

	newYork := &routing.TimePricingConfig{Timezone: "America/New_York", Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}
	require.Equal(t, 2.0, newYork.MultiplierAt(time.Date(2026, 6, 29, 14, 0, 0, 0, time.UTC), contractTimeLocation(newYork)))
}

func TestTimePricingConfigMultiplierAtWeekdaysOnly(t *testing.T) {
	config := &routing.TimePricingConfig{
		Timezone:     "Asia/Shanghai",
		WeekdaysOnly: true,
		Periods: []routing.TimePricingPeriod{{
			StartTime: "09:00", EndTime: "12:00", Multiplier: 2,
		}},
	}

	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{name: "Monday in configured timezone", at: time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC), want: 2},
		{name: "Saturday in configured timezone", at: time.Date(2026, 7, 4, 1, 0, 0, 0, time.UTC), want: 1},
		{name: "Sunday in configured timezone", at: time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, config.MultiplierAt(tt.at, contractTimeLocation(config)))
		})
	}
}

func TestTimePricingConfigMultiplierAtMidnightSplit(t *testing.T) {
	config := pricingTimePricingTestConfig(
		routing.TimePricingPeriod{StartTime: "22:00", EndTime: "00:00", Multiplier: 2},
		routing.TimePricingPeriod{StartTime: "00:00", EndTime: "02:00", Multiplier: 3},
	)
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	require.Equal(t, 2.0, config.MultiplierAt(time.Date(2026, 6, 29, 23, 59, 0, 0, location), contractTimeLocation(config)))
	require.Equal(t, 3.0, config.MultiplierAt(time.Date(2026, 6, 30, 0, 0, 0, 0, location), contractTimeLocation(config)))
	require.Equal(t, 1.0, config.MultiplierAt(time.Date(2026, 6, 30, 2, 0, 0, 0, location), contractTimeLocation(config)))
}

func TestTimePricingConfigMultiplierAtDegradesForInvalidConfiguration(t *testing.T) {
	var nilConfig *routing.TimePricingConfig
	validAt := time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC)
	require.Equal(t, 1.0, nilConfig.MultiplierAt(validAt, contractTimeLocation(nilConfig)))
	require.Equal(t, 1.0, (&routing.TimePricingConfig{Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}).MultiplierAt(validAt, contractTimeLocation(&routing.TimePricingConfig{Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}})))
	require.Equal(t, 1.0, (&routing.TimePricingConfig{Timezone: "UTC+8", Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}).MultiplierAt(validAt, contractTimeLocation(&routing.TimePricingConfig{Timezone: "UTC+8", Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}})))

	err := (routing.PricingConfigValidation{LoadLocation: pricingprovider.LoadPricingLocation}).TimePricing(&routing.TimePricingConfig{Timezone: "Local", Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "timezone"))
}
