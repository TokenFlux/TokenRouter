package billing

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"time"
)

// DateRuntime 显式注入时钟和日历；遗留无参数入口继续使用兼容默认值。
type DateRuntime struct {
	Now      func() time.Time
	Calendar *timezone.Calendar
}

func (r DateRuntime) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
func (r DateRuntime) calendar() timezone.Calendar {
	if r.Calendar != nil {
		return *r.Calendar
	}
	return timezone.NewCalendar(timezone.Location())
}
