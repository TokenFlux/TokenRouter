//go:build unit

package service

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func channelTimePricingTestConfig(periods ...ChannelTimePricingPeriod) *ChannelTimePricing {
	return &ChannelTimePricing{Timezone: "Asia/Shanghai", Periods: periods}
}

func TestValidateChannelTimePricing(t *testing.T) {
	tests := []struct {
		name    string
		config  *ChannelTimePricing
		wantErr string
	}{
		{name: "nil disabled"},
		{name: "empty disabled", config: channelTimePricingTestConfig()},
		{name: "adjacent", config: channelTimePricingTestConfig(
			ChannelTimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 2},
			ChannelTimePricingPeriod{StartTime: "12:00", EndTime: "14:00", Multiplier: 1.5},
		)},
		{name: "midnight split", config: channelTimePricingTestConfig(
			ChannelTimePricingPeriod{StartTime: "22:00", EndTime: "00:00", Multiplier: 2},
			ChannelTimePricingPeriod{StartTime: "00:00", EndTime: "02:00", Multiplier: 2},
		)},
		{name: "empty timezone", config: &ChannelTimePricing{Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}, wantErr: "timezone"},
		{name: "invalid timezone", config: &ChannelTimePricing{Timezone: "UTC+8", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}, wantErr: "timezone"},
		{name: "invalid format", config: channelTimePricingTestConfig(ChannelTimePricingPeriod{StartTime: "9:00", EndTime: "12:00", Multiplier: 2}), wantErr: "HH:mm"},
		{name: "cross midnight", config: channelTimePricingTestConfig(ChannelTimePricingPeriod{StartTime: "22:00", EndTime: "02:00", Multiplier: 2}), wantErr: "before"},
		{name: "overlap", config: channelTimePricingTestConfig(
			ChannelTimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 2},
			ChannelTimePricingPeriod{StartTime: "11:59", EndTime: "14:00", Multiplier: 2},
		), wantErr: "overlap"},
		{name: "zero multiplier", config: channelTimePricingTestConfig(ChannelTimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 0}), wantErr: "greater than 0"},
		{name: "minimum multiplier", config: channelTimePricingTestConfig(ChannelTimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 0.01})},
		{name: "too many decimals", config: channelTimePricingTestConfig(ChannelTimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 1.001}), wantErr: "decimal"},
		{name: "overflow", config: channelTimePricingTestConfig(ChannelTimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: math.MaxFloat64}), wantErr: "finite"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateChannelTimePricing(tt.config)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestChannelTimePricingMultiplierAt(t *testing.T) {
	config := channelTimePricingTestConfig(ChannelTimePricingPeriod{StartTime: "09:00", EndTime: "12:00", Multiplier: 2})
	require.Equal(t, 1.0, channelTimeMultiplierAt(config, time.Date(2026, 6, 29, 0, 59, 0, 0, time.UTC)))
	require.Equal(t, 2.0, channelTimeMultiplierAt(config, time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC)))
	require.Equal(t, 1.0, channelTimeMultiplierAt(config, time.Date(2026, 6, 29, 4, 0, 0, 0, time.UTC)))

	newYork := &ChannelTimePricing{Timezone: "America/New_York", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}
	require.Equal(t, 2.0, channelTimeMultiplierAt(newYork, time.Date(2026, 6, 29, 14, 0, 0, 0, time.UTC)))
}

func TestChannelTimePricingMultiplierAtWeekdaysOnly(t *testing.T) {
	config := &ChannelTimePricing{
		Timezone:     "Asia/Shanghai",
		WeekdaysOnly: true,
		Periods: []ChannelTimePricingPeriod{{
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
			require.Equal(t, tt.want, channelTimeMultiplierAt(config, tt.at))
		})
	}
}

func TestChannelTimePricingMultiplierAtMidnightSplit(t *testing.T) {
	config := channelTimePricingTestConfig(
		ChannelTimePricingPeriod{StartTime: "22:00", EndTime: "00:00", Multiplier: 2},
		ChannelTimePricingPeriod{StartTime: "00:00", EndTime: "02:00", Multiplier: 3},
	)
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	require.Equal(t, 2.0, channelTimeMultiplierAt(config, time.Date(2026, 6, 29, 23, 59, 0, 0, location)))
	require.Equal(t, 3.0, channelTimeMultiplierAt(config, time.Date(2026, 6, 30, 0, 0, 0, 0, location)))
	require.Equal(t, 1.0, channelTimeMultiplierAt(config, time.Date(2026, 6, 30, 2, 0, 0, 0, location)))
}

func TestChannelTimePricingMultiplierAtDegradesForInvalidConfiguration(t *testing.T) {
	var nilConfig *ChannelTimePricing
	validAt := time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC)
	require.Equal(t, 1.0, channelTimeMultiplierAt(nilConfig, validAt))
	require.Equal(t, 1.0, channelTimeMultiplierAt(&ChannelTimePricing{Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}, validAt))
	require.Equal(t, 1.0, channelTimeMultiplierAt(&ChannelTimePricing{Timezone: "UTC+8", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}}, validAt))

	err := validateChannelTimePricing(&ChannelTimePricing{Timezone: "Local", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}}})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "timezone"))
}
