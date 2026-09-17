//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 仅保留已有 unit 断言需要的旧入口，退出 S16。
func validateAndNormalizeAccountSchedulingThresholds(input map[string]int) (map[string]int, error) {
	return account.ValidateAndNormalizeAccountSchedulingThresholds(input)
}
