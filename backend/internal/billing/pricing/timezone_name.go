// 本文件维护 pricing 的所属能力；兼容入口复用唯一实现。
package pricing

import (
	"fmt"
	"strings"
)

func ValidateTimezoneName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("timezone is required")
	}
	if name == "Local" {
		return fmt.Errorf("local is not a supported timezone")
	}
	return nil
}
