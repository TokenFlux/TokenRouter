// 本文件维护 team 的所属能力；兼容入口复用唯一实现。
package team

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var (
	ErrTeamFeatureDisabled        = infraerrors.Forbidden("TEAM_FEATURE_DISABLED", "团队功能未启用")
	ErrTeamSelfServiceDisabled    = infraerrors.Forbidden("TEAM_SELF_SERVICE_DISABLED", "暂不允许用户自助创建团队")
	ErrTeamNotFound               = infraerrors.NotFound("TEAM_NOT_FOUND", "团队不存在")
	ErrTeamMembershipRequired     = billing.ErrTeamMembershipRequired
	ErrTeamOwnerRequired          = infraerrors.Forbidden("TEAM_OWNER_REQUIRED", "仅团队所有者可执行此操作")
	ErrTeamAlreadyJoined          = infraerrors.Conflict("TEAM_ALREADY_JOINED", "用户已经属于一个团队")
	ErrTeamMemberLimitReached     = infraerrors.Conflict("TEAM_MEMBER_LIMIT_REACHED", "团队成员数量已达上限")
	ErrTeamInvitationInvalid      = infraerrors.BadRequest("TEAM_INVITATION_INVALID", "团队邀请无效")
	ErrTeamInvitationExpired      = infraerrors.BadRequest("TEAM_INVITATION_EXPIRED", "团队邀请已过期")
	ErrTeamInvitationEmail        = infraerrors.Forbidden("TEAM_INVITATION_EMAIL_MISMATCH", "当前账号邮箱与受邀邮箱不一致")
	ErrTeamInvitationRateLimited  = infraerrors.TooManyRequests("TEAM_INVITATION_RATE_LIMITED", "团队邀请发送过于频繁")
	ErrTeamInvitationUnavailable  = infraerrors.ServiceUnavailable("TEAM_INVITATION_UNAVAILABLE", "团队邀请服务暂时不可用")
	ErrTeamFrontendURLUnavailable = infraerrors.ServiceUnavailable("TEAM_FRONTEND_URL_UNAVAILABLE", "未配置前端地址，无法生成团队邮件链接")
	ErrTeamOwnerCannotLeave       = infraerrors.Conflict("TEAM_OWNER_CANNOT_LEAVE", "团队所有者必须先转让所有权或解散团队")
	ErrTeamOwnerTransferRequired  = infraerrors.Conflict("TEAM_OWNER_TRANSFER_REQUIRED", "删除团队所有者前必须先转让所有权或解散团队")
	ErrTeamTransferInvalid        = infraerrors.BadRequest("TEAM_TRANSFER_INVALID", "所有权转让无效")
	ErrTeamTransferExpired        = infraerrors.BadRequest("TEAM_TRANSFER_EXPIRED", "所有权转让已过期")
	ErrTeamSuspended              = infraerrors.Forbidden("TEAM_SUSPENDED", "团队已暂停")
	ErrTeamMemberDailyExceeded    = billing.ErrTeamMemberDailyExceeded
	ErrTeamMemberWeeklyExceeded   = billing.ErrTeamMemberWeeklyExceeded
	ErrTeamMemberMonthlyExceeded  = billing.ErrTeamMemberMonthlyExceeded
)
