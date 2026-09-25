package billing

import (
	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var ErrAPIKeyNotFound = apperror.NotFound("API_KEY_NOT_FOUND", "api key not found")

var ErrAPIKeyQuotaExhausted = apperror.TooManyRequests("API_KEY_QUOTA_EXHAUSTED", "api key quota exhausted")

var ErrAPIKeyRateLimit1dExceeded = apperror.TooManyRequests("API_KEY_RATE_1D_EXCEEDED", "api key 日限额已用完")

var ErrAPIKeyRateLimit5hExceeded = apperror.TooManyRequests("API_KEY_RATE_5H_EXCEEDED", "api key 5小时限额已用完")

var ErrAPIKeyRateLimit7dExceeded = apperror.TooManyRequests("API_KEY_RATE_7D_EXCEEDED", "api key 7天限额已用完")

var ErrAccountNotFound = apperror.NotFound("ACCOUNT_NOT_FOUND", "account not found")

var ErrTaskInsufficientBalance = apperror.New(apperror.Category(402), "BATCH_IMAGE_INSUFFICIENT_BALANCE", "insufficient balance for batch image hold")

var ErrTaskNotFound = apperror.New(apperror.CategoryNotFound, "BATCH_IMAGE_JOB_NOT_FOUND", "batch image job not found")

var ErrPreferredSubscriptionInsufficient = apperror.TooManyRequests("PREFERRED_SUBSCRIPTION_EXHAUSTED", "preferred subscription has insufficient remaining quota")

var ErrPreferredSubscriptionInvalid = apperror.Forbidden("PREFERRED_SUBSCRIPTION_INVALID", "preferred subscription is unavailable")

var ErrSubscriptionNotFound = apperror.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found")

var ErrTeamMemberDailyExceeded = apperror.TooManyRequests("TEAM_MEMBER_DAILY_LIMIT_EXCEEDED", "团队成员日限额已用完")

var ErrTeamMemberMonthlyExceeded = apperror.TooManyRequests("TEAM_MEMBER_MONTHLY_LIMIT_EXCEEDED", "团队成员月限额已用完")

var ErrTeamMemberWeeklyExceeded = apperror.TooManyRequests("TEAM_MEMBER_WEEKLY_LIMIT_EXCEEDED", "团队成员周限额已用完")

var ErrTeamMembershipRequired = apperror.Forbidden("TEAM_MEMBERSHIP_REQUIRED", "需要先加入团队")

var ErrUserNotFound = apperror.NotFound("USER_NOT_FOUND", "user not found")

var ErrInsufficientBalance = apperror.BadRequest("INSUFFICIENT_BALANCE", "insufficient balance")

var ErrPreferredSubscriptionGroup = apperror.Forbidden("PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED", "preferred subscription does not allow this group")
