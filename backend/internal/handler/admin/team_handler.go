// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	service "github.com/TokenFlux/TokenRouter/internal/service"
	teamhttp "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
)

type TeamHandler = teamhttp.AdminHandler

func NewTeamHandler(teamService *service.TeamService) *TeamHandler {
	return teamhttp.NewAdminHandler(teamService)
}
