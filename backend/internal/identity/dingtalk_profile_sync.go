// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	strings "strings"
)

// DingTalkProfileSnapshot 只包含同步资料所需字段，不携带提供方客户端。
type DingTalkProfileSnapshot struct {
	UserID, Name, Nickname, Email string
	DeptIDs                       []int64
}

// DingTalkSyncOptions 是每次登录取得的动态设置投影。
type DingTalkSyncOptions struct {
	CorpRestrictionPolicy                                         string
	SyncCorpEmail, SyncDisplayName, SyncDept                      bool
	SyncCorpEmailAttrKey, SyncDisplayNameAttrKey, SyncDeptAttrKey string
}
type DingTalkDepartmentReader interface {
	ResolveDepartmentPath(context.Context, int64) (string, error)
}
type DingTalkDepartmentFunc func(context.Context, int64) (string, error)

func (f DingTalkDepartmentFunc) ResolveDepartmentPath(ctx context.Context, id int64) (string, error) {
	return f(ctx, id)
}

// ProfileSyncObserver 把既有诊断交给装配适配，核心不安装日志后端。
type ProfileSyncObserver func(level, message string, args ...any)
type DingTalkProfileSync struct {
	Users      *UserService
	Attributes *UserAttributeService
	Observe    ProfileSyncObserver
}

func (s *DingTalkProfileSync) log(level, message string, args ...any) {
	if s.Observe != nil {
		s.Observe(level, message, args...)
	}
}

// syncDingTalkIdentity 在 internal_only 模式下，按三个 sync 开关把钉钉身份信息
// 同步到用户属性表（以及 users.username）。
// 任何错误仅记日志，不中断登录流程（最终一致性）。
func (s *DingTalkProfileSync) Sync(ctx context.Context, cfg DingTalkSyncOptions, client DingTalkDepartmentReader, userID int64, staff *DingTalkProfileSnapshot, syncUsername bool) {
	s.log("info", "dingtalk sync: entry",
		"user_id", userID,
		"policy", cfg.CorpRestrictionPolicy,
		"sync_corp_email", cfg.SyncCorpEmail,
		"sync_display_name", cfg.SyncDisplayName,
		"sync_dept", cfg.SyncDept,
		"sync_username", syncUsername,
		"attr_key_email", cfg.SyncCorpEmailAttrKey,
		"attr_key_name", cfg.SyncDisplayNameAttrKey,
		"attr_key_dept", cfg.SyncDeptAttrKey,
		"staff_nil", staff == nil,
	)
	if cfg.CorpRestrictionPolicy != "internal_only" || staff == nil {
		s.log("info", "dingtalk sync: skip, not internal_only or staff nil")
		return
	}
	s.log("info", "dingtalk sync: staff snapshot",
		"name", staff.Name, "email", staff.Email, "dept_ids", staff.DeptIDs,
	)
	if !cfg.SyncCorpEmail && !cfg.SyncDisplayName && !cfg.SyncDept {
		s.log("info", "dingtalk sync: skip, all flags disabled")
		return
	}
	if s.Attributes == nil {
		s.log("warn", "dingtalk sync: userAttributeService not available, skipping")
		return
	}

	// 仅首次注册时覆盖 users.username（避免每次登录覆盖用户后续手动改过的名字）。
	// dingtalk_name 属性下面单独每次写入企业 name，不受此条件影响。
	if syncUsername && cfg.SyncDisplayName {
		username := strings.TrimSpace(staff.Nickname)
		source := "nickname"
		if username == "" {
			username = strings.TrimSpace(staff.Name)
			source = "name(fallback)"
		}
		if username != "" && s.Users != nil {
			if _, err := s.Users.UpdateProfile(ctx, userID, UpdateProfileRequest{Username: &username}); err != nil {
				s.log("warn", "dingtalk sync: failed to update username", "user_id", userID, "err", err)
			} else {
				s.log("info", "dingtalk sync: username updated (register)", "user_id", userID, "username", username, "source", source)
			}
		}
	}

	// 属性同步（目标 attr key 从 cfg 读取，默认值由 GetDingTalkConnectOAuthConfig 保证非空）
	type syncField struct {
		key   string
		value string
	}
	var fields []syncField

	if cfg.SyncDisplayName && strings.TrimSpace(staff.Name) != "" {
		fields = append(fields, syncField{cfg.SyncDisplayNameAttrKey, strings.TrimSpace(staff.Name)})
	}
	if cfg.SyncCorpEmail && strings.TrimSpace(staff.Email) != "" {
		fields = append(fields, syncField{cfg.SyncCorpEmailAttrKey, strings.TrimSpace(staff.Email)})
	}
	if cfg.SyncDept && len(staff.DeptIDs) > 0 {
		// 跳过根部门 ID=1，找第一个真实子部门；都是根则保留 1（最终写入空字符串覆盖旧值）。
		primaryDeptID := int64(0)
		for _, id := range staff.DeptIDs {
			if id > 1 {
				primaryDeptID = id
				break
			}
		}
		if primaryDeptID == 0 {
			primaryDeptID = staff.DeptIDs[0]
		}
		s.log("info", "dingtalk sync: pick primary dept", "user_id", userID, "all_dept_ids", staff.DeptIDs, "primary", primaryDeptID)
		path, err := client.ResolveDepartmentPath(ctx, primaryDeptID)
		if err != nil {
			s.log("warn", "dingtalk sync: failed to resolve dept path", "user_id", userID, "dept_id", primaryDeptID, "err", err)
		} else {
			// path="" 表示公司直属（仅在根部门下），仍写入空串覆盖旧值。
			fields = append(fields, syncField{cfg.SyncDeptAttrKey, path})
		}
	}

	if len(fields) == 0 {
		return
	}

	// 逐 key 查 definition 并 upsert
	for _, f := range fields {
		if err := s.setUserAttributeByKey(ctx, userID, f.key, f.value); err != nil {
			s.log("warn", "dingtalk sync: failed to set attribute", "user_id", userID, "key", f.key, "err", err)
		}
	}
}

// setUserAttributeByKey 按 attribute key 查找 definition，再 upsert 用户属性值。
// definition 不存在时记 warn 日志跳过（admin 在 settings 保存时已按需 upsert
// 对应 def；缺失意味着 admin 改了 attr key 但未保存 settings，或 def 被手工删除）。
func (s *DingTalkProfileSync) setUserAttributeByKey(ctx context.Context, userID int64, key, value string) error {
	def, err := s.Attributes.GetDefinitionByKey(ctx, key)
	if err != nil {
		s.log("warn", "dingtalk sync: attribute definition not found, skipping", "key", key, "err", err.Error())
		return nil
	}
	if err := s.Attributes.UpdateUserAttributes(ctx, userID, []UpdateUserAttributeInput{
		{AttributeID: def.ID, Value: value},
	}); err != nil {
		return err
	}
	s.log("info", "dingtalk sync: attribute upserted", "user_id", userID, "key", key, "attr_id", def.ID)
	return nil
}

// dingTalkStaffFromClaims 从 upstreamClaims 重建最小 DingTalkProfileSnapshot。
func DingTalkProfileFromClaims(claims map[string]any) *DingTalkProfileSnapshot {
	if claims == nil {
		return &DingTalkProfileSnapshot{}
	}
	staff := &DingTalkProfileSnapshot{}
	if v, ok := claims["username"].(string); ok {
		staff.Name = v
	}
	if v, ok := claims["nickname"].(string); ok {
		staff.Nickname = v
	}
	if v, ok := claims["email"].(string); ok {
		staff.Email = v
	}
	if v, ok := claims["corp_user_id"].(string); ok {
		staff.UserID = v
	}
	// primary_dept_id 存为 int64 或 float64（JSON round-trip）
	switch v := claims["primary_dept_id"].(type) {
	case int64:
		if v > 0 {
			staff.DeptIDs = []int64{v}
		}
	case float64:
		if id := int64(v); id > 0 {
			staff.DeptIDs = []int64{id}
		}
	}
	return staff
}
