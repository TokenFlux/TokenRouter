//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func matchWildcard(pattern, str string) bool { return accountcore.MatchWildcard(pattern, str) }
