// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	service "github.com/TokenFlux/TokenRouter/internal/service"
	rediscache "github.com/TokenFlux/TokenRouter/internal/team/rediscache"
	redis "github.com/redis/go-redis/v9"
)

func NewTeamInvitationLimiter(redisClient *redis.Client) service.TeamInvitationLimiter {
	return rediscache.NewTeamInvitationLimiter(redisClient)
}
