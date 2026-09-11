package pricing

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type ParsedChannelTimePeriod struct {
	start      int
	end        int
	multiplier float64
}

// ValidateChannelTimePricing 校验分时倍率配置；nil 或空 periods 表示未启用。
func ValidateChannelTimePricing(config *ChannelTimePricing) error {
	if config == nil || len(config.Periods) == 0 {
		return nil
	}
	_, err := ParseChannelTimePeriods(config.Periods)
	return err
}

// MultiplierAt 返回 at 对应的分时倍率；无配置或脏配置安全降级为 1。
func (config *ChannelTimePricing) MultiplierAt(at time.Time, location *time.Location) float64 {
	if config == nil || len(config.Periods) == 0 || at.IsZero() {
		return 1.0
	}
	if err := ValidateChannelTimePricing(config); err != nil {
		return 1.0
	}
	if location == nil {
		return 1.0
	}
	periods, err := ParseChannelTimePeriods(config.Periods)
	if err != nil {
		return 1.0
	}

	local := at.In(location)
	if config.WeekdaysOnly && (local.Weekday() == time.Saturday || local.Weekday() == time.Sunday) {
		return 1.0
	}
	second := local.Hour()*60*60 + local.Minute()*60 + local.Second()
	for _, period := range periods {
		if second >= period.start && second < period.end {
			return period.multiplier
		}
	}
	return 1.0
}

func ParseChannelTime(value string, end bool) (int, error) {
	if end && (value == "00:00" || value == "00:00:00") {
		return 24 * 60 * 60, nil
	}
	layout := "15:04:05"
	if len(value) == len("15:04") {
		layout = "15:04"
	}
	parsed, err := time.Parse(layout, value)
	if err != nil || parsed.Format(layout) != value {
		return 0, fmt.Errorf("time %q must use HH:mm or HH:mm:ss format", value)
	}
	return parsed.Hour()*60*60 + parsed.Minute()*60 + parsed.Second(), nil
}

func ParseChannelTimePeriods(periods []ChannelTimePricingPeriod) ([]ParsedChannelTimePeriod, error) {
	parsed := make([]ParsedChannelTimePeriod, 0, len(periods))
	for _, period := range periods {
		if math.IsNaN(period.Multiplier) || math.IsInf(period.Multiplier, 0) || period.Multiplier <= 0 {
			return nil, fmt.Errorf("multiplier must be finite and greater than 0")
		}
		if period.Multiplier < 0.01 {
			return nil, fmt.Errorf("multiplier must be at least 0.01")
		}
		scaled := period.Multiplier * 100
		if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
			return nil, fmt.Errorf("multiplier must remain finite when scaled")
		}
		if math.Abs(scaled-math.Round(scaled)) > 1e-9 {
			return nil, fmt.Errorf("multiplier must have at most two decimal places")
		}

		start, err := ParseChannelTime(period.StartTime, false)
		if err != nil {
			return nil, err
		}
		end, err := ParseChannelTime(period.EndTime, true)
		if err != nil {
			return nil, err
		}
		if period.StartTime == period.EndTime || start >= end {
			return nil, fmt.Errorf("start time must be before end time")
		}
		parsed = append(parsed, ParsedChannelTimePeriod{start: start, end: end, multiplier: period.Multiplier})
	}

	sort.Slice(parsed, func(i, j int) bool { return parsed[i].start < parsed[j].start })
	for i := 1; i < len(parsed); i++ {
		if parsed[i].start < parsed[i-1].end {
			return nil, fmt.Errorf("time pricing periods overlap")
		}
	}
	return parsed, nil
}
