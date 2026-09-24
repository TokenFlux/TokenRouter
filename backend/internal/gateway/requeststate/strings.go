package requeststate

import (
	"strings"
)

// FirstNonEmpty 保持剩余适配入口的首个非空字符串选择顺序。
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
