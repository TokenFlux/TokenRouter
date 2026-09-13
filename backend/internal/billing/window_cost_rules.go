package billing

import "time"

// WindowCostSchedulability 保留原费用窗口的三区数值与严格小于边界。
type WindowCostSchedulability int

const (
	WindowCostSchedulable WindowCostSchedulability = iota
	WindowCostStickyOnly
	WindowCostNotSchedulable
)

func CheckWindowCost(current, limit, reserve float64) WindowCostSchedulability {
	if limit <= 0 || current < limit {
		return WindowCostSchedulable
	}
	if current < limit+reserve {
		return WindowCostStickyOnly
	}
	return WindowCostNotSchedulable
}

// CurrentCostWindowStart 保留活动窗口及过期后按当前时区整点预测的取时语义。
func CurrentCostWindowStart(start, end *time.Time, now time.Time) time.Time {
	if start != nil && end != nil && now.Before(*end) {
		return *start
	}
	return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
}
