//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package service

import (
	team "github.com/TokenFlux/TokenRouter/internal/team"
)

func hashTeamToken(token string) string { return team.HashTeamToken(token) }
