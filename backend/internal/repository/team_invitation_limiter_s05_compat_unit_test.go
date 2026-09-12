//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package repository

import (
	rediscache "github.com/TokenFlux/TokenRouter/internal/team/rediscache"
)

const teamInvitationRecipientCooldown = rediscache.TeamInvitationRecipientCooldown

const teamInvitationHourlyWindow = rediscache.TeamInvitationHourlyWindow

const teamInvitationHourlyLimit = rediscache.TeamInvitationHourlyLimit
