package scheduler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// RPM 拒绝错误保持既有 reason、状态与消息。
var (
	ErrGroupRPMExceeded = apperror.TooManyRequests("GROUP_RPM_EXCEEDED", "group requests-per-minute limit exceeded")
	ErrUserRPMExceeded  = apperror.TooManyRequests("USER_RPM_EXCEEDED", "user requests-per-minute limit exceeded")
)

// RPMUser 只投影准入字段，nil override 保留按需回源。
type RPMUser struct {
	ID                   int64
	RPMLimit             int
	UserGroupRPMOverride *int
}
type RPMGroup struct {
	ID       int64
	RPMLimit int
}
type RPMOverrides interface {
	GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error)
}

// RPMAdmission 不重复持有资金缓存，调用者仍在资金检查通过后调用。
type RPMAdmission struct {
	cache       UserRPMCache
	overrides   RPMOverrides
	diagnostics Diagnostics
}

func NewRPMAdmission(cache UserRPMCache, overrides RPMOverrides, diagnostics Diagnostics) *RPMAdmission {
	return &RPMAdmission{cache: cache, overrides: overrides, diagnostics: diagnostics}
}

// checkRPM 执行并行 RPM 限流，所有适用的限制同时生效，任一超限即拒绝：
//
//  1. (用户, 分组) rpm_override       — 最细粒度：管理员为特定用户在特定分组设定的专属限额。
//     override=0 表示该用户在该分组免检（绿灯），但 user 级全局上限仍然生效。
//  2. group.rpm_limit                 — 分组级：该分组的统一 RPM 容量（仅当无 override 时生效）。
//  3. user.rpm_limit                  — 用户级全局硬上限：无论 override/group 如何配置，始终生效。
//
// 与旧版"级联互斥"设计不同，新版确保 user.rpm_limit 作为全局天花板不会被 group 或 override 覆盖。
// Redis 故障一律 fail-open（打 warning，不阻塞业务）。
func (s *RPMAdmission) Check(ctx context.Context, user *RPMUser, group *RPMGroup) error {
	if s == nil || s.cache == nil || user == nil {
		return nil
	}

	// ── 第一层：分组级检查（override 或 group.rpm_limit） ──
	if group != nil {
		// 解析 override：优先从 auth cache snapshot，nil 时回退 DB。
		var override *int
		if user.UserGroupRPMOverride != nil {
			override = user.UserGroupRPMOverride
		} else if s.overrides != nil {
			dbOverride, err := s.overrides.GetRPMOverrideByUserAndGroup(ctx, user.ID, group.ID)
			if err != nil {
				s.diagnostics.printf(
					"service.billing_cache",
					"Warning: rpm override lookup failed for user=%d group=%d: %v",
					user.ID, group.ID, err,
				)
			} else {
				override = dbOverride
			}
		}

		if override != nil {
			// override=0 → 该用户在该分组免检（但 user 级仍会在下面检查）。
			if *override > 0 {
				count, incErr := s.cache.IncrementUserGroupRPM(ctx, user.ID, group.ID)
				if incErr != nil {
					s.diagnostics.printf(
						"service.billing_cache",
						"Warning: rpm increment (override) failed for user=%d group=%d: %v",
						user.ID, group.ID, incErr,
					)
					// fail-open
				} else if count > *override {
					return ErrGroupRPMExceeded
				}
			}
			// override 命中后跳过 group.rpm_limit（override 替代 group），但不 return——继续检查 user 级。
		} else if group.RPMLimit > 0 {
			// 无 override，检查 group.rpm_limit。
			count, err := s.cache.IncrementUserGroupRPM(ctx, user.ID, group.ID)
			if err != nil {
				s.diagnostics.printf(
					"service.billing_cache",
					"Warning: rpm increment (group) failed for user=%d group=%d: %v",
					user.ID, group.ID, err,
				)
				// fail-open
			} else if count > group.RPMLimit {
				return ErrGroupRPMExceeded
			}
		}
	}

	// ── 第二层：用户级全局硬上限（始终生效） ──
	if user.RPMLimit > 0 {
		count, err := s.cache.IncrementUserRPM(ctx, user.ID)
		if err != nil {
			s.diagnostics.printf(
				"service.billing_cache",
				"Warning: rpm increment (user) failed for user=%d: %v",
				user.ID, err,
			)
			return nil // fail-open
		}
		if count > user.RPMLimit {
			return ErrUserRPMExceeded
		}
	}

	return nil
}
