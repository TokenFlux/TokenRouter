package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// provideCalendar 在 bootstrap 完成时区初始化后固定日期计算位置；依赖关系保证装配顺序。
func provideCalendar(_ *ent.Client) timezone.Calendar {
	return timezone.NewCalendar(time.Local)
}
