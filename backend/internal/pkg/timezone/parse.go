// 共用已有的时间文本布局与尝试顺序，不改变默认时区或错误值。
package timezone

import (
	"strconv"
	"time"
)

func ParseFlexibleTimestamp(raw string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
	}
	for _, format := range formats {
		if ts, err := time.Parse(format, raw); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, strconv.ErrSyntax
}
